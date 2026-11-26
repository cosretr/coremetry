package ml

// pod_oom.go — v0.10.2476 (operatör isteği 2026-11-26, canlı davranış
// onaylı): OOMKilled pod Problem açar.
//
// KAYNAK: her etkin Remote Cluster'ın kube-state-metrics serileri (ML'nin
// Thanos/VM işçi girişi thanos.Service.WorkerQuery; cluster etiketi ve
// NamespaceFilter kalkanı orada), yalnız ANLIK sorgu:
//   - restart sayacı kube_pod_container_status_restarts_total;
//   - son sonlanma nedeni kube_pod_container_status_last_terminated_reason
//     {reason="OOMKilled"} — YAPIŞKAN: kill'den çok sonra da 1 kalır, tek
//     başına ASLA tetiklemez;
//   - bellek limiti kube_pod_container_resource_limits{resource="memory"}.
//
// KURAL: YENİ OOM kill = restart sayacı son tik penceresinde arttı VE son
// sonlanma nedeni OOMKilled (aynı namespace/pod/container). Cluster başına
// tik başına 1 sorgu (oomKillsExpr, topk ile sınırlı); yalnız sonuç varsa
// ikinci sorgu (oomExtrasExpr: 15 dk + 30 dk sayım + limit, etkilenen
// namespace'lerle sınırlı, her kol topk).
//
// GRUPLAMA: pod → iş yükü → servis ENTITY modelinden (canlı dizin,
// entity.LiveView: pod'un wl: ebeveyni ve runs kenarları); ad eşlemesi YOK.
// İş yükü başına tik başına TEK olay (etkilenen pod/container'lar, sayımlar
// kanıtta). Pod dizinde yoksa ya da iş yükü / servis çözülemezse olay
// yazılmaz, "ölçülemedi" notunda sayılır.
//
// PROBLEM: olay servis düzeyi dedektördür (svc_shadow.go isSvcDetector):
// servisin tek anomaly-auto:svc:<svc> Problem'ine üye olur; servis düzeyi
// olay yoksa Problem'i kendisi açar ve manşet chstore.MLPodOOMSentence.
// Etki kuralı ailesi gibi yayın kapılarından muaf (quality.go impactRuleRun).
// Koşu: aynı iş yükünde yeni kill geldikçe aynı kimlik uzar; episodeCloseMisses
// tik kill yoksa kapanır.
//
// ÖNCELİK: tek tanım chstore.OOMPriority — P2; 30 dk'da ≥ 3 kill ya da (iş
// yükünün ≥ 2 pod'u varken) pod'ların ≥ %50'si 15 dk'da OOMKilled → P1.
// Problem: Metric oom_kills, Value = 30 dk kill, Threshold = pod sayısı
// (oran değil); okuma anında computePriority kendi OOM dalından okur,
// gerekçe gerçek nedeni söyler ("30 dk'da 4 kez OOMKilled").

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	detPodOOM = chstore.MLDetPodOOM
	// oomMaxRows — yeni kill sorgusunun topk tavanı (cluster × tik).
	oomMaxRows = 200
	// oomMaxExtraRows — ek sorgunun kol başına topk tavanı.
	oomMaxExtraRows = 500
	// oomMaxNamespaces — ek sorgunun namespace süzgecinde en çok ad.
	oomMaxNamespaces = 50
	// oomMaxWorkloads — tik başına en çok olay (cluster'lar toplamı).
	oomMaxWorkloads = 200
	// oomMaxPodsEvidence — kanıtta listelenen en çok pod/container.
	oomMaxPodsEvidence = 20
	// oomRepeatMin — 30 dk sayım penceresi (kural chstore.OOMPriority).
	oomRepeatMin = chstore.OOMRepeatWindowMin
	// oomMinWindowSec — yeni kill penceresinin tabanı; pencere tik
	// aralığı + 60 sn (geç tik kill kaçırmasın; çift sayım yalnız koşuyu uzatır).
	oomMinWindowSec = 120
)

// oomLive — entity canlı dizini (entity.LiveIndex; sahte ile sınanır).
type oomLive interface {
	View(ctx context.Context, cid string) (*entity.LiveView, bool)
}

// PodOOM — dedektör (adı metric_ önekli: tik ölçümünde metrik türü; olay
// dedektörü pod_oom).
type PodOOM struct {
	th   ThanosQuerier
	live oomLive

	mu   sync.Mutex
	note string
}

