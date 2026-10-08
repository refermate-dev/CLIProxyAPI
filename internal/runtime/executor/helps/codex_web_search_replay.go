package helps

import (
	"bytes"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/tidwall/sjson"
)

// SanitizeCodexWebSearchReplay omits completed native search-call artifacts from
// ChatGPT OAuth history. Replaying these artifacts can return "response protection
// is unavailable" even when the complete conversation works without them. Search
// tools, readable answers, citations and encrypted reasoning remain unchanged.
func SanitizeCodexWebSearchReplay(body []byte, auth *cliproxyauth.Auth, isCompat bool) []byte {
	if auth == nil || auth.AuthKind() != cliproxyauth.AuthKindOAuth || isCompat ||
		!bytes.Contains(body, []byte(`"web_search_call"`)) {
		return body
	}
	input := util.GetGJSONBytesNoCopy(body, "input")
	if !input.IsArray() {
		return body
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
		return body
	}
	updated, errSet := sjson.SetRawBytes(body, "input", []byte("["+strings.Join(retained, ",")+"]"))
	if errSet != nil {
		return body
	}
	return updated
}
