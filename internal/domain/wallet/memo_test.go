package wallet

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMemo(t *testing.T) {
	assert.NoError(t, ValidateMemo(""))
	assert.NoError(t, ValidateMemo(strings.Repeat("a", MaxMemoBytes)))
	assert.Error(t, ValidateMemo(strings.Repeat("a", MaxMemoBytes+1)))
	// 10 × 3-byte runes = 30 bytes: over the byte limit despite 10 characters.
	assert.Error(t, ValidateMemo(strings.Repeat("€", 10)))
}

func TestNormalizeLabel(t *testing.T) {
	got, err := NormalizeLabel("  rent for May  ")
	require.NoError(t, err)
	assert.Equal(t, "rent for May", got)

	got, err = NormalizeLabel("")
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = NormalizeLabel(strings.Repeat("x", MaxLabelLength+1))
	assert.Error(t, err)

	_, err = NormalizeLabel("bad\nlabel")
	assert.Error(t, err)
}
