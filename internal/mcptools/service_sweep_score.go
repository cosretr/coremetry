package mcptools

// service_sweep_score.go — v0.10.1276 (CoSRE adım 7): service_health_sweep'in
// SAF sınıflandırma + puanlama yarısı (I/O yok; tablo testli).
//
// Eşik kaynakları (yeni eşik UYDURULMAZ, ASSUMPTION olanı işaretli):
//   - operasyon / fonksiyon: internal/healthverdict.RowSplit — servis HÜKMÜ ile
//     aynı hata (+1/+5 puan, mutlak %5) eşikleri; gecikme LatencyLevel
//     (v0.10.1299: iki pencerede ≥300 span, cari p99 ≥100 ms, uyarı +%25 VE
//     ≥+50 ms VE kova oynaklığı dışında, kritik 2× ve ≥200 ms); operasyon
//     gecikme adayı ham span doğrulamasıyla bulgu olur (service_sweep_latency.go);
//     hacim tabanı > 100 span.
//   - JVM heap / GC: GetRuntimeAlerts (classifyJVM, pivot_trace.go ile ortak).
//   - pod CPU / bellek % limit: 85 uyarı / 90 kritik (evaluator/db_capacity.go
//     emsali; pivot_trace.go ile aynı sabitler).
//   - OOMKilled son sonlanma ya da faz Running/Succeeded dışı → kritik.
//   - restart (penceredeki ARTIŞ, increase(...[pencere]) — KSM sayacı ömür
//     boyu olduğu için ham sayaç puanlanmaz): ≥1 uyarı, ≥3 kritik.
//     ASSUMPTION: depoda emsal eşik yok.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/healthverdict"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// Restart eşikleri (penceredeki artış). ASSUMPTION — bkz. dosya başı.
const (
	sweepRestartWarn = 1.0
	sweepRestartCrit = 3.0
)

// Bulgu türleri.
const (
	sweepKindPod       = "pod"
	sweepKindJVM       = "jvm"
	sweepKindOperation = "operation"
	sweepKindFunction  = "function"
)

// Top-N: standart 3, derin 5; açık istek en çok 8.
const (
	sweepTopNStandard = 3
	sweepTopNDeep     = 5
	sweepTopNMax      = 8
)

// sweepTopN — SAF: istenen bulgu sayısı (0 → kipin varsayılanı, tavan 8).
func sweepTopN(deep bool, requested int) int {
	switch {
	case requested > sweepTopNMax:
		return sweepTopNMax
	case requested > 0:
		return requested
	case deep:
		return sweepTopNDeep
	}
	return sweepTopNStandard
}

// sweepCand — puanlanacak aday (bulgu olmadan önceki hâli).
type sweepCand struct {
	Kind      string
	Name      string
	Cluster   string
	ClusterID string
	Namespace string
	Level     string
	Reasons   []string
	Impact    float64
	Hop       string // kanıt halkası (ops | functions | pods | jvm)
	// v0.10.1299 — operasyon/fonksiyon satırının kollu hükmü (gecikme kolu
	// ham span'le doğrulanır; doğrulanmazsa YALNIZ o kol düşer). nil = pod/JVM.
	row *healthverdict.RowVerdict
	// latThrMs — doğrulama eşiği (max(baseline p99 × (1+LatWarnRel), LatMinP99Ms));
	// latTotal — etki payının paydası (yeniden puanlamada).
	latThrMs    float64
	latTotal    uint64
	latSpans    uint64
	basePriorMs float64
}

