package responses

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

const testCrossTaskDelegation = `<codex_delegation><task>Walmart</task><message>Finished the handoff.</message></codex_delegation>`

func TestConvertOpenAIResponsesRequestToClaude_DowngradesNoCallIDDelegationToUserText(t *testing.T) {
	raw := responsesRequestFromItems(
		`{"type":"function_call_output","name":"send_message_to_thread","namespace":"codex_app","output":"`+testCrossTaskDelegation+`"}`,
		`{"type":"message","role":"user","cache_control":{"type":"ephemeral"},"content":[{"type":"input_text","text":"please continue"}]}`,
	)

	out := ConvertOpenAIResponsesRequestToClaude("claude-opus-5", raw, false)
	root := gjson.ParseBytes(out)
	assertClaudeToolResultAdjacency(t, root)

	if strings.Contains(string(out), `"type":"tool_result"`) {
		t.Fatalf("orphan delegation became tool_result: %s", out)
	}
	if strings.Contains(string(out), "toolu_") {
		t.Fatalf("orphan delegation received a synthetic tool id: %s", out)
	}
	content := root.Get("messages.0.content").Array()
	if len(content) != 2 {
		t.Fatalf("user content count = %d, want delegation and continuation: %s", len(content), out)
	}
	if got := content[0].Get("text").String(); got != testCrossTaskDelegation {
		t.Fatalf("delegation text = %q, want %q", got, testCrossTaskDelegation)
	}
	if got := content[1].Get("text").String(); got != "please continue" {
		t.Fatalf("continuation text = %q, want please continue", got)
	}
}

func TestConvertOpenAIResponsesRequestToClaude_DowngradesNonEmptyOrphanCallID(t *testing.T) {
	t.Run("no call anywhere", func(t *testing.T) {
		raw := responsesRequestFromItems(
			`{"type":"function_call_output","call_id":"call.orphan:1","output":"orphan result"}`,
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}`,
		)

		out := ConvertOpenAIResponsesRequestToClaude("claude-opus-5", raw, false)
		root := gjson.ParseBytes(out)
		assertClaudeToolResultAdjacency(t, root)
		if strings.Contains(string(out), `"type":"tool_result"`) {
			t.Fatalf("non-empty orphan id became tool_result: %s", out)
		}
		if got := root.Get("messages.0.content.0.text").String(); got != "orphan result" {
			t.Fatalf("orphan result text = %q, want orphan result: %s", got, out)
		}
	})

	t.Run("matching call is not in previous assistant", func(t *testing.T) {
		raw := responsesRequestFromItems(
			`{"type":"function_call","call_id":"call_current","name":"read","arguments":"{}"}`,
			`{"type":"function_call","call_id":"call_stale","name":"read","arguments":"{}"}`,
			`{"type":"function_call_output","call_id":"call_current","output":"current result"}`,
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"next step"}]}`,
			`{"type":"function_call_output","call_id":"call_stale","output":"stale result"}`,
		)

		out := ConvertOpenAIResponsesRequestToClaude("claude-opus-5", raw, false)
		root := gjson.ParseBytes(out)
		assertClaudeToolResultAdjacency(t, root)
		if got := root.Get("messages.3.content").String(); got != "stale result" {
			t.Fatalf("stale result text = %q, want stale result: %s", got, out)
		}
		if root.Get("messages.3.content.0.type").String() == "tool_result" {
			t.Fatalf("stale non-adjacent result remained a tool_result: %s", out)
		}
	})
}

func TestConvertOpenAIResponsesRequestToClaude_PreservesValidSingleAndParallelToolPairs(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		want  []string
	}{
		{
			name: "single",
			items: []string{
				`{"type":"function_call","call_id":"call.single:1","name":"read","arguments":"{}"}`,
				`{"type":"function_call_output","call_id":"call.single:1","output":"one"}`,
			},
			want: []string{"call_single_1"},
		},
		{
			name: "parallel",
			items: []string{
				`{"type":"function_call","call_id":"call_a","name":"read","arguments":"{}"}`,
				`{"type":"function_call","call_id":"call_b","name":"read","arguments":"{}"}`,
				`{"type":"function_call_output","call_id":"call_a","output":"a"}`,
				`{"type":"function_call_output","call_id":"call_b","output":"b"}`,
			},
			want: []string{"call_a", "call_b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := ConvertOpenAIResponsesRequestToClaude("claude-opus-5", responsesRequestFromItems(tt.items...), false)
			root := gjson.ParseBytes(out)
			assertClaudeToolResultAdjacency(t, root)
			uses := root.Get("messages.0.content").Array()
			results := root.Get("messages.1.content").Array()
			if len(uses) != len(tt.want) || len(results) != len(tt.want) {
				t.Fatalf("tool pair counts = %d/%d, want %d: %s", len(uses), len(results), len(tt.want), out)
			}
			for index, wantID := range tt.want {
				if got := uses[index].Get("id").String(); got != wantID {
					t.Fatalf("tool_use[%d].id = %q, want %q", index, got, wantID)
				}
				if got := results[index].Get("tool_use_id").String(); got != wantID {
					t.Fatalf("tool_result[%d].tool_use_id = %q, want %q", index, got, wantID)
				}
			}
		})
	}
}

