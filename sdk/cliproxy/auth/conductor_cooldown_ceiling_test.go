package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestBoundQuotaDeadlineKeepsTheConfiguredBackoff(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		deadline time.Time
		maximum  time.Duration
		level    int
		want     time.Time
	}{
		{name: "zero", want: time.Time{}},
		{name: "past", deadline: now.Add(-time.Minute), maximum: time.Hour, want: now.Add(-time.Minute)},
		{name: "short quota", deadline: now.Add(5 * time.Minute), maximum: time.Hour, want: now.Add(5 * time.Minute)},
		{name: "first bounded window", deadline: now.Add(7 * 24 * time.Hour), maximum: time.Hour, want: now.Add(time.Hour)},
		{name: "escalated window", deadline: now.Add(7 * 24 * time.Hour), maximum: time.Hour, level: 2, want: now.Add(4 * time.Hour)},
		{name: "custom bound", deadline: now.Add(7 * 24 * time.Hour), maximum: 15 * time.Minute, want: now.Add(15 * time.Minute)},
		{name: "disabled bound", deadline: now.Add(7 * 24 * time.Hour), want: now.Add(7 * 24 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := boundQuotaDeadline(tc.deadline, now, tc.maximum, tc.level); !got.Equal(tc.want) {
				t.Fatalf("deadline = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestApplyAuthFailureStateBoundsAnExistingQuotaDeadline(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	stale := now.Add(7 * 24 * time.Hour)
	auth := &Auth{ID: "stale-codex-quota", Provider: "codex", NextRetryAfter: stale, Quota: QuotaState{Exceeded: true, NextRecoverAt: stale}}
	short := 5 * time.Minute
	applyAuthFailureState(auth, &Error{HTTPStatus: http.StatusTooManyRequests}, &short, now, false, time.Hour)
	want := now.Add(time.Hour)
	if !auth.Quota.NextRecoverAt.Equal(want) || !auth.NextRetryAfter.Equal(want) {
		t.Fatalf("stale quota remained active: quota=%s retry=%s, want %s", auth.Quota.NextRecoverAt, auth.NextRetryAfter, want)
	}
}

// A cooldown store written before the ceiling existed can hold a multi-day quota
// deadline. The selector blocks such a credential, so no 429 path can run to re-clamp
// it; unless the deadline is migrated on restore the credential stays latched for its
// original duration even after this fix ships.
func TestRestoreCooldownStates_ClampsPreFixMultiDayQuotaRecord(t *testing.T) {
	now := time.Now()
	sevenDaysOut := now.Add(7 * 24 * time.Hour)

	manager := NewManager(nil, nil, nil)
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{ID: "auth-prefix-record", Provider: "codex"}); errRegister != nil {
		t.Fatalf("Register() returned error: %v", errRegister)
	}
	manager.SetCooldownStateStore(&mockCooldownStateStore{records: []CooldownStateRecord{
		{
			Provider:       "codex",
			AuthID:         "auth-prefix-record",
			NextRetryAfter: sevenDaysOut,
			Reason:         "quota exhausted",
			Quota: QuotaState{
				Exceeded:      true,
				Reason:        "credential_quota",
				NextRecoverAt: sevenDaysOut,
			},
			UpdatedAt: now,
		},
	}})

	if errRestore := manager.RestoreCooldownStates(context.Background()); errRestore != nil {
		t.Fatalf("RestoreCooldownStates() returned error: %v", errRestore)
	}

	restored, ok := manager.GetByID("auth-prefix-record")
	if !ok || restored == nil {
		t.Fatal("restored auth not found")
	}
	checkedAt := time.Now()
	lower := checkedAt.Add(defaultMaxTrustedQuotaCooldown - time.Minute)
	upper := checkedAt.Add(defaultMaxTrustedQuotaCooldown + time.Minute)
	if restored.Quota.NextRecoverAt.Before(lower) || restored.Quota.NextRecoverAt.After(upper) {
		t.Fatalf("restored Quota.NextRecoverAt = %v, want between %v and %v", restored.Quota.NextRecoverAt, lower, upper)
	}
	if restored.NextRetryAfter.Before(lower) || restored.NextRetryAfter.After(upper) {
		t.Fatalf("restored NextRetryAfter = %v, want between %v and %v", restored.NextRetryAfter, lower, upper)
	}
	// The credential must still be cooling; migrating the deadline must not clear it.
	if !restored.Quota.Exceeded {
		t.Fatal("restored Quota.Exceeded = false, want the cooldown preserved")
	}
	if blocked, _, _ := isAuthBlockedForModel(restored, "", checkedAt); !blocked {
		t.Fatal("restored credential is not blocked, want it still cooling until the ceiling")
	}
}

func TestRestoreCooldownStates_BoundsAndPersistsLegacyModelQuota(t *testing.T) {
	now := time.Now()
	legacyNext := now.Add(7 * 24 * time.Hour)
	manager := NewManager(nil, nil, nil)
	if _, err := manager.Register(WithSkipPersist(context.Background()), &Auth{ID: "auth-legacy-model", Provider: "claude"}); err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}
	store := &recordingCooldownStateStore{load: []CooldownStateRecord{{
		Provider: "claude", AuthID: "auth-legacy-model", Model: "claude-opus-5-5",
		NextRetryAfter: legacyNext, Quota: QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: legacyNext},
		LastError: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"}, UpdatedAt: now,
	}}}
	manager.SetCooldownStateStore(store)
	if err := manager.RestoreCooldownStates(context.Background()); err != nil {
		t.Fatalf("RestoreCooldownStates() returned error: %v", err)
	}
	checkedAt := time.Now()
	lower := checkedAt.Add(defaultMaxTrustedQuotaCooldown - time.Minute)
	upper := checkedAt.Add(defaultMaxTrustedQuotaCooldown + time.Minute)
	assertBounded := func(label string, quotaNext, retryNext time.Time) {
		t.Helper()
		if quotaNext.Before(lower) || quotaNext.After(upper) || retryNext.Before(lower) || retryNext.After(upper) {
			t.Fatalf("%s deadlines quota=%s retry=%s, want about one hour", label, quotaNext, retryNext)
		}
	}
	restored, ok := manager.GetByID("auth-legacy-model")
	if !ok || restored == nil {
		t.Fatal("restored auth not found")
	}
	state := existingModelState(restored, "claude-opus-5-5")
	if state == nil {
		t.Fatal("restored model state not found")
	}
	assertBounded("restored", state.Quota.NextRecoverAt, state.NextRetryAfter)
	if blocked, _, _ := isAuthBlockedForModel(restored, "claude-opus-5-5", checkedAt); !blocked {
		t.Fatal("restored model is immediately selectable")
	}
	for _, record := range store.savedRecords() {
		if record.Model == "claude-opus-5-5" {
			assertBounded("persisted", record.Quota.NextRecoverAt, record.NextRetryAfter)
			return
		}
	}
	t.Fatal("bounded model record was not persisted")
}

// A restored record whose retry deadline is longer than its quota deadline carries a
// non-quota cooldown (e.g. a 12h 404). The ceiling must not shorten that.
func TestRestoreCooldownStates_LeavesLongerNonQuotaDeadlineIntact(t *testing.T) {
	now := time.Now()
	quotaNext := now.Add(20 * time.Minute)
	notFoundNext := now.Add(12 * time.Hour)

	manager := NewManager(nil, nil, nil)
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{ID: "auth-mixed-record", Provider: "codex"}); errRegister != nil {
		t.Fatalf("Register() returned error: %v", errRegister)
	}
	manager.SetCooldownStateStore(&mockCooldownStateStore{records: []CooldownStateRecord{
		{
			Provider:       "codex",
			AuthID:         "auth-mixed-record",
			NextRetryAfter: notFoundNext,
			Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: quotaNext},
			UpdatedAt:      now,
		},
	}})

	if errRestore := manager.RestoreCooldownStates(context.Background()); errRestore != nil {
		t.Fatalf("RestoreCooldownStates() returned error: %v", errRestore)
	}

	restored, ok := manager.GetByID("auth-mixed-record")
	if !ok || restored == nil {
		t.Fatal("restored auth not found")
	}
	if !restored.NextRetryAfter.Equal(notFoundNext) {
		t.Fatalf("restored NextRetryAfter = %v, want the non-quota deadline %v untouched", restored.NextRetryAfter, notFoundNext)
	}
	if !restored.Quota.NextRecoverAt.Equal(quotaNext) {
		t.Fatalf("restored Quota.NextRecoverAt = %v, want %v untouched (already inside the ceiling)", restored.Quota.NextRecoverAt, quotaNext)
	}
}

