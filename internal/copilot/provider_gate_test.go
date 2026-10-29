package copilot

// v0.10.1564 — denetim L1: model sunucusu eşzamanlılık kapısı
// (provider_gate.go). Sınıflar, sınırlar, "etkileşimli uçuşta/beklerken arka
// plan başlamaz", iptal, akışta slotun gövde kapanana dek tutulması ve 503
// kısa geri çekilmesi.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	aiprovider "github.com/cilcenk/coremetry/internal/ai/provider"
)

func TestGateClassOf(t *testing.T) {
	cases := map[string]gateClass{
		"problem-auto-explain":   gateBackground,
		"exception-auto-explain": gateBackground,
		"sre-investigation":      gateBackground,
		"evalset-chat":           gateBackground,
		"chat":                   gateInteractive,
		"explain-trace":          gateInteractive,
		"cosre-turn:loop":        gateInteractive,
		"":                       gateInteractive, // bilinmeyen → operatör beklemesin
	}
	for s, want := range cases {
		if got := gateClassOf(s); got != want {
			t.Errorf("%q → %s, want %s", s, got, want)
		}
	}
}

// started — acquire'ı goroutine'de başlatır; slot alınınca kanala yazar.
func started(p *providerGate, c gateClass, ctx context.Context) (<-chan func(), <-chan error) {
	got := make(chan func(), 1)
	errc := make(chan error, 1)
	go func() {
		rel, _, err := p.acquire(ctx, "gpu:8000", c)
		if err != nil {
			errc <- err
			return
		}
		got <- rel
	}()
	return got, errc
}

func mustNotStart(t *testing.T, ch <-chan func(), what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s başlamamalıydı", what)
	case <-time.After(50 * time.Millisecond):
	}
}

func mustStart(t *testing.T, ch <-chan func(), what string) func() {
	t.Helper()
	select {
	case rel := <-ch:
		return rel
	case <-time.After(time.Second):
		t.Fatalf("%s başlamalıydı", what)
	}
	return nil
}

func TestProviderGateLimits(t *testing.T) {
	p := newProviderGate()
	ctx := context.Background()

	// Etkileşimli: 2 slot, üçüncüsü bekler.
	i1, _, _ := p.acquire(ctx, "gpu:8000", gateInteractive)
	i2, _, _ := p.acquire(ctx, "gpu:8000", gateInteractive)
	i3, _ := started(p, gateInteractive, ctx)
	mustNotStart(t, i3, "3. etkileşimli")

	// Etkileşimli uçuşta/beklerken arka plan başlamaz.
	b1, _ := started(p, gateBackground, ctx)
	mustNotStart(t, b1, "etkileşimli uçuştayken arka plan")

	// Başka host bağımsız (bulut profili yerel GPU'yu beklemez).
	if rel, _, err := p.acquire(ctx, "api.anthropic.com", gateBackground); err != nil {
		t.Fatal(err)
	} else {
		rel()
	}

	i1()
	r3 := mustStart(t, i3, "slot boşalınca bekleyen etkileşimli")
	mustNotStart(t, b1, "etkileşimli hâlâ uçuştayken arka plan")
	i2()
	r3()
	rb1 := mustStart(t, b1, "etkileşimli kalmayınca arka plan")

	// Arka plan: 1 slot.
	b2, _ := started(p, gateBackground, ctx)
	mustNotStart(t, b2, "2. arka plan")
	// Uçuştaki arka plan etkileşimliyi ENGELLEMEZ (kesilmez ama bekletmez).
	if rel, _, err := p.acquire(ctx, "gpu:8000", gateInteractive); err != nil {
		t.Fatal(err)
	} else {
		rel()
	}
	rb1()
	rb1() // release tek kez etkili
	mustStart(t, b2, "arka plan slotu boşalınca")()

	p.mu.Lock()
	g := p.hosts["gpu:8000"]
	if g.inter != 0 || g.back != 0 || g.interWait != 0 {
		t.Errorf("sayaçlar sıfırlanmalı: %+v", *g)
	}
	p.mu.Unlock()
}

