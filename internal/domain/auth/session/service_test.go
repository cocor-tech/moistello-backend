package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRefreshTTL = 24 * time.Hour
	testAccessTTL  = 15 * time.Minute
)

type jwtCall struct {
	userID uuid.UUID
	wallet string
	role   string
	ttl    time.Duration
}

// stubJWT records token requests and mints a distinct token per call.
type stubJWT struct {
	err   error
	calls []jwtCall
}

func (s *stubJWT) GenerateToken(_ context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	s.calls = append(s.calls, jwtCall{userID: userID, wallet: walletAddress, role: role, ttl: ttl})
	if s.err != nil {
		return "", s.err
	}
	return fmt.Sprintf("access-jwt-%d", len(s.calls)), nil
}

func newTestService(t *testing.T) (Service, *miniredis.Miniredis, *stubJWT) {
	t.Helper()
	mr, client := newTestClient(t)
	jwt := &stubJWT{}
	return NewService(client, testRefreshTTL, jwt), mr, jwt
}

// unreachableService returns a Service whose Redis server is already gone.
// Retries are off so every call fails immediately.
func unreachableService(t *testing.T) Service {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	mr.Close()
	return NewService(client, testRefreshTTL, &stubJWT{})
}

func testSessionKey(refreshToken string) string {
	return "session:" + sha256Hash(refreshToken)
}

func testIndexKey(userID uuid.UUID) string {
	return "user:sessions:" + userID.String()
}

func mustCreate(t *testing.T, svc Service, userID uuid.UUID, device string) *TokenPair {
	t.Helper()
	pair, err := svc.Create(t.Context(), userID, "user", testAccessTTL, device)
	require.NoError(t, err)
	return pair
}

// ── Create ──

func TestServiceCreate_StoresSessionCSRFTokenAndIndexEntry(t *testing.T) {
	svc, mr, jwt := newTestService(t)
	uid := uuid.New()

	pair, err := svc.Create(t.Context(), uid, "admin", testAccessTTL, "iPhone 15")
	require.NoError(t, err)

	assert.Equal(t, "access-jwt-1", pair.AccessToken)
	assert.Len(t, pair.RefreshToken, 128, "64 random bytes, hex encoded")
	assert.Len(t, pair.CSRFToken, 64, "32 random bytes, hex encoded")
	require.Len(t, jwt.calls, 1)
	assert.Equal(t, jwtCall{userID: uid, role: "admin", ttl: testAccessTTL}, jwt.calls[0])

	hash := sha256Hash(pair.RefreshToken)
	data, err := mr.Get("session:" + hash)
	require.NoError(t, err)
	assert.Regexp(t, "^"+uid.String()+`\|iPhone 15\|\d+\|admin$`, data)
	assert.Equal(t, testRefreshTTL, mr.TTL("session:"+hash))

	csrfKey := fmt.Sprintf("csrf:%x", sha256.Sum256([]byte(pair.AccessToken)))
	csrf, err := mr.Get(csrfKey)
	require.NoError(t, err)
	assert.Equal(t, pair.CSRFToken, csrf)
	assert.Equal(t, testAccessTTL, mr.TTL(csrfKey))

	members, err := mr.SMembers(testIndexKey(uid))
	require.NoError(t, err)
	assert.Equal(t, []string{hash}, members)
	assert.Equal(t, testAccessTTL, mr.TTL(testIndexKey(uid)))
}

func TestServiceCreate_DefaultsAnEmptyRoleToUser(t *testing.T) {
	svc, mr, jwt := newTestService(t)

	pair, err := svc.Create(t.Context(), uuid.New(), "", testAccessTTL, "")
	require.NoError(t, err)

	assert.Equal(t, "user", jwt.calls[0].role)
	data, err := mr.Get(testSessionKey(pair.RefreshToken))
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(data, "|user"), "stored session data should end in the defaulted role: %q", data)
}

