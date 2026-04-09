package services

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"tavily-proxy/server/internal/db"
)

func newKeyServiceSelectionTestDeps(t *testing.T) (context.Context, *KeyService) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	return context.Background(), NewKeyService(database, logger)
}

func TestCandidatesByPolicy_FillFirstSticksToLastUsedKey(t *testing.T) {
	t.Parallel()

	ctx, keys := newKeyServiceSelectionTestDeps(t)

	first, err := keys.Create(ctx, "tvly-a", "first", 1000)
	if err != nil {
		t.Fatalf("create first key: %v", err)
	}
	_, err = keys.Create(ctx, "tvly-b", "second", 1000)
	if err != nil {
		t.Fatalf("create second key: %v", err)
	}

	if err := keys.IncrementUsed(ctx, first.ID); err != nil {
		t.Fatalf("increment first key: %v", err)
	}

	candidates, err := keys.CandidatesByPolicy(ctx, KeySelectionPolicyFillFirst)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("unexpected candidate count: got %d want %d", len(candidates), 2)
	}
	if candidates[0].ID != first.ID {
		t.Fatalf("fill-first should keep using first key: got %d want %d", candidates[0].ID, first.ID)
	}
}

func TestCandidatesByPolicy_BalancePrefersHigherRemainingQuota(t *testing.T) {
	t.Parallel()

	ctx, keys := newKeyServiceSelectionTestDeps(t)

	first, err := keys.Create(ctx, "tvly-a", "first", 1000)
	if err != nil {
		t.Fatalf("create first key: %v", err)
	}
	second, err := keys.Create(ctx, "tvly-b", "second", 1000)
	if err != nil {
		t.Fatalf("create second key: %v", err)
	}

	if err := keys.IncrementUsed(ctx, first.ID); err != nil {
		t.Fatalf("increment first key: %v", err)
	}

	candidates, err := keys.CandidatesByPolicy(ctx, KeySelectionPolicyBalance)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("unexpected candidate count: got %d want %d", len(candidates), 2)
	}
	if candidates[0].ID != second.ID {
		t.Fatalf("balance should prefer second key with more remaining quota: got %d want %d", candidates[0].ID, second.ID)
	}
}
