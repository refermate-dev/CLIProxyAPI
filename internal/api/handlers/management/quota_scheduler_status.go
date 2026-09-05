package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// Upstream usage endpoints, overridable in tests.
var (
	anthropicUsageURL = "https://api.anthropic.com/api/oauth/usage"
	codexUsageURL     = "https://chatgpt.com/backend-api/wham/usage"
)

const (
	quotaClaudeUserAgent = "claude-cli/2.1.258 (external, cli)"
	quotaCodexUserAgent  = "codex-tui/0.146.0 (Mac OS 26.5.0; arm64)"
	// A window of at most six hours is the session window; anything longer is weekly.
	quotaSessionWindowMaxSeconds = 6 * 60 * 60
)

// quotaWindow is one rate-limit window as the quota-scheduler status reports it.
type quotaWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at,omitempty"`
	Known       bool    `json:"known"`
	HardLimited bool    `json:"hard_limited"`
}

// quotaAccount is one pooled auth file's usage. Claude accounts carry
// five_hour / seven_day / fable, Codex accounts five_hour / weekly.
type quotaAccount struct {
	Provider  string       `json:"provider"`
	Plan      string       `json:"plan,omitempty"`
	FetchedAt string       `json:"fetched_at"`
	FiveHour  *quotaWindow `json:"five_hour,omitempty"`
	SevenDay  *quotaWindow `json:"seven_day,omitempty"`
	Weekly    *quotaWindow `json:"weekly,omitempty"`
	Fable     *quotaWindow `json:"fable,omitempty"`
}

// QuotaSchedulerStatus reports live subscription usage for every OAuth-backed
// Claude and Codex credential in the pool, keyed by auth file name. It is the
// contract T3 Code's `cliproxy` usage-limit source reads
// (GET /v0/management/quota-scheduler/status). Credentials whose usage read
// fails are omitted rather than reported as limitless.
func (h *Handler) QuotaSchedulerStatus(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auth manager unavailable"})
		return
	}
	accounts := make(map[string]quotaAccount)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, auth := range h.authManager.List() {
		if auth == nil || auth.Disabled {
			continue
		}
		if auth.Provider != "claude" && auth.Provider != "codex" {
			continue
		}
		token, _ := auth.Metadata["access_token"].(string)
		if strings.TrimSpace(token) == "" {
			continue
		}
		key := auth.FileName
		if key == "" {
			key = auth.ID
		}
		wg.Add(1)
		go func(auth *coreauth.Auth, key string) {
			defer wg.Done()
			client := &http.Client{Transport: h.apiCallTransport(auth, "")}
			account, err := fetchQuotaAccount(c.Request.Context(), client, auth)
			if err != nil {
				log.Debugf("quota-scheduler status: %s: %v", key, err)
				return
			}
			mu.Lock()
			accounts[key] = account
			mu.Unlock()
		}(auth, key)
	}
	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"accounts": accounts})
}

func fetchQuotaAccount(ctx context.Context, client *http.Client, auth *coreauth.Auth) (quotaAccount, error) {
	token, _ := auth.Metadata["access_token"].(string)
	var (
		req *http.Request
		err error
	)
	switch auth.Provider {
	case "claude":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, anthropicUsageURL, nil)
		if err != nil {
			return quotaAccount{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
		req.Header.Set("User-Agent", quotaClaudeUserAgent)
	case "codex":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
		if err != nil {
			return quotaAccount{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", quotaCodexUserAgent)
		if accountID, _ := auth.Metadata["account_id"].(string); strings.TrimSpace(accountID) != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
	default:
		return quotaAccount{}, fmt.Errorf("unsupported provider %q", auth.Provider)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return quotaAccount{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return quotaAccount{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return quotaAccount{}, fmt.Errorf("usage endpoint returned %d", resp.StatusCode)
	}
	fetchedAt := time.Now().UTC().Format(time.RFC3339)
	if auth.Provider == "claude" {
		return parseClaudeQuota(body, fetchedAt), nil
	}
	return parseCodexQuota(body, fetchedAt), nil
}

// parseClaudeQuota maps the Anthropic OAuth usage `limits[]` entries: the
// session window, the all-models weekly window, and the Fable-scoped weekly
// window. Scoped windows for other models are ignored.
func parseClaudeQuota(body []byte, fetchedAt string) quotaAccount {
	account := quotaAccount{Provider: "claude", FetchedAt: fetchedAt}
	gjson.GetBytes(body, "limits").ForEach(func(_, limit gjson.Result) bool {
		window := &quotaWindow{
			UsedPercent: limit.Get("percent").Float(),
			ResetAt:     limit.Get("resets_at").String(),
			Known:       true,
		}
		window.HardLimited = window.UsedPercent >= 100
		switch limit.Get("kind").String() {
		case "session":
			account.FiveHour = window
		case "weekly_all":
			account.SevenDay = window
		case "weekly_scoped":
			if strings.EqualFold(limit.Get("scope.model.display_name").String(), "fable") {
				account.Fable = window
			}
		}
		return true
	})
	// Older payload shape: top-level windows without a `limits` array.
	if account.FiveHour == nil {
		if window := claudeTopLevelWindow(body, "five_hour"); window != nil {
			account.FiveHour = window
		}
	}
	if account.SevenDay == nil {
		if window := claudeTopLevelWindow(body, "seven_day"); window != nil {
			account.SevenDay = window
		}
	}
	return account
}

func claudeTopLevelWindow(body []byte, key string) *quotaWindow {
	node := gjson.GetBytes(body, key)
	if !node.Exists() || node.Type == gjson.Null {
		return nil
	}
	used := node.Get("utilization").Float()
	return &quotaWindow{
		UsedPercent: used,
		ResetAt:     node.Get("resets_at").String(),
		Known:       true,
		HardLimited: used >= 100,
	}
}

// parseCodexQuota maps the WHAM usage response. Windows are classified by
// their advertised length rather than by position, because a Pro account with
// no session limit reports its weekly window as `primary_window`.
func parseCodexQuota(body []byte, fetchedAt string) quotaAccount {
	account := quotaAccount{Provider: "codex", FetchedAt: fetchedAt}
	account.Plan = strings.TrimSpace(gjson.GetBytes(body, "plan_type").String())
	limitReached := gjson.GetBytes(body, "rate_limit.limit_reached").Bool()
	for _, key := range []string{"rate_limit.primary_window", "rate_limit.secondary_window"} {
		node := gjson.GetBytes(body, key)
		if !node.Exists() || node.Type == gjson.Null {
			continue
		}
		used := node.Get("used_percent").Float()
		window := &quotaWindow{UsedPercent: used, Known: true, HardLimited: limitReached && used >= 100}
		if resetAt := node.Get("reset_at").Int(); resetAt > 0 {
			window.ResetAt = time.Unix(resetAt, 0).UTC().Format(time.RFC3339)
		}
		if node.Get("limit_window_seconds").Int() <= quotaSessionWindowMaxSeconds {
			account.FiveHour = window
		} else {
			account.Weekly = window
		}
	}
	return account
}

// quotaAccountJSON keeps the wire shape stable for tests.
func (a quotaAccount) MarshalJSON() ([]byte, error) {
	type alias quotaAccount
	return json.Marshal(alias(a))
}
