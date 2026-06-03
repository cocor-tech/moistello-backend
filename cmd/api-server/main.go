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
	"time"

	"github.com/rs/zerolog/log"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/infrastructure/email"
	"github.com/moistello/backend/internal/infrastructure/ratelimit"
	"github.com/moistello/backend/pkg/logger"
	"github.com/moistello/backend/pkg/postgres"
	"github.com/moistello/backend/pkg/redis"
	"github.com/moistello/backend/pkg/validator"
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

	userSvc := user.NewService(userRepo)
	circleSvc := circle.NewService(circleRepo, circle.NewTransactor(db))
	contribSvc := contribution.NewService(contribRepo, contribution.NewTransactor(db))
	payoutSvc := payout.NewService(payoutRepo)
	reputationSvc := reputation.NewService(reputationRepo)
	notificationSvc := notification.NewService(notificationRepo, nil)
	authSvc, err := auth.NewService(redisClient, cfg.Auth.NonceTTL, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL, cfg.Auth.JWTPrivateKeyPath, cfg.Auth.JWTPublicKeyPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize auth service")
	}

	verifStore := auth.NewVerificationRepository(db)
	brevoSender := email.NewBrevoSender(cfg.Brevo.APIKey, cfg.Brevo.FromEmail, cfg.Brevo.FromName)
	redisLimiter := ratelimit.NewRedisRateLimiter(redisClient)
	verifSvc := auth.NewVerificationService(verifStore, brevoSender, redisLimiter, auth.VerificationConfig{
		CodeLength:       6,
		CodeExpiry:       10 * time.Minute,
		MaxAttempts:      5,
		MaxSendsPerEmail: 3,
		ResendCooldown:   60 * time.Second,
	})
	inviteSvc := invite.NewService(inviteRepo)
	_ = reputationSvc
	_ = contribSvc
	_ = payoutSvc
	_ = auditRepo

	jwtSecret, err := os.ReadFile(cfg.Auth.JWTPrivateKeyPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load JWT secret")
	}

	authH := handler.NewAuthHandler(authSvc, userSvc, redisClient, verifSvc)
	userH := handler.NewUserHandler(userSvc)
	circleH := handler.NewCircleHandler(circleSvc, inviteSvc)
	contribH := handler.NewContributionHandler(contribSvc, contribRepo)
	payoutH := handler.NewPayoutHandler(payoutSvc, payoutRepo)
	inviteH := handler.NewInviteHandler(inviteSvc)
	notifH := handler.NewNotificationHandler(notificationSvc)
	adminH := handler.NewAdminHandler(userSvc, userRepo, circleSvc, auditRepo)
	webhookH := handler.NewWebhookHandler()
	healthH := handler.NewHealthHandler(db.DB, redisClient)
	verifH := handler.NewVerificationHandler(verifSvc, userSvc)

	router := api.NewRouter(cfg, redisClient, authH, userH, circleH, contribH, payoutH, inviteH, notifH, adminH, webhookH, healthH, verifH, jwtSecret)

	if err := api.RunServer(router, cfg.Server); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}
