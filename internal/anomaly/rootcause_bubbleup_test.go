package anomaly

// v0.10.1577 — dış skill denetimi V1 dilim 2: sentezleyicinin derin
// kanıtına BubbleUp. Maliyet sözleşmesi bu dosyada pinli: bölüm (problem id +
// StartedAt) başına BİR tarama + taşıma, tik başına bütçe 2, 8 s tavan,
// açılışı izleyen 10 dk pencere, kıyas türü none olan problemde HİÇ okuma.
// Servis adları sentetik.

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rca"
)

type bubbleCall struct {
	service     string
	errorSubset bool
	started     time.Time
	end         time.Time
	deadline    time.Duration
}

type fakeBubbleReader struct {
	calls []bubbleCall
	res   *chstore.BubbleUpResult
	err   error
}

func (f *fakeBubbleReader) ServiceBubbleUp(ctx context.Context, service string, errorSubset bool, started, end time.Time) (*chstore.BubbleUpResult, error) {
	c := bubbleCall{service: service, errorSubset: errorSubset, started: started, end: end}
	if dl, ok := ctx.Deadline(); ok {
		c.deadline = time.Until(dl)
	}
	f.calls = append(f.calls, c)
	return f.res, f.err
}

func bubbleVal(value string, sel, selTotal, base, baseTotal int64) chstore.BubbleUpValue {
	sp, bp := float64(sel)/float64(selTotal), float64(base)/float64(baseTotal)
	return chstore.BubbleUpValue{Value: value, SelectionCount: sel, BaselineCount: base, SelectionPct: sp, BaselinePct: bp, Score: sp - bp}
}

func concentratedResult() *chstore.BubbleUpResult {
	return &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: []chstore.BubbleUpAttribute{
		{Key: "http.route", Values: []chstore.BubbleUpValue{bubbleVal("/v1/pay-now", 32, 40, 99, 900), bubbleVal("/v1/cart", 4, 40, 300, 900)}},
		{Key: "k8s.pod.name", Values: []chstore.BubbleUpValue{bubbleVal("orders-api-7f", 24, 40, 270, 900)}},
		{Key: "noise", Values: []chstore.BubbleUpValue{bubbleVal("x", 5, 40, 90, 900)}},
		{Key: "service.version", Values: []chstore.BubbleUpValue{bubbleVal("2.4.1", 20, 40, 90, 900)}},
		{Key: "fourth", Values: []chstore.BubbleUpValue{bubbleVal("y", 20, 40, 90, 900)}},
	}}
}

var bubbleT0 = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

func errProblem(id string, started time.Time) chstore.Problem {
	return chstore.Problem{ID: id, RuleID: id, Service: "orders-api", Metric: "error_rate", Severity: "critical", StartedAt: started.UnixNano()}
}

