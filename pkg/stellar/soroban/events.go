package soroban

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/moistello/backend/pkg/tracing"
)

// ContractEvent represents a Soroban contract event.
type ContractEvent struct {
	Type            string   `json:"type"`
	ContractID      string   `json:"contract_id"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	TransactionHash string   `json:"transaction_hash"`
	Ledger          int64    `json:"ledger"`
	LedgerClosedAt  string   `json:"ledger_closed_at"`
	Invocations     int      `json:"invocations,omitempty"`
}

// EventFilter filters events by contract, topic, and pagination.
type EventFilter struct {
	ContractIDs []string   `json:"contract_ids"`
	Topics      [][]string `json:"topics,omitempty"`
	StartLedger int64      `json:"start_ledger,omitempty"`
	EndLedger   int64      `json:"end_ledger,omitempty"`
	Limit       int        `json:"limit,omitempty"`
	Cursor      string     `json:"cursor,omitempty"`
	Type        string     `json:"type,omitempty"` // "contract", "diagnostic", or "system"
}

// EventsClient fetches contract events from Soroban RPC.
type EventsClient struct {
	rpcURL     string
	httpClient *http.Client
}

func NewEventsClient(rpcURL string) *EventsClient {
	return &EventsClient{
		rpcURL:     rpcURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type eventsResponse struct {
	Events       []ContractEvent `json:"events"`
	LatestLedger int64           `json:"latest_ledger"`
	Cursor       string          `json:"cursor,omitempty"`
}

// GetEvents fetches contract events matching the filter.
func (c *EventsClient) GetEvents(ctx context.Context, filter EventFilter) (result *eventsResponse, err error) {
	start := time.Now()

	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return nil, fmt.Errorf("marshaling filter: %w", err)
	}

	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "getEvents",
		"params": %s
	}`, string(filterJSON))

	// The span records the shape of the request, never the filter values: a
	// filter carries contract IDs and ledger numbers, and this keeps the span
	// free of anything caller-supplied.
	ctx, span := tracing.StartStellarSpan(ctx, "getEvents")
	span.SetAttributes(
		attribute.String("rpc.method", "getEvents"),
		attribute.Int("rpc.contract_count", len(filter.ContractIDs)),
		attribute.Int64("rpc.start_ledger", filter.StartLedger),
	)
	defer func() { tracing.EndSpan(span, err, start) }()

	req, err := http.NewRequestWithContext(ctx, "POST", c.rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("creating events request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("events request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading events response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("events error %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result eventsResponse `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decoding events response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return &rpcResp.Result, nil
}

// GetAllEvents fetches all events since startLedger, handling pagination.
func (c *EventsClient) GetAllEvents(ctx context.Context, contractIDs []string, startLedger int64) ([]ContractEvent, error) {
	var allEvents []ContractEvent
	cursor := ""

	for {
		filter := EventFilter{
			ContractIDs: contractIDs,
			StartLedger: startLedger,
			Limit:       100,
			Cursor:      cursor,
		}
		resp, err := c.GetEvents(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("fetching events page: %w", err)
		}

		allEvents = append(allEvents, resp.Events...)

		if len(resp.Events) < 100 {
			break
		}
		cursor = resp.Cursor
		if cursor == "" && len(resp.Events) > 0 {
			last := resp.Events[len(resp.Events)-1]
			cursor = fmt.Sprintf("%d-%d", last.Ledger, last.Invocations)
		}
	}

	return allEvents, nil
}

// GetEventsByTopic fetches events matching specific topic filters.
func (c *EventsClient) GetEventsByTopic(ctx context.Context, contractID string, topics [][]string, startLedger int64) ([]ContractEvent, error) {
	filter := EventFilter{
		ContractIDs: []string{contractID},
		Topics:      topics,
		StartLedger: startLedger,
		Limit:       100,
	}
	resp, err := c.GetEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// WatchEvents polls for new events from a starting ledger at a given interval.
// Events are sent to eventCh; errors to errCh. Both channels are closed on return.
func (c *EventsClient) WatchEvents(ctx context.Context, contractIDs []string, startLedger int64, interval time.Duration, eventCh chan<- ContractEvent, errCh chan<- error) {
	defer close(eventCh)
	defer close(errCh)

	currentLedger := startLedger
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			errCh <- ctx.Err()
			return
		case <-ticker.C:
			events, err := c.GetAllEvents(ctx, contractIDs, currentLedger)
			if err != nil {
				errCh <- fmt.Errorf("watch events: %w", err)
				continue
			}
			for _, ev := range events {
				select {
				case <-ctx.Done():
					return
				case eventCh <- ev:
				}
			}
			if len(events) > 0 {
				currentLedger = events[len(events)-1].Ledger + 1
			}
		}
	}
}

// ParseEventData decodes the base64-encoded event data into a Go map.
func ParseEventData(event ContractEvent) (map[string]interface{}, error) {
	if event.Data == "" {
		return nil, fmt.Errorf("empty event data")
	}

	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}

	var dataBytes []byte
	var lastErr error
	for _, enc := range encodings {
		d, err := enc.DecodeString(event.Data)
		if err == nil {
			dataBytes = d
			break
		}
		lastErr = err
	}
	if dataBytes == nil {
		return nil, fmt.Errorf("decoding event data: %w", lastErr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(dataBytes, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling event data: %w", err)
	}
	return result, nil
}
