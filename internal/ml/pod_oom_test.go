package ml

// pod_oom_test.go — v0.10.2476 sözleşmesi: OOMKilled pod Problem açar.
//   - YENİ kill = penceredeki restart artışı VE son sonlanma nedeni OOMKilled
//     (neden yapışkan: tek başına asla tetiklemez);
//   - iş yükü başına tek olay, pod → iş yükü → servis entity canlı dizininden;
//   - P2 varsayılan; pod'ların ≥ %50'si ya da 30 dk'da ≥ 3 kill → P1;
//   - servis olayı yoksa Problem'i açar (manşet OOM cümlesi), varsa üye.
// Ad / cluster / host değerleri uydurmadır.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

func TestOOMPriority(t *testing.T) {
	cases := []struct {
		name                   string
		killed, total, kills30 int
		want                   string
		reasonHas              string
	}{
		{"tek kill, 4 pod'dan 1", 1, 4, 1, "P2", "1/4"},
		{"2/5 pod, 2 kill", 2, 5, 2, "P2", "2/5"},
		{"yarısı → P1", 2, 4, 2, "P1", "2/4"},
		{"tek replika tek kill → P2 (pay kolu ≥ 2 pod ister)", 1, 1, 1, "P2", "1/1"},
		{"tek replika 30 dk'da 2 kill → P2", 1, 1, 2, "P2", "1/1"},
		{"tek replika 30 dk'da 3 kill → P1", 1, 1, 3, "P1", "30 dk'da 3 kez OOMKilled"},
		{"2 pod'dan 1 → P1 (%50)", 1, 2, 1, "P1", "1/2 pod (%50)"},
		{"30 dk'da 3 kill → P1", 1, 10, 3, "P1", "30 dk'da 3"},
		{"30 dk'da 2 kill, az pod → P2", 1, 10, 2, "P2", ""},
		{"toplam bilinmiyor → pay kolu yok", 1, 0, 1, "P2", ""},
	}
	for _, c := range cases {
		p, why := oomPriority(c.killed, c.total, c.kills30)
		if p != c.want || !strings.Contains(why, c.reasonHas) {
			t.Errorf("%s: %s %q", c.name, p, why)
		}
	}
}

// Tetik sorgusu restart artışı olmadan son sonlanma nedenine bakmaz.
func TestOOMKillsExprNeverStickyAlone(t *testing.T) {
	e := oomKillsExpr(300, `,namespace=~"team-.*"`)
	for _, want := range []string{
		"topk(200, ",
		`increase(kube_pod_container_status_restarts_total{container!="",namespace=~"team-.*"}[300s])`,
		`> 0 and on (namespace, pod, container) (max by (namespace, pod, container) (kube_pod_container_status_last_terminated_reason{reason="OOMKilled",namespace=~"team-.*"}) == 1)`,
	} {
		if !strings.Contains(e, want) {
			t.Errorf("eksik %q\n%s", want, e)
		}
	}
	if strings.Index(e, "increase(") > strings.Index(e, "last_terminated_reason") {
		t.Errorf("artış koşulu nedenin önünde olmalı: %s", e)
	}
	if !strings.Contains(oomKillsExpr(30, ""), "[120s]") {
		t.Error("pencere tabanı 120 sn")
	}
}

func TestOOMExtrasExprBounded(t *testing.T) {
	nss := []string{"b.ns", "a-ns"}
	for i := 0; i < 60; i++ {
		nss = append(nss, "z"+strings.Repeat("x", i))
	}
	e := oomExtrasExpr(nss, "")
	if !strings.Contains(e, `namespace=~"a-ns|b\\.ns|z|`) {
		t.Errorf("sıralı + kaçışlı namespace süzgeci yok: %.200s", e)
	}
	i := strings.Index(e, `namespace=~"`) + len(`namespace=~"`)
	if got := strings.Count(e[i:i+strings.Index(e[i:], `"`)], "|") + 1; got != oomMaxNamespaces {
		t.Errorf("namespace tavanı: %d", got)
	}
	for _, w := range []string{`"w", "15"`, `"w", "30"`, `"w", "lim"`, "[15m]", "[30m]", `resource="memory"`, "topk(500, "} {
		if !strings.Contains(e, w) {
			t.Errorf("eksik %q", w)
		}
	}
}

const tCid = "c1"

