package money

import (
	"encoding/json"
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromString_ExactParsing(t *testing.T) {
	cases := map[string]int64{
		"0":            0,
		"1":            10_000_000,
		"1.5":          15_000_000,
		"0.0000001":    1,
		"-0.0000001":   -1,
		"123.4567890":  1_234_567_890,
		".5":           5_000_000,
		"7.":           70_000_000,
		"922337203685": 922337203685 * 10_000_000,
	}
	for in, want := range cases {
		got, err := FromString(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got.Stroops(), in)
	}
}

func TestFromString_Rejects(t *testing.T) {
	for _, in := range []string{"", "-", ".", "abc", "1e7", "1,000", "+1", "0.00000001", "1.2.3", "922337203686"} {
		_, err := FromString(in)
		assert.Error(t, err, in)
	}
}

func TestFromFloat64_RoundsToNearestStroop(t *testing.T) {
	cases := map[float64]int64{
		0.1 + 0.2:    3_000_000,
		1.00000006:   10_000_001,
		-1.00000006:  -10_000_001,
		100:          1_000_000_000,
		0.123456789:  1_234_568,
		33.33:        333_300_000,
		1e-8:         0,
		-0.00000004:  0,
		12345.678901: 123_456_789_010,
	}
	for in, want := range cases {
		got, err := FromFloat64(in)
		require.NoError(t, err)
		assert.Equal(t, want, got.Stroops(), "%v", in)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e300} {
		_, err := FromFloat64(bad)
		assert.Error(t, err, "%v", bad)
	}
}

func TestString_CanonicalSevenDecimals(t *testing.T) {
	assert.Equal(t, "0.0000000", Zero().String())
	assert.Equal(t, "1.5000000", MustFromString("1.5").String())
	assert.Equal(t, "-0.0000001", FromStroops(-1).String())
	assert.Equal(t, "922337203685.4775807", FromStroops(math.MaxInt64).String())
	assert.Equal(t, "-922337203685.4775808", FromStroops(math.MinInt64).String())
}

func TestStringRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 10_000; i++ {
		m := FromStroops(rng.Int63() - rng.Int63())
		back, err := FromString(m.String())
		require.NoError(t, err)
		assert.True(t, m.Equal(back), "%s", m)
	}
}

func TestArithmetic(t *testing.T) {
	a := MustFromString("10.5")
	b := MustFromString("0.25")

	sum, err := a.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "10.7500000", sum.String())

	diff, err := a.Sub(b)
	require.NoError(t, err)
	assert.Equal(t, "10.2500000", diff.String())

	prod, err := b.MulInt(5)
	require.NoError(t, err)
	assert.Equal(t, "1.2500000", prod.String())

	fee, err := a.MulDiv(25, 1000) // 2.5%
	require.NoError(t, err)
	assert.Equal(t, "0.2625000", fee.String())

	pct, err := a.Percent(2.5)
	require.NoError(t, err)
	assert.True(t, fee.Equal(pct))

	neg, err := b.Neg()
	require.NoError(t, err)
	assert.Equal(t, "-0.2500000", neg.String())

	total, err := Sum(a, b, neg)
	require.NoError(t, err)
	assert.True(t, total.Equal(a))

	assert.Equal(t, -1, b.Cmp(a))
	assert.Equal(t, 1, a.Cmp(b))
	assert.Equal(t, 0, a.Cmp(a))
	assert.True(t, Zero().IsZero())
	assert.True(t, neg.IsNegative())
	assert.True(t, a.IsPositive())
}

func TestMulDiv_RoundsHalfAwayFromZero(t *testing.T) {
	one := FromStroops(1)
	half, err := one.MulDiv(1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(1), half.Stroops())

	negHalf, err := FromStroops(-1).MulDiv(1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(-1), negHalf.Stroops())

	third, err := one.MulDiv(1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(0), third.Stroops())

	_, err = one.MulDiv(1, 0)
	assert.ErrorIs(t, err, ErrDivideByZero)
}