func TestBubbleUpDecision(t *testing.T) {
	p := errProblem("r1", bubbleT0)
	start := bubbleT0.UnixNano()
	win := rca.ExtrasWindow.Nanoseconds()
	ev := func(status string, attempts uint8, computed time.Time) *chstore.BubbleUpEvidence {
		return &chstore.BubbleUpEvidence{Mode: "errors", AnchorStartNs: start, FromNs: start, ToNs: start + win,
			ComputedNs: computed.UnixNano(), Attempts: attempts, Status: status}
	}
	ok, unr := chstore.BubbleUpStatusOK, chstore.BubbleUpStatusUnreadable
	after := bubbleT0.Add(30 * time.Minute) // pencere çoktan doldu
	cases := []struct {
		name   string
		p      chstore.Problem
		prior  *chstore.BubbleUpEvidence
		now    time.Time
		budget int
		want   bubbleAction
		mode   chstore.BubbleMode
	}{
		{"runtime: → hiç", chstore.Problem{ID: "runtime:jvm-gc:orders-api:orders-api-7f", Service: "orders-api", Metric: "jvm_gc_pause_ratio"}, nil, after, 2, bubbleSkip, chstore.BubbleModeNone},
		{"db-capacity: → hiç", chstore.Problem{ID: "db-capacity:db1", Service: "db.example.test", Kind: "database"}, nil, after, 2, bubbleSkip, chstore.BubbleModeNone},
		{"db-health: → hiç", chstore.Problem{ID: "db-health:x", Service: "orders-api", Metric: "error_count"}, nil, after, 2, bubbleSkip, chstore.BubbleModeNone},
		{"monitor: → hiç", chstore.Problem{ID: "monitor:abc", Service: "orders-api", Metric: "up"}, nil, after, 2, bubbleSkip, chstore.BubbleModeNone},
		{"self- → hiç", chstore.Problem{ID: "self-ingest", Service: "coremetry", Metric: "lag"}, nil, after, 2, bubbleSkip, chstore.BubbleModeNone},
		{"servissiz → hiç", chstore.Problem{ID: "exception-storm", Metric: "error_count"}, nil, after, 2, bubbleSkip, chstore.BubbleModeNone},
		{"none türünde önceki kanıt bile taşınmaz", chstore.Problem{ID: "monitor:abc", Service: "orders-api", StartedAt: start}, ev(ok, 1, after), after, 2, bubbleSkip, chstore.BubbleModeNone},
		{"önceki yok → tara", p, nil, after, 2, bubbleScan, chstore.BubbleModeErrors},
		{"gecikme → window türü", chstore.Problem{ID: "r2", Service: "orders-api", Metric: "p99_ms", StartedAt: start}, nil, after, 1, bubbleScan, chstore.BubbleModeWindow},
		{"önceki yok, bütçe 0 → atla (sonraki tik)", p, nil, after, 0, bubbleSkip, chstore.BubbleModeErrors},
		{"aynı bölüm ok (pencere dolu) → taşı", p, ev(ok, 1, after), after.Add(time.Hour), 2, bubbleReuse, chstore.BubbleModeErrors},
		{"aynı bölüm ok, bütçe 0 → yine taşı", p, ev(ok, 1, after), after, 0, bubbleReuse, chstore.BubbleModeErrors},
		{"başka bölüm (StartedAt farklı) → tara", p, &chstore.BubbleUpEvidence{Mode: "errors", AnchorStartNs: start - int64(time.Hour), Status: ok, Attempts: 1}, after, 2, bubbleScan, chstore.BubbleModeErrors},
		{"başka bölüm, bütçe 0 → atla (bayat kanıt taşınmaz)", p, &chstore.BubbleUpEvidence{Mode: "errors", AnchorStartNs: start - int64(time.Hour), Status: ok, Attempts: 1}, after, 0, bubbleSkip, chstore.BubbleModeErrors},
		{"okunamadı, 1 dk önce → taşı", p, ev(unr, 1, after), after.Add(time.Minute), 2, bubbleReuse, chstore.BubbleModeErrors},
		{"okunamadı, 6 dk önce → yeniden dene", p, ev(unr, 1, after), after.Add(6 * time.Minute), 2, bubbleScan, chstore.BubbleModeErrors},
		{"okunamadı, tam 5 dk → yeniden dene", p, ev(unr, 1, after), after.Add(bubbleUpRetryAfter), 2, bubbleScan, chstore.BubbleModeErrors},
		{"okunamadı, 6 dk ama bütçe 0 → taşı", p, ev(unr, 1, after), after.Add(6 * time.Minute), 0, bubbleReuse, chstore.BubbleModeErrors},
		{"okunamadı, 2 deneme → kalır", p, ev(unr, 2, after), after.Add(time.Hour), 2, bubbleReuse, chstore.BubbleModeErrors},
		{"eksik pencere, pencere hâlâ açık → taşı", p, ev(ok, 1, bubbleT0.Add(3*time.Minute)), bubbleT0.Add(9 * time.Minute), 2, bubbleReuse, chstore.BubbleModeErrors},
		{"eksik pencere, pencere doldu → bir tazeleme", p, ev(ok, 1, bubbleT0.Add(3*time.Minute)), bubbleT0.Add(10 * time.Minute), 2, bubbleScan, chstore.BubbleModeErrors},
		{"eksik pencere tazelemesi düşmüş (2 deneme) → kalır", p, ev(ok, 2, bubbleT0.Add(3*time.Minute)), bubbleT0.Add(time.Hour), 2, bubbleReuse, chstore.BubbleModeErrors},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, mode := bubbleUpDecision(c.p, c.prior, c.now, c.budget)
			if got != c.want || mode != c.mode {
				t.Errorf("bubbleUpDecision = (%s, %s), want (%s, %s)", got, mode, c.want, c.mode)
			}
		})
	}
}

