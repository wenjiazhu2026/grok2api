package conversation

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestChatMultiTurnReasoningRestoration(t *testing.T) {
	// 模拟第一轮：上游返回 Responses 包含 reasoning 和 function_call
	callID := "call_integration_test_123"
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	env := responseEnvelope{
		Output: []responseItem{
			{
				ID:        "rs_integ_001",
				Type:      "reasoning",
				Status:    "completed",
				Encrypted: "encrypted_secret_chain_proof",
			},
			{
				ID:        "fc_integ_001",
				Type:      "function_call",
				CallID:    callID,
				Name:      "list_dir",
				Arguments: `{"path":"/"}`,
			},
		},
	}

	// 触发出站记录
	cache.RememberReasoningForEnvelope(scope, env)

	// 模拟第二轮：下游客户端传回标准的 Chat Completions 历史（带有刚才的 tool_calls 和 tool output）
	chatReqJSON := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "hello investigate"},
			{
				"role": "assistant",
				"content": null,
				"tool_calls": [
					{
						"id": "` + callID + `",
						"type": "function",
						"function": {
							"name": "list_dir",
							"arguments": "{\"path\":\"/\"}"
						}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "` + callID + `",
				"content": "{\"error\":\"not found\"}"
			}
		]
	}`)

	convertedBody, _, err := ConvertRequestWithReasoningReplay(chatReqJSON, "grok-4.6", OperationChat, cache, scope)
	if err != nil {
		t.Fatalf("ConvertRequestWithOptions failed: %v", err)
	}

	var convertedMap map[string]any
	if err := json.Unmarshal(convertedBody, &convertedMap); err != nil {
		t.Fatalf("Unmarshal convertedBody failed: %v", err)
	}

	inputs, ok := convertedMap["input"].([]any)
	if !ok || len(inputs) == 0 {
		t.Fatalf("expected non-empty input array, got: %v", convertedMap["input"])
	}

	// 验证 input 列表中必须包含我们注入的 reasoning 项！
	foundReasoning := false
	for _, itemRaw := range inputs {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		if item["type"] == "reasoning" {
			if item["id"] == "rs_integ_001" && item["encrypted_content"] == "encrypted_secret_chain_proof" {
				foundReasoning = true
				break
			}
		}
	}

	if !foundReasoning {
		t.Fatalf("FAIL: expected reasoning block with id rs_integ_001 to be restored in input, but not found. Inputs: %#v", inputs)
	}
	for _, itemRaw := range inputs {
		item, ok := itemRaw.(map[string]any)
		if ok && item["type"] == "reasoning" {
			if _, exists := item["content"]; exists {
				t.Fatalf("opaque reasoning replay must omit content: %#v", item)
			}
		}
	}
}

func TestAnthropicWebSearchDoesNotInjectCachedReasoning(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	cache.SetScoped(scope, "call_search", responseItem{
		ID: "rs_search", Type: "reasoning", Encrypted: "search-proof",
	})
	body := []byte(`{
		"model":"grok-4.6","max_tokens":128,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_call_search","name":"lookup","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_call_search","content":"ok"}]}
		],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]
	}`)
	converted, options, err := ConvertRequestWithReasoningReplay(body, "grok-4.6", OperationMessages, cache, scope)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(converted, &payload); err != nil {
		t.Fatal(err)
	}
	for _, item := range payload.Input {
		if item["type"] == "reasoning" {
			t.Fatalf("web-search request received cached reasoning: %#v", payload.Input)
		}
	}
	if options.reasoningCache != nil || options.reasoningScope != "" {
		t.Fatalf("web-search options retained reasoning bridge: %#v", options)
	}
}

func TestAnthropicFunctionToolRestoresCachedReasoning(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	cache.SetScoped(scope, "call_function", responseItem{
		ID: "rs_function", Type: "reasoning", Encrypted: "function-proof",
	})
	body := []byte(`{
		"model":"grok-4.6","max_tokens":128,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_call_function","name":"lookup","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_call_function","content":"ok"}]}
		],
		"tools":[{"name":"lookup","input_schema":{"type":"object"}}]
	}`)
	converted, options, err := ConvertRequestWithReasoningReplay(body, "grok-4.6", OperationMessages, cache, scope)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(converted, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) < 2 || payload.Input[0]["type"] != "reasoning" || payload.Input[0]["encrypted_content"] != "function-proof" {
		t.Fatalf("function reasoning was not restored: %#v", payload.Input)
	}
	if options.reasoningCache == nil || options.reasoningScope == "" {
		t.Fatal("ordinary Messages request lost reasoning bridge")
	}
}

func TestStreamOutputItemDoneFeedsReasoningReplayWhenCompletedOutputIsEmpty(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	stream := strings.Join([]string{
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"rs_stream","type":"reasoning","status":"completed","encrypted_content":"stream-proof"}}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"fc_stream","type":"function_call","call_id":"call_stream","name":"list_dir","arguments":"{}"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_stream","status":"completed","output":[]}}`,
		``,
	}, "\n")
	converted := ConvertResponseStreamWithOptions(
		io.NopCloser(strings.NewReader(stream)),
		OperationChat,
		ResponseOptions{}.WithReasoningReplay(cache, scope),
	)
	if _, err := io.ReadAll(converted); err != nil {
		t.Fatalf("stream conversion failed: %v", err)
	}
	if _, ok := cache.GetScoped(scope, "call_stream"); !ok {
		t.Fatal("output_item.done reasoning was not cached")
	}

	body := []byte(`{"model":"grok-4.6","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_stream","type":"function","function":{"name":"list_dir","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_stream","content":"ok"}]}`)
	convertedBody, _, err := ConvertRequestWithReasoningReplay(body, "grok-4.6", OperationChat, cache, scope)
	if err != nil {
		t.Fatalf("request conversion failed: %v", err)
	}
	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(convertedBody, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) < 2 || payload.Input[0]["type"] != "reasoning" || payload.Input[0]["encrypted_content"] != "stream-proof" || payload.Input[1]["type"] != "function_call" {
		t.Fatalf("restored input = %#v", payload.Input)
	}
}