// İptal edilen etkileşimli bekleyen, bekleme sayacını bırakır ve arka planı
// uyandırır — yoksa arka plan sonsuza dek beklerdi.
func TestProviderGateCancelledWaiterUnblocksBackground(t *testing.T) {
	p := newProviderGate()
	ctx := context.Background()
	i1, _, _ := p.acquire(ctx, "gpu:8000", gateInteractive)
	i2, _, _ := p.acquire(ctx, "gpu:8000", gateInteractive)
	cctx, cancel := context.WithCancel(ctx)
	_, werr := started(p, gateInteractive, cctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-werr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iptal hatası beklenirdi: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("iptal bekleyeni bırakmalı")
	}
	i1()
	i2()
	if rel, _, err := p.acquire(ctx, "gpu:8000", gateBackground); err != nil {
		t.Fatalf("bekleyen kalmayınca arka plan başlamalı: %v", err)
	} else {
		rel()
	}
}

// fakeRT — gövdesi kapanana dek "üretim süren" sağlayıcı.
type fakeRT struct {
	err  error
	body string
}

func (f fakeRT) RoundTrip(*http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestGatedRoundTripHoldsSlotUntilBodyClosed(t *testing.T) {
	prev := gate
	gate = newProviderGate()
	t.Cleanup(func() { gate = prev })

	req := func(surface string) *http.Request {
		r, _ := http.NewRequestWithContext(WithMeta(context.Background(), CallMeta{Surface: surface}),
			http.MethodPost, "http://gpu.local:8000/v1/chat/completions", nil)
		return r
	}
	resp, err := gatedRoundTrip(fakeRT{body: "data: x"}, req("chat"))
	if err != nil {
		t.Fatal(err)
	}
	inflight := func() int {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		return gate.hosts["gpu.local:8000"].inter
	}
	if inflight() != 1 {
		t.Fatalf("akış sürerken slot tutulmalı, inflight=%d", inflight())
	}
	_ = resp.Body.Close()
	if inflight() != 0 {
		t.Fatalf("gövde kapanınca slot bırakılmalı, inflight=%d", inflight())
	}
	// Taşıyıcı hatası: slot hemen bırakılır.
	if _, err := gatedRoundTrip(fakeRT{err: errors.New("dial")}, req("chat")); err == nil || inflight() != 0 {
		t.Fatalf("hata yolunda slot sızmamalı: err=%v inflight=%d", err, inflight())
	}
	// EOF'a kadar okunan gövde de bırakır (Close unutulsa bile).
	resp, _ = gatedRoundTrip(fakeRT{body: "ok"}, req("chat"))
	_, _ = io.ReadAll(resp.Body)
	if inflight() != 0 {
		t.Fatalf("EOF'ta slot bırakılmalı, inflight=%d", inflight())
	}
}

func TestOverloadBackoff(t *testing.T) {
	e503 := &aiprovider.HTTPError{Provider: "openai-compat", Status: 503, Body: "no slot available"}
	if !isOverloadErr(e503) || isOverloadErr(&aiprovider.HTTPError{Status: 500}) || isOverloadErr(errors.New("503")) {
		t.Fatal("isOverloadErr yalnız HTTP 503 sağlayıcı hatası")
	}
	if isQuotaErr(e503) {
		t.Fatal("503 kota sayılmamalı (1 sa kesici yanlış olurdu)")
	}
	s := &Service{}
	s.noteProviderError(e503)
	if !s.QuotaBackoffActive() {
		t.Fatal("503 sonrası arka plan geri çekilmeli")
	}
	if d := time.Until(s.overloadUntil); d <= 0 || d > providerOverloadPause {
		t.Fatalf("geri çekilme kısa olmalı (≤%s), got %s", providerOverloadPause, d)
	}
	if !s.quotaUntil.IsZero() {
		t.Fatal("503 kota kesicisini açmamalı")
	}
}
