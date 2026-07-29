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

	"tiggy-manage-agent/internal/biographyvoice"
	"tiggy-manage-agent/internal/serverconfig"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := serverconfig.LoadDotEnv(".env"); err != nil {
		logger.Error("load biography voice gateway environment", "error", err)
		os.Exit(1)
	}
	config, err := biographyvoice.ConfigFromEnvironment(os.Getenv)
	if err != nil {
		logger.Error("invalid biography voice gateway configuration", "error", err)
		os.Exit(1)
	}
	voiceServer, err := biographyvoice.NewServer(config, logger)
	if err != nil {
		logger.Error("create biography voice gateway", "error", err)
		os.Exit(1)
	}
	httpServer := &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           voiceServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown biography voice gateway", "error", err)
		}
	}()

	logger.Info("biography voice gateway listening", "addr", config.HTTPAddr, "provider", config.Provider)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("biography voice gateway failed", "error", err)
		os.Exit(1)
	}
}
