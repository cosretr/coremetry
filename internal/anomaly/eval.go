package anomaly

// eval.go — v0.10.1793 (Davis paritesi FAZ 1.5, docs/plans/spec-davis-faz1.md):
// etiketli anomali vakalarının REPLAY'i ve FP / recall tablosu. SAF çekirdek;
// okuma replayer arayüzünün arkasında.
//
// Vaka dosyası JSONL (satır başına bir EvalCase; boş satır ve `#` yorum
// atlanır). Etiket operatörün mevcut "gerçek / değil" düğmelerinden gelir
// (karar 11). Replay iki yoldan:
//
//   - series: vaka sentetik ya da dışa aktarılmış seriyi taşır → karar
//     çekirdeği (evaluateAnomaly — anomaly_explain'in her adımda koştuğu
//     çekirdek) doğrudan koşar; okuma yok.
//   - canlı: anomaly_explain ucu (/api/settings/anomaly-red-source/explain)
//     pencereyi replay eder (make eval-anomaly, COREMETRY_ANOMALY_EVAL_BASE_URL).
//
// Kapı (spec F1.5): vaka sayısı ≥ evalMinCases VE her iki etiketten örnek;
// FP oranı ≤ FAZ 0.5 kıyas kolonu, recall ≥ FAZ 0.5, endpoint düzeyinde ≥ 1
// yakalanmış gerçek vaka. Bunlardan biri ölçülemiyorsa sonuç "ÖLÇÜLMEDİ" —
// sayı uydurulmaz. Özneler bu kapı geçmeden varsayılan AÇILMAZ.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// EvalCase — tek etiketli vaka.
type EvalCase struct {
	ID          string `json:"id"`
	Family      string `json:"family"`                // ör. "metric", "trace_op", "log_pattern", "behavior_change"
	SubjectKind string `json:"subjectKind,omitempty"` // service (boş) | endpoint | db | queue
	Subject     string `json:"subject"`               // servis adı ya da özne kimliği
	Metric      string `json:"metric,omitempty"`      // error_rate | p99_ms | request_rate
	From        int64  `json:"from,omitempty"`        // unix sn
	To          int64  `json:"to,omitempty"`
	// Real — operatör etiketi: gerçek bir anomali mi.
	Real bool `json:"real"`
	// Phase05Opened — FAZ 0.5 replay'inin bu vakadaki kararı (kıyas kolonu);
	// nil = ölçülmedi.
	Phase05Opened *bool `json:"phase05Opened,omitempty"`
	// Series — sentetik / dışa aktarılmış seri; doluysa okuma yapılmaz.
	Series *EvalSeries `json:"series,omitempty"`
}

// EvalSeries — karar çekirdeğinin girdisi (fetchAllBuckets / fetchAllSeasonal ikizi).
type EvalSeries struct {
	Buckets  []float64 `json:"buckets"`
	Seasonal []float64 `json:"seasonal,omitempty"`
	Rates    []float64 `json:"rates"`
}

// EvalOutcome — bir vakanın replay sonucu. Err doluysa vaka ÖLÇÜLEMEDİ
// (tabloya girmez; ayrı sayılır — "açılmadı" sanılmaz).
type EvalOutcome struct {
	CaseID string
	Opened bool
	Reason string
	Err    string
}

// EvalReplayer — vaka replay'i.
type EvalReplayer interface {
	Replay(ctx context.Context, c EvalCase) EvalOutcome
}

// ParseEvalCases — SAF: JSONL vaka dosyası. Kimliksiz / ailesiz satır, seri
// taşımayıp penceresi bozuk satır ve tekrar eden kimlik HATADIR (satır
// numarasıyla): etiket setinde sessiz düşüş paydayı değiştirir.
func ParseEvalCases(r io.Reader) ([]EvalCase, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var out []EvalCase
	seen := map[string]int{}
	line := 0
	for sc.Scan() {
		line++
		t := strings.TrimSpace(sc.Text())
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		var c EvalCase
		if err := json.Unmarshal([]byte(t), &c); err != nil {
			return nil, fmt.Errorf("satır %d: %w", line, err)
		}
		switch {
		case strings.TrimSpace(c.ID) == "":
			return nil, fmt.Errorf("satır %d: id boş", line)
		case strings.TrimSpace(c.Family) == "":
			return nil, fmt.Errorf("satır %d (%s): family boş", line, c.ID)
		case c.Series == nil && (c.From <= 0 || c.To <= c.From):
			return nil, fmt.Errorf("satır %d (%s): seri yok ve pencere geçersiz (from < to)", line, c.ID)
		case c.Series == nil && strings.TrimSpace(c.Subject) == "":
			return nil, fmt.Errorf("satır %d (%s): subject boş", line, c.ID)
		}
		if prev, ok := seen[c.ID]; ok {
			return nil, fmt.Errorf("satır %d: id %q satır %d'de de var", line, c.ID, prev)
		}
		seen[c.ID] = line
		out = append(out, c)
	}
	return out, sc.Err()
}