// classifyOpRow — SAF: operasyon satırı → aday. share = satırın servis
// çağrılarındaki payı (0..1); etki = pay × (hata artışı puanı + 5 × (p99 katı − 1)).
// v0.10.1299: cur/prior p99 PENCERE düzeyi birleşik tDigest (operation_summary_5m
// quantilesTDigestMerge); oynaklık kolu önceki pencerenin 5 dk kova p99'ları
// (PriorP99Sparkline) — healthverdict.LatencyLevel.
func classifyOpRow(op chstore.OperationSummary, totalSpans uint64) sweepCand {
	cur := healthverdict.RED{Spans: op.SpanCount, ErrorRate: op.ErrorRate, P99Ms: op.P99Ms}
	base := healthverdict.RED{Spans: op.PriorSpanCount, ErrorRate: op.PriorErrorRate, P99Ms: op.PriorP99Ms}
	v := healthverdict.RowSplit(cur, base, op.HasPrior, op.PriorP99Sparkline)
	return sweepCandFromRow(sweepKindOperation, op.Name, sweepHopOps, v, op.SpanCount, totalSpans, op.PriorP99Ms)
}

// classifyFnRow — SAF: fonksiyon (FUNCTION_CODE) satırı → aday. Geniş rollup'ın
// p99'u 20'lik sabit histogram KESTİRİMİ (±%15-30, /clickhouse-schema §6):
// gecikme kolu ASLA kritik değildir (kritik → uyarı) ve gerekçe "kova
// kestirimi" der. priorBuckets = önceki pencerenin kova p99'ları.
func classifyFnRow(code string, cur, base healthverdict.RED, hasBase bool, totalCalls uint64, priorBuckets []float64) sweepCand {
	v := healthverdict.RowSplit(cur, base, hasBase, priorBuckets)
	if v.LatLevel != levelOK {
		if v.LatLevel == levelCrit {
			v.LatLevel = levelWarn
		}
		v.LatReason += " (kova kestirimi, yaklaşık)"
		v.Compose()
	}
	return sweepCandFromRow(sweepKindFunction, code, sweepHopFunctions, v, cur.Spans, totalCalls, base.P99Ms)
}

// sweepCandFromRow — SAF: kollu hüküm → aday (etki = pay × (hata artışı + 5 ×
// (p99 katı − 1))).
func sweepCandFromRow(kind, name, hop string, v healthverdict.RowVerdict, spans, total uint64, baseP99 float64) sweepCand {
	c := sweepCand{Kind: kind, Name: name, Hop: hop, row: &v, latTotal: total, latSpans: spans, basePriorMs: baseP99}
	c.applyRow()
	if v.LatLevel != levelOK {
		c.latThrMs = healthverdict.CorroborationThresholdMs(baseP99)
	}
	return c
}

// applyRow — adayın seviye/gerekçe/etkisini kollu hükümden yeniden kurar.
func (c *sweepCand) applyRow() {
	if c.row == nil {
		return
	}
	c.row.Compose()
	c.Level, c.Reasons = c.row.Level, c.row.Reasons
	c.Impact = sweepREDImpact(c.latSpans, c.latTotal, c.row.ErrDelta, c.row.P99Mult)
}

func sweepREDImpact(spans, total uint64, errDelta, p99Mult float64) float64 {
	if total == 0 {
		return 0
	}
	share := float64(spans) / float64(total)
	m := 0.0
	if p99Mult > 1 {
		m = p99Mult - 1
	}
	return round4(share * (errDelta + 5*m))
}

