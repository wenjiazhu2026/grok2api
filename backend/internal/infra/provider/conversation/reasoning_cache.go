package conversation

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

const (
	defaultReasoningCacheCapacity = 4096
	defaultReasoningCacheTTL      = 30 * time.Minute
)

type reasoningCacheEntry struct {
	key       string
	item      responseItem
	createdAt time.Time
}

// ReasoningCache stores only short-lived, scoped reasoning proofs. A scope is
// supplied by the caller and must identify the client conversation and the
// upstream plane; a call_id by itself is never a sufficient cache key.
type ReasoningCache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	order    *list.List
	capacity int
	ttl      time.Duration
}

func NewReasoningCache(capacity int, ttl time.Duration) *ReasoningCache {
	if capacity <= 0 {
		capacity = defaultReasoningCacheCapacity
	}
	if ttl <= 0 {
		ttl = defaultReasoningCacheTTL
	}
	return &ReasoningCache{
		entries:  make(map[string]*list.Element, capacity),
		order:    list.New(),
		capacity: capacity,
		ttl:      ttl,
	}
}

// newReasoningCache is kept local to the package tests and makes the default
// policy explicit without exposing cache internals to providers.
func newReasoningCache(capacity int, ttl time.Duration) *ReasoningCache {
	return NewReasoningCache(capacity, ttl)
}

func scopedReasoningCacheKey(scope, callID string) string {
	scope = strings.TrimSpace(scope)
	callID = normalizeReasoningCallID(callID)
	if scope == "" || callID == "" {
		return ""
	}
	return scope + "\x00" + callID
}

// normalizeReasoningCallID handles the item-id suffix emitted by some
// Responses clients while retaining the actual conversation scope.
func normalizeReasoningCallID(callID string) string {
	callID = strings.TrimSpace(callID)
	if index := strings.IndexByte(callID, '|'); index > 0 {
		callID = strings.TrimSpace(callID[:index])
	}
	return callID
}

func reasoningCallIDCandidates(callID string) []string {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil
	}
	result := []string{callID}
	canonical := normalizeReasoningCallID(callID)
	if canonical != callID && canonical != "" {
		result = append(result, canonical)
	}
	const anthropicPrefix = "toolu_"
	if strings.HasPrefix(canonical, anthropicPrefix) {
		if upstream := strings.TrimPrefix(canonical, anthropicPrefix); upstream != "" {
			result = append(result, upstream)
		}
	} else {
		result = append(result, anthropicPrefix+canonical)
	}
	return result
}

// SetScoped records one proof under a conversation/plane scope.
func (c *ReasoningCache) SetScoped(scope, callID string, item responseItem) {
	if c == nil || item.Type != "reasoning" || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Encrypted) == "" {
		return
	}
	key := scopedReasoningCacheKey(scope, callID)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if element, exists := c.entries[key]; exists {
		entry := element.Value.(*reasoningCacheEntry)
		entry.item = cloneResponseItem(item)
		entry.createdAt = now
		c.order.MoveToFront(element)
		return
	}
	for c.order.Len() >= c.capacity {
		c.removeExpiredOrOldestLocked(now)
		if c.order.Len() < c.capacity {
			break
		}
		c.removeElementLocked(c.order.Back())
	}
	entry := &reasoningCacheEntry{key: key, item: cloneResponseItem(item), createdAt: now}
	c.entries[key] = c.order.PushFront(entry)
}

// GetScoped returns a proof only from the requested conversation/plane scope.
// Reads refresh recency, making the bounded cache an actual LRU rather than a
// FIFO queue.
func (c *ReasoningCache) GetScoped(scope, callID string) (responseItem, bool) {
	if c == nil || strings.TrimSpace(scope) == "" {
		return responseItem{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for _, candidate := range reasoningCallIDCandidates(callID) {
		key := scopedReasoningCacheKey(scope, candidate)
		element, exists := c.entries[key]
		if !exists {
			continue
		}
		entry := element.Value.(*reasoningCacheEntry)
		if now.Sub(entry.createdAt) > c.ttl {
			c.removeElementLocked(element)
			continue
		}
		c.order.MoveToFront(element)
		return cloneResponseItem(entry.item), true
	}
	return responseItem{}, false
}

func (c *ReasoningCache) removeElementLocked(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*reasoningCacheEntry)
	delete(c.entries, entry.key)
	c.order.Remove(element)
}

func (c *ReasoningCache) removeExpiredOrOldestLocked(now time.Time) {
	for element := c.order.Back(); element != nil; {
		previous := element.Prev()
		entry := element.Value.(*reasoningCacheEntry)
		if now.Sub(entry.createdAt) > c.ttl {
			c.removeElementLocked(element)
		}
		element = previous
	}
}

// RememberReasoningForEnvelope associates each function call with the most
// recent preceding reasoning item. A single reasoning item may legitimately
// precede several parallel calls; separate reasoning items are never all
// collapsed onto the last call.
func (c *ReasoningCache) RememberReasoningForEnvelope(scope string, envelope responseEnvelope) {
	if c == nil || strings.TrimSpace(scope) == "" || len(envelope.Output) == 0 {
		return
	}
	var current *responseItem
	for _, item := range envelope.Output {
		switch item.Type {
		case "reasoning":
			if item.Encrypted == "" {
				current = nil
				continue
			}
			copy := cloneResponseItem(item)
			current = &copy
		case "function_call":
			if current != nil && item.CallID != "" {
				c.SetScoped(scope, item.CallID, *current)
			}
		}
	}
}

func cloneResponseItem(item responseItem) responseItem {
	clone := item
	if item.Content != nil {
		clone.Content = append([]responseContent(nil), item.Content...)
	}
	if item.Summary != nil {
		clone.Summary = append([]responseContent(nil), item.Summary...)
	}
	if item.Action != nil {
		clone.Action = make(map[string]any, len(item.Action))
		for key, value := range item.Action {
			clone.Action[key] = value
		}
	}
	return clone
}
