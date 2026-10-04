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
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/admin"
	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/auth/session"
	"github.com/moistello/backend/internal/domain/chat"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/community"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/deposit"
	"github.com/moistello/backend/internal/domain/email"
	"github.com/moistello/backend/internal/domain/featureflag"
	"github.com/moistello/backend/internal/domain/governance"
	"github.com/moistello/backend/internal/domain/incentives"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/internal/domain/mobilemoney"
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
	"github.com/moistello/backend/internal/domain/withdrawal"
	"github.com/moistello/backend/internal/domain/yellowcard"
	"github.com/moistello/backend/internal/indexer"
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

// payoutWalletAdapter satisfies payout.WalletLookup by resolving a user's
// on-chain Stellar wallet address from the user repository.
type payoutWalletAdapter struct {
	repo user.Repository
}

func (a *payoutWalletAdapter) WalletAddressForUser(ctx context.Context, userID uuid.UUID) (string, error) {
	u, err := a.repo.FindByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return u.WalletAddress, nil
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

// digestPreferenceAdapter resolves a notification.DigestPreferences from
// user.Repository — #415's digest cadence, without the notification package
// importing the user domain. Same adapter pattern as userLookupAdapter above.
type digestPreferenceAdapter struct {
	repo user.Repository
}

func (a *digestPreferenceAdapter) DigestPreferences(ctx context.Context, userID string) (notification.DigestPreferences, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return notification.DigestPreferences{}, err
	}
	u, err := a.repo.FindByID(ctx, id)
	if err != nil {
		return notification.DigestPreferences{}, err
	}
	return notification.DigestPreferences{
		Enabled:  u.DigestEnabled,
		Interval: time.Duration(u.DigestIntervalMinutes) * time.Minute,
	}, nil
}

// governanceWeightResolver computes a voter's governance weight for the
// creation-time snapshot (#418): their governance-token balance scaled by
// their reputation tier factor.
//
// It lives here rather than in the governance package so governance does not
// import the token, reputation and user domains — the same adapter pattern used
// by userLookupAdapter and digestPreferenceAdapter above.
type governanceWeightResolver struct {
	users      user.Repository
	reputation reputation.Repository
	tokens     token.Service
}

func (r *governanceWeightResolver) VotingWeight(ctx context.Context, userID string) (int64, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return 0, err
	}
	u, err := r.users.FindByID(ctx, uid)
	if err != nil {
		return 0, err
	}

	// A missing reputation snapshot is not an error: the user simply counts at
	// the default tier factor rather than being disenfranchised.
	tier := ""
	if snapshot, err := r.reputation.GetByUser(ctx, uid); err == nil && snapshot != nil {
		tier = snapshot.Level
	}

	if r.tokens == nil || u.WalletAddress == "" {
		// Without a token service (or address) there is no balance to weight,
		// so fall back to the reputation-scaled baseline of 1.
		return governance.ApplyTierFactor(1, tier), nil
	}

	balance, err := r.tokens.GetBalance(ctx, u.WalletAddress)
	if err != nil {
		return 0, err
	}
	return governance.ApplyTierFactor(int64(balance), tier), nil
}

