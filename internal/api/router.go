package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/redis/go-redis/v9"
)

func SetupRouter(
	redisClient *redis.Client,
	rateLimitCfg config.RateLimitConfig,
	authHandler *handler.AuthHandler,
	userHandler *handler.UserHandler,
	pubKey []byte,
	"github.com/moistello/backend/webhook"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// perResource is a small helper so route registration below doesn't have to
// repeat the redisClient/resource/limit/window/fail-closed boilerplate for
// every sensitive route (#197 — PerResourceRateLimitMiddleware existed but
// was never applied to any route).
func perResource(redisClient *redis.Client, resource string, limit, windowSeconds int) gin.HandlerFunc {
	return middleware.PerResourceRateLimitMiddleware(
		redisClient,
		resource,
		limit,
		time.Duration(windowSeconds)*time.Second,
		middleware.WithFailClosed(),
	)
}

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
	mobileMoneyHandler *handler.MobileMoneyHandler,
	chatHandler *handler.ChatHandler,
	communityHandler *handler.CommunityHandler,
	wsHandler *handler.WebSocketHandler,
	savingsGoalHandler *handler.SavingsGoalHandler,
	tokenHandler *handler.TokenHandler,
	swapHandler *handler.SwapHandler,
	governanceHandler *handler.GovernanceHandler,
	reputationHandler *handler.ReputationHandler,
	referralHandler *handler.ReferralHandler,
	consentHandler *handler.ConsentHandler,
	adminJobQueueHandler *handler.AdminJobQueueHandler,
	webhookRepo webhook.WebhookRepository,
	yellowCardWebhookHandler *handler.YellowCardWebhookHandler,
	jwtPublicKey []byte,
) *gin.Engine {
	r := gin.New()

	// Global rate limiter: fail-open for safe GET routes, fail-closed for others
	// Individual routes can override with middleware.WithFailClosed() or WithFailOpen()
	r.Use(middleware.RateLimitMiddleware(redisClient, rateLimitCfg))

	v1 := r.Group("/v1")
	{
		// Auth routes - use AuthRateLimitMiddleware (always fail-closed for auth/OTP)
		authGroup := v1.Group("/auth", middleware.AuthRateLimitMiddleware(redisClient, rateLimitCfg))
	r.Use(middleware.RecoveryMiddleware())
	r.Use(middleware.TracingMiddleware(cfg.Tracing.ServiceName))
	r.Use(middleware.LoggingMiddleware())
	r.Use(middleware.CORSMiddleware(cfg.CORS))
	r.Use(middleware.PrometheusMiddleware())

	// Prometheus metrics endpoint — protected by admin API key, un-rate-limited
	metricsKey := cfg.Auth.AdminAPIKey
	r.GET("/metrics", middleware.AdminAPIKeyMiddleware(metricsKey), gin.WrapH(promhttp.Handler()))

	r.Use(middleware.RateLimitMiddleware(redisClient, cfg.RateLimit))

	r.GET("/health", healthHandler.Health)
	r.GET("/health/ready", healthHandler.Readiness)
	r.GET("/health/live", healthHandler.Liveness)

	swaggerH := handler.NewSwaggerHandler()
	r.GET("/api-docs", swaggerH.ServeUI)
	r.GET("/api-docs/openapi.json", swaggerH.ServeJSON)

	// Public webhooks (idempotency-keyed internally)
	r.POST("/webhooks/incoming/:id", handler.NewIncomingWebhookHandler(webhookRepo).ReceiveWebhook)
	r.POST("/webhooks/yellowcard", yellowCardWebhookHandler.HandleWebhook)

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
			auth.POST("/register", perResource(redisClient, "otp", cfg.RateLimit.OTPLimit, cfg.RateLimit.OTPWindowSeconds), authHandler.Register)
			auth.POST("/register/verify", perResource(redisClient, "otp", cfg.RateLimit.OTPLimit, cfg.RateLimit.OTPWindowSeconds), authHandler.RegisterVerify)
			auth.POST("/refresh", middleware.RefreshTokenBlocklistMiddleware(redisClient), authHandler.Refresh)
			auth.POST("/nonce", authHandler.Nonce)
			auth.POST("/verify", authHandler.Verify)
		}

		// User & Profile routes (authenticated)
		usersGroup := v1.Group("/users", middleware.AuthMiddleware(pubKey))
		authenticated := api.Group("")
		authenticated.Use(middleware.AuthMiddleware(jwtPublicKey))
		authenticated.Use(middleware.TokenBlocklistMiddleware(redisClient))
		authenticated.Use(middleware.CSRFTokenValidator(redisClient))
		// Idempotency must run after AuthMiddleware so keys are scoped per
		// user (#198) — a global, pre-auth middleware let idempotency keys
		// collide across different users' requests.
		authenticated.Use(middleware.IdempotencyMiddleware(redisClient))
		{
			authenticated.GET("/me", authHandler.Me)
			authenticated.POST("/auth/logout", authHandler.Logout)
			authenticated.DELETE("/sessions/:id", authHandler.RevokeSessionByID)

			authenticated.POST("/users/username/claim", userHandler.ClaimName)

			// Public — claim a unique anonymous name (before auth)
			api.POST("/claim-name", userHandler.ClaimName)

			// Passkey credential store/retrieval
			authenticated.POST("/credential", passkeyCredentialHandler.StoreCredential)
			authenticated.GET("/credential", passkeyCredentialHandler.GetCredential)

			// Wallet routes
			authenticated.POST("/wallets", walletHandler.CreateWallet)
			authenticated.GET("/wallets", walletHandler.ListWallets)
			authenticated.GET("/wallets/balance", walletHandler.GetBalance)
			authenticated.POST("/wallets/withdraw", perResource(redisClient, "wallet-transfer", cfg.RateLimit.WalletTransferLimit, cfg.RateLimit.WalletTransferWindowSeconds), walletHandler.Withdraw)
			authenticated.DELETE("/wallets/:id", walletHandler.DeleteWallet)

			// Deposit / Withdraw routes
			authenticated.GET("/wallet/deposit/quote", depositHandler.GetDepositQuote)
			authenticated.POST("/wallet/deposit", perResource(redisClient, "wallet-transfer", cfg.RateLimit.WalletTransferLimit, cfg.RateLimit.WalletTransferWindowSeconds), depositHandler.InitiateDeposit)
			authenticated.POST("/wallet/withdraw", perResource(redisClient, "wallet-transfer", cfg.RateLimit.WalletTransferLimit, cfg.RateLimit.WalletTransferWindowSeconds), depositHandler.InitiateWithdraw)
			authenticated.GET("/wallet/transactions/:yellowCardId", depositHandler.GetTransactionStatus)
			authenticated.POST("/wallet/mobile-money/onramp", perResource(redisClient, "wallet-transfer", cfg.RateLimit.WalletTransferLimit, cfg.RateLimit.WalletTransferWindowSeconds), mobileMoneyHandler.InitiateOnramp)
			authenticated.POST("/wallet/mobile-money/offramp", perResource(redisClient, "wallet-transfer", cfg.RateLimit.WalletTransferLimit, cfg.RateLimit.WalletTransferWindowSeconds), mobileMoneyHandler.InitiateOfframp)
			authenticated.GET("/wallet/mobile-money/:id", mobileMoneyHandler.GetTransaction)

			authenticated.POST("/chat/keys", chatHandler.PublishKeys)
			authenticated.GET("/chat/keys/:userId", chatHandler.GetBundle)
			authenticated.POST("/chat/conversations", chatHandler.CreateConversation)
			authenticated.GET("/chat/conversations", chatHandler.ListConversations)
			authenticated.POST("/chat/conversations/:id/messages", chatHandler.SendMessage)
			authenticated.GET("/chat/conversations/:id/messages", chatHandler.ListMessages)

			// Circles
			authenticated.POST("/circles", circleHandler.CreateCircle)
			authenticated.GET("/circles/:id", circleHandler.GetCircle)
			authenticated.PATCH("/circles/:id", circleHandler.UpdateCircle)
			authenticated.POST("/circles/:id/start", circleHandler.StartCircle)
			authenticated.POST("/circles/:id/payout", circleHandler.TriggerPayout)
			authenticated.POST("/circles/:id/close", circleHandler.CloseCircle)
			authenticated.DELETE("/circles/:id", circleHandler.CancelCircle)
			authenticated.POST("/circles/:id/join", circleHandler.JoinCircle)
			authenticated.POST("/circles/:id/contribute", perResource(redisClient, "contribute", cfg.RateLimit.ContributeLimit, cfg.RateLimit.ContributeWindowSeconds), circleHandler.Contribute)
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
			authenticated.POST("/referral/code", perResource(redisClient, "referral", cfg.RateLimit.ReferralLimit, cfg.RateLimit.ReferralWindowSeconds), referralHandler.GenerateCode)
			authenticated.GET("/referral/stats", referralHandler.GetStats)
			authenticated.GET("/referral/history", referralHandler.GetHistory)

			// Communities
			authenticated.POST("/communities", communityHandler.Create)
			authenticated.GET("/communities", communityHandler.List)
			authenticated.GET("/communities/:id", communityHandler.Get)
			authenticated.GET("/communities/slug/:slug", communityHandler.GetBySlug)
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
			authenticated.POST("/swap/offer", perResource(redisClient, "swap", cfg.RateLimit.SwapLimit, cfg.RateLimit.SwapWindowSeconds), swapHandler.CreateSwapOffer)
			authenticated.POST("/swap/accept", perResource(redisClient, "swap", cfg.RateLimit.SwapLimit, cfg.RateLimit.SwapWindowSeconds), swapHandler.AcceptSwapOffer)
			authenticated.POST("/swap/cancel", perResource(redisClient, "swap", cfg.RateLimit.SwapLimit, cfg.RateLimit.SwapWindowSeconds), swapHandler.CancelSwapOffer)
			authenticated.GET("/swap/history", swapHandler.GetSwapHistory)

			authenticated.POST("/webhooks", webhookHandler.RegisterWebhook)
			authenticated.GET("/webhooks", webhookHandler.ListWebhooks)
			authenticated.GET("/webhooks/deliveries", webhookHandler.ListDeliveries)
			authenticated.DELETE("/webhooks/:id", webhookHandler.DeleteWebhook)
		}

		admin := authenticated.Group("/admin")
		admin.Use(middleware.AdminMiddleware())
		{
			admin.GET("/users", adminHandler.ListUsers)
			admin.GET("/circles", adminHandler.ListCircles)
			admin.GET("/audit-log", adminHandler.GetAuditLog)
			admin.GET("/metrics", adminHandler.GetMetrics)
			admin.GET("/feature-flags", adminHandler.ListFeatureFlags)
			admin.GET("/feature-flags/:flag", adminHandler.GetFeatureFlag)
			admin.POST("/feature-flags", adminHandler.UpdateFeatureFlag)
			admin.DELETE("/feature-flags/:flag", adminHandler.DeleteFeatureFlag)
			admin.GET("/jobs/dead-letter", adminJobQueueHandler.GetDeadLetterJobs)
			admin.POST("/jobs/dead-letter/:id/retry", adminJobQueueHandler.RetryDeadLetterJob)
		}

		optional := api.Group("")
		optional.Use(middleware.OptionalAuthMiddleware(jwtPublicKey))
		{
			optional.GET("/circles", circleHandler.ListCircles)

			// GDPR cookie consent — works for both authenticated and anonymous users
			optional.GET("/consent", consentHandler.GetConsent)
			optional.POST("/consent", consentHandler.SaveConsent)
		}
	}

	return r
}