func TestServiceCreate_ReturnsAccessTokenFailureWithoutStoringAnything(t *testing.T) {
	svc, mr, jwt := newTestService(t)
	signerDown := errors.New("signer down")
	jwt.err = signerDown

	pair, err := svc.Create(t.Context(), uuid.New(), "user", testAccessTTL, "phone")

	assert.Nil(t, pair)
	require.ErrorIs(t, err, signerDown)
	assert.Contains(t, err.Error(), "generating access token")
	assert.Empty(t, mr.Keys(), "no session state may be written when the access token cannot be minted")
}

func TestServiceCreate_ReportsRedisFailure(t *testing.T) {
	svc := unreachableService(t)

	pair, err := svc.Create(t.Context(), uuid.New(), "user", testAccessTTL, "phone")

	assert.Nil(t, pair)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storing session and CSRF in redis")
}

// ── Validate ──

// Validate parses the whole stored value as the user ID, so these tests seed
// the bare-UUID value it understands rather than the `uid|device|ts|role`
// string Create writes.

func TestServiceValidate_ReturnsTheSessionUser(t *testing.T) {
	svc, mr, _ := newTestService(t)
	uid := uuid.New()
	require.NoError(t, mr.Set(testSessionKey("refresh-token"), uid.String()))

	got, err := svc.Validate(t.Context(), "refresh-token")

	require.NoError(t, err)
	assert.Equal(t, uid, *got)
}

func TestServiceValidate_UnknownTokenIsExpired(t *testing.T) {
	svc, _, _ := newTestService(t)

	got, err := svc.Validate(t.Context(), "never-issued")

	assert.Nil(t, got)
	assert.ErrorIs(t, err, ErrTokenExpired)
	assert.EqualError(t, ErrTokenExpired, "token expired")
}

func TestServiceValidate_BlocklistedUserIsRevokedAndTheSessionDeleted(t *testing.T) {
	svc, mr, _ := newTestService(t)
	uid := uuid.New()
	require.NoError(t, mr.Set(testSessionKey("refresh-token"), uid.String()))
	require.NoError(t, mr.Set("refresh:blocklist:"+uid.String(), "1"))

	got, err := svc.Validate(t.Context(), "refresh-token")

	assert.Nil(t, got)
	assert.EqualError(t, err, "session revoked")
	assert.False(t, mr.Exists(testSessionKey("refresh-token")), "a revoked session must be deleted immediately")
}

