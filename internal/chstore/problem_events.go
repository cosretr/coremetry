package chstore

// problem_events.go — v0.10.2163 (Davis tarzı tek Problem D4, spec
// docs/plans/spec-davis-style-problem-merge.md): OKUYUCULAR. Bir Problem'e
// bağlı olaylar (üyeler) iki yüzeye iner:
//
//   - olay ÇİZELGESİ (Problem detayı): ProblemEvents — tek servis, tam satır;
//   - olay SAYISI (Problems listesi "· N olay", MCP): EnrichProblemsWithEventCounts
//     — sayfa başına TEK hafif okuma (kanıt gövdesi taşınmaz; Problem bağı
//     ClickHouse'ta JSON'dan çıkarılır).
//
// Şema değişmez: üyelik ml_events.evidence'ta (üye olayının `problem_id`'si,
// ML'in canlı groupShadow / svcShadow damgası). Go tarafındaki okuyucu
// MLEventProblemID ile SQL ifadesi (mlEventProblemIDExpr) AYNI kuralı
// uygular — test ikisini aynı satırlarla pinler.
//
// "Olay" sayımı: Problem'e bağlı ml_events satırları + sahibin KENDİ sinyali
// (yalnız eski aile sahibinde: onun sinyali bir ml_events satırı değildir).
// Üyesi olmayan Problem'de sayı 0'dır (birleşme yok → rozet yok).

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"
)

// ProblemEventSlack — olayın Problem başlangıcından en çok bu kadar ÖNCE
// bitmiş olması kabul edilir (üye sahibinden biraz önce başlamış olabilir;
// aynı kimlikle yeniden açılan satırın eski bölümü bu pencerenin dışındadır).
const ProblemEventSlack = 30 * time.Minute

// problemEventsReadCap — çizelge okuması (tek servis) tavanı.
const problemEventsReadCap = 500

// problemEventRefsCap — sayım okuması (sayfanın servisleri) tavanı.
const problemEventRefsCap = 5000

// eventCountLookback — sayım okumasının en geniş penceresi. Liste rozeti
// sıcak bir uçta (15 sn cache) koşar ve kanıt JSON'unu ClickHouse'ta açar;
// günlerdir açık tek bir Problem pencereyi günlere yaymasın. Sonuç: çok eski
// bir Problem'in rozeti son 24 saatte görülen olayları sayar; tam geçmiş
// detaydaki çizelgededir (tek servis okuması).
const eventCountLookback = 24 * time.Hour

// ProblemEventStat — Problem'e bağlı olay sayımı.
type ProblemEventStat struct {
	Events    int // bağlı olaylar + (eski aile sahibinde) sahibin kendi sinyali
	Detectors int // farklı dedektör sayısı (aynı kural)
}

// problemEventRef — sayım okumasının hafif satırı.
type problemEventRef struct {
	Service, Detector, ProblemID string
	StartedAt, LastSeen          int64
}

// problemOwnsEvent — SAF: olay bu Problem'in BU bölümüne mi ait (ProblemEventsOf
// ile aynı kural; iki okuyucu tek yüklem).
func problemOwnsEvent(p Problem, service, problemID string, startedAt, lastSeen int64, slack time.Duration) bool {
	if service != p.Service || problemID == "" || problemID != p.ID {
		return false
	}
	if lastSeen < p.StartedAt-int64(slack) {
		return false
	}
	return p.ResolvedAt == nil || startedAt <= *p.ResolvedAt
}

// ownSignalCounts — SAF: sahibin kendi sinyali ayrı bir olay mı (eski aile:
// evet; ML ailesi: hayır — onun sinyali zaten bağlı ML olaylarıdır).
func ownSignalCounts(p Problem) bool { return !IsMLLiveProblem(p.RuleID) }