func TestStreamCompletedOutputFeedsReasoningReplayWhenItemDoneIsOmitted(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	stream := strings.Join([]string{
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_terminal","status":"completed","output":[{"id":"rs_terminal","type":"reasoning","status":"completed","encrypted_content":"terminal-proof"},{"id":"fc_terminal","type":"function_call","call_id":"call_terminal","name":"list_dir","arguments":"{}"}]}}`,
		``,
	}, "\n")
	converted := ConvertResponseStreamWithOptions(
		io.NopCloser(strings.NewReader(stream)),
		OperationChat,
		ResponseOptions{}.WithReasoningReplay(cache, scope),
	)
	if _, err := io.ReadAll(converted); err != nil {
		t.Fatalf("stream conversion failed: %v", err)
	}
	item, ok := cache.GetScoped(scope, "call_terminal")
	if !ok || item.Encrypted != "terminal-proof" {
		t.Fatalf("terminal output was not cached: item=%#v found=%v", item, ok)
	}
}

func TestStreamReplayPreservesOutputItemDoneOrderWhenTerminalOmitsReasoning(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	stream := strings.Join([]string{
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"rs_ordered","type":"reasoning","status":"completed","encrypted_content":"ordered-proof"}}`, "",
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"fc_ordered","type":"function_call","call_id":"call_ordered","name":"list_dir","arguments":"{}"}}`, "",
		// The terminal envelope contains only the function call. Some Build
		// responses do this even though reasoning was sent in item.done.
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_ordered","status":"completed","output":[{"id":"fc_ordered","type":"function_call","call_id":"call_ordered","name":"list_dir","arguments":"{}"}]}}`, "",
	}, "\n")
	converted := ConvertResponseStreamWithOptions(io.NopCloser(strings.NewReader(stream)), OperationChat, ResponseOptions{}.WithReasoningReplay(cache, scope))
	if _, err := io.ReadAll(converted); err != nil {
		t.Fatalf("stream conversion failed: %v", err)
	}
	item, ok := cache.GetScoped(scope, "call_ordered")
	if !ok || item.Encrypted != "ordered-proof" {
		t.Fatalf("reasoning item.done was not paired with the later function call: item=%#v found=%v", item, ok)
	}
}

