package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
	"github.com/moistello/backend/pkg/validator"
)

type CircleHandler struct {
	circleService  circle.Service
	inviteService  invite.Service
	contribService contribution.Service
	payoutService  payout.Service
}

func NewCircleHandler(circleSvc circle.Service, inviteSvc invite.Service, contribSvc contribution.Service, payoutSvc payout.Service) *CircleHandler {
	return &CircleHandler{
		circleService:  circleSvc,
		inviteService:  inviteSvc,
		contribService: contribSvc,
		payoutService:  payoutSvc,
	}
}

// ExportCircle streams a portable JSON snapshot in bounded pages.
func (h *CircleHandler) ExportCircle(c *gin.Context) {
	ctx := c.Request.Context()
	circleID, userID := c.Param("id"), middleware.GetUserID(c)
	cir, err := h.circleService.Get(ctx, circleID)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		response.Forbidden(c, "circle membership required")
		return
	}
	organizer := cir.OrganizerID == uid
	if !organizer {
		member, memberErr := h.circleService.IsMember(ctx, circleID, userID)
		if memberErr != nil || !member {
			response.Forbidden(c, "circle membership required")
			return
		}
	}
	members, err := h.circleService.GetMembers(ctx, circleID)
	if err != nil {
		response.InternalError(c, "failed to export circle")
		return
	}
	enc := json.NewEncoder(c.Writer)
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"circle-%s-export.json\"", cir.ID))
	c.Status(http.StatusOK)
	write := func(s string) bool { _, e := c.Writer.WriteString(s); return e == nil }
	if !write("{\"version\":1,\"exportedAt\":") || enc.Encode(time.Now().UTC()) != nil || !write(",\"circle\":") || enc.Encode(cir) != nil || !write(fmt.Sprintf(",\"scope\":%q,\"rounds\":{\"current\":%d,\"total\":%d},\"members\":[", map[bool]string{true: "circle", false: "member"}[organizer], cir.CurrentRound, cir.CurrentRound)) {
		return
	}
	first := true
	for _, record := range members {
		if !organizer && record.UserID != uid {
			continue
		}
		if !first && !write(",") {
			return
		}
		first = false
		if enc.Encode(record) != nil {
			return
		}
	}
	if !write("],\"contributions\":[") {
		return
	}
	first = true
	const pageSize = 500
	for page := 1; ; page++ {
		records, total, e := h.contribService.GetCircleHistory(ctx, circleID, page, pageSize)
		if e != nil {
			return
		}
		for _, record := range records {
			if !organizer && record.UserID != uid {
				continue
			}
			if !first && !write(",") {
				return
			}
			first = false
			if enc.Encode(record) != nil {
				return
			}
		}
		if page*pageSize >= total || len(records) == 0 {
			break
		}
	}
	if !write("],\"payouts\":[") {
		return
	}
	first = true
	for page := 1; ; page++ {
		records, total, e := h.payoutService.GetCircleHistory(ctx, circleID, page, pageSize)
		if e != nil {
			return
		}
		for _, record := range records {
			if !organizer && record.RecipientID != uid {
				continue
			}
			if !first && !write(",") {
				return
			}
			first = false
			if enc.Encode(record) != nil {
				return
			}
		}
		if page*pageSize >= total || len(records) == 0 {
			break
		}
	}
	write("]}")
}

// @Summary List circles
// @Description Returns a paginated list of savings circles with optional search, status, and type filters.
// @Tags Circles
// @Produce json
// @Param search query string false "Search term"
// @Param status query string false "Filter by status" Enums(pending,active,completed,cancelled)
// @Param type query string false "Filter by type" Enums(fixed,flexible,auction)
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{circles=array},meta=response.PaginationMeta}
// @Router /circles [get]
func (h *CircleHandler) ListCircles(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := circle.CircleFilter{
		Search: c.Query("search"),
		Status: circle.CircleStatus(c.Query("status")),
		Type:   circle.CircleType(c.Query("type")),
		Page:   page,
		Limit:  limit,
	}
	if communityID := c.Query("communityId"); communityID != "" {
		if id, err := uuid.Parse(communityID); err == nil {
			filter.CommunityID = &id
		}
	}
	if organizerID := c.Query("organizerId"); organizerID != "" {
		if id, err := uuid.Parse(organizerID); err == nil {
			filter.OrganizerID = &id
		}
	}
	circles, total, err := h.circleService.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list circles")
		return
	}
	if circles == nil {
		circles = []circle.Circle{}
	}
	response.OKWithMeta(c, gin.H{"circles": circles}, response.NewPaginationMeta(page, limit, total))
}

