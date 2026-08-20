package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

// LOCAL PATCH 2 test - remove together with dropAmbiguousThinkingSets when
// upstream stops merging reasoning items across response boundaries.
func TestDropAmbiguousThinkingSets(t *testing.T) {
	countThinking := func(raw []byte) int {
		count := 0
		for _, part := range gjson.ParseBytes(raw).Get("content").Array() {
			if part.Get("type").String() == "thinking" || part.Get("type").String() == "redacted_thinking" {
				count++
			}
		}
		return count
	}

	// Sentry shape: a reasoning-only response merged into the next response, so
	// the latest assistant message carries a thinking block it never produced.
	// The latest assistant message is NOT the last message here.
	earlier := `{"role":"assistant","content":[{"type":"thinking","thinking":"early","signature":"s0"},{"type":"tool_use","id":"c0","name":"exec","input":{}}]}`
	blocks := [][]byte{
		[]byte(`{"role":"user","content":[{"type":"text","text":"hi"}]}`),
		[]byte(earlier),
		[]byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c0","content":"ok"}]}`),
		[]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"orphan","signature":"s1"},{"type":"thinking","thinking":"real","signature":"s2"},{"type":"tool_use","id":"c1","name":"exec","input":{}}]}`),
		[]byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}`),
	}
	out := dropAmbiguousThinkingSets(blocks)
	if len(out) != 5 {
		t.Fatalf("message count changed: %d", len(out))
	}
	if got := countThinking(out[3]); got != 0 {
		t.Fatalf("latest assistant message kept %d thinking blocks, want 0", got)
	}
	if parts := gjson.ParseBytes(out[3]).Get("content").Array(); len(parts) != 1 || parts[0].Get("type").String() != "tool_use" {
		t.Fatalf("tool_use lost from the latest assistant message: %s", out[3])
	}
	if string(out[1]) != earlier {
		t.Fatalf("an earlier assistant message was modified: %s", out[1])
	}

	// Server-side search blocks now survive the Responses round trip, so genuine
	// interleaved thinking blocks are distinguishable and must remain intact.
	interleaved := `{"role":"assistant","content":[{"type":"thinking","thinking":"t0","signature":"s0"},{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[]},{"type":"thinking","thinking":"t3","signature":"s3"},{"type":"tool_use","id":"c1","name":"exec","input":{}}]}`
	interleavedOut := dropAmbiguousThinkingSets([][]byte{[]byte(interleaved)})
	if string(interleavedOut[0]) != interleaved {
		t.Fatalf("interleaved thinking must be replayed untouched: %s", interleavedOut[0])
	}

	// A formerly gapped response without its separator blocks still presents as
	// adjacent thinking and remains unsafe to replay.
	gapped := [][]byte{
		[]byte(`{"role":"user","content":[{"type":"text","text":"hi"}]}`),
		[]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"t0","signature":"s0"},{"type":"thinking","thinking":"t3","signature":"s3"},{"type":"tool_use","id":"c1","name":"exec","input":{}},{"type":"tool_use","id":"c2","name":"exec","input":{}}]}`),
	}
	gappedOut := dropAmbiguousThinkingSets(gapped)
	if got := countThinking(gappedOut[1]); got != 0 {
		t.Fatalf("gapped message kept %d thinking blocks, want 0", got)
	}
	if got := len(gjson.ParseBytes(gappedOut[1]).Get("content").Array()); got != 2 {
		t.Fatalf("both tool_use blocks must survive, got %d parts", got)
	}

	// The common case is unambiguous and must keep its thinking block, or every
	// Claude turn would lose reasoning continuity.
	single := `{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"c1","name":"exec","input":{}}]}`
	singleOut := dropAmbiguousThinkingSets([][]byte{[]byte(single)})
	if len(singleOut) != 1 || string(singleOut[0]) != single {
		t.Fatalf("a single thinking block must be replayed untouched: %s", singleOut[0])
	}

	// A thinking-only message would become empty content, so it belongs to
	// dropTrailingFinalAssistantThinking instead.
	thinkingOnly := `{"role":"assistant","content":[{"type":"thinking","thinking":"a","signature":"s1"},{"type":"thinking","thinking":"b","signature":"s2"}]}`
	thinkingOnlyOut := dropAmbiguousThinkingSets([][]byte{[]byte(thinkingOnly)})
	if string(thinkingOnlyOut[0]) != thinkingOnly {
		t.Fatalf("thinking-only message must be left alone: %s", thinkingOnlyOut[0])
	}

	// t3-code shape: the offending message sits four turns back and the latest
	// assistant message is a legal thinking+tool_use pair. Anthropic still
	// rejects it, so the sweep cannot stop at the latest assistant message.
	latest := `{"role":"assistant","content":[{"type":"thinking","thinking":"latest","signature":"sN"},{"type":"tool_use","id":"cN","name":"exec","input":{}}]}`
	midHistory := [][]byte{
		[]byte(`{"role":"user","content":[{"type":"text","text":"hi"}]}`),
		[]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"a","signature":"s1"},{"type":"thinking","thinking":"b","signature":"s2"},{"type":"tool_use","id":"c1","name":"exec","input":{}}]}`),
		[]byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}`),
		[]byte(latest),
		[]byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"cN","content":"ok"}]}`),
	}
	midOut := dropAmbiguousThinkingSets(midHistory)
	if got := countThinking(midOut[1]); got != 0 {
		t.Fatalf("mid-history ambiguous set kept %d thinking blocks, want 0", got)
	}
	if string(midOut[3]) != latest {
		t.Fatalf("the latest assistant message must be left alone: %s", midOut[3])
	}
}
