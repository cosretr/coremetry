package sre

// v0.10.1288 — AI SRE Faz 2: hipotez kartları + zaman çizelgesi + doğrula/ele.
// Skor tablosu (tür başına doğrula / çürüt / bilinmiyor), determinizm,
// operatör işaretinin skoru değiştirmemesi, ajanın hipotez tablosuna /
// problemlere / önceliğe yazmaması, zaman çizelgesi sırası + tavanı, tembel
// node kararı ve doğrulama koşusunun okumaları.

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func fp(v float64) *float64 { return &v }

func ns(d time.Duration) int64 { return start.Add(d).UnixNano() }

// baseEv — boş gerçekler, hata belirtisi.
func baseEv(symptom string, steps ...string) Evidence {
	ev := Evidence{Service: "checkout", Symptom: symptom, StartNs: start.UnixNano(), Steps: map[string]string{}, Evidence: map[string][]evRef{}}
	for _, s := range steps {
		st := StepOK
		if i := strings.IndexByte(s, '='); i > 0 {
			s, st = s[:i], s[i+1:]
		}
		ev.Steps[s] = st
		ev.Evidence[s] = []evRef{{Hop: "", Tool: "x"}, {Hop: "", Tool: "y"}}
	}
	return ev
}

func hypOf(t *testing.T, hs []Hypothesis, kind string) Hypothesis {
	t.Helper()
	for _, h := range hs {
		if h.Kind == kind {
			return h
		}
	}
	t.Fatalf("%s hipotezi yok", kind)
	return Hypothesis{}
}

func outcomes(h Hypothesis) string {
	var b []string
	for _, c := range h.Checks {
		b = append(b, strconv.Itoa(c.Outcome))
	}
	return strings.Join(b, ",")
}