func TestRestoreCooldownStates_PreservesNonQuotaRetryInheritedFromLongQuota(t *testing.T) {
	now := time.Now()
	legacyQuotaNext := now.Add(7 * 24 * time.Hour)
	manager := NewManager(nil, nil, nil)
	if _, err := manager.Register(WithSkipPersist(context.Background()), &Auth{ID: "auth-later-404", Provider: "codex"}); err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}
	manager.SetCooldownStateStore(&mockCooldownStateStore{records: []CooldownStateRecord{{
		Provider: "codex", AuthID: "auth-later-404", NextRetryAfter: legacyQuotaNext,
		Quota:     QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: legacyQuotaNext},
		LastError: &Error{HTTPStatus: http.StatusNotFound, Message: "not found"}, UpdatedAt: now,
	}}})
	if err := manager.RestoreCooldownStates(context.Background()); err != nil {
		t.Fatalf("RestoreCooldownStates() returned error: %v", err)
	}
	restored, ok := manager.GetByID("auth-later-404")
	if !ok || restored == nil {
		t.Fatal("restored auth not found")
	}
	if restored.Quota.NextRecoverAt.After(now.Add(defaultMaxTrustedQuotaCooldown + time.Minute)) {
		t.Fatalf("quota deadline was not bounded: %s", restored.Quota.NextRecoverAt)
	}
	if !restored.NextRetryAfter.Equal(legacyQuotaNext) {
		t.Fatalf("non-quota retry was shortened: got %s, want %s", restored.NextRetryAfter, legacyQuotaNext)
	}
}

