package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

const codexReserveUsageLimitBody = `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","resets_in_seconds":3600,"plan_type":"pro"}}`

// reserveUpstream answers 429 usage_limit_reached for every model except the
// reserve wire model, which streams a minimal successful Responses lifecycle.
func reserveUpstream(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var models []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		model := gjson.GetBytes(body, "model").String()
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		if model != codexReserveModel {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(codexReserveUsageLimitBody))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.created\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.6-luna"}}` + "\n\n"))
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"gpt-5.6-luna","output":[]}}` + "\n\n"))
	}))
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), models...)
	}
}

func reserveAuth(serverURL, plan string) *cliproxyauth.Auth {
	attrs := map[string]string{"base_url": serverURL, "api_key": "test"}
	if plan != "" {
		attrs["plan_type"] = plan
	}
	return &cliproxyauth.Auth{ID: "auth-1", Label: "developer", Attributes: attrs}
}

func drainReserveStream(t *testing.T, res *cliproxyexecutor.StreamResult) string {
	t.Helper()
	var out bytes.Buffer
	timeout := time.After(3 * time.Second)
	for {
		select {
		case chunk, ok := <-res.Chunks:
			if !ok {
				return out.String()
			}
			if chunk.Err != nil {
				t.Fatalf("unexpected chunk error: %v", chunk.Err)
			}
			out.Write(chunk.Payload)
		case <-timeout:
			t.Fatal("timed out reading stream chunks")
		}
	}
}

func TestCodexExecuteStreamFallsBackToLunaReserveOnUsageLimit(t *testing.T) {
	server, models := reserveUpstream(t)
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	res, err := executor.ExecuteStream(context.Background(), reserveAuth(server.URL, "pro"), cliproxyexecutor.Request{
		Model:   "gpt-5.6-luna",
		Payload: []byte(`{"model":"gpt-5.6-luna","input":"test"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	output := drainReserveStream(t, res)
	if !strings.Contains(output, "response.completed") {
		t.Fatalf("reserve stream missing completion, got:\n%s", output)
	}
	got := models()
	if len(got) != 2 || got[0] != "gpt-5.6-luna" || got[1] != codexReserveModel {
		t.Fatalf("upstream models = %v, want [gpt-5.6-luna %s]", got, codexReserveModel)
	}
}

func TestCodexExecuteFallsBackToLunaReserveOnUsageLimit(t *testing.T) {
	server, models := reserveUpstream(t)
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.Execute(context.Background(), reserveAuth(server.URL, "plus"), cliproxyexecutor.Request{
		Model:   "gpt-5.6-luna",
		Payload: []byte(`{"model":"gpt-5.6-luna","input":"test"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := models(); len(got) != 2 || got[1] != codexReserveModel {
		t.Fatalf("upstream models = %v, want a single reserve retry", got)
	}
}

func TestCodexExecuteStreamSkipsLunaReserveOutsidePlusAndPro(t *testing.T) {
	server, models := reserveUpstream(t)
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.ExecuteStream(context.Background(), reserveAuth(server.URL, "team"), cliproxyexecutor.Request{
		Model:   "gpt-5.6-luna",
		Payload: []byte(`{"model":"gpt-5.6-luna","input":"test"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       true,
	})
	if err == nil {
		t.Fatal("team credential must surface the usage limit, not fall back")
	}
	if got := models(); len(got) != 1 {
		t.Fatalf("upstream models = %v, want exactly the original send", got)
	}
}

func TestCodexExecuteStreamNeverResubstitutesReserve(t *testing.T) {
	var mu sync.Mutex
	sends := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		sends++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(codexReserveUsageLimitBody))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.ExecuteStream(context.Background(), reserveAuth(server.URL, "pro"), cliproxyexecutor.Request{
		Model:   codexReserveModel,
		Payload: []byte(`{"model":"gpt-reserve","input":"test"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       true,
	})
	if err == nil {
		t.Fatal("an exhausted reserve must surface its own usage limit")
	}
	mu.Lock()
	defer mu.Unlock()
	if sends != 1 {
		t.Fatalf("sends = %d, want 1 (no reserve-on-reserve retry)", sends)
	}
}

func TestCodexReserveEligible(t *testing.T) {
	cases := []struct {
		model, plan string
		want        bool
	}{
		{"gpt-5.6-luna", "pro", true},
		{"gpt-5.6-sol", "plus", true},
		{"gpt-6-astra", "prolite", true},
		{"gpt-5.6-luna", "", true},
		{"gpt-reserve", "pro", false},
		{"claude-opus-5", "pro", false},
		{"gpt-5.6-luna", "team", false},
		{"gpt-5.6-luna", "business", false},
		{"gpt-5.6-luna", "free", false},
	}
	for _, tc := range cases {
		auth := reserveAuth("http://example.invalid", tc.plan)
		if got := codexReserveEligible(auth, tc.model); got != tc.want {
			t.Fatalf("codexReserveEligible(%q, plan=%q) = %v, want %v", tc.model, tc.plan, got, tc.want)
		}
	}
	if codexReserveEligible(nil, "gpt-5.6-luna") != true {
		t.Fatal("nil auth is treated as unknown plan and allowed through")
	}
}
