// Package core defines engine-agnostic interfaces and value types for the
// subscription server. It is the seam between the API/syncer layers and the
// concrete VPN engine implementation (today: internal/xray).
//
// Phase 1 of the internal/core refactor introduces only declarations — no
// code in this repository imports core yet. internal/xray will be wired as
// the first implementation in Phase 2; see docs/internal-core-design.md.
//
// Design rules:
//
//   - core has no dependency on internal/xray.
//   - core types deliberately mirror existing xray field names (RawJSON,
//     ConfigJSON, …) so Phase 2 is a mechanical alias rather than a rename.
//   - REALITY/Vision/XHTTP/mldsa65 are NOT abstracted here. They remain
//     private to the engine implementation. core stays at the level of
//     "inbound, client, identity, routing target" — the lowest-common
//     vocabulary that survives across engines without becoming
//     lowest-common-denominator.
package core

// Inbound is a server-side ingress point exposed by the engine, materialised
// from the engine's on-disk config. The Tag is the engine-level identifier
// (e.g. "vless-reality-v2-in"). RawJSON carries the engine-native config blob
// for that single inbound; nothing outside the engine implementation parses it.
type Inbound struct {
	Tag      string
	Protocol string // "vless" | "vmess" | "trojan" | "shadowsocks" | "socks"
	Port     int
	RawJSON  string
	Clients  []Client
}

// Client is one credential entry attached to an Inbound, as discovered by the
// parser. Identity is the engine-level account label (Xray "email" field —
// usually a username in email form). ConfigJSON is the stored client-side
// credential blob; the format is engine-specific and core does not interpret it.
type Client struct {
	Identity   string
	ConfigJSON string
}

// OutboundLink is a single sharable client URI, ready for inclusion in
// links_txt / links_b64 / QR rendering. The engine implementation produces
// the URI string; the API layer treats it as opaque text.
type OutboundLink struct {
	Tag      string
	Protocol string
	URI      string
}

// View enumerates the wire formats a generated client config can be rendered
// as. The set is intentionally small: any new view requires an explicit
// addition both here and in every engine implementation.
type View int

const (
	// ViewFullJSON is the engine-native JSON config the client app loads.
	// For Xray this is the full root config (inbounds + outbounds + routing).
	ViewFullJSON View = iota

	// ViewLinksTxt is one URI per line, plain text.
	ViewLinksTxt

	// ViewLinksB64 is base64-encoded ViewLinksTxt.
	ViewLinksB64

	// ViewCompact is the compact subscription format served at /c/{token}.
	ViewCompact
)