// SeriesReplayer — vaka serisi üzerinde karar çekirdeği (evaluateAnomaly).
// Serisiz vaka ÖLÇÜLEMEDİ döner (bir sonraki replayer'a bırakılır).
type SeriesReplayer struct {
	Sens       chstore.AnomalySensitivityConfig
	MinSamples int // mevsimsel taban örnek eşiği (0 → seasonalMinSamples)
}

// Replay — EvalReplayer.
func (r SeriesReplayer) Replay(_ context.Context, c EvalCase) EvalOutcome {
	if c.Series == nil {
		return EvalOutcome{CaseID: c.ID, Err: "seri yok"}
	}
	metric := c.Metric
	if metric == "" {
		metric = errorRampMetric
	}
	if _, err := metricValueExpr(metric); err != nil {
		return EvalOutcome{CaseID: c.ID, Err: err.Error()}
	}
	minS := r.MinSamples
	if minS <= 0 {
		minS = seasonalMinSamples
	}
	oc := evaluateAnomaly(metric, c.Series.Buckets, c.Series.Seasonal, c.Series.Rates, minS, false, batchLatSeries{}, r.Sens)
	reason := oc.Action
	if oc.ZBlocker != "" {
		reason += ": " + oc.ZBlocker
	}
	return EvalOutcome{CaseID: c.ID, Opened: oc.Action == "open", Reason: reason}
}

// ChainReplayer — sıradaki ilk ölçebilen replayer kazanır.
type ChainReplayer []EvalReplayer

// Replay — EvalReplayer.
func (ch ChainReplayer) Replay(ctx context.Context, c EvalCase) EvalOutcome {
	last := EvalOutcome{CaseID: c.ID, Err: "replayer yok"}
	for _, r := range ch {
		o := r.Replay(ctx, c)
		if o.Err == "" {
			return o
		}
		last = o
	}
	return last
}

// RunEval — vakaları sırayla replay eder.
func RunEval(ctx context.Context, cases []EvalCase, r EvalReplayer) []EvalOutcome {
	out := make([]EvalOutcome, 0, len(cases))
	for _, c := range cases {
		out = append(out, r.Replay(ctx, c))
	}
	return out
}

// EvalRow — aile başına sayım. Oranlar paydası 0 iken NaN (ÖLÇÜLMEDİ).
type EvalRow struct {
	Family               string
	Cases, Unmeasured    int
	TP, FP, FN, TN       int
	FPRate, Recall       float64
	P05TP, P05FP, P05FN  int
	P05TN, P05Cases      int
	P05FPRate, P05Recall float64
	EndpointCaught       int // gerçek + endpoint öznesi + yakalandı
	EndpointReal         int
}

// EvalTable — aileler (ada göre sıralı) + toplam.
type EvalTable struct {
	Rows  []EvalRow
	Total EvalRow
}

// ScoreEval — SAF: vakalar + sonuçlar → tablo. Sonucu olmayan ya da Err'li
// vaka Unmeasured; FAZ 0.5 kolonu yalnız ÖLÇÜLEN ve Phase05Opened dolu
// vakalardan (iki kolon aynı paydayla kıyaslansın).
func ScoreEval(cases []EvalCase, outs []EvalOutcome) EvalTable {
	byID := make(map[string]EvalOutcome, len(outs))
	for _, o := range outs {
		byID[o.CaseID] = o
	}
	rows := map[string]*EvalRow{}
	total := &EvalRow{Family: "TOPLAM"}
	for _, c := range cases {
		r := rows[c.Family]
		if r == nil {
			r = &EvalRow{Family: c.Family}
			rows[c.Family] = r
		}
		for _, row := range []*EvalRow{r, total} {
			row.Cases++
			o, ok := byID[c.ID]
			if !ok || o.Err != "" {
				row.Unmeasured++
				continue // FAZ 0.5 kolonu da yalnız ölçülen vakalardan: aynı payda
			}
			countConfusion(&row.TP, &row.FP, &row.FN, &row.TN, c.Real, o.Opened)
			if c.Real && c.SubjectKind == chstore.AnomalySubjectEndpoint {
				row.EndpointReal++
				if o.Opened {
					row.EndpointCaught++
				}
			}
			if c.Phase05Opened != nil {
				row.P05Cases++
				countConfusion(&row.P05TP, &row.P05FP, &row.P05FN, &row.P05TN, c.Real, *c.Phase05Opened)
			}
		}
	}
	out := EvalTable{Total: finishEvalRow(*total)}
	for _, r := range rows {
		out.Rows = append(out.Rows, finishEvalRow(*r))
	}
	sort.Slice(out.Rows, func(i, j int) bool { return out.Rows[i].Family < out.Rows[j].Family })
	return out
}

