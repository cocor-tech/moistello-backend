package indexer

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/stellar/go/xdr"
)

// maxPayloadSnippet bounds how much raw payload is attached to a log line.
const maxPayloadSnippet = 128

// payloadSnippet truncates s so failure logs carry enough of the raw payload
// for triage without dumping an entire event.
func payloadSnippet(s string) string {
	if len(s) > maxPayloadSnippet {
		return s[:maxPayloadSnippet] + "..."
	}
	return s
}

// eventTypeHash returns a stable hex digest identifying an event type from
// its raw bytes (or its name), so undecodable events can still be grouped.
func eventTypeHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// logSkippedEvent records why a contract event could not be decoded, with the
// contract, event type hash, ledger, and a raw payload snippet.
func logSkippedEvent(reason, txHash string, ledger int64, contractID string, raw xdr.ContractEvent, topics []xdr.ScVal) {
	var typeHash, snippet string
	if len(topics) > 0 {
		if b, err := topics[0].MarshalBinary(); err == nil {
			typeHash = eventTypeHash(b)
		}
	}
	if b, err := raw.MarshalBinary(); err == nil {
		snippet = payloadSnippet(base64.StdEncoding.EncodeToString(b))
	}
	log.Warn().
		Str("reason", reason).
		Str("contract", contractID).
		Str("event_type_hash", typeHash).
		Int64("ledger", ledger).
		Str("hash", txHash).
		Str("payload", snippet).
		Msg("skipping undecodable contract event")
}