// A credential-scoped 429 promotes a deadline to every model on the credential. If the
// stored aggregate deadline were carried through the maximum unclamped, it would become
// credential_quota and park the whole credential for the original multi-day duration.
func TestMarkResult_CredentialScopePropagationIsClamped(t *testing.T) {
	previous := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(previous) })

	m, auth := newCooldownMonotonicManager(t, "model-a", "model-b")

	// Simulate a credential that already carries a pre-fix multi-day quota deadline.
	now := time.Now()
	stale := auth.Clone()
	stale.Quota.Exceeded = true
	stale.Quota.Reason = "quota"
	stale.Quota.NextRecoverAt = now.Add(7 * 24 * time.Hour)
	stale.NextRetryAfter = stale.Quota.NextRecoverAt
	sibling := ensureModelState(stale, "model-b")
	sibling.Quota.Exceeded = true
	sibling.Quota.NextRecoverAt = stale.Quota.NextRecoverAt
	sibling.NextRetryAfter = stale.Quota.NextRecoverAt
	if _, errUpdate := m.Update(WithSkipPersist(context.Background()), stale); errUpdate != nil {
		t.Fatalf("Update() returned error: %v", errUpdate)
	}

	short := 5 * time.Minute
	m.MarkResult(context.Background(), Result{
		AuthID: auth.ID, Provider: auth.Provider, Model: "model-a",
		Success: false, RetryAfter: &short, CredentialScope: true,
		Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "credential 429"},
	})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("auth not found")
	}
	ceiling := now.Add(defaultMaxTrustedQuotaCooldown).Add(time.Minute)
	if updated.Quota.NextRecoverAt.After(ceiling) {
		t.Fatalf("credential-wide Quota.NextRecoverAt = %v, want clamped to ~%v", updated.Quota.NextRecoverAt, ceiling)
	}
	if updated.NextRetryAfter.After(ceiling) {
		t.Fatalf("credential-wide NextRetryAfter = %v, want clamped to ~%v", updated.NextRetryAfter, ceiling)
	}
	for _, model := range []string{"model-a", "model-b"} {
		state := existingModelState(updated, canonicalModelKey(model))
		if state == nil {
			continue
		}
		if state.Quota.NextRecoverAt.After(ceiling) {
			t.Fatalf("model %q Quota.NextRecoverAt = %v, want clamped to ~%v", model, state.Quota.NextRecoverAt, ceiling)
		}
		if state.NextRetryAfter.After(ceiling) {
			t.Fatalf("model %q NextRetryAfter = %v, want clamped to ~%v", model, state.NextRetryAfter, ceiling)
		}
	}
}

