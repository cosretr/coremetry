package mcptools

// trace_tools.go — v0.10.944 (CoSRE araştırma asistanı): get_trace'in
// yükseltilmiş hâli. Entegrasyonda ToolList'teki getTraceTool(d) bununla
// (getTraceAnalyzedTool) değişir; ad ("get_trace") ve 200 span tavanı aynı.
//
// Neden: eski get_trace modele yalnız ham span listesi veriyordu ve model
// "en yavaş bileşen trace'in %X'ini tüketti" cümlesini span sürelerini
// TOPLAYARAK kuruyordu — iç içe (çocuk ebeveynin içinde) ve paralel span'lerde
// bu aynı anı iki-üç kez sayar. Artık sunucu chstore.BuildTraceAnalysis'in
// (Trace sayfasının kullandığı TEK tanım) sayılarını verir:
//
//   - öz süre = span süresi − doğrudan çocukların ARALIK BİRLEŞİMİ (paralel
//     çocuk iki kez düşülmez, negatif olmaz);
//   - kritik yol her adımın YOLDA tek başına geçen süresiyle
//     (self_on_path_ms) gelir; "kritik yol toplamı" diye bir sayı YOK — yolun
//     duvar saati kökün süresidir (wall_ms);
//   - servis bağlamı (env/cluster/namespace/pod/sürüm) resource
//     özniteliklerinden, ClickHouse kolonlarının türetildiği AYNI anahtarlarla.
//
// Analiz, 200'lük liste tavanından ÖNCE, okunan TÜM span'ler üzerinden koşar:
// liste kırpılsa da servis/kritik yol sayıları eksik kalmaz.
//
// v0.10.944 (inceleme) — Distributed okuma 0 span döndüğünde "yok" demeden
// önce REST trace ucunun (trace_routes.go) yolu izlenir: trace_summary_5m
// özeti → ham TTL içindeyse tüm replikalardan yedek okuma (replika
// ıraksaması) → yoksa aged_out. Eskiden Trace sayfasında açılan bir trace
// modele "saklanmıyor" diye bildiriliyordu. Karar saf traceToolMissPayload'da.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// traceStoreSpanCap — chstore.GetTrace'in SQL tavanı (repo.go `LIMIT 50000`).
// Okunan span sayısı buna eşitse trace daha büyüktür; analiz ilk 50k üzerindendir.
const traceStoreSpanCap = 50000

// Analiz liste tavanları — bağlam bütçesi (tool açıklaması da ilan eder).
const (
	traceToolTopSelf     = 10
	traceToolErrorSpans  = 10
	traceToolServices    = 20
	traceToolPathSteps   = 25
	traceToolPods        = 10
	traceToolVersions    = 5
	traceToolStatusRunes = 300
)

// traceToolNoteCPU — sabit dürüstlük notu (spec metni birebir).
const traceToolNoteCPU = "Süre CPU tüketimi değildir; profiling verisi yok — metot CPU/allocation dağılımı çıkarılamaz."

type traceToolArgs struct {
	TraceID string `json:"trace_id"`
}

// traceToolTraceID — SAF: 32 hex, küçük harfe normalize. Hata metni
// "geçersiz"/"zorunlu" taşır → mcp toolerr bad_args sınıfı.
func traceToolTraceID(raw string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return "", fmt.Errorf("trace_id zorunlu (32 hex karakter)")
	}
	if !isHexLen(id, 32) {
		return "", fmt.Errorf("trace_id geçersiz: 32 hex karakter olmalı (gelen %q)", raw)
	}
	return id, nil
}

type traceToolSpanRef struct {
	SpanID     string  `json:"span_id"`
	Service    string  `json:"service"`
	Name       string  `json:"name"`
	DurationMs float64 `json:"duration_ms"`
}

type traceToolService struct {
	Service string  `json:"service"`
	SelfMs  float64 `json:"self_ms"`
	SelfPct float64 `json:"self_pct"` // toplam ÖZ sürenin payı, duvar saatinin değil
	Spans   uint32  `json:"spans"`
	Errors  uint32  `json:"errors"`
}

