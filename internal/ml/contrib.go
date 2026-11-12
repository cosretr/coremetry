package ml

// contrib.go — v0.10.2037 (anomali tekilleştirme: "suçlu seri listede ve
// cümlede"). Metrik olayı AÇILIRKEN bir kez (olay süresince en çok bir kez
// daha: tepe 1,5 katından fazla büyürse) dedektörün ifadesinin etiket
// kırılımlı hâliyle TEK anlık sorgu; sonuç olayın kanıtına (`contrib`) yazılır:
// ad + değer + toplam içindeki pay + seri sayısı. Sunucu cümlesi
// (ExplainSummary) ve liste "Ne oldu" bu kanıttan okur; liste ek sorgu atmaz.
//
// Kırılım etiketi dedektöre göre:
//   metric_kafka (VM ve Thanos)  → client_id (Messaging panelinin kırılımı)
//   diğer metrik aileleri, VM    → etiket haritasının pod etiketi (yoksa sorgu yok)
//   diğer metrik aileleri, Thanos → pod (servisin pod regex'iyle)
//   dependency_latency           → sorgu yok: olayın öznesi zaten hedef
// VM sorgusu ailenin VM bütçe/backoff kapısından (vmInstant) geçer; Thanos
// sorgusu işçi sınırlarıyla. Sorgu başarısızsa olay yine yazılır, ad boş.
//
// "Belirgin" kuralı (FE topContributors'ın aynısı, SAF): en büyük ≥ toplamın
// %60'ı → tek ad; değilse sıfır olmayanlar arasında en büyük, medyanın 1,5
// katından küçükse "N seriye yayılmış"; değilse ilk 3.

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	contribMaxQueries = 2   // olay başına (açılış + en çok bir tepe güncellemesi)
	contribPeakGrowth = 1.5 // tepe bu kattan fazla büyürse bir kez daha
	contribMaxRows    = 500 // kırılım satırı tavanı (fazlası seri sayısına girer, sıralamaya girmez)
)

// Contrib — olayın suçlu seri kanıtı (evidence.contrib).
type Contrib struct {
	Kind   string        `json:"kind"` // single | top | spread
	Items  []ContribItem `json:"items,omitempty"`
	Series int           `json:"series"` // kırılımdaki seri sayısı
	Active int           `json:"active"` // sıfırdan büyük değerli seri sayısı
	Label  string        `json:"label"`
	Peak   float64       `json:"peak"` // sorgu anındaki olay değeri (tepe güncelleme kapısı)
	N      int           `json:"n"`    // bu olay için atılan sorgu sayısı
}

// ContribItem — tek seri.
type ContribItem struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Share float64 `json:"share"` // 0..1, toplam içindeki pay
}

type namedVal struct {
	name string
	v    float64
}

// pickContributors — SAF: kırılım değerleri → belirgin seri(ler). ok=false:
// toplam sıfır / boş.
func pickContributors(vals []namedVal, label string) (Contrib, bool) {
	var nz []namedVal
	total := 0.0
	for _, x := range vals {
		if x.name == "" || math.IsNaN(x.v) || math.IsInf(x.v, 0) || x.v <= 0 {
			continue
		}
		nz = append(nz, x)
		total += x.v
	}
	if len(nz) == 0 || total <= 0 {
		return Contrib{}, false
	}
	sort.Slice(nz, func(i, j int) bool { return nz[i].v > nz[j].v || (nz[i].v == nz[j].v && nz[i].name < nz[j].name) })
	item := func(x namedVal) ContribItem { return ContribItem{Name: x.name, Value: x.v, Share: x.v / total} }
	c := Contrib{Series: len(vals), Active: len(nz), Label: label}
	if nz[0].v >= 0.6*total {
		c.Kind, c.Items = "single", []ContribItem{item(nz[0])}
		return c, true
	}
	asc := make([]float64, len(nz))
	for i, x := range nz {
		asc[len(nz)-1-i] = x.v
	}
	med := asc[(len(asc)-1)/2]
	if len(nz) > 1 && nz[0].v < 1.5*med {
		c.Kind = "spread"
		return c, true
	}
	c.Kind = "top"
	for i := 0; i < len(nz) && i < 3; i++ {
		c.Items = append(c.Items, item(nz[i]))
	}
	return c, true
}

// contribLabel — SAF: dedektör + parça → kırılım etiketi ("" = sorgu yok).
func contribLabel(detector, code, vmPodLabel string) string {
	if !strings.HasPrefix(detector, "metric_") {
		return ""
	}
	if detector == "metric_kafka" {
		if partTopic(code) != "" { // v0.10.2064: topic'li olayda suçlu zaten topic; servis düzeyi kırılım yanıltır
			return ""
		}
		return "client_id"
	}
	if _, _, cluster := splitThanosPart(code); cluster != "" {
		return "pod"
	}
	return vmPodLabel
}

// contribQuery — SAF: kırılımlı anlık ifade. Pencere dedektörün kovası
// (olayın değeriyle aynı ölçü). ok=false: ifade kurulamaz.
func contribQuery(cfg chstore.MLConfig, ev chstore.MLEvent, svcLabel, label string) (expr, cluster string, ok bool) {
	fam, id, cluster := splitThanosPart(ev.Code)
	if fam != ev.Detector || label == "" || ev.Service == "" {
		return "", "", false
	}
	for _, e := range cfg.MetricFamilies[fam] {
		if e.ID != id {
			continue
		}
		win := int(ev.WindowSec)
		if win <= 0 {
			win = cfg.BucketSec
		}
		x := renderExpr(e.Expr, label, win)
		if cluster != "" {
			return thanos.WithRegexMatcher(x, "pod", thanos.ServicePodRegex(ev.Service, "")), cluster, true
		}
		if svcLabel == "" {
			svcLabel = "service_name"
		}
		return thanos.WithEqualMatcher(x, svcLabel, ev.Service), "", true
	}
	return "", "", false
}