func TestOverflowIsDetected(t *testing.T) {
	maxM := FromStroops(math.MaxInt64)
	_, err := maxM.Add(FromStroops(1))
	assert.ErrorIs(t, err, ErrOverflow)
	_, err = FromStroops(math.MinInt64).Sub(FromStroops(1))
	assert.ErrorIs(t, err, ErrOverflow)
	_, err = maxM.MulInt(2)
	assert.ErrorIs(t, err, ErrOverflow)
	_, err = FromStroops(math.MinInt64).Neg()
	assert.ErrorIs(t, err, ErrOverflow)
	_, err = maxM.MulDiv(3, 2)
	assert.ErrorIs(t, err, ErrOverflow)
	_, err = Sum(maxM, maxM)
	assert.ErrorIs(t, err, ErrOverflow)
}

func TestPercent_ConservesFunds(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		gross := FromStroops(rng.Int63n(1_000_000_000_000))
		pct := float64(rng.Intn(10000)) / 100.0
		fee, err := gross.Percent(pct)
		require.NoError(t, err)
		net, err := gross.Sub(fee)
		require.NoError(t, err)
		back, err := net.Add(fee)
		require.NoError(t, err)
		assert.True(t, back.Equal(gross), "gross %s fee %s net %s", gross, fee, net)
		assert.False(t, fee.IsNegative())
		assert.LessOrEqual(t, fee.Stroops(), gross.Stroops())
	}
}

func TestSQLScanAndValue(t *testing.T) {
	var m Money
	require.NoError(t, m.Scan([]byte("100.5000000")))
	assert.Equal(t, "100.5000000", m.String())

	require.NoError(t, m.Scan("0.25"))
	assert.Equal(t, "0.2500000", m.String())

	require.NoError(t, m.Scan(int64(15)))
	assert.Equal(t, int64(15), m.Stroops())

	require.NoError(t, m.Scan(1.5))
	assert.Equal(t, "1.5000000", m.String())

	require.NoError(t, m.Scan(nil))
	assert.True(t, m.IsZero())

	assert.Error(t, m.Scan(true))
	assert.Error(t, m.Scan([]byte("1e5")))

	v, err := MustFromString("42.1").Value()
	require.NoError(t, err)
	assert.Equal(t, "42.1000000", v)
}

func TestJSON(t *testing.T) {
	type payload struct {
		Amount Money `json:"amount"`
	}
	out, err := json.Marshal(payload{Amount: MustFromString("12.5")})
	require.NoError(t, err)
	assert.Equal(t, `{"amount":12.5000000}`, string(out))

	var in payload
	require.NoError(t, json.Unmarshal([]byte(`{"amount":100}`), &in))
	assert.Equal(t, "100.0000000", in.Amount.String())
	require.NoError(t, json.Unmarshal([]byte(`{"amount":"0.0000001"}`), &in))
	assert.Equal(t, int64(1), in.Amount.Stroops())
	require.NoError(t, json.Unmarshal([]byte(`{"amount":1.5e2}`), &in))
	assert.Equal(t, "150.0000000", in.Amount.String())
	require.NoError(t, json.Unmarshal([]byte(`{"amount":null}`), &in))
	assert.True(t, in.Amount.IsZero())
	assert.Error(t, json.Unmarshal([]byte(`{"amount":"abc"}`), &in))
	assert.Error(t, json.Unmarshal([]byte(`{"amount":0.00000001}`), &in))
}

func TestFloat64RoundTripWithinOneStroop(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 10_000; i++ {
		m := FromStroops(rng.Int63n(1_000_000_000_000_000))
		back, err := FromFloat64(m.Float64())
		require.NoError(t, err)
		assert.LessOrEqual(t, math.Abs(float64(back.Stroops()-m.Stroops())), 1.0, "%s", m)
	}
}