func tWL(name string) string { return entity.WorkloadID(tCid, "shop", "Deployment", name) }

func tLive() []entity.LivePod {
	svc := func(n string, s int64) []entity.LiveService { return []entity.LiveService{{Name: n, Spans: s}} }
	return []entity.LivePod{
		{Namespace: "shop", Name: "cart-7f9c-a1", Workload: tWL("cart"), Services: svc("cart-api", 100)},
		{Namespace: "shop", Name: "cart-7f9c-b2", Workload: tWL("cart"), Services: svc("cart-api", 90)},
		{Namespace: "shop", Name: "cart-7f9c-c3", Workload: tWL("cart"), Services: svc("cart-api", 80)},
		{Namespace: "shop", Name: "cart-7f9c-d4", Workload: tWL("cart")}, // span basmıyor, iş yükü sayımında var
		{Namespace: "shop", Name: "batch-x1", Workload: tWL("batch")},    // servissiz iş yükü
		{Namespace: "shop", Name: "bare-pod"},                            // iş yükü yok
	}
}

func TestOOMGroup(t *testing.T) {
	fresh := []oomRow{{NS: "shop", Pod: "cart-7f9c-a1", Ctr: "app", V: 1}}
	extras := []oomRow{
		{NS: "shop", Pod: "cart-7f9c-a1", Ctr: "app", W: "15", V: 1},
		{NS: "shop", Pod: "cart-7f9c-b2", Ctr: "app", W: "15", V: 1},
		{NS: "shop", Pod: "cart-7f9c-a1", Ctr: "app", W: "30", V: 1},
		{NS: "shop", Pod: "cart-7f9c-b2", Ctr: "app", W: "30", V: 1},
		{NS: "shop", Pod: "cart-7f9c-a1", Ctr: "app", W: "lim", V: 512 << 20},
		{NS: "shop", Pod: "cart-7f9c-b2", Ctr: "app", W: "lim", V: 512 << 20},
		{NS: "shop", Pod: "other-1", Ctr: "app", W: "15", V: 4}, // dizinde yok, tetik de yok: yok sayılır
	}
	ws, bad := oomGroup(tCid, fresh, extras, tLive())
	if bad != 0 || len(ws) != 1 {
		t.Fatalf("ws=%+v bad=%d", ws, bad)
	}
	w := ws[0]
	if w.Service != "cart-api" || w.Name != "cart" || w.Kind != "Deployment" || w.Namespace != "shop" || w.WorkloadID != tWL("cart") {
		t.Errorf("kimlik %+v", w)
	}
	if w.KilledPods != 2 || w.TotalPods != 4 || w.NewKills != 1 || w.Kills15 != 2 || w.Kills30 != 2 || w.LimitMi != 512 {
		t.Errorf("sayımlar %+v", w)
	}
	if w.Priority != "P1" { // 2/4 pod = %50
		t.Errorf("öncelik %s %s", w.Priority, w.Reason)
	}
	if len(w.Pods) != 2 || w.Pods[0].Pod != "cart-7f9c-a1" || w.Pods[0].LimitMi != 512 {
		t.Errorf("pod listesi %+v", w.Pods)
	}

	// Ek sorgu yok (hata): sayım yeni kill'den; 1/4 pod → P2.
	ws, _ = oomGroup(tCid, fresh, nil, tLive())
	if len(ws) != 1 || ws[0].Kills15 != 1 || ws[0].Kills30 != 1 || ws[0].KilledPods != 1 || ws[0].Priority != "P2" || ws[0].LimitMi != 0 {
		t.Errorf("ek sorgusuz %+v", ws)
	}

	// Çözülemeyenler olay üretmez: dizinde yok, iş yükü yok, servis yok.
	ws, bad = oomGroup(tCid, []oomRow{
		{NS: "shop", Pod: "ghost-1", Ctr: "app", V: 1},
		{NS: "shop", Pod: "bare-pod", Ctr: "app", V: 1},
		{NS: "shop", Pod: "batch-x1", Ctr: "job", V: 2},
	}, nil, tLive())
	if len(ws) != 0 || bad != 3 {
		t.Errorf("çözülemeyen: ws=%+v bad=%d", ws, bad)
	}

	// Aynı iş yükünde iki pod tetik → TEK olay.
	ws, _ = oomGroup(tCid, []oomRow{{NS: "shop", Pod: "cart-7f9c-a1", Ctr: "app", V: 1}, {NS: "shop", Pod: "cart-7f9c-c3", Ctr: "app", V: 2}}, nil, tLive())
	if len(ws) != 1 || ws[0].NewKills != 3 || ws[0].KilledPods != 2 {
		t.Errorf("iş yükü başına tek olay: %+v", ws)
	}
}

