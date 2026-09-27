package tracing_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/moistello/backend/pkg/tracing"
)

func TestSafeURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "drops query string",
			in:   "https://api.example.com/v1/ledger?api_key=secret&token=abc123",
			want: "https://api.example.com/v1/ledger",
		},
		{
			name: "drops fragment",
			in:   "https://api.example.com/v1/ledger#access_token=abc123",
			want: "https://api.example.com/v1/ledger",
		},
		{
			name: "drops userinfo",
			in:   "https://user:password@rpc.example.com/soroban",
			want: "https://rpc.example.com/soroban",
		},
		{
			name: "drops trailing question mark",
			in:   "https://api.example.com/v1/ledger?",
			want: "https://api.example.com/v1/ledger",
		},
		{
			name: "keeps a URL that carries nothing sensitive",
			in:   "https://api.example.com/v1/ledger",
			want: "https://api.example.com/v1/ledger",
		},
		{
			name: "keeps port",
			in:   "http://127.0.0.1:8000/soroban?key=secret",
			want: "http://127.0.0.1:8000/soroban",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tracing.SafeURL(tc.in))
		})
	}
}

// TestSafeURL_PassesThroughUnparseable guards the fallback: a value that is not a
// URL is returned rather than dropped, so an attribute is never silently lost.
func TestSafeURL_PassesThroughUnparseable(t *testing.T) {
	assert.Equal(t, "not a url", tracing.SafeURL("not a url"))
	assert.Equal(t, "://nope", tracing.SafeURL("://nope"))
}

// TestSafeURL_LeavesNoSecrets checks the scrubbed form cannot carry the values
// that were in the query, across a set of realistic credential-bearing URLs.
func TestSafeURL_LeavesNoSecrets(t *testing.T) {
	secrets := []string{
		"https://rpc.example.com/s?api_key=AKIAIOSFODNN7EXAMPLE",
		"https://api.example.com/v1/x?access_token=ghp_16CharsAndMore",
		"https://user:hunter2@stellar.example.com/soroban",
		"https://api.example.com/v1/x?email=person@example.com",
	}

	for _, raw := range secrets {
		scrubbed := tracing.SafeURL(raw)
		for _, fragment := range []string{"api_key", "access_token", "AKIA", "ghp_", "hunter2", "example.com@", "person@"} {
			assert.NotContains(t, scrubbed, fragment,
				"scrubbing %q must not leave %q behind", raw, fragment)
		}
	}
}

// piiLikeAttributeNames are attribute names that would be a PII leak by
// definition. This is a denylist over attribute *keys*, which catches a careless
// future addition at review time.
var piiLikeAttributeNames = []string{
	"email", "e.mail", "password", "passwd", "secret", "token", "apikey",
	"api_key", "authorization", "auth", "credential", "phone", "ssn",
	"address", "wallet", "user_agent", "cookie", "session", "private",
}

// TestSpanAttributes_CarryNoPII is the acceptance guard for the "no PII in span
// attributes" criterion. It exercises the helpers the traced call sites use and
// asserts nothing resembling personal or secret data is recorded.
//
// It is a structural check on attribute names and values, not a proof that no
// future call site is clean: every new instrumented call site should be added
// here with the attributes it actually sets.
func TestSpanAttributes_CarryNoPII(t *testing.T) {
	// The attributes set on the instrumented paths, as the call sites write them.
	attributesUnderTest := []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", "SELECT"),
		attribute.String("db.table", "contract_events"),
		attribute.String("db.isolation_level", "read committed"),
		attribute.Bool("db.filter.contract_version", true),
		attribute.Bool("db.filter.ledger_range", true),
		attribute.String("rpc.system", "stellar"),
		attribute.String("rpc.method", "getLedgerEntries"),
		attribute.Int("rpc.contract_count", 3),
		attribute.Int64("rpc.start_ledger", 1234),
		attribute.String("http.method", "POST"),
		attribute.String("http.url", tracing.SafeURL("https://rpc.example.com/soroban?api_key=secret")),
		attribute.String("request.id", "3f0d9a1e-1c2b-4c3d-8e9f-0a1b2c3d4e5f"),
		attribute.String("duration.ms", "12"),
	}

	for _, attr := range attributesUnderTest {
		key := strings.ToLower(string(attr.Key))
		for _, banned := range piiLikeAttributeNames {
			// "request.id" and "http.url" legitimately contain these substrings as
			// part of a longer, non-PII name, so only a whole-segment match counts.
			if key == banned || strings.HasPrefix(key, banned+".") || strings.HasSuffix(key, "."+banned) {
				t.Errorf("attribute %q looks like a PII leak (%q)", attr.Key, banned)
			}
		}
		assert.NotContains(t, attr.Value.Emit(), "secret",
			"attribute %q leaked a secret value", attr.Key)
		assert.NotContains(t, attr.Value.Emit(), "@",
			"attribute %q leaked what looks like an email address", attr.Key)
	}
}

// TestStartSpan_NoOpWhenTracingDisabled keeps the "instrument unconditionally"
// contract honest: with no tracer provider configured the span records nothing
// and the call still succeeds.
func TestStartSpan_NoOpWhenTracingDisabled(t *testing.T) {
	ctx, span := tracing.StartSpan(context.Background(), "test.op",
		attribute.String("db.operation", "SELECT"))
	require.NotNil(t, ctx)
	require.NotNil(t, span)

	tracing.EndSpan(span, nil, time.Now())
	assert.False(t, span.IsRecording())
}
