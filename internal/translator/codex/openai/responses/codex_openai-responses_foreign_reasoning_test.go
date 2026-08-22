package responses

import (
	"encoding/base64"
	"encoding/binary"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/encoding/protowire"

	sigcompat "github.com/router-for-me/CLIProxyAPI/v7/internal/signature"
)

// Local copies of the internal/signature test builders (those are _test.go-only,
// not importable from this package). If a validator changes shape, the
// signature package's own tests break first and this fixture gets updated to match.
func gptReasoningFixture() string {
	payload := make([]byte, 1+8+16+16+32)
	payload[0] = 0x80
	binary.BigEndian.PutUint64(payload[1:9], 0)
	for i := 9; i < len(payload); i++ {
		payload[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// Fixtures mirror the internal/signature package's own test builders so this
// table tracks the real validators instead of hand-rolled strings that drift.
func foreignReasoningFixture(t *testing.T, blob string) []byte {
	t.Helper()
	return []byte(`{"model":"gpt-5.6-sol","input":[{"type":"reasoning","id":"rs_test","summary":[],"encrypted_content":"` + blob + `"},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
}

func TestDropForeignReasoningReplayForGPTTarget(t *testing.T) {
	gptBlob := gptReasoningFixture()
	caisBlob := claudeCAISFixture("claude-opus-5")
	geminiBlob := geminiEnvelopeFixture()
	kimiSized := base64.RawStdEncoding.EncodeToString(make([]byte, 437))

	tests := []struct {
		name     string
		blob     string
		wantKept bool
	}{
		{name: "gpt fernet blob kept", blob: gptBlob, wantKept: true},
		{name: "claude CAIS dropped", blob: caisBlob, wantKept: false},
		{name: "claude thinking envelope dropped", blob: claudeThinkingEnvelopeFixture(), wantKept: false},
		{name: "gemini envelope dropped", blob: geminiBlob, wantKept: false},
		{name: "kimi-sized collision kept", blob: kimiSized, wantKept: true},
		{name: "gemini bypass literal kept", blob: sigcompat.GeminiContextEngineeringBypass, wantKept: true},
		{name: "unknown opaque kept", blob: "K1ZAIbzDbO-random-opaque-ciphertext", wantKept: true},
		{name: "empty blob kept", blob: "", wantKept: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := dropForeignReasoningReplayForGPTTarget(foreignReasoningFixture(t, tc.blob))
			got := gjson.GetBytes(out, "input.#")
			// Guard the serialization path, not just the count: a dropped item must
			// leave the retained items as JSON objects, not base64-marshaled strings.
			firstType := gjson.GetBytes(out, "input.0.type")
			if firstType.String() != "reasoning" && firstType.String() != "message" {
				t.Fatalf("input.0.type = %q, want reasoning or message object", firstType.String())
			}
			if tc.blob == "" {
				// Empty blob keeps the reasoning item; verify its content survived.
				if gjson.GetBytes(out, "input.0.encrypted_content").String() != "" {
					t.Fatal("empty blob item was mutated")
				}
			}
			wantCount := 2
			if !tc.wantKept {
				wantCount = 1
			}
			if got.Int() != int64(wantCount) {
				t.Fatalf("input count = %d, want %d", got.Int(), wantCount)
			}
		})
	}
}

// claudeCAISFixture mirrors defaultClaudeCAISParts in the signature package:
// top envelope tag1=2, container tag2 with a channel block carrying the claude-*
// model text, then trailer tag3=1.
func claudeCAISFixture(model string) string {
	var channelBlock []byte
	channelBlock = protowire.AppendTag(channelBlock, 1, protowire.VarintType)
	channelBlock = protowire.AppendVarint(channelBlock, 16)
	channelBlock = protowire.AppendTag(channelBlock, 3, protowire.VarintType)
	channelBlock = protowire.AppendVarint(channelBlock, 2)
	channelBlock = protowire.AppendTag(channelBlock, 5, protowire.BytesType)
	channelBlock = protowire.AppendBytes(channelBlock, make([]byte, 64))
	channelBlock = protowire.AppendTag(channelBlock, 6, protowire.BytesType)
	channelBlock = protowire.AppendString(channelBlock, model)
	channelBlock = protowire.AppendTag(channelBlock, 7, protowire.VarintType)
	channelBlock = protowire.AppendVarint(channelBlock, 1)
	channelBlock = protowire.AppendTag(channelBlock, 8, protowire.BytesType)
	channelBlock = protowire.AppendString(channelBlock, "thinking")
	channelBlock = protowire.AppendTag(channelBlock, 11, protowire.BytesType)
	channelBlock = protowire.AppendString(channelBlock, "d9743975-4bb0-4936-9e28-d5b0d21bdc48")
	var container []byte
	container = protowire.AppendTag(container, 1, protowire.BytesType)
	container = protowire.AppendBytes(container, channelBlock)
	var payload []byte
	payload = protowire.AppendTag(payload, 1, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 2)
	payload = protowire.AppendTag(payload, 2, protowire.BytesType)
	payload = protowire.AppendBytes(payload, container)
	payload = protowire.AppendTag(payload, 3, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 1)
	return base64.StdEncoding.EncodeToString(payload)
}

// claudeThinkingEnvelopeFixture mirrors testClaudeThinkingSignature from the
// signature package: protowire container with a claude-* channel block.
func claudeThinkingEnvelopeFixture() string {
	var channelBlock []byte
	channelBlock = protowire.AppendTag(channelBlock, 1, protowire.VarintType)
	channelBlock = protowire.AppendVarint(channelBlock, 12)
	channelBlock = protowire.AppendTag(channelBlock, 2, protowire.VarintType)
	channelBlock = protowire.AppendVarint(channelBlock, 2)
	channelBlock = protowire.AppendTag(channelBlock, 6, protowire.BytesType)
	channelBlock = protowire.AppendString(channelBlock, "claude-sonnet-4-6")
	container := protowire.AppendTag(nil, 1, protowire.BytesType)
	container = protowire.AppendBytes(container, channelBlock)
	payload := protowire.AppendTag(nil, 2, protowire.BytesType)
	payload = protowire.AppendBytes(payload, container)
	payload = protowire.AppendTag(payload, 3, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 1)
	return base64.StdEncoding.EncodeToString(payload)
}

// geminiEnvelopeFixture mirrors testGeminiThoughtSignatureEnvelope: outer tag 0x12,
// inner tag 0x0a, payload starting with protobuf field2 bytes {0x01, 0x0c, ...}.
func geminiEnvelopeFixture() string {
	payload := []byte{0x01, 0x0c}
	for i := 0; i < 97; i++ {
		payload = append(payload, byte(i))
	}
	inner := append([]byte{0x0a, byte(len(payload))}, payload...)
	outer := append([]byte{0x12, byte(len(inner))}, inner...)
	return base64.RawStdEncoding.EncodeToString(outer)
}
