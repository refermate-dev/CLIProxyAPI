package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func webSearchReplayRequest() []byte {
	return []byte(`{"model":"gpt-6-astra","stream":true,"store":false,"input":[{"type":"message","role":"user","content":"Find the docs"},{"type":"web_search_call","status":"completed","action":{"type":"search","query":"docs"}},{"type":"reasoning","summary":[],"encrypted_content":"` + validCodexReasoningEncryptedContentForTest() + `"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Docs","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}],"tools":[{"type":"web_search"}]}`)
}

func assertWebSearchReplayBody(t *testing.T, body []byte, wantSearch int) {
	t.Helper()
	search := 0
	reasoning := 0
	citations := 0
	for _, item := range ojson.GetBytesNoCopy(body, "input").Array() {
		if item.Get("type").String() == "web_search_call" {
			search++
		}
		if item.Get("type").String() == "reasoning" && item.Get("encrypted_content").String() == validCodexReasoningEncryptedContentForTest() {
			reasoning++
		}
		if item.Get("role").String() == "assistant" && item.Get("content.0.annotations.0.url").String() == "https://example.com" {
			citations++
		}
	}
	if search != wantSearch || reasoning != 1 || citations != 1 {
		t.Fatalf("search=%d reasoning=%d citations=%d body=%s", search, reasoning, citations, body)
	}
	if !ojson.GetBytesNoCopy(body, `tools.#(type=="web_search")`).Exists() {
		t.Fatal("web-search availability was removed")
	}
}

func TestCodexWebSearchReplayHTTP(t *testing.T) {
	for _, mode := range []string{"execute", "stream", "compact"} {
		for _, variant := range []string{"oauth", "api-key", "siwc", "payload-override", "payload-filter"} {
			t.Run(mode+"/"+variant, func(t *testing.T) {
				var upstream []byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var err error
					upstream, err = io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					if mode == "compact" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"id":"cmp1","object":"response.compaction","output":[]}`))
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
				}))
				defer server.Close()
				auth := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{"base_url": server.URL}, Metadata: map[string]any{"access_token": "test"}}
				if variant == "api-key" {
					auth.Attributes["api_key"] = "test"
					auth.Metadata = nil
				}
				if variant == "siwc" {
					auth.Metadata["use_siwc"] = true
					auth.Metadata["siwc"] = map[string]any{"access_token": "siwc-test"}
				}
				cfg := webSearchReplayConfig(variant)
				e := NewCodexExecutor(cfg)
				req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: webSearchReplayRequest()}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse}
				var err error
				if mode == "stream" {
					var stream *cliproxyexecutor.StreamResult
					stream, err = e.ExecuteStream(context.Background(), auth, req, opts)
					if err == nil {
						for chunk := range stream.Chunks {
							if chunk.Err != nil {
								err = chunk.Err
							}
						}
					}
				} else {
					if mode == "compact" {
						opts.Alt = "responses/compact"
					}
					_, err = e.Execute(context.Background(), auth, req, opts)
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if variant == "api-key" || variant == "siwc" || variant == "payload-override" {
					want = 1
				}
				assertWebSearchReplayBody(t, upstream, want)
			})
		}
	}
}

func TestCodexWebSearchReplayWebsocket(t *testing.T) {
	for _, variant := range []string{"oauth", "siwc", "payload-override", "payload-filter"} {
		t.Run(variant, func(t *testing.T) {
			var upstream []byte
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_, upstream, err = conn.ReadMessage()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp1","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
			}))
			defer server.Close()
			auth := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{"base_url": server.URL}, Metadata: map[string]any{"access_token": "test"}}
			if variant == "siwc" {
				auth.Metadata["use_siwc"] = true
				auth.Metadata["siwc"] = map[string]any{"access_token": "siwc-test"}
			}
			cfg := webSearchReplayConfig(variant)
			e := NewCodexWebsocketsExecutor(cfg)
			req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: webSearchReplayRequest()}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse}
			_, err := e.Execute(context.Background(), auth, req, opts)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if variant == "siwc" || variant == "payload-override" {
				want = 1
			}
			assertWebSearchReplayBody(t, upstream, want)
			prepared, err := e.prepareCodexWebsocketStream(context.Background(), auth, req, opts)
			if err != nil {
				t.Fatal(err)
			}
			assertWebSearchReplayBody(t, prepared.clientBody, want)
		})
	}
}

func TestCodexWebSearchReplayPreservesPayloadOverride(t *testing.T) {
	auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"access_token": "test"}}
	input := ojson.GetBytesNoCopy(webSearchReplayRequest(), "input").Value()
	cfg := &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "gpt-6-astra"}}, Params: map[string]any{"input": input}}}}}
	cfg.DisableImageGeneration = config.DisableImageGenerationAll
	e := NewCodexWebsocketsExecutor(cfg)
	prepared, err := e.prepareCodexWebsocketStream(context.Background(), auth, cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: webSearchReplayRequest()}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if err != nil {
		t.Fatal(err)
	}
	assertWebSearchReplayBody(t, prepared.clientBody, 1)
}

// Rules observe the sanitized history. An override can deliberately restore it;
// a filter must run once, so reasoning survives removal of the first user item.
func webSearchReplayConfig(variant string) *config.Config {
	cfg := &config.Config{}
	cfg.DisableImageGeneration = config.DisableImageGenerationAll
	switch variant {
	case "payload-override":
		cfg.Payload.Override = []config.PayloadRule{{
			Models: []config.PayloadModelRule{{Name: "gpt-6-astra", Match: []map[string]any{{"input.#": 3}}}},
			Params: map[string]any{"input": ojson.GetBytesNoCopy(webSearchReplayRequest(), "input").Value()},
		}}
	case "payload-filter":
		cfg.Payload.Filter = []config.PayloadFilterRule{{
			Models: []config.PayloadModelRule{{Name: "gpt-6-astra"}},
			Params: []string{"input.0"},
		}}
	}
	return cfg
}
