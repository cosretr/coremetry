package evaluator

// anomaly_subject_group.go — v0.10.1798 (Davis paritesi FAZ 3 dilim 2,
// docs/plans/spec-davis-faz3.md): özne olaylarının (subject_red) terfi
// kapısı ve endpoint olaylarının gruplanması. Hepsi SAF ve tablo-testli
// (anomaly_subject_group_test.go); süpürmeye bağlanması dilim 3 (gölge) ve
// dilim 4 (canlı).
//
// Gruplama: bir servisin N rotası aynı metrikte aynı yönde kötüleşirse
// Problems'ta N satır değil TEK aday — rule_id
// `anomaly-auto:subject:<servis>:<metrik>:<yön>`. Tek rota da aynı rule_id'yi
// alır (rota adı başlıkta). db / queue öznesi gruplanmaz: kendi parmak izi
// (`anomaly-auto:<fp>`, mevcut yol) ve aynı kapı.

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// subjectGroupWindow — gruplama penceresi: açılışta bir adayın üyeleri,
// kapıyı geçen rotalar ile kapıyı ilk geçen rotanın başlangıcına ± bu kadar
// yakın başlamış diğer kötüleşen rotalardır (aynı olayın rotaları birlikte
// başlar; henüz kapıyı geçmemiş komşu rota başlıkta sayılır). Açık Problem'in
// tazelenmesinde pencere uygulanmaz — o anda kötüleşen bütün rotalar listede.
const subjectGroupWindow = 15 * time.Minute

// subjectGroupDetailTop — detayda adı yazılan en çok rota; kalanı "ve N rota daha".
const subjectGroupDetailTop = 5

// promoteSubjectRulePrefix — gruplanmış endpoint adayının rule_id öneki.
// "anomaly-auto:" ailesinin içinde: bildirim türü / kategori / bayat süpürme
// sınıflandırıcıları öneke baktığı için yeni önek tüketicisi gerekmez.
const promoteSubjectRulePrefix = promoteAnomalyRuleID + "subject:"

// subjectMetricConsumerLag — anomaly.subjectMetricConsumerLag'ın ikizi (paket
// dışa açmıyor; TestSubjectConsumerLagMetricName pinler).
const subjectMetricConsumerLag = "consumer_lag"

// subjectEventMetric — SAF: özne olayının metriği. Endpoint deseni
// "metrik @ rota", db/queue deseni yalnız metrik (subjectEventPattern).
func subjectEventMetric(ev chstore.AnomalyEvent) string {
	if i := strings.Index(ev.Pattern, " @ "); i >= 0 {
		return ev.Pattern[:i]
	}
	return ev.Pattern
}

// subjectEventRoute — SAF: endpoint olayının rotası (SubjectID; eski/boş
// satırda desenin "@" sonrası).
func subjectEventRoute(ev chstore.AnomalyEvent) string {
	if ev.SubjectID != "" {
		return ev.SubjectID
	}
	if i := strings.Index(ev.Pattern, " @ "); i >= 0 {
		return ev.Pattern[i+3:]
	}
	return ev.Pattern
}

// subjectWorsening — SAF: (metrik, yön) bir kötüleşme mi. Hata oranı ve
// gecikme ARTIŞI kötüleşmedir; istek hızı hiçbir yönde terfi etmez (düşüş
// FAZ 3 kapsamı dışında — sessiz servis / takvim yollarının işi; artış yük
// sinyali, Problem değil). Yön bilinmiyorsa ("" — eski satır) terfi yok.
//
// v0.10.1802 — Kafka consumer_lag ARTIŞI da kötüleşmedir (aynı sürdürme /
// oran eşikleri; tabansız lag'in sayım kolu YOK — subjectCountOnly yalnız
// error_rate).
func subjectWorsening(metric, direction string) bool {
	switch metric {
	case "error_rate", "p95_ms", "p99_ms", subjectMetricConsumerLag:
		return direction == chstore.AnomalyDirectionUp
	}
	return false
}

// subjectCountOnly — SAF: oranı bilinmeyen (taban 0 / tabansız) hata oranı
// olayı mı. Bu kol oran yerine sayım tabanını kullanır.
func subjectCountOnly(ev chstore.AnomalyEvent) bool {
	return subjectEventMetric(ev) == "error_rate" && !chstore.AnomalyRatioKnown(ev)
}

