package markdown

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRenderCacheBudgetAndFIFO(t *testing.T) {
	var c renderedCache
	text := strings.Repeat("x", maxRenderEntryBytes/2)
	for i := 0; i < 100; i++ {
		key := cacheKey{width: i, text: text}
		c.put(key, text)
		if c.bytes > maxRenderCacheBytes {
			t.Fatalf("payload budget exceeded: %d", c.bytes)
		}
	}
	if _, ok := c.get(cacheKey{width: 0, text: text}); ok {
		t.Fatal("oldest entry retained")
	}
	if _, ok := c.get(cacheKey{width: 99, text: text}); !ok {
		t.Fatal("new entry missing")
	}
	bytes := c.bytes
	c.put(cacheKey{width: 99, text: text}, text)
	if c.bytes != bytes {
		t.Fatal("duplicate counted twice")
	}
	c.put(cacheKey{text: strings.Repeat("x", maxRenderEntryBytes+1)}, "")
	c.put(cacheKey{text: "large-output"}, strings.Repeat("x", maxRenderEntryBytes))
	if c.bytes != bytes {
		t.Fatal("oversized input/output cached")
	}
}

func TestRenderCacheEntryLimitAndAccounting(t *testing.T) {
	var c renderedCache
	for i := 0; i < maxRenderCacheEntries+20; i++ {
		c.put(cacheKey{text: fmt.Sprint(i)}, "rendered")
	}
	if len(c.items) != maxRenderCacheEntries || len(c.order) != maxRenderCacheEntries {
		t.Fatalf("entry count %d/%d", len(c.items), len(c.order))
	}
	sum := 0
	for key, out := range c.items {
		sum += len(key.text) + len(out)
	}
	if c.bytes != sum {
		t.Fatalf("accounting %d != %d", c.bytes, sum)
	}
}

func TestRenderCacheConcurrentDuplicate(t *testing.T) {
	var c renderedCache
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { c.put(cacheKey{width: 80, text: "body"}, "output") })
	}
	wg.Wait()
	if c.bytes != len("bodyoutput") || len(c.items) != 1 {
		t.Fatalf("duplicate accounting: %+v", &c)
	}
}
