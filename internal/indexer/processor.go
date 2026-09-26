package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/rabbitmq"
)

// EventProcessor maps Stellar transactions to domain events, persists them
// to PostgreSQL, broadcasts real-time updates via WebSocket, and publishes
// events to RabbitMQ for async workers.
type EventProcessor struct {
	db             *sqlx.DB
	rmqClient      *rabbitmq.Client
	circleRepo     circle.Repository
	contribRepo    contribution.Repository
	payoutRepo     payout.Repository
	reputationRepo reputation.Repository
	userRepo       user.Repository
	wsBroadcast    func(circleID string, data any)
}

// NewEventProcessor creates a new EventProcessor with all required dependencies.
func NewEventProcessor(
	db *sqlx.DB,
	rmqClient *rabbitmq.Client,
	circleRepo circle.Repository,
	contribRepo contribution.Repository,
	payoutRepo payout.Repository,
	reputationRepo reputation.Repository,
	userRepo user.Repository,
) *EventProcessor {
	return &EventProcessor{
		db:             db,
		rmqClient:      rmqClient,
		circleRepo:     circleRepo,
		contribRepo:    contribRepo,
		payoutRepo:     payoutRepo,
		reputationRepo: reputationRepo,
		userRepo:       userRepo,
	}
}

// SetWebSocketBroadcast sets the callback for real-time WebSocket updates.
// When set, every processed event will be broadcast to connected clients
// subscribed to the relevant circle room.
func (p *EventProcessor) SetWebSocketBroadcast(fn func(circleID string, data any)) {
	p.wsBroadcast = fn
}

