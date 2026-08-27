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

	"github.com/google/uuid"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/admin"
	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/community"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/email"
	"github.com/moistello/backend/internal/domain/governance"
	"github.com/moistello/backend/internal/domain/incentives"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/internal/domain/push"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/internal/domain/savings"
	"github.com/moistello/backend/internal/domain/sms"
	"github.com/moistello/backend/internal/domain/swap"
	"github.com/moistello/backend/internal/domain/token"
	"github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/verification"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/domain/yellowcard"
	ws "github.com/moistello/backend/internal/websocket"
	"github.com/moistello/backend/pkg/jobqueue"
	"github.com/moistello/backend/pkg/logger"
	"github.com/moistello/backend/pkg/postgres"
	"github.com/moistello/backend/pkg/rabbitmq"
	"github.com/moistello/backend/pkg/redis"
	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/tracing"
	"github.com/moistello/backend/pkg/validator"
	"github.com/moistello/backend/webhook"
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

// userLookupAdapter resolves a notification.Recipient from user.Repository —
// #191's delivery channels need a user's contact details and preferences,
// without the notification package importing the full user domain.
type userLookupAdapter struct {
	repo user.Repository
}

func (a *userLookupAdapter) FindRecipient(ctx context.Context, userID string) (notification.Recipient, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return notification.Recipient{}, err
	}
	u, err := a.repo.FindByID(ctx, id)
	if err != nil {
		return notification.Recipient{}, err
	}
	return notification.Recipient{
		Email:             u.Email,
		Phone:             u.Phone,
		PushToken:         u.PushToken,
		PreferredChannels: []string(u.NotificationChannels),
		Muted:             u.NotificationsMuted,
	}, nil
}

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}

	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	validator.Init()

	// Initialize OpenTelemetry tracing
	if err := tracing.Init(cfg.Tracing); err != nil {
		log.Fatal().Err(err).Msg("failed to initialize tracing")
	}

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
	notificationDeliveryRepo := notification.NewDeliveryAuditRepository(db)
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
	authSvc, err := auth.NewService(redisClient, cfg.Auth.NonceTTL, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL, cfg.Auth.JWTPrivateKeyPEM, cfg.Auth.JWTPublicKeyPEM)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize auth service")
	}

	totpSvc := totp.NewService()
	verificationSvc := verification.NewService(redisClient)
	emailSvc := email.NewService(email.Config{
		APIKey:      cfg.Brevo.APIKey,
		FromAddress: cfg.Brevo.FromEmail,
		FromName:    cfg.Brevo.FromName,
	})

	// Notification delivery channels (#191): email reuses the same Brevo
	// client already used for OTP/backup-code/recovery emails; SMS/push are
	// new clients reading the notification.sms.*/notification.push.* config
	// that already existed (see config.NotificationConfig) but had nothing
	// wired to it.
	smsSvc := sms.NewService(sms.Config{
		AccountSID: cfg.Notification.SMS.AccountSID,
		AuthToken:  cfg.Notification.SMS.AuthToken,
		FromNumber: cfg.Notification.SMS.FromNumber,
	})
	pushSvc := push.NewService(push.Config{
		ServerKey: cfg.Notification.Push.FCMServerKey,
	})
	notificationSvc := notification.NewService(notificationRepo, nil, wsBroadcaster,
		notification.WithDeliveryChannels(
			&userLookupAdapter{repo: userRepo},
			notificationDeliveryRepo,
			&notification.EmailChannel{Sender: emailSvc},
			&notification.SMSChannel{Sender: smsSvc},
			&notification.PushChannel{Sender: pushSvc},
		),
	)

	inviteSvc := invite.NewService(inviteRepo)
	_ = auditRepo

	// Wallet service (needed before auth handler for wallet creation)
	walletCfg := wallet.Config{
		MasterSecretKey:   cfg.Stellar.MasterSecretKey,
		MasterPublicKey:   cfg.Stellar.MasterPublicKey,
		HorizonURL:        cfg.Stellar.HorizonURL,
		USDCIssuer:        cfg.Stellar.USDCIssuer,
		NetworkPassphrase: cfg.Stellar.NetworkPassphrase,
		MinBalanceXLM:     cfg.Stellar.WalletMinBalance,
		// Deterministic seed derivation for email-based wallets (#166).
		WalletPepper:  cfg.Security.WalletPepper,
		Argon2Time:    cfg.Security.Argon2Time,
		Argon2Memory:  cfg.Security.Argon2Memory,
		Argon2Threads: cfg.Security.Argon2Threads,
	}
	walletSvc, err := wallet.NewService(wallet.NewRepository(db), walletCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize wallet service")
	}

	tokenSvc, err := token.NewService(wallet.NewRepository(db), token.Config{
		GovernanceTokenContractID: cfg.Stellar.GovernanceTokenContractID,
		SorobanRPCURL:             cfg.Stellar.SorobanRPCURL,
		NetworkPassphrase:         cfg.Stellar.NetworkPassphrase,
		HorizonURL:                cfg.Stellar.HorizonURL,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize token service")
	}
	tokenH := handler.NewTokenHandler(tokenSvc)

	jwtPublicKey := []byte(cfg.Auth.JWTPublicKeyPEM)

	wsH := handler.NewWebSocketHandler(wsHub, cfg.CORS.AllowedOrigins)

	authH := handler.NewAuthHandler(authSvc, userSvc, walletSvc, totpSvc, verificationSvc, emailSvc, redisClient, userRepo)
	userH := handler.NewUserHandler(userSvc, redisClient)
	circleH := handler.NewCircleHandler(circleSvc, inviteSvc, contribSvc, payoutSvc)
	contribH := handler.NewContributionHandler(contribSvc, contribRepo)
	payoutH := handler.NewPayoutHandler(payoutSvc, payoutRepo)
	inviteH := handler.NewInviteHandler(inviteSvc)
	notifH := handler.NewNotificationHandler(notificationSvc, userSvc)
	adminSvc := admin.NewService(nil, 0)
	adminH := handler.NewAdminHandler(userSvc, userRepo, circleSvc, auditRepo, adminSvc)
	webhookRepo := webhook.NewPostgresRepository(db.DB)
	webhookH := handler.NewWebhookHandler(webhookRepo)
	healthH := handler.NewHealthHandler(db.DB, redisClient, cfg.Stellar.SorobanRPCURL, cfg.Stellar.HorizonURL)
	passkeyCredH := handler.NewPasskeyCredentialHandler(db)
	walletH := handler.NewWalletHandler(walletSvc)

	// Community service
	communitySvc := community.NewService(communityRepo, wsBroadcaster)
	communityH := handler.NewCommunityHandler(communitySvc)

	// Yellow Card integration
	ycClient := yellowcard.NewClient(cfg.YellowCard.APIKey, cfg.YellowCard.APISecret, cfg.Stellar.MasterPublicKey)
	depositH := handler.NewDepositHandler(ycClient, walletSvc)

	// Savings goals
	savingsRepo := savings.NewRepository(db)
	savingsSvc := savings.NewService(savingsRepo)
	savingsH := handler.NewSavingsGoalHandler(savingsSvc)

	// Initialize Soroban client for escrow swap contract
	sorobanClient := soroban.NewClient(cfg.Stellar.SorobanRPCURL)
	signer, err := stellar.NewSigner(cfg.Stellar.MasterSecretKey)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create stellar signer")
	}
	horizonClient := stellar.NewClient(cfg.Stellar.HorizonURL, cfg.Stellar.SorobanRPCURL, cfg.Stellar.NetworkPassphrase)
	accountMgr := stellar.NewAccountManager(horizonClient, cfg.Stellar.MasterPublicKey)

	// Create escrow swap contract invoker and client
	escrowSwapInvoker := soroban.NewContractInvoker(sorobanClient, signer, accountMgr, cfg.Stellar.EscrowSwapContractID)
	escrowSwapClient := soroban.NewEscrowSwapClient(escrowSwapInvoker)

	// Swap service and handler
	swapRepo := swap.NewPostgresRepository(db)
	swapSvc := swap.NewService(swapRepo, circleSvc, userSvc, escrowSwapClient)
	swapH := handler.NewSwapHandler(swapSvc)

	governanceRepo := governance.NewRepository(db)
	governanceSvc := governance.NewService(governanceRepo)
	governanceH := handler.NewGovernanceHandler(governanceSvc)

	incentivesRepo := incentives.NewRepository(db)
	incentivesSvc := incentives.NewService(incentivesRepo)
	reputationH := handler.NewReputationHandler(reputationSvc)
	referralH := handler.NewReferralHandler(incentivesSvc)

	// GDPR cookie consent handler
	consentH := handler.NewConsentHandler(db.DB)

	// RabbitMQ connection for health checks and event publishing
	rmqClient, rmqErr := rabbitmq.New(cfg.RabbitMQ)
	if rmqErr != nil {
		log.Warn().Err(rmqErr).Msg("RabbitMQ unavailable — health checks will report degraded")
	} else {
		defer rmqClient.Close()
	}

	// Wire RabbitMQ into health handler for /health and /health/ready probes
	if rmqClient != nil {
		healthH.WithRabbitMQ(rmqClient)
	}

	// Job queue for background tasks
	jobQueue := jobqueue.NewJobQueue(db)
	adminJobQueueH := handler.NewAdminJobQueueHandler(jobQueue)

	router := api.NewRouter(cfg, redisClient, authH, userH, circleH, contribH, payoutH, inviteH, notifH, adminH, webhookH, healthH, passkeyCredH, walletH, depositH, communityH, wsH, savingsH, tokenH, swapH, governanceH, reputationH, referralH, consentH, adminJobQueueH, webhookRepo, jwtPublicKey)

	if err := api.RunServer(router, cfg.Server); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}