// Bölüm başına BİR tarama: ikinci tik kalıcı kanıtı taşır (okuma yok, kanıt
// bayt-aynı); yeni bölüm (StartedAt değişti) yeniden tarar.
func TestAttachBubbleUpScansOncePerAnchor(t *testing.T) {
	fake := &fakeBubbleReader{res: concentratedResult()}
	s := &RootCauseSynthesizer{bubble: fake}
	p := errProblem("r1", bubbleT0)
	now := bubbleT0.Add(20 * time.Minute)

	var d1 chstore.DeepEvidence
	budget := synthesizerBubbleScansPerTick
	s.attachBubbleUp(context.Background(), p, &d1, nil, &budget, now)
	if len(fake.calls) != 1 || d1.BubbleUp == nil || d1.BubbleUp.Status != chstore.BubbleUpStatusOK || budget != synthesizerBubbleScansPerTick-1 {
		t.Fatalf("tik 1: tek tarama + ok kanıt beklenir: calls=%d ev=%+v budget=%d", len(fake.calls), d1.BubbleUp, budget)
	}
	// Tik 2..10 — önceki tikin kalıcı kanıtı taşınır.
	prior := d1.BubbleUp
	for tick := 2; tick <= 10; tick++ {
		var d chstore.DeepEvidence
		budget := synthesizerBubbleScansPerTick
		s.attachBubbleUp(context.Background(), p, &d, prior, &budget, now.Add(time.Duration(tick)*30*time.Second))
		if !reflect.DeepEqual(d.BubbleUp, d1.BubbleUp) {
			t.Fatalf("tik %d: kanıt bayt-aynı taşınmalı: %+v", tick, d.BubbleUp)
		}
		if d.BubbleUp == prior {
			t.Fatal("taşıma kopya olmalı (önceki satırın işaretçisi paylaşılmaz)")
		}
		prior = d.BubbleUp
	}
	if len(fake.calls) != 1 {
		t.Fatalf("aynı bölümde tek tarama: %d", len(fake.calls))
	}
	// Yeni bölüm (problem kapanıp aynı id ile yeniden açıldı).
	var d3 chstore.DeepEvidence
	budget = synthesizerBubbleScansPerTick
	p2 := errProblem("r1", bubbleT0.Add(2*time.Hour))
	s.attachBubbleUp(context.Background(), p2, &d3, prior, &budget, bubbleT0.Add(3*time.Hour))
	if len(fake.calls) != 2 || d3.BubbleUp == nil || d3.BubbleUp.AnchorStartNs != p2.StartedAt || d3.BubbleUp.Attempts != 1 {
		t.Fatalf("yeni bölüm yeniden taranmalı: calls=%d ev=%+v", len(fake.calls), d3.BubbleUp)
	}
}