// needContrib — SAF: bu tikte sorgu atılır mı.
func needContrib(prev *Contrib, observed float64) bool {
	if prev == nil {
		return true
	}
	return prev.N < contribMaxQueries && prev.Peak > 0 && observed > contribPeakGrowth*prev.Peak
}

// evidenceContrib — SAF: kanıttaki contrib (yoksa nil).
func evidenceContrib(raw string) *Contrib {
	m := evidenceMap(raw)
	c, ok := m["contrib"].(map[string]any)
	if !ok {
		return nil
	}
	out := &Contrib{Kind: evStr(c, "kind"), Label: evStr(c, "label")}
	if v, ok := evNum(c, "series"); ok {
		out.Series = int(v)
	}
	if v, ok := evNum(c, "active"); ok {
		out.Active = int(v)
	}
	if v, ok := evNum(c, "peak"); ok {
		out.Peak = v
	}
	if v, ok := evNum(c, "n"); ok {
		out.N = int(v)
	}
	if items, ok := c["items"].([]any); ok {
		for _, it := range items {
			if im, ok := it.(map[string]any); ok {
				v, _ := evNum(im, "value")
				sh, _ := evNum(im, "share")
				out.Items = append(out.Items, ContribItem{Name: evStr(im, "name"), Value: v, Share: sh})
			}
		}
	}
	return out
}

// errContribSkipped — kaynak beklemede (backoff) ya da cluster yok: sorgu atılmadı.
var errContribSkipped = errors.New("contrib: kaynak beklemede ya da yok")

// contribSource — kırılım sorgusunun kaynakları (recorder bağlar). Thanos
// sorgusu MetricThanos'un backoff'unu (cluster|aile anahtarı) paylaşır.
type contribSource struct {
	thanos  ThanosQuerier
	backoff *vmBackoff
}

// enrichContributors — kept içindeki metrik olaylarına contrib yazar; prev:
// epizot kimliği → önceki tikte yazılan hal. Döner: atılan sorgu sayısı.
func (cs contribSource) enrichContributors(ctx context.Context, deps Deps, cfg chstore.MLConfig, kept []chstore.MLEvent, prev func(string) (chstore.MLEvent, bool), now time.Time) int {
	if deps.VM == nil && cs.thanos == nil {
		return 0
	}
	podLabel, svcLabel := "", ""
	if deps.VM != nil {
		lm := deps.VM.MetricLabelMap()
		podLabel, svcLabel = lm.Pod, lm.Service
	}
	queries := 0
	for i := range kept {
		ev := &kept[i]
		if ev.Signal != chstore.MLSignalMetric || ev.Stage == chstore.MLStageClosed {
			continue
		}
		var old *Contrib
		if p, ok := prev(ev.ID); ok {
			old = evidenceContrib(p.Evidence)
		}
		if !needContrib(old, ev.Observed) {
			if old != nil {
				ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"contrib": old})
			}
			continue
		}
		label := contribLabel(ev.Detector, ev.Code, podLabel)
		expr, cluster, ok := contribQuery(cfg, *ev, svcLabel, label)
		if !ok {
			continue
		}
		at := time.Unix(0, ev.LastSeen)
		if at.After(now) || at.IsZero() {
			at = now
		}
		vals, qerr := cs.contribValues(ctx, deps, ev.Detector, expr, cluster, label, at, now)
		if errors.Is(qerr, errContribSkipped) {
			if old != nil {
				ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"contrib": old})
			}
			continue // sorgu atılmadı: deneme sayılmaz, sonraki tik yeniden dener
		}
		queries++
		n := 1
		if old != nil {
			n = old.N + 1
		}
		if qerr != nil {
			if old != nil { // eski ad kalır; deneme sayılır
				old.N = n
				ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"contrib": old})
			}
			continue
		}
		c, ok := pickContributors(vals, label)
		if !ok {
			continue
		}
		c.Peak, c.N = ev.Observed, n
		ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"contrib": c})
	}
	return queries
}

func (cs contribSource) contribValues(ctx context.Context, deps Deps, det, expr, cluster, label string, at, now time.Time) ([]namedVal, error) {
	var out []namedVal
	if cluster == "" {
		if deps.VM == nil || !deps.VM.Configured() {
			return nil, errContribSkipped
		}
		rows, skip, err := vmInstant(ctx, deps, "contrib:"+det, expr, at, now)
		if err != nil {
			return nil, err
		}
		if skip {
			return nil, errContribSkipped
		}
		for _, r := range rows {
			if v, ok := instantValue(r); ok && len(out) < contribMaxRows {
				out = append(out, namedVal{name: groupKeyLabels(r.GroupKey)[label], v: v})
			}
		}
		return out, nil
	}
	if cs.thanos == nil {
		return nil, errContribSkipped
	}
	id := ""
	for _, c := range cs.thanos.Clusters() {
		if c.Name == cluster {
			id = c.ID
		}
	}
	if id == "" {
		return nil, errContribSkipped
	}
	key := cluster + "|" + det
	if cs.backoff != nil && cs.backoff.skipping(key, now) {
		return nil, errContribSkipped
	}
	rows, err := cs.thanos.Instant(ctx, id, expr, at)
	if err != nil {
		if cs.backoff != nil && heavyVMError(err) {
			cs.backoff.fail(key, err, now)
		}
		return nil, err
	}
	if cs.backoff != nil {
		cs.backoff.ok(key)
	}
	for _, r := range rows {
		if len(out) < contribMaxRows {
			out = append(out, namedVal{name: r.Labels[label], v: r.Value})
		}
	}
	return out, nil
}
