package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// v0.10.2483 — yaygınlık satırı: anahtar türetimi, pencere, sınırlı okuma,
// hata/zaman aşımında satırın düşmesi, model satırı.

func prevSpans() []chstore.SpanRow {
	return []chstore.SpanRow{
		{SpanID: "r", ServiceName: "orders-bff", Name: "POST /orders", StartTime: 1, EndTime: 2},
		{SpanID: "c", ParentSpanID: "r", ServiceName: "customer-api", Name: "GET /customers/{id}", StartTime: 1, EndTime: 2},
	}
}

func TestTracePrevalenceKeyPerKind(t *testing.T) {
	sp := prevSpans()
	origin := tracechain.Hop{Span: sp[1]}
	k, ok := tracePrevalenceKeyOf(tracechain.Chain{OK: true, Hops: []tracechain.Hop{origin, {Span: sp[0]}}, Origin: origin}, sp)
	if !ok || k != (tracePrevalenceKey{Kind: tracePrevError, Service: "customer-api", Op: "GET /customers/{id}"}) {
		t.Fatalf("hata: zincirin kaynağı: %+v", k)
	}
	k, ok = tracePrevalenceKeyOf(tracechain.Chain{}, sp)
	if !ok || k != (tracePrevalenceKey{Kind: tracePrevOp, Service: "orders-bff", Op: "POST /orders"}) {
		t.Fatalf("zincir yok: kök operasyon: %+v", k)
	}
	if _, ok := tracePrevalenceKeyOf(tracechain.Chain{}, nil); ok {
		t.Fatal("span yok → anahtar yok")
	}
	if _, ok := tracePrevalenceKeyOf(tracechain.Chain{}, []chstore.SpanRow{{SpanID: "r", ServiceName: "orders-bff"}}); ok {
		t.Fatal("operasyon adı yok → anahtar yok")
	}
}

func TestTracePrevalenceWindow(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 27, 9, 30, 12, 0, time.UTC)
	from, to := tracePrevalenceWindowOf(end.UnixNano(), now)
	if !to.Equal(time.Date(2026, 11, 27, 9, 31, 0, 0, time.UTC)) || to.Sub(from) != time.Hour {
		t.Fatalf("trace saatine kadarki 1 sa: %v → %v", from, to)
	}
	if _, to := tracePrevalenceWindowOf(now.Add(time.Hour).UnixNano(), now); !to.Equal(now) {
		t.Fatalf("gelecek → şimdi: %v", to)
	}
	k := tracePrevalenceKey{Kind: tracePrevError, Service: "customer-api", Op: "GET /x"}
	a := tracePrevalenceKeyCache(k, from, to)
	for _, other := range []string{
		tracePrevalenceKeyCache(tracePrevalenceKey{Kind: tracePrevOp, Service: k.Service, Op: k.Op}, from, to),
		tracePrevalenceKeyCache(tracePrevalenceKey{Kind: k.Kind, Service: "orders-bff", Op: k.Op}, from, to),
		tracePrevalenceKeyCache(tracePrevalenceKey{Kind: k.Kind, Service: k.Service, Op: "GET /y"}, from, to),
		tracePrevalenceKeyCache(k, from.Add(time.Minute), to.Add(time.Minute)),
	} {
		if other == a {
			t.Fatal("önbellek anahtarı tüm girdileri özetlemeli")
		}
	}
}

