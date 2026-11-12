package ml

// explain_evidence.go — v0.10.1873, öğrenme döngüsü Faz 2 parti B (audit
// §7.1 "Kanıt" satırları, §7.2 şablon tablosu; mockup görünüm 2–4 "Kanıt"):
// sinyale göre kanıt bölümlerinin SAF kurulumu. Okumalar çağıranda
// (internal/api/ml_evidence.go); her bölüm kendi ucunda, yalnız açılınca /
// gösterilince istenir.
//
//   trace  → endpoint tablosu: servis endpoint'leri (spanmetrics MV) olay
//            penceresi vs hemen önceki eşit pencere — çağrı, p95 şimdi,
//            p95 önce; olayın endpoint'i başta.
//   log    → mesaj kalıbı tablosu: logstore imza grupları (ES/CH, örneklemeli)
//            olay penceresi vs önceki eşit pencere; oran ifadesi kartın
//            merdiveninden.
//   metric → komşu metrikler: aynı servisin dedektörün izlediği diğer
//            ifadeleri (ml_series, VM'den zaten çekilmiş seri), olay öncesi
//            medyan → pencere uç değeri.
//
// Cümleler burada (FE cümle kurmaz); sayı uydurulmaz (taban yoksa ifade yok).

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	evidenceMaxWindow = time.Hour // kanıt penceresi: başlangıçtan en çok 1 sa (ilk sapma burada)
	evidenceMinWindow = 15 * time.Minute
	evidenceEndpoints = 10
	evidencePatterns  = 10
	evidenceNeighbors = 3
)

// EvidenceWindow — SAF: [başlangıç, min(bitiş, başlangıç + 1 sa)), en az 15
// dk; önceki = hemen önceki eşit pencere.
func EvidenceWindow(in CardInput) (from, to, prevFrom time.Time) {
	end := cardEnd(in, cardOpen(in))
	from, to = in.StartedAt, end
	if to.Sub(from) > evidenceMaxWindow {
		to = from.Add(evidenceMaxWindow)
	}
	if to.Sub(from) < evidenceMinWindow {
		to = from.Add(evidenceMinWindow)
		if !in.Now.IsZero() && to.After(in.Now) {
			to = in.Now
		}
	}
	return from, to, from.Add(-to.Sub(from))
}

// EvidenceRange — SAF: kanıt satırlarının linklerindeki pencere (FE
// windowRangeParam ikizi; kanıt penceresi + 5 dk kuyruk).
func EvidenceRange(in CardInput) string {
	from, to, _ := EvidenceWindow(in)
	return rangeParamOf(from, to.Add(5*time.Minute))
}

// EndpointEvidenceRow — endpoint tablosu satırı.
type EndpointEvidenceRow struct {
	Route    string  `json:"route"`
	Calls    uint64  `json:"calls"`
	P95Ms    float64 `json:"p95Ms"`
	PrevP95  float64 `json:"prevP95Ms,omitempty"` // 0 = önceki pencerede yok
	Deviates bool    `json:"deviates,omitempty"`  // p95 öncekinin ≥ 1,8 katı (kartın "yaklaşık 2 kat" basamağı)
	Focus    bool    `json:"focus,omitempty"`     // olayın endpoint'i
	Href     string  `json:"href"`                // Traces (pencere + endpoint süzgeci)
}

// EndpointEvidence — SAF: iki pencere → satırlar (olayın endpoint'i önce,
// sonra çağrı sayısı). En çok 10.
func EndpointEvidence(in CardInput, cur, prev []chstore.EndpointRow, rangeParam string) []EndpointEvidenceRow {
	before := map[string]float64{}
	for _, p := range prev {
		before[p.Path] = p.P95Ms
	}
	route := eventRoute(in)
	out := make([]EndpointEvidenceRow, 0, len(cur))
	for _, c := range cur {
		r := EndpointEvidenceRow{Route: c.Path, Calls: c.Calls, P95Ms: c.P95Ms, PrevP95: before[c.Path],
			Focus: route != "" && c.Path == route, Href: tracesLink(in.Service, rangeParam, tracesOpts{route: c.Path})}
		r.Deviates = r.PrevP95 > 0 && r.P95Ms >= 1.8*r.PrevP95
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Focus != out[j].Focus {
			return out[i].Focus
		}
		return out[i].Calls > out[j].Calls
	})
	if len(out) > evidenceEndpoints {
		out = out[:evidenceEndpoints]
	}
	return out
}

// PatternEvidenceRow — mesaj kalıbı tablosu satırı (sayılar örneklem içi).
type PatternEvidenceRow struct {
	Template  string `json:"template"`
	Count     int64  `json:"count"`
	Compare   string `json:"compare"`             // "normalin yaklaşık 12 katı" / "önceki eşit pencerede görülmedi"
	New       bool   `json:"new,omitempty"`       // önceki pencerenin örnekleminde yok
	FirstSeen string `json:"firstSeen,omitempty"` // yeni ise ilk görülme saati (operatör dilimi)
	Sample    string `json:"sample,omitempty"`    // örnek satır (verbatim)
	Href      string `json:"href"`
}