// TestHypothesisTable — tür başına üç satır: doğrulayan kanıt (yüksek),
// çürüten kanıt (düşük, sıfıra kırpılır), okunamayan / olmayan kanıt (öncül).
func TestHypothesisTable(t *testing.T) {
	deployRow := func(d time.Duration, src, ver, prev, status string) DeployFact {
		return DeployFact{At: ns(d), Source: src, Name: "checkout", Version: ver, Prev: prev, Status: status}
	}
	cases := []struct {
		kind, name string
		ev         Evidence
		score      float64
		band       string
		outcomes   string
	}{
		// deploy
		{HypDeploy, "doğrular: 12 dk önce stalled rollout, hata sonrasında", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-12*time.Minute, "rollout", "rev42", "rev41", "stalled")}}
			return ev
		}(), 0.90, BandHigh, "1,1,0,1"},
		{HypDeploy, "çürütür: ±2 sa deploy yok", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{}}
			return ev
		}(), 0, BandLow, "-1,0,0,0"},
		{HypDeploy, "çürütür: anomali deploy'dan önce de düzenli (yinelenen)", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-12*time.Minute, "", "1.4.2", "1.4.1", "")}}
			ev.Facts.Problem = &ProblemFacts{EpisodeCount: 5, FirstStartedAt: ns(-4 * 24 * time.Hour)}
			return ev
		}(), 0.50, BandMedium, "1,1,-1,0"},
		{HypDeploy, "bilinmiyor: deploy okunamadı", baseEv(symptomError, stepDeploys+"=error"), 0.20, BandLow, "0,0,0,0"},
		// config
		{HypConfig, "doğrular: imaj değişmeden rollout, hatadan önce", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-8*time.Minute, "rollout", "", "", "complete")}}
			return ev
		}(), 0.70, BandHigh, "1,1,0"},
		{HypConfig, "çürütür: ±2 sa değişiklik yok", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{}}
			return ev
		}(), 0, BandLow, "-1,0,0"},
		{HypConfig, "bilinmiyor: geçmiş okunamadı", baseEv(symptomError), 0.10, BandLow, "0,0,0"},
		// v0.10.1310 — argo_sync (Facts.Argo; koşu başına tek Deps.ArgoApps okuması)
		{HypConfig, "doğrular: imajsız rollout + Argo OutOfSync (0.70 tavanı kalktı)", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-8*time.Minute, "rollout", "", "", "complete")}}
			ev.Facts.Argo = argoAt(t0, argoApp("checkout-prod", "OutOfSync", "Healthy", 1, "checkout"))
			return ev
		}(), 0.90, BandHigh, "1,1,1"},
		{HypConfig, "doğrular: Argo Synced ama 24 sa'te senkron + pencerede imajsız rollout", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-8*time.Minute, "rollout", "", "", "complete")}}
			ev.Facts.Argo = argoAt(t0, argoApp("checkout-prod", "Synced", "Healthy", 2, "checkout"), argoApp("web-prod", "Synced", "Healthy", 0, "web"))
			return ev
		}(), 0.90, BandHigh, "1,1,1"},
		{HypConfig, "çürütür: Argo Synced/Healthy, pencerede senkron yok; yakın değişiklik imajlı", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-10*time.Minute, "rollout", "rev42", "rev41", "complete")}}
			ev.Facts.Argo = argoAt(t0, argoApp("checkout-prod", "Synced", "Healthy", 0, "checkout"))
			return ev
		}(), 0, BandLow, "0,0,-1"},
		{HypConfig, "bilinmiyor: servise eşlenen Argo uygulaması yok", func() Evidence {
			ev := baseEv(symptomError)
			ev.Facts.Argo = argoAt(t0)
			return ev
		}(), 0.10, BandLow, "0,0,0"},
		{HypConfig, "bilinmiyor: Argo okunamadı", func() Evidence {
			ev := baseEv(symptomError)
			ev.Facts.Argo = &ArgoFacts{State: argoStateError, ReadAt: t0.UnixNano()}
			return ev
		}(), 0.10, BandLow, "0,0,0"},
		{HypConfig, "bilinmiyor: tekrar — canlı Argo durumu geçmiş vakayı puanlamaz", func() Evidence {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-8*time.Minute, "rollout", "", "", "complete")}}
			a := argoAt(t0, argoApp("checkout-prod", "OutOfSync", "Degraded", 3, "checkout"))
			a.State, a.Note = argoStateReplay, ReplayArgoNote // satır olsa bile replay puanlanmaz
			ev.Facts.Argo = a
			return ev
		}(), 0.70, BandHigh, "1,1,0"},
		// saturation
		{HypSaturation, "doğrular: OOM + post-GC + node", func() Evidence {
			ev := baseEv(symptomLatency, stepSweep, stepNode)
			ev.Facts.Pods = &PodFacts{PodsState: "ok", Pods: 3, MaxCPUPct: fp(95), OOMPods: []string{"checkout-1"}, JVMPods: 2, MaxPostGCPct: fp(91), HotJVM: "checkout-2"}
			ev.Facts.Node = &NodeFacts{State: "ok", Nodes: []mcptools.NodePressure{{Node: "n1", CPUPct: fp(93)}}}
			return ev
		}(), 1, BandHigh, "1,1,1,1"},
		{HypSaturation, "çürütür: pod / JVM / node rahat", func() Evidence {
			ev := baseEv(symptomError, stepSweep, stepNode)
			ev.Facts.Pods = &PodFacts{PodsState: "ok", Pods: 3, MaxCPUPct: fp(30), MaxMemPct: fp(40), JVMPods: 2, MaxPostGCPct: fp(40)}
			ev.Facts.Node = &NodeFacts{State: "ok", Nodes: []mcptools.NodePressure{{Node: "n1", CPUPct: fp(50), TopPods: []mcptools.NodeTopPod{{Pod: "x", Pct: 20}}}}}
			return ev
		}(), 0, BandLow, "-1,-1,-1,0"},
		{HypSaturation, "bilinmiyor: Thanos yok, node koşmadı", func() Evidence {
			ev := baseEv(symptomError, stepSweep)
			ev.Facts.Pods = &PodFacts{PodsState: "skipped"}
			return ev
		}(), 0.15, BandLow, "0,0,0,0"},
		// dependency
		{HypDependency, "doğrular: aşağı akış kötüleşti, sentezleyici aynı komşu", func() Evidence {
			ev := baseEv(symptomError, stepDeps)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "payments", Calls: 100, ErrorRate: 12}},
				Causes: []depsCause{{Service: "payments", Relation: chstore.RelationDownstream, Direction: chstore.ChangeWorse}}}
			ev.Synth = &SynthRef{TopSuspect: "payments", Confidence: 0.6}
			return ev
		}(), 0.90, BandHigh, "1,1,1"},
		{HypDependency, "çürütür: komşular temiz, sentezleyici servisin kendisi", func() Evidence {
			ev := baseEv(symptomError, stepDeps)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "payments", Calls: 100, ErrorRate: 0.2}}}
			ev.Synth = &SynthRef{TopSuspect: "checkout"}
			return ev
		}(), 0, BandLow, "-1,-1,-1"},
		{HypDependency, "bilinmiyor: bağımlılık okunamadı", baseEv(symptomError), 0.15, BandLow, "0,0,0"},
		// external
		{HypExternal, "doğrular: ext-down açık + dış kenar hatası", func() Evidence {
			ev := baseEv(symptomError, stepDeps, stepEvents)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "ext:api.stripe.com", Calls: 50, ErrorRate: 35}}}
			ev.Facts.Events = &EventFacts{AlertsOK: true, RelatedOK: true, Related: []AlertFact{{ID: "x", RuleID: "anomaly:ext-down:stripe", Service: "api.stripe.com"}}}
			return ev
		}(), 0.85, BandHigh, "1,1"},
		{HypExternal, "çürütür: dış çağrılar temiz, problem yok", func() Evidence {
			ev := baseEv(symptomError, stepDeps, stepEvents)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "ext:api.stripe.com", Calls: 50}}}
			ev.Facts.Events = &EventFacts{AlertsOK: true, RelatedOK: true}
			return ev
		}(), 0, BandLow, "-1,-1"},
		{HypExternal, "bilinmiyor: dış kenar yok", baseEv(symptomError), 0.10, BandLow, "0,0"},
		// db_slow
		{HypDBSlow, "doğrular: db-health açık + kritik yol DB + yavaş kenar", func() Evidence {
			ev := baseEv(symptomLatency, stepDeps, stepEvents, stepPivot)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "db:postgresql@orders", Calls: 200, AvgMs: 800}}}
			ev.Facts.Events = &EventFacts{RelatedOK: true, Related: []AlertFact{{RuleID: "db-health:postgresql@orders/orders", Service: "db:postgresql@orders"}}}
			ev.Facts.Pivot = &PivotFacts{TraceID: "t", WallMs: 2000, Path: []PathFact{{Service: "checkout", Name: "SELECT orders", SelfMs: 1500}}}
			return ev
		}(), 0.95, BandHigh, "1,1,1"},
		{HypDBSlow, "çürütür: DB hızlı, problem yok, kritik yol HTTP", func() Evidence {
			ev := baseEv(symptomLatency, stepDeps, stepEvents, stepPivot)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "db:postgresql@orders", Calls: 200, AvgMs: 4}}}
			ev.Facts.Events = &EventFacts{RelatedOK: true}
			ev.Facts.Pivot = &PivotFacts{TraceID: "t", WallMs: 2000, Path: []PathFact{{Service: "payments", Name: "POST /charge", SelfMs: 1700}}}
			return ev
		}(), 0, BandLow, "-1,-1,-1"},
		{HypDBSlow, "bilinmiyor: DB kenarı yok, trace yok", baseEv(symptomLatency), 0.10, BandLow, "0,0,0"},
		// traffic_surge
		{HypTrafficSurge, "doğrular: hız +120%, p99 +60%, çağıran sıçradı", func() Evidence {
			ev := baseEv(symptomLatency, stepDeps)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Subject: &SubjectChange{RateDeltaPct: 120, P99DeltaPct: 60, CurP99Ms: 800, BaseP99Ms: 500},
				Causes: []depsCause{{Service: "web", Relation: chstore.RelationUpstream, Direction: chstore.ChangeWorse}}}
			ev.Facts.Pods = &PodFacts{PodsState: "ok", LatencyCandidates: 1, LatencyCorroborated: 1} // v0.10.1299 — ham span doğruladı
			return ev
		}(), 0.90, BandHigh, "1,1,1"},
		{HypTrafficSurge, "çürütür: hız düz, p99 düşük", func() Evidence {
			ev := baseEv(symptomLatency, stepDeps)
			ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Subject: &SubjectChange{RateDeltaPct: -5, P99DeltaPct: -3}}
			return ev
		}(), 0, BandLow, "-1,-1,0"},
		{HypTrafficSurge, "bilinmiyor: değişim okunamadı", baseEv(symptomLatency), 0.10, BandLow, "0,0,0"},
		// code_regression
		{HypCodeRegression, "doğrular: deploy sonrası yeni desen + hatalı trace", func() Evidence {
			ev := baseEv(symptomError, stepDeploys, stepLogSpike, stepPivot)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{deployRow(-12*time.Minute, "", "1.4.2", "1.4.1", "")}}
			ev.Facts.Spike = &SpikeFacts{Level: "error", FromMs: start.Add(-2 * time.Minute).UnixMilli(), Factor: 6,
				Contributors: []SpikeContrib{{Dim: "pattern", Value: "NullPointerException at Foo.bar", SharePct: 62, New: true}}}
			ev.Facts.Pivot = &PivotFacts{TraceID: "t", ErrorSpans: 3, WallMs: 900}
			return ev
		}(), 1, BandHigh, "1,1,1,1"},
		{HypCodeRegression, "çürütür: desen önceden vardı, deploy yok", func() Evidence {
			ev := baseEv(symptomLatency, stepDeploys, stepLogSpike, stepPivot)
			ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{}}
			ev.Facts.Spike = &SpikeFacts{Level: "all", FromMs: start.UnixMilli(), Factor: 4,
				Contributors: []SpikeContrib{{Dim: "pattern", Value: "Connection reset", SharePct: 40, BaseCount: 500}}}
			ev.Facts.Pivot = &PivotFacts{TraceID: "t", WallMs: 900}
			return ev
		}(), 0, BandLow, "-1,-1,0,0"},
		{HypCodeRegression, "bilinmiyor: log / deploy / trace yok", baseEv(symptomLatency), 0.15, BandLow, "0,0,0,0"},
	}
	seen := map[string]int{}
	for _, c := range cases {
		t.Run(c.kind+"/"+c.name, func(t *testing.T) {
			h := hypOf(t, Score(c.ev), c.kind)
			if h.Score != c.score || h.Band != c.band || outcomes(h) != c.outcomes {
				t.Fatalf("score=%.2f band=%s outcomes=%s, beklenen %.2f %s %s\n%+v", h.Score, h.Band, outcomes(h), c.score, c.band, c.outcomes, h.Checks)
			}
			for _, ck := range h.Checks {
				if strings.TrimSpace(ck.Reason) == "" {
					t.Fatalf("%s: gerekçesiz kontrol", ck.Name)
				}
			}
		})
		seen[c.kind]++
	}
	// Her tür en az üç satırla (doğrula / çürüt / bilinmiyor) — yeni tür satırsız giremez.
	for _, s := range hypothesisTable {
		if seen[s.Kind] < 3 {
			t.Errorf("%s: tablo satırı eksik (%d)", s.Kind, seen[s.Kind])
		}
		if !slices.Contains(cosrelearn.CaseCategories, s.Category) {
			t.Errorf("%s: kategori %q incident_cases sözlüğünde yok", s.Kind, s.Category)
		}
		w := s.Prior
		for _, c := range s.Checks {
			w += c.Weight
		}
		if s.Prior <= 0 || s.Prior > 0.3 || w < bandHighMin {
			t.Errorf("%s: öncül %.2f / tam doğrulamada %.2f — yüksek banda çıkamıyor", s.Kind, s.Prior, w)
		}
	}
	if len(hypothesisTable) != 8 {
		t.Fatalf("8 tür beklenir: %d", len(hypothesisTable))
	}
	if BandOf(0.70) != BandHigh || BandOf(0.6999) != BandMedium || BandOf(0.40) != BandMedium || BandOf(0.39) != BandLow {
		t.Fatal("bant eşikleri: yüksek ≥0.70, orta ≥0.40")
	}
}

