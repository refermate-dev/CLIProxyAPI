package executor

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// LOCAL tests. Fable quota used to be kept model-scoped by a local model-name
// check in the classifier. Upstream's claude.model-level-cooling replaces it;
// these cases pin the shapes that check existed for so a regression in the
// upstream flag shows up here.

func claudeLocalRateLimitHeaders(pairs ...string) http.Header {
	headers := make(http.Header)
	for index := 0; index+1 < len(pairs); index += 2 {
		headers.Set(pairs[index], pairs[index+1])
	}
	return headers
}

func claudeLocalIsCredentialScoped(t *testing.T, err error) bool {
	t.Helper()
	var scoped interface{ IsCredentialScoped() bool }
	if !errors.As(err, &scoped) || scoped == nil {
		t.Fatalf("expected %T to expose credential scope", err)
	}
	return scoped.IsCredentialScoped()
}

func TestLocalModelLevelCoolingKeepsFableShapesModelScoped(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."}}`)
	cases := map[string]http.Header{
		"unified rejected without window headers": claudeLocalRateLimitHeaders(
			"Anthropic-Ratelimit-Unified-Status", "rejected",
		),
		"7d_oi rejected with shared windows omitted": claudeLocalRateLimitHeaders(
			"Anthropic-Ratelimit-Unified-Status", "rejected",
			"Anthropic-Ratelimit-Unified-7d_oi-Status", "rejected",
		),
		"shared window headers on a Fable 429": claudeLocalRateLimitHeaders(
			"Anthropic-Ratelimit-Unified-Status", "rejected",
			"Anthropic-Ratelimit-Unified-5h-Status", "rejected",
			"Retry-After", "120",
		),
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			if !claudeLocalIsCredentialScoped(t, classifyClaudeUpstreamErrorWithCooling(http.StatusTooManyRequests, headers, body, false)) {
				t.Fatal("without model-level cooling this shape should stay credential-scoped (upstream default)")
			}
			if claudeLocalIsCredentialScoped(t, classifyClaudeUpstreamErrorWithCooling(http.StatusTooManyRequests, headers, body, true)) {
				t.Fatal("with model-level cooling this shape must be model-scoped")
			}
		})
	}
}

func TestLocalModelLevelCoolingKeepsFastDirectErrorsModelScoped(t *testing.T) {
	newResponse := func() *http.Response {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header: claudeLocalRateLimitHeaders(
				"Anthropic-Ratelimit-Unified-Status", "rejected",
				"Anthropic-Ratelimit-Unified-5h-Status", "rejected",
				"Retry-After", "120",
			),
		}
	}
	body := []byte(`{"type":"error"}`)

	if !claudeLocalIsCredentialScoped(t, newClaudeFastDirectResponseError(newResponse(), body)) {
		t.Fatal("default fast direct response should stay credential-scoped")
	}
	err := newClaudeFastDirectResponseErrorWithCooling(newResponse(), body, true)
	if claudeLocalIsCredentialScoped(t, err) {
		t.Fatal("fast direct response ignored model-level cooling; want model-scoped")
	}
	var retry retryAfterProvider
	if !errors.As(err, &retry) || retry.RetryAfter() == nil {
		t.Fatalf("fast direct response = %T, want Retry-After", err)
	}
	if got := *retry.RetryAfter(); got < 2*time.Minute || got > 2*time.Minute+30*time.Second {
		t.Fatalf("RetryAfter = %v, want ~120s with fuzz", got)
	}
}