func oomTestEvent(t *testing.T, kills15, kills30, killed, total int) chstore.MLEvent {
	t.Helper()
	cfg := chstore.DefaultMLConfig()
	w := oomWorkload{ClusterID: tCid, WorkloadID: tWL("cart"), Kind: "Deployment", Namespace: "shop", Name: "cart", Service: "cart-api",
		Pods: []oomPod{{Namespace: "shop", Pod: "cart-7f9c-a1", Container: "app", Kills: kills15}}, KilledPods: killed, TotalPods: total,
		NewKills: 1, Kills15: kills15, Kills30: kills30, LimitMi: 512}
	w.Priority, w.Reason = oomPriority(killed, total, kills30)
	return oomEvent(w, cfg, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC))
}

// Servis olayı yoksa OOM olayı Problem'i açar; öncelik ve gerekçe gerçek
// nedenden (oran / "100×" yok), Problem okumasında aynı karar.
func TestOOMOpensSvcProblem(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	prio := chstore.DefaultProblemPriority()
	for _, c := range []struct {
		name                    string
		k15, k30, killed, total int
		want, reason, head      string
	}{
		{"P2", 1, 2, 1, 4, "P2", "OOMKilled: 1/4 pod, 30 dk'da 2 kez", "1 pod, son 15 dk'da 1 kez"},
		{"P1 tekrar", 3, 4, 1, 4, "P1", "30 dk'da 4 kez OOMKilled", "1 pod, son 15 dk'da 3 kez"},
		{"P1 pay", 2, 2, 3, 5, "P1", "3/5 pod (%60) bellek sınırında sonlandırıldı", "3 pod, son 15 dk'da 2 kez"},
		{"tek replika P2", 1, 1, 1, 1, "P2", "OOMKilled: 1/1 pod, 30 dk'da 1 kez", "1 pod, son 15 dk'da 1 kez"},
	} {
		ev := oomTestEvent(t, c.k15, c.k30, c.killed, c.total)
		if ev.Detector != detPodOOM || ev.Signal != chstore.MLSignalMetric || ev.Code != "cart" || ev.Observed != float64(c.k15) || ev.Baseline != 0 {
			t.Fatalf("olay %+v", ev)
		}
		ds := decideSvcShadow([]chstore.MLEvent{ev}, nil, nil, prio, now)
		d := ds[ev.ID]
		if !d.WouldOpen || !d.Owner || d.RuleID != MLSvcRulePrefix+"cart-api" || d.Priority != c.want || d.PriorityReason != c.reason {
			t.Errorf("%s: karar %+v", c.name, d)
		}
		ins := svcProblemInputs([]chstore.MLEvent{ev}, ds)
		want := "cart · pod bellek sınırında sonlandırıldı (OOMKilled): " + c.head + " — limit 512 Mi."
		if len(ins) != 1 || ins[0].Desc != want || ins[0].Metric != chstore.MLOOMMetric {
			t.Errorf("%s: girdi %+v", c.name, ins)
		}
		p := newSvcProblem(ins[0])
		if h, _ := chstore.MLPromotedHeadline(p); h != want {
			t.Errorf("%s: manşet %q", c.name, h)
		}
		if got, why := chstore.PreviewProblemPriority(p, now.UnixNano(), prio); got != c.want || why != c.reason {
			t.Errorf("%s: Problem önceliği %s %q", c.name, got, why)
		}
	}
}

// Servis düzeyi olay varken OOM olayı yalnız üye: manşet ve öncelik ondan değil.
func TestOOMMemberOfSvcProblem(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	oom := oomTestEvent(t, 3, 4, 2, 4)
	svc := svcEv("e1", detSvcErrorRate, "cart-api", 0, 0.004, 0.068)
	evs := []chstore.MLEvent{svc, oom}
	ds := decideSvcShadow(evs, nil, nil, chstore.DefaultProblemPriority(), now)
	if !ds[oom.ID].WouldOpen || ds[oom.ID].Owner || ds[oom.ID].RuleID != ds[svc.ID].RuleID {
		t.Errorf("üye değil: %+v", ds[oom.ID])
	}
	ins := svcProblemInputs(evs, ds)
	if len(ins) != 1 || strings.Contains(ins[0].Desc, "OOMKilled") || len(ins[0].Events) != 2 {
		t.Errorf("girdi %+v", ins)
	}
}

