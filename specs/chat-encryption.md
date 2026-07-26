# Specification: End-to-End Chat Encryption with X3DH & Double Ratchet

## 1. Overview
This specification details the End-to-End Encryption (E2EE) design implemented for Moistello chat. It uses the **Extended Triple Diffie-Hellman (X3DH)** protocol for initial key agreement and an adaptation of the **Double Ratchet** protocol for forward secrecy and post-compromise security across message sessions.

## 2. Key Architecture

### 2.1 Key Types
- **Identity Key ($IK$)**: Long-term X25519 keypair bound to a user's account.
- **Signed Prekey ($SPK$)**: Medium-term X25519 keypair signed with the user's Ed25519 key. Periodically rotated.
- **One-Time Prekeys ($OPK$)**: Pool of single-use X25519 keypairs consumed during session setup.
- **Ephemeral Key ($EK$)**: Single-use X25519 keypair created by the sender per handshake.

### 2.2 Prekey Bundle Publishing
Each client generates and uploads a Prekey Bundle to the server:
- $IK_{pub}$ (Public Identity Key)
- $SPK_{pub}$ (Public Signed Prekey) + $Sig(SPK_{pub})$
- Array of $OPK_{pub}$ (One-Time Prekeys)

## 3. X3DH Key Agreement Flow

```
   Alice (Initiator)                               Bob (Recipient)
  -------------------                             -----------------
 1. Fetch Bob's Bundle -------- Server ---------> 
 2. Verify Bob's SPK Signature
 3. Generate Ephemeral Key EK_A
 4. Calculate Shared DH Secrets:
    DH1 = ECDH(IK_A, SPK_B)
    DH2 = ECDH(EK_A, IK_B)
    DH3 = ECDH(EK_A, SPK_B)
    DH4 = ECDH(EK_A, OPK_B) [if present]
 5. Shared Master Key = HKDF(DH1 || DH2 || DH3 || DH4)
 6. Send Initial Message (with EK_A & OPK_B_ID) ---> 7. Reconstruct Shared Master Key
```

## 4. Double Ratchet & Forward Secrecy

Once the initial master secret $SK$ is established via X3DH:
1. **Ratchet Step**: Each message derivation step uses an HMAC-SHA256 ratchet step to produce distinct symmetric keys for message encryption (`AES-GCM-256`).
2. **Forward Secrecy**: Message keys are erased immediately after decryption, ensuring past messages cannot be decrypted even if long-term keys are compromised later.
3. **One-Time Prekey Consumption**: After receiving an initial message containing an $OPK_{ID}$, the recipient server marks the one-time prekey as `used` to prevent reuse.

## 5. Storage Schema
The underlying PostgreSQL database stores prekey bundles under the following tables:
- `x3dh_identity_keys`: User identity public keys
- `x3dh_signed_prekeys`: Active signed prekeys and Ed25519 signatures
- `x3dh_one_time_prekeys`: One-time prekey pool and usage flags (`used = TRUE`)
