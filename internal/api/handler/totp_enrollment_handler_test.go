package handler_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	totpdomain "github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
)

// totpFixture wires the handler against an in-memory user so every
// endpoint sees the state the previous one persisted, as production does
// through Postgres.
type totpFixture struct {
	t      *testing.T
	router *gin.Engine
	user   *user.User
	repo   *userMocks.Repository
}

func newTOTPFixture(t *testing.T) *totpFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	email := "jane@example.com"
	u := &user.User{ID: uuid.New(), Email: &email, WalletAddress: "GJANE"}
	repo := new(userMocks.Repository)
	repo.On("FindByID", mock.Anything, u.ID).Return(u, nil)
	repo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	h := handler.NewAuthHandler(nil, user.NewService(repo, nil), nil, totpdomain.NewService(), nil, nil, nil, repo)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", u.ID.String()); c.Next() })
	r.POST("/auth/totp/enroll", h.EnrollTOTP)
	r.POST("/auth/totp/enable", h.EnableTOTP)
	r.POST("/auth/totp/verify", h.VerifyTOTP)
	r.POST("/auth/totp/disable", h.DisableTOTP)
	r.POST("/auth/totp/recovery-codes", h.RegenerateTOTPRecoveryCodes)
	return &totpFixture{t: t, router: r, user: u, repo: repo}
}

func (f *totpFixture) post(path string, body any) (int, map[string]any) {
	f.t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	f.router.ServeHTTP(w, req)
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	return w.Code, envelope.Data
}

func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	return code
}

func (f *totpFixture) enroll() (secret string) {
	f.t.Helper()
	code, data := f.post("/auth/totp/enroll", nil)
	require.Equal(f.t, http.StatusOK, code)
	return data["secret"].(string)
}

func (f *totpFixture) enable(secret string) []string {
	f.t.Helper()
	code, data := f.post("/auth/totp/enable", gin.H{"code": currentCode(f.t, secret)})
	require.Equal(f.t, http.StatusOK, code)
	raw := data["recoveryCodes"].([]any)
	codes := make([]string, len(raw))
	for i, c := range raw {
		codes[i] = c.(string)
	}
	return codes
}

func TestTOTP_Enroll_ReturnsProvisioningURIAndQR(t *testing.T) {
	f := newTOTPFixture(t)
	code, data := f.post("/auth/totp/enroll", nil)
	require.Equal(t, http.StatusOK, code)

	key, err := otp.NewKeyFromURL(data["uri"].(string))
	require.NoError(t, err, "URI must be parseable by a standard TOTP library")
	assert.Equal(t, "Moistello", key.Issuer())
	assert.Equal(t, "jane@example.com", key.AccountName())
	assert.Equal(t, data["secret"], key.Secret())

	qr, err := base64.StdEncoding.DecodeString(data["qrPngBase64"].(string))
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(qr))
	require.NoError(t, err)
	assert.Equal(t, totpdomain.DefaultQRSize, img.Bounds().Dx())

	assert.False(t, f.user.TOTPEnabled, "enrolling must not enable TOTP yet")
	assert.Equal(t, key.Secret(), f.user.TOTPSecret.String)
}

