package core

// AdminAPI mutates server-side engine state: it adds and removes clients on
// existing inbounds, and adds and removes whole inbounds. Implementations
// today come in two flavours that share this interface:
//
//   - file-backed: writes engine config files on disk under config_dir.
//     Used when xray_api_addr is unset (Mode 1).
//   - gRPC-backed: calls the engine's runtime admin RPC (HandlerService for
//     Xray on 127.0.0.1:10085). Used when xray_api_addr is set (Mode 2).
//
// Both backends produce identical StoredClientConfig output for the same
// (inbound, identity) pair. This is enforced by the cross-impl equivalence
// test introduced in Phase 4.
type AdminAPI interface {
	// AddClient creates a new client on the given inbound and returns the
	// stored client-side credential JSON the API layer persists in the
	// user_clients table.
	AddClient(inboundTag, identity string, hint AddClientHint) (storedConfigJSON string, err error)

	// AddExistingClient re-adds a client whose stored credentials already
	// exist (e.g. on engine restart, or when a user is moved between inbounds).
	AddExistingClient(inboundTag, identity, storedConfigJSON string) error

	// RemoveClient removes a client by identity (Xray "email" field).
	RemoveClient(inboundTag, identity string) error

	// AddInboundFromJSON inserts a fully-formed engine-native inbound. Used
	// by the emergency rotation killswitch to bring up the fallback inbound.
	AddInboundFromJSON(rawJSON string) error

	// RemoveInbound removes an inbound by tag. The engine drops active
	// connections on that inbound's listener.
	RemoveInbound(tag string) error

	// Engine returns a stable identifier for the backing engine implementation
	// ("xray" today). Surfaced in /api/sync/status and engine-related metrics.
	Engine() string

	// Version returns the running engine version (e.g. "v26.3.27"). Empty
	// string is permitted when version is not introspectable from the backend.
	Version() string
}

// AddClientHint carries optional engine-private metadata for AddClient. None
// of the fields are required; an engine that doesn't recognise a hint must
// ignore it.
type AddClientHint struct {
	// ProtocolFallback is the inbound's protocol when the parser could not
	// derive one (e.g. corrupt config). Engines use this only as a last
	// resort; first preference is always the protocol read from the inbound's
	// stored config.
	ProtocolFallback string

	// ClientEncryption is the per-tag VLESS Encryption client string
	// (cfg.VLESSClientEncryption[tag]). Non-VLESS engines ignore this field.
	ClientEncryption string
}