// NewPodOOM — th nil (Remote Cluster yok) ya da live nil ise tik sessizdir.
func NewPodOOM(th ThanosQuerier, live *entity.LiveIndex) *PodOOM {
	d := &PodOOM{th: th}
	if live != nil {
		d.live = live
	}
	return d
}

func (d *PodOOM) Name() string { return "metric_pod_oom" }

func (d *PodOOM) Enabled(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detPodOOM) }

// Unmeasured — tik durumunda "ölçülemedi" notu (dedektör → neden).
func (d *PodOOM) Unmeasured() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.note == "" {
		return nil
	}
	return map[string]string{detPodOOM: d.note}
}

// ── sorgular (SAF) ────────────────────────────────────────────────────────

// promStr — SAF: PromQL çift tırnaklı dize kaçışı.
func promStr(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// oomSel — SAF: seçici gövdesi + cluster NamespaceFilter kalkanı + ek matcher.
func oomSel(metric, matchers, nsm, extra string) string {
	return metric + "{" + matchers + nsm + extra + "}"
}

// oomKilledExpr — SAF: penceredeki restart artışı, son sonlanma nedeni
// OOMKilled olan container'larla sınırlı (namespace, pod, container).
func oomKilledExpr(win, nsm, extra string) string {
	reason := oomSel("kube_pod_container_status_last_terminated_reason", `reason="OOMKilled"`, nsm, extra)
	restarts := oomSel("kube_pod_container_status_restarts_total", `container!=""`, nsm, extra)
	return "round(max by (namespace, pod, container) (increase(" + restarts + "[" + win + "]))) > 0" +
		" and on (namespace, pod, container) (max by (namespace, pod, container) (" + reason + ") == 1)"
}

// oomKillsExpr — SAF: son pencerede YENİ OOM kill'ler (tetik sorgusu).
func oomKillsExpr(winSec int, nsm string) string {
	if winSec < oomMinWindowSec {
		winSec = oomMinWindowSec
	}
	return fmt.Sprintf("topk(%d, %s)", oomMaxRows, oomKilledExpr(fmt.Sprintf("%ds", winSec), nsm, ""))
}

// oomExtrasExpr — SAF: etkilenen namespace'lerde 15 dk + 30 dk kill sayımı
// ve OOMKilled container'ların bellek limiti; kol `w` etiketinde (15 | 30 | lim).
func oomExtrasExpr(namespaces []string, nsm string) string {
	ns := append([]string(nil), namespaces...)
	sort.Strings(ns)
	if len(ns) > oomMaxNamespaces {
		ns = ns[:oomMaxNamespaces]
	}
	q := make([]string, len(ns))
	for i, n := range ns {
		q[i] = regexp.QuoteMeta(n)
	}
	extra := `,namespace=~"` + promStr(strings.Join(q, "|")) + `"`
	reason := oomSel("kube_pod_container_status_last_terminated_reason", `reason="OOMKilled"`, nsm, extra)
	limits := oomSel("kube_pod_container_resource_limits", `resource="memory"`, nsm, extra)
	arm := func(expr, w string) string {
		return fmt.Sprintf(`label_replace(topk(%d, %s), "w", "%s", "", "")`, oomMaxExtraRows, expr, w)
	}
	return arm(oomKilledExpr(fmt.Sprintf("%dm", chstore.MLPodOOMWindowMin), nsm, extra), "15") + " or " +
		arm(oomKilledExpr(fmt.Sprintf("%dm", oomRepeatMin), nsm, extra), "30") + " or " +
		arm("max by (namespace, pod, container) ("+limits+") and on (namespace, pod, container) (max by (namespace, pod, container) ("+reason+") == 1)", "lim")
}

// ── gruplama ve karar (SAF) ──────────────────────────────────────────────

// oomRow — anlık vektörün bir satırı.
type oomRow struct {
	NS, Pod, Ctr, W string
	V               float64
}

func oomRows(s []ThanosSample) []oomRow {
	out := make([]oomRow, 0, len(s))
	for _, x := range s {
		if x.Labels["namespace"] == "" || x.Labels["pod"] == "" || math.IsNaN(x.Value) || math.IsInf(x.Value, 0) {
			continue
		}
		out = append(out, oomRow{NS: x.Labels["namespace"], Pod: x.Labels["pod"], Ctr: x.Labels["container"], W: x.Labels["w"], V: x.Value})
	}
	return out
}

// oomPod — kanıttaki etkilenen pod/container.
type oomPod struct {
	Namespace string  `json:"namespace"`
	Pod       string  `json:"pod"`
	Container string  `json:"container,omitempty"`
	Kills     int     `json:"kills"`
	LimitMi   float64 `json:"limitMi,omitempty"`
}

// oomWorkload — iş yükü başına tik kararı.
type oomWorkload struct {
	ClusterID, WorkloadID, Kind, Namespace, Name, Service string
	Pods                                                  []oomPod
	KilledPods, TotalPods                                 int
	NewKills, Kills15, Kills30                            int
	LimitMi                                               float64
	Priority, Reason                                      string
}

// oomPriority — SAF: chstore.OOMPriority (tek tanım) sarmalayıcısı.
func oomPriority(killedPods, totalPods, kills30 int) (string, string) {
	p, why, _ := chstore.OOMPriority(killedPods, totalPods, kills30)
	return p, why
}

// oomWorkloadService — SAF: iş yükünün servisi = pod'larının span payı
// toplamında baskın servis (eşitlikte alfabetik ilk; entity runs kenarları).
func oomWorkloadService(pods []entity.LivePod) string {
	spans := map[string]int64{}
	for _, p := range pods {
		for _, s := range p.Services {
			if s.Name != "" {
				spans[s.Name] += s.Spans
			}
		}
	}
	best := ""
	for s, n := range spans {
		if best == "" || n > spans[best] || (n == spans[best] && s < best) {
			best = s
		}
	}
	return best
}

// oomGroup — SAF: yeni kill satırları + ek satırlar + canlı dizin pod'ları →
// iş yükü başına karar (servis, iş yükü kimliği sıralı; en çok
// oomMaxWorkloads). unresolved: dizinde olmayan ya da iş yükü / servisi
// çözülemeyen kill'li pod sayısı.
func oomGroup(cid string, fresh, extras []oomRow, live []entity.LivePod) (out []oomWorkload, unresolved int) {
	byPod := make(map[string]int, len(live))
	for i, p := range live {
		byPod[p.Namespace+"/"+p.Name] = i
	}
	wlOf := func(ns, pod string) (string, bool) {
		i, ok := byPod[ns+"/"+pod]
		if !ok || !strings.HasPrefix(live[i].Workload, "wl:") {
			return "", false
		}
		return live[i].Workload, true
	}
	type acc struct {
		w    *oomWorkload
		ctrs map[string]*oomPod // ns/pod/ctr
		k15  map[string]int
		k30  map[string]int
		lim  map[string]float64
	}
	by := map[string]*acc{}
	badPods := map[string]bool{}
	for _, r := range fresh {
		wl, ok := wlOf(r.NS, r.Pod)
		if !ok {
			badPods[r.NS+"/"+r.Pod] = true
			continue
		}
		a := by[wl]
		if a == nil {
			a = &acc{w: &oomWorkload{ClusterID: cid, WorkloadID: wl}, ctrs: map[string]*oomPod{}, k15: map[string]int{}, k30: map[string]int{}, lim: map[string]float64{}}
			by[wl] = a
		}
		k := r.NS + "/" + r.Pod + "/" + r.Ctr
		n := int(math.Round(r.V))
		a.w.NewKills += n
		if c := a.ctrs[k]; c != nil {
			c.Kills = max(c.Kills, n)
		} else {
			a.ctrs[k] = &oomPod{Namespace: r.NS, Pod: r.Pod, Container: r.Ctr, Kills: n}
		}
	}
	for _, r := range extras {
		wl, ok := wlOf(r.NS, r.Pod)
		if !ok || by[wl] == nil {
			continue
		}
		a := by[wl]
		k := r.NS + "/" + r.Pod + "/" + r.Ctr
		switch r.W {
		case "15":
			a.k15[k] = int(math.Round(r.V))
		case "30":
			a.k30[k] = int(math.Round(r.V))
		case "lim":
			a.lim[k] = r.V / (1 << 20)
		}
	}
	for wl, a := range by {
		ref, _ := entity.ParseID(wl)
		var wpods []entity.LivePod
		for _, p := range live {
			if entity.SameWorkload(p.Workload, wl) {
				wpods = append(wpods, p)
			}
		}
		svc := oomWorkloadService(wpods)
		if svc == "" {
			for _, c := range a.ctrs {
				badPods[c.Namespace+"/"+c.Pod] = true
			}
			continue
		}
		w := a.w
		w.Kind, w.Namespace, w.Name, w.Service, w.TotalPods = ref.Kind, ref.Namespace, ref.Name, svc, len(wpods)
		// 15 dk kümesi: ek sorgunun satırları ∪ yeni kill'ler (sayım en az yeni kill kadar)
		for k, n := range a.k15 {
			if a.ctrs[k] == nil {
				parts := strings.SplitN(k, "/", 3)
				a.ctrs[k] = &oomPod{Namespace: parts[0], Pod: parts[1], Container: parts[2]}
			}
			a.ctrs[k].Kills = max(a.ctrs[k].Kills, n)
		}
		pods := map[string]bool{}
		for k, c := range a.ctrs {
			w.Kills15 += c.Kills
			pods[c.Namespace+"/"+c.Pod] = true
			if l := a.lim[k]; l > 0 {
				c.LimitMi = l
				w.LimitMi = math.Max(w.LimitMi, l)
			}
		}
		for _, n := range a.k30 {
			w.Kills30 += n
		}
		w.Kills30 = max(w.Kills30, w.Kills15)
		w.KilledPods = len(pods)
		if w.TotalPods < w.KilledPods {
			w.TotalPods = w.KilledPods
		}
		for _, c := range a.ctrs {
			w.Pods = append(w.Pods, *c)
		}
		sort.Slice(w.Pods, func(i, j int) bool {
			if w.Pods[i].Kills != w.Pods[j].Kills {
				return w.Pods[i].Kills > w.Pods[j].Kills
			}
			if w.Pods[i].Pod != w.Pods[j].Pod {
				return w.Pods[i].Pod < w.Pods[j].Pod
			}
			return w.Pods[i].Container < w.Pods[j].Container
		})
		if len(w.Pods) > oomMaxPodsEvidence {
			w.Pods = w.Pods[:oomMaxPodsEvidence]
		}
		w.Priority, w.Reason = oomPriority(w.KilledPods, w.TotalPods, w.Kills30)
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].WorkloadID < out[j].WorkloadID
	})
	if len(out) > oomMaxWorkloads {
		out = out[:oomMaxWorkloads]
	}
	return out, len(badPods)
}