// ProcessTransaction maps a Stellar transaction to domain events and
// persists the resulting entities to PostgreSQL.
func (p *EventProcessor) ProcessTransaction(ctx context.Context, txn *Transaction) error {
	if len(txn.Operations) == 0 {
		return nil
	}

	var errs []error
	for _, op := range txn.Operations {
		if err := p.processOperation(ctx, txn, &op); err != nil {
			log.Warn().Err(err).
				Str("hash", txn.Hash).
				Str("op_type", op.Type).
				Msg("processing operation")
			errs = append(errs, err)
			continue
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("processing %d/%d operations: %w", len(errs), len(txn.Operations), errs[0])
	}
	return nil
}

func (p *EventProcessor) processOperation(ctx context.Context, txn *Transaction, op *Operation) error {
	switch {
	case op.Type == "create_account":
		return p.handleCreateAccount(ctx, txn, op)
	case op.Type == "payment":
		return p.handlePayment(ctx, txn, op)
	case op.Type == "invoke_host_function":
		return p.handleSorobanInvoke(ctx, txn, op)
	case op.Type == "extend_footprint_ttl":
		return p.handleExtendTTL(ctx, txn, op)
	default:
		log.Debug().Str("type", op.Type).Msg("unhandled operation type")
		return nil
	}
}

func (p *EventProcessor) handleCreateAccount(ctx context.Context, txn *Transaction, op *Operation) error {
	// A new Stellar account was created — potentially a new user onboarding.
	// In production this would trigger user auto-creation and KYC workflows.
	log.Info().
		Str("hash", txn.Hash).
		Str("source", op.SourceAccount).
		Msg("create_account detected")

	p.Broadcast(ctx, op.SourceAccount, "account_created", map[string]any{
		"hash":    txn.Hash,
		"account": op.SourceAccount,
		"ledger":  txn.Ledger,
	})
	return nil
}

func (p *EventProcessor) handlePayment(ctx context.Context, txn *Transaction, op *Operation) error {
	// A payment operation was detected — could represent a circle contribution,
	// a payout distribution, or a Soroban contract interaction.
	log.Info().
		Str("hash", txn.Hash).
		Str("source", op.SourceAccount).
		Msg("payment detected")

	p.Broadcast(ctx, op.SourceAccount, "payment_detected", map[string]any{
		"hash":   txn.Hash,
		"source": op.SourceAccount,
		"ledger": txn.Ledger,
	})
	return nil
}

// handleSorobanInvoke decodes the result_meta_xdr attached to an
// invoke_host_function operation, extracts all Soroban contract events, and
// dispatches each to the appropriate typed handler.
func (p *EventProcessor) handleSorobanInvoke(ctx context.Context, txn *Transaction, op *Operation) error {
	events, err := ParseContractEvents(txn.Hash, txn.Ledger, op.ResultMetaXDR)
	if err != nil {
		// Non-fatal: log and continue; the event is on-chain and will be
		// retried by the reconciler on the next pass.
		log.Warn().Err(err).
			Str("hash", txn.Hash).
			Int64("ledger", txn.Ledger).
			Str("payload", payloadSnippet(op.ResultMetaXDR)).
			Msg("parsing contract events from result_meta_xdr")
		return nil
	}

	// Re-scanning a ledger range must be a no-op: events already recorded in
	// the contract_events log are skipped, and an event is recorded only
	// after it was applied so a failed one is retried on the next pass.
	for _, ev := range events {
		if p.eventRecorded(ctx, &ev) {
			log.Debug().
				Str("event_type", ev.EventType).
				Str("hash", txn.Hash).
				Msg("contract event already processed — skipping")
			continue
		}
		if err := p.dispatchEvent(ctx, &ev); err != nil {
			log.Warn().Err(err).
				Str("event_type", ev.EventType).
				Str("contract", ev.ContractID).
				Str("event_type_hash", eventTypeHash([]byte(ev.EventType))).
				Int64("ledger", ev.Ledger).
				Str("hash", txn.Hash).
				Str("payload", payloadSnippet(fmt.Sprint(ev.Payload))).
				Msg("dispatching contract event")
			continue
		}
		if err := p.persistContractEvent(ctx, &ev); err != nil {
			log.Warn().Err(err).
				Str("event_type", ev.EventType).
				Msg("persisting contract event to audit log")
		}
	}
	return nil
}

func (p *EventProcessor) handleExtendTTL(ctx context.Context, txn *Transaction, op *Operation) error {
	// Contract instance storage TTL was extended — no domain event required,
	// but we track it for observability.
	log.Debug().
		Str("hash", txn.Hash).
		Str("source", op.SourceAccount).
		Msg("extend_footprint_ttl detected")
	return nil
}

// ---------------------------------------------------------------------------
// dispatchEvent routes a decoded ContractEvent to its typed handler.
// ---------------------------------------------------------------------------

func (p *EventProcessor) dispatchEvent(ctx context.Context, ev *ContractEvent) error {
	switch ev.EventType {
	case EventCircleCreated:
		return p.onCircleCreated(ctx, ev)
	case EventMemberJoined:
		return p.onMemberJoined(ctx, ev)
	case EventContributionReceived:
		return p.onContributionReceived(ctx, ev)
	case EventPayoutExecuted:
		return p.onPayoutExecuted(ctx, ev)
	case EventLateReported:
		return p.onLateReported(ctx, ev)
	case EventMemberExited:
		return p.onMemberExited(ctx, ev)
	case EventDefaultRecorded:
		return p.onDefaultRecorded(ctx, ev)
	case EventCircleCompleted:
		return p.onCircleCompleted(ctx, ev)
	case EventAuctionBid:
		return p.onAuctionBid(ctx, ev)
	case EventVoteCast:
		return p.onVoteCast(ctx, ev)
	case EventDisputeRaised:
		return p.onDisputeRaised(ctx, ev)
	case EventFeeDeposited:
		return p.onFeeDeposited(ctx, ev)
	default:
		log.Debug().Str("event_type", ev.EventType).Msg("unknown contract event — skipping")
		return nil
	}
}

// ---------------------------------------------------------------------------
// Individual event handlers
// ---------------------------------------------------------------------------

// onCircleCreated handles CircleCreated(circle_id, creator, config_hash).
// The circle must already exist in the DB (created via the API); this handler
// links it to its on-chain contract ID.
func (p *EventProcessor) onCircleCreated(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	if contractID == "" {
		contractID = ev.ContractID
	}

	existing, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("onCircleCreated find: %w", err)
	}
	if existing != nil {
		// Already linked — idempotent.
		log.Debug().Str("contract_id", contractID).Msg("CircleCreated: circle already linked")
	} else {
		log.Info().
			Str("contract_id", contractID).
			Str("creator", payloadStr(ev.Payload, "creator")).
			Msg("CircleCreated: new on-chain circle (not yet matched to DB record)")
	}

	p.Broadcast(ctx, contractID, "circle.created", map[string]any{
		"contract_id": contractID,
		"creator":     payloadStr(ev.Payload, "creator"),
		"tx_hash":     ev.TxHash,
		"ledger":      ev.Ledger,
	})
	return nil
}

