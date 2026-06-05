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
	"os"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/community"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/domain/yellowcard"
	"github.com/moistello/backend/pkg/logger"
	"github.com/moistello/backend/pkg/postgres"
	"github.com/moistello/backend/pkg/redis"
	"github.com/moistello/backend/pkg/validator"
	"github.com/rs/zerolog/log"
)

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

	userSvc := user.NewService(userRepo, circleRepo)
	circleSvc := circle.NewService(circleRepo, circle.NewTransactor(db))
	contribSvc := contribution.NewService(contribRepo, contribution.NewTransactor(db))
	payoutSvc := payout.NewService(payoutRepo)
	reputationSvc := reputation.NewService(reputationRepo)
	notificationSvc := notification.NewService(notificationRepo, nil)
	authSvc, err := auth.NewService(redisClient, cfg.Auth.NonceTTL, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL, cfg.Auth.JWTPrivateKeyPath, cfg.Auth.JWTPublicKeyPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize auth service")
	}

	inviteSvc := invite.NewService(inviteRepo)
	_ = reputationSvc
	_ = auditRepo

	jwtPublicKey, err := os.ReadFile(cfg.Auth.JWTPublicKeyPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load JWT public key")
	}

	authH := handler.NewAuthHandler(authSvc, userSvc, redisClient)
	userH := handler.NewUserHandler(userSvc)
	circleH := handler.NewCircleHandler(circleSvc, inviteSvc, contribSvc, payoutSvc)
	contribH := handler.NewContributionHandler(contribSvc, contribRepo)
	payoutH := handler.NewPayoutHandler(payoutSvc, payoutRepo)
	inviteH := handler.NewInviteHandler(inviteSvc)
	notifH := handler.NewNotificationHandler(notificationSvc)
	adminH := handler.NewAdminHandler(userSvc, userRepo, circleSvc, auditRepo)
	webhookH := handler.NewWebhookHandler()
	healthH := handler.NewHealthHandler(db.DB, redisClient)
	passkeyCredH := handler.NewPasskeyCredentialHandler(db)

	// Wallet service
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
	walletH := handler.NewWalletHandler(walletSvc)

	// Community service
	communityRepo := community.NewRepository(db)
	communitySvc := community.NewService(communityRepo)
	communityH := handler.NewCommunityHandler(communitySvc)

	// Yellow Card integration
	ycClient := yellowcard.NewClient(cfg.YellowCard.APIKey, cfg.YellowCard.APISecret)
	depositH := handler.NewDepositHandler(ycClient, walletSvc)

	router := api.NewRouter(cfg, redisClient, authH, userH, circleH, contribH, payoutH, inviteH, notifH, adminH, webhookH, healthH, passkeyCredH, walletH, depositH, communityH, jwtPublicKey)

	if err := api.RunServer(router, cfg.Server); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}
