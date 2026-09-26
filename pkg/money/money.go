// Package money provides a fixed-point monetary amount with seven decimal
// places, matching the precision of Stellar assets (XLM and Stellar-issued
// tokens such as USDC). All balance arithmetic in the codebase goes through
// this type so that units are never mixed: a Money value always represents
// whole tokens, and its integer representation is always stroops.
package money

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Precision is the number of decimal places carried by Money.
const Precision = 7

// StroopsPerUnit is the number of stroops in one whole token.
const StroopsPerUnit int64 = 10_000_000

var (
	// ErrOverflow is returned when an operation does not fit in int64 stroops.
	ErrOverflow = errors.New("money: arithmetic overflow")
	// ErrInvalidAmount is returned when a string or float cannot be parsed
	// into an exact amount.
	ErrInvalidAmount = errors.New("money: invalid amount")
	// ErrDivideByZero is returned by MulDiv when the divisor is zero.
	ErrDivideByZero = errors.New("money: divide by zero")
)

var (
	maxStroops = big.NewInt(math.MaxInt64)
	minStroops = big.NewInt(math.MinInt64)
)

// Money is an immutable amount of a single asset expressed in stroops.
// The zero value is a zero amount.
type Money struct {
	stroops int64
}

// Zero returns a zero amount.
func Zero() Money { return Money{} }

// FromStroops builds a Money from an integer number of stroops.
func FromStroops(stroops int64) Money { return Money{stroops: stroops} }

// FromString parses a decimal string such as "12.5" or "-0.0000001". At most
// seven fractional digits are accepted; exponents, thousands separators and
// leading '+' are rejected so that database and API values round-trip exactly.
func FromString(s string) (Money, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Money{}, fmt.Errorf("%w: empty string", ErrInvalidAmount)
	}
	negative := false
	if s[0] == '-' {
		negative = true
		s = s[1:]
	}
	if s == "" {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if intPart == "" && fracPart == "" {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > Precision {
		return Money{}, fmt.Errorf("%w: %q has more than %d decimal places", ErrInvalidAmount, s, Precision)
	}
	if !allDigits(intPart) || (fracPart != "" && !allDigits(fracPart)) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	fracPart += strings.Repeat("0", Precision-len(fracPart))
	digits := strings.TrimLeft(intPart+fracPart, "0")
	if digits == "" {
		return Money{}, nil
	}
	v, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	if negative {
		v.Neg(v)
	}
	return fromBig(v)
}

// MustFromString is FromString for constants; it panics on invalid input.
func MustFromString(s string) Money {
	m, err := FromString(s)
	if err != nil {
		panic(err)
	}
	return m
}

// FromFloat64 converts a float amount of whole tokens to Money, rounding
// half away from zero to the nearest stroop. NaN, infinities and values that
// do not fit in int64 stroops are rejected.
func FromFloat64(f float64) (Money, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalidAmount, f)
	}
	// Format with exactly seven decimals first so that binary artefacts such
	// as 0.1+0.2 round the same way a human would expect, then parse exactly.
	return FromString(strconv.FormatFloat(f, 'f', Precision, 64))
}

// MustFromFloat64 is FromFloat64 for trusted finite values; it panics otherwise.
func MustFromFloat64(f float64) Money {
	m, err := FromFloat64(f)
	if err != nil {
		panic(err)
	}
	return m
}

// Stroops returns the amount as an integer number of stroops.
func (m Money) Stroops() int64 { return m.stroops }

// Float64 returns the amount as whole tokens. It is intended for display and
// for legacy call sites that still carry float fields; never feed the result
// back into arithmetic.
func (m Money) Float64() float64 { return float64(m.stroops) / float64(StroopsPerUnit) }

// String renders the amount with exactly seven decimal places, the same
// canonical form Horizon uses for balances and payment amounts.
func (m Money) String() string {
	s := m.stroops
	sign := ""
	var abs uint64
	if s < 0 {
		sign = "-"
		abs = uint64(-(s + 1)) + 1 // safe for MinInt64
	} else {
		abs = uint64(s)
	}
	whole := abs / uint64(StroopsPerUnit)
	frac := abs % uint64(StroopsPerUnit)
	return fmt.Sprintf("%s%d.%07d", sign, whole, frac)
}

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.stroops == 0 }

// IsNegative reports whether the amount is below zero.
func (m Money) IsNegative() bool { return m.stroops < 0 }

// IsPositive reports whether the amount is above zero.
func (m Money) IsPositive() bool { return m.stroops > 0 }

// Cmp compares two amounts, returning -1, 0 or +1.
func (m Money) Cmp(o Money) int {
	switch {
	case m.stroops < o.stroops:
		return -1
	case m.stroops > o.stroops:
		return 1
	default:
		return 0
	}
}

// Equal reports whether two amounts are identical.
func (m Money) Equal(o Money) bool { return m.stroops == o.stroops }

