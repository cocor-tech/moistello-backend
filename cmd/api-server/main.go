// @title Moistello API
// @version 1.0.0
// @description Decentralized savings circles on Stellar. REST API for circles, contributions, payouts, reputation, and governance.
// @termsOfService https://moistello.com/terms
// @contact.name Moistello Support
// @contact.email support@moistello.com
// @contact.url https://moistello.com/support
// @license.name MIT
// @license.url https://opensource.org/licenses/MIT
// @host moistello.com
// @BasePath /v1
// @schemes https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description JWT token obtained from /auth/verify or /auth/register. Format: "Bearer <token>"
package main

import (
	"context"
	"os"

	"github.com/google/uuid"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/community"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/email"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/internal/domain/savings"
	"github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/verification"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/domain/yellowcard"
	ws "github.com/moistello/backend/internal/websocket"
	"github.com/moistello/backend/pkg/logger"
	"github.com/moistello/backend/pkg/postgres"
	"github.com/moistello/backend/pkg/redis"
	"github.com/moistello/backend/pkg/validator"
	"github.com/rs/zerolog/log"
)

type moiAdapter struct {
	repo user.Repository
}

func (a *moiAdapter) FindByID(ctx context.Context, id uuid.UUID) (*circle.UserMOIData, error) {
	u, err := a.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &circle.UserMOIData{MoiScore: u.MoiScore}, nil
}

type communityAdapter struct {
	repo community.Repository
}

func (a *communityAdapter) IsMember(ctx context.Context, communityID, userID uuid.UUID) (bool, error) {
	return a.repo.IsMember(ctx, communityID, userID)
}

func main() {
	cfg, err := config.Load(".")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	validator.Init()
	log.Info().Msg("starting Moistello API server")

	db, err := postgres.New(cfg.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer db.Close()

	redisClient, err := redis.New(cfg.Redis)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to redis")
	}
	defer redisClient.Close()

	userRepo := user.NewRepository(db)
	circleRepo := circle.NewRepository(db)
	contribRepo := contribution.NewRepository(db)
	payoutRepo := payout.NewRepository(db)
	reputationRepo := reputation.NewRepository(db)
	notificationRepo := notification.NewRepository(db)
	inviteRepo := invite.NewRepository(db)
	auditRepo := audit.NewRepository(db)

	communityRepo := community.NewRepository(db)

	wsHub := ws.NewHub()
	wsBroadcaster := ws.NewBroadcaster(wsHub, redisClient)
	_ = ws.NewRedisBridge(wsHub, redisClient)

	userSvc := user.NewService(userRepo, circleRepo)
	circleSvc := circle.NewService(circleRepo, &moiAdapter{repo: userRepo}, &communityAdapter{repo: communityRepo}, wsBroadcaster, circle.NewTransactor(db))
	contribSvc := contribution.NewService(contribRepo, wsBroadcaster, contribution.NewTransactor(db))
	payoutSvc := payout.NewService(payoutRepo)
	reputationSvc := reputation.NewService(reputationRepo)
	notificationSvc := notification.NewService(notificationRepo, nil, wsBroadcaster)
	authSvc, err := auth.NewService(redisClient, cfg.Auth.NonceTTL, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL, cfg.Auth.JWTPrivateKeyPath, cfg.Auth.JWTPublicKeyPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize auth service")
	}

	totpSvc := totp.NewService()
	verificationSvc := verification.NewService(redisClient)
	emailSvc := email.NewService(email.ConfigFromEnv())

	inviteSvc := invite.NewService(inviteRepo)
	_ = reputationSvc
	_ = auditRepo

	// Wallet service (needed before auth handler for wallet creation)
	walletCfg := wallet.Config{
		MasterSecretKey:   cfg.Stellar.MasterSecretKey,
		MasterPublicKey:   cfg.Stellar.MasterPublicKey,
		HorizonURL:        cfg.Stellar.HorizonURL,
		USDCIssuer:        cfg.Stellar.USDCIssuer,
		NetworkPassphrase: cfg.Stellar.NetworkPassphrase,
		MinBalanceXLM:     cfg.Stellar.WalletMinBalance,
	}
	walletSvc, err := wallet.NewService(wallet.NewRepository(db), walletCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize wallet service")
	}

	jwtPublicKey, err := os.ReadFile(cfg.Auth.JWTPublicKeyPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load JWT public key")
	}

	wsH := handler.NewWebSocketHandler(wsHub, cfg.CORS.AllowedOrigins)

	authH := handler.NewAuthHandler(authSvc, userSvc, walletSvc, totpSvc, verificationSvc, emailSvc, redisClient, userRepo)
	userH := handler.NewUserHandler(userSvc, redisClient)
	circleH := handler.NewCircleHandler(circleSvc, inviteSvc, contribSvc, payoutSvc)
	contribH := handler.NewContributionHandler(contribSvc, contribRepo)
	payoutH := handler.NewPayoutHandler(payoutSvc, payoutRepo)
	inviteH := handler.NewInviteHandler(inviteSvc)
	notifH := handler.NewNotificationHandler(notificationSvc, userSvc)
	adminH := handler.NewAdminHandler(userSvc, userRepo, circleSvc, auditRepo)
	webhookH := handler.NewWebhookHandler()
	healthH := handler.NewHealthHandler(db.DB, redisClient, cfg.Stellar.SorobanRPCURL, cfg.Stellar.HorizonURL)
	passkeyCredH := handler.NewPasskeyCredentialHandler(db)
	walletH := handler.NewWalletHandler(walletSvc)

	// Community service
	communitySvc := community.NewService(communityRepo, wsBroadcaster)
	communityH := handler.NewCommunityHandler(communitySvc)

	// Yellow Card integration
	ycClient := yellowcard.NewClient(cfg.YellowCard.APIKey, cfg.YellowCard.APISecret)
	depositH := handler.NewDepositHandler(ycClient, walletSvc)

	// Savings goals
	savingsRepo := savings.NewRepository(db)
	savingsSvc := savings.NewService(savingsRepo)
	savingsH := handler.NewSavingsGoalHandler(savingsSvc)

	router := api.NewRouter(cfg, redisClient, authH, userH, circleH, contribH, payoutH, inviteH, notifH, adminH, webhookH, healthH, passkeyCredH, walletH, depositH, communityH, wsH, savingsH, jwtPublicKey)

	if err := api.RunServer(router, cfg.Server); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}