type traceToolSelfSpan struct {
	SpanID     string  `json:"span_id"`
	Service    string  `json:"service"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind,omitempty"`
	SelfMs     float64 `json:"self_ms"`
	DurationMs float64 `json:"duration_ms"`
	Error      bool    `json:"error,omitempty"`
}

type traceToolErrorSpan struct {
	SpanID        string  `json:"span_id"`
	Service       string  `json:"service"`
	Name          string  `json:"name"`
	StatusMessage string  `json:"status_message,omitempty"`
	StartISO      string  `json:"start_iso"`
	StartUnixNs   int64   `json:"start_unix_ns"`
	DurationMs    float64 `json:"duration_ms"`
}

type traceToolPathStep struct {
	SpanID       string  `json:"span_id"`
	Service      string  `json:"service"`
	Name         string  `json:"name"`
	SelfOnPathMs float64 `json:"self_on_path_ms"`
}

type traceToolContext struct {
	Service   string   `json:"service"`
	Env       string   `json:"env,omitempty"`
	Cluster   string   `json:"cluster,omitempty"`
	Namespace string   `json:"namespace,omitempty"`
	Pods      []string `json:"pods"`
	PodsTotal int      `json:"pods_total"`
	Versions  []string `json:"versions"`
}

// traceToolAnalysis — v0.10.944: Notes ÜSTTE (OrphanCount'tan hemen sonra):
// sohbet araç sonucunu 6000 rune'da kırpar (clampToolResultForModel); en
// sonda kalan dürüstlük notları (CPU≠süre, kritik yol toplanmaz, yetim span /
// eksik ağaç, 50k tavanı) büyük trace'te modele hiç ulaşmıyordu.
type traceToolAnalysis struct {
	WallMs            float64              `json:"wall_ms"`   // kök span süresi = kritik yolun duvar saati
	ExtentMs          float64              `json:"extent_ms"` // ilk başlangıç → son bitiş (async kuyruk dahil)
	StartISO          string               `json:"start_iso"`
	StartUnixNs       int64                `json:"start_unix_ns"`
	EndISO            string               `json:"end_iso"`
	EndUnixNs         int64                `json:"end_unix_ns"`
	Root              traceToolSpanRef     `json:"root"`
	SpanCount         int                  `json:"span_count"`
	ErrorSpanCount    int                  `json:"error_span_count"`
	OrphanCount       uint32               `json:"orphan_count"`
	Notes             []string             `json:"notes"`
	Services          []traceToolService   `json:"services"`
	ServicesTruncated bool                 `json:"services_truncated,omitempty"`
	TopSelfSpans      []traceToolSelfSpan  `json:"top_self_spans"`
	ErrorSpans        []traceToolErrorSpan `json:"error_spans"`
	CriticalPath      []traceToolPathStep  `json:"critical_path"`
	CriticalPathSteps int                  `json:"critical_path_steps"`
	Context           []traceToolContext   `json:"context"` // ≤ traceToolServices (v0.10.944)
}

func ttNsToMs(ns int64) float64 {
	if ns <= 0 {
		return 0
	}
	return float64(ns/100_000) / 10 // 1 ondalık, aşağı yuvarlı
}

func ttSpanDur(s *chstore.SpanRow) int64 {
	if s.EndTime <= s.StartTime {
		return 0
	}
	return s.EndTime - s.StartTime
}

func ttFirstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}

// ttSpanContext — SAF: bir span'in env/cluster/namespace/pod/sürümü.
// Anahtar zincirleri ClickHouse'un türettiği kolonlarla AYNI: deploy_env
// (otlp/convert.go: deployment.environment.name → deployment.environment),
// cluster (clusterDeriveExpr: resource önce, sonra span öznitelikleri),
// k8s_namespace / k8s_pod (promoted_attr.go; pod yoksa host_name),
// sürüm chstore.EffectiveVersion (deploys.go effectiveVersionExpr ikizi).
func ttSpanContext(s *chstore.SpanRow) (env, cluster, namespace, pod, version string) {
	res := s.ResourceAttributes
	env = ttFirstNonEmpty(res, "deployment.environment.name", "deployment.environment")
	cluster = ttFirstNonEmpty(res, "k8s.cluster.name", "openshift.cluster.name", "cluster")
	if cluster == "" {
		cluster = ttFirstNonEmpty(s.Attributes, "k8s.cluster.name", "openshift.cluster.name", "cluster")
	}
	namespace = ttFirstNonEmpty(res, "k8s.namespace.name", "kubernetes.namespace.name")
	pod = ttFirstNonEmpty(res, "k8s.pod.name")
	if pod == "" {
		pod = strings.TrimSpace(s.HostName)
	}
	version = chstore.EffectiveVersion(res)
	return
}

