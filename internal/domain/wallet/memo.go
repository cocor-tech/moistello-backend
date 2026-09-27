package wallet

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxMemoBytes is the Stellar limit for a MEMO_TEXT value.
const MaxMemoBytes = 28

// MaxLabelLength is the maximum length of a user-facing transfer label.
const MaxLabelLength = 64

// ValidateMemo checks that memo fits a Stellar text memo. An empty memo is allowed.
func ValidateMemo(memo string) error {
	if len(memo) > MaxMemoBytes {
		return fmt.Errorf("memo must be at most %d bytes", MaxMemoBytes)
	}
	return nil
}

// NormalizeLabel trims a transfer label and rejects over-long or
// control-character values. An empty label is allowed.
func NormalizeLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if len([]rune(label)) > MaxLabelLength {
		return "", fmt.Errorf("label must be at most %d characters", MaxLabelLength)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("label must not contain control characters")
		}
	}
	return label, nil
}
