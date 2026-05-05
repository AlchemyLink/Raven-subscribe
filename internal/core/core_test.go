package core_test

import (
	"go/build"
	"strings"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/core"
)

// TestCoreInvariants pins the Phase-1 promises of the core package:
//
//  1. core declares the four interfaces and their value types.
//  2. core does NOT depend on internal/xray, ever. If somebody adds such an
//     import, this test fails before the build does.
//
// The test does not exercise behaviour — there is none yet. It guards the
// public surface so accidental drift in Phase 2/3 is caught.
func TestCoreInvariants(t *testing.T) {
	t.Run("interfaces are declared", func(t *testing.T) {
		// Compile-time assertions that the four interfaces exist and that
		// nil values can be typed against them. These will fail to compile
		// (caught at `go vet`/`go build` time) if any signature drifts.
		var _ core.ConfigParser
		var _ core.ClientConfigBuilder
		var _ core.AdminAPI
		var _ core.ConfigSyncer
		var _ core.EngineConfig
	})

	t.Run("value types are zero-constructible", func(t *testing.T) {
		_ = core.Inbound{}
		_ = core.Client{}
		_ = core.OutboundLink{}
		_ = core.BuildRequest{}
		_ = core.BalancerSpec{}
		_ = core.LocalProxySpec{}
		_ = core.AddClientHint{}
		_ = core.ClientToRestore{}
		_ = core.SyncResult{}
	})

	t.Run("View enum stable order", func(t *testing.T) {
		// Wire-format ordering is part of the contract: changing iota order
		// would silently swap what /sub/{token} variants render. Pin it.
		if core.ViewFullJSON != 0 || core.ViewLinksTxt != 1 || core.ViewLinksB64 != 2 || core.ViewCompact != 3 {
			t.Fatalf("View enum order changed: %d %d %d %d",
				core.ViewFullJSON, core.ViewLinksTxt, core.ViewLinksB64, core.ViewCompact)
		}
	})

	t.Run("no dependency on internal/xray", func(t *testing.T) {
		pkg, err := build.Default.Import("github.com/alchemylink/raven-subscribe/internal/core", "", 0)
		if err != nil {
			t.Fatalf("import core: %v", err)
		}
		for _, imp := range pkg.Imports {
			if strings.Contains(imp, "/internal/xray") {
				t.Fatalf("core must not depend on internal/xray, got import %q", imp)
			}
		}
	})
}
