// Command mudflaps runs a standalone, stateful local emulator of the Fly.io
// Machines API ("flaps"). Point a Machines-API client at it by setting
// FLY_FLAPS_BASE_URL to the address it listens on (default http://localhost:4280).
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

	"github.com/intentius/mudflaps/internal/server"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// defaultAddr echoes Fly's internal flaps address (_api.internal:4280).
const defaultAddr = ":4280"

func main() {
	if err := run(); err != nil {
		slog.Error("mudflaps exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", envOr("MUDFLAPS_ADDR", defaultAddr), "address to listen on (host:port)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if *showVersion {
		logger.Info("mudflaps", "version", version)
		return nil
	}

	srv := server.New(server.Options{Version: version, Logger: logger})

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("mudflaps listening",
			"addr", *addr,
			"version", version,
			"hint", "set FLY_FLAPS_BASE_URL=http://localhost"+portHint(*addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("mudflaps stopped cleanly")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// portHint renders the ":4280" style suffix for the FLY_FLAPS_BASE_URL hint.
func portHint(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return addr
	}
	if i := lastColon(addr); i >= 0 {
		return addr[i:]
	}
	return ":4280"
}

func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}
