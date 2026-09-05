package registry

import "testing"

func TestWithClaudeBuiltinsKeepsFable51AheadOfRemoteCatalog(t *testing.T) {
	models := WithClaudeBuiltins([]*ModelInfo{
		{ID: claudeLegacyFable5ModelID, DisplayName: "Legacy Claude Fable"},
		{ID: "claude-opus-5", DisplayName: "Claude Opus 5", ContextLength: 1000000},
		{ID: claudeBuiltinFable51ModelID, DisplayName: "Stale Fable 5.1 metadata"},
	})

	var found *ModelInfo
	var opus *ModelInfo
	count := 0
	for _, model := range models {
		if model != nil && model.ID == claudeLegacyFable5ModelID {
			t.Fatalf("legacy Claude Fable model remained in catalog: %+v", model)
		}
		if model != nil && model.ID == "claude-opus-5" {
			opus = model
		}
		if model != nil && model.ID == claudeBuiltinFable51ModelID {
			found = model
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Claude Fable 5.1 count = %d, want 1", count)
	}
	if found == nil || found.DisplayName != "Claude Fable 5.1" {
		t.Fatalf("Claude Fable 5.1 built-in = %+v", found)
	}
	if found.Thinking == nil || !found.Thinking.DynamicAllowed {
		t.Fatalf("Claude Fable 5.1 thinking = %+v, want adaptive thinking", found.Thinking)
	}
	if opus == nil || opus.DisplayName != "Claude Opus 5" || opus.ContextLength != 1000000 {
		t.Fatalf("Claude Opus 5 changed while filtering legacy Fable: %+v", opus)
	}
}

func TestGetStaticModelDefinitionsByChannelSupportsGeminiInteractions(t *testing.T) {
	models := GetStaticModelDefinitionsByChannel("gemini-interactions")
	if len(models) == 0 {
		t.Fatal("GetStaticModelDefinitionsByChannel(gemini-interactions) returned no models")
	}
}

func TestModelOverrideHeadersFromEmbeddedModels(t *testing.T) {
	const wantUA = "codex-tui/0.144.0 (Mac OS 26.5.1; arm64) iTerm.app/3.6.11 (codex-tui; 0.144.0)"
	got := ModelOverrideHeaders("gpt-5.6-luna")
	if got == nil {
		t.Fatal("ModelOverrideHeaders(gpt-5.6-luna) = nil, want headers")
	}
	if got["user-agent"] != wantUA {
		t.Fatalf("user-agent = %q, want %q", got["user-agent"], wantUA)
	}
	if got := ModelOverrideHeaders("gpt-5.4"); got != nil {
		t.Fatalf("ModelOverrideHeaders(gpt-5.4) = %#v, want nil", got)
	}
}

func TestGeminiVertexModelsUseFlashLiteReleaseID(t *testing.T) {
	const releaseID = "gemini-3.1-flash-lite"
	const previewID = releaseID + "-preview"

	for _, model := range GetGeminiVertexModels() {
		if model == nil {
			continue
		}
		if model.ID == previewID {
			t.Fatalf("Vertex model ID = %q, want release ID %q", model.ID, releaseID)
		}
		if model.ID == releaseID {
			return
		}
	}

	t.Fatalf("Vertex models do not contain %q", releaseID)
}

func TestWithXAIBuiltinsIncludesImage20(t *testing.T) {
	models := WithXAIBuiltins(nil)
	for _, model := range models {
		if model != nil && model.ID == xaiBuiltinImage20ModelID {
			if model.Created != 1786060800 {
				t.Fatalf("created = %d, want 1786060800 (2026-08-07)", model.Created)
			}
			return
		}
	}
	t.Fatalf("expected xAI builtin model %s", xaiBuiltinImage20ModelID)
}

func TestWithXAIBuiltinsIncludesVideo15GAAndPreviewAlias(t *testing.T) {
	models := WithXAIBuiltins(nil)
	foundGA := false
	foundPreviewAlias := false

	for _, model := range models {
		if model == nil {
			continue
		}
		if model.ID == xaiBuiltinVideo15ModelID {
			foundGA = true
		}
		if model.ID == xaiBuiltinVideo15PreviewID {
			foundPreviewAlias = true
		}
	}

	if !foundGA {
		t.Fatalf("expected xAI builtin model %s", xaiBuiltinVideo15ModelID)
	}
	if !foundPreviewAlias {
		t.Fatalf("expected xAI builtin compatibility alias %s", xaiBuiltinVideo15PreviewID)
	}
}

func TestAntigravityWebSearchModelForRequiresRequestedModelCapability(t *testing.T) {
	registryRef := GetGlobalRegistry()
	registryRef.RegisterClient("test-antigravity-websearch-route", "antigravity", []*ModelInfo{
		{ID: "gemini-route-test"},
		{ID: "gemini-web-search-test", SupportsWebSearch: true},
	})
	registryRef.RegisterClient("test-gemini-websearch-route", "gemini", []*ModelInfo{
		{ID: "gemini-cross-provider-route"},
		{ID: "gemini-cross-provider-search", SupportsWebSearch: true},
	})
	t.Cleanup(func() {
		registryRef.UnregisterClient("test-antigravity-websearch-route")
		registryRef.UnregisterClient("test-gemini-websearch-route")
	})

	if got := AntigravityWebSearchModelFor("gemini-route-test"); got != "" {
		t.Fatalf("route model without web search support should not get fallback model, got %q", got)
	}
	if got := AntigravityWebSearchModelFor("gemini-route-test(high)"); got != "" {
		t.Fatalf("suffix route model without web search support should not get fallback model, got %q", got)
	}
	if got := AntigravityWebSearchModelFor("gemini-web-search-test"); got != "gemini-web-search-test" {
		t.Fatalf("AntigravityWebSearchModelFor capable model = %q, want itself", got)
	}
	if got := AntigravityWebSearchModelFor("gemini-cross-provider-route"); got != "" {
		t.Fatalf("cross-provider model should not get Antigravity web search model, got %q", got)
	}
	if got := AntigravityWebSearchModelFor("unknown-model"); got != "" {
		t.Fatalf("unknown model should not get Antigravity web search model, got %q", got)
	}
}

func TestCodexReserveBuiltinIsScopedToPlusAndPro(t *testing.T) {
	count := func(models []*ModelInfo) int {
		n := 0
		for _, model := range models {
			if model != nil && model.ID == codexBuiltinReserveModelID {
				n++
			}
		}
		return n
	}
	if got := count(GetCodexPlusModels()); got != 1 {
		t.Fatalf("Plus gpt-reserve count = %d, want 1", got)
	}
	if got := count(GetCodexProModels()); got != 1 {
		t.Fatalf("Pro gpt-reserve count = %d, want 1", got)
	}
	// Reserve is a Plus/Pro entitlement. Advertising it on Team or Free would route
	// a request upstream that can only fail.
	if got := count(GetCodexTeamModels()); got != 0 {
		t.Fatalf("Team gpt-reserve count = %d, want 0", got)
	}
	if got := count(GetCodexFreeModels()); got != 0 {
		t.Fatalf("Free gpt-reserve count = %d, want 0", got)
	}
}

func TestLookupStaticModelInfoResolvesCodexReserve(t *testing.T) {
	info := LookupStaticModelInfo(codexBuiltinReserveModelID)
	if info == nil || info.Type != "openai" || info.DisplayName != "GPT Reserve" {
		t.Fatalf("gpt-reserve static lookup = %+v", info)
	}
	if info.Config == nil || info.Config.OverrideHeader["originator"] != "codex-tui" {
		t.Fatalf("gpt-reserve must carry the codex-tui originator override, got %+v", info.Config)
	}
}