// EligibleVoters returns every active user's id, so the snapshot covers anyone
// who might later vote. It pages through the user list rather than loading
// everyone at once, since a snapshot covering the whole electorate is the
// point and a single unbounded query is not.
func (r *governanceWeightResolver) EligibleVoters(ctx context.Context) ([]string, error) {
	const pageSize = 200
	const maxPages = 50 // cap at 10k voters per proposal

	ids := make([]string, 0, pageSize)
	for page := 1; page <= maxPages; page++ {
		users, err := r.users.List(ctx, user.UserFilter{Page: page, Limit: pageSize})
		if err != nil {
			return nil, err
		}
		for i := range users {
			ids = append(ids, users[i].ID.String())
		}
		if len(users) < pageSize {
			return ids, nil
		}
	}
	return ids, nil
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "config-validate" || os.Args[1] == "--config-validate" || (os.Args[1] == "config" && len(os.Args) > 2 && os.Args[2] == "validate")) {
		cfg, err := config.Load("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Configuration load failed:\n%v\n", err)
			os.Exit(1)
		}
		if err := cfg.ValidateOffline(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Configuration offline validation failed:\n%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Configuration is valid (environment=%s, offline preflight passed)\n", cfg.Environment)
		os.Exit(0)
	}

	cfg, err := config.Load("")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}

	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	validator.Init()

	// Apply whitelisted, non-critical config changes (log level, rate limits)
	// without a restart; an invalid edit is rejected and the old values stay.
	if err := cfg.Hot.Watch(
		func(h config.HotConfig) {
			logger.SetLevel(h.LogLevel)
			log.Info().Str("log_level", h.LogLevel).Msg("config reloaded")
		},
		func(err error) {
			log.Warn().Err(err).Msg("config reload rejected; keeping previous values")
		},
	); err != nil {
		log.Warn().Err(err).Msg("config hot-reload disabled")
	}

	// Initialize OpenTelemetry tracing
	if err := tracing.Init(cfg.Tracing); err != nil {
		log.Fatal().Err(err).Msg("failed to initialize tracing")
	}

	log.Info().Msg("starting Moistello API server")

	db, err := postgres.New(cfg.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}

	// Optional read replica for analytics queries; nil falls back to the primary.
	replicaDB := postgres.NewReplica(cfg.Database)

	redisClient, err := redis.New(cfg.Redis)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to redis")
	}

	// Detect connection leaks and pool exhaustion; alerts are logged and counted
	// in moistello_db_pool_alerts_total.
	poolMonitorCtx, stopPoolMonitor := context.WithCancel(context.Background())
	poolMonitor := postgres.NewPoolMonitor(db, postgres.PoolMonitorOptions{}, func(a postgres.PoolAlert) {
		log.Warn().Str("kind", a.Kind).Str("detail", a.Detail).Msg("db pool alert")
	})
	go poolMonitor.Run(poolMonitorCtx)

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
	wsBridge := ws.NewRedisBridge(wsHub, redisClient)

	userSvc := user.NewService(userRepo, circleRepo)
	circleSvc := circle.NewService(circleRepo, &moiAdapter{repo: userRepo}, circle.Dependencies{
		CommunityChecker: &communityAdapter{repo: communityRepo},
		Broadcaster:      wsBroadcaster,
		Transactor:       circle.NewTransactor(db),
	})
	// Stellar client used for on-chain verification
	horizonClient := stellar.NewClient(cfg.Stellar.HorizonURL, cfg.Stellar.SorobanRPCURL, cfg.Stellar.NetworkPassphrase)

	contribSvc := contribution.NewService(contribRepo, wsBroadcaster, contribution.NewTransactor(db), horizonClient, cfg.Stellar.MasterPublicKey, circleSvc)
	payoutSvc := payout.NewService(payoutRepo, horizonClient, &payoutWalletAdapter{repo: userRepo}, circleSvc)
	reputationSvc := reputation.NewService(reputationRepo)
	authSvc, err := auth.NewServiceWithKeyConfig(redisClient, cfg.Auth.NonceTTL, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL, auth.KeyConfig{
		CurrentPrivateKeyPEM:  cfg.Auth.JWTPrivateKeyPEM,
		CurrentPublicKeyPEM:   cfg.Auth.JWTPublicKeyPEM,
		CurrentKID:            cfg.Auth.JWTCurrentKID,
		PreviousPrivateKeyPEM: cfg.Auth.JWTPreviousPrivateKeyPEM,
		PreviousPublicKeyPEM:  cfg.Auth.JWTPreviousPublicKeyPEM,
		PreviousKID:           cfg.Auth.JWTPreviousKID,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize auth service")
	}

	// Expired-session cleanup (#374). This job is the only thing that reclaims
	// session state by age — request paths delete sessions by explicit token
	// hash on logout or revocation and never sweep. Every replica schedules the
	// job with jitter and contends for a Redis lock, so a tick performs exactly
	// one sweep across the fleet.
	sessionCleaner := session.NewCleaner(redisClient, session.NewSessionStore(db), cfg.Auth.CleanupInterval, cfg.Auth.CleanupJitter)
	sessionCleaner.Start(context.Background())

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
		// Digest batching (#415): non-urgent circle events collapse into a
		// periodic per-user summary; urgent classes still go out immediately.
		notification.WithDigestBatching(
			notification.NewDigestBuffer(),
			&digestPreferenceAdapter{repo: userRepo},
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
	go wsHub.StartMembershipAuditor(context.Background(), 30*time.Second)

	authH := handler.NewAuthHandler(authSvc, userSvc, walletSvc, totpSvc, verificationSvc, emailSvc, redisClient, userRepo)
	userH := handler.NewUserHandler(userSvc)
	circleH := handler.NewCircleHandler(circleSvc, inviteSvc, contribSvc, payoutSvc)
	contribH := handler.NewContributionHandler(contribSvc, contribRepo)
	payoutH := handler.NewPayoutHandler(payoutSvc, payoutRepo)
	inviteH := handler.NewInviteHandler(inviteSvc)
	notifH := handler.NewNotificationHandler(notificationSvc, userSvc)
	adminSvc, err := admin.NewService(admin.NewRepositoryWithReader(postgres.NewReader(db, replicaDB)), 0)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize admin metrics service")
	}
	featureFlagRepo := featureflag.NewRepository(db)
	featureFlagSvc := featureflag.NewService(featureFlagRepo)
	featureFlagCache := featureflag.NewCache(featureFlagSvc, featureflag.DefaultReloadInterval)
	if err := featureFlagCache.Start(context.Background()); err != nil {
		log.Warn().Err(err).Msg("failed to load feature flags on startup — cache starts empty until the next reload")
	}
	adminH := handler.NewAdminHandler(userSvc, userRepo, circleSvc, auditRepo, adminSvc, featureFlagSvc, featureFlagCache)
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
	depositRepo := deposit.NewRepository(db)
	withdrawalRepo := withdrawal.NewRepository(db)
	depositH := handler.NewDepositHandler(ycClient, walletSvc).
		WithRedis(redisClient).
		WithConfig(cfg.YellowCard).
		WithRepositories(depositRepo, withdrawalRepo).
		WithFeatureFlags(featureFlagCache)
	ycWebhookH := handler.NewYellowCardWebhookHandler(depositRepo, withdrawalRepo, cfg.YellowCard.WebhookSecret)

	// Mobile-money bridge (#190) — on/off-ramp for non-NGN markets. Each
	// provider is only registered when its credentials are configured, so
	// deployments that haven't onboarded a given provider yet just don't
	// advertise support for its currency rather than failing to start.
	mmRegistry := mobilemoney.NewRegistry()
	if cfg.MobileMoney.MPesaConsumerKey != "" {
		mmRegistry.Register(mobilemoney.NewMPesaProvider(mobilemoney.MPesaConfig{
			ConsumerKey:        cfg.MobileMoney.MPesaConsumerKey,
			ConsumerSecret:     cfg.MobileMoney.MPesaConsumerSecret,
			Shortcode:          cfg.MobileMoney.MPesaShortcode,
			Passkey:            cfg.MobileMoney.MPesaPasskey,
			SecurityCredential: cfg.MobileMoney.MPesaSecurityCredential,
			InitiatorName:      cfg.MobileMoney.MPesaInitiatorName,
			CallbackBaseURL:    cfg.MobileMoney.CallbackBaseURL,
			Sandbox:            cfg.MobileMoney.MPesaSandbox,
		}))
	}
	if cfg.MobileMoney.MTNSubscriptionKey != "" {
		mmRegistry.Register(mobilemoney.NewMTNProvider(mobilemoney.MTNConfig{
			SubscriptionKey: cfg.MobileMoney.MTNSubscriptionKey,
			APIUser:         cfg.MobileMoney.MTNAPIUser,
			APIKey:          cfg.MobileMoney.MTNAPIKey,
			TargetCurrency:  cfg.MobileMoney.MTNTargetCurrency,
			CallbackBaseURL: cfg.MobileMoney.CallbackBaseURL,
			Sandbox:         cfg.MobileMoney.MTNSandbox,
		}))
	}
	if cfg.MobileMoney.AirtelClientID != "" {
		mmRegistry.Register(mobilemoney.NewAirtelProvider(mobilemoney.AirtelConfig{
			ClientID:        cfg.MobileMoney.AirtelClientID,
			ClientSecret:    cfg.MobileMoney.AirtelClientSecret,
			Country:         cfg.MobileMoney.AirtelCountry,
			TargetCurrency:  cfg.MobileMoney.AirtelTargetCurrency,
			CallbackBaseURL: cfg.MobileMoney.CallbackBaseURL,
			Sandbox:         cfg.MobileMoney.AirtelSandbox,
		}))
	}
	mmRepo := mobilemoney.NewRepository(db)
	mmSvc := mobilemoney.NewService(mmRepo, mmRegistry)
	mobileMoneyH := handler.NewMobileMoneyHandler(mmSvc, walletSvc)

	// Startup provider-presence probe and logging (#412)
	activeProviders := mmRegistry.ActiveProviderNames()
	if len(activeProviders) > 0 {
		log.Info().
			Strs("active_providers", activeProviders).
			Strs("supported_currencies", mmRegistry.SupportedCurrencies()).
			Msg("mobile money providers initialized")
	} else {
		isDev := cfg.Environment == "development" || cfg.Environment == "dev" || cfg.Environment == "test"
		if !isDev {
			log.Error().Msg("zero mobile money providers configured in non-development environment")
		} else {
			log.Warn().Msg("no mobile money providers configured")
		}
	}

	// E2EE chat (#188): X3DH key bundles + encrypted message store on top
	// of the crypto primitives in internal/domain/chat/x3dh.go.
	chatKeyRepo := chat.NewKeyRepository(db)
	chatMsgRepo := chat.NewRepository(db)
	chatSvc := chat.NewService(chatKeyRepo, chatMsgRepo, wsBroadcaster)
	chatH := handler.NewChatHandler(chatSvc)

	reconcileInterval := time.Duration(cfg.MobileMoney.ReconcileIntervalMin) * time.Minute
	if reconcileInterval <= 0 {
		reconcileInterval = 5 * time.Minute
	}
	// Reconciler with Redis single-flight lock across replicas (#413)
	mmReconciler := mobilemoney.NewReconciler(redisClient, mmSvc, reconcileInterval, cfg.Auth.CleanupJitter)
	mmReconciler.Start(context.Background())

	// Digest flusher (#415): drains each user's batched circle events into a
	// single summary once their cadence elapses. The tick is a heartbeat only
	// — the per-user cadence in the buffer decides what is actually due.
	digestFlusher := notification.NewDigestFlusher(notificationSvc, notification.DefaultDigestFlushInterval)
	digestFlusher.Start(context.Background())

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
	accountMgr := stellar.NewAccountManager(horizonClient, cfg.Stellar.MasterPublicKey)

	// Create escrow swap contract invoker and client
	escrowSwapInvoker := soroban.NewContractInvoker(sorobanClient, signer, accountMgr, cfg.Stellar.EscrowSwapContractID)
	escrowSwapClient := soroban.NewEscrowSwapClient(escrowSwapInvoker)

	// Swap service and handler
	swapRepo := swap.NewPostgresRepository(db)
	swapSvc := swap.NewService(swapRepo, circleSvc, userSvc, escrowSwapClient)
	swapH := handler.NewSwapHandler(swapSvc)

	// Swap sweep worker with Redis single-flight lock across replicas (#416).
	// The lock avoids duplicated work; the per-offer atomic claim in
	// SweepExpiredOffers is what guarantees escrow is never released twice.
	swapSweeper := swap.NewSweeper(swapSvc, redisClient, cfg.Swap.SweepInterval)
	swapSweeper.Start(context.Background())

	governanceRepo := governance.NewRepository(db)
	// Both #418 and #414 configure the same service: weight is snapshotted at
	// proposal creation so tokens moved mid-vote cannot change an outcome, and a
	// passed proposal waits out an execution timelock during which it can still
	// be cancelled by threshold vote. A zero timelock delay restores the
	// pre-#414 immediate execution.
	governanceSvc := governance.NewService(governanceRepo,
		governance.WithWeightResolver(
			&governanceWeightResolver{users: userRepo, reputation: reputationRepo, tokens: tokenSvc},
		),
		governance.WithTimelock(governance.TimelockConfig{
			Delay:              cfg.Governance.ExecutionTimelock,
			CancelThresholdPct: cfg.Governance.CancelThresholdPct,
		}),
	)
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
	}

	// Wire RabbitMQ and MobileMoney into health handler for /health and /health/ready probes
	if rmqClient != nil {
		healthH.WithRabbitMQ(rmqClient)
	}
	healthH.WithMobileMoney(mmRegistry)

	// Job queue for background tasks
	jobQueue := jobqueue.NewJobQueue(db)
	adminJobQueueH := handler.NewAdminJobQueueHandler(jobQueue)
	adminIndexerH := handler.NewAdminIndexerHandler(indexer.NewDeadLetterStore(db))

	router := api.NewRouter(cfg, redisClient, authH, userH, circleH, contribH, payoutH, inviteH, notifH, adminH, webhookH, healthH, passkeyCredH, walletH, depositH, mobileMoneyH, chatH, communityH, wsH, savingsH, tokenH, swapH, governanceH, reputationH, referralH, consentH, adminJobQueueH, adminIndexerH, webhookRepo, ycWebhookH, jwtPublicKey)

	// Shutdown order: fail readiness first so the load balancer stops sending
	// traffic, drain in-flight HTTP requests (bounded by
	// server.shutdown_timeout), close long-lived WebSocket connections and
	// background loops, and only then close the pools they were using.
	hooks := api.ShutdownHooks{
		PreDrain: []func(){healthH.BeginShutdown},
		Drain: []func(context.Context){
			func(ctx context.Context) {
				if err := wsHub.Shutdown(ctx); err != nil {
					log.Warn().Err(err).Msg("websocket drain incomplete")
				}
			},
			func(context.Context) { wsBridge.Close() },
			func(context.Context) { stopPoolMonitor() },
			func(context.Context) {
				featureFlagCache.Stop()
				mmReconciler.Stop()
			},
			func(context.Context) { digestFlusher.Stop() },
			func(context.Context) { swapSweeper.Stop() },
			func(context.Context) { sessionCleaner.Stop() },
		},
		CloseLast: []func(){
			func() {
				if rmqClient != nil {
					rmqClient.Close()
				}
			},
			func() {
				if err := redisClient.Close(); err != nil {
					log.Warn().Err(err).Msg("closing redis")
				}
			},
			func() {
				if replicaDB != nil {
					if err := replicaDB.Close(); err != nil {
						log.Warn().Err(err).Msg("closing postgres replica")
					}
				}
				if err := db.Close(); err != nil {
					log.Warn().Err(err).Msg("closing postgres")
				}
			},
		},
	}

	if err := api.RunServerWithHooks(router, cfg.Server, hooks); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}
