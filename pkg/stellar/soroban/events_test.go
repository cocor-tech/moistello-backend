package soroban_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar/soroban"
)

func TestEventsClient_GetEvents(t *testing.T) {
	t.Run("success returns events and cursor", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"events": []map[string]any{
						{
							"type":             "contract",
							"contract_id":      "CA123",
							"topics":           []string{"AAAA"},
							"data":             "AQID",
							"transaction_hash": "tx-hash-1",
							"ledger":           100,
							"ledger_closed_at": "2026-01-01T00:00:00Z",
						},
					},
					"latest_ledger": 105,
					"cursor":        "cursor-1",
				},
			})
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		resp, err := client.GetEvents(context.Background(), soroban.EventFilter{
			ContractIDs: []string{"CA123"},
			StartLedger: 100,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(105), resp.LatestLedger)
		assert.Equal(t, "cursor-1", resp.Cursor)
		require.Len(t, resp.Events, 1)
		assert.Equal(t, "CA123", resp.Events[0].ContractID)
	})

	t.Run("http status error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		_, err := client.GetEvents(context.Background(), soroban.EventFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "events error 500: internal error")
	})

	t.Run("rpc error response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"error": map[string]any{
					"code":    -32600,
					"message": "Invalid request parameters",
				},
			})
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		_, err := client.GetEvents(context.Background(), soroban.EventFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rpc error -32600: Invalid request parameters")
	})

	t.Run("malformed json response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{invalid-json"))
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		_, err := client.GetEvents(context.Background(), soroban.EventFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decoding events response")
	})
}

func TestEventsClient_GetAllEvents(t *testing.T) {
	t.Run("single page less than limit", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"events": []map[string]any{
						{"type": "contract", "contract_id": "C1", "ledger": 10},
					},
					"latest_ledger": 10,
				},
			})
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		events, err := client.GetAllEvents(context.Background(), []string{"C1"}, 1)
		require.NoError(t, err)
		assert.Len(t, events, 1)
	})

	t.Run("multi-page with cursor pagination and fallback cursor", func(t *testing.T) {
		callCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			if callCount == 1 {
				// 100 events, no explicit cursor, triggers fallback cursor generation
				events := make([]map[string]any, 100)
				for i := 0; i < 100; i++ {
					events[i] = map[string]any{
						"type":        "contract",
						"contract_id": "C1",
						"ledger":      int64(10 + i),
						"invocations": 1,
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"events":        events,
						"latest_ledger": 200,
						"cursor":        "",
					},
				})
			} else {
				// Second page has 5 events (< 100)
				events := make([]map[string]any, 5)
				for i := 0; i < 5; i++ {
					events[i] = map[string]any{
						"type":        "contract",
						"contract_id": "C1",
						"ledger":      int64(120 + i),
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"events":        events,
						"latest_ledger": 200,
					},
				})
			}
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		events, err := client.GetAllEvents(context.Background(), []string{"C1"}, 1)
		require.NoError(t, err)
		assert.Len(t, events, 105)
		assert.Equal(t, 2, callCount)
	})

	t.Run("error propagating from GetEvents", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		_, err := client.GetAllEvents(context.Background(), []string{"C1"}, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetching events page")
	})
}

func TestEventsClient_GetEventsByTopic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"events": []map[string]any{
					{"type": "contract", "contract_id": "C1", "topics": []string{"topic1"}},
				},
				"latest_ledger": 50,
			},
		})
	}))
	defer srv.Close()

	client := soroban.NewEventsClient(srv.URL)
	events, err := client.GetEventsByTopic(context.Background(), "C1", [][]string{{"topic1"}}, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "C1", events[0].ContractID)

	// Error case
	clientBad := soroban.NewEventsClient("http://127.0.0.1:0")
	_, err = clientBad.GetEventsByTopic(context.Background(), "C1", nil, 1)
	require.Error(t, err)
}

func TestEventsClient_WatchEvents(t *testing.T) {
	t.Run("context cancelled immediately", func(t *testing.T) {
		client := soroban.NewEventsClient("http://example.com")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		eventCh := make(chan soroban.ContractEvent, 5)
		errCh := make(chan error, 5)

		client.WatchEvents(ctx, []string{"C1"}, 1, 10*time.Millisecond, eventCh, errCh)

		select {
		case err := <-errCh:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("expected error on errCh")
		}
	})

	t.Run("ticker receives event and stops on cancel", func(t *testing.T) {
		callCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"events": []map[string]any{
						{
							"type":        "contract",
							"contract_id": "C1",
							"ledger":      int64(100 + callCount),
						},
					},
					"latest_ledger": 200,
				},
			})
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		eventCh := make(chan soroban.ContractEvent, 10)
		errCh := make(chan error, 10)

		client.WatchEvents(ctx, []string{"C1"}, 1, 10*time.Millisecond, eventCh, errCh)

		var received []soroban.ContractEvent
		for ev := range eventCh {
			received = append(received, ev)
		}
		assert.NotEmpty(t, received)
	})

	t.Run("polling error is forwarded to errCh", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		client := soroban.NewEventsClient(srv.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		eventCh := make(chan soroban.ContractEvent, 10)
		errCh := make(chan error, 10)

		client.WatchEvents(ctx, []string{"C1"}, 1, 5*time.Millisecond, eventCh, errCh)

		var errs []error
		for err := range errCh {
			errs = append(errs, err)
		}
		assert.NotEmpty(t, errs)
	})
}

func TestParseEventData(t *testing.T) {
	t.Run("empty event data returns error", func(t *testing.T) {
		_, err := soroban.ParseEventData(soroban.ContractEvent{Data: ""})
		require.EqualError(t, err, "empty event data")
	})

	sampleJSON := `{"circleId":"circle-123","amount":500}`

	t.Run("StdEncoding base64 decodes successfully", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte(sampleJSON))
		data, err := soroban.ParseEventData(soroban.ContractEvent{Data: encoded})
		require.NoError(t, err)
		assert.Equal(t, "circle-123", data["circleId"])
		assert.Equal(t, float64(500), data["amount"])
	})

	t.Run("RawStdEncoding base64 decodes successfully", func(t *testing.T) {
		encoded := base64.RawStdEncoding.EncodeToString([]byte(sampleJSON))
		data, err := soroban.ParseEventData(soroban.ContractEvent{Data: encoded})
		require.NoError(t, err)
		assert.Equal(t, "circle-123", data["circleId"])
	})

	t.Run("URLEncoding base64 decodes successfully", func(t *testing.T) {
		encoded := base64.URLEncoding.EncodeToString([]byte(sampleJSON))
		data, err := soroban.ParseEventData(soroban.ContractEvent{Data: encoded})
		require.NoError(t, err)
		assert.Equal(t, "circle-123", data["circleId"])
	})

	t.Run("RawURLEncoding base64 decodes successfully", func(t *testing.T) {
		encoded := base64.RawURLEncoding.EncodeToString([]byte(sampleJSON))
		data, err := soroban.ParseEventData(soroban.ContractEvent{Data: encoded})
		require.NoError(t, err)
		assert.Equal(t, "circle-123", data["circleId"])
	})

	t.Run("invalid base64 returns decoding error", func(t *testing.T) {
		_, err := soroban.ParseEventData(soroban.ContractEvent{Data: "!!!not-valid-base64!!!"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decoding event data")
	})

	t.Run("valid base64 but invalid JSON returns unmarshaling error", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("not a json object"))
		_, err := soroban.ParseEventData(soroban.ContractEvent{Data: encoded})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling event data")
	})
}
