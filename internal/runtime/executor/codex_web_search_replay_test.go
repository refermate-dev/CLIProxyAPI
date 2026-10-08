package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func webSearchReplayRequest() []byte {
	return []byte(`{"model":"gpt-6-astra","stream":true,"store":false,"input":[{"type":"message","role":"user","content":"Find the docs"},{"type":"web_search_call","status":"completed","action":{"type":"search","query":"docs"}},{"type":"reasoning","summary":[],"encrypted_content":"` + validCodexReasoningEncryptedContentForTest() + `"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Docs","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}],"tools":[{"type":"web_search"}]}`)
}

func assertWebSearchReplayBody(t *testing.T, body []byte, wantSearch int) {
	t.Helper()
	search := 0
	reasoning := 0
	citations := 0
	for _, item := range gjson.GetBytes(body, "input").Array() {
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
	if !gjson.GetBytes(body, `tools.#(type=="web_search")`).Exists() {
		t.Fatal("web-search availability was removed")
	}
}

func TestCodexWebSearchReplayHTTP(t *testing.T) {
	for _, mode := range []string{"execute", "stream", "compact"} {
		for _, apiKey := range []bool{false, true} {
			name := mode + "/oauth"
			if apiKey {
				name = mode + "/api-key"
			}
			t.Run(name, func(t *testing.T) {
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
				if apiKey {
					auth.Attributes["api_key"] = "test"
					auth.Metadata = nil
				}
				cfg := &config.Config{}
				cfg.DisableImageGeneration = config.DisableImageGenerationAll
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
				if apiKey {
					want = 1
				}
				assertWebSearchReplayBody(t, upstream, want)
			})
		}
	}
}

func TestCodexWebSearchReplayWebsocket(t *testing.T) {
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
	cfg := &config.Config{}
	cfg.DisableImageGeneration = config.DisableImageGenerationAll
	e := NewCodexWebsocketsExecutor(cfg)
	req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: webSearchReplayRequest()}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse}
	_, err := e.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	assertWebSearchReplayBody(t, upstream, 0)
	prepared, err := e.prepareCodexWebsocketStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	assertWebSearchReplayBody(t, prepared.clientBody, 0)
}

func TestCodexWebSearchReplayPreservesPayloadOverride(t *testing.T) {
	auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"access_token": "test"}}
	input := gjson.GetBytes(webSearchReplayRequest(), "input").Value()
	cfg := &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "gpt-6-astra"}}, Params: map[string]any{"input": input}}}}}
	cfg.DisableImageGeneration = config.DisableImageGenerationAll
	e := NewCodexWebsocketsExecutor(cfg)
	prepared, err := e.prepareCodexWebsocketStream(context.Background(), auth, cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: webSearchReplayRequest()}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if err != nil {
		t.Fatal(err)
	}
	assertWebSearchReplayBody(t, prepared.clientBody, 1)
}
