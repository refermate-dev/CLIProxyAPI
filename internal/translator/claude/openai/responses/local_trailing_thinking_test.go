package responses

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// LOCAL PATCH test - remove together with dropTrailingFinalAssistantThinking
// when upstream handles dangling reasoning tails itself.
func TestDropTrailingFinalAssistantThinking(t *testing.T) {
	midHistoryThinkingOnly := `{"role":"assistant","content":[{"type":"thinking","thinking":"mid","signature":"s0"}]}`
	blocks := [][]byte{
		[]byte(`{"role":"user","content":[{"type":"text","text":"hi"}]}`),
		[]byte(midHistoryThinkingOnly),
		[]byte(`{"role":"user","content":[{"type":"text","text":"again"}]}`),
		[]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"dangling","signature":"s3"}]}`),
	}
	out := dropTrailingFinalAssistantThinking(blocks)
	if len(out) != 3 {
		t.Fatalf("expected the trailing thinking-only message dropped, got %d", len(out))
	}
	if string(out[1]) != midHistoryThinkingOnly {
		t.Fatalf("mid-history thinking-only message must be untouched")
	}
	last := gjson.ParseBytes(out[len(out)-1])
	if strings.EqualFold(last.Get("role").String(), "assistant") {
		parts := last.Get("content").Array()
		if len(parts) > 0 && parts[len(parts)-1].Get("type").String() == "thinking" {
			t.Fatalf("final message still ends in thinking")
		}
	}

	// trailing text+thinking: trim only the tail, keep the text
	blocks2 := [][]byte{
		[]byte(`{"role":"user","content":[{"type":"text","text":"hi"}]}`),
		[]byte(`{"role":"assistant","content":[{"type":"text","text":"answer"},{"type":"thinking","thinking":"t2","signature":"s2"}]}`),
	}
	out2 := dropTrailingFinalAssistantThinking(blocks2)
	if len(out2) != 2 || !strings.Contains(string(out2[1]), `"answer"`) {
		t.Fatalf("text content lost while trimming the tail: %s", out2[len(out2)-1])
	}
	lastParts := gjson.ParseBytes(out2[1]).Get("content").Array()
	if lastParts[len(lastParts)-1].Get("type").String() == "thinking" {
		t.Fatalf("tail thinking survived")
	}

	// a valid final turn (thinking then tool_use) is untouched
	valid := `{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"c1","name":"shell","input":{}}]}`
	out3 := dropTrailingFinalAssistantThinking([][]byte{[]byte(valid)})
	if len(out3) != 1 || string(out3[0]) != valid {
		t.Fatalf("valid thinking+tool_use final message was modified")
	}
}