// argoApp / argoAt — v0.10.1310 Facts.Argo kurucuları (syncs < 0 = sayım okunmadı).
func argoApp(app, sync, health string, syncs int, workloads ...string) mcptools.ArgoAppStatus {
	a := mcptools.ArgoAppStatus{App: app, Sync: sync, Health: health, Workloads: workloads}
	if syncs >= 0 {
		a.Syncs24h = &syncs
	}
	return a
}

func argoAt(read time.Time, apps ...mcptools.ArgoAppStatus) *ArgoFacts {
	return argoFactsOf(apps, read)
}

// TestChkArgoWindow — v0.10.1310: argo_sync'in pencere kapsamı ve eşleme
// kenarları (Syncs24h zaman damgası taşımaz; pencere okuma anıyla kapsanır).
func TestChkArgoWindow(t *testing.T) {
	cases := []struct {
		name    string
		argo    *ArgoFacts
		deploys []DeployFact
		want    int
		reason  string
	}{
		{"dikiş yok → okunmadı", nil, nil, 0, "okunmadı"},
		{"sakin + kapsıyor → çürütür", argoAt(t0, argoApp("a", "Synced", "Healthy", 0)), nil, -1, "senkron yok"},
		{"sakin ama okuma başlangıçtan > 23 sa sonra → bilinmiyor", argoAt(start.Add(30*time.Hour), argoApp("a", "Synced", "Healthy", 0)), nil, 0, "kapsamıyor"},
		{"sakin ama okuma başlangıçtan önce → bilinmiyor", argoAt(start.Add(-time.Minute), argoApp("a", "Synced", "Healthy", 0)), nil, 0, "kapsamıyor"},
		{"senkron sayımı okunmadı → bilinmiyor", argoAt(t0, argoApp("a", "Synced", "Healthy", -1)), nil, 0, "bağlanamıyor"},
		{"Degraded → çürütmez", argoAt(t0, argoApp("a", "Synced", "Degraded", 0)), nil, 0, "bağlanamıyor"},
		{"senkron var, imajlı deploy → config kanıtı değil", argoAt(t0, argoApp("a", "Synced", "Healthy", 1, "checkout")),
			[]DeployFact{{At: ns(-10 * time.Minute), Source: "rollout", Name: "checkout", Version: "r2", Prev: "r1"}}, 0, "bağlanamıyor"},
		{"senkron başka iş yükünün uygulamasında → bağlanmaz", argoAt(t0, argoApp("a", "Synced", "Healthy", 0, "checkout"), argoApp("b", "Synced", "Healthy", 4, "web")),
			[]DeployFact{{At: ns(-10 * time.Minute), Source: "rollout", Name: "checkout"}}, 0, "bağlanamıyor"},
		{"imajsız rollout pencere dışında → bağlanmaz", argoAt(t0, argoApp("a", "Synced", "Healthy", 1, "checkout")),
			[]DeployFact{{At: ns(-90 * time.Minute), Source: "rollout", Name: "checkout"}}, 0, "bağlanamıyor"},
		{"OutOfSync okuma anından bağımsız doğrular", argoAt(start.Add(30*time.Hour), argoApp("a", "OutOfSync", "Healthy", -1)), nil, 1, "OutOfSync"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := baseEv(symptomError, stepDeploys)
			ev.Facts.Deploys = &DeployFacts{Rows: c.deploys}
			ev.Facts.Argo = c.argo
			o, why, _ := chkArgo(ev)
			if o != c.want || !strings.Contains(why, c.reason) {
				t.Fatalf("chkArgo = %d %q, beklenen %d (%q)", o, why, c.want, c.reason)
			}
		})
	}
}

