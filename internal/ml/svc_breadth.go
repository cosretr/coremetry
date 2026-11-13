package ml

// svc_breadth.go — v0.10.2086 (spec docs/plans/spec-ml-service-level.md
// dilim 2, operatör onayı 2026-11-13): "yaygın yavaşlama". Eski
// `svc-slowdown:<svc>` (evaluator/service_slowdown.go) işinin ML karşılığı;
// bu dilimde YALNIZ ml_events yazar (Problem yok; gölge kapısı dilim 3).
//
// TÜRETİLMİŞ — YENİ SORGU YOK: bu tikte kalite süzgecinden geçmiş (açık)
// op_latency olaylarından. Kural (sabit, vida yok; operatörün onayladığı
// spec varsayılanları):
//   - aynı serviste ≥ 3 farklı operasyon,
//   - her biri p95 ölçülü op_latency olayı, p95 ≥ 1000 ms VE p95 ≥ 2 × taban,
//   - koşul ardışık 2 tik (5 dk tikte 10 dk) sürmüş,
//   - `-batch` servis DIŞARIDA (eski svc-slowdown ile aynı).
// Yalnız p95 ölçülü olaylar sayılır: yavaş-istek-oranı ölçülü op_latency
// olayının p95 tabanı yoktur, "≥ 2× ve p95 ≥ 1000 ms" sınanamaz.
// Servis başına koşul sürdükçe TEK olay (kimlik servis; epizot modeli aynı).
// Cümle yavaş operasyonları adıyla söyler (en büyük 3 + "ve N işlem daha").
//
// Sürdürme durumu süreç-içi (tracker gibi): lider değişince sıfırlanır,
// yeni lider 2 tik sonra yeniden açar (kabul edilen sapma).

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detSvcBreadth = "svc_breadth"

	breadthMinOps    = 3
	breadthMinRatio  = 2.0
	breadthMinP95Ms  = 1000.0
	breadthMinTicks  = 2 // ardışık tik (10 dk)
	breadthShowOps   = 3
	breadthEvidenceN = 10 // kanıtta en çok bu kadar operasyon
)

// BreadthOp — yaygın yavaşlamanın bir operasyonu (kanıt "slowOps").
type BreadthOp struct {
	Op     string  `json:"op"`
	P95Ms  float64 `json:"p95Ms"`
	BaseMs float64 `json:"baseMs"`
	Ratio  float64 `json:"ratio"`
	Score  float64 `json:"-"`
}

// breadthRun — servisin ardışık koşul tik sayısı (süreç-içi).
type breadthRun struct {
	since time.Time
	ticks int
}

// breadthOpsBySvc — SAF: olaylar → servis → koşulu sağlayan operasyonlar
// (oran azalan, sonra ad). Batch servisleri ve kronik kayıtlar dışarıda.
func breadthOpsBySvc(evs []chstore.MLEvent, isBatch func(string) bool) map[string][]BreadthOp {
	out := map[string][]BreadthOp{}
	seen := map[string]bool{}
	for _, e := range evs {
		if e.Detector != detOpLatency || e.Stage == chstore.MLStageChronic || e.Direction != "up" || e.Code == "" {
			continue
		}
		if isBatch != nil && isBatch(e.Service) {
			continue
		}
		if evStr(evidenceMap(e.Evidence), "measure") != "p95" || e.Baseline <= 0 {
			continue
		}
		r := e.Observed / e.Baseline
		if e.Observed < breadthMinP95Ms || r < breadthMinRatio {
			continue
		}
		k := e.Service + "\x00" + e.Code
		if seen[k] {
			continue
		}
		seen[k] = true
		out[e.Service] = append(out[e.Service], BreadthOp{Op: e.Code, P95Ms: e.Observed, BaseMs: e.Baseline, Ratio: r, Score: e.Score})
	}
	for svc, ops := range out {
		sort.Slice(ops, func(i, j int) bool {
			if ops[i].Ratio != ops[j].Ratio {
				return ops[i].Ratio > ops[j].Ratio
			}
			return ops[i].Op < ops[j].Op
		})
		out[svc] = ops
	}
	return out
}

// breadthVerdict — SAF: bu tikin operasyonları + önceki tikin koşu durumu →
// ateşleyen servisler + yeni koşu durumu. Koşul düşen servis durumdan
// çıkar (ardışıklık sıfırlanır).
func breadthVerdict(ops map[string][]BreadthOp, prev map[string]breadthRun, now time.Time) (fire map[string]breadthRun, next map[string]breadthRun) {
	fire, next = map[string]breadthRun{}, map[string]breadthRun{}
	for svc, list := range ops {
		if len(list) < breadthMinOps {
			continue
		}
		run, ok := prev[svc]
		if !ok {
			run = breadthRun{since: now}
		}
		run.ticks++
		next[svc] = run
		if run.ticks >= breadthMinTicks {
			fire[svc] = run
		}
	}
	return fire, next
}