func TestCredentialPropagationPreservesBackoffLevelsAcrossRestore(t *testing.T) {
	m, auth := newCooldownMonotonicManager(t, "model-a", "model-b")
	store := &recordingCooldownStateStore{}
	m.SetCooldownStateStore(store)

	now := time.Now()
	stale := auth.Clone()
	// Upstream (aafa4e95) keeps credential-scoped cooldowns isolated from an
	// aggregated model quota, so the stale record must be a credential quota.
	stale.Quota = QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: now.Add(7 * 24 * time.Hour), BackoffLevel: 1}
	stale.NextRetryAfter = stale.Quota.NextRecoverAt
	sibling := ensureModelState(stale, "model-b")
	sibling.Quota = QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: stale.Quota.NextRecoverAt, BackoffLevel: 2}
	sibling.NextRetryAfter = sibling.Quota.NextRecoverAt
	if _, err := m.Update(WithSkipPersist(context.Background()), stale); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}

	short := 5 * time.Minute
	m.MarkResult(context.Background(), Result{
		AuthID: auth.ID, Provider: auth.Provider, Model: "model-a",
		RetryAfter: &short, CredentialScope: true,
		Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "credential 429"},
	})

	check := func(label string, current *Auth) {
		t.Helper()
		if current.Quota.BackoffLevel != 1 {
			t.Fatalf("%s credential level = %d, want 1", label, current.Quota.BackoffLevel)
		}
		if got := current.Quota.NextRecoverAt; got.Before(now.Add(2*time.Hour-time.Minute)) || got.After(now.Add(2*time.Hour+time.Minute)) {
			t.Fatalf("%s credential deadline = %s, want about 2h", label, got)
		}
		sibling := existingModelState(current, "model-b")
		if sibling == nil || sibling.Quota.BackoffLevel != 2 {
			t.Fatalf("%s sibling level = %+v, want 2", label, sibling)
		}
		if got := sibling.Quota.NextRecoverAt; got.Before(now.Add(4*time.Hour-time.Minute)) || got.After(now.Add(4*time.Hour+time.Minute)) {
			t.Fatalf("%s sibling deadline = %s, want about 4h", label, got)
		}
	}
	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth not found")
	}
	check("live", updated)

	records := store.savedRecords()
	if len(records) == 0 {
		t.Fatal("no cooldown records persisted")
	}
	restarted := NewManager(nil, nil, nil)
	if _, err := restarted.Register(WithSkipPersist(context.Background()), &Auth{ID: auth.ID, Provider: auth.Provider}); err != nil {
		t.Fatalf("restart Register() returned error: %v", err)
	}
	restarted.SetCooldownStateStore(&recordingCooldownStateStore{load: records})
	if err := restarted.RestoreCooldownStates(context.Background()); err != nil {
		t.Fatalf("RestoreCooldownStates() returned error: %v", err)
	}
	restored, ok := restarted.GetByID(auth.ID)
	if !ok || restored == nil {
		t.Fatal("restored auth not found")
	}
	check("restored", restored)
}