func richEvidence() Evidence {
	ev := baseEv(symptomError, stepSweep, stepDeploys, stepDeps, stepPivot, stepLogSpike, stepEvents)
	ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{{At: ns(-12 * time.Minute), Source: "rollout", Name: "checkout", Version: "rev42", Prev: "rev41"}}}
	ev.Facts.Pods = &PodFacts{PodsState: "ok", Pods: 3, MaxCPUPct: fp(60), MaxMemPct: fp(75)}
	ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "payments", Calls: 90, ErrorRate: 6}, {Target: "db:pg@orders", Calls: 300, AvgMs: 120}},
		Causes: []depsCause{{Service: "payments", Relation: chstore.RelationDownstream, Direction: chstore.ChangeWorse}}, Subject: &SubjectChange{RateDeltaPct: 20, P99DeltaPct: 40}}
	ev.Facts.Pivot = &PivotFacts{TraceID: "t", ErrorSpans: 1, WallMs: 1000, Path: []PathFact{{Name: "SELECT 1", SelfMs: 300}}}
	ev.Facts.Spike = &SpikeFacts{Level: "error", FromMs: start.UnixMilli(), Factor: 3, Contributors: []SpikeContrib{{Dim: "pattern", Value: "x", New: true}}}
	ev.Facts.Events = &EventFacts{AlertsOK: true, RelatedOK: true}
	ev.Synth = &SynthRef{TopSuspect: "payments", Confidence: 0.5}
	return ev
}

// TestScoreDeterministic — aynı girdi aynı çıktı (bayt bayt); sıra skora göre
// azalan, eşitlikte tablo sırası; skor [0,1], iki ondalık.
func TestScoreDeterministic(t *testing.T) {
	ev := richEvidence()
	first, _ := json.Marshal(Score(ev))
	for i := 0; i < 50; i++ {
		got, _ := json.Marshal(Score(ev))
		if string(got) != string(first) {
			t.Fatalf("koşu %d farklı:\n%s\n%s", i, got, first)
		}
	}
	hs := Score(ev)
	for i, h := range hs {
		if h.Score < 0 || h.Score > 1 || h.Score*100 != float64(int(h.Score*100+0.5)) {
			t.Fatalf("skor aralığı / yuvarlama: %+v", h)
		}
		if i > 0 && hs[i-1].Score < h.Score {
			t.Fatalf("sıra azalan değil: %v", hs)
		}
	}
	// Boş gerçekler: her kart öncülünde; eşitlikte tablo sırası.
	empty := Score(baseEv(""))
	var order []string
	for _, h := range empty {
		order = append(order, fmt.Sprintf("%s:%.2f", h.Kind, h.Score))
	}
	if got := strings.Join(order, " "); got != "deploy:0.20 saturation:0.15 dependency:0.15 code_regression:0.15 config:0.10 external:0.10 db_slow:0.10 traffic_surge:0.10" {
		t.Fatalf("boş girdi sırası: %s", got)
	}
}

