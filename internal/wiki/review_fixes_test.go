package wiki

// v0.10.1124 inceleme düzeltmeleri — F4: senkron sürerken mod canlıya
// alınırsa geçiş durur ve budamaz; canlı önbellek anahtarı yol biçiminden
// bağımsız. Adlar sentetik.

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSyncStopsWhenModeSwitchedToLive(t *testing.T) {
	f := runbookWiki()
	f.itemsOff = true // sayfa başına içerik okuması
	s, st := newTestService(t, f)
	f.onPageGet = func(n int) {
		if n == 1 {
			// İlk sayfa okunurken operatör modu canlıya aldı.
			go s.Configure(Config{Enabled: true, Mode: ModeLive})
			time.Sleep(20 * time.Millisecond)
		}
	}
	res, _ := s.Sync(context.Background())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, "mod canlıya alındı") {
			found = true
		}
	}
	if !found {
		t.Fatalf("geçiş durdurulduğunu söylemeli: %+v", res.Errors)
	}
	f.mu.Lock()
	gets := 0
	for _, c := range f.pageGets {
		gets += c
	}
	f.mu.Unlock()
	if gets >= 4 {
		t.Errorf("mod değişince kalan sayfalar okunmamalı: %d okuma", gets)
	}
	st.mu.Lock()
	dels := st.deletes
	st.mu.Unlock()
	if dels != 0 {
		t.Errorf("durdurulan geçiş budama yapmamalı: %d silme", dels)
	}
}

func TestLiveCacheKeyNormalizesPath(t *testing.T) {
	c := newLiveCache()
	now := time.UnixMilli(1_000_000)
	c.put(PageRecord{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Runbooks/A", Content: "x"}, nil, now)
	for _, p := range []string{"Runbooks/A", "/Runbooks/A/", "/Runbooks/A.md"} {
		if _, _, ok := c.get(liveKey("platform", "PLATFORM.WIKI", p), now); !ok {
			t.Errorf("yol biçimi %q aynı kayda düşmeli", p)
		}
	}
}

func TestMarkPurgedZeroesCounts(t *testing.T) {
	f := runbookWiki()
	s, _ := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status(context.Background(), true).IndexedPages == 0 {
		t.Fatal("ön koşul: indeks dolu")
	}
	st := s.MarkPurged(context.Background())
	if st.IndexedPages != 0 || st.IndexedChunks != 0 || st.LastFinishedAt == 0 {
		t.Errorf("temizlik sonrası sayılar sıfır, damga korunur: %+v", st)
	}
}
