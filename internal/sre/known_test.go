package sre

// known_test.go — v0.10.2477: araştırma platformun bildiğiyle başlar
// (known.go). Sözleşmeler:
//   - Problem grafı kökeni: yalnız gösterilebilir köken gerçek olur (belirsiz /
//     tek varlık / üye değil → yok); zayıf köken işaretli ve skoru ETKİLEMEZ;
//     köken yokken kartlar bayt bayt eski (kontrol listelenmez).
//   - Etki olayları: "normalde X, şimdi Y" cümleleri sabit bulgu; tekrarda
//     as-of'tan sonra başlayan olay yok.
//   - Benzer vakalar: YALNIZ onaylı; Problem'in kendisi ve tekrarda kesimden
//     sonra kapanan / onaylanan vaka dışarıda; en çok 3; önbellek anahtarı tüm
//     girdileri özetler.
//   - Adımlar erişim beyanına (bildirimdeki kör noktalar) girmez.
//   - Anahtarlar: canlıda ayar (varsayılan açık), tekrarda noKnown.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func originBlob(at time.Time, conf string, entities int, originEntity string) chstore.ProblemGraphShadow {
	return chstore.ProblemGraphShadow{At: at.Unix(), Groups: []chstore.ProblemGraphGroup{{
		OwnerID: "p0", Entities: entities,
		Members: []chstore.ProblemGraphMember{
			{ProblemID: "p0", Entity: "svc:payments", Type: "service", Name: "payments"},
			{ProblemID: "p1", Entity: "svc:checkout", Type: "service", Name: "checkout"},
		},
		Origin: &chstore.ProblemGraphOrigin{Entity: originEntity, Name: strings.TrimPrefix(originEntity, "svc:"), Type: "service",
			Confidence: conf, Rule: chstore.OriginRuleShared},
	}}}
}

func TestGraphOriginOf(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name     string
		rep      chstore.ProblemGraphShadow
		problem  string
		want     bool
		weak     bool
		self     bool
		ruleText string
	}{
		{"güçlü başka varlık", originBlob(now, chstore.OriginStrong, 2, "svc:payments"), "p1", true, false, false, "diğerlerinin ortak bağımlılığı"},
		{"zayıf işaretli", originBlob(now, chstore.OriginWeak, 2, "svc:payments"), "p1", true, true, false, "zayıf"},
		{"öznenin kendisi", originBlob(now, chstore.OriginStrong, 2, "svc:checkout"), "p1", true, false, true, "kökenin kendisi"},
		{"belirsiz = yok", originBlob(now, chstore.OriginUnclear, 2, "svc:payments"), "p1", false, false, false, ""},
		{"tek varlık = yok", originBlob(now, chstore.OriginStrong, 1, "svc:payments"), "p1", false, false, false, ""},
		{"üye değil = yok", originBlob(now, chstore.OriginStrong, 2, "svc:payments"), "p9", false, false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := graphOriginOf(c.rep, c.problem)
			if (o != nil) != c.want {
				t.Fatalf("köken var=%v, beklenen %v", o != nil, c.want)
			}
			if o == nil {
				return
			}
			if o.Weak != c.weak || o.Self != c.self {
				t.Fatalf("weak=%v self=%v", o.Weak, o.Self)
			}
			if s := graphOriginSummaryTR(o); !strings.Contains(s, c.ruleText) {
				t.Fatalf("özet %q, %q içermeli", s, c.ruleText)
			}
		})
	}
}

func TestGraphOriginBlobUsable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rep := chstore.ProblemGraphShadow{At: now.Add(-5 * time.Minute).Unix()}
	if !graphOriginBlobUsable(rep, true, now, false) {
		t.Fatal("canlı: 5 dk'lık blob taze")
	}
	if graphOriginBlobUsable(chstore.ProblemGraphShadow{At: now.Add(-20 * time.Minute).Unix()}, true, now, false) {
		t.Fatal("canlı: 20 dk'lık blob bayat")
	}
	if graphOriginBlobUsable(rep, false, now, false) {
		t.Fatal("blob yok")
	}
	// Tekrar: as-of anından SONRA hesaplanmış blob (bugünün kümesi) sızmaz.
	asOf := now.Add(-10 * time.Minute)
	if graphOriginBlobUsable(rep, true, asOf, true) {
		t.Fatal("tekrar: as-of sonrası blob kullanılmamalı")
	}
	if !graphOriginBlobUsable(rep, true, now.Add(-2*time.Minute), true) {
		t.Fatal("tekrar: as-of öncesi taze blob kullanılabilir")
	}
}

