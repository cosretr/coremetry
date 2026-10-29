package uistream

// ulid_test.go — v0.10.1603 (operatör kararı 2026-10-29 §3): `start`
// messageId'si için bağımlılıksız ULID. Pinler: kodlama (zaman ilk 10
// karakterde, Crockford alfabesi), monotonluk (aynı ms / saat geri / 80 bit
// taşması), eşzamanlılık, emitter'ın start parçasına yazması.

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEncodeULID(t *testing.T) {
	ones := [10]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	for _, c := range []struct {
		name string
		ms   uint64
		r    [10]byte
		want string
	}{
		{"sıfır", 0, [10]byte{}, "00000000000000000000000000"},
		{"ms=1", 1, [10]byte{}, "00000000010000000000000000"},
		{"ms=32", 32, [10]byte{}, "00000000100000000000000000"},
		{"rastgele son bit", 0, [10]byte{9: 1}, "00000000000000000000000001"},
		{"azami", 1<<48 - 1, ones, "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"},
		{"yalnız rastgele dolu", 0, ones, "0000000000ZZZZZZZZZZZZZZZZ"},
	} {
		if got := encodeULID(c.ms, c.r); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	g := GoldenMessageID()
	if len(g) != 26 || strings.Trim(g, crockford) != "" || !strings.HasSuffix(g, strings.Repeat("0", 16)) {
		t.Fatalf("GoldenMessageID geçersiz: %s", g)
	}
}

func TestULIDGeneratorMonotonic(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	clock := func() time.Time { return now }
	g := NewULIDGenerator(clock, bytes.NewReader(bytes.Repeat([]byte{0x10}, 1000)))
	prev := g.New()
	step := func(label string) {
		t.Helper()
		cur := g.New()
		if len(cur) != 26 || cur <= prev {
			t.Fatalf("%s: %s ≤ %s", label, cur, prev)
		}
		prev = cur
	}
	step("aynı ms")
	step("aynı ms 2")
	now = now.Add(-time.Hour)
	step("saat geri")
	if !strings.HasPrefix(prev, encodeULID(1_000_000, [10]byte{})[:10]) {
		t.Fatalf("saat geri giderken zaman damgası korunmalı: %s", prev)
	}
	now = time.UnixMilli(2_000_000)
	step("yeni ms")

	// 80 bit taşması → zaman 1 ms ileri, yine artan.
	ff := bytes.Repeat([]byte{0xFF}, 10)
	o := NewULIDGenerator(func() time.Time { return time.UnixMilli(5) }, bytes.NewReader(ff))
	a, b := o.New(), o.New()
	if b <= a || b[:10] != encodeULID(6, [10]byte{})[:10] {
		t.Fatalf("taşma: %s → %s", a, b)
	}
	// Entropi biterse yine artan.
	e := NewULIDGenerator(func() time.Time { return time.UnixMilli(int64(len(a))) }, bytes.NewReader(nil))
	x := e.New()
	if len(x) != 26 {
		t.Fatalf("entropisiz: %s", x)
	}
}

func TestULIDGeneratorConcurrentUnique(t *testing.T) {
	g := NewULIDGenerator(func() time.Time { return time.UnixMilli(42) }, nil)
	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				id := g.New()
				mu.Lock()
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 1600 {
		t.Fatalf("tekil olmayan kimlik: %d/1600", len(seen))
	}
}

func TestEmitterStartCarriesMessageID(t *testing.T) {
	for _, c := range []struct {
		name string
		gen  func() string
		want func(string) bool
	}{
		{"enjekte", func() string { return "01TEST" }, func(s string) bool { return s == "01TEST" }},
		{"varsayılan ULID", nil, func(s string) bool { return len(s) == 26 && strings.Trim(s, crockford) == "" }},
	} {
		rec := httptest.NewRecorder()
		e, _ := NewEmitter(rec, Options{MessageID: c.gen})
		e.Emit("delta", map[string]any{"text": "x"})
		e.Close()
		first := strings.SplitN(rec.Body.String(), "\n", 2)[0]
		var p map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(first, "data: ")), &p); err != nil {
			t.Fatal(err)
		}
		id, _ := p["messageId"].(string)
		if p["type"] != "start" || !c.want(id) {
			t.Errorf("%s: ilk parça %v", c.name, p)
		}
	}
	// Çeviricinin kendisi kimlik uydurmaz.
	if p := NewTranslator().Translate("delta", map[string]any{"text": "x"})[0]; p["messageId"] != nil {
		t.Fatalf("NewTranslator messageId uydurdu: %v", p)
	}
}