// onMemberJoined handles MemberJoined(circle_id, member, contribution_amount).
// Resolves the wallet address to an internal user ID and creates a CircleMember row.
func (p *EventProcessor) onMemberJoined(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	walletAddr := payloadStr(ev.Payload, "member")

	c, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("contract_id", contractID).Msg("MemberJoined: circle not found in DB")
			return nil
		}
		return fmt.Errorf("onMemberJoined find circle: %w", err)
	}

	u, err := p.userRepo.FindByWalletAddress(ctx, walletAddr)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("wallet", walletAddr).Msg("MemberJoined: user not found in DB")
			return nil
		}
		return fmt.Errorf("onMemberJoined find user: %w", err)
	}

	member := &circle.CircleMember{
		CircleID: c.ID,
		UserID:   u.ID,
		Status:   circle.MemberStatusActive,
		JoinedAt: time.Now().UTC(),
	}
	if err := p.circleRepo.CreateMember(ctx, member); err != nil {
		if errors.Is(err, circle.ErrAlreadyMember) {
			log.Debug().Str("tx_hash", ev.TxHash).Msg("MemberJoined: already recorded")
			return nil
		}
		return fmt.Errorf("onMemberJoined create member: %w", err)
	}

	log.Info().
		Str("circle_id", c.ID.String()).
		Str("user_id", u.ID.String()).
		Msg("MemberJoined: member record created")

	p.Broadcast(ctx, c.ID.String(), "member.joined", map[string]any{
		"circle_id": c.ID.String(),
		"user_id":   u.ID.String(),
		"wallet":    walletAddr,
		"tx_hash":   ev.TxHash,
	})
	return nil
}

// onContributionReceived handles ContributionReceived(circle_id, member, amount, round).
// Inserts a confirmed Contribution row linked to the on-chain transaction.
func (p *EventProcessor) onContributionReceived(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	walletAddr := payloadStr(ev.Payload, "member")
	amount := payloadFloat(ev.Payload, "amount")
	round := payloadInt(ev.Payload, "round")

	c, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("contract_id", contractID).Msg("ContributionReceived: circle not found")
			return nil
		}
		return fmt.Errorf("onContributionReceived find circle: %w", err)
	}

	u, err := p.userRepo.FindByWalletAddress(ctx, walletAddr)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("wallet", walletAddr).Msg("ContributionReceived: user not found")
			return nil
		}
		return fmt.Errorf("onContributionReceived find user: %w", err)
	}

	contrib := &contribution.Contribution{
		ID:          uuid.New(),
		CircleID:    c.ID,
		UserID:      u.ID,
		RoundNumber: round,
		Amount:      amount,
		TxnHash:     sql.NullString{String: ev.TxHash, Valid: true},
		Status:      contribution.StatusConfirmed,
		OnTime:      true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := p.contribRepo.Create(ctx, contrib); err != nil {
		if errors.Is(err, apperrors.ErrConflict) {
			// Already recorded by an earlier pass over this ledger range;
			// skip the counter update so totals are not counted twice.
			log.Debug().Str("tx_hash", ev.TxHash).Msg("ContributionReceived: already recorded")
			return nil
		}
		return fmt.Errorf("onContributionReceived create: %w", err)
	}

	// Update circle's total contributions counter.
	c.TotalContributions += amount
	if err := p.circleRepo.Update(ctx, c); err != nil {
		log.Warn().Err(err).Msg("ContributionReceived: updating circle totals")
	}

	log.Info().
		Str("circle_id", c.ID.String()).
		Str("user_id", u.ID.String()).
		Float64("amount", amount).
		Int("round", round).
		Msg("ContributionReceived: contribution persisted")

	p.Broadcast(ctx, c.ID.String(), "contribution.confirmed", map[string]any{
		"circle_id":       c.ID.String(),
		"user_id":         u.ID.String(),
		"amount":          amount,
		"round":           round,
		"contribution_id": contrib.ID.String(),
		"tx_hash":         ev.TxHash,
	})
	return nil
}