func TestGraphOriginScoring(t *testing.T) {
	base := Evidence{Service: "checkout", Steps: map[string]string{stepGraphOrigin: StepOK}, Evidence: map[string][]evRef{}}
	check := func(hs []Hypothesis, kind, name string) (HypothesisCheck, bool) {
		for _, h := range hs {
			if h.Kind != kind {
				continue
			}
			for _, c := range h.Checks {
				if c.Name == name {
					return c, true
				}
			}
		}
		return HypothesisCheck{}, false
	}
	score := func(hs []Hypothesis, kind string) float64 {
		for _, h := range hs {
			if h.Kind == kind {
				return h.Score
			}
		}
		return -1
	}
	// Köken yok → kontrol kartta HİÇ yok, skorlar eskisi.
	none := Score(base)
	if _, ok := check(none, HypDependency, "graph_origin_elsewhere"); ok {
		t.Fatal("köken yokken kontrol listelenmemeli")
	}
	strong := base
	strong.Facts.GraphOrigin = &GraphOriginFact{Entity: "svc:payments", Type: "service", Name: "payments", Rule: chstore.OriginRuleShared, Entities: 3}
	hs := Score(strong)
	c, ok := check(hs, HypDependency, "graph_origin_elsewhere")
	if !ok || c.Outcome != 1 || c.EvidenceRef != stepGraphOrigin {
		t.Fatalf("güçlü başka köken bağımlılığı doğrulamalı: %+v", c)
	}
	if score(hs, HypDependency) <= score(none, HypDependency) {
		t.Fatal("güçlü köken bağımlılık skorunu artırmalı")
	}
	weak := base
	w := *strong.Facts.GraphOrigin
	w.Weak = true
	weak.Facts.GraphOrigin = &w
	wh := Score(weak)
	c, _ = check(wh, HypDependency, "graph_origin_elsewhere")
	if c.Outcome != 0 || !strings.Contains(c.Reason, "zayıf") {
		t.Fatalf("zayıf köken işaretli ve etkisiz olmalı: %+v", c)
	}
	if score(wh, HypDependency) != score(none, HypDependency) {
		t.Fatal("zayıf köken skoru değiştirmemeli")
	}
	self := base
	self.Facts.GraphOrigin = &GraphOriginFact{Entity: "svc:checkout", Type: "service", Name: "checkout", Rule: chstore.OriginRuleRollout, Entities: 2, Self: true}
	sh := Score(self)
	if c, _ := check(sh, HypDeploy, "graph_origin_rollout"); c.Outcome != 1 {
		t.Fatalf("öznenin kendisi + K-ROLLOUT deploy'u doğrulamalı: %+v", c)
	}
	if c, _ := check(sh, HypDependency, "graph_origin_elsewhere"); c.Outcome != -1 {
		t.Fatalf("köken öznenin kendisiyse bağımlılık çürür: %+v", c)
	}
	db := base
	db.Facts.GraphOrigin = &GraphOriginFact{Entity: "db:orders", Type: "database", Name: "orders", Rule: chstore.OriginRuleDeep, Entities: 2}
	if c, _ := check(Score(db), HypDBSlow, "graph_origin_db"); c.Outcome != 1 {
		t.Fatalf("veritabanı kökeni DB yavaşlığını doğrulamalı: %+v", c)
	}
}

func TestImpactFactsOf(t *testing.T) {
	p := chstore.Problem{ID: "p1", Service: "checkout", StartedAt: start.UnixNano()}
	evs := []chstore.MLEvent{
		{Detector: "svc_response_time", Service: "checkout", StartedAt: start.Add(time.Minute).UnixNano(), Observed: 920, Baseline: 180},
		{Detector: "svc_error_rate", Service: "checkout", StartedAt: start.UnixNano(), Observed: 0.068, Baseline: 0.004},
		{Detector: "svc_error_rate", Service: "checkout", StartedAt: start.Add(2 * time.Minute).UnixNano(), Observed: 0.068, Baseline: 0.004}, // aynı cümle
		{Detector: "endpoint_impact", Service: "checkout", Code: "POST /pay", StartedAt: start.Add(3 * time.Minute).UnixNano(), Observed: 0.14, Baseline: 0.002},
		{Detector: "log_pattern_new", Service: "checkout", StartedAt: start.UnixNano()}, // etki kuralı değil
	}
	f := impactFactsOf(p, evs, 0)
	if len(f.Rows) != 3 {
		t.Fatalf("3 etki cümlesi bekleniyordu: %+v", f.Rows)
	}
	if !strings.Contains(f.Rows[0].Sentence, "hata oranı normalde %0,4, şimdi %6,8") {
		t.Fatalf("ilk satır (en erken) hata oranı: %q", f.Rows[0].Sentence)
	}
	if !strings.Contains(f.Rows[2].Sentence, "POST /pay") || f.Rows[2].Route != "POST /pay" {
		t.Fatalf("uç nokta satırı: %+v", f.Rows[2])
	}
	st, fs := impactStep(f)
	if st.State != StepOK || len(fs) != 3 || fs[0].Kind != "impact" || fs[0].Step != stepImpact {
		t.Fatalf("sabit bulgular: %+v %+v", st, fs)
	}
	// Tekrar: as-of'tan sonra başlayan olay yok.
	if g := impactFactsOf(p, evs, start.Add(30*time.Second).UnixNano()); len(g.Rows) != 1 {
		t.Fatalf("as-of süzgeci: %+v", g.Rows)
	}
	if e := impactFactsOf(p, nil, 0); e.Rows == nil || len(e.Rows) != 0 {
		t.Fatal("olay yoksa boş (okundu) liste")
	}
}

