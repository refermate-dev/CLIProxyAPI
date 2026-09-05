package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const anthropicUsageFixture = `{"five_hour":{"utilization":17,"resets_at":"2026-09-05T06:00:00+00:00"},"seven_day":{"utilization":10,"resets_at":"2026-09-05T19:00:00+00:00"},"limits":[
{"kind":"session","group":"session","percent":17,"resets_at":"2026-09-05T06:00:00+00:00","scope":null},
{"kind":"weekly_all","group":"weekly","percent":10,"resets_at":"2026-09-05T19:00:00+00:00","scope":null},
{"kind":"weekly_scoped","group":"weekly","percent":13,"resets_at":"2026-09-05T19:00:00+00:00","scope":{"model":{"id":null,"display_name":"Fable"}}},
{"kind":"weekly_scoped","group":"weekly","percent":40,"resets_at":"2026-09-05T19:00:00+00:00","scope":{"model":{"id":null,"display_name":"Opus"}}}]}`

const whamUsageFixture = `{"plan_type":"pro","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":1788814031},"secondary_window":null}}`

func TestQuotaSchedulerStatusReportsPooledClaudeAndCodexAccounts(t *testing.T) {
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer claude-token" || r.Header.Get("anthropic-beta") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(anthropicUsageFixture))
	}))
	defer anthropic.Close()
	wham := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer codex-token" || r.Header.Get("ChatGPT-Account-Id") != "acct-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(whamUsageFixture))
	}))
	defer wham.Close()
	restore := pointQuotaUpstreams(anthropic.URL, wham.URL)
	defer restore()

	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range []*coreauth.Auth{
		{ID: "claude-bright@example.com.json", FileName: "claude-bright@example.com.json", Provider: "claude", Metadata: map[string]any{"access_token": "claude-token"}},
		{ID: "codex-1234abcd-dev@example.com-pro.json", FileName: "codex-1234abcd-dev@example.com-pro.json", Provider: "codex", Metadata: map[string]any{"access_token": "codex-token", "account_id": "acct-1"}},
		{ID: "claude-apikey.json", FileName: "claude-apikey.json", Provider: "claude", Attributes: map[string]string{"api_key": "sk-ant"}},
		{ID: "claude-off.json", FileName: "claude-off.json", Provider: "claude", Disabled: true, Metadata: map[string]any{"access_token": "unused"}},
		{ID: "xai-dev.json", FileName: "xai-dev.json", Provider: "xai", Metadata: map[string]any{"access_token": "unused"}},
	} {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register %s: %v", auth.ID, err)
		}
	}

	body := serveQuotaSchedulerStatus(t, &Handler{cfg: &config.Config{}, authManager: manager})
	var status struct {
		Accounts map[string]quotaAccount `json:"accounts"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if len(status.Accounts) != 2 {
		t.Fatalf("accounts = %v, want the two OAuth-backed credentials only", keysOf(status.Accounts))
	}

	claude := status.Accounts["claude-bright@example.com.json"]
	if claude.Provider != "claude" || claude.FiveHour == nil || claude.SevenDay == nil || claude.Fable == nil {
		t.Fatalf("claude account incomplete: %+v", claude)
	}
	if claude.FiveHour.UsedPercent != 17 || claude.SevenDay.UsedPercent != 10 || claude.Fable.UsedPercent != 13 {
		t.Fatalf("claude windows = %v / %v / %v", claude.FiveHour, claude.SevenDay, claude.Fable)
	}
	if claude.Weekly != nil || claude.FiveHour.HardLimited {
		t.Fatalf("claude account must not carry a codex weekly window or a false hard limit: %+v", claude)
	}

	codex := status.Accounts["codex-1234abcd-dev@example.com-pro.json"]
	if codex.Provider != "codex" || codex.Plan != "pro" || codex.Weekly == nil || codex.FiveHour != nil {
		t.Fatalf("codex account incomplete: %+v", codex)
	}
	if codex.Weekly.UsedPercent != 100 || !codex.Weekly.HardLimited || codex.Weekly.ResetAt != "2026-09-07T20:47:11Z" {
		t.Fatalf("codex weekly window = %+v", codex.Weekly)
	}
}

func TestQuotaSchedulerStatusOmitsCredentialsWhoseUsageReadFails(t *testing.T) {
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer anthropic.Close()
	restore := pointQuotaUpstreams(anthropic.URL, anthropic.URL)
	defer restore()

	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "claude-expired@example.com.json", FileName: "claude-expired@example.com.json", Provider: "claude",
		Metadata: map[string]any{"access_token": "stale"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	body := serveQuotaSchedulerStatus(t, &Handler{cfg: &config.Config{}, authManager: manager})
	if string(body) != `{"accounts":{}}` {
		t.Fatalf("body = %s, want an empty account map", body)
	}
}

func TestParseCodexQuotaClassifiesWindowsByLength(t *testing.T) {
	account := parseCodexQuota([]byte(`{"plan_type":"plus","rate_limit":{"limit_reached":false,"primary_window":{"used_percent":42,"limit_window_seconds":18000,"reset_at":1788814031},"secondary_window":{"used_percent":7,"limit_window_seconds":604800,"reset_at":1788900000}}}`), "now")
	if account.FiveHour == nil || account.FiveHour.UsedPercent != 42 || account.Weekly == nil || account.Weekly.UsedPercent != 7 {
		t.Fatalf("windows = %+v / %+v", account.FiveHour, account.Weekly)
	}
	if account.FiveHour.HardLimited || account.Weekly.HardLimited {
		t.Fatal("no window may be hard-limited below 100%")
	}
}

func pointQuotaUpstreams(anthropic, codex string) func() {
	prevAnthropic, prevCodex := anthropicUsageURL, codexUsageURL
	anthropicUsageURL, codexUsageURL = anthropic, codex
	return func() { anthropicUsageURL, codexUsageURL = prevAnthropic, prevCodex }
}

func serveQuotaSchedulerStatus(t *testing.T, h *Handler) []byte {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/quota-scheduler/status", h.QuotaSchedulerStatus)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/quota-scheduler/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body.Bytes()
}

func keysOf(m map[string]quotaAccount) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
