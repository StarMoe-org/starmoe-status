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

	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/auth"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/httpapi"
	"github.com/StarMoe-org/starmoe-status/server/internal/ingest"
	"github.com/StarMoe-org/starmoe-status/server/internal/prober"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
	"github.com/StarMoe-org/starmoe-status/server/webdist"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("failed to open sqlite store", "db_path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := st.SyncMonitors(ctx, cfg.Monitors); err != nil {
		logger.Error("failed to sync monitors", "err", err)
		os.Exit(1)
	}

	au := auth.New(cfg)
	al := alert.New(st, cfg, logger)
	pr := prober.New(st, al, cfg, logger)
	in := ingest.New(st, al, cfg, logger)
	srv := httpapi.New(cfg, st, au, al, in, webdist.FS, logger)

	go pr.Run(ctx)
	go in.RunDeadMansSwitch(ctx)
	go runPruner(ctx, st, logger)
	if cfg.HealthcheckPingURL != "" {
		go runHealthcheckPing(ctx, cfg.HealthcheckPingURL, logger)
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starmoe-status server starting", "addr", cfg.ListenAddr, "monitors", len(cfg.Monitors))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down server")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	case err := <-errCh:
		if err != nil {
			logger.Error("http server error", "err", err)
			os.Exit(1)
		}
	}
}

func runPruner(ctx context.Context, st *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := st.Prune(ctx, time.Now()); err != nil {
				logger.Error("sqlite prune failed", "err", err)
			} else {
				logger.Info("sqlite prune completed")
			}
		}
	}
}

func runHealthcheckPing(ctx context.Context, pingURL string, logger *slog.Logger) {
	client := &http.Client{Timeout: 8 * time.Second}
	ping := func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			logger.Warn("healthcheck ping failed", "err", err)
			return
		}
		_ = resp.Body.Close()
	}

	ping()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ping()
		}
	}
}
