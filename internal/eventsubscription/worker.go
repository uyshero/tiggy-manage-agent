package eventsubscription

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type WorkerConfig struct {
	Store             DeliveryStore
	SigningKey        []byte
	HTTPClient        *http.Client
	LeaseOwner        string
	BatchSize         int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	MaxAttempts       int
	RetryInitialDelay time.Duration
	RetryMaxDelay     time.Duration
	Logger            *slog.Logger
}

type Worker struct {
	config WorkerConfig
}

func NewWorker(config WorkerConfig) (*Worker, error) {
	if config.Store == nil || len(config.SigningKey) < 32 {
		return nil, fmt.Errorf("event delivery store and a 32-byte signing key are required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if strings.TrimSpace(config.LeaseOwner) == "" {
		config.LeaseOwner = fmt.Sprintf("event-worker-%d", time.Now().UnixNano())
	}
	if config.BatchSize < 1 {
		config.BatchSize = 50
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.LeaseDuration <= config.PollInterval {
		config.LeaseDuration = 30 * time.Second
	}
	if config.MaxAttempts < 1 {
		config.MaxAttempts = 8
	}
	if config.RetryInitialDelay <= 0 {
		config.RetryInitialDelay = time.Second
	}
	if config.RetryMaxDelay < config.RetryInitialDelay {
		config.RetryMaxDelay = 5 * time.Minute
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Worker{config: config}, nil
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()
	for {
		w.deliverBatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) deliverBatch(ctx context.Context) {
	deliveries, err := w.config.Store.ClaimEventDeliveries(ClaimInput{
		LeaseOwner: w.config.LeaseOwner, LeaseDuration: w.config.LeaseDuration,
		MaxAttempts: w.config.MaxAttempts, Limit: w.config.BatchSize, Now: time.Now().UTC(),
	})
	if err != nil {
		w.config.Logger.Warn("claim webhook deliveries failed", "error", err)
		return
	}
	for _, delivery := range deliveries {
		if ctx.Err() != nil {
			return
		}
		w.deliver(ctx, delivery)
	}
}

func (w *Worker) deliver(ctx context.Context, delivery Delivery) {
	now := time.Now().UTC()
	secret, err := DeriveSecret(w.config.SigningKey, delivery.SubscriptionID, delivery.SecretVersion)
	if err != nil {
		w.fail(delivery, 0, err, true, now)
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.EndpointURL, bytes.NewReader(delivery.Payload))
	if err != nil {
		w.fail(delivery, 0, err, true, now)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "TMA-Webhook/1.0")
	request.Header.Set("X-TMA-Delivery-ID", delivery.ID)
	request.Header.Set("X-TMA-Event", delivery.EventType)
	request.Header.Set("X-TMA-Timestamp", fmt.Sprintf("%d", now.Unix()))
	request.Header.Set("X-TMA-Signature", Signature(secret, now, delivery.ID, delivery.Payload))
	request.Header.Set("X-TMA-Secret-Version", fmt.Sprintf("%d", delivery.SecretVersion))
	response, err := w.config.HTTPClient.Do(request)
	if err != nil {
		w.fail(delivery, 0, err, false, now)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if _, err := w.config.Store.CompleteEventDelivery(CompleteInput{
			DeliveryID: delivery.ID, LeaseOwner: w.config.LeaseOwner, HTTPStatus: response.StatusCode, Now: now,
		}); err != nil {
			w.config.Logger.Warn("complete webhook delivery failed", "delivery_id", delivery.ID, "error", err)
		}
		return
	}
	w.fail(delivery, response.StatusCode, fmt.Errorf("webhook endpoint returned HTTP %d", response.StatusCode), response.StatusCode == http.StatusGone, now)
}

func (w *Worker) fail(delivery Delivery, status int, cause error, deadLetter bool, now time.Time) {
	delay := w.config.RetryInitialDelay
	for attempt := 1; attempt < delivery.AttemptCount && delay < w.config.RetryMaxDelay; attempt++ {
		delay *= 2
	}
	if delay > w.config.RetryMaxDelay {
		delay = w.config.RetryMaxDelay
	}
	deadLetter = deadLetter || delivery.AttemptCount >= w.config.MaxAttempts
	_, err := w.config.Store.FailEventDelivery(FailInput{
		DeliveryID: delivery.ID, LeaseOwner: w.config.LeaseOwner, HTTPStatus: status,
		Error: cause.Error(), RetryAt: now.Add(delay), DeadLetter: deadLetter, Now: now,
	})
	if err != nil {
		w.config.Logger.Warn("fail webhook delivery update failed", "delivery_id", delivery.ID, "error", err)
	}
}