func TestAttachBubbleUpNeverScansNonScanKinds(t *testing.T) {
	fake := &fakeBubbleReader{res: concentratedResult()}
	s := &RootCauseSynthesizer{bubble: fake}
	for _, p := range []chstore.Problem{
		{ID: "runtime:heap:orders-api", Service: "orders-api", Metric: "heap_used_pct"},
		{ID: "x", RuleID: "runtime:jvm-gc:orders-api", Service: "orders-api", Metric: "error_rate"},
		{ID: "db-capacity:db1", Service: "db.example.test", Kind: "database", Metric: "conn_pct"},
		{ID: "db-health:x", Service: "orders-api", Metric: "error_count"},
		{ID: "monitor:abc", Service: "orders-api", Metric: "up"},
		{ID: "self-ingest", Service: "coremetry", Metric: "lag"},
		{ID: "exception-storm", Metric: "error_count"},
		{ID: "ext", Service: "orders-api", Kind: chstore.ProblemKindExternal, Metric: "error_rate"},
	} {
		var d chstore.DeepEvidence
		budget := synthesizerBubbleScansPerTick
		s.attachBubbleUp(context.Background(), p, &d, nil, &budget, bubbleT0)
		if d.BubbleUp != nil || budget != synthesizerBubbleScansPerTick {
			t.Errorf("%s: tarama/kanıt olmamalı: %+v budget=%d", p.ID, d.BubbleUp, budget)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("none türlerinde okuma: %d", len(fake.calls))
	}
	if ids := bubbleCandidateIDs([]chstore.Problem{
		errProblem("r1", bubbleT0), {ID: "runtime:heap:orders-api", Service: "orders-api"},
		{ID: "ext", Service: "orders-api", Kind: chstore.ProblemKindExternal, Metric: "error_rate"},
	}); !reflect.DeepEqual(ids, []string{"r1"}) {
		t.Errorf("önceki-hipotez okuması yalnız tarama adaylarını sormalı: %v", ids)
	}
}

// Tik başına bütçe: 3 yeni çıpa, bütçe 2 → 2 tarama; üçüncü sonraki tikte.
func TestAttachBubbleUpBudget(t *testing.T) {
	fake := &fakeBubbleReader{res: concentratedResult()}
	s := &RootCauseSynthesizer{bubble: fake}
	now := bubbleT0.Add(20 * time.Minute)
	ps := []chstore.Problem{errProblem("a", bubbleT0), errProblem("b", bubbleT0), errProblem("c", bubbleT0)}
	persisted := map[string]*chstore.BubbleUpEvidence{}
	tick := func(at time.Time) {
		budget := synthesizerBubbleScansPerTick
		for _, p := range ps {
			var d chstore.DeepEvidence
			s.attachBubbleUp(context.Background(), p, &d, persisted[p.ID], &budget, at)
			persisted[p.ID] = d.BubbleUp
		}
	}
	tick(now)
	if len(fake.calls) != 2 || persisted["c"] != nil {
		t.Fatalf("tik 1: bütçe 2 → 2 tarama, üçüncü kanıtsız: calls=%d c=%+v", len(fake.calls), persisted["c"])
	}
	tick(now.Add(30 * time.Second))
	if len(fake.calls) != 3 || persisted["c"] == nil || persisted["a"] == nil || persisted["b"] == nil {
		t.Fatalf("tik 2: yalnız üçüncü taranır: calls=%d", len(fake.calls))
	}
	tick(now.Add(time.Minute))
	if len(fake.calls) != 3 {
		t.Fatalf("kararlı durumda tarama 0: calls=%d", len(fake.calls))
	}
}

// Pencere = açılışı izleyen rca.ExtrasWindow (10 dk), 8 s tavan, servis
// süzgeci, hata ailesi → hata alt kümesi.
func TestAttachBubbleUpWindowAndCap(t *testing.T) {
	if rca.ExtrasWindow != 10*time.Minute || rca.BubbleUpTimeout != 8*time.Second {
		t.Fatalf("maliyet tavanı: pencere 10 dk + 8 s olmalı: %s / %s", rca.ExtrasWindow, rca.BubbleUpTimeout)
	}
	if synthesizerBubbleScansPerTick != 2 || bubbleUpMaxAttempts != 2 || bubbleUpRetryAfter != 5*time.Minute {
		t.Fatal("bütçe sabitleri: 2 tarama/tik, bölüm başına 2 deneme, 5 dk yeniden deneme")
	}
	fake := &fakeBubbleReader{res: concentratedResult()}
	s := &RootCauseSynthesizer{bubble: fake}
	for _, c := range []struct {
		p       chstore.Problem
		errorSS bool
	}{
		{errProblem("r1", bubbleT0), true},
		{chstore.Problem{ID: "r2", Service: "orders-api", Metric: "p99_ms", StartedAt: bubbleT0.UnixNano()}, false},
	} {
		var d chstore.DeepEvidence
		budget := 1
		// Problem 2 sa önce açıldı: pencere yine AÇILIŞIN 10 dakikası, "son 10 dk" değil.
		s.attachBubbleUp(context.Background(), c.p, &d, nil, &budget, bubbleT0.Add(2*time.Hour))
		call := fake.calls[len(fake.calls)-1]
		if call.service != "orders-api" || call.errorSubset != c.errorSS || !call.started.Equal(bubbleT0) || !call.end.Equal(bubbleT0.Add(rca.ExtrasWindow)) {
			t.Errorf("%s: çağrı %+v", c.p.ID, call)
		}
		if call.deadline <= 0 || call.deadline > rca.BubbleUpTimeout {
			t.Errorf("%s: ctx tavanı 8 s olmalı: %s", c.p.ID, call.deadline)
		}
		if d.BubbleUp.FromNs != bubbleT0.UnixNano() || d.BubbleUp.ToNs != bubbleT0.Add(10*time.Minute).UnixNano() {
			t.Errorf("%s: kanıt penceresi: %+v", c.p.ID, d.BubbleUp)
		}
	}
}

// Okuma düşerse: okunamadı + sınıf + deneme; ≥5 dk sonra BİR yeniden deneme;
// sonra kalır. Eksik pencere tazelemesi düşerse eski sonuç korunur.
func TestAttachBubbleUpFailureAndRefresh(t *testing.T) {
	fake := &fakeBubbleReader{err: context.DeadlineExceeded}
	s := &RootCauseSynthesizer{bubble: fake}
	p := errProblem("r1", bubbleT0)
	at := bubbleT0.Add(20 * time.Minute)
	run := func(prior *chstore.BubbleUpEvidence, now time.Time) *chstore.BubbleUpEvidence {
		var d chstore.DeepEvidence
		budget := synthesizerBubbleScansPerTick
		s.attachBubbleUp(context.Background(), p, &d, prior, &budget, now)
		return d.BubbleUp
	}
	e1 := run(nil, at)
	if e1 == nil || e1.Status != chstore.BubbleUpStatusUnreadable || e1.Error != "timeout" || e1.Attempts != 1 {
		t.Fatalf("okunamadı kanıtı: %+v", e1)
	}
	e2 := run(e1, at.Add(time.Minute))
	if len(fake.calls) != 1 || !reflect.DeepEqual(e1, e2) {
		t.Fatalf("5 dk dolmadan yeniden deneme yok: calls=%d", len(fake.calls))
	}
	fake.err = errors.New("dial tcp 10.0.0.9:9000: connect: connection refused")
	e3 := run(e2, at.Add(6*time.Minute))
	if len(fake.calls) != 2 || e3.Attempts != 2 || e3.Error != "unreachable" {
		t.Fatalf("bir yeniden deneme: calls=%d ev=%+v", len(fake.calls), e3)
	}
	run(e3, at.Add(time.Hour))
	if len(fake.calls) != 2 {
		t.Fatalf("2 denemeden sonra tarama yok: %d", len(fake.calls))
	}

	// Eksik pencere: açılıştan 3 dk sonra ok sonuç; pencere dolunca tazeleme
	// düşerse eski sonuç kalır (okunamadı'ya dönmez), bir daha tazelenmez.
	fake.calls, fake.err, fake.res = nil, nil, concentratedResult()
	early := run(nil, bubbleT0.Add(3*time.Minute))
	fake.err = context.DeadlineExceeded
	kept := run(early, bubbleT0.Add(11*time.Minute))
	if len(fake.calls) != 2 || kept.Status != chstore.BubbleUpStatusOK || kept.Attempts != 2 || kept.SelectionTotal != early.SelectionTotal {
		t.Fatalf("düşen tazeleme eski sonucu korumalı: calls=%d ev=%+v", len(fake.calls), kept)
	}
	run(kept, bubbleT0.Add(time.Hour))
	if len(fake.calls) != 2 {
		t.Fatalf("tazeleme bir kez: %d", len(fake.calls))
	}

	// Kapanış (ctx iptal): deneme sayılmaz, kanıt yazılmaz.
	fake.calls = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var d chstore.DeepEvidence
	budget := 1
	s.attachBubbleUp(ctx, p, &d, nil, &budget, at)
	if d.BubbleUp != nil {
		t.Fatalf("iptalde okunamadı yazılmamalı: %+v", d.BubbleUp)
	}
}

func TestTrimBubbleUp(t *testing.T) {
	got := trimBubbleUp(concentratedResult().Attributes, rca.BubbleUpRenderCap)
	var keys []string
	for _, a := range got {
		keys = append(keys, a.Key)
		if len(a.Values) != 1 {
			t.Errorf("%s: yalnız en üst değer saklanır: %d", a.Key, len(a.Values))
		}
	}
	if !reflect.DeepEqual(keys, []string{"http.route", "k8s.pod.name", "service.version"}) {
		t.Errorf("eşik altı (noise) elenir, tavan 3: %v", keys)
	}
	if got[0].Values[0].SelectionPct != 0.8 {
		t.Errorf("oran chstore biriminde kalır (0–1): %v", got[0].Values[0].SelectionPct)
	}
	if trimBubbleUp(nil, 3) != nil {
		t.Error("boş girdi → nil")
	}
}

// Kaynak pini: sentezleyici BubbleUp'ı derin blokta bağlar ve tik başına TEK
// önceki-hipotez okuması yapar; derin kanıtın başka yazıcısı yok.
func TestSynthesizerWiresBubbleUpOncePerTick(t *testing.T) {
	b, err := os.ReadFile("rootcause_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *RootCauseSynthesizer) run(ctx context.Context) {")
	if i < 0 {
		t.Fatal("run yok")
	}
	run := src[i : i+strings.Index(src[i:], "\n}\n")]
	if n := strings.Count(run, `s.store.GetHypotheses(ctx, "problem", ids)`); n != 1 {
		t.Errorf("tik başına tek GetHypotheses(problem): %d", n)
	}
	if strings.Index(run, "s.store.GetHypotheses(") > strings.Index(run, "for _, p := range problems {") {
		t.Error("önceki-hipotez okuması problem döngüsünden ÖNCE olmalı (döngü içinde = çıpa başına okuma)")
	}
	deep := strings.Index(run, "if deep {")
	attach := strings.Index(run, "s.attachBubbleUp(ctx, p, &d, priorBubbleUps(priorHyps, p.ID), &bubbleBudget, now)")
	if deep < 0 || attach < deep || attach > strings.Index(run, "if len(rolloutEv) > 0 {") {
		t.Errorf("attachBubbleUp derin bloğun içinde olmalı: deep=%d attach=%d", deep, attach)
	}
	// Plansız türde yalnız BubbleUp varken de Deep yazılır (yoksa kanıt kaybolur
	// ve her tik yeniden taranır).
	if !strings.Contains(run, "if len(plan) > 0 || d.BubbleUp != nil {\n\t\t\t\t\th.Deep = &d") {
		t.Error("h.Deep, plan boşken BubbleUp varsa da yazılmalı")
	}
	if !strings.Contains(run, "bubbleBudget := synthesizerBubbleScansPerTick") {
		t.Error("bütçe tik başına kurulmalı")
	}
	if strings.Count(src, "ServiceBubbleUp(") != 0 {
		t.Error("rootcause_worker.go taramayı doğrudan çağırmamalı — attachBubbleUp")
	}
}
