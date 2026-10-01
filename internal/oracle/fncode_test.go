package oracle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1000 — özne çözücünün FONKSİYON KODU basamağı (fncode.go): satırın kod
// alanı span'lerdeki FUNCTION_CODE ile aynı değer; trace'i olmayan operasyon
// o kodu taşıyan span'lerin servisine bağlanır ve eşleme zamanla öğrenilir.

func TestPickFunctionService(t *testing.T) {
	fs := func(svc string, spans, errs uint64) chstore.FunctionCodeService {
		return chstore.FunctionCodeService{Service: svc, Spans: spans, Errors: errs}
	}
	cases := []struct {
		name     string
		dist     []chstore.FunctionCodeService
		want     string
		best     string
		hits     uint64
		total    uint64
		onErrors bool
	}{
		{"boş dağılım", nil, "", "", 0, 0, false},
		{"tek servis, hata span'leri", []chstore.FunctionCodeService{fs("eft-svc", 400, 12)}, "eft-svc", "eft-svc", 12, 12, true},
		// Hata oyları belirleyici: çağrı hacmi gateway'de büyük ama hata eft-svc'de.
		{"hata çoğunluğu hacmi yener", []chstore.FunctionCodeService{fs("gateway", 5000, 2), fs("eft-svc", 900, 18)}, "eft-svc", "eft-svc", 18, 20, true},
		// Zincir boyunca eşit hata: çoğunluk yok → servis uydurulmaz.
		{"zincirde eşit hata", []chstore.FunctionCodeService{fs("gateway", 100, 10), fs("eft-svc", 100, 10), fs("core", 100, 10)}, "", "core", 10, 30, true},
		// Hata span'i < 3 → tüm span'ler oy olur.
		{"hata az, hacimden", []chstore.FunctionCodeService{fs("eft-svc", 90, 1), fs("gateway", 10, 0)}, "eft-svc", "eft-svc", 90, 100, false},
		{"hacimde çoğunluk yok", []chstore.FunctionCodeService{fs("a", 60, 0), fs("b", 40, 0)}, "", "a", 60, 100, false},
		{"kanıt az (2 span)", []chstore.FunctionCodeService{fs("eft-svc", 2, 0)}, "", "eft-svc", 2, 2, false},
		{"tam eşik %70", []chstore.FunctionCodeService{fs("a", 0, 7), fs("b", 0, 3)}, "a", "a", 7, 10, true},
	}
	for _, c := range cases {
		got := PickFunctionService(c.dist)
		if got.Service != c.want || got.Best != c.best || got.Hits != c.hits || got.Total != c.total || got.OnErrors != c.onErrors {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestMergeFunctionDists(t *testing.T) {
	got := MergeFunctionDists(
		[]chstore.FunctionCodeService{{Service: "b", Spans: 1, Errors: 1}, {Service: "a", Spans: 2}},
		[]chstore.FunctionCodeService{{Service: "a", Spans: 3, Errors: 4}},
	)
	if len(got) != 2 || got[0] != (chstore.FunctionCodeService{Service: "a", Spans: 5, Errors: 4}) || got[1].Service != "b" {
		t.Fatalf("birleşim: %+v", got)
	}
}

func TestPruneFnCache(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	cache := map[string]fnFact{"old": {at: t0}, "mid": {at: t0.Add(time.Minute)}, "new": {at: t0.Add(2 * time.Minute)}}
	pruneFnCache(cache, 2)
	if _, ok := cache["old"]; ok || len(cache) != 2 {
		t.Fatalf("en eski düşmeli: %v", cache)
	}
}

// fnHarness — sahte fonksiyon kodu okuyuculu çözücü.
type fnHarness struct {
	r      *SubjectResolver
	now    time.Time
	state  *fakeState
	calls  [][]string
	facts  map[string][]chstore.FunctionCodeService
	source string
	err    error
}

func newFnHarness(t *testing.T) *fnHarness {
	t.Helper()
	h := &fnHarness{now: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), state: &fakeState{kv: map[string][]byte{}},
		facts: map[string][]chstore.FunctionCodeService{}, source: chstore.FunctionCodeSourceRollup}
	h.r = NewSubjectResolver(h.state, nil, nil)
	h.r.now = func() time.Time { return h.now }
	h.r.SetFunctionCodeLookup(func(_ context.Context, codes []string, from, to time.Time) (chstore.FunctionCodeFacts, error) {
		h.calls = append(h.calls, append([]string(nil), codes...))
		if !to.After(from) {
			t.Fatalf("pencere ters: %s → %s", from, to)
		}
		if h.err != nil {
			return chstore.FunctionCodeFacts{}, h.err
		}
		out := chstore.FunctionCodeFacts{Source: h.source, ByCode: map[string][]chstore.FunctionCodeService{}}
		for _, c := range codes {
			if d, ok := h.facts[c]; ok {
				out.ByCode[c] = d
			}
		}
		return out, nil
	})
	return h
}

func (h *fnHarness) observe(src SourceConfig, rows ...chstore.OracleErrorRow) ObserveResult {
	return h.r.Observe(context.Background(), src, rows, h.now.Add(-5*time.Minute), h.now)
}

func TestSubjectResolverFunctionCode(t *testing.T) {
	ctx := context.Background()
	h := newFnHarness(t)
	h.facts["F100"] = []chstore.FunctionCodeService{{Service: "eft-svc", Spans: 500, Errors: 20}, {Service: "gateway", Spans: 900, Errors: 1}}
	h.facts["F200"] = []chstore.FunctionCodeService{{Service: "a", Spans: 10, Errors: 5}, {Service: "b", Spans: 10, Errors: 5}}
	src := SourceConfig{ID: "o-f", Name: "oracle-prod", FunctionCodeMatch: true}
	rows := []chstore.OracleErrorRow{
		{OperationCode: "DIGITAL_PAYMENT_EFT", ErrorCode: "F100"}, {OperationCode: "DIGITAL_PAYMENT_EFT", ErrorCode: "F100"},
		{OperationCode: "MULTI", ErrorCode: "F200"}, {OperationCode: "UNSEEN", ErrorCode: "F300"}, {OperationCode: "NOCODE"},
	}
	res := h.observe(src, rows...)
	if res.Error != "" || res.Learned != 1 {
		t.Fatalf("observe: %+v", res)
	}
	// Tek sorgu, tekrarsız kodlar (boş kod sorulmaz).
	if len(h.calls) != 1 || strings.Join(h.calls[0], ",") != "F100,F200,F300" {
		t.Fatalf("arama: %v", h.calls)
	}
	// Harita henüz onaysız (1 teyit) → fonksiyon kodu basamağı servisi verir.
	got := h.r.Resolve(ctx, "o-f", []string{"DIGITAL_PAYMENT_EFT", "F100", "C", "-"})
	if got.Service != "eft-svc" || got.Source != SubjectSourceFunctionCode || got.Note != "fonksiyon kodundan F100→eft-svc (20/21 hata span'i)" {
		t.Fatalf("fonksiyon kodundan: %+v", got)
	}
	// Çoğunluk yok → servis yok, not adayı söyler.
	if got := h.r.Resolve(ctx, "o-f", []string{"MULTI", "F200", "C", "-"}); got.Service != "" || !strings.Contains(got.Note, "fonksiyon kodu F200 2 serviste, çoğunluk yok") {
		t.Fatalf("çoğunluksuz: %+v", got)
	}
	if got := h.r.Resolve(ctx, "o-f", []string{"UNSEEN", "F300", "C", "-"}); got.Service != "" || !strings.Contains(got.Note, "fonksiyon kodu F300 span'lerde görülmedi") {
		t.Fatalf("görülmeyen kod: %+v", got)
	}
	// Kodsuz seri: basamak sessiz, eski not aynen.
	if got := h.r.Resolve(ctx, "o-f", []string{"NOCODE", "", "C", "-"}); got.Service != "" || got.Note != "operasyon seviyesi — servis bilinmiyor" {
		t.Fatalf("kodsuz: %+v", got)
	}
	// Kanıt notu serinin KENDİ çözümünü taşır (aynı op, farklı kod karışmaz).
	rf := h.r.ResolveFor("o-f")
	rf(ctx, []string{"MULTI", "F200", "C", "-"})
	rf(ctx, []string{"MULTI", "F100", "C", "-"})
	if res, ok := h.r.LastResolutionFor("o-f", "MULTI", "F200"); !ok || res.Service != "" {
		t.Fatalf("seri çözümü: %+v", res)
	}
	if res, _ := h.r.LastResolutionFor("o-f", "MULTI", "F100"); res.Service != "eft-svc" {
		t.Fatalf("seri çözümü (F100): %+v", res)
	}
	// Küme Problem'i yalnız operasyonu verir → operasyonun bu tikteki kodları.
	if got := h.r.Resolve(ctx, "o-f", []string{"DIGITAL_PAYMENT_EFT"}); got.Service != "eft-svc" || got.Source != SubjectSourceFunctionCode {
		t.Fatalf("küme: %+v", got)
	}
	// Operasyon kodu boş ama fonksiyon kodu dolu satır da bağlanır.
	if got := h.r.Resolve(ctx, "o-f", []string{"", "F100", "C", "-"}); got.Service != "eft-svc" {
		t.Fatalf("op'suz: %+v", got)
	}

	// Önbellek: 10 dk dolmadan aynı kodlar yeniden SORULMAZ ve oy YAZILMAZ
	// (aynı okuma üç tikte üç teyit sayılmasın).
	h.now = h.now.Add(2 * time.Minute)
	if res := h.observe(src, rows...); res.Learned != 0 || len(h.calls) != 1 {
		t.Fatalf("önbellek: learned=%d calls=%d", res.Learned, len(h.calls))
	}
	// İki bağımsız okuma daha → 3 teyit → harita onaylı → "öğrenilmiş".
	for i := 0; i < 2; i++ {
		h.now = h.now.Add(11 * time.Minute)
		h.observe(src, rows...)
	}
	if len(h.calls) != 3 {
		t.Fatalf("tazeleme sayısı: %d", len(h.calls))
	}
	if got := h.r.Resolve(ctx, "o-f", []string{"DIGITAL_PAYMENT_EFT", "F100", "C", "-"}); got.Service != "eft-svc" || got.Source != "learned" {
		t.Fatalf("öğrenilmiş: %+v", got)
	}
	// Servis değişir (fonksiyon başka servise taşındı): tek okumada 1 oy —
	// eşik (≥3 oy) yok, harita dönmez; pay düşer ve onay kalkınca fonksiyon
	// kodu basamağı YENİ servisi verir.
	h.facts["F100"] = []chstore.FunctionCodeService{{Service: "eft-v2", Spans: 800, Errors: 30}}
	for i := 0; i < 2; i++ {
		h.now = h.now.Add(11 * time.Minute)
		h.observe(src, rows...)
	}
	if got := h.r.Resolve(ctx, "o-f", []string{"DIGITAL_PAYMENT_EFT", "F100", "C", "-"}); got.Service != "eft-v2" || got.Source != SubjectSourceFunctionCode {
		t.Fatalf("servis değişimi: %+v", got)
	}
	if e := h.r.Learned(ctx, "o-f").Entries["DIGITAL_PAYMENT_EFT"]; e == nil || e.Service != "eft-svc" || e.Alt != "eft-v2" || e.AltHits != 2 {
		t.Fatalf("meydan okuyan birikmeli: %+v", e)
	}
	// Üçüncü ardışık okuma: eski servis hiç oy almadı → harita YENİ servise döner.
	h.now = h.now.Add(11 * time.Minute)
	h.observe(src, rows...)
	if got := h.r.Resolve(ctx, "o-f", []string{"DIGITAL_PAYMENT_EFT", "F100", "C", "-"}); got.Service != "eft-v2" || got.Source != "learned" {
		t.Fatalf("harita güncellenmeli: %+v", got)
	}
}

// Meydan okuyan sayaç, mevcut servis yeniden oy alınca sıfırlanır (tek tük
// sapma girdiyi döndürmez).
func TestLearnedChallengerResets(t *testing.T) {
	ctx := context.Background()
	h := newFnHarness(t)
	src := SourceConfig{ID: "o-c", Name: "oracle-prod", FunctionCodeMatch: true}
	rows := []chstore.OracleErrorRow{{OperationCode: "OP", ErrorCode: "F1"}}
	read := func(svc string) {
		h.facts["F1"] = []chstore.FunctionCodeService{{Service: svc, Spans: 100, Errors: 10}}
		h.now = h.now.Add(11 * time.Minute)
		h.observe(src, rows...)
	}
	for _, svc := range []string{"a", "a", "a", "b", "b", "a", "b", "b"} {
		read(svc)
	}
	e := h.r.Learned(ctx, "o-c").Entries["OP"]
	if e == nil || e.Service != "a" || e.Alt != "b" || e.AltHits != 2 {
		t.Fatalf("araya giren 'a' oyu sayacı sıfırlamalı: %+v", e)
	}
	read("b")
	if e := h.r.Learned(ctx, "o-c").Entries["OP"]; e == nil || e.Service != "b" || e.Alt != "" {
		t.Fatalf("üç ardışık oyda dönmeli: %+v", e)
	}
}

// Ayar kapalıyken (varsayılan) okuyucu HİÇ çağrılmaz ve özne değişmez; açık
// kaynak sonradan kapatılırsa önbellek düşer.
func TestSubjectResolverFunctionCodeOffByDefault(t *testing.T) {
	ctx := context.Background()
	h := newFnHarness(t)
	h.facts["F100"] = []chstore.FunctionCodeService{{Service: "eft-svc", Spans: 500, Errors: 20}}
	rows := []chstore.OracleErrorRow{{OperationCode: "OP", ErrorCode: "F100"}}
	off := SourceConfig{ID: "o-x", Name: "oracle-prod"}
	if res := h.observe(off, rows...); res.Learned != 0 || len(h.calls) != 0 {
		t.Fatalf("kapalı kaynak: learned=%d calls=%v", res.Learned, h.calls)
	}
	if got := h.r.Resolve(ctx, "o-x", []string{"OP", "F100"}); got.Service != "" || got.Note != "operasyon seviyesi — servis bilinmiyor" {
		t.Fatalf("kapalıyken özne: %+v", got)
	}
	on := off
	on.FunctionCodeMatch = true
	h.observe(on, rows...)
	if got := h.r.Resolve(ctx, "o-x", []string{"OP", "F100"}); got.Service != "eft-svc" {
		t.Fatalf("açıkken: %+v", got)
	}
	h.now = h.now.Add(time.Minute)
	h.observe(off, rows...)
	if got := h.r.Resolve(ctx, "o-x", []string{"OP", "F100"}); got.Source == SubjectSourceFunctionCode {
		t.Fatalf("kapatılınca basamak durmalı: %+v", got)
	}
}

// Trace oyu her zaman kazanır: fonksiyon kodu ne çözülen özneyi değiştirir ne
// de trace oyu olan operasyonda haritaya oy yazar.
func TestSubjectResolverFunctionCodeNeverOverridesTrace(t *testing.T) {
	ctx := context.Background()
	h := newFnHarness(t)
	h.r.lookup = func(_ context.Context, ids []string, _, _ time.Time) (map[string]chstore.TraceFact, error) {
		out := map[string]chstore.TraceFact{}
		for _, id := range ids {
			out[id] = chstore.TraceFact{Service: "loan-svc"}
		}
		return out, nil
	}
	h.facts["F100"] = []chstore.FunctionCodeService{{Service: "eft-svc", Spans: 500, Errors: 20}}
	src := SourceConfig{ID: "o-t", Name: "oracle-prod", FunctionCodeMatch: true}
	h.observe(src, chstore.OracleErrorRow{OperationCode: "OP", ErrorCode: "F100", TraceID: "t1"})
	if got := h.r.Resolve(ctx, "o-t", []string{"OP", "F100"}); got.Service != "loan-svc" || got.Source != "trace" {
		t.Fatalf("trace kazanmalı: %+v", got)
	}
	if e := h.r.Learned(ctx, "o-t").Entries["OP"]; e == nil || e.Service != "loan-svc" || e.Total != 1 {
		t.Fatalf("harita trace oyuyla beslenmeli: %+v", e)
	}
}

// Okuma yolu yoksa (rollup da terfi kolonu da yok) sonuç önbelleğe yazılmaz
// ve not bunu söyler; arama hatası poll'u düşürmez.
func TestSubjectResolverFunctionCodeUnavailable(t *testing.T) {
	ctx := context.Background()
	h := newFnHarness(t)
	h.source = ""
	src := SourceConfig{ID: "o-u", Name: "oracle-prod", FunctionCodeMatch: true}
	rows := []chstore.OracleErrorRow{{OperationCode: "OP", ErrorCode: "F100"}}
	h.observe(src, rows...)
	if got := h.r.Resolve(ctx, "o-u", []string{"OP", "F100"}); got.Service != "" || !strings.Contains(got.Note, "fonksiyon kodu okunamıyor") {
		t.Fatalf("okuma yolu yok: %+v", got)
	}
	h.observe(src, rows...)
	if len(h.calls) != 2 {
		t.Fatalf("yol yokken her tik yeniden denenmeli: %d", len(h.calls))
	}
	h.source, h.err = chstore.FunctionCodeSourceRollup, context.DeadlineExceeded
	if res := h.observe(src, rows...); !strings.Contains(res.Error, "fonksiyon kodu araması") {
		t.Fatalf("hata özeti: %+v", res)
	}
}