// subjectPromotionGate — SAF: özne olayının terfi kapısı (promotionGate'in
// özne kolu; o işlev genel yolda özneleri hâlâ reddeder, özne yolu buradan
// geçer). Üç şart birlikte:
//
//	yön      — subjectWorsening (yalnız kötüleşme)
//	sürdürme — now − StartedAt ≥ PromotionMinSustainedSec (600)
//	oran     — bölüm tepesi ≥ PromotionMinRatio (2,0); oranı bilinmeyen
//	           error_rate'te oran yerine CurrentCount ≥ CountOnlyMinCount.
//
// Durum (active) ve susturma çağıranda (anomalyPromotionStep emsali).
func subjectPromotionGate(ev chstore.AnomalyEvent, scfg chstore.AnomalySubjectsConfig, pcfg chstore.AnomalyPromotionConfig, now time.Time) bool {
	if !chstore.IsSubjectAnomaly(ev) || !subjectWorsening(subjectEventMetric(ev), ev.Direction) {
		return false
	}
	if now.Sub(time.Unix(0, ev.StartedAt)) < time.Duration(scfg.PromotionMinSustainedSec)*time.Second {
		return false
	}
	if subjectCountOnly(ev) {
		return ev.CurrentCount >= pcfg.CountOnlyMinCount
	}
	return ev.PeakRatio >= scfg.PromotionMinRatio
}

// subjectGroupKey — gruplama anahtarı.
type subjectGroupKey struct {
	Service, Metric, Direction string
}

// subjectGroupRuleID — SAF: anahtarın rule_id'si (pencere içermez: aynı
// servis + metrik + yön için en çok bir açık Problem).
func subjectGroupRuleID(k subjectGroupKey) string {
	return promoteSubjectRulePrefix + k.Service + ":" + k.Metric + ":" + k.Direction
}

// subjectCandidate — bir anahtarın terfi adayı.
type subjectCandidate struct {
	Key    subjectGroupKey
	RuleID string
	// Members — anahtarın aktif, susturulmamış, kötüleşen bütün rotaları;
	// orana göre azalan (eşitlikte rota artan — deterministik).
	Members []chstore.AnomalyEvent
	// Opening — kapıyı geçen rotalar + kapıyı ilk geçen rotanın
	// başlangıcına ±subjectGroupWindow içinde başlamış diğer üyeler (aynı sıra). Boş = kapıyı geçen rota yok → açılış
	// adayı değil (yalnız açık Problem tazelenebilir).
	Opening []chstore.AnomalyEvent
}

// subjectEventRatio — SAF: sıralama / başlık için olayın oranı (bilinmiyorsa 0).
func subjectEventRatio(ev chstore.AnomalyEvent) float64 {
	if !chstore.AnomalyRatioKnown(ev) {
		return 0
	}
	return ev.PeakRatio
}