// oomEvidence — kanıt alanları (svcValueThreshold / cümle buradan okur).
type oomEvidence struct {
	Priority   string  `json:"priority"`
	KilledPods int     `json:"killedPods"`
	TotalPods  int     `json:"totalPods"`
	Kills30    int     `json:"kills30"`
	LimitMi    float64 `json:"limitMi"`
}

func oomEvidenceOf(e chstore.MLEvent) oomEvidence {
	var ev oomEvidence
	_ = json.Unmarshal([]byte(e.Evidence), &ev)
	return ev
}

// oomEvent — SAF: iş yükünün olayı. Gözlenen = 15 dk kill sayısı, taban 0.
func oomEvent(w oomWorkload, cfg chstore.MLConfig, now time.Time) chstore.MLEvent {
	ev := map[string]any{"source": "kube-state-metrics", "cluster": w.ClusterID, "workload": w.WorkloadID, "workloadKind": w.Kind,
		"namespace": w.Namespace, "pods": w.Pods, "killedPods": w.KilledPods, "totalPods": w.TotalPods, "newKills": w.NewKills,
		"kills15": w.Kills15, "kills30": w.Kills30, "windowMin": chstore.MLPodOOMWindowMin, "repeatMin": oomRepeatMin,
		"limitMi": w.LimitMi, "priority": w.Priority, "priorityReason": w.Reason, "rule": "oom"}
	raw, _ := json.Marshal(ev)
	pod := ""
	if len(w.Pods) > 0 {
		pod = w.Pods[0].Pod
	}
	return chstore.MLEvent{
		ID: chstore.MLEventID(detPodOOM, w.Service, w.WorkloadID), Detector: detPodOOM, Signal: chstore.MLSignalMetric, Stage: chstore.MLStageShadow,
		Service: w.Service, Code: w.Name, Pod: pod, StartedAt: now.UnixNano(), LastSeen: now.UnixNano(), WindowSec: uint32(cfg.TickSec),
		Baseline: 0, Observed: float64(w.Kills15), Score: cfg.CriticalZ, Direction: "up", Evidence: string(raw),
	}
}