func TestCredentialPropagationPreservesMixedNonQuotaRetries(t *testing.T) {
	m, auth := newCooldownMonotonicManager(t, "model-a", "model-b")
	now := time.Now()
	legacyNext := now.Add(7 * 24 * time.Hour)
	stale := auth.Clone()
	stale.Quota = QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: legacyNext}
	stale.NextRetryAfter = legacyNext
	stale.LastError = &Error{HTTPStatus: http.StatusNotFound, Message: "not found"}
	sibling := ensureModelState(stale, "model-b")
	sibling.Quota = QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: legacyNext}
	sibling.NextRetryAfter = legacyNext
	sibling.LastError = &Error{HTTPStatus: http.StatusNotFound, Message: "not found"}
	if _, err := m.Update(WithSkipPersist(context.Background()), stale); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}
	short := 5 * time.Minute
	m.MarkResult(context.Background(), Result{
		AuthID: auth.ID, Provider: auth.Provider, Model: "model-a",
		RetryAfter: &short, CredentialScope: true,
		Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "credential 429"},
	})
	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth not found")
	}
	if updated.Quota.NextRecoverAt.After(now.Add(defaultMaxTrustedQuotaCooldown + time.Minute)) {
		t.Fatalf("credential quota remained unbounded: %s", updated.Quota.NextRecoverAt)
	}
	if !updated.NextRetryAfter.Equal(legacyNext) {
		t.Fatalf("credential non-quota retry shortened: %s", updated.NextRetryAfter)
	}
	sibling = existingModelState(updated, "model-b")
	if sibling == nil || sibling.Quota.NextRecoverAt.After(now.Add(defaultMaxTrustedQuotaCooldown+time.Minute)) {
		t.Fatalf("sibling quota remained unbounded: %+v", sibling)
	}
	if !sibling.NextRetryAfter.Equal(legacyNext) {
		t.Fatalf("sibling non-quota retry shortened: %s", sibling.NextRetryAfter)
	}
}

func TestModelQuotaRearmPreservesMixedNonQuotaRetry(t *testing.T) {
	m, auth := newCooldownMonotonicManager(t, "model-a")
	now := time.Now()
	legacyNext := now.Add(7 * 24 * time.Hour)
	stale := auth.Clone()
	state := ensureModelState(stale, "model-a")
	state.Quota = QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: legacyNext}
	state.NextRetryAfter = legacyNext
	state.LastError = &Error{HTTPStatus: http.StatusNotFound, Message: "not found"}
	if _, err := m.Update(WithSkipPersist(context.Background()), stale); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}
	short := 5 * time.Minute
	m.MarkResult(context.Background(), Result{
		AuthID: auth.ID, Provider: auth.Provider, Model: "model-a", RetryAfter: &short,
		Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "model 429"},
	})
	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth not found")
	}
	state = existingModelState(updated, "model-a")
	if state == nil || state.Quota.NextRecoverAt.After(now.Add(defaultMaxTrustedQuotaCooldown+time.Minute)) {
		t.Fatalf("model quota remained unbounded: %+v", state)
	}
	if !state.NextRetryAfter.Equal(legacyNext) {
		t.Fatalf("model non-quota retry shortened: %s", state.NextRetryAfter)
	}
}

func TestAuthQuotaRearmPreservesMixedNonQuotaRetry(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	legacyNext := now.Add(7 * 24 * time.Hour)
	auth := &Auth{
		ID: "mixed-auth-rearm", Provider: "codex", NextRetryAfter: legacyNext,
		Quota:     QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: legacyNext},
		LastError: &Error{HTTPStatus: http.StatusNotFound, Message: "not found"},
	}
	short := 5 * time.Minute
	applyAuthFailureState(auth, &Error{HTTPStatus: http.StatusTooManyRequests}, &short, now, false, time.Hour)
	if auth.Quota.NextRecoverAt.After(now.Add(time.Hour)) {
		t.Fatalf("auth quota remained unbounded: %s", auth.Quota.NextRecoverAt)
	}
	if !auth.NextRetryAfter.Equal(legacyNext) {
		t.Fatalf("auth non-quota retry shortened: %s", auth.NextRetryAfter)
	}
}