func TestConvertOpenAIResponsesRequestToClaude_MixesValidResultsOrphanDelegationAndUserText(t *testing.T) {
	raw := responsesRequestFromItems(
		`{"type":"function_call","call_id":"call_a","name":"read","arguments":"{}"}`,
		`{"type":"function_call","call_id":"call_b","name":"read","arguments":"{}"}`,
		`{"type":"function_call_output","call_id":"call_a","output":"a"}`,
		`{"type":"function_call_output","name":"send_message_to_thread","namespace":"codex_app","output":"`+testCrossTaskDelegation+`"}`,
		`{"type":"function_call_output","call_id":"call_b","output":"b"}`,
		`{"type":"message","role":"user","cache_control":{"type":"ephemeral"},"content":[{"type":"input_text","text":"please continue"}]}`,
	)

	out := ConvertOpenAIResponsesRequestToClaude("claude-opus-5", raw, false)
	root := gjson.ParseBytes(out)
	assertClaudeToolResultAdjacency(t, root)
	content := root.Get("messages.1.content").Array()
	if len(content) != 4 {
		t.Fatalf("mixed user content count = %d, want 4: %s", len(content), out)
	}
	for index, wantID := range []string{"call_a", "call_b"} {
		if got := content[index].Get("tool_use_id").String(); got != wantID {
			t.Fatalf("mixed tool_result[%d].tool_use_id = %q, want %q: %s", index, got, wantID, out)
		}
	}
	if got := content[2].Get("text").String(); got != testCrossTaskDelegation {
		t.Fatalf("mixed delegation text = %q, want delegation", got)
	}
	if got := content[3].Get("text").String(); got != "please continue" {
		t.Fatalf("mixed continuation text = %q, want please continue", got)
	}
	if got := content[3].Get("cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("mixed continuation cache_control.type = %q, want ephemeral", got)
	}
}

func TestConvertOpenAIResponsesRequestToClaude_DowngradesOrphanRichContentWithoutDroppingIt(t *testing.T) {
	raw := responsesRequestFromItems(
		`{"type":"function_call_output","call_id":"orphan","output":[{"type":"input_text","text":"caption"},{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="},{"type":"input_file","file_data":"data:application/pdf;base64,ZmlsZQ=="}]}`,
	)

	out := ConvertOpenAIResponsesRequestToClaude("claude-opus-5", raw, false)
	root := gjson.ParseBytes(out)
	assertClaudeToolResultAdjacency(t, root)
	content := root.Get("messages.0.content").Array()
	want := []string{"text", "image", "document"}
	if len(content) != len(want) {
		t.Fatalf("rich orphan content count = %d, want %d: %s", len(content), len(want), out)
	}
	for index, wantType := range want {
		if got := content[index].Get("type").String(); got != wantType {
			t.Fatalf("rich orphan content[%d].type = %q, want %q", index, got, wantType)
		}
	}
}

func assertClaudeToolResultAdjacency(t *testing.T, root gjson.Result) {
	t.Helper()
	var previousToolUseIDs map[string]struct{}
	for messageIndex, message := range root.Get("messages").Array() {
		role := message.Get("role").String()
		if role == "user" {
			for _, part := range message.Get("content").Array() {
				if part.Get("type").String() != "tool_result" {
					continue
				}
				id := part.Get("tool_use_id").String()
				if _, ok := previousToolUseIDs[id]; id == "" || !ok {
					t.Fatalf("messages[%d] orphan tool_result id %q: %s", messageIndex, id, root.Raw)
				}
			}
		}
		previousToolUseIDs = nil
		if role == "assistant" {
			previousToolUseIDs = map[string]struct{}{}
			for _, part := range message.Get("content").Array() {
				if part.Get("type").String() == "tool_use" {
					previousToolUseIDs[part.Get("id").String()] = struct{}{}
				}
			}
		}
	}
}