// classifySweepPod — SAF: Thanos pod satırı (+ penceredeki restart artışı) →
// aday. restarts nil = artış okunamadı (ham ömür boyu sayaç PUANLANMAZ).
// Etki sırası: OOMKilled (3) > faz (2.5) > bellek % (2 + %/100) > CPU %
// (1.5 + %/100) > restart (1 + n/100).
func classifySweepPod(r thanos.PodRow, restarts *float64) sweepCand {
	c := sweepCand{Kind: sweepKindPod, Name: r.Pod, Cluster: r.Cluster, Namespace: r.Namespace, Level: levelOK, Hop: sweepHopPods}
	bump := func(l, why string, impact float64) {
		if l == levelCrit || (l == levelWarn && c.Level == levelOK) {
			c.Level = l
		}
		c.Reasons = append(c.Reasons, why)
		if impact > c.Impact {
			c.Impact = impact
		}
	}
	if strings.EqualFold(r.LastTermReason, "OOMKilled") {
		bump(levelCrit, "son sonlanma OOMKilled", 3)
	}
	if r.Phase != "" && r.Phase != "Running" && r.Phase != "Succeeded" {
		bump(levelCrit, "faz "+r.Phase, 2.5)
	}
	for _, x := range []struct {
		pct  float64
		what string
		base float64
	}{{r.MemPct, "bellek", 2}, {r.CPUPct, "CPU", 1.5}} {
		switch {
		case x.pct >= pivotPodCritPct:
			bump(levelCrit, fmt.Sprintf("%s limitin %%%.0f'i", x.what, x.pct), x.base+x.pct/100)
		case x.pct >= pivotPodWarnPct:
			bump(levelWarn, fmt.Sprintf("%s limitin %%%.0f'i", x.what, x.pct), x.base+x.pct/100)
		}
	}
	if restarts != nil {
		n := math.Round(*restarts)
		switch {
		case n >= sweepRestartCrit:
			bump(levelCrit, fmt.Sprintf("pencerede %.0f restart (≥ %.0f)", n, sweepRestartCrit), 1+n/100)
		case n >= sweepRestartWarn:
			bump(levelWarn, fmt.Sprintf("pencerede %.0f restart", n), 1+n/100)
		}
	}
	c.Impact = round4(c.Impact)
	return c
}

// sweepJVMCand — SAF: JVM satırı → aday (etki = GC sonrası ya da ham heap %/100).
func sweepJVMCand(row pivotJVMRow) sweepCand {
	c := sweepCand{Kind: sweepKindJVM, Name: row.Pod, Level: row.Level, Hop: sweepHopJVM}
	if row.Reason != "" {
		c.Reasons = strings.Split(row.Reason, "; ")
	}
	switch {
	case row.PostGCPct != nil:
		c.Impact = round4(*row.PostGCPct / 100)
	case row.HeapPct != nil:
		c.Impact = round4(*row.HeapPct / 100)
	}
	if row.GCPauseMs != nil && *row.GCPauseMs/1000 > c.Impact {
		c.Impact = round4(*row.GCPauseMs / 1000)
	}
	return c
}

// sweepKindOrder — eşit seviye + etkide tür sırası (altyapı önce: pod sorunu
// operasyonları da sürükler).
// v0.10.1287 — k8s (bekleme sebebi / pencerede OOM) ve deploy (başlangıca
// yakın rollout) altyapının da önünde: follow-up B sırası "crit önce, sonra
// k8s > deploy"; mevcut pod > JVM > operasyon > fonksiyon sırası korunur.
var sweepKindOrder = map[string]int{sweepKindK8s: -2, sweepKindDeploy: -1, sweepKindPod: 0, sweepKindJVM: 1, sweepKindRuntime: 1, sweepKindOperation: 2, sweepKindFunction: 3}

// scoreFindings — SAF: yalnız ok OLMAYAN adaylar; sıra seviye → etki (azalan)
// → tür → ad. topN'e kırpılır; ikinci dönüş kırpıldı mı. Rank 1'den.
func scoreFindings(in []sweepCand, topN int) ([]SweepFinding, bool) {
	var bad []sweepCand
	for _, c := range in {
		if c.Level != levelOK {
			bad = append(bad, c)
		}
	}
	sort.SliceStable(bad, func(i, j int) bool {
		if a, b := healthverdict.LevelRank(bad[i].Level), healthverdict.LevelRank(bad[j].Level); a != b {
			return a < b
		}
		if bad[i].Impact != bad[j].Impact {
			return bad[i].Impact > bad[j].Impact
		}
		if a, b := sweepKindOrder[bad[i].Kind], sweepKindOrder[bad[j].Kind]; a != b {
			return a < b
		}
		return bad[i].Name < bad[j].Name
	})
	more := false
	if topN > 0 && len(bad) > topN {
		bad, more = bad[:topN], true
	}
	out := make([]SweepFinding, 0, len(bad))
	for i, c := range bad {
		reasons := c.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		out = append(out, SweepFinding{Rank: i + 1, Kind: c.Kind, Name: c.Name, Cluster: c.Cluster, ClusterID: c.ClusterID,
			Namespace: c.Namespace, Level: c.Level, Reasons: reasons, EvidenceRefs: []string{}, hop: c.Hop, impact: c.Impact})
	}
	return out, more
}

