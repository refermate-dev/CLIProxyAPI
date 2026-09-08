package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestBoundedQuotaCooldownPreservesStaleSuccessFence(t *testing.T) {
	withQuotaCooldownEnabled(t)
	for _, scope := range []string{"model", "auth", "credential"} {
		t.Run(scope, func(t *testing.T) {
			ctx := WithSkipPersist(context.Background())
			manager := NewManager(nil, nil, nil)
			authID := t.Name()
			if _, err := manager.Register(ctx, &Auth{
				ID: authID, Provider: "codex", Status: StatusActive,
				ModelStates: map[string]*ModelState{"gpt-6-luna": {Status: StatusActive}},
			}); err != nil {
				t.Fatal(err)
			}
			model := "gpt-6-astra"
			if scope == "auth" {
				model = ""
			}
			staleSeq := manager.nextExecutionSeq()
			week := 7 * 24 * time.Hour
			before := time.Now()
			manager.MarkResult(ctx, Result{
				AuthID: authID, Provider: "codex", Model: model,
				ExecutionSeq: manager.nextExecutionSeq(), CredentialScope: scope == "credential",
				RetryAfter: &week, Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "usage_limit_reached"},
			})
			if scope == "credential" {
				// A sibling success must preserve the propagated quota too.
				model = "gpt-6-luna"
			}
			readQuota := func() QuotaState {
				t.Helper()
				current, ok := manager.GetByID(authID)
				if !ok {
					t.Fatal("credential disappeared")
				}
				if model == "" {
					return current.Quota
				}
				if current.ModelStates[model] == nil {
					t.Fatal("model quota was not recorded")
				}
				return current.ModelStates[model].Quota
			}
			bounded := readQuota()
			if !bounded.Exceeded || !bounded.NextRecoverAt.After(before) || bounded.NextRecoverAt.After(before.Add(time.Hour+time.Minute)) {
				t.Fatalf("quota was not bounded to the first window: %+v", bounded)
			}
			manager.MarkResult(ctx, Result{AuthID: authID, Provider: "codex", Model: model, Success: true, ExecutionSeq: staleSeq})
			if got := readQuota(); !got.Exceeded || !got.NextRecoverAt.Equal(bounded.NextRecoverAt) {
				t.Fatalf("older success cleared or changed the bounded quota: %+v", got)
			}
			if scope != "credential" {
				manager.MarkResult(ctx, Result{AuthID: authID, Provider: "codex", Model: model, Success: true, ExecutionSeq: manager.nextExecutionSeq()})
				if got := readQuota(); got.Exceeded || !got.NextRecoverAt.IsZero() {
					t.Fatalf("new success did not clear the bounded quota: %+v", got)
				}
			}
		})
	}
}
