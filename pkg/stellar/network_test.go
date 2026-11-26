package stellar

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ValidateNetwork
// ---------------------------------------------------------------------------

func TestValidateNetwork_Testnet(t *testing.T) {
	n, err := ValidateNetwork("testnet")
	require.NoError(t, err)
	assert.Equal(t, Testnet, n)
}

func TestValidateNetwork_Mainnet(t *testing.T) {
	n, err := ValidateNetwork("mainnet")
	require.NoError(t, err)
	assert.Equal(t, Mainnet, n)
}

func TestValidateNetwork_Invalid(t *testing.T) {
	_, err := ValidateNetwork("stagenet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid network")
}

func TestValidateNetwork_EmptyString(t *testing.T) {
	_, err := ValidateNetwork("")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Known network constants match the official Stellar passphrases
// (see https://developers.stellar.org/docs/learn/fundamentals/networks)
// ---------------------------------------------------------------------------

func TestTestnetConfig_KnownValues(t *testing.T) {
	assert.Equal(t, "Test SDF Network ; September 2015", TestnetConfig.NetworkPassphrase)
	assert.Equal(t, "https://horizon-testnet.stellar.org", TestnetConfig.HorizonURL)
	assert.Equal(t, "https://soroban-testnet.stellar.org", TestnetConfig.SorobanRPCURL)
	assert.Equal(t, Testnet, TestnetConfig.Name)
}

func TestMainnetConfig_KnownValues(t *testing.T) {
	assert.Equal(t, "Public Global Stellar Network ; September 2015", MainnetConfig.NetworkPassphrase)
	assert.Equal(t, "https://horizon.stellar.org", MainnetConfig.HorizonURL)
	assert.Equal(t, "https://soroban.stellar.org", MainnetConfig.SorobanRPCURL)
	assert.Equal(t, Mainnet, MainnetConfig.Name)
}

// ---------------------------------------------------------------------------
// MultiNetworkClient
// ---------------------------------------------------------------------------

func TestNewMultiNetworkClient_DefaultsToTestnet(t *testing.T) {
	c := NewMultiNetworkClient()
	assert.Equal(t, Testnet, c.GetCurrent())
	assert.NotNil(t, c.Testnet)
	assert.NotNil(t, c.Mainnet)
}

func TestMultiNetworkClient_For_Testnet(t *testing.T) {
	c := NewMultiNetworkClient()
	client, err := c.For(Testnet)
	require.NoError(t, err)
	assert.Same(t, c.Testnet, client)
}

func TestMultiNetworkClient_For_Mainnet(t *testing.T) {
	c := NewMultiNetworkClient()
	client, err := c.For(Mainnet)
	require.NoError(t, err)
	assert.Same(t, c.Mainnet, client)
}

func TestMultiNetworkClient_For_Unknown(t *testing.T) {
	c := NewMultiNetworkClient()
	_, err := c.For(Network("unknown"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown network")
}

func TestMultiNetworkClient_SetNetwork_Mainnet(t *testing.T) {
	c := NewMultiNetworkClient()
	require.NoError(t, c.SetNetwork(Mainnet))
	assert.Equal(t, Mainnet, c.GetCurrent())
}

func TestMultiNetworkClient_SetNetwork_Testnet(t *testing.T) {
	c := NewMultiNetworkClient()
	require.NoError(t, c.SetNetwork(Mainnet))
	require.NoError(t, c.SetNetwork(Testnet))
	assert.Equal(t, Testnet, c.GetCurrent())
}

func TestMultiNetworkClient_SetNetwork_Invalid(t *testing.T) {
	c := NewMultiNetworkClient()
	err := c.SetNetwork(Network("devnet"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown network")
}