// @Summary Create a circle
// @Description Creates a new savings circle. Requires authentication.
// @Tags Circles
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body circle.CreateCircleInput true "Circle configuration"
// @Success 201 {object} response.Envelope{data=object{circle=object}}
// @Failure 400 {object} response.Envelope
// @Failure 422 {object} response.Envelope
// @Router /circles [post]
func (h *CircleHandler) CreateCircle(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var input circle.CreateCircleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(input); err != nil {
		response.ValidationErrors(c, "validation failed: "+err.Error())
		return
	}
	cir, err := h.circleService.Create(c.Request.Context(), userID, input)
	if err != nil {
		response.InternalError(c, "failed to create circle")
		return
	}
	response.Created(c, gin.H{"circle": cir})
}

// @Summary Get a circle
// @Description Returns a single savings circle by ID.
// @Tags Circles
// @Produce json
// @Param id path string true "Circle ID"
// @Success 200 {object} response.Envelope{data=object{circle=object}}
// @Failure 404 {object} response.Envelope
// @Router /circles/{id} [get]
func (h *CircleHandler) GetCircle(c *gin.Context) {
	id := c.Param("id")
	cir, err := h.circleService.Get(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	response.OK(c, gin.H{"circle": cir})
}

func (h *CircleHandler) UpdateCircle(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	var input circle.UpdateCircleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	cir, err := h.circleService.Update(c.Request.Context(), id, userID, input)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"circle": cir})
}