// ttCriticalPathSteps — SAF: yol adımlarının YOLDA geçen süresi. Adım i için
// süre(i) − (i+1'in i'ye kırpılmış aralığı); son adım kendi süresi. Toplamları
// kökün süresini aşamaz — ama bilerek TOPLANMAZ ve dışarı verilmez.
func ttCriticalPathSteps(ids []string, byID map[string]int, spans []chstore.SpanRow) []traceToolPathStep {
	out := make([]traceToolPathStep, 0, len(ids))
	for i, id := range ids {
		idx, ok := byID[id]
		if !ok {
			continue
		}
		s := &spans[idx]
		on := ttSpanDur(s)
		if i+1 < len(ids) {
			if nidx, ok := byID[ids[i+1]]; ok {
				n := &spans[nidx]
				a, b := n.StartTime, n.StartTime+ttSpanDur(n)
				if a < s.StartTime {
					a = s.StartTime
				}
				if end := s.StartTime + ttSpanDur(s); b > end {
					b = end
				}
				if b > a {
					on -= b - a
				}
			}
		}
		if on < 0 {
			on = 0
		}
		out = append(out, traceToolPathStep{SpanID: id, Service: s.ServiceName, Name: s.Name, SelfOnPathMs: ttNsToMs(on)})
	}
	return out
}

// ttTrimPathSteps — SAF: uzun yolu en büyük self_on_path adımlarıyla kırpar,
// YOL SIRASINI koruyarak (derin zincirde ilginç kısım dipte olabilir).
func ttTrimPathSteps(steps []traceToolPathStep, max int) []traceToolPathStep {
	if len(steps) <= max {
		return steps
	}
	idx := make([]int, len(steps))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return steps[idx[a]].SelfOnPathMs > steps[idx[b]].SelfOnPathMs })
	keep := idx[:max]
	sort.Ints(keep)
	out := make([]traceToolPathStep, 0, max)
	for _, i := range keep {
		out = append(out, steps[i])
	}
	return out
}

func ttSortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// buildTraceToolAnalysis — SAF: okunan TÜM span'ler → modele giden analiz.
// storeCapped: GetTrace SQL tavanına takıldı (trace daha büyük).
func buildTraceToolAnalysis(spans []chstore.SpanRow, storeCapped bool) traceToolAnalysis {
	ta := chstore.BuildTraceAnalysis(spans, storeCapped)
	out := traceToolAnalysis{
		SpanCount: len(spans), OrphanCount: ta.OrphanCount,
		Services: []traceToolService{}, TopSelfSpans: []traceToolSelfSpan{}, ErrorSpans: []traceToolErrorSpan{},
		CriticalPath: []traceToolPathStep{}, Context: []traceToolContext{}, Notes: []string{traceToolNoteCPU},
	}
	if len(spans) == 0 {
		return out
	}
	byID := make(map[string]int, len(spans))
	var minStart, maxEnd int64
	for i := range spans {
		s := &spans[i]
		if s.SpanID != "" {
			if _, dup := byID[s.SpanID]; !dup {
				byID[s.SpanID] = i
			}
		}
		end := s.StartTime + ttSpanDur(s)
		if i == 0 || s.StartTime < minStart {
			minStart = s.StartTime
		}
		if i == 0 || end > maxEnd {
			maxEnd = end
		}
	}
	out.StartUnixNs, out.EndUnixNs = minStart, maxEnd
	out.StartISO = time.Unix(0, minStart).UTC().Format(time.RFC3339Nano)
	out.EndISO = time.Unix(0, maxEnd).UTC().Format(time.RFC3339Nano)
	out.ExtentMs = ttNsToMs(maxEnd - minStart)
	if ri, ok := byID[ta.RootSpanID]; ok {
		r := &spans[ri]
		out.Root = traceToolSpanRef{SpanID: r.SpanID, Service: r.ServiceName, Name: r.Name, DurationMs: ttNsToMs(ttSpanDur(r))}
		out.WallMs = out.Root.DurationMs
	}

	for i, sv := range ta.Services {
		if i >= traceToolServices {
			out.ServicesTruncated = true
			break
		}
		out.Services = append(out.Services, traceToolService{
			Service: sv.Service, SelfMs: ttNsToMs(sv.SelfNs), SelfPct: float64(int(sv.SelfPct*10+0.5)) / 10,
			Spans: sv.SpanCount, Errors: sv.ErrorCount,
		})
	}

	nodes := append([]chstore.TraceNode(nil), ta.Nodes...)
	sort.SliceStable(nodes, func(a, b int) bool {
		if nodes[a].SelfNs != nodes[b].SelfNs {
			return nodes[a].SelfNs > nodes[b].SelfNs
		}
		return nodes[a].SpanID < nodes[b].SpanID
	})
	for _, n := range nodes {
		if len(out.TopSelfSpans) >= traceToolTopSelf || n.SelfNs <= 0 {
			break
		}
		s := &spans[byID[n.SpanID]]
		out.TopSelfSpans = append(out.TopSelfSpans, traceToolSelfSpan{
			SpanID: s.SpanID, Service: s.ServiceName, Name: s.Name, Kind: s.Kind,
			SelfMs: ttNsToMs(n.SelfNs), DurationMs: ttNsToMs(ttSpanDur(s)), Error: s.StatusCode == "error",
		})
	}

	errIdx := []int{}
	for i := range spans {
		if spans[i].StatusCode == "error" {
			errIdx = append(errIdx, i)
		}
	}
	out.ErrorSpanCount = len(errIdx)
	sort.SliceStable(errIdx, func(a, b int) bool { return spans[errIdx[a]].StartTime < spans[errIdx[b]].StartTime })
	for _, i := range errIdx {
		if len(out.ErrorSpans) >= traceToolErrorSpans {
			break
		}
		s := &spans[i]
		out.ErrorSpans = append(out.ErrorSpans, traceToolErrorSpan{
			SpanID: s.SpanID, Service: s.ServiceName, Name: s.Name,
			StatusMessage: ttCapRunes(s.StatusMessage, traceToolStatusRunes),
			StartISO:      time.Unix(0, s.StartTime).UTC().Format(time.RFC3339Nano), StartUnixNs: s.StartTime,
			DurationMs: ttNsToMs(ttSpanDur(s)),
		})
	}

	steps := ttCriticalPathSteps(ta.CriticalIDs, byID, spans)
	out.CriticalPathSteps = len(steps)
	out.CriticalPath = ttTrimPathSteps(steps, traceToolPathSteps)

	type ctxAcc struct {
		env, cluster, ns, pods, vers map[string]int
	}
	acc := map[string]*ctxAcc{}
	for i := range spans {
		s := &spans[i]
		a := acc[s.ServiceName]
		if a == nil {
			a = &ctxAcc{env: map[string]int{}, cluster: map[string]int{}, ns: map[string]int{}, pods: map[string]int{}, vers: map[string]int{}}
			acc[s.ServiceName] = a
		}
		env, cl, ns, pod, ver := ttSpanContext(s)
		for _, kv := range []struct {
			m map[string]int
			v string
		}{{a.env, env}, {a.cluster, cl}, {a.ns, ns}, {a.pods, pod}, {a.vers, ver}} {
			if kv.v != "" {
				kv.m[kv.v]++
			}
		}
	}
	for _, sv := range ta.Services { // servis sırası = öz süre sırası
		// v0.10.944 — services ile AYNI tavan; eskiden her servise bir satır.
		if len(out.Context) >= traceToolServices {
			out.ServicesTruncated = true
			break
		}
		a := acc[sv.Service]
		if a == nil {
			continue
		}
		pods, vers := ttSortedKeys(a.pods), ttSortedKeys(a.vers)
		c := traceToolContext{
			Service: sv.Service, Env: strings.Join(ttSortedKeys(a.env), ", "),
			Cluster: strings.Join(ttSortedKeys(a.cluster), ", "), Namespace: strings.Join(ttSortedKeys(a.ns), ", "),
			PodsTotal: len(pods), Pods: pods, Versions: vers,
		}
		if len(c.Pods) > traceToolPods {
			c.Pods = c.Pods[:traceToolPods]
		}
		if len(c.Versions) > traceToolVersions {
			c.Versions = c.Versions[:traceToolVersions]
		}
		out.Context = append(out.Context, c)
	}

	out.Notes = append(out.Notes,
		"Öz süre (self_ms) = span süresi − doğrudan çocukların ARALIK BİRLEŞİMİ; paralel çocuklar iki kez düşülmez. self_pct toplam öz sürenin payıdır, duvar saatinin değil.",
		"Kritik yol süreleri TOPLANMAZ: self_on_path_ms her adımın yolda tek başına geçen süresidir; yolun duvar saati wall_ms (kök süresi).")
	if out.CriticalPathSteps > len(out.CriticalPath) {
		out.Notes = append(out.Notes, fmt.Sprintf("Kritik yol %d adım; yalnız yolda en uzun kalan %d adım (yol sırasıyla) listelendi.", out.CriticalPathSteps, len(out.CriticalPath)))
	}
	if out.OrphanCount > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("%d yetim span var (ebeveyni bu trace'te yok) — ağaç eksik; eksik ebeveynin süresi hiçbir sayıda yok.", out.OrphanCount))
	}
	if out.ExtentMs > out.WallMs*1.01 && out.ExtentMs-out.WallMs >= 1 {
		out.Notes = append(out.Notes, "Trace kapsamı (extent_ms) kök süresinden uzun: kökten sonra biten async/consumer span'leri ya da ikinci bir kök var.")
	}
	if storeCapped {
		out.Notes = append(out.Notes, fmt.Sprintf("ClickHouse okuması %d span tavanına takıldı: trace daha büyük, analiz ilk %d span üzerinden.", traceStoreSpanCap, traceStoreSpanCap))
	}
	return out
}

