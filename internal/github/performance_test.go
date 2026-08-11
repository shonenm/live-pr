package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkNavigatorSerialization(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("prs=%d", size), func(b *testing.B) {
			cache := NewNavigatorCache()
			cache.PRs = make([]PR, size)
			for i := range cache.PRs {
				cache.PRs[i] = PR{Number: i + 1, State: "OPEN", Title: fmt.Sprintf("Pull request %d", i), Body: "cached preview body"}
				if i%10 == 0 {
					cache.SetSnapshot(PRSnapshot{PR: cache.PRs[i], Comments: []Comment{{Body: "cached comment body"}}})
				}
			}
			encoded, err := json.Marshal(cache)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(encoded)), "bytes")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := json.Marshal(cache); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	for _, size := range []int{100 << 10, 1 << 20, 10 << 20} {
		b.Run(fmt.Sprintf("payload=%d", size), func(b *testing.B) {
			cache := NewNavigatorCache()
			cache.SetSnapshot(PRSnapshot{PR: PR{Number: 1}, Comments: []Comment{{Body: strings.Repeat("x", size)}}})
			encoded, err := json.Marshal(cache)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(encoded)), "bytes")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := json.Marshal(cache); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