// TestHypothesisMarkNeverChangesScore — operatör işareti (Ele / Onayla) skoru,
// bandı, kontrolleri DEĞİŞTİRMEZ; yeniden skorlamada türe göre taşınır.
// Oylar / işaretler Evidence'a girmez (ratchet: Evidence'ta işaret alanı yok).
func TestHypothesisMarkNeverChangesScore(t *testing.T) {
	st := newFakeStore()
	hs := Score(richEvidence())
	inv := Investigation{ProblemID: "p1", Service: "checkout", State: StateDone, EnqueuedAt: 1, Hypotheses: hs}
	if err := save(context.Background(), st, inv); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(hs)
	for _, state := range []string{MarkDismissed, MarkConfirmed} {
		h, err := ApplyHypothesisMark(context.Background(), st, "p1", MarkRequest{Kind: hs[0].Kind, State: state, Note: " yanlış alarm ", By: "op@x"})
		if err != nil || h.HumanState == nil || h.HumanState.State != state || h.HumanState.Note != "yanlış alarm" {
			t.Fatalf("işaret: %+v %v", h, err)
		}
		got := st.get(t, "p1").Hypotheses
		for i := range got {
			got[i].HumanState = nil
		}
		after, _ := json.Marshal(got)
		if string(after) != string(before) {
			t.Fatalf("işaret skoru değiştirdi:\n%s\n%s", after, before)
		}
	}
	// Yeniden skor: işaret taşınır, skor yine gerçeklerden.
	next := carryHuman(Score(richEvidence()), st.get(t, "p1").Hypotheses)
	if hypOf(t, next, hs[0].Kind).HumanState == nil || hypOf(t, next, hs[0].Kind).Score != hs[0].Score {
		t.Fatal("yeniden skorda işaret taşınmalı, skor değişmemeli")
	}
	// Ret: geçersiz tür / durum, koşan kayıt, kayıt yok.
	if _, err := ApplyHypothesisMark(context.Background(), st, "p1", MarkRequest{Kind: "nope", State: MarkDismissed}); err != ErrMarkInvalid {
		t.Fatalf("geçersiz tür: %v", err)
	}
	if _, err := ApplyHypothesisMark(context.Background(), st, "p1", MarkRequest{Kind: HypDeploy, State: "maybe"}); err != ErrMarkInvalid {
		t.Fatalf("geçersiz durum: %v", err)
	}
	inv.State = StateRunning
	_ = save(context.Background(), st, inv)
	if _, err := ApplyHypothesisMark(context.Background(), st, "p1", MarkRequest{Kind: HypDeploy, State: MarkDismissed}); err != ErrMarkBusy {
		t.Fatalf("koşan kayıt: %v", err)
	}
	if _, err := ApplyHypothesisMark(context.Background(), st, "p9", MarkRequest{Kind: HypDeploy, State: MarkDismissed}); err != ErrMarkNotFound {
		t.Fatalf("kayıt yok: %v", err)
	}
	// Oylar OKUNMAZ (operatör: "oylara çok güvenmeyelim"): paket ai_feedback /
	// oy okuyucusuna dokunmaz.
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, needle := range []string{"ai_feedback", "Feedback(", "Votes", "ListAIFeedback"} {
			if strings.Contains(string(b), needle) {
				t.Errorf("%s: %q — skor / ajan oy okumaz", f, needle)
			}
		}
	}
	// Evidence işaret / oy taşımaz.
	src, _ := os.ReadFile("hypotheses.go")
	body := string(src)
	i := strings.Index(body, "type Evidence struct {")
	j := strings.Index(body[i:], "\n}\n")
	if decl := body[i : i+j]; strings.Contains(decl, "Human") || strings.Contains(decl, "Vote") || strings.Contains(decl, "Feedback") {
		t.Fatalf("Evidence işaret / oy alanı taşımamalı:\n%s", decl)
	}
}

// TestNeverWritesHypothesisTable — ajan (internal/sre + işaret / kuyruk uçları)
// root_cause_hypotheses / derin kanıt / problems / öncelik YAZMAZ: yazıcı
// seçicisi ve yazan SQL metni yok. Tek yazım kendi kaydı (PutSREInvestigation).
func TestNeverWritesHypothesisTable(t *testing.T) {
	writer := regexp.MustCompile(`^(Upsert|Insert|Write|Save|Put|Set|Update|Mark|Delete|Append|Store|Persist|Replace|Resolve|Ack)\w*(Hypothes|RootCause|DeepEvidence|Deep|Problem|Priority|Verdict|Incident)`)
	sqlWrite := regexp.MustCompile(`(?i)(insert\s+into|alter\s+table|delete\s+from|update)\s+(root_cause_hypotheses|problems|rca_verdicts|incident_problems)`)
	files, _ := filepath.Glob("*.go")
	files = append(files, "../api/sre_hypothesis.go", "../api/sre_status.go", "../api/sre_investigation_chat.go",
		"../api/sre_recommendation.go", "../api/sre_argo.go") // v0.10.1293 — Faz 4 uçları
	fset := token.NewFileSet()
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if writer.MatchString(x.Sel.Name) {
					t.Errorf("%s: %s — AI SRE hipotez tablosu / problem / öncelik yazamaz", fset.Position(x.Pos()), x.Sel.Name)
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING && sqlWrite.MatchString(x.Value) {
					t.Errorf("%s: yazan SQL %s", fset.Position(x.Pos()), x.Value)
				}
			}
			return true
		})
	}
	if scanned < 15 {
		t.Fatalf("yalnız %d dosya tarandı — tarama bozuk", scanned)
	}
}