// ── tik (sahte Remote Cluster + canlı dizin) ─────────────────────────────

type fakeOOMThanos struct {
	clusters []ThanosCluster
	kills    []ThanosSample
	extras   []ThanosSample
	err      error
	queries  []string
}

func (f *fakeOOMThanos) Clusters() []ThanosCluster { return f.clusters }
func (f *fakeOOMThanos) Instant(_ context.Context, _ string, expr string, _ time.Time) ([]ThanosSample, error) {
	f.queries = append(f.queries, expr)
	if f.err != nil {
		return nil, f.err
	}
	if strings.HasPrefix(expr, "topk(") {
		return f.kills, nil
	}
	return f.extras, nil
}

type fakeOOMLive struct{ pods []entity.LivePod }

func (f fakeOOMLive) View(context.Context, string) (*entity.LiveView, bool) {
	return &entity.LiveView{ClusterID: tCid, Pods: f.pods}, true
}

func TestPodOOMTick(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	lbl := func(pod, w string) map[string]string {
		return map[string]string{"namespace": "shop", "pod": pod, "container": "app", "w": w}
	}

	// Yeni kill yok: tek sorgu, olay yok.
	th := &fakeOOMThanos{clusters: []ThanosCluster{{ID: tCid, Name: "test-cluster"}}}
	d := &PodOOM{th: th, live: fakeOOMLive{tLive()}}
	evs, err := d.Tick(context.Background(), Deps{}, cfg, now)
	if err != nil || len(evs) != 0 || len(th.queries) != 1 {
		t.Fatalf("boş: evs=%v err=%v q=%d", evs, err, len(th.queries))
	}

	// Yeni kill: ek sorgu + iş yükü başına tek olay.
	th = &fakeOOMThanos{clusters: []ThanosCluster{{ID: tCid, Name: "test-cluster"}},
		kills:  []ThanosSample{{Labels: lbl("cart-7f9c-a1", ""), Value: 1}},
		extras: []ThanosSample{{Labels: lbl("cart-7f9c-a1", "30"), Value: 3}}}
	d = &PodOOM{th: th, live: fakeOOMLive{tLive()}}
	evs, err = d.Tick(context.Background(), Deps{}, cfg, now)
	if err != nil || len(evs) != 1 || len(th.queries) != 2 {
		t.Fatalf("kill: evs=%v err=%v q=%d", evs, err, len(th.queries))
	}
	if ev := evs[0]; ev.Service != "cart-api" || ev.ID != chstore.MLEventID(detPodOOM, "cart-api", tWL("cart")) || oomEvidenceOf(ev).Priority != "P1" {
		t.Errorf("olay %+v", ev)
	}

	// Tüm cluster'lar hata → tik hatası (koşular veri-yok tutmasına girer).
	th = &fakeOOMThanos{clusters: []ThanosCluster{{ID: tCid, Name: "test-cluster"}}, err: errors.New("422")}
	d = &PodOOM{th: th, live: fakeOOMLive{tLive()}}
	if _, err := d.Tick(context.Background(), Deps{}, cfg, now); err == nil {
		t.Error("hata bekleniyordu")
	}
	if d.Unmeasured()[detPodOOM] == "" {
		t.Error("ölçülemedi notu yok")
	}

	// Kaynak yok → sessiz, hata değil.
	d = &PodOOM{}
	if evs, err := d.Tick(context.Background(), Deps{}, cfg, now); err != nil || evs != nil {
		t.Errorf("kaynaksız: %v %v", evs, err)
	}
}

// Ayar: varsayılan açık, Settings › ML'den kapatılabilir.
func TestPodOOMSetting(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	d := NewPodOOM(nil, nil)
	if !chstore.MLDetectorDefaultOn(detPodOOM) || !d.Enabled(cfg) {
		t.Error("varsayılan açık olmalı")
	}
	cfg.Detectors[detPodOOM] = false
	if d.Enabled(cfg) {
		t.Error("kapatılabilmeli")
	}
}