// ProblemOwnDetector — SAF: sahibin kendi sinyalinin çizelgedeki dedektör adı
// ("" = ML ailesi: ayrı satır yok).
func ProblemOwnDetector(p Problem) string {
	if !ownSignalCounts(p) {
		return ""
	}
	return MemberDetectorForRule(p.RuleID)
}

// ProblemMeasureIsOwn — SAF: satırın ölçüsü (Metric / Value / Threshold)
// sahibin KENDİ ailesinin ölçüsü mü, yoksa önceliği yükselten bir üyeden mi
// geldi (RaiseOwner). Yaygın yavaşlama p99_ms yazar; metrik anomalisi kendi
// metriğini ">" / "<" ile yazar (üyelerin ölçüsü ">=" taşır — anomaly
// paketindeki ownAnomalyMeasure ile aynı ayrım). Diğer aileler: kendi.
func ProblemMeasureIsOwn(p Problem) bool {
	if strings.HasPrefix(p.RuleID, RuleSvcSlowdownPrefix) {
		return p.Metric == "p99_ms"
	}
	if m := anomalyServiceMetric(p.RuleID); m != "" {
		switch strings.TrimSpace(p.Comparator) {
		case "", ">", "<":
			return p.Metric == m
		}
		return false
	}
	return true
}

// ProblemBelowRule — SAF: satır "değer düştükçe kötüleşen" aileden mi.
func ProblemBelowRule(p Problem) bool { return isBelowRule(p.Comparator) }

// countProblemEvents — SAF: hafif satırlar → Problem başına sayım. Yalnız
// bağlı olayı olan Problem haritaya girer.
func countProblemEvents(problems []Problem, refs []problemEventRef) map[string]ProblemEventStat {
	out := map[string]ProblemEventStat{}
	for _, p := range problems {
		n := 0
		dets := map[string]bool{}
		for _, r := range refs {
			if problemOwnsEvent(p, r.Service, r.ProblemID, r.StartedAt, r.LastSeen, ProblemEventSlack) {
				n++
				dets[r.Detector] = true
			}
		}
		if n == 0 {
			continue
		}
		if own := ProblemOwnDetector(p); own != "" {
			n++
			dets[own] = true
		}
		out[p.ID] = ProblemEventStat{Events: n, Detectors: len(dets)}
	}
	return out
}

// mlEventProblemIDExpr — MLEventProblemID'nin SQL ikizi: üye olayının
// problem_id'si, yoksa ML'in CANLI servis düzeyi damgası, yoksa canlı grup
// damgası.
const mlEventProblemIDExpr = `multiIf(
			JSONExtractString(evidence, 'problem_id') != '', JSONExtractString(evidence, 'problem_id'),
			JSONExtractBool(evidence, 'svcShadow', 'live') AND JSONExtractString(evidence, 'svcShadow', 'problemId') != '', JSONExtractString(evidence, 'svcShadow', 'problemId'),
			JSONExtractBool(evidence, 'groupShadow', 'live'), JSONExtractString(evidence, 'groupShadow', 'problemId'),
			'')`