func countConfusion(tp, fp, fn, tn *int, real, opened bool) {
	switch {
	case real && opened:
		*tp++
	case !real && opened:
		*fp++
	case real && !opened:
		*fn++
	default:
		*tn++
	}
}

func finishEvalRow(r EvalRow) EvalRow {
	r.FPRate = ratioOrNaN(r.FP, r.FP+r.TN)
	r.Recall = ratioOrNaN(r.TP, r.TP+r.FN)
	r.P05FPRate = ratioOrNaN(r.P05FP, r.P05FP+r.P05TN)
	r.P05Recall = ratioOrNaN(r.P05TP, r.P05TP+r.P05FN)
	return r
}

func ratioOrNaN(n, d int) float64 {
	if d == 0 {
		return math.NaN()
	}
	return float64(n) / float64(d)
}

// evalMinCases — kapının vaka tabanı (karar 11: aile başına dengeli ≥ 50 satır;
// burada toplam taban, aile dengesi tabloda okunur).
const evalMinCases = 50

// EvalVerdictUnmeasured — kapı ölçülemediğinde sonuç metninin başı.
const EvalVerdictUnmeasured = "ÖLÇÜLMEDİ"

// EvalGate — SAF: kapı hükmü + gerekçeler. "GEÇTİ" | "KALDI" | "ÖLÇÜLMEDİ (…)".
func EvalGate(t EvalTable) (string, []string) {
	tot := t.Total
	var missing []string
	measured := tot.Cases - tot.Unmeasured
	if measured < evalMinCases {
		missing = append(missing, fmt.Sprintf("ölçülen vaka %d < %d", measured, evalMinCases))
	}
	if tot.TP+tot.FN == 0 || tot.FP+tot.TN == 0 {
		missing = append(missing, "iki etiketten de örnek yok")
	}
	if tot.P05Cases == 0 || math.IsNaN(tot.P05FPRate) || math.IsNaN(tot.P05Recall) {
		missing = append(missing, "FAZ 0.5 kıyas kolonu yok")
	}
	if len(missing) > 0 {
		return EvalVerdictUnmeasured + " (eval seti bekleniyor)", missing
	}
	var fails []string
	if tot.FPRate > tot.P05FPRate {
		fails = append(fails, fmt.Sprintf("FP oranı %.3f > FAZ 0.5 %.3f", tot.FPRate, tot.P05FPRate))
	}
	if tot.Recall < tot.P05Recall {
		fails = append(fails, fmt.Sprintf("recall %.3f < FAZ 0.5 %.3f", tot.Recall, tot.P05Recall))
	}
	if tot.EndpointCaught < 1 {
		fails = append(fails, "endpoint düzeyinde yakalanmış onaylı vaka yok")
	}
	if len(fails) > 0 {
		return "KALDI", fails
	}
	return "GEÇTİ", nil
}

// RenderEvalMarkdown — SAF: tablo + hüküm (docs/audit satırı için).
func RenderEvalMarkdown(t EvalTable) string {
	var b strings.Builder
	b.WriteString("| Aile | Vaka | Ölçülemedi | TP | FP | FN | TN | FP oranı | Recall | FAZ 0.5 FP | FAZ 0.5 recall | Endpoint yakalanan |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range append(append([]EvalRow{}, t.Rows...), t.Total) {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %s | %s | %s | %s | %d/%d |\n",
			r.Family, r.Cases, r.Unmeasured, r.TP, r.FP, r.FN, r.TN,
			fmtRate(r.FPRate), fmtRate(r.Recall), fmtRate(r.P05FPRate), fmtRate(r.P05Recall), r.EndpointCaught, r.EndpointReal)
	}
	v, why := EvalGate(t)
	b.WriteString("\nKapı: **" + v + "**")
	if len(why) > 0 {
		b.WriteString(" — " + strings.Join(why, "; "))
	}
	b.WriteString("\n")
	return b.String()
}

func fmtRate(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf("%.3f", v)
}