// ttCapRunes — rune sınırında keser, kesildiğini "…" ile söyler.
func ttCapRunes(s string, max int) string {
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// traceToolEnvelope — v0.10.944: get_trace zarfı. Anahtarlar SABİT sırayla
// yazılır: durum (source/stub_reason/read_path) en önde, analiz ortada, ham
// span listesi EN SONDA. Düz map'te encoding/json alfabetik sıralar —
// `analysis` `source`'tan önce gelir ve sohbetin 6000 rune'luk model kırpması
// (clampToolResultForModel) büyük trace'te durumu ve notları keserdi.
// Map tipi korunur: out["source"] indekslemesi ve tip dönüşümleri çalışır.
type traceToolEnvelope map[string]any

var (
	traceToolEnvelopeFirst = []string{"source", "stub_reason", "read_path", "replica_read_state",
		"trace_id", "span_count", "total_span_count", "truncated", "stub", "analysis"}
	traceToolEnvelopeLast = []string{"spans"}
)

// MarshalJSON — sabit sıra; kalan anahtarlar alfabetik, spans en sonda.
func (e traceToolEnvelope) MarshalJSON() ([]byte, error) {
	return marshalOrderedMap(e, traceToolEnvelopeFirst, traceToolEnvelopeLast)
}

// marshalOrderedMap — SAF (v0.10.944): map'i `first` sırasıyla, sonra
// listelenmemiş anahtarları alfabetik, en sonda `last` sırasıyla yazar.
// Olmayan anahtar atlanır. Değerler encoding/json ile (HTML kaçışı dıştaki
// kodlayıcının varsayılanıyla aynı). compare_periods zarfı da kullanır.
func marshalOrderedMap(m map[string]any, first, last []string) ([]byte, error) {
	if m == nil {
		return []byte("null"), nil
	}
	fixed := make(map[string]bool, len(first)+len(last))
	for _, k := range first {
		fixed[k] = true
	}
	for _, k := range last {
		fixed[k] = true
	}
	mid := make([]string, 0, len(m))
	for k := range m {
		if !fixed[k] {
			mid = append(mid, k)
		}
	}
	sort.Strings(mid)
	var b bytes.Buffer
	b.WriteByte('{')
	n := 0
	write := func(k string) error {
		v, ok := m[k]
		if !ok {
			return nil
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return err
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if n > 0 {
			b.WriteByte(',')
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
		n++
		return nil
	}
	for _, list := range [][]string{first, mid, last} {
		for _, k := range list {
			if err := write(k); err != nil {
				return nil, err
			}
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// traceToolPayload — SAF: store sonucu → tool zarfı (liste + analiz + kaynak).
// Liste traceBodyPayload'dan (coremetry://trace/{id} resource'uyla ortak
// tavan) geçer; analiz tüm span'ler üzerinden.
func traceToolPayload(traceID string, spans []chstore.SpanRow) traceToolEnvelope {
	out := traceToolEnvelope(traceBodyPayload(traceID, spans))
	storeCapped := len(spans) >= traceStoreSpanCap
	listed := out["span_count"].(int)
	listTruncated := out["truncated"].(bool)
	st := sourcestate.Result("traces", "clickhouse", sourcestate.Outcome{
		Returned: listed, Limit: getTraceSpanCap, Truncated: listTruncated || storeCapped,
	})
	if len(spans) == 0 {
		out["source"] = st.WithNote("Coremetry ClickHouse'ta bu trace yok — saklama süresi dışında, örneklenmiş ya da henüz yazılmamış olabilir (bu araç Tempo yedeğini denemez). Boş sonuç 'hata yok' demek DEĞİLDİR.", false)
		return out
	}
	an := buildTraceToolAnalysis(spans, storeCapped)
	st = st.WithWindow(time.Unix(0, an.StartUnixNs), time.Unix(0, an.EndUnixNs))
	if listTruncated {
		st = st.WithNote(fmt.Sprintf("span listesi %d/%d'e kırpıldı (önce hata span'leri, sonra en yavaşlar); analysis okunan %d span'in TAMAMI üzerinden", listed, len(spans), len(spans)), false)
	}
	if storeCapped {
		st = st.WithNote(fmt.Sprintf("ClickHouse okuması %d span tavanına takıldı — trace daha büyük", traceStoreSpanCap), false)
	}
	out["analysis"] = an
	out["source"] = st
	return out
}

// traceToolNotFoundPayload — SAF (v0.10.944): ham okuma da trace_summary_5m
// özeti de boş — bugünkü boş zarf, özette de olmadığı notuyla.
func traceToolNotFoundPayload(id string) traceToolEnvelope {
	out := traceToolPayload(id, nil)
	out["source"] = out["source"].(sourcestate.Status).WithNote("trace_summary_5m özetinde de yok — trace Coremetry'ye hiç ulaşmamış (örnekleme/iletim) ya da özetin saklama süresi de dolmuş.", false)
	return out
}

// traceToolStubUnreadablePayload — SAF (v0.10.944): Distributed okuma 0 span
// döndü ve trace_summary_5m özet okuması BAŞARISIZ oldu (zaman aşımı/limit/
// erişilemedi). Eskiden bu da "özette yok → hiç ulaşmamış" diye bildiriliyordu;
// durum okuma hatasının sınıfıdır, yokluk kanıtı DEĞİL.
func traceToolStubUnreadablePayload(id string, err error) traceToolEnvelope {
	out := traceToolPayload(id, nil)
	out["stub_reason"] = "stub_unreadable"
	out["source"] = sourcestate.FromError("traces", "clickhouse", err).WithNote("Distributed okuma 0 span döndü; trace_summary_5m özeti okunamadı — yokluk kanıtı DEĞİL (replika ıraksaması / saklama ayırt edilemedi)", false)
	return out
}

// traceToolStubJSON — MV özetinin modele giden hâli (UTC ISO + unix ns).
func traceToolStubJSON(stub chstore.TraceAggregateStub) map[string]any {
	return map[string]any{
		"root_service": stub.RootService, "root_name": stub.RootName,
		"span_count": stub.SpanCount, "error_count": stub.ErrorCount,
		"start_iso": time.Unix(0, stub.StartTimeNs).UTC().Format(time.RFC3339Nano), "start_unix_ns": stub.StartTimeNs,
		"end_iso": time.Unix(0, stub.EndTimeNs).UTC().Format(time.RFC3339Nano), "end_unix_ns": stub.EndTimeNs,
	}
}

// traceToolMissPayload — SAF (v0.10.944): Distributed okuma 0 span döndü ve
// trace_summary_5m özeti VAR. Üç sonuç:
//
//	(a) tüm-replika okuması span buldu → normal zarf (ok/truncated) + replika
//	    ıraksaması notu, read_path=clickhouse_all_replicas;
//	(b) TTL içinde ama span okunamadı (tek düğümde yedek nil,nil döner; ya da
//	    yedek hata verdi) → partial + stub_reason=replica_miss;
//	(c) başlangıç ham TTL'in dışında → empty + stub_reason=aged_out.
//
// aged_out bir sourcestate DEĞİL, gerekçe alanıdır (durum empty kalır).
func traceToolMissPayload(id string, stub chstore.TraceAggregateStub, agedOut bool, replicaRows []chstore.SpanRow, replicaErr error) traceToolEnvelope {
	if len(replicaRows) > 0 {
		out := traceToolPayload(id, replicaRows)
		out["source"] = out["source"].(sourcestate.Status).WithNote("replika ıraksaması: Distributed okuma 0 span döndü; span'ler tüm replikalardan (clusterAllReplicas) okundu — Admin → Replika tutarlılığı", false)
		out["read_path"] = "clickhouse_all_replicas"
		return out
	}
	start, end := time.Unix(0, stub.StartTimeNs), time.Unix(0, stub.EndTimeNs)
	facts := fmt.Sprintf("%d span, %d hata, kök %s/%s, başlangıç %s", stub.SpanCount, stub.ErrorCount,
		stub.RootService, stub.RootName, start.UTC().Format(time.RFC3339))
	out := traceToolEnvelope{
		"trace_id": id, "spans": []chstore.SpanRow{}, "span_count": 0, "total_span_count": 0, "truncated": false,
		"stub": traceToolStubJSON(stub),
	}
	st := sourcestate.Result("traces", "clickhouse", sourcestate.Outcome{Limit: getTraceSpanCap}).WithWindow(start, end)
	if agedOut {
		out["stub_reason"] = "aged_out"
		out["source"] = st.WithNote("ham span saklama süresi (TTL) dışında — yalnız trace_summary_5m özeti kaldı ("+facts+"). Boş liste 'hata yok' demek DEĞİLDİR.", false)
		return out
	}
	out["stub_reason"] = "replica_miss"
	st = st.WithNote("replica_miss: trace özeti MV'de var ("+facts+") ama ham span'ler okunamadı — replika ıraksaması olası; saklama/örnekleme DEĞİL", true)
	if replicaErr != nil {
		cls := sourcestate.Classify(replicaErr)
		out["replica_read_state"] = cls
		st = st.WithNote(fmt.Sprintf("tüm-replika yedek okuması başarısız (%s)", cls), false)
	} else {
		st = st.WithNote("tüm-replika yedek okuması span bulamadı (tek düğümlü kurulumda bu yedek koşmaz)", false)
	}
	out["source"] = st
	return out
}

// getTraceAnalyzedTool — get_trace (v0.10.944 yükseltmesi).
func getTraceAnalyzedTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "get_trace",
		ShortDescription: "Trace span'leri + analiz: öz süreler, hatalar, kritik yol, env/pod/sürüm. Süre toplanmaz, CPU değildir.",
		Description: "Fetch one trace by its 32-hex trace ID: the span waterfall plus a server-computed `analysis`. " +
			"Use after search_traces / search_logs / get_exemplar_traces surfaced a trace ID. " +
			"Bounded span list: at most 200 spans (all ERROR spans first, then the slowest); truncated=true + total_span_count carry the real size. " +
			"`analysis` is computed over ALL spans read (not just the 200 listed): wall_ms (root span duration = the critical path's wall clock), extent_ms (first start → last end, includes async tails), " +
			"services[{service, self_ms, self_pct, spans, errors}] where self time = span duration minus the UNION of its direct children's intervals (parallel children are never subtracted twice; self_pct is a share of total self time, not of wall time), " +
			"top_self_spans (≤10), error_spans (≤10, earliest first, with status_message), critical_path[{span_id, service, name, self_on_path_ms}] — " +
			"NEVER add span durations or critical-path steps together: nested and parallel spans overlap in time, so sums overstate latency; quote wall_ms for the trace's duration. " +
			"context[{service, env, cluster, namespace, pods, versions}] comes from resource attributes using the same keys ClickHouse derives its columns from " +
			"(env = deployment.environment.name|deployment.environment, cluster = k8s.cluster.name|openshift.cluster.name|cluster, namespace = k8s.namespace.name, pod = k8s.pod.name else host.name, version = container image tag → service.version chain). " +
			"Span duration is NOT CPU time and there is no profiling data: method-level CPU/allocation cannot be inferred. " +
			"`source` reports the read state (ok | empty | partial | truncated | timeout | unreachable …). When the Distributed read returns no spans the tool checks the trace_summary_5m stub: " +
			"stub_reason=replica_miss (state partial — the stub exists inside raw retention but spans could not be read; or state ok with read_path=clickhouse_all_replicas when the spans were recovered from all replicas) or " +
			"stub_reason=aged_out (state empty — raw spans are past retention; `stub` carries root service/operation, span/error counts and start/end). " +
			"No stub (a clean summary read) means Coremetry never received the trace; if the summary read itself fails, source carries timeout/unreachable with stub_reason=stub_unreadable and absence is NOT established — empty is not 'nothing failed'. Reads ClickHouse only (no Tempo fallback). No env argument: the trace ID is the scope.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"trace_id": map[string]any{"type": "string", "description": "32-char hex trace ID (case-insensitive)."},
			},
			"required": []string{"trace_id"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a traceToolArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("decode args: %w", err)
			}
			id, err := traceToolTraceID(a.TraceID)
			if err != nil {
				return nil, err
			}
			if d.Store == nil {
				return traceToolEnvelope{
					"trace_id": id, "spans": []chstore.SpanRow{}, "span_count": 0, "total_span_count": 0, "truncated": false,
					"source": sourcestate.Status{Source: "traces", Backend: "clickhouse", State: sourcestate.NotConfigured},
				}, nil
			}
			spans, err := d.Store.GetTrace(ctx, id)
			if err != nil {
				if sourcestate.IsCancelled(err) {
					return nil, err
				}
				return traceToolEnvelope{
					"trace_id": id, "spans": []chstore.SpanRow{}, "span_count": 0, "total_span_count": 0, "truncated": false,
					"source": sourcestate.FromError("traces", "clickhouse", err),
				}, nil
			}
			if len(spans) > 0 {
				return traceToolPayload(id, spans), nil
			}
			// v0.10.944 — boş Distributed okuması: REST ucunun (trace_routes.go)
			// stub → TTL → tüm-replika yolu. ctx ayrıca sorulur.
			// v0.10.944 — özet okuma HATASI "özette yok" değildir: hata
			// sınıfı (timeout/unreachable) + stub_reason=stub_unreadable.
			stub, ok, serr := d.Store.GetTraceAggregateStubErr(ctx, id)
			if cerr := ctx.Err(); sourcestate.IsCancelled(cerr) {
				return nil, cerr
			}
			if serr != nil {
				if sourcestate.IsCancelled(serr) {
					return nil, serr
				}
				return traceToolStubUnreadablePayload(id, serr), nil
			}
			if !ok {
				return traceToolNotFoundPayload(id), nil
			}
			ret := ""
			if rs, rerr := d.Store.GetRetention(ctx); rerr == nil {
				ret = rs.Spans
			}
			agedOut := chstore.TraceAgedOut(stub.StartTimeNs, wallNow(), ret, d.Store.DefaultSpansDays())
			var rows []chstore.SpanRow
			var rerr error
			if !agedOut {
				rows, rerr = d.Store.GetTraceAllReplicas(ctx, id, stub.StartTimeNs, stub.EndTimeNs)
				if rerr != nil && sourcestate.IsCancelled(rerr) {
					return nil, rerr
				}
			}
			return traceToolMissPayload(id, stub, agedOut, rows, rerr), nil
		},
	}
}
