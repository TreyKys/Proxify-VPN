// Command edge-agent runs on each edge server and applies the peer set the
// control plane asks for.
//
// It is deliberately small and boring. The one thing it must never do is lose
// track of reality: it reports an instance ID so the control plane can tell a
// rebuilt box from a running one, and it persists every change so a reboot
// doesn't quietly disconnect everyone.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/treykys/proxify-vpn/edge/internal/agent"
)

var version = "dev"

func main() {
	var (
		addr      = flag.String("addr", "127.0.0.1:8443", "listen address")
		iface     = flag.String("interface", "wg0", "WireGuard interface")
		tokenFile = flag.String("token-file", "/etc/proxify/agent.token", "shared secret file")
		stateDir  = flag.String("state-dir", "/var/lib/proxify-edge", "state directory")
		certFile  = flag.String("tls-cert", "", "TLS certificate (optional if behind a TLS terminator)")
		keyFile   = flag.String("tls-key", "", "TLS key")
	)
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	token, err := agent.ReadTokenFile(*tokenFile)
	if err != nil {
		log.Error("read token", "path", *tokenFile, "err", err)
		os.Exit(1)
	}

	a, err := agent.New(agent.Options{
		Interface: *iface,
		Token:     token,
		StateDir:  *stateDir,
		Version:   version,
		Logger:    log,
	})
	if err != nil {
		log.Error("start agent", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       agent.ReadTimeout,
		WriteTimeout:      agent.WriteTimeout,
		IdleTimeout:       agent.IdleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("edge agent listening", "addr", *addr, "interface", *iface, "version", version)

	if *certFile != "" && *keyFile != "" {
		err = srv.ListenAndServeTLS(*certFile, *keyFile)
	} else {
		// No TLS here means something else must be terminating it. Binding to
		// loopback by default keeps a misconfiguration from exposing peer
		// management to the internet.
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