// onPayoutExecuted handles PayoutExecuted(circle_id, recipient, amount, round, payout_type).
// Inserts a Payout row and advances the circle's CurrentRound counter.
func (p *EventProcessor) onPayoutExecuted(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	recipientWallet := payloadStr(ev.Payload, "recipient")
	amount := payloadFloat(ev.Payload, "amount")
	round := payloadInt(ev.Payload, "round")
	payoutTypeStr := payloadStr(ev.Payload, "payout_type")

	c, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("contract_id", contractID).Msg("PayoutExecuted: circle not found")
			return nil
		}
		return fmt.Errorf("onPayoutExecuted find circle: %w", err)
	}

	recipient, err := p.userRepo.FindByWalletAddress(ctx, recipientWallet)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("wallet", recipientWallet).Msg("PayoutExecuted: recipient not found")
			return nil
		}
		return fmt.Errorf("onPayoutExecuted find recipient: %w", err)
	}

	pt := payout.PayoutTypeRandom
	switch payoutTypeStr {
	case "fixed":
		pt = payout.PayoutTypeFixed
	case "auction":
		pt = payout.PayoutTypeAuction
	case "vote":
		pt = payout.PayoutTypeVote
	}

	p2 := &payout.Payout{
		ID:          uuid.New(),
		CircleID:    c.ID,
		RecipientID: recipient.ID,
		RoundNumber: round,
		Amount:      amount,
		TxnHash:     sql.NullString{String: ev.TxHash, Valid: true},
		PayoutType:  pt,
		CreatedAt:   time.Now().UTC(),
	}
	if err := p.payoutRepo.Create(ctx, p2); err != nil {
		if errors.Is(err, apperrors.ErrConflict) {
			log.Debug().Str("tx_hash", ev.TxHash).Msg("PayoutExecuted: already recorded")
			return nil
		}
		return fmt.Errorf("onPayoutExecuted create payout: %w", err)
	}

	// Advance current round.
	c.CurrentRound = round + 1
	if err := p.circleRepo.Update(ctx, c); err != nil {
		log.Warn().Err(err).Msg("PayoutExecuted: advancing circle round")
	}

	log.Info().
		Str("circle_id", c.ID.String()).
		Str("recipient_id", recipient.ID.String()).
		Float64("amount", amount).
		Int("round", round).
		Msg("PayoutExecuted: payout persisted")

	p.Broadcast(ctx, c.ID.String(), "payout.received", map[string]any{
		"circle_id":    c.ID.String(),
		"recipient_id": recipient.ID.String(),
		"amount":       amount,
		"round":        round,
		"payout_id":    p2.ID.String(),
		"tx_hash":      ev.TxHash,
	})
	return nil
}

// onLateReported handles LateReported(circle_id, member, penalty_amount, strikes).
// Updates the contribution status to late and marks it not on-time.
func (p *EventProcessor) onLateReported(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	walletAddr := payloadStr(ev.Payload, "member")
	penaltyAmount := payloadFloat(ev.Payload, "penalty_amount")
	strikes := payloadInt(ev.Payload, "strikes")

	c, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("contract_id", contractID).Msg("LateReported: circle not found")
			return nil
		}
		return fmt.Errorf("onLateReported find circle: %w", err)
	}

	u, err := p.userRepo.FindByWalletAddress(ctx, walletAddr)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("wallet", walletAddr).Msg("LateReported: user not found")
			return nil
		}
		return fmt.Errorf("onLateReported find user: %w", err)
	}

	log.Warn().
		Str("circle_id", c.ID.String()).
		Str("user_id", u.ID.String()).
		Float64("penalty", penaltyAmount).
		Int("strikes", strikes).
		Msg("LateReported: late payment recorded")

	p.Broadcast(ctx, c.ID.String(), "contribution.late", map[string]any{
		"circle_id":      c.ID.String(),
		"user_id":        u.ID.String(),
		"penalty_amount": penaltyAmount,
		"strikes":        strikes,
		"tx_hash":        ev.TxHash,
	})
	return nil
}

