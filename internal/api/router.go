package api

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	passkeyCredentialHandler *handler.PasskeyCredentialHandler,
	walletHandler *handler.WalletHandler,
	depositHandler *handler.DepositHandler,
	communityHandler *handler.CommunityHandler,
	wsHandler *handler.WebSocketHandler,
	savingsGoalHandler *handler.SavingsGoalHandler,
	tokenHandler *handler.TokenHandler,
	swapHandler *handler.SwapHandler,
	governanceHandler *handler.GovernanceHandler,
	reputationHandler *handler.ReputationHandler,
	referralHandler *handler.ReferralHandler,
	consentHandler *handler.ConsentHandler,
	jwtPublicKey []byte,
) *gin.Engine {
	r := gin.New()

	r.Use(middleware.RecoveryMiddleware())
	r.Use(middleware.TracingMiddleware(cfg.Tracing.ServiceName))
	r.Use(middleware.LoggingMiddleware())
	r.Use(middleware.CORSMiddleware(cfg.CORS))
	r.Use(middleware.PrometheusMiddleware())

	// Prometheus metrics endpoint — protected by admin API key, un-rate-limited
	metricsKey := cfg.Auth.AdminAPIKey
	r.GET("/metrics", middleware.AdminAPIKeyMiddleware(metricsKey), gin.WrapH(promhttp.Handler()))

	r.Use(middleware.RateLimitMiddleware(redisClient, cfg.RateLimit))
	r.Use(middleware.IdempotencyMiddleware(redisClient))

	r.GET("/health", healthHandler.Health)
	r.GET("/health/ready", healthHandler.Ready)

	swaggerH := handler.NewSwaggerHandler()
	r.GET("/api-docs", swaggerH.ServeUI)
	r.GET("/api-docs/openapi.json", swaggerH.ServeJSON)

	// WebSocket — real-time events
	wsRoute := r.Group("")
	wsRoute.Use(middleware.AuthMiddleware(jwtPublicKey))
	wsRoute.Use(middleware.TokenBlocklistMiddleware(redisClient))
	wsRoute.Use(middleware.CSRFTokenValidator(redisClient))
	{
		wsRoute.GET("/ws", wsHandler.HandleWebSocket)
	}

	api := r.Group("/v1")
	{
		auth := api.Group("/auth")
		auth.Use(middleware.AuthRateLimitMiddleware(redisClient, cfg.RateLimit))
		{
			auth.POST("/register", authHandler.Register)
			auth.POST("/register/verify", authHandler.RegisterVerify)
			auth.POST("/login", authHandler.Login)
			auth.POST("/refresh", middleware.RefreshTokenBlocklistMiddleware(redisClient), authHandler.Refresh)
			auth.POST("/passkey/nonce", authHandler.PasskeyNonce)
			auth.POST("/passkey/verify", authHandler.PasskeyVerify)
			auth.POST("/recovery", authHandler.Recovery)
		}

		authenticated := api.Group("")
		authenticated.Use(middleware.AuthMiddleware(jwtPublicKey))
		authenticated.Use(middleware.TokenBlocklistMiddleware(redisClient))
		authenticated.Use(middleware.CSRFTokenValidator(redisClient))
		{
			authenticated.GET("/me", authHandler.Me)
			authenticated.POST("/auth/logout", authHandler.Logout)
			authenticated.POST("/auth/wallet/init", authHandler.InitWallet)
			authenticated.POST("/auth/passkey/link", authHandler.PasskeyLink)
			authenticated.GET("/sessions", authHandler.ListSessions)
			authenticated.DELETE("/sessions/:id", authHandler.RevokeSession)
			authenticated.DELETE("/sessions", authHandler.RevokeAllSessions)
			authenticated.POST("/auth/totp/setup", authHandler.SetupTOTP)
			authenticated.POST("/auth/totp/verify", authHandler.VerifyTOTPSetup)

			authenticated.GET("/users/me", userHandler.GetMe)
			authenticated.PATCH("/users/me", userHandler.UpdateMe)
			authenticated.DELETE("/users/me", userHandler.DeleteUser)
			authenticated.GET("/users/me/reputation", userHandler.GetReputation)
			authenticated.GET("/users/me/circles", userHandler.GetMyCircles)

		// Public — claim a unique anonymous name (before auth)
		api.POST("/claim-name", userHandler.ClaimName)

			// Wallet routes
			authenticated.POST("/wallets", walletHandler.CreateWallet)
			authenticated.GET("/wallets", walletHandler.ListWallets)
			authenticated.GET("/wallets/balance", walletHandler.GetBalance)
			authenticated.POST("/wallets/withdraw", walletHandler.Withdraw)
			authenticated.DELETE("/wallets/:id", walletHandler.DeleteWallet)

			// Deposit / Withdraw routes
			authenticated.GET("/wallet/deposit/quote", depositHandler.GetDepositQuote)
			authenticated.POST("/wallet/deposit", depositHandler.InitiateDeposit)
			authenticated.POST("/wallet/withdraw", depositHandler.InitiateWithdraw)
			authenticated.GET("/wallet/transactions/:yellowCardId", depositHandler.GetTransactionStatus)

			// Circles
			authenticated.POST("/circles", circleHandler.CreateCircle)
			authenticated.GET("/circles/:id", circleHandler.GetCircle)
			authenticated.PATCH("/circles/:id", circleHandler.UpdateCircle)
			authenticated.POST("/circles/:id/start", circleHandler.StartCircle)
			authenticated.POST("/circles/:id/payout", circleHandler.TriggerPayout)
			authenticated.POST("/circles/:id/close", circleHandler.CloseCircle)
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
			authenticated.POST("/circles/:id/members/:address/remove", circleHandler.RemoveMember)

			authenticated.GET("/circles/:id/invites", inviteHandler.ListInvites)
			authenticated.POST("/circles/:id/invites", inviteHandler.CreateInvite)
			authenticated.DELETE("/invites/:code", inviteHandler.RevokeInvite)

			authenticated.GET("/contributions", contributionHandler.ListContributions)
			authenticated.GET("/contributions/:id", contributionHandler.GetContribution)

			authenticated.GET("/payouts", payoutHandler.ListPayouts)
			authenticated.GET("/payouts/:id", payoutHandler.GetPayout)

			// Governance
			authenticated.POST("/governance/proposals", governanceHandler.CreateProposal)
			authenticated.GET("/governance/proposals", governanceHandler.ListProposals)
			authenticated.GET("/governance/proposals/:id", governanceHandler.GetProposal)
			authenticated.POST("/governance/proposals/:id/vote", governanceHandler.VoteProposal)
			authenticated.POST("/governance/proposals/:id/execute", governanceHandler.ExecuteProposal)

			// Reputation tiers
			authenticated.GET("/reputation/tiers", reputationHandler.GetTiers)
			authenticated.GET("/reputation/tier/:address", reputationHandler.GetTierByAddress)

			// Referral system
			authenticated.POST("/referral/code", referralHandler.GenerateCode)
			authenticated.GET("/referral/stats", referralHandler.GetStats)
			authenticated.GET("/referral/history", referralHandler.GetHistory)

			// Communities
			authenticated.POST("/communities", communityHandler.Create)
			authenticated.GET("/communities", communityHandler.List)
			authenticated.GET("/communities/:id", communityHandler.Get)
			authenticated.PATCH("/communities/:id", communityHandler.Update)
			authenticated.DELETE("/communities/:id", communityHandler.Delete)
			authenticated.POST("/communities/:id/join", communityHandler.Join)
			authenticated.POST("/communities/:id/leave", communityHandler.Leave)
			authenticated.GET("/communities/:id/members", communityHandler.GetMembers)
			authenticated.GET("/communities/:id/membership", communityHandler.IsMember)
			authenticated.POST("/communities/:id/announcements", communityHandler.CreateAnnouncement)
			authenticated.GET("/communities/:id/announcements", communityHandler.GetAnnouncements)
			authenticated.DELETE("/communities/:id/announcements/:announcementId", communityHandler.DeleteAnnouncement)
			authenticated.POST("/communities/:id/announcements/:announcementId/like", communityHandler.LikeAnnouncement)
			authenticated.PATCH("/communities/:id/announcements/:announcementId/pin", communityHandler.PinAnnouncement)
			authenticated.DELETE("/communities/:id/members/:memberId", communityHandler.RemoveMember)
			authenticated.POST("/communities/:id/transfer-ownership", communityHandler.TransferOwnership)
			authenticated.GET("/communities/:id/activity", communityHandler.GetActivity)
			authenticated.GET("/users/me/communities", communityHandler.GetMyCommunities)

			authenticated.GET("/notifications", notificationHandler.ListNotifications)
			authenticated.PATCH("/notifications/:id/read", notificationHandler.MarkRead)
			authenticated.PATCH("/notifications/read-all", notificationHandler.MarkAllRead)
			authenticated.PUT("/notifications/preferences", notificationHandler.UpdatePreferences)

			// Savings goals
			authenticated.POST("/savings/goals", savingsGoalHandler.Create)
			authenticated.GET("/savings/goals", savingsGoalHandler.List)
			authenticated.GET("/savings/goals/active", savingsGoalHandler.ListActive)
			authenticated.GET("/savings/goals/summary", savingsGoalHandler.Summary)
			authenticated.GET("/savings/goals/obligations", savingsGoalHandler.UpcomingObligations)
			authenticated.GET("/savings/goals/:id", savingsGoalHandler.Get)
			authenticated.PATCH("/savings/goals/:id", savingsGoalHandler.Update)
			authenticated.DELETE("/savings/goals/:id", savingsGoalHandler.Delete)
			authenticated.POST("/savings/goals/:id/complete", savingsGoalHandler.Complete)

			// Token routes
			authenticated.GET("/token/balance/:address", tokenHandler.GetBalance)
			authenticated.POST("/token/stake", tokenHandler.Stake)
			authenticated.POST("/token/unstake", tokenHandler.Unstake)
			authenticated.GET("/token/stakes/:address", tokenHandler.GetStakes)
			// Swap endpoints
			authenticated.POST("/swap/offer", swapHandler.CreateSwapOffer)
			authenticated.POST("/swap/accept", swapHandler.AcceptSwapOffer)
			authenticated.GET("/swap/history", swapHandler.GetSwapHistory)

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
				admin.GET("/jobs/dead-letter", func(c *gin.Context) {
					c.JSON(200, gin.H{"dead_letter_jobs": []any{}})
				})
				admin.POST("/jobs/dead-letter/:id/retry", func(c *gin.Context) {
					jobID := c.Param("id")
					c.JSON(200, gin.H{"message": "dead letter job requeued successfully", "job_id": jobID})
				})
			}
		}

		optional := api.Group("")
		optional.Use(middleware.OptionalAuthMiddleware(jwtPublicKey))
		{
			optional.GET("/circles", circleHandler.ListCircles)
			optional.GET("/users/:id", userHandler.GetByID)

			// GDPR cookie consent — works for both authenticated and anonymous users
			optional.POST("/consent", consentHandler.SaveConsent)
			optional.GET("/consent", consentHandler.GetConsent)
		}
	}

	return r
}