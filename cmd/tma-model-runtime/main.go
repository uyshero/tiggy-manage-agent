package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

const defaultHTTPAddr = ":8090"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	addr := strings.TrimSpace(os.Getenv("TMA_MODEL_RUNTIME_HTTP_ADDR"))
	if addr == "" {
		addr = defaultHTTPAddr
	}
	handler, err := modelruntime.NewHandler(modelruntime.HandlerConfig{
		AuthToken: os.Getenv("TMA_MODEL_RUNTIME_AUTH_TOKEN"),
	})
	if err != nil {
		logger.Error("configure model runtime failed", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 90 * time.Second,
	}
	serverError := make(chan error, 1)
	go func() {
		logger.Info("model runtime listening", "addr", addr)
		serverError <- server.ListenAndServe()
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-signalContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("model runtime shutdown failed", "error", err)
			os.Exit(1)
		}
	case err := <-serverError:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("model runtime failed", "error", err)
			os.Exit(1)
		}
	}
}