func TestServiceValidate_RejectsAStoredUserIDThatIsNotAUUID(t *testing.T) {
	svc, mr, _ := newTestService(t)
	require.NoError(t, mr.Set(testSessionKey("corrupt"), "not-a-uuid|phone|0|user"))

	got, err := svc.Validate(t.Context(), "corrupt")

	assert.Nil(t, got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing session user ID")
}

func TestServiceValidate_ReportsRedisFailure(t *testing.T) {
	got, err := unreachableService(t).Validate(t.Context(), "any")

	assert.Nil(t, got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retrieving session from redis")
}

// ── Refresh ──

func TestServiceRefresh_IssuesANewPairKeepingTheRoleAndGracesTheOldSession(t *testing.T) {
	svc, mr, jwt := newTestService(t)
	uid := uuid.New()
	old, err := svc.Create(t.Context(), uid, "admin", testAccessTTL, "phone")
	require.NoError(t, err)

	fresh, err := svc.Refresh(t.Context(), old.RefreshToken, 5*time.Minute)

	require.NoError(t, err)
	assert.NotEqual(t, old.RefreshToken, fresh.RefreshToken)
	assert.NotEqual(t, old.AccessToken, fresh.AccessToken)
	require.Len(t, jwt.calls, 2)
	assert.Equal(t, jwtCall{userID: uid, role: "admin", ttl: 5 * time.Minute}, jwt.calls[1], "the admin role must survive a refresh")
	assert.Equal(t, 60*time.Second, mr.TTL(testSessionKey(old.RefreshToken)), "the old token only lingers for a grace period")

	data, err := mr.Get(testSessionKey(fresh.RefreshToken))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(data, uid.String()+"|"), "the replacement session belongs to the same user: %q", data)
	assert.True(t, strings.HasSuffix(data, "|admin"), "and carries the same role: %q", data)
}

func TestServiceRefresh_UnknownTokenIsExpired(t *testing.T) {
	svc, _, _ := newTestService(t)

	fresh, err := svc.Refresh(t.Context(), "never-issued", testAccessTTL)

	assert.Nil(t, fresh)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestServiceRefresh_BlocklistedUserIsRevokedAndTheSessionDeleted(t *testing.T) {
	svc, mr, jwt := newTestService(t)
	uid := uuid.New()
	pair := mustCreate(t, svc, uid, "phone")
	require.NoError(t, mr.Set("refresh:blocklist:"+uid.String(), "1"))

	fresh, err := svc.Refresh(t.Context(), pair.RefreshToken, testAccessTTL)

	assert.Nil(t, fresh)
	assert.EqualError(t, err, "session revoked")
	assert.False(t, mr.Exists(testSessionKey(pair.RefreshToken)))
	assert.Len(t, jwt.calls, 1, "no new access token may be minted for a revoked user")
}

func TestServiceRefresh_RejectsAStoredUserIDThatIsNotAUUID(t *testing.T) {
	svc, mr, _ := newTestService(t)
	require.NoError(t, mr.Set(testSessionKey("corrupt"), "not-a-uuid|phone|0|user"))

	fresh, err := svc.Refresh(t.Context(), "corrupt", testAccessTTL)

	assert.Nil(t, fresh)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing session user ID")
}

func TestServiceRefresh_LeavesTheOldSessionValidWhenTheNewOneCannotBeCreated(t *testing.T) {
	svc, mr, jwt := newTestService(t)
	old := mustCreate(t, svc, uuid.New(), "phone")
	jwt.err = errors.New("signer down")

	fresh, err := svc.Refresh(t.Context(), old.RefreshToken, testAccessTTL)

	assert.Nil(t, fresh)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating new session")
	assert.True(t, mr.Exists(testSessionKey(old.RefreshToken)))
	assert.Equal(t, testRefreshTTL, mr.TTL(testSessionKey(old.RefreshToken)), "the old session must not be shortened by a failed refresh")
}

func TestServiceRefresh_ReportsRedisFailure(t *testing.T) {
	fresh, err := unreachableService(t).Refresh(t.Context(), "any", testAccessTTL)

	assert.Nil(t, fresh)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retrieving session from redis")
}

// ── List ──

func TestServiceList_ReportsEachSessionAndFlagsTheCurrentOne(t *testing.T) {
	svc, _, _ := newTestService(t)
	uid := uuid.New()
	phone := mustCreate(t, svc, uid, "phone")
	laptop := mustCreate(t, svc, uid, "laptop")
	currentHash := sha256Hash(laptop.RefreshToken)

	sessions, err := svc.List(t.Context(), uid.String(), currentHash)

	require.NoError(t, err)
	require.Len(t, sessions, 2)
	byID := map[string]SessionInfo{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	assert.Equal(t, "phone", byID[sha256Hash(phone.RefreshToken)].DeviceInfo)
	assert.False(t, byID[sha256Hash(phone.RefreshToken)].IsCurrent)
	assert.Equal(t, "laptop", byID[currentHash].DeviceInfo)
	assert.True(t, byID[currentHash].IsCurrent)
}

func TestServiceList_ParsesLastActiveSkipsStaleIndexEntriesAndToleratesBareData(t *testing.T) {
	svc, mr, _ := newTestService(t)
	uid := uuid.New()
	require.NoError(t, mr.Set("session:timed", uid.String()+"|tablet|1700000000"))
	require.NoError(t, mr.Set("session:bare", uid.String()))
	_, err := mr.SAdd(testIndexKey(uid), "timed", "bare", "expired-with-no-session-key")
	require.NoError(t, err)

	sessions, err := svc.List(t.Context(), uid.String(), "")

	require.NoError(t, err)
	require.Len(t, sessions, 2, "an index entry whose session key has expired must be skipped")
	byID := map[string]SessionInfo{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	assert.Equal(t, "tablet", byID["timed"].DeviceInfo)
	assert.Equal(t, time.Unix(1700000000, 0).Format(time.RFC3339), byID["timed"].LastActive)
	assert.Equal(t, SessionInfo{ID: "bare"}, byID["bare"])
}

func TestServiceList_IsEmptyWhenTheUserHasNoSessions(t *testing.T) {
	svc, _, _ := newTestService(t)

	sessions, err := svc.List(t.Context(), uuid.NewString(), "")

	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestServiceList_ReportsRedisFailure(t *testing.T) {
	sessions, err := unreachableService(t).List(t.Context(), uuid.NewString(), "")

	assert.Nil(t, sessions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing sessions")
}

// ── Revoke / RevokeAll ──

func TestServiceRevoke_RemovesOnlyTheNamedSession(t *testing.T) {
	svc, mr, _ := newTestService(t)
	uid := uuid.New()
	doomed := mustCreate(t, svc, uid, "phone")
	kept := mustCreate(t, svc, uid, "laptop")

	err := svc.Revoke(t.Context(), uid.String(), sha256Hash(doomed.RefreshToken))

	require.NoError(t, err)
	assert.False(t, mr.Exists(testSessionKey(doomed.RefreshToken)))
	assert.True(t, mr.Exists(testSessionKey(kept.RefreshToken)))
	members, err := mr.SMembers(testIndexKey(uid))
	require.NoError(t, err)
	assert.Equal(t, []string{sha256Hash(kept.RefreshToken)}, members)
}

func TestServiceRevoke_IsANoOpForAnUnknownSession(t *testing.T) {
	svc, _, _ := newTestService(t)

	assert.NoError(t, svc.Revoke(t.Context(), uuid.NewString(), "no-such-hash"))
}

func TestServiceRevokeAll_KeepsOnlyTheCurrentSession(t *testing.T) {
	svc, mr, _ := newTestService(t)
	uid := uuid.New()
	first := mustCreate(t, svc, uid, "phone")
	current := mustCreate(t, svc, uid, "laptop")
	third := mustCreate(t, svc, uid, "tablet")
	currentHash := sha256Hash(current.RefreshToken)

	err := svc.RevokeAll(t.Context(), uid.String(), currentHash)

	require.NoError(t, err)
	assert.False(t, mr.Exists(testSessionKey(first.RefreshToken)))
	assert.False(t, mr.Exists(testSessionKey(third.RefreshToken)))
	assert.True(t, mr.Exists(testSessionKey(current.RefreshToken)))
	members, err := mr.SMembers(testIndexKey(uid))
	require.NoError(t, err)
	assert.Equal(t, []string{currentHash}, members)
	assert.Equal(t, testRefreshTTL, mr.TTL(testIndexKey(uid)))
}

func TestServiceRevokeAll_WithNoCurrentSessionRemovesEverything(t *testing.T) {
	svc, mr, _ := newTestService(t)
	uid := uuid.New()
	first := mustCreate(t, svc, uid, "phone")
	second := mustCreate(t, svc, uid, "laptop")

	err := svc.RevokeAll(t.Context(), uid.String(), "")

	require.NoError(t, err)
	assert.False(t, mr.Exists(testSessionKey(first.RefreshToken)))
	assert.False(t, mr.Exists(testSessionKey(second.RefreshToken)))
	assert.False(t, mr.Exists(testIndexKey(uid)))
}

func TestServiceRevokeAll_ReportsRedisFailure(t *testing.T) {
	err := unreachableService(t).RevokeAll(t.Context(), uuid.NewString(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing sessions for revoke")
}

func TestServiceCreate_RevokesPreExistingSessionsWhenRoleChanges(t *testing.T) {
	svc, mr, _ := newTestService(t)
	uid := uuid.New()

	userPhone := mustCreate(t, svc, uid, "phone")
	userLaptop := mustCreate(t, svc, uid, "laptop")
	assert.True(t, mr.Exists(testSessionKey(userPhone.RefreshToken)))
	assert.True(t, mr.Exists(testSessionKey(userLaptop.RefreshToken)))

	// Promoting the user to "admin" must invalidate all pre-existing "user"
	// sessions to prevent session fixation across privilege changes (#459).
	adminPair, err := svc.Create(t.Context(), uid, "admin", testAccessTTL, "desktop")
	require.NoError(t, err)
	assert.NotEqual(t, userPhone.RefreshToken, adminPair.RefreshToken)
	assert.NotEqual(t, userLaptop.RefreshToken, adminPair.RefreshToken)

	assert.False(t, mr.Exists(testSessionKey(userPhone.RefreshToken)), "pre-promotion session must be revoked")
	assert.False(t, mr.Exists(testSessionKey(userLaptop.RefreshToken)), "pre-promotion session must be revoked")
	assert.True(t, mr.Exists(testSessionKey(adminPair.RefreshToken)), "new admin session must be active")

	members, err := mr.SMembers(testIndexKey(uid))
	require.NoError(t, err)
	assert.Equal(t, []string{sha256Hash(adminPair.RefreshToken)}, members)
}

func TestServiceRotateOnPrivilegeChange_InvalidatesOldSessionsAndMintsNewSession(t *testing.T) {
	svc, mr, jwt := newTestService(t)
	uid := uuid.New()

	oldSession := mustCreate(t, svc, uid, "phone")
	require.True(t, mr.Exists(testSessionKey(oldSession.RefreshToken)))

	rotated, err := svc.RotateOnPrivilegeChange(t.Context(), uid, "admin", testAccessTTL, "phone")
	require.NoError(t, err)
	assert.NotEqual(t, oldSession.RefreshToken, rotated.RefreshToken)
	assert.NotEqual(t, oldSession.AccessToken, rotated.AccessToken)
	assert.NotEqual(t, oldSession.CSRFToken, rotated.CSRFToken)

	assert.False(t, mr.Exists(testSessionKey(oldSession.RefreshToken)), "old session must be deleted on privilege change")
	assert.True(t, mr.Exists(testSessionKey(rotated.RefreshToken)), "rotated session must exist")
	require.Len(t, jwt.calls, 2)
	assert.Equal(t, "admin", jwt.calls[1].role)

	// Attempting to refresh with the old pre-privilege-change refresh token must fail.
	replayed, err := svc.Refresh(t.Context(), oldSession.RefreshToken, testAccessTTL)
	assert.Nil(t, replayed)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

// ── GenerateJWT ──

func TestServiceGenerateJWT_DelegatesToTheJWTService(t *testing.T) {
	svc, _, jwt := newTestService(t)
	uid := uuid.New()

	token, err := svc.GenerateJWT(t.Context(), uid, "GWALLET", "admin", time.Hour)

	require.NoError(t, err)
	assert.Equal(t, "access-jwt-1", token)
	assert.Equal(t, []jwtCall{{userID: uid, wallet: "GWALLET", role: "admin", ttl: time.Hour}}, jwt.calls)
}

func TestServiceGenerateJWT_PropagatesTheSignerError(t *testing.T) {
	svc, _, jwt := newTestService(t)
	signerDown := errors.New("signer down")
	jwt.err = signerDown

	token, err := svc.GenerateJWT(t.Context(), uuid.New(), "", "user", time.Hour)

	assert.Empty(t, token)
	assert.ErrorIs(t, err, signerDown)
}

// ── helpers ──

func TestSessionRole(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"admin role", "uid|phone|1700000000|admin", "admin"},
		{"user role", "uid|phone|1700000000|user", "user"},
		{"unknown role falls back to user", "uid|phone|1700000000|superuser", "user"},
		{"legacy data without a role falls back to user", "uid|phone|1700000000", "user"},
		{"empty data falls back to user", "", "user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SessionRole(tt.data))
		})
	}
}

func TestSha256Hash_MatchesTheKnownDigest(t *testing.T) {
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", sha256Hash("abc"))
}