func TestTOTP_Enable_RequiresCodeFromRealAuthenticator(t *testing.T) {
	f := newTOTPFixture(t)

	code, _ := f.post("/auth/totp/enable", gin.H{"code": "123456"})
	assert.Equal(t, http.StatusBadRequest, code, "enable before enroll is rejected")

	secret := f.enroll()

	code, _ = f.post("/auth/totp/enable", gin.H{"code": "000000"})
	assert.Equal(t, http.StatusBadRequest, code, "wrong code is rejected")
	assert.False(t, f.user.TOTPEnabled)

	stale, err := totp.GenerateCode(secret, time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	code, _ = f.post("/auth/totp/enable", gin.H{"code": stale})
	assert.Equal(t, http.StatusBadRequest, code, "expired code is rejected")

	recovery := f.enable(secret)
	assert.True(t, f.user.TOTPEnabled)
	assert.Len(t, recovery, totpdomain.BackupCodeCount)
	assert.Len(t, f.user.BackupCodes, totpdomain.BackupCodeCount)
	for i, plain := range recovery {
		assert.NotEqual(t, plain, f.user.BackupCodes[i], "only hashes are stored")
	}

	code, _ = f.post("/auth/totp/enable", gin.H{"code": currentCode(t, secret)})
	assert.Equal(t, http.StatusConflict, code, "enabling twice is a conflict")
	code, _ = f.post("/auth/totp/enroll", nil)
	assert.Equal(t, http.StatusConflict, code, "re-enrolling while enabled is a conflict")
}

func TestTOTP_Verify_AcceptsCodeAndConsumesRecoveryCodesOnce(t *testing.T) {
	f := newTOTPFixture(t)

	code, _ := f.post("/auth/totp/verify", gin.H{"code": "123456"})
	assert.Equal(t, http.StatusBadRequest, code, "verify before enabling is rejected")

	secret := f.enroll()
	recovery := f.enable(secret)

	code, data := f.post("/auth/totp/verify", gin.H{"code": currentCode(t, secret)})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "totp", data["method"])
	assert.Equal(t, float64(totpdomain.BackupCodeCount), data["recoveryCodesRemaining"])

	code, data = f.post("/auth/totp/verify", gin.H{"recoveryCode": recovery[4]})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "recovery", data["method"])
	assert.Equal(t, float64(totpdomain.BackupCodeCount-1), data["recoveryCodesRemaining"])
	assert.Len(t, f.user.BackupCodes, totpdomain.BackupCodeCount-1, "used code is persisted as removed")

	code, _ = f.post("/auth/totp/verify", gin.H{"recoveryCode": recovery[4]})
	assert.Equal(t, http.StatusBadRequest, code, "a recovery code is single-use")

	code, _ = f.post("/auth/totp/verify", gin.H{"recoveryCode": "ZZZZ-ZZZZ-ZZZZ"})
	assert.Equal(t, http.StatusBadRequest, code)

	code, _ = f.post("/auth/totp/verify", gin.H{})
	assert.Equal(t, http.StatusBadRequest, code, "a body without any code is rejected")

	// Lower-case, unhyphenated input still matches.
	loose := []byte(recovery[5])
	for i, b := range loose {
		if b >= 'A' && b <= 'Z' {
			loose[i] = b + 32
		}
	}
	code, _ = f.post("/auth/totp/verify", gin.H{"recoveryCode": string(loose)})
	assert.Equal(t, http.StatusOK, code)
}

func TestTOTP_RegenerateRecoveryCodes_InvalidatesOldOnes(t *testing.T) {
	f := newTOTPFixture(t)
	secret := f.enroll()
	old := f.enable(secret)

	code, _ := f.post("/auth/totp/recovery-codes", gin.H{"code": "000000"})
	assert.Equal(t, http.StatusBadRequest, code)

	code, data := f.post("/auth/totp/recovery-codes", gin.H{"code": currentCode(t, secret)})
	require.Equal(t, http.StatusOK, code)
	fresh := data["recoveryCodes"].([]any)
	assert.Len(t, fresh, totpdomain.BackupCodeCount)

	code, _ = f.post("/auth/totp/verify", gin.H{"recoveryCode": old[0]})
	assert.Equal(t, http.StatusBadRequest, code, "old recovery codes stop working")
	code, _ = f.post("/auth/totp/verify", gin.H{"recoveryCode": fresh[0].(string)})
	assert.Equal(t, http.StatusOK, code)
}

func TestTOTP_Disable_WithCodeOrRecoveryCodeClearsEverything(t *testing.T) {
	f := newTOTPFixture(t)

	code, _ := f.post("/auth/totp/disable", gin.H{"code": "123456"})
	assert.Equal(t, http.StatusBadRequest, code, "cannot disable what is not enabled")

	secret := f.enroll()
	recovery := f.enable(secret)

	code, _ = f.post("/auth/totp/disable", gin.H{"code": "000000"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.True(t, f.user.TOTPEnabled, "a wrong code must not disable TOTP")

	code, _ = f.post("/auth/totp/disable", gin.H{"recoveryCode": recovery[0]})
	require.Equal(t, http.StatusOK, code)
	assert.False(t, f.user.TOTPEnabled)
	assert.False(t, f.user.TOTPSecret.Valid, "secret is discarded")
	assert.Empty(t, f.user.BackupCodes, "recovery codes are discarded")

	// A fresh enrollment gets a new secret and can be disabled with a code.
	secret2 := f.enroll()
	assert.NotEqual(t, secret, secret2)
	f.enable(secret2)
	code, _ = f.post("/auth/totp/disable", gin.H{"code": currentCode(t, secret2)})
	require.Equal(t, http.StatusOK, code)
	assert.False(t, f.user.TOTPEnabled)
}

func TestTOTP_RequiresAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := new(userMocks.Repository)
	h := handler.NewTOTPEnrollmentHandler(user.NewService(repo, nil), repo, nil)
	r := gin.New()
	r.POST("/auth/totp/enroll", h.EnrollTOTP)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/auth/totp/enroll", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