func (h *CircleHandler) StartCircle(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.circleService.Start(c.Request.Context(), id, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CircleHandler) CancelCircle(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.circleService.Cancel(c.Request.Context(), id, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

// RecipientDistribution describes the calculated payout amounts for one recipient.
type RecipientDistribution struct {
	RecipientID string  `json:"recipientId"`
	GrossAmount float64 `json:"grossAmount"`
	FeeAmount   float64 `json:"feeAmount"`
	NetAmount   float64 `json:"netAmount"`
}

// PayoutPreviewResponse is the dry-run breakdown of an upcoming payout round.
type PayoutPreviewResponse struct {
	CircleID           string                  `json:"circleId"`
	RoundNumber        int                     `json:"roundNumber"`
	Currency           string                  `json:"currency"`
	TotalGrossAmount   float64                 `json:"totalGrossAmount"`
	TotalNetAmount     float64                 `json:"totalNetAmount"`
	ProtocolFee        float64                 `json:"protocolFee"`
	StellarFeeEstimate float64                 `json:"stellarFeeEstimate"`
	Recipients         []RecipientDistribution `json:"recipients"`
	DryRun             bool                    `json:"dryRun"`
}

func (h *CircleHandler) buildPayoutPreview(cir *circle.Circle, reqAmount *float64, reqFee *float64, recipientID string, roundNumber int) PayoutPreviewResponse {
	round := roundNumber
	if round < 1 {
		round = cir.CurrentRound
		if round < 1 {
			round = 1
		}
	}

	recipient := recipientID
	if recipient == "" {
		recipient = cir.OrganizerID.String()
	}

	gross := 0.0
	if reqAmount != nil && *reqAmount > 0 {
		gross = *reqAmount
	} else if cir.ContributionAmount > 0 {
		members := cir.MemberCount
		if members < 1 {
			members = cir.MaxMembers
			if members < 1 {
				members = 1
			}
		}
		gross = cir.ContributionAmount * float64(members)
	}

	protocolFee := 0.0
	if reqFee != nil && *reqFee >= 0 {
		protocolFee = *reqFee
	} else {
		// 1% default protocol fee
		protocolFee = math.Round(gross*0.01*100) / 100
	}

	net := gross - protocolFee
	if net < 0 {
		net = 0
	}

	// 100 stroops (0.00001 XLM) base fee per Stellar payment operation
	stellarFeeEstimate := 0.00001

	return PayoutPreviewResponse{
		CircleID:           cir.ID.String(),
		RoundNumber:        round,
		Currency:           string(cir.Currency),
		TotalGrossAmount:   gross,
		TotalNetAmount:     net,
		ProtocolFee:        protocolFee,
		StellarFeeEstimate: stellarFeeEstimate,
		Recipients: []RecipientDistribution{
			{
				RecipientID: recipient,
				GrossAmount: gross,
				FeeAmount:   protocolFee,
				NetAmount:   net,
			},
		},
		DryRun: true,
	}
}

// @Summary Dry-run preview of circle payout distribution
// @Description Calculates and returns the computed payout distribution for an active circle without executing money movement.
// @Tags Circles
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Param body body object{recipientId=string,roundNumber=int,amount=number,feeAmount=number} false "Optional overrides for preview"
// @Success 200 {object} response.Envelope{data=object{preview=PayoutPreviewResponse}}
// @Failure 400 {object} response.Envelope
// @Failure 403 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /circles/{id}/payout/preview [post]
func (h *CircleHandler) PreviewPayout(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	cir, err := h.circleService.Get(c.Request.Context(), circleID)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	if cir.OrganizerID.String() != userID {
		response.Forbidden(c, "only the organizer can preview or trigger a payout")
		return
	}
	if cir.Status != circle.CircleStatusActive {
		response.BadRequest(c, "circle is not active")
		return
	}

	var req struct {
		RecipientID string   `json:"recipientId"`
		RoundNumber int      `json:"roundNumber"`
		Amount      *float64 `json:"amount"`
		FeeAmount   *float64 `json:"feeAmount"`
	}
	_ = c.ShouldBindJSON(&req)

	preview := h.buildPayoutPreview(cir, req.Amount, req.FeeAmount, req.RecipientID, req.RoundNumber)
	response.OK(c, gin.H{"preview": preview})
}

// TriggerPayout records the payout for an active circle. Payout submission to
// Stellar remains upstream of this endpoint; txnHash is the on-chain receipt.
// If ?dry_run=true or body.dryRun=true is provided, returns the distribution preview without executing.
func (h *CircleHandler) TriggerPayout(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	cir, err := h.circleService.Get(c.Request.Context(), circleID)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	if cir.OrganizerID.String() != userID {
		response.Forbidden(c, "only the organizer can trigger a payout")
		return
	}
	if cir.Status != circle.CircleStatusActive {
		response.BadRequest(c, "circle is not active")
		return
	}

	if c.Query("dry_run") == "true" || c.Query("dryRun") == "true" {
		var previewReq struct {
			RecipientID string   `json:"recipientId"`
			RoundNumber int      `json:"roundNumber"`
			Amount      *float64 `json:"amount"`
			FeeAmount   *float64 `json:"feeAmount"`
		}
		_ = c.ShouldBindJSON(&previewReq)
		preview := h.buildPayoutPreview(cir, previewReq.Amount, previewReq.FeeAmount, previewReq.RecipientID, previewReq.RoundNumber)
		response.OK(c, gin.H{"preview": preview})
		return
	}

	var req payout.RecordInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(req); err != nil {
		response.ValidationErrors(c, "validation failed: "+err.Error())
		return
	}
	req.CircleID = circleID
	record, err := h.payoutService.Record(c.Request.Context(), req)
	if err != nil {
		response.InternalError(c, "failed to record payout: "+err.Error())
		return
	}
	response.Created(c, gin.H{"payout": record, "status": cir.Status})
}

func (h *CircleHandler) CloseCircle(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	_, payoutCount, err := h.payoutService.GetCircleHistory(
		c.Request.Context(), circleID, 1, 1,
	)
	if err != nil {
		response.InternalError(c, "failed to verify circle payouts")
		return
	}
	if payoutCount == 0 {
		response.BadRequest(c, "circle cannot close before its payout")
		return
	}
	if err := h.circleService.Close(c.Request.Context(), circleID, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"status": circle.CircleStatusCompleted})
}

// @Summary Join a circle
// @Description Joins an existing savings circle. Requires an invite code if the circle is private.
// @Tags Circles
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Param body body object{inviteCode=string} false "Invite code (optional for public circles)"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 400 {object} response.Envelope
// @Router /circles/{id}/join [post]
func (h *CircleHandler) JoinCircle(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		InviteCode string `json:"inviteCode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.InviteCode = ""
	}
	if req.InviteCode != "" {
		if _, err := h.inviteService.Validate(c.Request.Context(), req.InviteCode); err != nil {
			response.BadRequest(c, "invalid invite code")
			return
		}
	}
	if err := h.circleService.Join(c.Request.Context(), circleID, userID, req.InviteCode); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CircleHandler) Contribute(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		Amount      float64 `json:"amount" validate:"required,gt=0"`
		TxnHash     string  `json:"txnHash" validate:"required"`
		RoundNumber int     `json:"roundNumber" validate:"required,gte=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(req); err != nil {
		response.ValidationErrors(c, "validation failed: "+err.Error())
		return
	}

	cir, err := h.circleService.Get(c.Request.Context(), circleID)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	if cir.CircleType == circle.CircleTypePremium {
		if cir.Currency == circle.CurrencyUSDC && req.Amount < 50 {
			response.BadRequest(c, "premium circles require minimum 50 USDC contribution")
			return
		}
		if cir.Currency == circle.CurrencyXLM && req.Amount < 100 {
			response.BadRequest(c, "premium circles require minimum 100 XLM contribution")
			return
		}
	}

	contrib, err := h.contribService.Record(c.Request.Context(), contribution.RecordInput{
		CircleID:    circleID,
		UserID:      userID,
		RoundNumber: req.RoundNumber,
		Amount:      req.Amount,
		TxnHash:     req.TxnHash,
	})
	if err != nil {
		response.InternalError(c, "failed to record contribution: "+err.Error())
		return
	}

	response.Created(c, gin.H{"success": true, "contribution": contrib})
}

func (h *CircleHandler) ExitCircle(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.circleService.Exit(c.Request.Context(), id, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CircleHandler) GetMembers(c *gin.Context) {
	circleID := c.Param("id")
	members, err := h.circleService.GetMembers(c.Request.Context(), circleID)
	if err != nil {
		response.InternalError(c, "failed to get members")
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsedLimit, err := strconv.Atoi(l); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	if limit > 100 {
		limit = 100
	}

	cursorOffset := 0
	if cur := c.Query("cursor"); cur != "" {
		if offset, err := strconv.Atoi(cur); err == nil && offset >= 0 {
			cursorOffset = offset
		}
	}

	total := len(members)
	if cursorOffset > total {
		cursorOffset = total
	}

	end := cursorOffset + limit
	hasMore := false
	nextCursor := ""
	if end < total {
		hasMore = true
		nextCursor = strconv.Itoa(end)
	} else {
		end = total
	}

	paged := members[cursorOffset:end]
	response.OKWithMeta(c, gin.H{"members": paged}, gin.H{
		"total":      total,
		"limit":      limit,
		"cursor":     c.Query("cursor"),
		"nextCursor": nextCursor,
		"hasMore":    hasMore,
	})
}

func (h *CircleHandler) GetRounds(c *gin.Context) {
	circleID := c.Param("id")
	page, limit, _ := pagination.Parse(c)

	cir, err := h.circleService.Get(c.Request.Context(), circleID)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}

	contribs, contribTotal, err := h.contribService.GetCircleHistory(c.Request.Context(), circleID, page, limit)
	if err != nil {
		contribs = nil
	}

	payouts, payoutTotal, err := h.payoutService.GetCircleHistory(c.Request.Context(), circleID, page, limit)
	if err != nil {
		payouts = nil
	}

	total := contribTotal
	if payoutTotal > total {
		total = payoutTotal
	}

	roundMap := make(map[int]map[string]any)
	for _, c := range contribs {
		entry, ok := roundMap[c.RoundNumber]
		if !ok {
			entry = map[string]any{
				"roundNumber":   c.RoundNumber,
				"contributions": []any{},
			}
			roundMap[c.RoundNumber] = entry
		}
		entry["contributions"] = append(entry["contributions"].([]any), c)
	}
	for _, p := range payouts {
		entry, ok := roundMap[p.RoundNumber]
		if !ok {
			entry = map[string]any{
				"roundNumber":   p.RoundNumber,
				"contributions": []any{},
			}
			roundMap[p.RoundNumber] = entry
		}
		entry["payout"] = p
	}

	roundNumbers := make([]int, 0, len(roundMap))
	for roundNum := range roundMap {
		roundNumbers = append(roundNumbers, roundNum)
	}

	for roundNum, entry := range roundMap {
		configSnapshot, err := h.circleService.QueryRoundConfig(c.Request.Context(), circleID, roundNum)
		if err == nil && configSnapshot != nil {
			entry["configSnapshot"] = configSnapshot
		}
	}

	rounds := make([]map[string]any, 0, len(roundMap))
	for _, v := range roundMap {
		rounds = append(rounds, v)
	}

	response.OKWithMeta(c, gin.H{
		"rounds":       rounds,
		"currentRound": cir.CurrentRound,
		"totalMembers": cir.MaxMembers,
	}, response.NewPaginationMeta(page, limit, total))
}

func (h *CircleHandler) GetPayouts(c *gin.Context) {
	circleID := c.Param("id")
	page, limit, _ := pagination.Parse(c)
	payouts, total, err := h.payoutService.GetCircleHistory(c.Request.Context(), circleID, page, limit)
	if err != nil {
		response.InternalError(c, "failed to get payouts")
		return
	}
	if payouts == nil {
		payouts = []payout.Payout{}
	}
	response.OKWithMeta(c, gin.H{"payouts": payouts}, response.NewPaginationMeta(page, limit, total))
}

func (h *CircleHandler) Dispute(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req circle.DisputeInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(req); err != nil {
		response.ValidationErrors(c, "validation failed: "+err.Error())
		return
	}

	dispute, err := h.circleService.RaiseDispute(c.Request.Context(), circleID, userID, req)
	if err != nil {
		if err == circle.ErrCircleNotFound {
			response.NotFound(c, "circle not found")
			return
		}
		if err == circle.ErrNotMember {
			response.Forbidden(c, "only active circle members can raise disputes")
			return
		}
		if err == circle.ErrCircleNotActive {
			response.BadRequest(c, "circle is not active")
			return
		}
		response.InternalError(c, "failed to raise dispute: "+err.Error())
		return
	}

	response.Created(c, gin.H{"success": true, "dispute": dispute})
}

func (h *CircleHandler) Vote(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req circle.VoteInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(req); err != nil {
		response.ValidationErrors(c, "validation failed: "+err.Error())
		return
	}

	vote, allVoted, winnerID, err := h.circleService.CastVote(c.Request.Context(), circleID, userID, req)
	if err != nil {
		if err == circle.ErrCircleNotFound {
			response.NotFound(c, "circle not found")
			return
		}
		if err == circle.ErrNotMember {
			response.Forbidden(c, "only active circle members can vote")
			return
		}
		if err == circle.ErrCircleNotActive {
			response.BadRequest(c, "circle is not active")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	res := gin.H{
		"success":  true,
		"vote":     vote,
		"allVoted": allVoted,
	}
	if allVoted {
		res["winnerId"] = winnerID
	}
	response.OK(c, res)
}

// RemoveMember allows the circle organizer to remove a member by their user ID (address).
// The member's status is set to 'removed' and a MemberLeft event is broadcast.
// @Summary Remove a member from a circle
// @Description Organizer-only. Removes a member, broadcasts event for stake redistribution.
// @Tags Circles
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Param address path string true "Member user ID (address)"
// @Param body body object{reason=string} false "Optional removal reason"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 400 {object} response.Envelope
// @Failure 403 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /circles/{id}/members/{address}/remove [post]
func (h *CircleHandler) RemoveMember(c *gin.Context) {
	circleID := c.Param("id")
	memberAddress := c.Param("address")
	callerID := middleware.GetUserID(c)

	var req struct {
		Reason string `json:"reason"`
	}
	// reason is optional; ignore bind errors
	_ = c.ShouldBindJSON(&req)

	if err := h.circleService.RemoveMember(c.Request.Context(), circleID, callerID, memberAddress, req.Reason); err != nil {
		switch err {
		case circle.ErrNotOrganizer:
			response.Forbidden(c, "only the organizer can remove members")
		case circle.ErrNotMember:
			response.NotFound(c, "member not found or already inactive")
		default:
			response.BadRequest(c, err.Error())
		}
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CircleHandler) AuctionBid(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req circle.AuctionBidInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(req); err != nil {
		response.ValidationErrors(c, "validation failed: "+err.Error())
		return
	}

	bid, err := h.circleService.SubmitAuctionBid(c.Request.Context(), circleID, userID, req)
	if err != nil {
		if err == circle.ErrCircleNotFound {
			response.NotFound(c, "circle not found")
			return
		}
		if err == circle.ErrNotMember {
			response.Forbidden(c, "only active circle members can bid")
			return
		}
		if err == circle.ErrCircleNotActive {
			response.BadRequest(c, "circle is not active")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	response.Created(c, gin.H{"success": true, "bid": bid})
}

// GetRoundConfig returns the immutable configuration snapshot captured for a round.
// @Summary Get circle configuration snapshot for a round
// @Tags Circles
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Param round path int true "Round Number"
// @Success 200 {object} response.Envelope{data=circle.RoundConfigSnapshot}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /circles/{id}/rounds/{round}/config [get]
func (h *CircleHandler) GetRoundConfig(c *gin.Context) {
	circleID := c.Param("id")
	roundStr := c.Param("round")
	round, err := strconv.Atoi(roundStr)
	if err != nil || round <= 0 {
		response.BadRequest(c, "invalid round number")
		return
	}

	snapshot, err := h.circleService.QueryRoundConfig(c.Request.Context(), circleID, round)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) || errors.Is(err, circle.ErrCircleNotFound) {
			response.NotFound(c, "round configuration snapshot not found")
			return
		}
		response.InternalError(c, "failed to query round config: "+err.Error())
		return
	}

	response.OK(c, snapshot)
}

// GetSnapshot godoc
// @Summary      Circle state snapshot
// @Description  Returns a single-transaction consistent view of a circle's full state for the authenticated user:
//
//	circle details, members, all rounds (contributions + payouts), the user's balance, and the circle's vault balance.
//
// @Tags         Circles
// @Produce      json
// @Param        id   path      string  true  "Circle UUID"
// @Success      200  {object}  response.Envelope{data=object{snapshot=circle.CircleSnapshot}}
// @Failure      400  {object}  response.Envelope
// @Failure      404  {object}  response.Envelope
// @Failure      500  {object}  response.Envelope
// @Router       /circles/{id}/snapshot [get]
// @Security     BearerAuth
func (h *CircleHandler) GetSnapshot(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)

	// A malformed id is the caller's mistake, not a server fault: answer 400
	// here rather than letting the service's parse failure fall through to the
	// 500 branch below.
	if _, err := uuid.Parse(circleID); err != nil {
		response.BadRequest(c, "invalid circle id")
		return
	}

	snap, err := h.circleService.GetCircleSnapshot(c.Request.Context(), circleID, userID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.NotFound(c, "circle not found")
			return
		}
		// A snapshot is circle-private: a caller who is neither organizer nor an
		// active member is refused rather than served a redacted one (#440).
		if errors.Is(err, apperrors.ErrForbidden) {
			response.Forbidden(c, "not a member of this circle")
			return
		}
		response.InternalError(c, "failed to build circle snapshot")
		return
	}
	response.OK(c, gin.H{"snapshot": snap})
}

// GetBulkSnapshots godoc
// @Summary      Bulk circle state snapshots
// @Description  Returns consistent snapshots for all circles the authenticated user belongs to or organizes.
//
//	Optionally accepts a comma-separated list of circle UUIDs via the `ids` query parameter to filter.
//	Each snapshot is fetched in a separate repeatable-read transaction, so results are internally consistent.
//
// @Tags         Circles
// @Produce      json
// @Param        ids  query     string  false  "Comma-separated circle UUIDs to fetch (omit for all user circles)"
// @Success      200  {object}  response.Envelope{data=object{snapshots=[]circle.CircleSnapshot}}
// @Failure      400  {object}  response.Envelope
// @Failure      500  {object}  response.Envelope
// @Router       /circles/snapshots [get]
// @Security     BearerAuth
func (h *CircleHandler) GetBulkSnapshots(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var circleIDs []string
	if raw := c.Query("ids"); raw != "" {
		for _, id := range splitComma(raw) {
			if id != "" {
				// Same reasoning as GetSnapshot: a malformed filter is a bad
				// request, not a server fault.
				if _, err := uuid.Parse(id); err != nil {
					response.BadRequest(c, "invalid circle id")
					return
				}
				circleIDs = append(circleIDs, id)
			}
		}
	}

	snaps, err := h.circleService.GetBulkCircleSnapshots(c.Request.Context(), userID, circleIDs)
	if err != nil {
		if errors.Is(err, apperrors.ErrForbidden) {
			response.Forbidden(c, "not a member of one or more of these circles")
			return
		}
		response.InternalError(c, "failed to build circle snapshots")
		return
	}
	response.OK(c, gin.H{"snapshots": snaps})
}

// splitComma splits a comma-separated string and trims whitespace from each element.
func splitComma(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