// onMemberExited handles MemberExited(circle_id, member, penalty).
// Updates the circle member status to exited.
func (p *EventProcessor) onMemberExited(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	walletAddr := payloadStr(ev.Payload, "member")
	penalty := payloadFloat(ev.Payload, "penalty")

	c, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("contract_id", contractID).Msg("MemberExited: circle not found")
			return nil
		}
		return fmt.Errorf("onMemberExited find circle: %w", err)
	}

	u, err := p.userRepo.FindByWalletAddress(ctx, walletAddr)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("wallet", walletAddr).Msg("MemberExited: user not found")
			return nil
		}
		return fmt.Errorf("onMemberExited find user: %w", err)
	}

	if err := p.circleRepo.UpdateMemberStatus(ctx, c.ID, u.ID, circle.MemberStatusExited); err != nil {
		return fmt.Errorf("onMemberExited update status: %w", err)
	}

	log.Info().
		Str("circle_id", c.ID.String()).
		Str("user_id", u.ID.String()).
		Float64("penalty", penalty).
		Msg("MemberExited: member status updated to exited")

	p.Broadcast(ctx, c.ID.String(), "member.exited", map[string]any{
		"circle_id": c.ID.String(),
		"user_id":   u.ID.String(),
		"penalty":   penalty,
		"tx_hash":   ev.TxHash,
	})
	return nil
}

// onDefaultRecorded handles DefaultRecorded(circle_id, member).
// Triggers an on-chain default reputation penalty via reputation score update.
func (p *EventProcessor) onDefaultRecorded(ctx context.Context, ev *ContractEvent) error {
	walletAddr := payloadStr(ev.Payload, "member")
	contractID := payloadStr(ev.Payload, "circle_id")

	u, err := p.userRepo.FindByWalletAddress(ctx, walletAddr)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("wallet", walletAddr).Msg("DefaultRecorded: user not found")
			return nil
		}
		return fmt.Errorf("onDefaultRecorded find user: %w", err)
	}

	// Retrieve current reputation snapshot and recalculate with a penalty.
	existing, err := p.reputationRepo.GetByUser(ctx, u.ID)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("onDefaultRecorded get reputation: %w", err)
	}

	currentScore := 0
	if existing != nil {
		currentScore = existing.Score
	}

	// Apply default penalty (−50 points, floored at 0).
	newScore := currentScore - 50
	if newScore < 0 {
		newScore = 0
	}
	level := reputationLevel(newScore)

	snapshot := &reputation.ReputationSnapshot{
		UserID:    u.ID,
		Score:     newScore,
		Level:     level,
		Month:     time.Now().UTC(),
		CreatedAt: time.Now().UTC(),
	}
	if err := p.reputationRepo.SaveSnapshot(ctx, snapshot); err != nil {
		return fmt.Errorf("onDefaultRecorded save snapshot: %w", err)
	}

	if err := p.userRepo.UpdateMoiScore(ctx, u.ID, newScore); err != nil {
		log.Warn().Err(err).Msg("DefaultRecorded: updating user moi_score")
	}

	log.Warn().
		Str("user_id", u.ID.String()).
		Str("contract_id", contractID).
		Int("new_score", newScore).
		Msg("DefaultRecorded: reputation penalty applied")

	p.Broadcast(ctx, contractID, "reputation.updated", map[string]any{
		"user_id":   u.ID.String(),
		"new_score": newScore,
		"level":     level,
		"reason":    "default",
		"tx_hash":   ev.TxHash,
	})
	return nil
}

// onCircleCompleted handles CircleCompleted(circle_id, total_contributions).
// Marks the circle as completed and sets the end date.
func (p *EventProcessor) onCircleCompleted(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	totalContribs := payloadFloat(ev.Payload, "total_contributions")

	c, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("contract_id", contractID).Msg("CircleCompleted: circle not found")
			return nil
		}
		return fmt.Errorf("onCircleCompleted find circle: %w", err)
	}

	now := time.Now().UTC()
	c.Status = circle.CircleStatusCompleted
	c.TotalContributions = totalContribs
	c.EndDate = sql.NullTime{Time: now, Valid: true}
	if err := p.circleRepo.Update(ctx, c); err != nil {
		return fmt.Errorf("onCircleCompleted update: %w", err)
	}

	log.Info().
		Str("circle_id", c.ID.String()).
		Float64("total_contributions", totalContribs).
		Msg("CircleCompleted: circle marked completed")

	p.Broadcast(ctx, c.ID.String(), "circle.completed", map[string]any{
		"circle_id":           c.ID.String(),
		"total_contributions": totalContribs,
		"tx_hash":             ev.TxHash,
	})
	return nil
}