func TestReadTracePrevalenceOmitsOnFailure(t *testing.T) {
	defer func(old func(context.Context, string, string, time.Time, time.Time) (chstore.OpPrevalence, error)) {
		tracePrevalenceRead = old
	}(tracePrevalenceRead)
	k := tracePrevalenceKey{Kind: tracePrevError, Service: "customer-api", Op: "GET /x"}
	from, to := time.Unix(0, 0), time.Unix(3600, 0)
	s := &Server{}

	tracePrevalenceRead = func(context.Context, string, string, time.Time, time.Time) (chstore.OpPrevalence, error) {
		return chstore.OpPrevalence{}, errors.New("boom")
	}
	if _, ok := s.readTracePrevalence(context.Background(), k, from, to); ok {
		t.Fatal("hata → satır yok")
	}
	tracePrevalenceRead = func(ctx context.Context, _, _ string, _, _ time.Time) (chstore.OpPrevalence, error) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		return chstore.OpPrevalence{Calls: 9, Errors: 9}, nil
	}
	start := time.Now()
	if _, ok := s.readTracePrevalence(context.Background(), k, from, to); ok {
		t.Fatal("bütçe aşıldı → satır yok")
	}
	if d := time.Since(start); d > tracePrevalenceTimeout+500*time.Millisecond {
		t.Fatalf("okuma beklemesi bütçeyle sınırlı olmalı: %v", d)
	}
	var gotSvc, gotOp string
	tracePrevalenceRead = func(_ context.Context, svc, op string, _, _ time.Time) (chstore.OpPrevalence, error) {
		gotSvc, gotOp = svc, op
		return chstore.OpPrevalence{Calls: 1840, Errors: 214}, nil
	}
	r, ok := s.readTracePrevalence(context.Background(), k, from, to)
	if !ok || r.Errors != 214 || gotSvc != "customer-api" || gotOp != "GET /x" {
		t.Fatalf("başarılı okuma: %+v %s %s", r, gotSvc, gotOp)
	}
}

func TestTracePrevalenceViewAndLine(t *testing.T) {
	from, to := time.Unix(0, 0), time.Unix(3600, 0)
	ek := tracePrevalenceKey{Kind: tracePrevError, Service: "customer-api", Op: "GET /x"}
	if tracePrevalenceView(ek, chstore.OpPrevalence{Calls: 50}, from, to, 80, true) != nil {
		t.Fatal("hata türünde 0 hata (MV gecikmesi) → yanlış sıfır gösterilmez")
	}
	if tracePrevalenceView(tracePrevalenceKey{Kind: tracePrevOp, Service: "a", Op: "b"}, chstore.OpPrevalence{}, from, to, 80, true) != nil {
		t.Fatal("0 çağrı → satır yok")
	}
	wide := tracePrevalenceView(ek, chstore.OpPrevalence{Calls: 1840, Errors: 214}, from, to, 80, true)
	if wide == nil || !wide.Wide || wide.WindowMin != 60 {
		t.Fatalf("yaygın: %+v", wide)
	}
	if l := tracePrevalenceLine(wide); !strings.Contains(l, "214 hatalı çağrı / 1840 çağrı → yaygın") || !strings.Contains(l, "trace saatine kadarki 1 saat") {
		t.Fatalf("satır: %q", l)
	}
	one := tracePrevalenceView(ek, chstore.OpPrevalence{Calls: 1840, Errors: 1}, from, to, 80, false)
	if one.Wide || !strings.Contains(tracePrevalenceLine(one), "tekil (yalnız bu istek)") || !strings.Contains(tracePrevalenceLine(one), "son 1 saat") {
		t.Fatalf("tekil: %q", tracePrevalenceLine(one))
	}
	ok := tracePrevalenceKey{Kind: tracePrevOp, Service: "orders-bff", Op: "POST /orders"}
	slow := tracePrevalenceView(ok, chstore.OpPrevalence{Calls: 1840, Over: 62, P95Ms: 640}, from, to, 2300, true)
	if !slow.Wide || slow.OverMs != chstore.OpFrustratedMs || !strings.Contains(tracePrevalenceLine(slow), "62 çağrı 800 ms'yi aştı → yaygın") {
		t.Fatalf("yavaş: %+v %q", slow, tracePrevalenceLine(slow))
	}
	fast := tracePrevalenceView(ok, chstore.OpPrevalence{Calls: 1840, Over: 62, P95Ms: 640}, from, to, 45, true)
	if fast.Wide || fast.OverMs != 0 || strings.Contains(tracePrevalenceLine(fast), "aştı") {
		t.Fatalf("hızlı trace eşik cümlesi almaz: %+v", fast)
	}
	if tracePrevalenceLine(nil) != "" {
		t.Fatal("nil → satır yok")
	}
}