// TestBuildTimelineOrderCap — ≤30 olay, zamana göre artan; zorunlu olaylar
// (başlangıç, ilk hata, artış, incident) her zaman; problemin kendi alarmı ve
// pencere dışı olaylar düşer; tavan aşılınca başlangıca uzak alarmlar düşer.
func TestBuildTimelineOrderCap(t *testing.T) {
	ev := baseEv(symptomLatency, stepDeploys, stepLogSpike, stepEvents, stepPivot)
	ev.Facts.Problem = &ProblemFacts{RuleName: "p99 > 2s"}
	var rows []DeployFact
	for i := 0; i < 10; i++ {
		rows = append(rows, DeployFact{At: ns(time.Duration(-50+i*5) * time.Minute), Source: "rollout", Name: "checkout", Version: fmt.Sprintf("rev%d", i), Prev: fmt.Sprintf("rev%d", i-1)})
	}
	rows[3].Version, rows[3].Prev = "", "" // imajsız rollout
	rows[9].At = ns(-3 * time.Hour)        // pencere dışı
	ev.Facts.Deploys = &DeployFacts{Rows: rows}
	ev.Facts.Spike = &SpikeFacts{Level: "error", FromMs: start.Add(-4 * time.Minute).UnixMilli(), Factor: 5}
	ev.Facts.Pivot = &PivotFacts{TraceID: "t", ErrorSpans: 2, StartMs: start.Add(-6 * time.Minute).UnixMilli()}
	var alerts []AlertFact
	alerts = append(alerts, AlertFact{ID: "p1", RuleName: "self", StartedAt: start.UnixNano()})
	for i := 0; i < 40; i++ {
		alerts = append(alerts, AlertFact{ID: fmt.Sprintf("a%d", i), RuleName: "rule", Priority: "P2", StartedAt: ns(time.Duration(i+1) * time.Minute)})
	}
	ev.Facts.Events = &EventFacts{AlertsOK: true, Alerts: alerts, IncidentID: "inc-1", IncidentAt: ns(90 * time.Minute)}
	now := start.Add(2 * time.Hour)
	tl := buildTimeline(ev, "p1", now)
	if len(tl) != TimelineMax {
		t.Fatalf("tavan %d: %d", TimelineMax, len(tl))
	}
	kinds := map[string]int{}
	for i, e := range tl {
		kinds[e.Kind]++
		if i > 0 && tl[i-1].At > e.At {
			t.Fatalf("sıra bozuk: %d %+v", i, tl)
		}
		if e.Ref == "p1" && e.Kind == TLAlert {
			t.Fatal("problemin kendi alarmı ikinci kez girdi")
		}
		if e.At < ns(-60*time.Minute) || e.At > now.UnixNano() {
			t.Fatalf("pencere dışı: %+v", e)
		}
	}
	if kinds[TLProblemStart] != 1 || kinds[TLFirstError] != 1 || kinds[TLSpikeStart] != 1 || kinds[TLIncident] != 1 || kinds[TLDeploy] != 9 {
		t.Fatalf("zorunlu olaylar / deploylar: %v", kinds)
	}
	if kinds[TLAlert] != TimelineMax-4-9 {
		t.Fatalf("kalan yer alarmlara: %v", kinds)
	}
	// Başlangıca en yakın alarmlar kalır (a0 … a16), uzaktakiler düşer.
	var last string
	for _, e := range tl {
		if e.Kind == TLAlert {
			last = e.Ref
		}
	}
	if last != "a16" {
		t.Fatalf("başlangıca yakın alarmlar seçilmeli; son %s", last)
	}
	if !strings.Contains(tl[0].Label, "Deploy") && !strings.Contains(tl[0].Label, "Rollout") {
		t.Fatalf("ilk olay en eski deploy: %+v", tl[0])
	}
	found := false
	for _, e := range tl {
		if e.Kind == TLDeploy && strings.HasPrefix(e.Label, "Rollout (imaj değişmedi)") {
			found = true
		}
	}
	if !found {
		t.Fatal("imajsız rollout etiketi")
	}
	// Hata belirtisinde ilk hata = başlangıç → ayrı olay yok.
	ev2 := baseEv(symptomError)
	if tl2 := buildTimeline(ev2, "p1", now); len(tl2) != 1 || tl2[0].Kind != TLProblemStart {
		t.Fatalf("boş gerçekler: %+v", tl2)
	}
}

