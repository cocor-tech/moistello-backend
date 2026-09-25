package stellar

import (
	"testing"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFundAccountTx(t *testing.T) {
	master, err := keypair.Random()
	require.NoError(t, err)
	dest, err := keypair.Random()
	require.NoError(t, err)

	tx, err := BuildFundAccountTx(master, 100, dest.Address(), "2.0000000", "Test SDF Network ; September 2015")
	require.NoError(t, err)

	assert.Equal(t, master.Address(), tx.SourceAccount().AccountID)
	assert.Len(t, tx.Signatures(), 1)

	ops := tx.Operations()
	require.Len(t, ops, 1)
	op, ok := ops[0].(*txnbuild.CreateAccount)
	require.True(t, ok)
	assert.Equal(t, dest.Address(), op.Destination)
	assert.Equal(t, "2.0000000", op.Amount)
}

func TestBuildFundAccountTx_InvalidDestination(t *testing.T) {
	master, err := keypair.Random()
	require.NoError(t, err)

	_, err = BuildFundAccountTx(master, 1, "not-an-address", "2.0000000", "Test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid destination address")
}

func TestBuildChangeTrustTx(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)
	issuer, err := keypair.Random()
	require.NoError(t, err)

	tx, err := BuildChangeTrustTx(kp, 5, "USDC", issuer.Address(), "Test SDF Network ; September 2015")
	require.NoError(t, err)

	assert.Equal(t, kp.Address(), tx.SourceAccount().AccountID)
	assert.Len(t, tx.Signatures(), 1)

	ops := tx.Operations()
	require.Len(t, ops, 1)
	op, ok := ops[0].(*txnbuild.ChangeTrust)
	require.True(t, ok)
	line, ok := op.Line.(txnbuild.ChangeTrustAssetWrapper)
	require.True(t, ok)
	asset, ok := line.Asset.(txnbuild.CreditAsset)
	require.True(t, ok)
	assert.Equal(t, "USDC", asset.Code)
	assert.Equal(t, issuer.Address(), asset.Issuer)
}
