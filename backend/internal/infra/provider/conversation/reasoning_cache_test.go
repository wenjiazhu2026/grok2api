package conversation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReasoningCacheSetAndGet(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "session-a/build"
	sample := responseItem{
		ID:        "rs_123",
		Type:      "reasoning",
		Status:    "completed",
		Encrypted: "encrypted-content-sample",
	}

	cache.SetScoped(scope, "call-1", sample)
	got, found := cache.GetScoped(scope, "call-1")
	if !found {
		t.Fatalf("expected call-1 to be found in cache")
	}
	if got.Encrypted != sample.Encrypted || got.ID != sample.ID {
		t.Fatalf("expected %v, got %v", sample, got)
	}

	_, found2 := cache.GetScoped(scope, "non-existent")
	if found2 {
		t.Fatalf("expected non-existent to return false")
	}
}

func TestRememberReasoningForEnvelope(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "session-a/build"
	env := responseEnvelope{
		Output: []responseItem{
			{
				ID:        "rs_abc",
				Type:      "reasoning",
				Status:    "completed",
				Encrypted: "encrypted_abc",
			},
			{
				ID:     "fc_1",
				Type:   "function_call",
				CallID: "call_abc_1",
				Name:   "test_fn",
			},
			{
				ID:     "fc_2",
				Type:   "function_call",
				CallID: "call_abc_2",
				Name:   "test_fn2",
			},
		},
	}

	cache.RememberReasoningForEnvelope(scope, env)

	r1, found1 := cache.GetScoped(scope, "call_abc_1")
	if !found1 || r1.Encrypted != "encrypted_abc" {
		t.Fatalf("expected call_abc_1 to be cached, got %v", r1)
	}

	r2, found2 := cache.GetScoped(scope, "call_abc_2")
	if !found2 || r2.Encrypted != "encrypted_abc" {
		t.Fatalf("expected call_abc_2 to be cached, got %v", r2)
	}
}

func TestReasoningCacheScopesAndLRU(t *testing.T) {
	cache := newReasoningCache(2, time.Hour)
	item := func(id string) responseItem {
		return responseItem{ID: id, Type: "reasoning", Encrypted: id + "-encrypted"}
	}
	cache.SetScoped("session-a/build", "call-1", item("one"))
	cache.SetScoped("session-a/build", "call-2", item("two"))
	if _, ok := cache.GetScoped("session-b/build", "call-1"); ok {
		t.Fatal("reasoning leaked across scopes")
	}
	if _, ok := cache.GetScoped("session-a/xai", "call-1"); ok {
		t.Fatal("reasoning leaked across planes")
	}
	if _, ok := cache.GetScoped("session-a/build", "call-1"); !ok {
		t.Fatal("call-1 should be present before LRU access")
	}
	cache.SetScoped("session-a/build", "call-3", item("three"))
	if _, ok := cache.GetScoped("session-a/build", "call-1"); !ok {
		t.Fatal("recently-read call-1 was evicted; cache is not LRU")
	}
	if _, ok := cache.GetScoped("session-a/build", "call-2"); ok {
		t.Fatal("least-recently-used call-2 was not evicted")
	}
}

func TestRememberReasoningForEnvelopePairsItemsInOrder(t *testing.T) {
	cache := newReasoningCache(10, time.Hour)
	scope := "session-a/build"
	cache.RememberReasoningForEnvelope(scope, responseEnvelope{Output: []responseItem{
		{ID: "reasoning-a", Type: "reasoning", Encrypted: "enc-a"},
		{ID: "call-item-a", Type: "function_call", CallID: "call-a"},
		{ID: "reasoning-b", Type: "reasoning", Encrypted: "enc-b"},
		{ID: "call-item-b", Type: "function_call", CallID: "call-b"},
	}})
	a, ok := cache.GetScoped(scope, "call-a")
	if !ok || a.ID != "reasoning-a" {
		t.Fatalf("call-a reasoning = %#v, found=%v", a, ok)
	}
	b, ok := cache.GetScoped(scope, "call-b")
	if !ok || b.ID != "reasoning-b" {
		t.Fatalf("call-b reasoning = %#v, found=%v", b, ok)
	}
}

func TestReasoningInputItemOmitsNullContent(t *testing.T) {
	payload := reasoningInputItem(responseItem{ID: "rs-1", Type: "reasoning", Encrypted: "opaque"})
	if _, exists := payload["content"]; exists {
		t.Fatalf("content must be omitted for an opaque item: %#v", payload)
	}
	if summary, ok := payload["summary"].([]any); !ok || len(summary) != 0 {
		t.Fatalf("opaque reasoning must carry an empty summary array: %#v", payload)
	}
}

func TestReasoningInputItemOmitsSyntheticContentFields(t *testing.T) {
	payload := reasoningInputItem(responseItem{
		ID:        "rs-2",
		Type:      "reasoning",
		Encrypted: "opaque",
		Summary:   []responseContent{{Type: "summary_text", Text: "plan"}},
		Content:   []responseContent{{Type: "reasoning_text", Text: "detail"}},
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, unwanted := range []string{`"content":null`, `"refusal":""`, `"annotations":null`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("replay item contains synthetic field %s: %s", unwanted, text)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if summary := decoded["summary"].([]any)[0].(map[string]any); summary["type"] != "summary_text" || summary["text"] != "plan" {
		t.Fatalf("summary fields were not preserved: %#v", decoded["summary"])
	}
}
