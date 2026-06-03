package api

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/api/middleware"
)

func NewRouter(
	cfg *config.Config,
	redisClient *redis.Client,
	authHandler *handler.AuthHandler,
	userHandler *handler.UserHandler,
	circleHandler *handler.CircleHandler,
	contributionHandler *handler.ContributionHandler,
	payoutHandler *handler.PayoutHandler,
	inviteHandler *handler.InviteHandler,
	notificationHandler *handler.NotificationHandler,
	adminHandler *handler.AdminHandler,
	webhookHandler *handler.WebhookHandler,
	healthHandler *handler.HealthHandler,
	verificationHandler *handler.VerificationHandler,
	passkeyCredentialHandler *handler.PasskeyCredentialHandler,
	jwtPublicKey []byte,
) *gin.Engine {
	r := gin.New()

	r.Use(middleware.RecoveryMiddleware())
	r.Use(middleware.LoggingMiddleware())
	r.Use(middleware.CORSMiddleware(cfg.CORS))
	r.Use(middleware.RateLimitMiddleware(redisClient, cfg.RateLimit))

	r.GET("/health", healthHandler.Health)
	r.GET("/health/ready", healthHandler.Ready)

	// Passkey credential storage — public, called from Next.js API routes
	passkey := r.Group("/v1/passkey")
	{
		passkey.POST("/credentials", passkeyCredentialHandler.StoreCredential)
		passkey.GET("/credentials/:id", passkeyCredentialHandler.GetCredential)
	}

	api := r.Group("/v1")
	{
		auth := api.Group("/auth")
		auth.Use(middleware.AuthRateLimitMiddleware(redisClient, cfg.RateLimit))
		{
			auth.POST("/nonce", authHandler.Nonce)
			auth.POST("/verify", authHandler.Verify)
			auth.POST("/register", authHandler.Register)
			auth.POST("/refresh", authHandler.Refresh)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/verification/send", verificationHandler.SendCode)
			auth.POST("/verification/verify", verificationHandler.VerifyCode)
			auth.POST("/verification/resend", verificationHandler.ResendCode)
		}

		authenticated := api.Group("")
		authenticated.Use(middleware.AuthMiddleware(jwtPublicKey))
		authenticated.Use(middleware.TokenBlocklistMiddleware(redisClient))
		{
			authenticated.POST("/auth/me", authHandler.Me)

			authenticated.GET("/users/me", userHandler.GetMe)
			authenticated.PATCH("/users/me", userHandler.UpdateMe)
			authenticated.GET("/users/me/reputation", userHandler.GetReputation)
			authenticated.POST("/users/me/kyc", userHandler.InitiateKYC)
			authenticated.GET("/users/me/kyc/status", userHandler.GetKYCStatus)
			authenticated.GET("/users/me/circles", userHandler.GetMyCircles)

			authenticated.POST("/circles", circleHandler.CreateCircle)
			authenticated.GET("/circles/:id", circleHandler.GetCircle)
			authenticated.PATCH("/circles/:id", circleHandler.UpdateCircle)
			authenticated.DELETE("/circles/:id", circleHandler.CancelCircle)
			authenticated.POST("/circles/:id/join", circleHandler.JoinCircle)
			authenticated.POST("/circles/:id/contribute", circleHandler.Contribute)
			authenticated.POST("/circles/:id/exit", circleHandler.ExitCircle)
			authenticated.GET("/circles/:id/members", circleHandler.GetMembers)
			authenticated.GET("/circles/:id/rounds", circleHandler.GetRounds)
			authenticated.GET("/circles/:id/payouts", circleHandler.GetPayouts)
			authenticated.POST("/circles/:id/dispute", circleHandler.Dispute)
			authenticated.POST("/circles/:id/vote", circleHandler.Vote)
			authenticated.POST("/circles/:id/auction-bid", circleHandler.AuctionBid)

			authenticated.GET("/circles/:id/invites", inviteHandler.ListInvites)
			authenticated.POST("/circles/:id/invites", inviteHandler.CreateInvite)
			authenticated.DELETE("/invites/:code", inviteHandler.RevokeInvite)

			authenticated.GET("/contributions", contributionHandler.ListContributions)
			authenticated.GET("/contributions/:id", contributionHandler.GetContribution)

			authenticated.GET("/payouts", payoutHandler.ListPayouts)
			authenticated.GET("/payouts/:id", payoutHandler.GetPayout)

			authenticated.GET("/notifications", notificationHandler.ListNotifications)
			authenticated.PATCH("/notifications/:id/read", notificationHandler.MarkRead)
			authenticated.PATCH("/notifications/read-all", notificationHandler.MarkAllRead)
			authenticated.PUT("/notifications/preferences", notificationHandler.UpdatePreferences)

			authenticated.POST("/webhooks", webhookHandler.RegisterWebhook)
			authenticated.GET("/webhooks", webhookHandler.ListWebhooks)
			authenticated.DELETE("/webhooks/:id", webhookHandler.DeleteWebhook)

			admin := authenticated.Group("/admin")
			admin.Use(middleware.AdminMiddleware())
			{
				admin.GET("/users", adminHandler.ListUsers)
				admin.GET("/circles", adminHandler.ListCircles)
				admin.GET("/audit-log", adminHandler.GetAuditLog)
				admin.GET("/metrics", adminHandler.GetMetrics)
				admin.POST("/feature-flags", adminHandler.UpdateFeatureFlag)
			}
		}

		optional := api.Group("")
		optional.Use(middleware.OptionalAuthMiddleware(jwtPublicKey))
		{
			optional.GET("/circles", circleHandler.ListCircles)
			optional.GET("/users/:id", userHandler.GetByID)
		}
	}

	return r
}
