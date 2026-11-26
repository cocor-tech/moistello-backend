package stellar

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validAddress returns a syntactically valid Stellar G-address for use in
// tests that need a placeholder address without network connectivity.
// Stellar public keys are 56-character base32 strings starting with 'G'.
const validStellarAddress = "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN"

func TestValidateAddress_ValidAddress(t *testing.T) {
	require.NoError(t, ValidateAddress(validStellarAddress))
}

func TestValidateAddress_TooShort(t *testing.T) {
	err := ValidateAddress("GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "56 characters")
}

func TestValidateAddress_TooLong(t *testing.T) {
	long := validStellarAddress + "X"
	err := ValidateAddress(long)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "56 characters")
}

func TestValidateAddress_WrongPrefix(t *testing.T) {
	// Replace 'G' with 'S' – that's the seed prefix, not a public key.
	bad := "S" + validStellarAddress[1:]
	err := ValidateAddress(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must start with G")
}

func TestValidateAddress_InvalidCharacters(t *testing.T) {
	// Base32 alphabet is A-Z and 2-7. '0' and '1' are invalid.
	// Replace a character in the middle with '0'.
	bad := validStellarAddress[:10] + "0" + validStellarAddress[11:]
	err := ValidateAddress(bad)
	require.Error(t, err)
}

func TestValidateAddress_Exactly56CharsButInvalidChars(t *testing.T) {
	// All 'A's except prefix – 'G' + 55 'A's is valid base32, but let's
	// inject a lowercase character which is not in the regexp.
	bad := "G" + strings.Repeat("a", 55)
	err := ValidateAddress(bad)
	require.Error(t, err)
}

func TestValidateAddress_EmptyString(t *testing.T) {
	err := ValidateAddress("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "56 characters")
}

func TestAddressRegexp_MatchesValidAddress(t *testing.T) {
	assert.True(t, AddressRegexp.MatchString(validStellarAddress))
}

func TestAddressRegexp_RejectsInvalidAddress(t *testing.T) {
	assert.False(t, AddressRegexp.MatchString("not-valid"))
}