// problemEventRefsSQL — sayım okuması: yalnız bir Problem'e bağlı satırlar,
// kanıt gövdesi istemciye taşınmaz. Zaman sınırlı + LIMIT + max_execution_time.
// position() ön elemesi JSON açmayı yalnız bağ taşıyabilecek satırlara indirir.
const problemEventRefsSQL = `
		SELECT service, detector, started_at, last_seen, ` + mlEventProblemIDExpr + ` AS problem_ref
		FROM ml_events FINAL
		WHERE last_seen >= ? AND service IN ?
		  AND (position(evidence, 'problem_id') > 0 OR position(evidence, 'problemId') > 0)
		  AND problem_ref != ''
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// eventCountCandidates — SAF: sayım okumasına girecek Problem'ler (yalnız
// açık, servis özneli, SAHİP olabilen aile — üyesi yalnız onların olur) ve
// okuma penceresinin başı. Aday yoksa ok=false (okuma yapılmaz).
func eventCountCandidates(problems []Problem, now time.Time) (cands []Problem, services []string, since time.Time, ok bool) {
	seen := map[string]bool{}
	minStart := int64(0)
	for _, p := range problems {
		if !mergeable(&p) || !OwnerEligibleRule(p.RuleID) {
			continue
		}
		cands = append(cands, p)
		if !seen[p.Service] {
			seen[p.Service] = true
			services = append(services, p.Service)
		}
		if minStart == 0 || p.StartedAt < minStart {
			minStart = p.StartedAt
		}
	}
	if len(cands) == 0 {
		return nil, nil, time.Time{}, false
	}
	sort.Strings(services)
	since = time.Unix(0, minStart).Add(-ProblemEventSlack)
	if floor := now.Add(-eventCountLookback); since.Before(floor) {
		since = floor
	}
	return cands, services, since, true
}

// EnrichProblemsWithEventCounts — açık sahip Problem'lere EventCount /
// EventDetectors ekler (okuma-anı; saklanmaz). Aday yoksa okuma yok. Okuma
// hatası loglanır ve satırlar aynen döner — sayı bir süsleme, liste değil.
func (s *Store) EnrichProblemsWithEventCounts(ctx context.Context, problems []Problem) []Problem {
	cands, services, since, ok := eventCountCandidates(problems, time.Now())
	if !ok {
		return problems
	}
	rows, err := s.conn.Query(ctx, problemEventRefsSQL, chDateTime64Arg(since), services, problemEventRefsCap)
	if err != nil {
		log.Printf("[problem-events] olay sayımı okunamadı (sayısız devam): %v", err)
		return problems
	}
	defer rows.Close()
	var refs []problemEventRef
	for rows.Next() {
		var r problemEventRef
		var st, ls time.Time
		if err := rows.Scan(&r.Service, &r.Detector, &st, &ls, &r.ProblemID); err != nil {
			log.Printf("[problem-events] olay sayımı satırı okunamadı (sayısız devam): %v", err)
			return problems
		}
		r.StartedAt, r.LastSeen = st.UnixNano(), ls.UnixNano()
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[problem-events] olay sayımı okunamadı (sayısız devam): %v", err)
		return problems
	}
	stats := countProblemEvents(cands, refs)
	for i := range problems {
		if st, ok := stats[problems[i].ID]; ok {
			problems[i].EventCount, problems[i].EventDetectors = st.Events, st.Detectors
		}
	}
	return problems
}

// problemCanHaveEvents — SAF: satırın üyesi olabilir mi. Yalnız servis özneli,
// SAHİP olabilen aileler (açık ya da kapanmış: çizelge olay sonrası da
// okunur). Detay sayfası çizelgeyi her alarm problemi için ister; üyesi
// olamayacak satırda ml_events okunmaz.
func problemCanHaveEvents(p Problem) bool {
	return p.ID != "" && p.Service != "" && ProblemSubjectKind(p.Kind) == ProblemKindService && OwnerEligibleRule(p.RuleID)
}

// ProblemEvents — bir Problem'in olay çizelgesi (zaman sıralı; ≤ tavan).
// Tek servis, Problem başlangıcı − 30 dk'dan beri; üye dedektörleri dahil.
func (s *Store) ProblemEvents(ctx context.Context, p Problem) ([]MLEvent, error) {
	if !problemCanHaveEvents(p) {
		return nil, nil // üyesi olamayacak satır (alarm kuralı, dış kaynak …): okuma yok
	}
	since := time.Unix(0, p.StartedAt).Add(-ProblemEventSlack)
	if floor := time.Now().Add(-30 * 24 * time.Hour); since.Before(floor) {
		since = floor
	}
	evs, err := s.ListMLEvents(ctx, MLEventsFilter{Service: p.Service, Since: since, Limit: problemEventsReadCap, Members: true})
	if err != nil {
		return nil, err
	}
	return ProblemEventsOf(evs, p, ProblemEventSlack), nil
}
