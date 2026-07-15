package managedagents

import (
	"context"
	"time"
)

type SecurityAuditOutboxContextStore interface {
	RecordSecurityAuditOutboxContext(context.Context, RecordSecurityAuditOutboxInput) (SecurityAuditOutboxEvent, error)
	ClaimSecurityAuditOutboxContext(context.Context, ClaimSecurityAuditOutboxInput) ([]SecurityAuditOutboxEvent, error)
	CompleteSecurityAuditOutboxContext(context.Context, CompleteSecurityAuditOutboxInput) (int, error)
	FailSecurityAuditOutboxContext(context.Context, FailSecurityAuditOutboxInput) (int, error)
	ReplaySecurityAuditDeadLettersContext(context.Context, ReplaySecurityAuditDeadLettersInput) (int, error)
	GetSecurityAuditOutboxStatsContext(context.Context, time.Time) (SecurityAuditOutboxStats, error)
	ListSecurityAuditIntegrityKeyStatsContext(context.Context) ([]SecurityAuditIntegrityKeyStats, error)
	PruneDeliveredSecurityAuditOutboxContext(context.Context, time.Time, int) (int, error)
}

func RecordSecurityAuditOutboxWithContext(ctx context.Context, store SecurityAuditOutboxStore, input RecordSecurityAuditOutboxInput) (SecurityAuditOutboxEvent, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.RecordSecurityAuditOutboxContext(ctx, input)
	}
	return store.RecordSecurityAuditOutbox(input)
}

func ClaimSecurityAuditOutboxWithContext(ctx context.Context, store SecurityAuditOutboxStore, input ClaimSecurityAuditOutboxInput) ([]SecurityAuditOutboxEvent, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.ClaimSecurityAuditOutboxContext(ctx, input)
	}
	return store.ClaimSecurityAuditOutbox(input)
}

func CompleteSecurityAuditOutboxWithContext(ctx context.Context, store SecurityAuditOutboxStore, input CompleteSecurityAuditOutboxInput) (int, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.CompleteSecurityAuditOutboxContext(ctx, input)
	}
	return store.CompleteSecurityAuditOutbox(input)
}

func FailSecurityAuditOutboxWithContext(ctx context.Context, store SecurityAuditOutboxStore, input FailSecurityAuditOutboxInput) (int, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.FailSecurityAuditOutboxContext(ctx, input)
	}
	return store.FailSecurityAuditOutbox(input)
}

func ReplaySecurityAuditDeadLettersWithContext(ctx context.Context, store SecurityAuditOutboxStore, input ReplaySecurityAuditDeadLettersInput) (int, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.ReplaySecurityAuditDeadLettersContext(ctx, input)
	}
	return store.ReplaySecurityAuditDeadLetters(input)
}

func GetSecurityAuditOutboxStatsWithContext(ctx context.Context, store SecurityAuditOutboxStore, now time.Time) (SecurityAuditOutboxStats, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.GetSecurityAuditOutboxStatsContext(ctx, now)
	}
	return store.GetSecurityAuditOutboxStats(now)
}

func ListSecurityAuditIntegrityKeyStatsWithContext(ctx context.Context, store SecurityAuditOutboxStore) ([]SecurityAuditIntegrityKeyStats, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.ListSecurityAuditIntegrityKeyStatsContext(ctx)
	}
	return store.ListSecurityAuditIntegrityKeyStats()
}

func PruneDeliveredSecurityAuditOutboxWithContext(ctx context.Context, store SecurityAuditOutboxStore, before time.Time, limit int) (int, error) {
	if scoped, ok := store.(SecurityAuditOutboxContextStore); ok {
		return scoped.PruneDeliveredSecurityAuditOutboxContext(ctx, before, limit)
	}
	return store.PruneDeliveredSecurityAuditOutbox(before, limit)
}