func TestSimilarFactsOf(t *testing.T) {
	at := func(d int) time.Time { return start.Add(time.Duration(d) * 24 * time.Hour) }
	mk := func(id, src, status string, closed, confirmed time.Time) chstore.SimilarCase {
		return chstore.SimilarCase{IncidentCase: chstore.IncidentCase{CaseID: id, Source: "problem", SourceID: src, Title: "checkout hata oranı",
			Service: "checkout", ClosedAt: closed, Status: status, FinalCause: "pool tükendi", Resolution: "havuz büyütüldü", ConfirmedAt: confirmed}, Score: 4}
	}
	rows := []chstore.SimilarCase{
		mk("p:p1", "p1", chstore.CaseConfirmed, at(-1), at(-1)), // Problem'in kendisi
		mk("p:a", "a", "draft", at(-3), time.Time{}),            // onaysız
		mk("p:b", "b", chstore.CaseConfirmed, at(-5), at(-4)),   // ok
		mk("p:c", "c", chstore.CaseConfirmed, at(-6), at(2)),    // kesimden sonra onaylanmış
		mk("p:d", "d", chstore.CaseConfirmed, at(-7), at(-7)),   // ok
		mk("p:e", "e", chstore.CaseConfirmed, at(-8), at(-8)),   // ok
		mk("p:f", "f", chstore.CaseConfirmed, at(-9), at(-9)),   // tavan dışı
		mk("p:g", "g", chstore.CaseConfirmed, at(1), at(1)),     // kesimden sonra kapanmış
	}
	live := similarFactsOf(rows, "p1", "", 0, []string{"MCA-1"})
	if len(live.Cases) != 3 || live.Cases[0].CaseID != "p:b" {
		t.Fatalf("canlı: onaylı ilk 3 (kendisi hariç): %+v", live.Cases)
	}
	rep := similarFactsOf(rows, "p1", "", start.UnixNano(), nil)
	var ids []string
	for _, c := range rep.Cases {
		ids = append(ids, c.CaseID)
	}
	if strings.Join(ids, ",") != "p:b,p:d,p:e" {
		t.Fatalf("tekrar sızıntı süzgeci: %v", ids)
	}
	line := similarLineTR(rep.Cases[0])
	for _, want := range []string{"checkout hata oranı", "→ kök neden: pool tükendi", "→ çözüm: havuz büyütüldü", "(/problems?problem=b)"} {
		if !strings.Contains(line, want) {
			t.Fatalf("satır %q, %q içermeli", line, want)
		}
	}
}

func TestSimilarCacheKeyHashesAllInputs(t *testing.T) {
	from := start
	k := similarCacheKey("checkout", []string{"B", "A"}, from, 10)
	if k != similarCacheKey("checkout", []string{"A", "B"}, from, 10) {
		t.Fatal("kod sırası anahtarı değiştirmemeli")
	}
	for _, other := range []string{
		similarCacheKey("payments", []string{"A", "B"}, from, 10),
		similarCacheKey("checkout", []string{"A", "C"}, from, 10),
		similarCacheKey("checkout", []string{"A"}, from, 10),
		similarCacheKey("checkout", []string{"A", "B"}, from.Add(-48*time.Hour), 10),
		similarCacheKey("checkout", []string{"A", "B"}, from, 3),
	} {
		if other == k {
			t.Fatal("girdi farkı anahtarı değiştirmeli")
		}
	}
}

func TestKnownFlagsFor(t *testing.T) {
	off := false
	rd := readers{settings: func() Settings { return Settings{GraphOrigin: &off} }}
	f := knownFlagsFor(rd, &Investigation{Kind: KindAuto})
	if f.GraphOrigin || !f.Impact || !f.Similar {
		t.Fatalf("canlı: ayar (varsayılan açık): %+v", f)
	}
	if f := knownFlagsFor(rd, &Investigation{Kind: KindReplay}); !f.GraphOrigin || !f.Impact || !f.Similar {
		t.Fatalf("tekrar: canlı ayar etkilemez, varsayılan açık: %+v", f)
	}
	if f := knownFlagsFor(rd, &Investigation{Kind: KindReplay, NoKnown: true}); f.any() {
		t.Fatalf("tekrar noKnown: hepsi kapalı: %+v", f)
	}
	if f := knownFlagsFor(rd, &Investigation{Kind: KindAuto}).wired(readers{}); f.any() {
		t.Fatal("okuyucusu bağlı olmayan adım planlanmaz")
	}
}

