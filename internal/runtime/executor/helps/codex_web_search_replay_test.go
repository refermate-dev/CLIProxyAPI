package helps

import (
	"bytes"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

func TestSanitizeCodexWebSearchReplayPreservesConversation(t *testing.T) {
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "test"}}
	body := []byte(`{"input":[{"type":"message","role":"user","content":"Find the docs"},{"type":"web_search_call","status":"completed","action":{"type":"search","query":"docs"}},{"type":"reasoning","encrypted_content":"opaque","summary":[]},{"type":"custom_tool_call","call_id":"call1","name":"exec","input":"pwd"},{"type":"custom_tool_call_output","call_id":"call1","output":"/workspace"},{"type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://example.com"}},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Docs","annotations":[{"type":"url_citation","url":"https://example.com"}]}]},{"type":"web_search_call","status":"in_progress"}],"tools":[{"type":"web_search"}]}`)
	got := SanitizeCodexWebSearchReplay(body, auth, false)
	original := gjson.GetBytes(body, "input").Array()
	retained := gjson.GetBytes(got, "input").Array()
	indices := []int{0, 2, 3, 4, 6, 7}
	if len(retained) != len(indices) {
		t.Fatalf("retained %d items, want %d", len(retained), len(indices))
	}
	for i, index := range indices {
		if retained[i].Raw != original[index].Raw {
			t.Fatalf("retained item %d changed: %s", i, retained[i].Raw)
		}
	}
	if gjson.GetBytes(got, "tools").Raw != gjson.GetBytes(body, "tools").Raw {
		t.Fatal("current web-search tool declaration changed")
	}
	if !gjson.ValidBytes(got) {
		t.Fatal("result is not valid JSON")
	}
}

func TestSanitizeCodexWebSearchReplayNoOp(t *testing.T) {
	oauth := &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "test"}}
	apiKey := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test"}}
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
