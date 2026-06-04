package wallet

type WalletType string

const (
	WalletTypeAuto     WalletType = "auto"
	WalletTypeFreighter WalletType = "freighter"
	WalletTypePasskey  WalletType = "passkey"
)

type Wallet struct {
	ID                 string     `json:"id" db:"id"`
	UserID             string     `json:"userId" db:"user_id"`
	PublicKey          string     `json:"publicKey" db:"public_key"`
	EncryptedSecretKey []byte     `json:"-" db:"encrypted_secret_key"`
	EncryptionNonce    []byte     `json:"-" db:"encryption_nonce"`
	WalletType         WalletType `json:"walletType" db:"wallet_type"`
	IsPrimary          bool       `json:"isPrimary" db:"is_primary"`
	CreatedAt          string     `json:"createdAt" db:"created_at"`
	UpdatedAt          string     `json:"updatedAt" db:"updated_at"`
}
