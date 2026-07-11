package xray

import (
	"errors"
	"fmt"
	"os"

	"github.com/alchemylink/raven-subscribe/internal/core"
)

// grpcAdmin implements core.AdminAPI by calling Xray's gRPC HandlerService
// (Mode 2 — xray_api_addr set). RemoveClient also cleans up the client from
// the on-disk config file, mirroring the dual-write the API layer already
// performed by hand: additions only ever reach Xray at runtime, but removals
// belt-and-suspenders the config file too, so a client added via gRPC that
// happened to also be present on disk (e.g. after a restore) doesn't
// resurrect after an Xray restart reloads config.d.
//
// When configDir is empty the admin runs in grpc-only mode: it never touches
// local config files. This is used for remote fanout nodes (multi-node), whose
// config.d is not on this host — durability there comes from the per-node
// reconcile loop, not from a local file mirror (see docs/multi-node-design.md §8).
type grpcAdmin struct {
	apiAddr   string
	configDir string
	filePerm  os.FileMode
}

var _ core.AdminAPI = (*grpcAdmin)(nil)

// NewGRPCAdmin returns a core.AdminAPI backed by Xray's gRPC HandlerService at
// apiAddr. A non-empty configDir enables the config-file mirror in RemoveClient
// (and the protocol lookup in AddClient); an empty configDir makes the admin
// grpc-only (remote fanout node).
func NewGRPCAdmin(apiAddr, configDir string, filePerm os.FileMode) core.AdminAPI {
	return &grpcAdmin{apiAddr: apiAddr, configDir: configDir, filePerm: filePerm}
}

func (a *grpcAdmin) AddClient(inboundTag, identity string, hint core.AddClientHint) (string, error) {
	return AddClientToInboundViaAPI(a.apiAddr, a.configDir, inboundTag, identity, hint.ProtocolFallback, hint.ClientEncryption)
}

func (a *grpcAdmin) AddExistingClient(inboundTag, identity, storedConfigJSON string) error {
	return AddExistingClientToInboundViaAPI(a.apiAddr, inboundTag, identity, storedConfigJSON)
}

func (a *grpcAdmin) RemoveClient(inboundTag, identity string) error {
	apiErr := RemoveUserFromInboundViaAPI(a.apiAddr, inboundTag, identity)
	if a.configDir == "" {
		return apiErr // grpc-only: no local config.d to mirror
	}
	fileErr := RemoveUserFromInbound(a.configDir, inboundTag, identity, a.filePerm)
	return errors.Join(apiErr, fileErr)
}

func (a *grpcAdmin) AddInboundFromJSON(rawJSON string) error {
	return AddInboundFromJSONViaAPI(a.apiAddr, rawJSON)
}

func (a *grpcAdmin) RemoveInbound(tag string) error {
	return RemoveInboundViaAPI(a.apiAddr, tag)
}

func (a *grpcAdmin) Engine() string { return "xray" }

func (a *grpcAdmin) Version() string { return "" }

// fileAdmin implements core.AdminAPI by writing directly to Xray's on-disk
// config.d files (Mode 1 — xray_api_addr unset). It has no way to add or
// remove whole inbounds at runtime — that requires the gRPC HandlerService —
// so those two methods return an error rather than silently no-op.
type fileAdmin struct {
	configDir string
	filePerm  os.FileMode
}

var _ core.AdminAPI = (*fileAdmin)(nil)

// NewFileAdmin returns a core.AdminAPI backed by direct writes to Xray config
// files under configDir.
func NewFileAdmin(configDir string, filePerm os.FileMode) core.AdminAPI {
	return &fileAdmin{configDir: configDir, filePerm: filePerm}
}

func (a *fileAdmin) AddClient(inboundTag, identity string, hint core.AddClientHint) (string, error) {
	return AddClientToInbound(a.configDir, inboundTag, identity, a.filePerm, hint.ClientEncryption)
}

func (a *fileAdmin) AddExistingClient(inboundTag, identity, storedConfigJSON string) error {
	return AddExistingClientToInbound(a.configDir, inboundTag, identity, storedConfigJSON, a.filePerm)
}

func (a *fileAdmin) RemoveClient(inboundTag, identity string) error {
	return RemoveUserFromInbound(a.configDir, inboundTag, identity, a.filePerm)
}

func (a *fileAdmin) AddInboundFromJSON(_ string) error {
	return fmt.Errorf("xray: AddInboundFromJSON requires xray_api_addr (gRPC); file-only mode cannot add runtime inbounds")
}

func (a *fileAdmin) RemoveInbound(_ string) error {
	return fmt.Errorf("xray: RemoveInbound requires xray_api_addr (gRPC); file-only mode cannot remove runtime inbounds")
}

func (a *fileAdmin) Engine() string { return "xray" }

func (a *fileAdmin) Version() string { return "" }
