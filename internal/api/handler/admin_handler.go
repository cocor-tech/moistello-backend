package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/internal/domain/admin"
	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/featureflag"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
	"github.com/rs/zerolog/log"
)

type AdminHandler struct {
	userService    user.Service
	userRepo       user.Repository
	circleService  circle.Service
	auditRepo      audit.Repository
	metricsSvc     *admin.Service
	featureFlagSvc featureflag.Service
	flagCache      *featureflag.Cache
}

func NewAdminHandler(userSvc user.Service, userRepo user.Repository, circleSvc circle.Service, auditRepo audit.Repository, metricsSvc *admin.Service, featureFlagSvc featureflag.Service, flagCache *featureflag.Cache) *AdminHandler {
	return &AdminHandler{
		userService:    userSvc,
		userRepo:       userRepo,
		circleService:  circleSvc,
		auditRepo:      auditRepo,
		metricsSvc:     metricsSvc,
		featureFlagSvc: featureFlagSvc,
		flagCache:      flagCache,
	}
}

// @Summary [Admin] List users
// @Description Lists all users with pagination and search. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param search query string false "Search by wallet or email"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{users=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /admin/users [get]
func (h *AdminHandler) ListUsers(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := user.UserFilter{
		Search: c.Query("search"),
		Page:   page,
		Limit:  limit,
	}
	users, err := h.userRepo.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list users")
		return
	}
	total, err := h.userRepo.Count(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to count users")
		return
	}
	response.OKWithMeta(c, gin.H{"users": users}, response.NewPaginationMeta(page, limit, total))
}

// @Summary [Admin] List soft-deleted users
// @Description Lists soft-deleted users with pagination and search. Admin only. Soft-deleted users are excluded from every normal user and auth lookup; this endpoint is the administrative view of them.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param search query string false "Search by wallet or email"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{users=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /admin/users/deleted [get]
func (h *AdminHandler) ListDeletedUsers(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := user.UserFilter{
		Search: c.Query("search"),
		Page:   page,
		Limit:  limit,
	}
	users, err := h.userRepo.ListDeleted(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list deleted users")
		return
	}
	total, err := h.userRepo.CountDeleted(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to count deleted users")
		return
	}
	response.OKWithMeta(c, gin.H{"users": users}, response.NewPaginationMeta(page, limit, total))
}

// @Summary [Admin] Restore a soft-deleted user
// @Description Restores a soft-deleted user by clearing deleted_at, returning them to the active set so they can authenticate again. The audit trail and all related records are preserved. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Success 200 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Failure 409 {object} response.Envelope
// @Router /admin/users/{id}/restore [post]
func (h *AdminHandler) RestoreUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid user ID")
		return
	}

	ctx := c.Request.Context()
	if err := h.userRepo.Restore(ctx, id); err != nil {
		switch {
		case errors.Is(err, user.ErrUserNotFound):
			response.NotFound(c, "user not found")
			return
		case errors.Is(err, user.ErrUserNotDeleted):
			// The user exists and is already active; restoring would be a no-op.
			response.Conflict(c, "user is not deleted")
			return
		default:
			response.InternalError(c, "failed to restore user")
			return
		}
	}

	// Record the administrative action so the audit trail captures who
	// re-admitted the account, not just that the account was deleted.
	if h.auditRepo != nil {
		actorID, _ := uuid.Parse(c.GetString("userID"))
		details, _ := json.Marshal(map[string]string{"restoredUserId": id.String()})
		entry := &audit.AuditEntry{
			ActorID:      actorID,
			Action:       "user.restored",
			ResourceType: "user",
			ResourceID:   sql.NullString{String: id.String(), Valid: true},
			Details:      details,
		}
		if err := h.auditRepo.Log(ctx, entry); err != nil {
			// The user is already restored; failing the request here would be
			// misleading, so record the problem and still report success.
			log.Warn().Err(err).Str("userId", id.String()).Msg("failed to audit user restore")
		}
	}

	response.OK(c, gin.H{"id": id, "restored": true})
}

// @Summary [Admin] List all circles
// @Description Lists all circles with pagination, search, and status filter. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param search query string false "Search term"
// @Param status query string false "Filter by status"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{circles=array},meta=response.PaginationMeta}
// @Router /admin/circles [get]
func (h *AdminHandler) ListCircles(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := circle.CircleFilter{
		Search: c.Query("search"),
		Status: circle.CircleStatus(c.Query("status")),
		Page:   page,
		Limit:  limit,
	}
	circles, total, err := h.circleService.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list circles")
		return
	}
	response.OKWithMeta(c, gin.H{"circles": circles}, response.NewPaginationMeta(page, limit, total))
}