// Neg returns the negated amount.
func (m Money) Neg() (Money, error) {
	if m.stroops == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{stroops: -m.stroops}, nil
}

// Add returns m + o, failing on int64 overflow.
func (m Money) Add(o Money) (Money, error) {
	sum := m.stroops + o.stroops
	if (o.stroops > 0 && sum < m.stroops) || (o.stroops < 0 && sum > m.stroops) {
		return Money{}, ErrOverflow
	}
	return Money{stroops: sum}, nil
}

// Sub returns m - o, failing on int64 overflow.
func (m Money) Sub(o Money) (Money, error) {
	diff := m.stroops - o.stroops
	if (o.stroops < 0 && diff < m.stroops) || (o.stroops > 0 && diff > m.stroops) {
		return Money{}, ErrOverflow
	}
	return Money{stroops: diff}, nil
}

// MulInt returns m * n, failing on int64 overflow.
func (m Money) MulInt(n int64) (Money, error) {
	return fromBig(new(big.Int).Mul(big.NewInt(m.stroops), big.NewInt(n)))
}

// MulDiv returns m * num / den computed exactly in big integers and rounded
// half away from zero to the nearest stroop. It is the primitive for fees and
// percentages: a 2.5% fee is MulDiv(25, 1000).
func (m Money) MulDiv(num, den int64) (Money, error) {
	if den == 0 {
		return Money{}, ErrDivideByZero
	}
	prod := new(big.Int).Mul(big.NewInt(m.stroops), big.NewInt(num))
	return fromBig(roundDiv(prod, big.NewInt(den)))
}

// Percent returns the given percentage of m. The percentage is taken to six
// decimal places (so 12.345678% is exact) and the result is rounded half away
// from zero to the nearest stroop.
func (m Money) Percent(percent float64) (Money, error) {
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		return Money{}, fmt.Errorf("%w: percent %v", ErrInvalidAmount, percent)
	}
	micro := math.Round(percent * 1_000_000)
	if math.Abs(micro) > float64(math.MaxInt64) {
		return Money{}, ErrOverflow
	}
	return m.MulDiv(int64(micro), 100_000_000)
}

// Sum adds any number of amounts, failing on overflow.
func Sum(amounts ...Money) (Money, error) {
	total := Money{}
	for _, a := range amounts {
		var err error
		if total, err = total.Add(a); err != nil {
			return Money{}, err
		}
	}
	return total, nil
}

// Scan implements sql.Scanner. NUMERIC/DECIMAL columns arrive as text and are
// parsed exactly; integer columns are interpreted as stroops.
func (m *Money) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*m = Money{}
		return nil
	case []byte:
		parsed, err := FromString(string(v))
		if err != nil {
			return err
		}
		*m = parsed
	case string:
		parsed, err := FromString(v)
		if err != nil {
			return err
		}
		*m = parsed
	case int64:
		*m = FromStroops(v)
	case float64:
		parsed, err := FromFloat64(v)
		if err != nil {
			return err
		}
		*m = parsed
	default:
		return fmt.Errorf("%w: cannot scan %T into Money", ErrInvalidAmount, src)
	}
	return nil
}

// Value implements driver.Valuer, emitting the canonical decimal string so
// the amount lands in NUMERIC columns without floating-point rounding.
func (m Money) Value() (driver.Value, error) { return m.String(), nil }

// MarshalJSON encodes the amount as a JSON number with seven decimal places.
func (m Money) MarshalJSON() ([]byte, error) { return []byte(m.String()), nil }

// UnmarshalJSON accepts either a JSON number or a decimal string.
func (m *Money) UnmarshalJSON(data []byte) error {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	text := strings.TrimSpace(string(raw))
	if text == "null" {
		*m = Money{}
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
	}
	if strings.ContainsAny(text, "eE") {
		// JSON numbers may carry an exponent; route them through float parsing.
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidAmount, text)
		}
		parsed, err := FromFloat64(f)
		if err != nil {
			return err
		}
		*m = parsed
		return nil
	}
	parsed, err := FromString(text)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func fromBig(v *big.Int) (Money, error) {
	if v.Cmp(maxStroops) > 0 || v.Cmp(minStroops) < 0 {
		return Money{}, ErrOverflow
	}
	return Money{stroops: v.Int64()}, nil
}

// roundDiv divides num by den rounding half away from zero.
func roundDiv(num, den *big.Int) *big.Int {
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Mul(rem, big.NewInt(2))
	twice.Abs(twice)
	absDen := new(big.Int).Abs(den)
	if twice.Cmp(absDen) >= 0 {
		if (num.Sign() < 0) != (den.Sign() < 0) {
			quo.Sub(quo, big.NewInt(1))
		} else {
			quo.Add(quo, big.NewInt(1))
		}
	}
	return quo
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