// ParseContractEvents decodes the base64-encoded result_meta_xdr from a Stellar
// transaction and extracts all Soroban contract events contained within. Each
// returned ContractEvent is fully decoded with its EventType and Payload fields
// populated from the raw SCVal topics and data.
//
// Malformed or unrecognised XDR is silently skipped — the function returns
// whatever events could be successfully decoded along with the first error
// encountered (if any). Callers should treat partial results as valid.
func ParseContractEvents(txHash string, ledger int64, resultMetaXDR string) ([]ContractEvent, error) {
	if resultMetaXDR == "" {
		return nil, nil
	}

	raw, err := base64.StdEncoding.DecodeString(resultMetaXDR)
	if err != nil {
		return nil, fmt.Errorf("base64 decode result_meta_xdr: %w", err)
	}

	var meta xdr.TransactionMeta
	if _, err := xdr.Unmarshal(bytes.NewReader(raw), &meta); err != nil {
		return nil, fmt.Errorf("xdr unmarshal TransactionMeta: %w", err)
	}

	// Soroban contract events live in V3 metadata.
	v3 := meta.V3
	if v3 == nil {
		return nil, nil
	}

	var events []ContractEvent
	for _, diagEvent := range v3.SorobanMeta.Events {
		ev, ok := decodeContractEvent(txHash, ledger, diagEvent)
		if !ok {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

// decodeContractEvent converts a raw xdr.ContractEvent into a typed ContractEvent.
// Returns (event, true) on success or (zero, false) if the event cannot be decoded.
func decodeContractEvent(txHash string, ledger int64, raw xdr.ContractEvent) (ContractEvent, bool) {
	// Only process contract-type events (not system or diagnostic).
	if raw.Type != xdr.ContractEventTypeContract {
		return ContractEvent{}, false
	}

	contractID := ""
	if raw.ContractId != nil {
		h := *raw.ContractId
		contractID = fmt.Sprintf("%x", h[:])
	}

	body, ok := raw.Body.GetV0()
	if !ok {
		logSkippedEvent("unsupported event body version", txHash, ledger, contractID, raw, nil)
		return ContractEvent{}, false
	}

	topics := body.Topics
	if len(topics) == 0 {
		logSkippedEvent("event has no topics", txHash, ledger, contractID, raw, topics)
		return ContractEvent{}, false
	}

	eventType := decodeSymbol(topics[0])
	if eventType == "" {
		logSkippedEvent("event type topic is not a symbol or string", txHash, ledger, contractID, raw, topics)
		return ContractEvent{}, false
	}

	payload := decodePayload(topics[1:], body.Data)

	return ContractEvent{
		ContractID: contractID,
		EventType:  eventType,
		Ledger:     ledger,
		TxHash:     txHash,
		Payload:    payload,
	}, true
}

// decodeSymbol extracts a string from an SCVal of type SCV_SYMBOL or SCV_STRING.
func decodeSymbol(v xdr.ScVal) string {
	switch v.Type {
	case xdr.ScValTypeScvSymbol:
		if sym, ok := v.GetSym(); ok {
			return string(sym)
		}
	case xdr.ScValTypeScvString:
		if s, ok := v.GetStr(); ok {
			return string(s)
		}
	}
	return ""
}

// decodePayload converts the remaining event topics and data SCVal into a flat
// map suitable for storage as JSONB. Keys are positional ("topic_1", "topic_2", …)
// for remaining topics, and "data" for the event data field.
//
// Named fields are extracted where the on-chain convention encodes them as
// alternating key-value symbol pairs within the topics list.
func decodePayload(remainingTopics []xdr.ScVal, data xdr.ScVal) map[string]any {
	payload := make(map[string]any)

	for i, topic := range remainingTopics {
		key := fmt.Sprintf("topic_%d", i+1)
		payload[key] = scValToGo(topic)
	}

	dataVal := scValToGo(data)
	if dataVal != nil {
		payload["data"] = dataVal
	}

	return payload
}

// scValToGo converts an xdr.ScVal to its closest Go-native representation.
// Complex nested types (maps, vecs) are recursively expanded.
func scValToGo(v xdr.ScVal) any {
	switch v.Type {
	case xdr.ScValTypeScvBool:
		b, _ := v.GetB()
		return b

	case xdr.ScValTypeScvSymbol:
		sym, _ := v.GetSym()
		return string(sym)

	case xdr.ScValTypeScvString:
		s, _ := v.GetStr()
		return string(s)

	case xdr.ScValTypeScvU32:
		u, _ := v.GetU32()
		return uint32(u)

	case xdr.ScValTypeScvI32:
		i, _ := v.GetI32()
		return int32(i)

	case xdr.ScValTypeScvU64:
		u, _ := v.GetU64()
		return uint64(u)

	case xdr.ScValTypeScvI64:
		i, _ := v.GetI64()
		return int64(i)

	case xdr.ScValTypeScvU128:
		u, _ := v.GetU128()
		// Represent as decimal string to avoid precision loss in JSON.
		hi := uint64(u.Hi)
		lo := uint64(u.Lo)
		if hi == 0 {
			return strconv.FormatUint(lo, 10)
		}
		return fmt.Sprintf("%d%018d", hi, lo)

	case xdr.ScValTypeScvI128:
		i, _ := v.GetI128()
		hi := int64(i.Hi)
		lo := uint64(i.Lo)
		if hi == 0 {
			return strconv.FormatUint(lo, 10)
		}
		return fmt.Sprintf("%d%018d", hi, lo)

	case xdr.ScValTypeScvAddress:
		addr, _ := v.GetAddress()
		return scAddressToString(addr)

	case xdr.ScValTypeScvBytes:
		b, _ := v.GetBytes()
		return base64.StdEncoding.EncodeToString(b)

	case xdr.ScValTypeScvMap:
		m, _ := v.GetMap()
		if m == nil {
			return nil
		}
		result := make(map[string]any, len(*m))
		for _, entry := range *m {
			k := fmt.Sprintf("%v", scValToGo(entry.Key))
			result[k] = scValToGo(entry.Val)
		}
		return result

	case xdr.ScValTypeScvVec:
		vec, _ := v.GetVec()
		if vec == nil {
			return nil
		}
		result := make([]any, len(*vec))
		for i, elem := range *vec {
			result[i] = scValToGo(elem)
		}
		return result

	case xdr.ScValTypeScvVoid:
		return nil

	default:
		return fmt.Sprintf("<SCVal type=%d>", v.Type)
	}
}

// scAddressToString converts an xdr.ScAddress to a human-readable string
// (Stellar account ID or contract hex).
func scAddressToString(addr xdr.ScAddress) string {
	switch addr.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if addr.AccountId != nil {
			return addr.AccountId.Address()
		}
	case xdr.ScAddressTypeScAddressTypeContract:
		if addr.ContractId != nil {
			return fmt.Sprintf("%x", (*addr.ContractId)[:])
		}
	}
	return ""
}