// @Summary [Admin] Get audit log
// @Description Returns a paginated system audit log, optionally filtered by resource type, action, actor, and time range. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param resource_type query string false "Filter by resource type, e.g. circle"
// @Param action query string false "Filter by action, e.g. circle.inspected"
// @Param actor_id query string false "Filter by acting user ID (UUID)"
// @Param from query string false "Inclusive lower bound on created_at (RFC3339 or YYYY-MM-DD)"
// @Param to query string false "Inclusive upper bound on created_at (RFC3339 or YYYY-MM-DD)"
// @Success 200 {object} response.Envelope{data=object{entries=array},meta=response.PaginationMeta}
// @Failure 400 {object} response.Envelope
// @Failure 500 {object} response.Envelope
// @Router /admin/audit-log [get]
func (h *AdminHandler) GetAuditLog(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)

	filter, err := parseAuditLogFilter(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	entries, total, err := h.auditRepo.List(c.Request.Context(), filter, page, limit)
	if err != nil {
		response.InternalError(c, "failed to fetch audit log")
		return
	}
	if entries == nil {
		entries = []audit.AuditEntry{}
	}
	response.OKWithMeta(c, gin.H{"entries": entries}, response.NewPaginationMeta(page, limit, total))
}

// parseAuditLogFilter reads the query-string filters. Anything malformed is
// reported as an error rather than dropped, so a typo in a filter is never
// silently turned into an unfiltered query over the whole audit log.
func parseAuditLogFilter(c *gin.Context) (audit.ListFilter, error) {
	var filter audit.ListFilter
	filter.ResourceType = strings.TrimSpace(c.Query("resource_type"))
	filter.Action = strings.TrimSpace(c.Query("action"))

	if raw := strings.TrimSpace(c.Query("actor_id")); raw != "" {
		actorID, err := uuid.Parse(raw)
		if err != nil {
			return filter, errors.New("actor_id must be a valid UUID")
		}
		filter.ActorID = &actorID
	}

	from, err := parseAuditLogTime(c.Query("from"))
	if err != nil {
		return filter, fmt.Errorf("from: %w", err)
	}
	filter.From = from

	to, err := parseAuditLogTime(c.Query("to"))
	if err != nil {
		return filter, fmt.Errorf("to: %w", err)
	}
	filter.To = to

	if filter.Inverted() {
		return filter, errors.New("from must not be after to")
	}
	return filter, nil
}

var auditLogTimeLayouts = []string{time.RFC3339, "2006-01-02"}

// parseAuditLogTime accepts a full timestamp or a plain date. A date-only value
// is anchored at the start of that day in UTC, so `from=2026-01-02` and
// `to=2026-01-02` bracket that whole day rather than one instant.
func parseAuditLogTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	for _, layout := range auditLogTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t, nil
		}
	}
	return nil, errors.New("must be an RFC3339 timestamp or a YYYY-MM-DD date")
}

// @Summary [Admin] Get system metrics
// @Description Returns platform-wide metrics (users, circles, contributions, payouts, volume, active users, and time-bucketed daily volume). Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param days query int false "Number of trailing days for time-bucketed aggregates" default(30)
// @Success 200 {object} response.Envelope{data=object{totalUsers=number,totalCircles=number,activeCircles=number,totalContributions=number,totalPayouts=number,activeUsers=number,newUsers30d=number,contributionVolume=number,payoutVolume=number,totalVolumeUSD=number,volumeUSD30d=number,dailyVolume=array}}
// @Failure 500 {object} response.Envelope
// @Router /admin/metrics [get]
func (h *AdminHandler) GetMetrics(c *gin.Context) {
	days := 30
	if raw := c.Query("days"); raw != "" {
		if d, err := strconv.Atoi(raw); err == nil && d > 0 && d <= 365 {
			days = d
		}
	}

	metrics, err := h.metricsSvc.GetMetrics(c.Request.Context(), days)
	if err != nil {
		response.InternalError(c, "failed to fetch platform metrics")
		return
	}
	// Flatten the aggregate onto the response data to preserve the original
	// top-level keys (totalUsers, totalCircles, activeCircles, totalVolumeUSD)
	// while exposing the new aggregates.
	response.OK(c, gin.H{
		"totalUsers":         metrics.TotalUsers,
		"totalCircles":       metrics.TotalCircles,
		"activeCircles":      metrics.ActiveCircles,
		"totalContributions": metrics.TotalContributions,
		"totalPayouts":       metrics.TotalPayouts,
		"activeUsers":        metrics.ActiveUsers,
		"newUsers30d":        metrics.NewUsers30d,
		"contributionVolume": metrics.ContributionVolume,
		"payoutVolume":       metrics.PayoutVolume,
		"totalVolumeUSD":     metrics.TotalVolumeUSD,
		"volumeUSD30d":       metrics.VolumeUSD30d,
		"dailyVolume":        metrics.DailyVolume,
	})
}

