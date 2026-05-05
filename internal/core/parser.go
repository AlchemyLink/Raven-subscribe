package core

// ConfigParser reads an engine's on-disk server config directory and returns
// the inbounds and clients it finds. It is read-only: parsing must not mutate
// any engine state or rewrite files. The fsnotify-driven syncer calls ParseDir
// on every change in the watched directory; GetInboundByTag is a fast path
// for CRUD handlers that need to confirm an inbound exists before adding a
// user to it.
//
// Implementation expectations:
//
//   - clientEncMap is an engine-private hint dictionary keyed by inbound tag.
//     For the Xray implementation it carries the per-tag VLESS Encryption
//     client string (cfg.VLESSClientEncryption). Engines that don't need it
//     ignore the map. core does not interpret values.
//   - The returned map is keyed by file basename (e.g. "200-in-vless-reality.json")
//     so callers can correlate inbounds back to their source file. A single
//     file may contain multiple inbounds.
type ConfigParser interface {
	ParseDir(dir string, clientEncMap map[string]string) (map[string][]Inbound, error)
	GetInboundByTag(dir, tag string) (inbound *Inbound, configFile string, err error)
}