// TestNeedsNodeCheck — node okuması yalnız doygunluk hipotezi isterse.
func TestNeedsNodeCheck(t *testing.T) {
	pods := func(state string, cpu float64, oom bool) Evidence {
		ev := baseEv(symptomLatency, stepSweep)
		ev.Facts.Pods = &PodFacts{PodsState: state, Pods: 2, MaxCPUPct: fp(cpu), MaxMemPct: fp(cpu)}
		if oom {
			ev.Facts.Pods.OOMPods = []string{"x"}
		}
		return ev
	}
	for _, c := range []struct {
		name   string
		ev     Evidence
		verify string
		want   bool
	}{
		{"doğrulama her zaman", baseEv(symptomError), HypSaturation, true},
		{"pod verisi yok", baseEv(symptomLatency), "", false},
		{"Thanos yok (skipped)", pods("skipped", 0, false), "", false},
		{"OOM zaten doğruluyor", pods("ok", 50, true), "", false},
		{"pod rahat + gecikme → gürültülü komşu?", pods("ok", 30, false), "", true},
		{"pod eşik altı belirsiz", func() Evidence { e := pods("ok", 80, false); e.Symptom = symptomError; return e }(), "", true},
		{"pod rahat + hata belirtisi", func() Evidence { e := pods("ok", 30, false); e.Symptom = symptomError; return e }(), "", false},
	} {
		if got := needsNodeCheck(c.ev, c.verify); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

// TestVerifyRun — "Doğrula": önceki koşunun adımları + gerçekleri taşınır;
// yalnız hipotezin adımları yeniden okunur (saturation → tarama + node);
// anlatım koşu başına ≤1 çağrı (doğrulama da bir koşudur); işaret taşınır.
func TestVerifyRun(t *testing.T) {
	c := &calls{}
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
	rd := okReaders(c, n)
	rd.sweep = func(context.Context, mcptools.SweepRequest) (mcptools.SweepResult, error) {
		c.hit("sweep")
		return mcptools.SweepResult{OK: true, Verdict: mcptools.SweepVerdict{Label: "Dikkat"}, Scanned: mcptools.SweepScanned{Pods: 2},
			Sections: []mcptools.SweepSection{{Name: "pods", State: "ok"}}}, nil
	}
	rd.node = func(context.Context, string, time.Time, time.Time) (mcptools.NodeCheck, error) {
		c.hit("node")
		v := 95.0
		return mcptools.NodeCheck{Section: mcptools.SweepSection{Name: "node", State: "ok", Summary: "1 node"},
			Nodes: []mcptools.NodePressure{{Node: "n1", CPUPct: &v}}}, nil
	}
	st := newFakeStore()
	ctx := context.Background()
	first := newInv()
	runInvestigation(ctx, rd, first, func(*Investigation) {}, func() time.Time { return t0 })
	first.State, first.FinishedAt, first.Run = StateDone, t0.UnixNano(), 1
	first.Hypotheses[1].HumanState = &HumanState{State: MarkDismissed, By: "op"}
	marked := first.Hypotheses[1].Kind
	if err := save(ctx, st, *first); err != nil {
		t.Fatal(err)
	}
	if c.get("node") != 1 {
		t.Fatalf("gecikme yok, pod'lar limit bilgisiz → node bir kez: %v", c.n)
	}
	// Geçersiz hipotez reddedilir.
	if _, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "p1", Service: "checkout", Kind: KindVerify, Hypothesis: "nope"}, NormalizeSettings(DefaultSettings())); err != ErrInvalid {
		t.Fatalf("geçersiz hipotez: %v", err)
	}
	res, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "p1", Service: "checkout", Kind: KindVerify, Hypothesis: HypSaturation, ProblemStartedAt: start.UnixNano()}, NormalizeSettings(DefaultSettings()))
	if err != nil || !res.Created || res.State != StateQueued {
		t.Fatalf("doğrulama kuyruğa: %+v %v", res, err)
	}
	q := st.get(t, "p1")
	if q.Kind != KindVerify || q.Hypothesis != HypSaturation || q.Run != 2 || len(q.Steps) != len(first.Steps) || q.Facts == nil || q.Facts.Deps == nil {
		t.Fatalf("doğrulama önceki adımları + gerçekleri taşımalı: %+v", q)
	}
	if h := hypOf(t, q.Hypotheses, marked); h.HumanState == nil {
		t.Fatal("işaret taşınmalı")
	}
	// İkinci doğrulama isteği kuyruktayken no-op.
	if r2, _ := Enqueue(ctx, st, EnqueueRequest{ProblemID: "p1", Service: "checkout", Kind: KindVerify, Hypothesis: HypDeploy}, NormalizeSettings(DefaultSettings())); r2.Created {
		t.Fatal("kuyruktaki işe ikinci doğrulama açılmamalı")
	}
	before := map[string]int{}
	for k, v := range c.n {
		before[k] = v
	}
	n.calls = 0
	runInvestigation(ctx, rd, &q, func(*Investigation) {}, func() time.Time { return t0.Add(30 * time.Minute) })
	if c.get("sweep") != before["sweep"]+1 || c.get("node") != before["node"]+1 {
		t.Fatalf("zorlanan adımlar (tarama + node) yeniden okunmalı: %v → %v", before, c.n)
	}
	for _, k := range []string{"deploys", "graph", "spike", "incident"} {
		if c.get(k) != before[k] {
			t.Fatalf("%s yeniden okunmamalı (önceki koşunun gerçeği): %v → %v", k, before, c.n)
		}
	}
	if n.calls != 1 {
		t.Fatalf("doğrulama koşusu da tek anlatım: %d", n.calls)
	}
	sat := hypOf(t, q.Hypotheses, HypSaturation)
	if outcomes(sat) == "" || sat.Checks[2].Outcome != 1 {
		t.Fatalf("node baskısı doygunluğu doğrulamalı: %+v", sat)
	}
	if hypOf(t, q.Hypotheses, marked).HumanState == nil {
		t.Fatal("doğrulama koşusu işareti korumalı")
	}
	// Elle yeniden araştırma adımları temizler ama işareti korur.
	q.State = StateDone
	_ = save(ctx, st, q)
	if _, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "p1", Service: "checkout", Kind: KindManual}, NormalizeSettings(DefaultSettings())); err != nil {
		t.Fatal(err)
	}
	m := st.get(t, "p1")
	if len(m.Steps) != 0 || m.Facts != nil || hypOf(t, m.Hypotheses, marked).HumanState == nil {
		t.Fatalf("elle koşu: adımlar temiz, işaret korunur: %+v", m)
	}
}