// oomValueThreshold — SAF: Problem Value = 30 dk kill, Threshold = iş
// yükünün pod sayısı (chstore.MLOOMMetric; oran değil, öncelik kendi dalında).
func oomValueThreshold(e chstore.MLEvent) (float64, float64) {
	ev := oomEvidenceOf(e)
	k30 := max(float64(ev.Kills30), e.Observed, 1)
	return k30, float64(max(ev.TotalPods, ev.KilledPods, 1))
}

// oomSentence — SAF: Problem cümlesi (tek tanım chstore.MLPodOOMSentence).
func oomSentence(e chstore.MLEvent) string {
	ev := oomEvidenceOf(e)
	return chstore.MLPodOOMSentence(e.Code, max(ev.KilledPods, 1), int(math.Round(e.Observed)), ev.LimitMi)
}

// ── tik ──────────────────────────────────────────────────────────────────

func (d *PodOOM) setNote(s string) {
	d.mu.Lock()
	d.note = s
	d.mu.Unlock()
}

func (d *PodOOM) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	if d.th == nil || d.live == nil {
		d.setNote("Remote Cluster ya da entity canlı dizini yok: OOMKilled ölçülmüyor")
		return nil, nil
	}
	cls := d.th.Clusters()
	if len(cls) == 0 {
		d.setNote("etkin Remote Cluster yok: OOMKilled ölçülmüyor")
		return nil, nil
	}
	var (
		mu         sync.Mutex
		all        []oomWorkload
		failed     []string
		unresolved int
	)
	forClusters(ctx, cls, func(cctx context.Context, c ThanosCluster) {
		ws, bad, err := d.cluster(cctx, c, cfg, now)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			failed = append(failed, c.Name)
			return
		}
		all = append(all, ws...)
		unresolved += bad
	})
	sort.Strings(failed)
	var notes []string
	if len(failed) > 0 {
		notes = append(notes, "sorgu hatası: "+strings.Join(failed, ", "))
	}
	if unresolved > 0 {
		notes = append(notes, fmt.Sprintf("%d OOMKilled pod iş yükü/servise çözülemedi", unresolved))
	}
	d.setNote(strings.Join(notes, "; "))
	if len(failed) == len(cls) {
		return nil, fmt.Errorf("pod_oom: tüm cluster sorguları başarısız (%s)", strings.Join(failed, ", "))
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Service != all[j].Service {
			return all[i].Service < all[j].Service
		}
		return all[i].WorkloadID < all[j].WorkloadID
	})
	if len(all) > oomMaxWorkloads {
		all = all[:oomMaxWorkloads]
	}
	out := make([]chstore.MLEvent, 0, len(all))
	for _, w := range all {
		out = append(out, oomEvent(w, cfg, now))
	}
	return out, nil
}