// onAuctionBid handles AuctionBid(circle_id, bidder, discount_bips, round).
// No persistent table exists yet — logs and broadcasts only.
// TODO: Persist to auction_bids table (follow-up issue).
func (p *EventProcessor) onAuctionBid(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	bidder := payloadStr(ev.Payload, "bidder")
	discountBips := payloadInt(ev.Payload, "discount_bips")
	round := payloadInt(ev.Payload, "round")

	log.Info().
		Str("contract_id", contractID).
		Str("bidder", bidder).
		Int("discount_bips", discountBips).
		Int("round", round).
		Msg("AuctionBid: bid received")

	p.Broadcast(ctx, contractID, "auction.bid", map[string]any{
		"contract_id":   contractID,
		"bidder":        bidder,
		"discount_bips": discountBips,
		"round":         round,
		"tx_hash":       ev.TxHash,
	})
	return nil
}

// onVoteCast handles VoteCast(circle_id, voter, vote_for, round).
// No persistent table exists yet — logs and broadcasts only.
// TODO: Persist to circle_votes table (follow-up issue).
func (p *EventProcessor) onVoteCast(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	voter := payloadStr(ev.Payload, "voter")
	voteFor := payloadStr(ev.Payload, "vote_for")
	round := payloadInt(ev.Payload, "round")

	log.Info().
		Str("contract_id", contractID).
		Str("voter", voter).
		Str("vote_for", voteFor).
		Int("round", round).
		Msg("VoteCast: vote recorded")

	p.Broadcast(ctx, contractID, "vote.cast", map[string]any{
		"contract_id": contractID,
		"voter":       voter,
		"vote_for":    voteFor,
		"round":       round,
		"tx_hash":     ev.TxHash,
	})
	return nil
}

// onDisputeRaised handles DisputeRaised(circle_id, member, evidence_hash).
// Transitions the circle to "disputed" status, freezing payouts until resolved.
func (p *EventProcessor) onDisputeRaised(ctx context.Context, ev *ContractEvent) error {
	contractID := payloadStr(ev.Payload, "circle_id")
	member := payloadStr(ev.Payload, "member")
	evidenceHash := payloadStr(ev.Payload, "evidence_hash")

	c, err := p.circleRepo.FindByContractID(ctx, contractID)
	if err != nil {
		if isNotFound(err) {
			log.Warn().Str("contract_id", contractID).Msg("DisputeRaised: circle not found")
			return nil
		}
		return fmt.Errorf("onDisputeRaised find circle: %w", err)
	}

	// Set circle status to disputed to freeze further payouts.
	c.Status = "disputed"
	if err := p.circleRepo.Update(ctx, c); err != nil {
		return fmt.Errorf("onDisputeRaised update circle status: %w", err)
	}

	log.Warn().
		Str("circle_id", c.ID.String()).
		Str("member_wallet", member).
		Str("evidence_hash", evidenceHash).
		Msg("DisputeRaised: circle frozen")

	p.Broadcast(ctx, c.ID.String(), "dispute.raised", map[string]any{
		"circle_id":     c.ID.String(),
		"member_wallet": member,
		"evidence_hash": evidenceHash,
		"tx_hash":       ev.TxHash,
	})
	return nil
}

// onFeeDeposited handles FeeDeposited events from the Treasury contract.
// No persistent table exists yet — logs and broadcasts only.
// TODO: Persist to treasury_fees table (follow-up issue).
func (p *EventProcessor) onFeeDeposited(ctx context.Context, ev *ContractEvent) error {
	circleID := payloadStr(ev.Payload, "circle_id")
	amount := payloadFloat(ev.Payload, "amount")

	log.Info().
		Str("circle_id", circleID).
		Float64("amount", amount).
		Str("tx_hash", ev.TxHash).
		Msg("FeeDeposited: protocol fee collected")

	p.Broadcast(ctx, circleID, "fee.deposited", map[string]any{
		"circle_id": circleID,
		"amount":    amount,
		"tx_hash":   ev.TxHash,
		"ledger":    ev.Ledger,
	})
	return nil
}

