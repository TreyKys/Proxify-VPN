// Command api runs the Proxify control plane: the HTTP API plus the peer
// reconciler that keeps edge servers matching the database.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/api"
	"github.com/treykys/proxify-vpn/server/internal/auth"
	"github.com/treykys/proxify-vpn/server/internal/config"
	"github.com/treykys/proxify-vpn/server/internal/edge"
	"github.com/treykys/proxify-vpn/server/internal/payments/paystack"
	"github.com/treykys/proxify-vpn/server/internal/provision"
	"github.com/treykys/proxify-vpn/server/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	edgeClient := edge.NewHTTPClient(cfg.EdgeTimeout)
	provisioner := provision.New(db, edgeClient, log)
	reconciler := provision.NewReconciler(provisioner)

	srv := api.NewServer(api.Deps{
		Config:     cfg,
		Store:      db,
		Provision:  provisioner,
		Reconciler: reconciler,
		Signer:     auth.NewSigner(cfg.JWTSecret, cfg.AccessTokenTTL),
		Paystack:   paystack.New(cfg.PaystackSecretKey, cfg.PaystackWebhookKey),
		Logger:     log,
		AdminToken: os.Getenv("PROXIFY_ADMIN_TOKEN"),
	})

	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Handler(),
		// Generous read timeouts: clients are on bad mobile links and a slow
		// request body is normal here, not an attack signal.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Reconciler and health sweep run in-process. At our scale that is the
	// right call; when we outgrow one control-plane box they become their own
	// deployment without any code change, because both are already driven by
	// SKIP LOCKED claims rather than in-memory state.
	go reconciler.Run(ctx, cfg.ReconcileInterval)
	go runHealthSweep(ctx, reconciler, log)

	errCh := make(chan error, 1)
	go func() {
		log.Info("control plane listening", "addr", cfg.Addr, "env", cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func runHealthSweep(ctx context.Context, r *provision.Reconciler, log *slog.Logger) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			if err := r.HealthSweep(sweepCtx); err != nil {
				log.Error("health sweep", "err", err)
			}
			cancel()
		}
	}
}