// TestRenderHypothesesTR — anlatım girdisinin hipotez satırları (golden):
// elenmiş kart en olası sayılmaz, düşük bant "belirgin hipotez yok".
func TestRenderHypothesesTR(t *testing.T) {
	hs := []Hypothesis{
		{Kind: HypDeploy, Label: "Deploy", Score: 0.9, Band: BandHigh, HumanState: &HumanState{State: MarkDismissed}},
		{Kind: HypDependency, Label: "Bağımlılık arızası", Score: 0.55, Band: BandMedium, Checks: []HypothesisCheck{{Outcome: 0, Reason: "x"}, {Outcome: 1, Reason: "aşağı akış payments kötüleşti"}}},
		{Kind: HypConfig, Label: "Konfigürasyon değişikliği", Score: 0.1, Band: BandLow},
	}
	want := "AJAN HİPOTEZİ (kod skoru): Bağımlılık arızası — orta 0.55 — aşağı akış payments kötüleşti\n" +
		"DİĞER HİPOTEZLER: Konfigürasyon değişikliği düşük 0.10 · Deploy yüksek 0.90 (ekip reddetti)\n"
	if got := renderHypothesesTR(hs); got != want {
		t.Fatalf("golden:\n%s\nbeklenen:\n%s", got, want)
	}
	low := []Hypothesis{{Label: "Deploy", Score: 0.2, Band: BandLow}}
	if got := renderHypothesesTR(low); got != "AJAN HİPOTEZİ (kod skoru): belirgin hipotez yok — en yüksek Deploy düşük 0.20\n" {
		t.Fatalf("düşük: %q", got)
	}
	if renderHypothesesTR(nil) != "" {
		t.Fatal("hipotezsiz kayıt satır basmaz")
	}
	// Sohbet özeti: ≤3, destek / karşı gerekçe, operatör işareti.
	// Ekip kararı sırası: reddedilen en sona (hypothesis_ruling.go).
	sum := SummaryOf(Investigation{State: StateDone, Hypotheses: hs})
	if len(sum.Hypotheses) != 3 || sum.Hypotheses[2].Operator != MarkDismissed || len(sum.Hypotheses[0].Support) != 1 {
		t.Fatalf("özet hipotezleri: %+v", sum.Hypotheses)
	}
	if txt := RenderSavedTR("P-1", sum, time.UTC); !strings.Contains(txt, "Ajanın hipotezleri (kod skoru — değiştirme):") || !strings.Contains(txt, "(ekip reddetti") {
		t.Fatalf("kayıtlı araştırma bloğu: %s", txt)
	}
}

// TestFactsEncodeCap — Faz 2 alanlarıyla kayıt 64 KB'ı aşarsa önce gerçeklerin
// uzun listeleri, sonra kart fazlası düşer; ilk 3 kart kalır.
func TestFactsEncodeCap(t *testing.T) {
	inv := Investigation{ProblemID: "p1", Service: "checkout", Hypotheses: Score(richEvidence()), Facts: &Facts{Events: &EventFacts{}}}
	for i := 0; i < 400; i++ {
		inv.Facts.Events.Alerts = append(inv.Facts.Events.Alerts, AlertFact{ID: strings.Repeat("a", 120), RuleName: strings.Repeat("ğ", 60)})
		inv.Timeline = append(inv.Timeline, TimelineEvent{Label: strings.Repeat("ş", 100)})
	}
	raw, err := Encode(inv)
	if err != nil {
		t.Fatal(err)
	}
	var back Investigation
	_ = json.Unmarshal([]byte(raw), &back)
	if len(raw) > chstore.SREInvestigationMaxBytes || len(back.Hypotheses) < HypothesesShown || len(back.Timeline) > TimelineMax {
		t.Fatalf("tavan: %d B, %d kart, %d olay", len(raw), len(back.Hypotheses), len(back.Timeline))
	}
	if len(inv.Timeline) != 400 {
		t.Fatal("Encode çağıranın kaydına dokunmamalı")
	}
}

// v0.10.1299 — operatör 2026-10-17: 5 dk p99 gürültüsü yanlış bulgu üretiyordu.
// chkP99Up: göreli artış + mutlak tabanlar (≥ 100 ms, ≥ +50 ms) + ham span
// doğrulaması; doğrulanmamış MV p99 farkı "olası" → 0.
func TestChkP99UpFloorsAndCorroboration(t *testing.T) {
	for _, c := range []struct {
		name string
		sc   SubjectChange
		pods *PodFacts
		want int
		in   string
	}{
		{"3 ms vs 2 ms (+%50) tabanın altında", SubjectChange{P99DeltaPct: 50, CurP99Ms: 3, BaseP99Ms: 2}, &PodFacts{LatencyCorroborated: 1}, 0, "pratik eşik altı"},
		{"300 vs 200 ms doğrulandı", SubjectChange{P99DeltaPct: 50, CurP99Ms: 300, BaseP99Ms: 200}, &PodFacts{LatencyCandidates: 1, LatencyCorroborated: 1}, 1, "ham span doğruladı"},
		{"300 vs 200 ms doğrulanmadı", SubjectChange{P99DeltaPct: 50, CurP99Ms: 300, BaseP99Ms: 200}, &PodFacts{LatencyCandidates: 2}, 0, "olası"},
		{"300 vs 200 ms tarama yok", SubjectChange{P99DeltaPct: 50, CurP99Ms: 300, BaseP99Ms: 200}, nil, 0, "olası"},
		{"eski kayıt: mutlak değer yok", SubjectChange{P99DeltaPct: 60}, &PodFacts{LatencyCorroborated: 1}, 0, "pratik eşik altı"},
		{"düşüş çürütür", SubjectChange{P99DeltaPct: -10, CurP99Ms: 180, BaseP99Ms: 200}, nil, -1, "artmadı"},
		{"+%10 eşik altı", SubjectChange{P99DeltaPct: 10, CurP99Ms: 220, BaseP99Ms: 200}, nil, 0, "eşik +%25"},
	} {
		ev := baseEv(symptomLatency, stepDeps)
		sc := c.sc
		ev.Facts.Deps = &DepsFacts{TopologyOK: true, ChangesOK: true, Subject: &sc}
		ev.Facts.Pods = c.pods
		got, why, _ := chkP99Up(ev)
		if got != c.want || !strings.Contains(why, c.in) {
			t.Errorf("%s: %d %q (beklenen %d, %q)", c.name, got, why, c.want, c.in)
		}
	}
}
