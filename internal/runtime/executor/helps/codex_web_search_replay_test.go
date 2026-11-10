package helps

import (
	"bytes"
	"testing"

	"github.com/MoeForge/moego/ojson"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestSanitizeCodexWebSearchReplayPreservesConversation(t *testing.T) {
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "test"}}
	body := []byte(`{"input":[{"type":"message","role":"user","content":"Find the docs"},{"type":"web_search_call","status":"completed","action":{"type":"search","query":"docs"}},{"type":"reasoning","encrypted_content":"opaque","summary":[]},{"type":"custom_tool_call","call_id":"call1","name":"exec","input":"pwd"},{"type":"custom_tool_call_output","call_id":"call1","output":"/workspace"},{"type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://example.com"}},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Docs","annotations":[{"type":"url_citation","url":"https://example.com"}]}]},{"type":"web_search_call","status":"in_progress"}],"tools":[{"type":"web_search"}]}`)
	got := SanitizeCodexWebSearchReplay(body, auth, false)
	original := ojson.GetBytesNoCopy(body, "input").Array()
	retained := ojson.GetBytesNoCopy(got, "input").Array()
	indices := []int{0, 2, 3, 4, 6, 7}
	if len(retained) != len(indices) {
		t.Fatalf("retained %d items, want %d", len(retained), len(indices))
	}
	for i, index := range indices {
		if retained[i].Raw != original[index].Raw {
			t.Fatalf("retained item %d changed: %s", i, retained[i].Raw)
		}
	}
	if ojson.GetBytesNoCopy(got, "tools").Raw != ojson.GetBytesNoCopy(body, "tools").Raw {
		t.Fatal("current web-search tool declaration changed")
	}
	if !ojson.ValidBytes(got) {
		t.Fatal("result is not valid JSON")
	}
}

func TestSanitizeCodexWebSearchReplayNoOp(t *testing.T) {
	oauth := &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "test"}}
	apiKey := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test"}}
	siwc := &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "outer", "use_siwc": true, "siwc": map[string]any{"access_token": "test"}}}
	for _, tc := range []struct {
		name, body string
		auth       *cliproxyauth.Auth
		compat     bool
	}{
		{"no search", `{ "input": [{"role":"user","content":"hi"}] }`, oauth, false},
		{"string input", `{ "input": "web_search_call" }`, oauth, false},
		{"running search", `{ "input": [{"type":"web_search_call","status":"in_progress"}] }`, oauth, false},
		{"missing status", `{ "input": [{"type":"web_search_call"}] }`, oauth, false},
		{"api key", `{ "input": [{"type":"web_search_call","status":"completed"}] }`, apiKey, false},
		{"sign in with ChatGPT", `{ "input": [{"type":"web_search_call","status":"completed"}] }`, siwc, false},
		{"compatible model", `{ "input": [{"type":"web_search_call","status":"completed"}] }`, oauth, true},
		{"unknown credential", `{ "input": [{"type":"web_search_call","status":"completed"}] }`, &cliproxyauth.Auth{}, false},
		{"nil credential", `{ "input": [{"type":"web_search_call","status":"completed"}] }`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			if !bytes.Equal(SanitizeCodexWebSearchReplay(body, tc.auth, tc.compat), body) {
				t.Fatal("no-op request changed")
			}
		})
	}
}

func TestSanitizeCodexWebSearchReplayEscapedType(t *testing.T) {
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "test"}}
	body := []byte(`{"input":[{"type":"web\u005fsearch_call","status":"com\u0070leted"},{"type":"message","role":"user","content":"hi"}]}`)
	got := SanitizeCodexWebSearchReplay(body, auth, false)
	items := ojson.GetBytesNoCopy(got, "input").Array()
	if len(items) != 1 || items[0].Get("content").String() != "hi" {
		t.Fatalf("escaped replay artifact survived or readable history changed: %s", got)
	}
}

func TestSanitizeCodexWebSearchReplayDocKeepsPendingChanges(t *testing.T) {
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "test"}}
	doc := ojson.NewDocString(`{"model":"old","input":[{"type":"web_search_call","status":"completed"},{"type":"message","role":"user","content":"hi"}]}`)
	if err := doc.Set("model", "gpt-6-astra"); err != nil {
		t.Fatal(err)
	}
	if !SanitizeCodexWebSearchReplayDoc(doc, auth, false) {
		t.Fatal("completed search artifact was not removed")
	}
	if doc.Get("model").String() != "gpt-6-astra" || doc.Get("input.#").Int() != 1 || doc.Get("input.0.content").String() != "hi" {
		t.Fatalf("pending model or readable history changed: %s", doc.Bytes())
	}
}
