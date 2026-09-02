package chat_completions

import (
	"context"
	"testing"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestStrictJSONResponseWarnsOnlyWhenMessagesAreCollapsed(t *testing.T) {
	logger := log.StandardLogger()
	previousHooks := logger.ReplaceHooks(make(log.LevelHooks))
	previousLevel := logger.GetLevel()
	logger.SetLevel(log.WarnLevel)
	hook := logtest.NewGlobal()
	t.Cleanup(func() {
		logger.ReplaceHooks(previousHooks)
		logger.SetLevel(previousLevel)
	})

	multiple := []byte(`{"type":"response.completed","response":{"model":"gpt-6.1-sol","output":[{"type":"message","content":[{"type":"output_text","text":"{\"draft\":true}"}]},{"type":"message","content":[{"type":"output_text","text":"{\"answer\":true}"}]}]}}`)
	single := []byte(`{"type":"response.completed","response":{"model":"gpt-6.1-sol","output":[{"type":"message","content":[{"type":"output_text","text":"{\"answer\":true}"}]}]}}`)
	for _, tc := range []struct {
		name, request string
		response      []byte
		wantWarning   bool
	}{
		{"strict multiple", `{"response_format":{"type":"json_schema"}}`, multiple, true},
		{"strict single", `{"response_format":{"type":"json_schema"}}`, single, false},
		{"prose multiple", `{}`, multiple, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook.Reset()
			ConvertCodexResponseToOpenAINonStream(context.Background(), "gpt-6.1-sol", []byte(tc.request), nil, tc.response, nil)
			entries := hook.AllEntries()
			if !tc.wantWarning {
				if len(entries) != 0 {
					t.Fatalf("unexpected warning: %v", entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("warnings = %d, want 1", len(entries))
			}
			entry := entries[0]
			if entry.Level != log.WarnLevel || entry.Data["messageItems"] != 2 || entry.Data["model"] != "gpt-6.1-sol" {
				t.Fatalf("warning metadata = %v, level = %v", entry.Data, entry.Level)
			}
			if len(entry.Data) != 2 {
				t.Fatalf("warning must contain counts and model only: %v", entry.Data)
			}
		})
	}
}