// sweepVerdictOf — SAF: servis RED hükmü + bulgular → tarama hükmü. Etiket
// ikisinin KÖTÜSÜ; gerekçeler RED gerekçeleri + bulgu özetleri. RED "Veri yok"
// ama bulgu varsa bulgu kazanır (pod/JVM tek başına da sorundur). Hiç bir şey
// okunamadıysa "Veri yok".
func sweepVerdictOf(redLabel string, redReasons []string, redNext string, findings []SweepFinding, anyData bool) SweepVerdict {
	v := SweepVerdict{Label: redLabel, Reasons: append([]string(nil), redReasons...), Next: redNext}
	worst := levelOK
	for _, f := range findings {
		if healthverdict.LevelRank(f.Level) < healthverdict.LevelRank(worst) {
			worst = f.Level
		}
	}
	if fl := healthverdict.LabelForLevel(worst); worst != levelOK && healthverdict.LabelRank(fl) > healthverdict.LabelRank(v.Label) {
		if v.Label == healthverdict.LabelNoData {
			v.Reasons = nil
		}
		v.Label = fl
	}
	for i, f := range findings {
		if i >= 3 {
			break
		}
		v.Reasons = append(v.Reasons, fmt.Sprintf("%s %s: %s", sweepKindTR(f.Kind), f.Name, strings.Join(f.Reasons, ", ")))
	}
	if v.Label == healthverdict.LabelNoData && !anyData {
		v.Next = "Zaman aralığını genişletin ya da servis adını kontrol edin."
	}
	if len(findings) > 0 && v.Label != healthverdict.LabelNoData {
		f := findings[0]
		v.Next = fmt.Sprintf("Önce 1 numaralı bulguya bakın (%s %s)", sweepKindTR(f.Kind), f.Name)
		if f.Logs != nil && len(f.Logs.Rows) > 0 {
			v.Next += " — logları aşağıda"
		}
		v.Next += "."
	}
	if v.Reasons == nil {
		v.Reasons = []string{}
	}
	return v
}

// sweepKindTR — bulgu türünün Türkçe adı.
func sweepKindTR(k string) string {
	switch k {
	case sweepKindPod:
		return "pod"
	case sweepKindJVM:
		return "JVM"
	case sweepKindRuntime:
		return "runtime"
	case sweepKindOperation:
		return "operasyon"
	case sweepKindFunction:
		return "fonksiyon"
	case sweepKindK8s: // v0.10.1287
		return "Kubernetes"
	case sweepKindDeploy:
		return "deploy"
	}
	return k
}

// sweepCacheKey — SAF: hash-all-inputs (CLAUDE.md sert kısıtı). Pencere 30 s
// kovaya oturur; cluster kayıtları (id:ad + span değerleri) ve JVM eşikleri
// sonucu değiştirir → anahtarda.
func sweepCacheKey(req SweepRequest, clusters []ClusterRef, alerts chstore.RuntimeAlertConfig) string {
	cs := make([]string, 0, len(clusters))
	for _, c := range clusters {
		vals := append([]string(nil), c.SpanValues...)
		sort.Strings(vals)
		cs = append(cs, c.ID+":"+c.Name+"\x00"+strings.Join(vals, ","))
	}
	sort.Strings(cs)
	ab, _ := json.Marshal(alerts)
	// v0.10.1287 — kontroller (sıralı, nil ≠ boş) + deploy çıpası anahtarda.
	return "sweep:v1:" + fnvHex(req.Service, req.Env, req.Window,
		fmt.Sprint(req.From.Unix()/30), fmt.Sprint(req.To.Unix()/30),
		fmt.Sprint(req.Deep), fmt.Sprint(req.TopN), fmt.Sprint(req.SkipPatterns),
		string(ab), fnvHex(cs...), sweepChecksKey(req), fmt.Sprint(sweepAnchor(req).Unix()/30))
}