// @Summary [Admin] List feature flags
// @Description Lists all feature flags and their current state. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{flags=array}}
// @Failure 500 {object} response.Envelope
// @Router /admin/feature-flags [get]
func (h *AdminHandler) ListFeatureFlags(c *gin.Context) {
	flags, err := h.featureFlagSvc.List(c.Request.Context())
	if err != nil {
		response.InternalError(c, "failed to list feature flags")
		return
	}
	response.OK(c, gin.H{"flags": flags})
}

// @Summary [Admin] Get feature flag
// @Description Returns a single feature flag by name. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param flag path string true "Feature flag name"
// @Success 200 {object} response.Envelope{data=object{flag=object}}
// @Failure 404 {object} response.Envelope
// @Router /admin/feature-flags/{flag} [get]
func (h *AdminHandler) GetFeatureFlag(c *gin.Context) {
	flag, err := h.featureFlagSvc.Get(c.Request.Context(), c.Param("flag"))
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.NotFound(c, "feature flag not found")
			return
		}
		response.InternalError(c, "failed to get feature flag")
		return
	}
	response.OK(c, gin.H{"flag": flag})
}

// @Summary [Admin] Create or update feature flag
// @Description Creates a feature flag if it doesn't exist, or updates its enabled state and description. Admin only. Takes effect for this process immediately; other instances pick it up within the flag cache's reload interval.
// @Tags Admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object{flag=string,value=bool,description=string} true "Feature flag name, value, and optional description"
// @Success 200 {object} response.Envelope{data=object{flag=object}}
// @Failure 400 {object} response.Envelope
// @Router /admin/feature-flags [post]
func (h *AdminHandler) UpdateFeatureFlag(c *gin.Context) {
	var req struct {
		Flag        string `json:"flag" binding:"required"`
		Value       bool   `json:"value"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	f, err := h.featureFlagSvc.Set(c.Request.Context(), req.Flag, req.Value, req.Description)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if h.flagCache != nil {
		// Best-effort: refresh this process's cache immediately so the
		// change is visible right away instead of waiting for the next
		// scheduled reload. A failure here just means the periodic reload
		// picks it up on its own schedule.
		_ = h.flagCache.Refresh(c.Request.Context())
	}

	response.OK(c, gin.H{"flag": f})
}

// @Summary [Admin] Delete feature flag
// @Description Permanently removes a feature flag. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param flag path string true "Feature flag name"
// @Success 200 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /admin/feature-flags/{flag} [delete]
func (h *AdminHandler) DeleteFeatureFlag(c *gin.Context) {
	flag := c.Param("flag")
	if err := h.featureFlagSvc.Delete(c.Request.Context(), flag); err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.NotFound(c, "feature flag not found")
			return
		}
		response.InternalError(c, "failed to delete feature flag")
		return
	}

	if h.flagCache != nil {
		_ = h.flagCache.Refresh(c.Request.Context())
	}

	response.OK(c, gin.H{"deleted": flag})
}

// @Summary [Admin] Inspect circle state
// @Description Read-only inspection of circle state (members, rounds, balances) with audit logging. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Success 200 {object} response.Envelope{data=object}
// @Failure 403 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /admin/circles/{id}/inspect [get]
func (h *AdminHandler) InspectCircleState(c *gin.Context) {
	circleID := c.Param("id")
	callerID := c.GetString("userID")
	role := c.GetString("role")

	if role != "admin" && c.GetString("user_role") != "admin" && !c.GetBool("isAdmin") {
		response.Forbidden(c, "admin access required")
		return
	}

	circ, err := h.circleService.Get(c.Request.Context(), circleID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) || errors.Is(err, circle.ErrCircleNotFound) {
			response.NotFound(c, "circle not found")
			return
		}
		response.InternalError(c, "failed to fetch circle")
		return
	}

	members, err := h.circleService.GetMembers(c.Request.Context(), circleID)
	if err != nil {
		members = []circle.CircleMember{}
	}

	if h.auditRepo != nil {
		callerUID, _ := uuid.Parse(callerID)
		_ = h.auditRepo.Log(c.Request.Context(), &audit.AuditEntry{
			ID:           uuid.New(),
			ActorID:      callerUID,
			Action:       "admin.circle.inspect",
			ResourceType: "circle",
			ResourceID:   sql.NullString{String: circleID, Valid: circleID != ""},
			IPAddress:    sql.NullString{String: c.ClientIP(), Valid: c.ClientIP() != ""},
			UserAgent:    sql.NullString{String: c.Request.UserAgent(), Valid: c.Request.UserAgent() != ""},
			CreatedAt:    time.Now().UTC(),
		})
	}

	response.OK(c, gin.H{
		"circle":       circ,
		"members":      members,
		"memberCount":  len(members),
		"currentRound": circ.CurrentRound,
		"status":       circ.Status,
	})
}
