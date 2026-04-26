// Package main is the entry point for the xray-subscription service.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alchemylink/raven-subscribe/internal/api"
	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/syncer"
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

	// ── Syncer ──────────────────────────────────────────────────────────────
	sync := syncer.New(cfg, db)
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
	log.Println("Stopped")
}