func TestStreamWithoutResponsesTerminalDoesNotCommitReasoningReplay(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	// The source ends after item.done without response.completed/incomplete.
	// Downstream compatibility still emits its normal terminator, but this is
	// not sufficient evidence that the upstream tool turn completed.
	stream := strings.Join([]string{
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"rs_truncated","type":"reasoning","encrypted_content":"truncated-proof"}}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"fc_truncated","type":"function_call","call_id":"call_truncated","name":"list_dir","arguments":"{}"}}`,
		``,
	}, "\n")
	converted := ConvertResponseStreamWithOptions(
		io.NopCloser(strings.NewReader(stream)),
		OperationChat,
		ResponseOptions{}.WithReasoningReplay(cache, scope),
	)
	if _, err := io.ReadAll(converted); err != nil {
		t.Fatalf("stream conversion failed: %v", err)
	}
	if _, ok := cache.GetScoped(scope, "call_truncated"); ok {
		t.Fatal("truncated stream polluted reasoning replay cache")
	}
}

func TestAnthropicParallelToolUsesInjectOneReasoningProof(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	proof := responseItem{
		ID:        "rs_parallel",
		Type:      "reasoning",
		Encrypted: "parallel-proof",
		Summary:   []responseContent{{Type: "summary_text", Text: "plan"}},
	}
	cache.SetScoped(scope, "call-a", proof)
	cache.SetScoped(scope, "call-b", proof)

	messages := []anthropicMessage{
		{Role: "assistant", Content: json.RawMessage(`[
			{"type":"tool_use","id":"toolu_call-a","name":"first","input":{}},
			{"type":"tool_use","id":"toolu_call-b","name":"second","input":{}}
		]`)},
		{Role: "user", Content: json.RawMessage(`[
			{"type":"tool_result","tool_use_id":"toolu_call-a","content":"one"},
			{"type":"tool_result","tool_use_id":"toolu_call-b","content":"two"}
		]`)},
	}
	input, _, err := convertAnthropicMessagesWithReasoningReplay(messages, nil, cache, scope)
	if err != nil {
		t.Fatal(err)
	}
	reasoningCount := 0
	functionCount := 0
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "reasoning":
			reasoningCount++
			if item["encrypted_content"] != "parallel-proof" {
				t.Fatalf("reasoning = %#v", item)
			}
		case "function_call":
			functionCount++
		}
	}
	if reasoningCount != 1 || functionCount != 2 {
		t.Fatalf("parallel tool input = %#v", input)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"content":null`) || strings.Contains(string(encoded), `"refusal":""`) || strings.Contains(string(encoded), `"annotations":null`) {
		t.Fatalf("replayed reasoning contains synthetic null fields: %s", encoded)
	}
}

func TestAnthropicExplicitThinkingSignatureSuppressesCachedDuplicate(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	cache.SetScoped(scope, "call-a", responseItem{ID: "rs_cached", Type: "reasoning", Encrypted: "same-proof"})
	messages := []anthropicMessage{
		{Role: "assistant", Content: json.RawMessage(`[
			{"type":"thinking","thinking":"plan","signature":"same-proof"},
			{"type":"tool_use","id":"toolu_call-a","name":"first","input":{}}
		]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"toolu_call-a","content":"ok"}]`)},
	}
	input, _, err := convertAnthropicMessagesWithReasoningReplay(messages, nil, cache, scope)
	if err != nil {
		t.Fatal(err)
	}
	reasoningCount := 0
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["type"] == "reasoning" {
			reasoningCount++
		}
	}
	if reasoningCount != 1 {
		t.Fatalf("cached proof duplicated explicit thinking: %#v", input)
	}
}

func TestStreamReasoningReplayCommitsOnlyAfterSuccessfulTerminal(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "client-session/build"
	failed := strings.Join([]string{
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"rs_failed","type":"reasoning","encrypted_content":"failed-proof"}}`, "",
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"id":"fc_failed","type":"function_call","call_id":"call_failed","name":"first","arguments":"{}"}}`, "",
		`event: response.failed`,
		`data: {"type":"response.failed","response":{"error":{"message":"upstream failed"}}}`, "", "",
	}, "\n")
	converted := ConvertResponseStreamWithOptions(io.NopCloser(strings.NewReader(failed)), OperationChat, ResponseOptions{}.WithReasoningReplay(cache, scope))
	if _, err := io.ReadAll(converted); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.GetScoped(scope, "call_failed"); ok {
		t.Fatal("failed stream left reasoning proof in replay cache")
	}
}
