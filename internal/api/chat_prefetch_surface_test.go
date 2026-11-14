package api

// v0.10.2134 (router boşlukları D1) — döngü satırının kayıt etiketi: yalnız
// üretim "chat" + tohumlu tur "chat-prefetch" olur; tohumsuz tur ve
// değerlendirme etiketi (evalset-*) aynen.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

func TestPrefetchSeededLoop(t *testing.T) {
	for _, c := range []struct {
		cur    string
		seeded bool
		want   bool
	}{
		{aisurface.Chat, true, true},
		{aisurface.Chat, false, false},
		{"evalset-ChatLoop", true, false},
		{"", true, false},
	} {
		if got := prefetchSeededLoop(c.cur, c.seeded); got != c.want {
			t.Errorf("prefetchSeededLoop(%q,%v) = %v, want %v", c.cur, c.seeded, got, c.want)
		}
	}
}
