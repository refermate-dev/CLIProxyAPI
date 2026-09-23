package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

// LOCAL PATCH test - remove together with dropAdjacentRedactedThinkingSets when
// upstream stops merging reasoning items across response boundaries.

func localRedactedReasoningItem(data string) string {
	return `{"type":"reasoning","encrypted_content":"` + ClaudeResponsesRedactedThinkingPrefix + data + `","summary":[]}`
}

func localContentTypes(message gjson.Result) []string {
	var kinds []string
	for _, part := range message.Get("content").Array() {
		kinds = append(kinds, part.Get("type").String())
	}
	return kinds
}

func localAssertTypes(t *testing.T, label string, message gjson.Result, want []string, out []byte) {
	t.Helper()
	got := localContentTypes(message)
	if len(got) != len(want) {
		t.Fatalf("%s content types = %v, want %v. Output: %s", label, got, want, out)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s content types = %v, want %v. Output: %s", label, got, want, out)
		}
	}
}

// Two redacted_thinking items from two responses merge into one assistant
// message. Neither can be attributed to the tool call that follows, so the
// whole thinking set is dropped and the tool call survives.
func TestLocalAdjacentRedactedThinkingIsDropped(t *testing.T) {
	raw := responsesRequestFromItems(
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`,
		localRedactedReasoningItem("first-response"),
		localRedactedReasoningItem("second-response"),
		responsesFunctionCallItem("call_1", "exec"),
		responsesFunctionCallOutputItem("call_1", "ok"),
	)

	out, _ := ConvertOpenAIResponsesRequestToClaude("claude-opus-5-5", raw, false)
	localAssertTypes(t, "assistant", gjson.GetBytes(out, "messages.1"), []string{"tool_use"}, out)
}

// The sweep covers every assistant message, not only the latest one: Anthropic
// rejected a captured body for a set four turns back while the latest assistant
// message was a legal thinking + tool_use pair.
func TestLocalAdjacentRedactedThinkingIsDroppedMidHistory(t *testing.T) {
	latestRaw, latestSignature := testClaudeResponsesThinkingSignatureForModel(t, "claude-opus-5-latest")
	raw := responsesRequestFromItems(
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`,
		localRedactedReasoningItem("a"),
		localRedactedReasoningItem("b"),
		responsesFunctionCallItem("call_1", "exec"),
		responsesFunctionCallOutputItem("call_1", "ok"),
		responsesReasoningItem(latestRaw, "latest"),
		responsesFunctionCallItem("call_2", "exec"),
		responsesFunctionCallOutputItem("call_2", "ok"),
	)

	out, _ := ConvertOpenAIResponsesRequestToClaude("claude-opus-5-5", raw, false)
	localAssertTypes(t, "earlier assistant", gjson.GetBytes(out, "messages.1"), []string{"tool_use"}, out)
	latest := gjson.GetBytes(out, "messages.3")
	localAssertTypes(t, "latest assistant", latest, []string{"thinking", "tool_use"}, out)
	if got := latest.Get("content.0.signature").String(); got != latestSignature {
		t.Fatalf("latest thinking signature = %q, want %q", got, latestSignature)
	}
}

// A lone redacted_thinking block, and redacted_thinking between two thinking
// blocks, are valid single-response shapes and must be replayed untouched.
func TestLocalValidRedactedThinkingIsKept(t *testing.T) {
	thinkingRaw, _ := testClaudeResponsesThinkingSignatureForModel(t, "claude-opus-5-first")
	otherRaw, _ := testClaudeResponsesThinkingSignatureForModel(t, "claude-opus-5-second")

	lone := responsesRequestFromItems(
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`,
		localRedactedReasoningItem("only"),
		responsesFunctionCallItem("call_1", "exec"),
		responsesFunctionCallOutputItem("call_1", "ok"),
	)
	out, _ := ConvertOpenAIResponsesRequestToClaude("claude-opus-5-5", lone, false)
	localAssertTypes(t, "lone redacted", gjson.GetBytes(out, "messages.1"), []string{"redacted_thinking", "tool_use"}, out)

	mixed := responsesRequestFromItems(
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`,
		responsesReasoningItem(thinkingRaw, "a"),
		localRedactedReasoningItem("middle"),
		responsesReasoningItem(otherRaw, "b"),
		responsesFunctionCallItem("call_1", "exec"),
		responsesFunctionCallOutputItem("call_1", "ok"),
	)
	out, _ = ConvertOpenAIResponsesRequestToClaude("claude-opus-5-5", mixed, false)
	localAssertTypes(t, "mixed", gjson.GetBytes(out, "messages.1"), []string{"thinking", "redacted_thinking", "thinking", "tool_use"}, out)
}

// A thinking-only assistant message would become empty content, so the sweep
// leaves it alone; the compat path keeps its blocks as they are.
func TestLocalAdjacentRedactedThinkingEdgeCases(t *testing.T) {
	thinkingOnly := [][]byte{
		[]byte(`{"role":"user","content":[{"type":"text","text":"hi"}]}`),
		[]byte(`{"role":"assistant","content":[{"type":"redacted_thinking","data":"a"},{"type":"redacted_thinking","data":"b"}]}`),
		[]byte(`{"role":"user","content":[{"type":"text","text":"again"}]}`),
	}
	before := string(thinkingOnly[1])
	if got := dropAdjacentRedactedThinkingSets(thinkingOnly); string(got[1]) != before {
		t.Fatalf("thinking-only message was modified: %s", got[1])
	}

	raw := responsesRequestFromItems(
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`,
		localRedactedReasoningItem("a"),
		localRedactedReasoningItem("b"),
		responsesFunctionCallItem("call_1", "exec"),
		responsesFunctionCallOutputItem("call_1", "ok"),
	)
	out, _ := ConvertOpenAIResponsesRequestToClaudeWithCompat("claude-opus-5-5", raw, false)
	localAssertTypes(t, "compat", gjson.GetBytes(out, "messages.1"), []string{"redacted_thinking", "redacted_thinking", "tool_use"}, out)
}