func knownReaders(c *calls, n Narrator) readers {
	rd := okReaders(c, n)
	rd.problemGraph = func(context.Context) (chstore.ProblemGraphShadow, bool, error) {
		c.hit("pgraph")
		rep := originBlob(t0.Add(-time.Minute), chstore.OriginStrong, 2, "svc:payments")
		return rep, true, nil
	}
	rd.problemEvents = func(_ context.Context, p chstore.Problem) ([]chstore.MLEvent, error) {
		c.hit("pevents:" + p.ID)
		return []chstore.MLEvent{{Detector: "svc_error_rate", Service: "checkout", StartedAt: start.UnixNano(), Observed: 0.068, Baseline: 0.004}}, nil
	}
	rd.errorCodes = func(context.Context, string, time.Time, time.Time, int) ([]string, error) {
		return []string{"MCA-1"}, nil
	}
	rd.similarCases = func(_ context.Context, svc string, codes []string, _ time.Time, limit int) ([]chstore.SimilarCase, error) {
		c.hit("similar:" + svc + ":" + strings.Join(codes, ","))
		if limit > similarRead {
			c.hit("overlimit")
		}
		return []chstore.SimilarCase{{IncidentCase: chstore.IncidentCase{CaseID: "p:old", Source: "problem", SourceID: "old", Title: "checkout hata",
			Service: "checkout", Status: chstore.CaseConfirmed, FinalCause: "pool tükendi", ClosedAt: start.Add(-48 * time.Hour),
			ConfirmedAt: start.Add(-47 * time.Hour)}}}, nil
	}
	return rd
}

func TestKnownRunLive(tt *testing.T) {
	c := &calls{}
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: checkout."}
	inv := newInv()
	runInvestigation(context.Background(), knownReaders(c, n), inv, func(*Investigation) {}, func() time.Time { return t0 })
	for _, s := range []string{stepGraphOrigin, stepImpact, stepSimilar} {
		if st := inv.step(s); st == nil || st.State != StepOK {
			tt.Fatalf("%s adımı ok olmalı: %+v", s, st)
		}
	}
	f := inv.Facts
	if f.GraphOrigin == nil || f.GraphOrigin.Name != "payments" || f.Impact == nil || len(f.Impact.Rows) != 1 || f.Similar == nil || len(f.Similar.Cases) != 1 {
		tt.Fatalf("gerçekler: %+v %+v %+v", f.GraphOrigin, f.Impact, f.Similar)
	}
	if c.get("similar:checkout:MCA-1") != 1 || c.get("overlimit") != 0 {
		tt.Fatal("vaka hafızası servis + exception kodlarıyla bir kez okunmalı")
	}
	hasImpact := false
	for _, fd := range inv.Findings {
		hasImpact = hasImpact || fd.Kind == "impact"
	}
	if !hasImpact {
		tt.Fatalf("etki cümlesi sabit bulgu olmalı: %+v", inv.Findings)
	}
	for _, r := range inv.Reach {
		if knownSteps[r.Source] {
			tt.Fatalf("platform bilgisi erişim beyanına girmemeli: %+v", r)
		}
	}
	txt := RenderChatRecordTR(*withState(inv, StateDone), "P-1042", time.UTC, 0)
	for _, want := range []string{"```text\n", "köken payments", "etki: checkout servisinde hata oranı normalde", "onaylı benzer vaka", "kök neden: pool tükendi"} {
		if !strings.Contains(txt, want) {
			tt.Fatalf("sohbet bloğu %q içermeli:\n%s", want, txt)
		}
	}
}

func TestKnownReplayNoKnown(tt *testing.T) {
	c := &calls{}
	inv := newInv()
	inv.Kind, inv.EvalRun, inv.NoKnown = KindReplay, "r1", true
	inv.AsOf = start.Add(replayAsOfDelay).UnixNano()
	runInvestigation(context.Background(), knownReaders(c, &fakeNarrator{}), inv, func(*Investigation) {}, func() time.Time { return t0 })
	for _, s := range []string{stepGraphOrigin, stepImpact, stepSimilar} {
		if inv.step(s) != nil {
			tt.Fatalf("noKnown: %s adımı yok olmalı", s)
		}
	}
	if c.get("pgraph") != 0 || c.get("pevents:p1") != 0 {
		tt.Fatal("noKnown: okuma yapılmamalı")
	}
}

func withState(inv *Investigation, s string) *Investigation {
	x := *inv
	x.State = s
	return &x
}