// breadthEvent — SAF: ateşleyen servisin olayı. Taban = operasyon
// tabanlarının medyanı; gözlenen = oran medyanı × taban (spec: Value /
// Threshold oranı = operasyon oranlarının medyanı).
func breadthEvent(svc string, ops []BreadthOp, run breadthRun, now time.Time, cfg chstore.MLConfig) chstore.MLEvent {
	ratios, bases := make([]float64, 0, len(ops)), make([]float64, 0, len(ops))
	score := 0.0
	for _, o := range ops {
		ratios = append(ratios, o.Ratio)
		bases = append(bases, o.BaseMs)
		score = math.Max(score, o.Score)
	}
	medR, _ := medianMAD(ratios)
	medB, _ := medianMAD(bases)
	shown := ops
	if len(shown) > breadthEvidenceN {
		shown = shown[:breadthEvidenceN]
	}
	raw := mergeEvidence("", map[string]any{"source": "op_latency", "slowOps": shown, "slowOpsTotal": len(ops),
		"medianRatio": medR, "minOps": breadthMinOps, "minRatio": breadthMinRatio, "minP95Ms": breadthMinP95Ms,
		"dwell": breadthMinTicks, "bucketSec": cfg.BucketSec})
	return chstore.MLEvent{
		ID: chstore.MLEventID(detSvcBreadth, svc, ""), Detector: detSvcBreadth, Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow,
		Service: svc, StartedAt: run.since.UnixNano(), LastSeen: now.UnixNano(), WindowSec: uint32(cfg.BucketSec),
		Baseline: medB, Observed: medR * medB, Score: score, Direction: "up", Evidence: raw,
	}
}

// breadthEvents — recorder adımı: tik olayları → yaygın yavaşlama olayları
// (servis sıralı) + yeni koşu durumu.
func breadthEvents(kept []chstore.MLEvent, isBatch func(string) bool, prev map[string]breadthRun, now time.Time, cfg chstore.MLConfig) ([]chstore.MLEvent, map[string]breadthRun) {
	ops := breadthOpsBySvc(kept, isBatch)
	fire, next := breadthVerdict(ops, prev, now)
	out := make([]chstore.MLEvent, 0, len(fire))
	for svc, run := range fire {
		out = append(out, breadthEvent(svc, ops[svc], run, now, cfg))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out, next
}

// breadthOpsOf — SAF: kanıttan operasyonlar + toplam (kart / cümle).
func breadthOpsOf(evidence string) ([]BreadthOp, int) {
	var m struct {
		Ops   []BreadthOp `json:"slowOps"`
		Total int         `json:"slowOpsTotal"`
	}
	if evidence == "" || json.Unmarshal([]byte(evidence), &m) != nil {
		return nil, 0
	}
	if m.Total < len(m.Ops) {
		m.Total = len(m.Ops)
	}
	return m.Ops, m.Total
}

// msTR — SAF: "850 ms" / "1,4 sn".
func msTR(ms float64) string {
	if ms >= 1000 {
		return trDec(ms/1000, 1) + " sn"
	}
	return trInt(ms) + " ms"
}

// breadthHead — SAF: "/anomalies" cümlesi: "orders-api: 4 işlem birlikte
// yavaşladı — GET /a normalde 200 ms, şimdi 1,4 sn; …; ve 1 işlem daha".
func breadthHead(in CardInput) string {
	if len(in.BreadthOps) == 0 {
		return fmt.Sprintf("%s: birden çok işlem birlikte yavaşladı", in.Service)
	}
	total := max(in.BreadthTotal, len(in.BreadthOps))
	parts := make([]string, 0, breadthShowOps)
	for i, o := range in.BreadthOps {
		if i == breadthShowOps {
			break
		}
		parts = append(parts, fmt.Sprintf("%s normalde %s, şimdi %s", o.Op, msTR(o.BaseMs), msTR(o.P95Ms)))
	}
	s := fmt.Sprintf("%s: %d işlem birlikte yavaşladı (p95) — %s", in.Service, total, strings.Join(parts, "; "))
	if rest := total - len(parts); rest > 0 {
		s += fmt.Sprintf("; ve %d işlem daha", rest)
	}
	return s
}
