package helps

import (
	"bytes"
	"strings"

	"github.com/MoeForge/moego/ojson"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// SanitizeCodexWebSearchReplay omits completed native search-call artifacts from
// ChatGPT OAuth history. Replaying these artifacts can return "response protection
// is unavailable" even when the complete conversation works without them. Search
// tools, readable answers, citations and encrypted reasoning remain unchanged.
// SIWC uses the OpenAI API route, where this replay failure has not been observed.
func SanitizeCodexWebSearchReplay(body []byte, auth *cliproxyauth.Auth, isCompat bool) []byte {
	if auth == nil || auth.AuthKind() != cliproxyauth.AuthKindOAuth || cliproxyauth.IsCodexUseSIWC(auth) || isCompat ||
		(!bytes.Contains(body, []byte(`"web_search_call"`)) && !bytes.Contains(body, []byte(`\u`))) {
		return body
	}
	doc := ojson.NewDoc(body)
	if !SanitizeCodexWebSearchReplayDoc(doc, auth, isCompat) {
		return body
	}
	return doc.Bytes()
}

// SanitizeCodexWebSearchReplayDoc removes completed OAuth search artifacts before
// tool normalization and operator payload rules, using the executor's document.
func SanitizeCodexWebSearchReplayDoc(doc *ojson.Doc, auth *cliproxyauth.Auth, isCompat bool) bool {
	if doc == nil || auth == nil || auth.AuthKind() != cliproxyauth.AuthKindOAuth || cliproxyauth.IsCodexUseSIWC(auth) || isCompat {
		return false
	}
	input := doc.Get("input")
	if !input.IsArray() {
		return false
	}
	items := input.Array()
	retained := make([]string, 0, len(items))
	changed := false
	for _, item := range items {
		if item.Get("type").String() == "web_search_call" && item.Get("status").String() == "completed" {
			changed = true
			continue
		}
		retained = append(retained, item.Raw)
	}
	if !changed {
		return false
	}
	return doc.SetRawBytes("input", []byte("["+strings.Join(retained, ",")+"]")) == nil
}