// groupSubjectEvents — SAF: süpürmenin olaylarından endpoint adayları.
// Yalnız aktif + susturulmamış + kötüleşen endpoint özne olayları girer
// (db/queue ve diğer türler atlanır). Çıktı rule_id'ye göre sıralı.
func groupSubjectEvents(events []chstore.AnomalyEvent, muted map[string]bool, gate func(chstore.AnomalyEvent) bool) []subjectCandidate {
	byKey := map[subjectGroupKey][]chstore.AnomalyEvent{}
	for _, ev := range events {
		if !chstore.IsSubjectAnomaly(ev) || ev.SubjectKind != chstore.AnomalySubjectEndpoint ||
			ev.Status != "active" || muted[ev.ID] {
			continue
		}
		m := subjectEventMetric(ev)
		if !subjectWorsening(m, ev.Direction) {
			continue
		}
		k := subjectGroupKey{Service: ev.Service, Metric: m, Direction: ev.Direction}
		byKey[k] = append(byKey[k], ev)
	}
	out := make([]subjectCandidate, 0, len(byKey))
	for k, mem := range byKey {
		sort.Slice(mem, func(i, j int) bool {
			ri, rj := subjectEventRatio(mem[i]), subjectEventRatio(mem[j])
			if ri != rj {
				return ri > rj
			}
			return subjectEventRoute(mem[i]) < subjectEventRoute(mem[j])
		})
		c := subjectCandidate{Key: k, RuleID: subjectGroupRuleID(k), Members: mem}
		anchor := int64(math.MaxInt64)
		for _, ev := range mem {
			if gate(ev) && ev.StartedAt < anchor {
				anchor = ev.StartedAt
			}
		}
		if anchor != math.MaxInt64 {
			for _, ev := range mem {
				d := ev.StartedAt - anchor
				if d < 0 {
					d = -d
				}
				if d <= int64(subjectGroupWindow) || gate(ev) {
					c.Opening = append(c.Opening, ev)
				}
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuleID < out[j].RuleID })
	return out
}

// subjectMetricLabel — SAF: başlıktaki metrik adı.
func subjectMetricLabel(metric string) string {
	switch metric {
	case "p99_ms":
		return "p99"
	case "p95_ms":
		return "p95"
	case "error_rate":
		return "hata oranı"
	case subjectMetricConsumerLag:
		return "consumer lag"
	}
	return metric
}

// formatRatioTR — SAF: "2,4×" (Türkçe ondalık virgül, bir hane).
func formatRatioTR(r float64) string {
	return strings.Replace(strconv.FormatFloat(r, 'f', 1, 64), ".", ",", 1) + "×"
}

// formatDurationTR — SAF: "15 dk", "1 sa 20 dk" (dakikaya aşağı yuvarlı, en az 1 dk).
func formatDurationTR(d time.Duration) string {
	m := int(d / time.Minute)
	if m < 1 {
		m = 1
	}
	if m < 60 {
		return strconv.Itoa(m) + " dk"
	}
	if m%60 == 0 {
		return strconv.Itoa(m/60) + " sa"
	}
	return strconv.Itoa(m/60) + " sa " + strconv.Itoa(m%60) + " dk"
}

// subjectGroupStats — SAF: üyelerin en yüksek bilinen oranı ve en erken başlangıcı.
func subjectGroupStats(mem []chstore.AnomalyEvent) (peak float64, started int64) {
	started = math.MaxInt64
	for _, ev := range mem {
		if r := subjectEventRatio(ev); r > peak {
			peak = r
		}
		if ev.StartedAt < started {
			started = ev.StartedAt
		}
	}
	if started == math.MaxInt64 {
		started = 0
	}
	return peak, started
}

// subjectGroupTitle — SAF: düz Türkçe başlık.
//
//	orders-api: 3 rota yavaşladı (p99 2,4× taban, 15 dk)
//	orders-api: GET /cart yavaşladı (p99 2,4× taban, 15 dk)
//	orders-api: 3 rotada hata oranı arttı (2,4× taban, 15 dk)
//	orders-api: GET /cart rotasında hata oranı arttı (yeni hata, taban yok, 15 dk)
func subjectGroupTitle(k subjectGroupKey, mem []chstore.AnomalyEvent, now time.Time) string {
	peak, started := subjectGroupStats(mem)
	dur := formatDurationTR(now.Sub(time.Unix(0, started)))
	var what string
	single := len(mem) == 1 && mem[0].SubjectKind != chstore.AnomalySubjectEndpoint
	if single && k.Metric == subjectMetricConsumerLag {
		// v0.10.1802 — orders-consumer: orders topic'inde consumer lag 40,0× taban (15 dk)
		ratio := "taban yok"
		if peak > 0 {
			ratio = formatRatioTR(peak) + " taban"
		}
		return k.Service + ": " + subjectQueueTopic(mem[0].SubjectID) + " topic'inde consumer lag " + ratio + " (" + dur + ")"
	}
	switch {
	case single && k.Metric == "error_rate":
		// db / queue öznesi: özne zaten servis alanında (db:… / queue:…).
		what = "hata oranı arttı"
	case single:
		what = "yavaşladı"
	case k.Metric == "error_rate":
		if len(mem) == 1 {
			what = subjectEventRoute(mem[0]) + " rotasında hata oranı arttı"
		} else {
			what = strconv.Itoa(len(mem)) + " rotada hata oranı arttı"
		}
	case len(mem) == 1:
		what = subjectEventRoute(mem[0]) + " yavaşladı"
	default:
		what = strconv.Itoa(len(mem)) + " rota yavaşladı"
	}
	var paren string
	switch {
	case peak <= 0:
		paren = "yeni hata, taban yok, " + dur
	case k.Metric == "error_rate":
		paren = formatRatioTR(peak) + " taban, " + dur
	default:
		paren = subjectMetricLabel(k.Metric) + " " + formatRatioTR(peak) + " taban, " + dur
	}
	return k.Service + ": " + what + " (" + paren + ")"
}

// subjectQueueTopic — SAF: kuyruk öznesi kimliğinden hedef (topic) adı;
// çözülemezse kimliğin kendisi.
func subjectQueueTopic(id string) string {
	if _, dest, ok := chstore.ParseQueueSubjectID(id); ok {
		return dest
	}
	return id
}

// subjectGroupDescription — SAF: detay — rotalar orana göre ilk 5 + "ve N
// rota daha". Üyeler groupSubjectEvents sırasında (oran azalan) gelir.
func subjectGroupDescription(k subjectGroupKey, mem []chstore.AnomalyEvent) string {
	if len(mem) == 1 && mem[0].SubjectKind != chstore.AnomalySubjectEndpoint {
		// v0.10.1800 — db / queue öznesi: rota listesi yok, tek cümle.
		r := "taban yok"
		if x := subjectEventRatio(mem[0]); x > 0 {
			r = "tabanın " + formatRatioTR(x)
		}
		out := subjectMetricLabel(k.Metric) + ": " + r
		if mem[0].Sample != "" {
			out += " (" + mem[0].Sample + ")"
		}
		return out + "."
	}
	var b strings.Builder
	b.WriteString("Etkilenen rotalar (")
	b.WriteString(subjectMetricLabel(k.Metric))
	b.WriteString(", tabana oranla): ")
	n := len(mem)
	if n > subjectGroupDetailTop {
		n = subjectGroupDetailTop
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(subjectEventRoute(mem[i]))
		b.WriteString(" ")
		if r := subjectEventRatio(mem[i]); r > 0 {
			b.WriteString(formatRatioTR(r))
		} else {
			b.WriteString("taban yok")
		}
	}
	if rest := len(mem) - n; rest > 0 {
		b.WriteString(" ve ")
		b.WriteString(strconv.Itoa(rest))
		b.WriteString(" rota daha")
	}
	b.WriteString(".")
	return b.String()
}

// subjectGroupProblem — SAF: adayın Problem gövdesi. open != nil → tazeleme
// (bütün üyeler listelenir, StartedAt ve operatör alanları açık satırdan);
// open == nil → açılış (yalnız Opening üyeleri). Şiddet: bilinen tepe ≥
// CriticalPeakRatio → critical, aksi (ve tabansız) warning; yaş tabanına
// kelepçe (effectiveSeverity) çağıranda — terfi yolunun aynı sırası.
func subjectGroupProblem(c subjectCandidate, open *chstore.Problem, scfg chstore.AnomalySubjectsConfig, pcfg chstore.AnomalyPromotionConfig, now time.Time) chstore.Problem {
	mem := c.Opening
	if open != nil || len(mem) == 0 {
		mem = c.Members
	}
	peak, started := subjectGroupStats(mem)
	sev := "warning"
	if peak > 0 && peak >= pcfg.CriticalPeakRatio {
		sev = "critical"
	}
	p := chstore.Problem{
		ID:          c.RuleID + ":" + c.Key.Service,
		RuleID:      c.RuleID,
		RuleName:    subjectGroupTitle(c.Key, mem, now),
		Severity:    sev,
		Service:     c.Key.Service,
		Metric:      "anomaly_ratio",
		Value:       peak,
		Threshold:   scfg.PromotionMinRatio,
		Status:      "open",
		Description: truncate(subjectGroupDescription(c.Key, mem), 480),
		StartedAt:   started,
	}
	if peak <= 0 {
		var maxCount uint64
		for _, ev := range mem {
			if ev.CurrentCount > maxCount {
				maxCount = ev.CurrentCount
			}
		}
		p.Metric, p.Comparator, p.Value, p.Threshold = "anomaly_count", ">=", float64(maxCount), float64(pcfg.CountOnlyMinCount)
	}
	if open != nil {
		p.ID = open.ID
		p.StartedAt = open.StartedAt
		carryProblemOperatorState(&p, open)
	}
	return p
}

// subjectSingleCandidate — SAF (v0.10.1800): db / queue olayının adayı.
// Gruplanmaz; rule_id mevcut yolun `anomaly-auto:<fp>`'si (spec), kapı aynı.
func subjectSingleCandidate(ev chstore.AnomalyEvent, gateOK bool) subjectCandidate {
	c := subjectCandidate{
		Key:     subjectGroupKey{Service: ev.Service, Metric: subjectEventMetric(ev), Direction: ev.Direction},
		RuleID:  promoteAnomalyRuleID + ev.ID,
		Members: []chstore.AnomalyEvent{ev},
	}
	if gateOK {
		c.Opening = c.Members
	}
	return c
}

// subjectLiveCandidates — SAF (v0.10.1800): canlı kipin bütün adayları —
// endpoint grupları + aktif, susturulmamış, kötüleşen db / queue olayları
// (kapı geçmese de: açık Problem'i tazelenebilsin; Opening boşsa yeni açmaz).
func subjectLiveCandidates(events []chstore.AnomalyEvent, muted map[string]bool, gate func(chstore.AnomalyEvent) bool) []subjectCandidate {
	out := groupSubjectEvents(events, muted, gate)
	for _, ev := range events {
		if !chstore.IsSubjectAnomaly(ev) || ev.SubjectKind == chstore.AnomalySubjectEndpoint ||
			ev.Status != "active" || muted[ev.ID] || !subjectWorsening(subjectEventMetric(ev), ev.Direction) {
			continue
		}
		out = append(out, subjectSingleCandidate(ev, gate(ev)))
	}
	return out
}

// activeSubjectGroupRules — SAF (v0.10.1800): en az bir aktif, susturulmamış,
// kötüleşen rotası olan endpoint adaylarının rule_id'leri. Çözülme geçişi
// `anomaly-auto:subject:` Problem'ini bu kümede yoksa kapatır.
func activeSubjectGroupRules(events []chstore.AnomalyEvent, muted map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, c := range groupSubjectEvents(events, muted, func(chstore.AnomalyEvent) bool { return false }) {
		out[c.RuleID] = true
	}
	return out
}