// PatternEvidence — SAF: imza grupları (taban birleşmiş) → yeni ve artan
// mesajlar (yeni önce, sonra oran, sonra sayı). baseOK=false: taban
// okunamadı → oran ifadesi yazılmaz, "yeni" denmez (yokluk ≠ sıfır).
func PatternEvidence(in CardInput, groups []logstore.SignatureGroup, baseOK bool, rangeParam string) []PatternEvidenceRow {
	type row struct {
		r     PatternEvidenceRow
		ratio float64
	}
	errOnly := in.Part == partError
	var rows []row
	for _, g := range groups {
		if g.Count <= 0 {
			continue
		}
		x := PatternEvidenceRow{Template: g.Template, Count: g.Count, Sample: truncRunes(g.Sample, 300),
			Href: logsLink(in.logSvc(), g.Query, errOnly, rangeParam)}
		ratio := 0.0
		if baseOK {
			switch {
			case g.PrevCount <= 0:
				x.New, x.Compare = true, "önceki eşit pencerede görülmedi"
				if g.FirstSeen > 0 {
					x.FirstSeen = time.Unix(0, g.FirstSeen).In(in.loc()).Format("15:04")
				}
				ratio = math.Inf(1)
			default:
				ratio = float64(g.Count) / float64(g.PrevCount)
				if ratio < 1.12 {
					continue // yalnız yeni ve artan
				}
				x.Compare = RatioPhrase(float64(g.Count), float64(g.PrevCount), "up", RatioLong, "")
			}
		}
		rows = append(rows, row{x, ratio})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ratio != rows[j].ratio {
			return rows[i].ratio > rows[j].ratio
		}
		return rows[i].r.Count > rows[j].r.Count
	})
	out := make([]PatternEvidenceRow, 0, evidencePatterns)
	for _, r := range rows {
		if len(out) == evidencePatterns {
			break
		}
		out = append(out, r.r)
	}
	return out
}

// MetricNeighbor — aynı servisin dedektörün izlediği başka bir metrik ifadesi.
type MetricNeighbor struct {
	Part   string    `json:"part"`
	Label  string    `json:"label"`
	Unit   string    `json:"unit,omitempty"`
	Times  []int64   `json:"times"`
	Values []float64 `json:"values"`
	Text   string    `json:"text"`
}

// MetricNeighbors — SAF: servisin ml_series metrik parçaları (olayın kendi
// parçası hariç) → olay öncesi medyan → pencerede en çok sapan değer; en
// çok sapan 3'ü. Olay öncesi nokta yoksa ifade yazılmaz (yalnız grafik).
func MetricNeighbors(in CardInput, series map[string][]chstore.MLPoint, cfg chstore.MLConfig) []MetricNeighbor {
	type cand struct {
		n     MetricNeighbor
		score float64
	}
	from, to := in.StartedAt.Unix(), cardEnd(in, cardOpen(in)).Unix()
	var cands []cand
	for part, pts := range series {
		if part == in.Part || len(pts) < 2 || !strings.Contains(part, "|") {
			continue
		}
		fam, _, _ := strings.Cut(part, "|")
		label, unit := MetricLabelFor(cfg, fam, part)
		n := MetricNeighbor{Part: part, Label: label, Unit: unit, Times: make([]int64, len(pts)), Values: make([]float64, len(pts))}
		var pre []float64
		for i, p := range pts {
			n.Times[i], n.Values[i] = p.T, p.V
			if p.T < from {
				pre = append(pre, p.V)
			}
		}
		score := 0.0
		if len(pre) > 0 {
			med, _ := medianMAD(pre)
			peak, dev := med, 0.0
			for _, p := range pts {
				if p.T >= from && p.T < to && math.Abs(p.V-med) > dev {
					peak, dev = p.V, math.Abs(p.V-med)
				}
			}
			f := newValueFmt(detSpec{unit: unitMetric}, CardInput{MetricUnit: unit}, math.Max(math.Abs(med), math.Abs(peak)))
			metricS := unit == "s"
			if RatioPhrase(math.Max(peak, med), math.Min(peak, med), "up", RatioShort, "") == "normal aralıkta" {
				n.Text = "Değişmedi."
			} else {
				n.Text = f.one(med, metricS) + " → " + f.one(peak, metricS) + "."
			}
			score = dev / math.Max(math.Abs(med), 1e-9)
		}
		cands = append(cands, cand{n, score})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].n.Part < cands[j].n.Part
	})
	out := []MetricNeighbor{}
	for _, c := range cands {
		if len(out) == evidenceNeighbors {
			break
		}
		out = append(out, c.n)
	}
	return out
}