// eventRecorded reports whether the event is already in the audit log, which
// means an earlier pass fully applied it. Lookup failures are treated as
// "not recorded": the unique constraints on the domain tables still keep a
// replay harmless.
func (p *EventProcessor) eventRecorded(ctx context.Context, ev *ContractEvent) bool {
	if p.db == nil {
		return false
	}
	var exists bool
	err := p.db.GetContext(ctx, &exists, `
		SELECT EXISTS (
			SELECT 1 FROM contract_events
			WHERE tx_hash = $1 AND contract_id = $2 AND event_type = $3
		)`, ev.TxHash, ev.ContractID, ev.EventType)
	if err != nil {
		log.Warn().Err(err).Str("event_type", ev.EventType).Msg("checking contract event log")
		return false
	}
	return exists
}

// ---------------------------------------------------------------------------
// persistContractEvent appends a processed event to the audit log.
// ---------------------------------------------------------------------------

func (p *EventProcessor) persistContractEvent(ctx context.Context, ev *ContractEvent) error {
	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("marshaling event payload: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO contract_events (tx_hash, ledger, contract_id, event_type, payload, processed_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING`,
		ev.TxHash, ev.Ledger, ev.ContractID, ev.EventType, payloadJSON, time.Now().UTC(),
	)
	return err
}

// ---------------------------------------------------------------------------
// Broadcast sends a real-time update via WebSocket and publishes the event
// to RabbitMQ for async workers (notifications, webhooks, analytics).
// ---------------------------------------------------------------------------

func (p *EventProcessor) Broadcast(ctx context.Context, circleID string, eventType string, payload any) {
	// Real-time WebSocket broadcast to subscribed clients
	if p.wsBroadcast != nil {
		p.wsBroadcast(circleID, payload)
	}

	// Async event publishing to RabbitMQ
	data, err := json.Marshal(map[string]any{
		"type":      eventType,
		"circleId":  circleID,
		"payload":   payload,
		"timestamp": time.Now().UTC(),
	})
	if err != nil {
		log.Warn().Err(err).Msg("marshaling event for rabbitmq")
		return
	}

	if p.rmqClient != nil {
		if err := p.rmqClient.Publish("moistello.events", "circle."+eventType, data); err != nil {
			log.Warn().Err(err).Msg("publishing to rabbitmq")
		}
	}
}

// ---------------------------------------------------------------------------
// Payload extraction helpers — safe, nil-tolerant accessors.
// ---------------------------------------------------------------------------

func payloadStr(p map[string]any, key string) string {
	if p == nil {
		return ""
	}
	v, ok := p[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func payloadFloat(p map[string]any, key string) float64 {
	if p == nil {
		return 0
	}
	v, ok := p[key]
	if !ok {
		return 0
	}
	switch f := v.(type) {
	case float64:
		return f
	case float32:
		return float64(f)
	case int:
		return float64(f)
	case int64:
		return float64(f)
	case uint64:
		return float64(f)
	}
	return 0
}

func payloadInt(p map[string]any, key string) int {
	if p == nil {
		return 0
	}
	v, ok := p[key]
	if !ok {
		return 0
	}
	switch i := v.(type) {
	case int:
		return i
	case int32:
		return int(i)
	case int64:
		return int(i)
	case uint32:
		return int(i)
	case float64:
		return int(i)
	}
	return 0
}

// isNotFound returns true for "not found" sentinel errors used by repositories.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "not found" || err == sql.ErrNoRows
}

// reputationLevel maps a MoiScore to the human-readable tier string.
func reputationLevel(score int) string {
	switch {
	case score > 800:
		return "Diamond"
	case score > 600:
		return "Platinum"
	case score > 400:
		return "Gold"
	case score > 200:
		return "Silver"
	default:
		return "Bronze"
	}
}
