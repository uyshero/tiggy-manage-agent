package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

const defaultHTTPAddr = ":8090"

type runtimeServerConfig struct {
	httpAddr              string
	healthHTTPAddr        string
	auth                  modelruntime.AuthConfig
	tlsMode               string
	tlsServerCertFile     string
	tlsServerKeyFile      string
	tlsClientCAFile       string
	backpressureThreshold time.Duration
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := runtimeConfigFromEnv()
	if err != nil {
		logger.Error("configure model runtime failed", "error", err)
		os.Exit(1)
	}
	metrics := modelruntime.NewRuntimeMetrics(config.backpressureThreshold)
	handler, err := modelruntime.NewHandler(modelruntime.HandlerConfig{
		Auth:    config.auth,
		Metrics: metrics,
	})
	if err != nil {
		logger.Error("configure model runtime failed", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr: config.httpAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 90 * time.Second,
	}
	if config.tlsMode == modelruntime.TLSModeMTLS {
		server.TLSConfig, err = modelruntime.NewServerTLSConfig(config.tlsClientCAFile)
		if err != nil {
			logger.Error("configure model runtime mTLS failed", "error", err)
			os.Exit(1)
		}
	}
	type namedServerError struct {
		name string
		err  error
	}
	serverError := make(chan namedServerError, 2)
	go func() {
		logger.Info("model runtime listening", "addr", config.httpAddr, "tls_mode", config.tlsMode, "auth_mode", config.auth.Mode)
		var serveErr error
		if config.tlsMode == modelruntime.TLSModeMTLS {
			serveErr = server.ListenAndServeTLS(config.tlsServerCertFile, config.tlsServerKeyFile)
		} else {
			serveErr = server.ListenAndServe()
		}
		serverError <- namedServerError{name: "runtime", err: serveErr}
	}()
	var healthServer *http.Server
	if config.healthHTTPAddr != "" {
		healthServer = &http.Server{
			Addr: config.healthHTTPAddr, Handler: modelruntime.NewHealthHandler(metrics),
			ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		}
		go func() {
			logger.Info("model runtime health endpoint listening", "addr", config.healthHTTPAddr)
			serverError <- namedServerError{name: "health", err: healthServer.ListenAndServe()}
		}()
	}

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
		if healthServer != nil {
			if err := healthServer.Shutdown(shutdownContext); err != nil {
				logger.Error("model runtime health endpoint shutdown failed", "error", err)
				os.Exit(1)
			}
		}
	case result := <-serverError:
		if result.err != nil && !errors.Is(result.err, http.ErrServerClosed) {
			logger.Error("model runtime failed", "server", result.name, "error", result.err)
			os.Exit(1)
		}
	}
}

func runtimeConfigFromEnv() (runtimeServerConfig, error) {
	config := runtimeServerConfig{
		httpAddr:          envOrDefault("TMA_MODEL_RUNTIME_HTTP_ADDR", defaultHTTPAddr),
		healthHTTPAddr:    strings.TrimSpace(os.Getenv("TMA_MODEL_RUNTIME_HEALTH_HTTP_ADDR")),
		tlsMode:           strings.ToLower(strings.TrimSpace(envOrDefault("TMA_MODEL_RUNTIME_TLS_MODE", modelruntime.TLSModeDisabled))),
		tlsServerCertFile: strings.TrimSpace(os.Getenv("TMA_MODEL_RUNTIME_TLS_SERVER_CERT_FILE")),
		tlsServerKeyFile:  strings.TrimSpace(os.Getenv("TMA_MODEL_RUNTIME_TLS_SERVER_KEY_FILE")),
		tlsClientCAFile:   strings.TrimSpace(os.Getenv("TMA_MODEL_RUNTIME_TLS_CLIENT_CA_FILE")),
	}
	tokenTTL, err := integerFromEnv("TMA_MODEL_RUNTIME_TOKEN_TTL_SECONDS", 60, 1, 300)
	if err != nil {
		return runtimeServerConfig{}, err
	}
	backpressure, err := integerFromEnv("TMA_MODEL_RUNTIME_BACKPRESSURE_THRESHOLD_MS", 100, 1, 60000)
	if err != nil {
		return runtimeServerConfig{}, err
	}
	config.auth = modelruntime.AuthConfig{
		Mode:     strings.ToLower(strings.TrimSpace(envOrDefault("TMA_MODEL_RUNTIME_AUTH_MODE", modelruntime.AuthModeStatic))),
		Secret:   os.Getenv("TMA_MODEL_RUNTIME_AUTH_TOKEN"),
		Issuer:   envOrDefault("TMA_MODEL_RUNTIME_TOKEN_ISSUER", "tma-server"),
		Audience: envOrDefault("TMA_MODEL_RUNTIME_TOKEN_AUDIENCE", "tma-model-runtime"),
		TokenTTL: time.Duration(tokenTTL) * time.Second,
	}
	config.backpressureThreshold = time.Duration(backpressure) * time.Millisecond
	if config.healthHTTPAddr != "" && config.healthHTTPAddr == config.httpAddr {
		return runtimeServerConfig{}, errors.New("model runtime health and primary addresses must differ")
	}
	switch config.tlsMode {
	case modelruntime.TLSModeDisabled, modelruntime.TLSModeServiceMesh:
	case modelruntime.TLSModeMTLS:
		if config.tlsServerCertFile == "" || config.tlsServerKeyFile == "" || config.tlsClientCAFile == "" {
			return runtimeServerConfig{}, errors.New("model runtime mTLS requires server certificate, server key, and client CA files")
		}
	default:
		return runtimeServerConfig{}, fmt.Errorf("unsupported TMA_MODEL_RUNTIME_TLS_MODE %q", config.tlsMode)
	}
	environment := strings.ToLower(strings.TrimSpace(envOrDefault("TMA_ENV", "development")))
	if environment == "production" || environment == "prod" {
		if config.auth.Mode != modelruntime.AuthModeSigned {
			return runtimeServerConfig{}, errors.New("TMA_MODEL_RUNTIME_AUTH_MODE must be signed in production")
		}
		if config.tlsMode != modelruntime.TLSModeMTLS && config.tlsMode != modelruntime.TLSModeServiceMesh {
			return runtimeServerConfig{}, errors.New("TMA_MODEL_RUNTIME_TLS_MODE must be mtls or service_mesh in production")
		}
	}
	return config, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func integerFromEnv(key string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}
