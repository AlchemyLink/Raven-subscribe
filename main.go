// Package main is the entry point for the xray-subscription service.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alchemylink/raven-subscribe/internal/api"
	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/models"
	"github.com/alchemylink/raven-subscribe/internal/syncer"
	"github.com/alchemylink/raven-subscribe/internal/xray"

	"google.golang.org/grpc/credentials"
)

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// ── Config ──────────────────────────────────────────────────────────────
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}
	log.Printf("Server host: %s | Config dir: %s | Listen: %s",
		cfg.ServerHost, cfg.ConfigDir, cfg.ListenAddr)

	// ── Node transport security (multi-node mTLS, Phase 5) ────────────────────
	// Build the per-node gRPC credentials before anything can dial a node. A
	// node with a tls block dials over mTLS; every other api_addr stays
	// plaintext. Fail closed: a configured-but-broken cert must never silently
	// fall back to plaintext against a public HandlerService.
	configureNodeCredentials(cfg)

	// ── Database ────────────────────────────────────────────────────────────
	db, err := database.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("Database error: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("DB close error: %v", err)
		}
	}()
	log.Printf("Database: %s", cfg.DBPath)

	// ── Multi-node topology (Phase 1) ─────────────────────────────────────────
	// Reconcile the configured nodes into the DB and place any unplaced users on
	// the enabled nodes. Single-node deployments get one implicit "local" node
	// and every user on it — behaviour unchanged, data ready for later phases.
	// Non-fatal: nothing downstream consumes node placement yet.
	if err := reconcileNodes(cfg, db); err != nil {
		log.Printf("Node reconcile warning: %v", err)
	}

	// ── Syncer ──────────────────────────────────────────────────────────────
	sync := syncer.New(cfg, db)

	// Verify we can actually write into config.d before claiming we're
	// healthy. A failure here means every periodic SyncDBToConfig will fail
	// the same way and newly-created users will silently never reach xray
	// — surface it loudly so monitoring/admins notice on day zero rather
	// than after a user complaint.
	sync.Probe()
	if status := sync.Status(); !status.ProbeOK {
		log.Printf("ERROR: config.d write probe failed: %s — newly-created users will not reach xray until fixed", status.ProbeError)
	}

	log.Println("Running initial sync...")
	if err := sync.Sync(); err != nil {
		log.Printf("Initial sync warning: %v", err)
	}

	// Restore API-created users to Xray after restart
	sync.RestoreOnStartup()

	// API server needs sync capability
	srv := api.NewServer(cfg, db, sync)

	// Apply current killswitch state to Xray inbounds via gRPC (idempotent).
	// When killswitch is disabled but Xray loaded the fallback inbound from its
	// config files on its own startup, this removes them so the listener state
	// matches the DB flag. Safe no-op when xray_api_addr or fallback tags unset.
	srv.ReconcileKillSwitchOnStartup()

	// Start background sync
	syncCtx, syncCancel := context.WithCancel(context.Background())
	defer syncCancel()
	go sync.Start(syncCtx)

	// Periodic killswitch reconcile — catches xray restarts that reload
	// fallback inbounds from config.d while the killswitch is OFF in the DB.
	// See internal/api/fallback.go ReconcileKillSwitchLoop for the full rationale.
	go srv.ReconcileKillSwitchLoop(syncCtx, time.Duration(cfg.KillSwitchReconcileInterval)*time.Second)

	// Multi-node reconcile: re-apply DB placement onto remote gRPC nodes every
	// sync interval (recovery after a node restart wipes its in-memory users).
	// No-op in single-node mode.
	go srv.ReconcileNodesLoop(syncCtx, time.Duration(cfg.SyncInterval)*time.Second)

	// ── HTTP Server ─────────────────────────────────────────────────────────
	httpServer := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      srv.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Listening on %s", cfg.ListenAddr)
		log.Printf("Subscription URL format: %s/sub/<token>", cfg.BaseURL)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// ── Metrics Server (separate listener — never exposed via the sub vhost) ──
	var metricsServer *http.Server
	if cfg.MetricsListen != "" {
		mm := http.NewServeMux()
		mm.HandleFunc("/metrics", srv.MetricsHandler())
		metricsServer = &http.Server{
			Addr:         cfg.MetricsListen,
			Handler:      mm,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		go func() {
			log.Printf("Metrics listening on %s/metrics", cfg.MetricsListen)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Metrics server error: %v", err)
			}
		}()
	}

	// ── Graceful Shutdown ───────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down...")
	syncCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	if metricsServer != nil {
		if err := metricsServer.Shutdown(ctx); err != nil {
			log.Printf("Metrics shutdown error: %v", err)
		}
	}
	log.Println("Stopped")
}

// configureNodeCredentials builds mTLS transport credentials for every node
// that declares a tls block and installs them on the xray dialer keyed by
// api_addr. Nodes without a tls block (WireGuard/loopback) keep dialing
// plaintext. A cert that fails to load is fatal — with a public api_addr the
// only safe alternative to mTLS is refusing to start, never a plaintext dial.
func configureNodeCredentials(cfg *config.Config) {
	creds := make(map[string]credentials.TransportCredentials)
	for _, n := range cfg.Nodes {
		if n.TLS == nil {
			continue
		}
		serverName := n.TLS.ServerName
		if serverName == "" {
			if host, _, err := net.SplitHostPort(n.APIAddr); err == nil {
				serverName = host
			} else {
				serverName = n.APIAddr
			}
		}
		c, err := xray.BuildTLSCredentials(n.TLS.CACert, n.TLS.ClientCert, n.TLS.ClientKey, serverName)
		if err != nil {
			log.Fatalf("Node %q mTLS: %v", n.Name, err)
		}
		creds[n.APIAddr] = c
		log.Printf("Node %q: mTLS enabled (server_name=%s)", n.Name, serverName)
	}
	xray.SetNodeCredentials(creds)
}

// reconcileNodes syncs the resolved node topology from config into the DB and
// backfills user placement. It maps config.NodeConfig (which also carries
// provisioning-only fields like deploy/allow_public_grpc) down to the DB's
// models.Node, reconciles by name, then places any unplaced users on the
// enabled nodes. See docs/multi-node-design.md §5.
func reconcileNodes(cfg *config.Config, db *database.DB) error {
	resolved := cfg.ResolvedNodes()
	desired := make([]models.Node, 0, len(resolved))
	for _, n := range resolved {
		desired = append(desired, models.Node{
			Name:       n.Name,
			APIAddr:    n.APIAddr,
			InboundTag: n.InboundTag,
			PublicHost: n.PublicHost,
			PublicPort: n.PublicPort,
			Enabled:    n.IsEnabled(),
		})
	}
	if err := db.ReconcileNodes(desired); err != nil {
		return err
	}
	return db.BackfillUserNodesToEnabled()
}