// cluster — tek cluster: tetik sorgusu, gerekirse ek sorgu + canlı dizin.
func (d *PodOOM) cluster(ctx context.Context, c ThanosCluster, cfg chstore.MLConfig, now time.Time) ([]oomWorkload, int, error) {
	nsm := thanos.NamespaceMatcher(c.NamespaceFilter)
	s, err := d.th.Instant(ctx, c.ID, oomKillsExpr(cfg.TickSec+60, nsm), now)
	if err != nil {
		return nil, 0, err
	}
	fresh := oomRows(s)
	if len(fresh) == 0 {
		return nil, 0, nil
	}
	nsSet := map[string]bool{}
	for _, r := range fresh {
		nsSet[r.NS] = true
	}
	nss := make([]string, 0, len(nsSet))
	for n := range nsSet {
		nss = append(nss, n)
	}
	var extras []oomRow
	if x, xerr := d.th.Instant(ctx, c.ID, oomExtrasExpr(nss, nsm), now); xerr == nil { // hata: sayım yeni kill'den
		extras = oomRows(x)
	}
	var live []entity.LivePod
	if v, ok := d.live.View(ctx, c.ID); ok && v != nil {
		live = v.Pods
	}
	ws, bad := oomGroup(c.ID, fresh, extras, live)
	return ws, bad, nil
}
