package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStream runs fn while *target is swapped for a pipe, and returns
// everything fn wrote to it.
func captureStream(t *testing.T, target **os.File, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := *target
	*target = w
	defer func() { *target = orig }()

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestTrimSpace(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "   ", want: ""},
		{in: "  a b \t\r\n", want: "a b"},
		{in: "plain", want: "plain"},
		{in: "\t\tleading", want: "leading"},
		{in: "trailing\r\n ", want: "trailing"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, trimSpace(c.in), "input %q", c.in)
	}
}

func TestSplitLines(t *testing.T) {
	assert.Empty(t, splitLines(""))
	assert.Equal(t, []string{"a", "b"}, splitLines("a\nb\n"))
	assert.Equal(t, []string{"a", "b"}, splitLines("a\r\nb"), "carriage returns are dropped")
	assert.Equal(t, []string{"a", "", "b"}, splitLines("a\n\nb"), "blank lines are kept")
}

func TestLoadEnvFile_SetsMissingKeys(t *testing.T) {
	// t.Setenv registers cleanup so the variables are removed after the test.
	keys := []string{"MOISTELLO_TEST_PLAIN", "MOISTELLO_TEST_EXPORT", "MOISTELLO_TEST_DQUOTE", "MOISTELLO_TEST_SQUOTE"}
	for _, k := range keys {
		t.Setenv(k, "")
	}

	content := "" +
		"# a comment line\n" +
		"\n" +
		"MOISTELLO_TEST_PLAIN=value-one\n" +
		"export MOISTELLO_TEST_EXPORT=value-two\n" +
		"MOISTELLO_TEST_DQUOTE=\"quoted value\"\n" +
		"MOISTELLO_TEST_SQUOTE='single quoted'\n" +
		"this line has no equals sign\n" +
		"=missing-key\n"

	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	require.NoError(t, loadEnvFile(path))

	assert.Equal(t, "value-one", os.Getenv("MOISTELLO_TEST_PLAIN"))
	assert.Equal(t, "value-two", os.Getenv("MOISTELLO_TEST_EXPORT"))
	assert.Equal(t, "quoted value", os.Getenv("MOISTELLO_TEST_DQUOTE"))
	assert.Equal(t, "single quoted", os.Getenv("MOISTELLO_TEST_SQUOTE"))
}

func TestLoadEnvFile_DoesNotOverrideExistingVariables(t *testing.T) {
	t.Setenv("MOISTELLO_TEST_KEEP", "from-environment")

	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("MOISTELLO_TEST_KEEP=from-file\n"), 0o600))

	require.NoError(t, loadEnvFile(path))
	assert.Equal(t, "from-environment", os.Getenv("MOISTELLO_TEST_KEEP"))
}

func TestLoadEnvFile_MissingFile(t *testing.T) {
	err := loadEnvFile(filepath.Join(t.TempDir(), "does-not-exist.env"))
	assert.Error(t, err)
}

func TestReportResult_JSONValid(t *testing.T) {
	out := captureStream(t, &os.Stdout, func() {
		reportResult(true, true, "testnet", nil)
	})

	var res ValidationResult
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.True(t, res.Valid)
	assert.Equal(t, "testnet", res.Environment)
	assert.Empty(t, res.Errors)
	assert.Equal(t, "configuration is valid (offline preflight passed)", res.Message)
}

func TestReportResult_JSONInvalid(t *testing.T) {
	out := captureStream(t, &os.Stdout, func() {
		reportResult(true, false, "production", []string{"jwt key missing", "bad port"})
	})

	var res ValidationResult
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.False(t, res.Valid)
	assert.Equal(t, "production", res.Environment)
	assert.Equal(t, []string{"jwt key missing", "bad port"}, res.Errors)
	assert.Equal(t, "configuration validation failed", res.Message)
}

func TestReportResult_TextValidWritesToStdout(t *testing.T) {
	out := captureStream(t, &os.Stdout, func() {
		reportResult(false, true, "testnet", nil)
	})
	assert.Contains(t, out, "Configuration is valid (environment=testnet")
}

func TestReportResult_TextInvalidWritesErrorsToStderr(t *testing.T) {
	stdout := captureStream(t, &os.Stdout, func() {
		stderr := captureStream(t, &os.Stderr, func() {
			reportResult(false, false, "", []string{"bad port"})
		})
		assert.Contains(t, stderr, "Configuration validation failed")
		assert.Contains(t, stderr, "- bad port")
	})
	assert.Empty(t, stdout, "failures must not be written to stdout")
}
