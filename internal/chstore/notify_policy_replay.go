package chstore

// notify_policy_replay.go — v0.10.1523 (Problem standardı R1): yeni bildirim
// barının geçmiş N günde (≤ 30) ne yapacağını ölçen replay. Okumalar
// sınırlı (LIMIT + max_execution_time 20 + zaman WHERE'i), birleştirme SAF
// (FoldNotifyReplay, tablo testli). Uç: api/notify_policy_replay.go.
//
// Sözleşme — "sonra" = "önce ∩ bar": yeni bar aynı hunide EK bir süzgeç;
// önce gönderilmiş her öğe için NotifiableAt yeniden oynatılır, tutulanlar
// kapı koduyla sayılır. Gönderilmemiş öğeler yalnız recall kontrolüne girer.
//
// RECALL KONTROLÜ (D8 kriteri, %100): "Gerçek problem" işaretli her imza ve
// onaylı (status=confirmed) incident_cases satırının kaynağı yeniden oynatılır;
// bar tutuyorsa satır İŞARETLENİR — sayıdan bağımsız, her biri listede.
//
// Yaklaşıklıklar (cevapta `notes`): öncelik gönderim satırının konusundaki
// "[P1]" etiketinden (yoksa açılış anında computePriority); trafik
// açılıştan önceki tamamlanmış 5 dk kovası + bir saat önceki kova
// (service_summary_5m, tek toplu okuma, çift tavanı); exception grubu
// önceliği gönderim etiketinden; değer problemin SON değeri.

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Replay tavanları.
const (
	NotifyReplayMaxDays       = 30
	NotifyReplayDefaultDays   = 14
	notifyReplaySendsLimit    = 50000
	notifyReplayProblemsLimit = 20000
	notifyReplayTrafficPairs  = 4000
	NotifyFlapWindow          = 30 * time.Minute
	notifyReplayRecallListMax = 500
)

// NotifyShadowChannelKind — gölge karar satırının channel_kind'i
// (notification_log). Gönderim değildir: /events, problem bildirim
// geçmişi, watchers çekmecesi ve kanal sağlığı bu türü DIŞLAR.
const NotifyShadowChannelKind = "policy"

// NotifyShadowRelatedKind — gölge satırının related_kind'i. Ayrı tür:
// HasNotification / HasAnyNotification / LastNotificationAt dedup kapıları
// related_kind ile süzer, gölge satırı hiçbirini tetikleyemez.
const NotifyShadowRelatedKind = "policy"

// NotifyShadowWouldSend — gölge satırının `error` kolonundaki "gönderilirdi".
const NotifyShadowWouldSend = "would_send"

// ServiceBucket — (servis, 5 dk kova başı) anahtarı.
type ServiceBucket struct {
	Service string
	Bucket  int64 // unix sn, 300'ün katı
}

// BucketTraffic — kova ölçüsü.
type BucketTraffic struct {
	Calls  float64
	Errors float64
	P99Ms  float64
}

// Bucket5m — SAF: t'yi içeren 5 dk kovasının başı (unix sn).
func Bucket5m(t time.Time) int64 { return t.Unix() / 300 * 300 }

// TrafficPairsFor — SAF: açılış anı için okunacak iki kova: açılıştan önceki
// TAMAMLANMIŞ kova ve bir saat öncesi (referans).
func TrafficPairsFor(service string, at time.Time) (cur, base ServiceBucket) {
	b := Bucket5m(at) - 300
	return ServiceBucket{service, b}, ServiceBucket{service, b - 3600}
}

// StdTrafficFrom — SAF: iki kovadan StdTraffic (cur yoksa nil).
func StdTrafficFrom(m map[ServiceBucket]BucketTraffic, cur, base ServiceBucket) *StdTraffic {
	c, ok := m[cur]
	if !ok {
		return nil
	}
	t := &StdTraffic{Calls5m: c.Calls, Errors5m: c.Errors, P99Ms: c.P99Ms}
	if b, ok := m[base]; ok {
		t.BaselineCalls5m, t.BaselineP99Ms = b.Calls, b.P99Ms
	}
	return t
}

// serviceBucketTrafficSQL — SAF: toplu kova okuması. Çiftler tuple IN ile;
// dış zaman sınırı birincil anahtarı budar. Boş çift → "".
func serviceBucketTrafficSQL(pairs []ServiceBucket) (string, []any) {
	if len(pairs) == 0 {
		return "", nil
	}
	if len(pairs) > notifyReplayTrafficPairs {
		pairs = pairs[:notifyReplayTrafficPairs]
	}
	lo, hi := pairs[0].Bucket, pairs[0].Bucket
	tuples := make([]string, len(pairs))
	args := make([]any, 0, 2*len(pairs)+3)
	for _, p := range pairs {
		if p.Bucket < lo {
			lo = p.Bucket
		}
		if p.Bucket > hi {
			hi = p.Bucket
		}
	}
	args = append(args, chDateTimeArg(time.Unix(lo, 0)), chDateTimeArg(time.Unix(hi, 0)))
	for i, p := range pairs {
		tuples[i] = "tuple(?, toDateTime(?, 'UTC'))"
		args = append(args, p.Service, chDateTimeArg(time.Unix(p.Bucket, 0)))
	}
	args = append(args, len(pairs))
	q := `
		SELECT service_name, toUnixTimestamp(time_bucket) AS b,
		       toFloat64(countMerge(span_count_state))  AS calls,
		       toFloat64(countMerge(error_count_state)) AS errs,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99
		FROM service_summary_5m
		WHERE time_bucket >= toDateTime(?, 'UTC') AND time_bucket <= toDateTime(?, 'UTC')
		  AND (service_name, time_bucket) IN (` + strings.Join(tuples, ", ") + `)
		GROUP BY service_name, time_bucket
		LIMIT ?
		SETTINGS max_execution_time = 20`
	return q, args
}

// ServiceBucketTraffic — kova ölçüleri (bulunamayan çift haritada yok =
// bilinmiyor). truncated = çift tavanına dayandı.
func (s *Store) ServiceBucketTraffic(ctx context.Context, pairs []ServiceBucket) (map[ServiceBucket]BucketTraffic, bool, error) {
	out := map[ServiceBucket]BucketTraffic{}
	q, args := serviceBucketTrafficSQL(pairs)
	if q == "" {
		return out, false, nil
	}
	q = s.weighSQL(q, time.Time{}, "service_summary_5m") // v0.10.2452 — per-bucket groups: each bucket one side of the switch
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var svc string
		var b uint32
		var t BucketTraffic
		if err := rows.Scan(&svc, &b, &t.Calls, &t.Errors, &t.P99Ms); err != nil {
			return nil, false, err
		}
		if math.IsNaN(t.P99Ms) {
			t.P99Ms = 0
		}
		out[ServiceBucket{svc, int64(b)}] = t
	}
	return out, len(pairs) > notifyReplayTrafficPairs, rows.Err()
}

// ReplaySend — bir ilişki kimliğinin pencere içi gönderimleri.
type ReplaySend struct {
	RelatedKind  string
	Rows         int
	OK           int
	FirstSubject string
	FirstAt      int64
}

// notifyReplaySendsSQL — SAF: gerçek gönderimler (sentetik "kimseye
// gitmedi", "sustur" ve gölge satırları hariç).
func notifyReplaySendsSQL(from, to time.Time) (string, []any) {
	return `
		SELECT related_kind, related_id,
		       count()                         AS rows_n,
		       countIf(ok = 1)                 AS ok_n,
		       argMin(subject, sent_at)        AS first_subject,
		       toUnixTimestamp64Nano(min(sent_at)) AS first_at
		FROM notification_log
		WHERE sent_at >= ? AND sent_at < ?
		  AND channel_kind NOT IN ('none', 'ignore', 'policy')
		  AND related_kind IN ('problem', 'exception', 'incident', 'watcher')
		GROUP BY related_kind, related_id
		LIMIT ?
		SETTINGS max_execution_time = 20`,
		[]any{chDateTime64Arg(from), chDateTime64Arg(to), notifyReplaySendsLimit}
}

// ReplayNotificationSends — related_id → gönderim özeti.
func (s *Store) ReplayNotificationSends(ctx context.Context, from, to time.Time) (map[string]ReplaySend, bool, error) {
	q, args := notifyReplaySendsSQL(from, to)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := map[string]ReplaySend{}
	for rows.Next() {
		var kind, id, subj string
		var n, ok uint64
		var first int64
		if err := rows.Scan(&kind, &id, &n, &ok, &subj, &first); err != nil {
			return nil, false, err
		}
		out[id] = ReplaySend{RelatedKind: kind, Rows: int(n), OK: int(ok), FirstSubject: subj, FirstAt: first}
	}
	return out, len(out) >= notifyReplaySendsLimit, rows.Err()
}

// notifyReplayShadowSQL — SAF: canlı gölge satırlarının kod başına sayısı.
func notifyReplayShadowSQL(from, to time.Time) (string, []any) {
	return `
		SELECT error AS code, count() AS n
		FROM notification_log
		WHERE sent_at >= ? AND sent_at < ?
		  AND channel_kind = 'policy'
		GROUP BY code
		ORDER BY n DESC
		LIMIT 100
		SETTINGS max_execution_time = 20`,
		[]any{chDateTime64Arg(from), chDateTime64Arg(to)}
}

// ReplayShadowCounts — gölge kodu → satır sayısı (dağıtımdan sonra dolar).
func (s *Store) ReplayShadowCounts(ctx context.Context, from, to time.Time) (map[string]int, error) {
	q, args := notifyReplayShadowSQL(from, to)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var code string
		var n uint64
		if err := rows.Scan(&code, &n); err != nil {
			return nil, err
		}
		out[code] = int(n)
	}
	return out, rows.Err()
}

// notifyReplayProblemsSQL — SAF: pencerede açılmış problemler.
func notifyReplayProblemsSQL(selectExpr string, from, to time.Time) (string, []any) {
	return `
		SELECT ` + selectExpr + `
		FROM problems FINAL
		WHERE started_at >= ? AND started_at < ?
		ORDER BY started_at ASC
		LIMIT ?
		SETTINGS max_execution_time = 20`,
		[]any{chDateTime64Arg(from), chDateTime64Arg(to), notifyReplayProblemsLimit}
}

// ReplayProblems — pencerede açılmış problemler (eskiden yeniye).
func (s *Store) ReplayProblems(ctx context.Context, from, to time.Time) ([]Problem, bool, error) {
	cols := s.problemCols()
	q, args := notifyReplayProblemsSQL(problemSelectExprFor(cols), from, to)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Problem
	for rows.Next() {
		p, err := scanProblemRow(rows, cols)
		if err != nil {
			return nil, false, err
		}
		out = append(out, p)
	}
	return out, len(out) >= notifyReplayProblemsLimit, rows.Err()
}

// ── SAF birleştirme ─────────────────────────────────────────────────────

// NotifyReplayInput — FoldNotifyReplay girdisi.
type NotifyReplayInput struct {
	From, To  time.Time
	Problems  []Problem // eskiden yeniye
	Sends     map[string]ReplaySend
	Shadow    map[string]int
	Verdicts  []ProblemVerdict
	Cases     []IncidentCase // status=confirmed
	Traffic   map[ServiceBucket]BucketTraffic
	Groups    map[string]ExceptionGroup // parmak izi → grup
	Keep      []string
	Policy    NotifyPolicy
	Priority  ProblemPriorityConfig
	Calendar  AnomalyCalendarConfig
	IsBatch   func(service string) bool
	Truncated NotifyReplayTruncation
}

// NotifyReplayTruncation — hangi okuma tavana dayandı.
type NotifyReplayTruncation struct {
	Problems bool `json:"problems"`
	Sends    bool `json:"sends"`
	Traffic  bool `json:"traffic"`
}

// NotifyReplayFamily — aile başına satır.
type NotifyReplayFamily struct {
	Family         string         `json:"family"`
	Opened         int            `json:"opened"`
	SentBefore     int            `json:"sentBefore"`
	SendRowsBefore int            `json:"sendRowsBefore"`
	WouldSendAfter int            `json:"wouldSendAfter"`
	Holds          map[string]int `json:"holds"`
	FlapReopens    int            `json:"flapReopens"`
	NoiseMarked    int            `json:"noiseMarked"`
}

// NotifyReplayRecallCase — bar'ın tuttuğu "gerçek" öğe.
type NotifyReplayRecallCase struct {
	Reason    string `json:"reason"` // real_mark | confirmed_case
	ID        string `json:"id"`
	CaseID    string `json:"caseId,omitempty"`
	Signature string `json:"signature,omitempty"`
	Family    string `json:"family"`
	Headline  string `json:"headline"`
	Priority  string `json:"priority"`
	Hold      string `json:"hold"`
	StartedAt int64  `json:"startedAt"`
}

// NotifyReplayRecall — recall kontrolü.
type NotifyReplayRecall struct {
	Checked     int                      `json:"checked"`
	Held        int                      `json:"held"`
	Pass        bool                     `json:"pass"` // Held == 0
	Cases       []NotifyReplayRecallCase `json:"cases"`
	OutOfFunnel int                      `json:"outOfFunnel"` // anomali olayı işaretleri — bildirim hunisine bugün de girmez
	NotFound    int                      `json:"notFound"`    // kaynağı okunamayan işaret / vaka
}

// NotifyReplayReport — uç cevabı.
type NotifyReplayReport struct {
	From      int64                  `json:"from"`
	To        int64                  `json:"to"`
	Days      int                    `json:"days"`
	Policy    NotifyPolicy           `json:"policy"`
	Families  []NotifyReplayFamily   `json:"families"`
	Totals    NotifyReplayFamily     `json:"totals"`
	Recall    NotifyReplayRecall     `json:"recall"`
	Shadow    map[string]int         `json:"shadow"`
	Truncated NotifyReplayTruncation `json:"truncated"`
	Notes     []string               `json:"notes"`
}

// v0.10.1532 (R4) — Türkçe başlık "AÇILDI · [P1] <manşet>" da tanınır
// (notify.trChatTitle); eski "[CRITICAL][P1] …" biçimi aynen.
var subjectPrioRe = regexp.MustCompile(`(?:\]|· )\[(P[123])\]`)
var subjectSevRe = regexp.MustCompile(`^\[([A-Z]+)\]`)

// subjectPriority — SAF: "[CRITICAL][P1] svc — …" → "P1" ("" = etiketsiz).
func subjectPriority(subj string) string {
	if m := subjectPrioRe.FindStringSubmatch(subj); m != nil {
		return m[1]
	}
	return ""
}

func subjectSeverity(subj string) string {
	if m := subjectSevRe.FindStringSubmatch(subj); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// ExceptionFingerprintOfNotifyID — SAF: exception bildirim kimliğinden parmak
// izi (notify exceptionGroupID / P1 anonsu kimliği). "" = çözülemedi (Oracle
// kaynak özeti tek bir grup değildir).
func ExceptionFingerprintOfNotifyID(id string) string {
	rest := strings.TrimPrefix(id, ExceptionGroupRulePrefix)
	if strings.HasPrefix(rest, "ora-source:") {
		return ""
	}
	if strings.HasPrefix(rest, OracleGroupPrefix) {
		fp, _, _ := strings.Cut(strings.TrimPrefix(rest, OracleGroupPrefix), ":")
		return OracleGroupPrefix + fp
	}
	fp, _, _ := strings.Cut(rest, ":")
	return fp
}

type replayAcc struct {
	fams map[string]*NotifyReplayFamily
}

func (a *replayAcc) fam(name string) *NotifyReplayFamily {
	if f, ok := a.fams[name]; ok {
		return f
	}
	f := &NotifyReplayFamily{Family: name, Holds: map[string]int{}}
	a.fams[name] = f
	return f
}

// FoldNotifyReplay — SAF: replay raporu.
func FoldNotifyReplay(in NotifyReplayInput) NotifyReplayReport {
	pol := NormalizeNotifyPolicy(in.Policy)
	loc := in.Calendar.Location()
	isBatch := in.IsBatch
	if isBatch == nil {
		isBatch = func(string) bool { return false }
	}
	rep := NotifyReplayReport{From: in.From.UnixNano(), To: in.To.UnixNano(),
		Days: int(math.Round(in.To.Sub(in.From).Hours() / 24)), Policy: pol,
		Shadow: in.Shadow, Truncated: in.Truncated}
	if rep.Shadow == nil {
		rep.Shadow = map[string]int{}
	}
	acc := &replayAcc{fams: map[string]*NotifyReplayFamily{}}

	noise := map[string]ProblemVerdict{}
	real := map[string]bool{}
	for _, v := range in.Verdicts {
		switch v.Verdict {
		case ProblemVerdictNoise:
			noise[v.Signature] = v
		case ProblemVerdictReal:
			real[v.Signature] = true
		}
	}
	confirmedProblem := map[string]string{} // problem id → case id
	confirmedException := map[string]string{}
	for _, c := range in.Cases {
		if c.Status != CaseConfirmed {
			continue
		}
		if c.Source == "exception" {
			confirmedException[c.SourceID] = c.CaseID
		} else {
			confirmedProblem[c.SourceID] = c.CaseID
		}
	}
	recall := &rep.Recall
	seenReal := map[string]bool{}
	flag := func(rc NotifyReplayRecallCase) {
		recall.Held++
		if len(recall.Cases) < notifyReplayRecallListMax {
			recall.Cases = append(recall.Cases, rc)
		}
	}

	// ── Problemler (kronolojik; imza başına yeni politikanın son gönderimi) ──
	last := map[string]*NotifyRecord{}
	lastResolved := map[string]int64{}
	probs := append([]Problem(nil), in.Problems...)
	sort.SliceStable(probs, func(i, j int) bool { return probs[i].StartedAt < probs[j].StartedAt })
	for _, p := range probs {
		start := time.Unix(0, p.StartedAt)
		sig := RuleProblemVerdictSignature(p.RuleID, p.Service)
		send, sent := in.Sends[p.ID]
		sentOK := sent && send.OK > 0
		srcPrio := ""
		if sent {
			srcPrio = subjectPriority(send.FirstSubject)
		}
		if srcPrio == "" {
			srcPrio, _ = computePriority(p, p.StartedAt, in.Priority)
		}
		pp := p
		pp.Priority = srcPrio
		prio := NotifyViewPriority(pp, in.Keep)
		cur, base := TrafficPairsFor(p.Service, start)
		facts := StdFacts{Now: start.Add(5 * time.Minute), Traffic: StdTrafficFrom(in.Traffic, cur, base),
			CalendarKind: in.Calendar.SpecialKindOn(start), Batch: isBatch(p.Service),
			BigBreachRatio: in.Priority.BigBreachRatio}
		std := StandardizeProblem(pp, facts)
		hist := NotifyHistory{LastSend: last[sig]}
		if v, ok := noise[sig]; ok && sig != "" {
			vv := v
			hist.Noise = NoiseMarkOf(&vv)
		}
		dec := NotifiableAt(std, prio, hist, pol, start.In(loc))
		if dec.Notifiable && sig != "" {
			last[sig] = &NotifyRecord{At: p.StartedAt, Priority: prio, Magnitude: dec.Magnitude}
		}
		// Pencere dışı açılış (onaylı vakanın kaynağı, ayrıca okundu): yalnız
		// recall kontrolüne girer, aile sayaçlarına girmez.
		inWin := !start.Before(in.From) && start.Before(in.To)
		f := &NotifyReplayFamily{Holds: map[string]int{}}
		if inWin {
			f = acc.fam(std.Family)
		}
		f.Opened++
		key := p.RuleID + "|" + p.Service
		if r, ok := lastResolved[key]; ok && r > 0 && p.StartedAt >= r && time.Duration(p.StartedAt-r) < NotifyFlapWindow {
			f.FlapReopens++
		}
		if p.ResolvedAt != nil && *p.ResolvedAt > 0 {
			lastResolved[key] = *p.ResolvedAt
		}
		if _, ok := noise[sig]; ok && sig != "" {
			f.NoiseMarked++
		}
		if sentOK {
			f.SentBefore++
			f.SendRowsBefore += send.Rows
			if dec.Notifiable {
				f.WouldSendAfter++
			} else {
				f.Holds[dec.Hold]++
			}
		}
		reason, caseID := "", ""
		if real[sig] && sig != "" {
			reason = "real_mark"
			seenReal[sig] = true
		}
		if cid, ok := confirmedProblem[p.ID]; ok {
			reason, caseID = "confirmed_case", cid
			delete(confirmedProblem, p.ID)
		}
		if reason != "" {
			recall.Checked++
			if !dec.Notifiable {
				flag(NotifyReplayRecallCase{Reason: reason, ID: p.ID, CaseID: caseID, Signature: sig,
					Family: std.Family, Headline: std.Headline, Priority: prio, Hold: dec.Hold, StartedAt: p.StartedAt})
			}
		}
	}
	recall.NotFound += len(confirmedProblem)

	// ── Exception / incident gönderimleri ──
	ids := make([]string, 0, len(in.Sends))
	for id := range in.Sends {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return in.Sends[ids[i]].FirstAt < in.Sends[ids[j]].FirstAt })
	exLast := map[string]*NotifyRecord{}
	for _, id := range ids {
		s := in.Sends[id]
		if s.OK == 0 {
			continue
		}
		switch s.RelatedKind {
		case "exception":
			fp := ExceptionFingerprintOfNotifyID(id)
			g, ok := in.Groups[fp]
			f := acc.fam(StdFamilyException)
			f.Opened++
			f.SentBefore++
			f.SendRowsBefore += s.Rows
			if !ok {
				f.WouldSendAfter++ // grup okunamadı: tutma kanıtı yok
				continue
			}
			g.Priority = firstNonEmpty(subjectPriority(s.FirstSubject), g.Priority)
			at := time.Unix(0, s.FirstAt)
			std := StandardizeException(g, StdFacts{Now: at, CalendarKind: in.Calendar.SpecialKindOn(at), Batch: isBatch(g.Service)})
			sig := "e:" + fp
			hist := NotifyHistory{LastSend: exLast[sig]}
			if v, ok := noise[sig]; ok {
				vv := v
				hist.Noise = NoiseMarkOf(&vv)
				f.NoiseMarked++
			}
			dec := NotifiableAt(std, firstNonEmpty(g.Priority, "P3"), hist, pol, at.In(loc))
			if dec.Notifiable {
				f.WouldSendAfter++
				exLast[sig] = &NotifyRecord{At: s.FirstAt, Priority: dec.Priority, Magnitude: dec.Magnitude}
			} else {
				f.Holds[dec.Hold]++
			}
		case "incident":
			f := acc.fam(StdFamilyIncident)
			f.Opened++
			f.SentBefore++
			f.SendRowsBefore += s.Rows
			sev := subjectSeverity(s.FirstSubject)
			at := time.Unix(0, s.FirstAt)
			inc := Incident{ID: id, Severity: sev, StartedAt: s.FirstAt}
			std := StandardizeIncident(inc, StdFacts{Now: at})
			p := Problem{RuleID: "incident:" + id, Kind: NotifyKindIncident, Severity: sev,
				Priority: firstNonEmpty(subjectPriority(s.FirstSubject), "P2")}
			dec := NotifiableAt(std, NotifyViewPriority(p, in.Keep), NotifyHistory{}, pol, at.In(loc))
			if dec.Notifiable {
				f.WouldSendAfter++
			} else {
				f.Holds[dec.Hold]++
			}
		}
	}

	// ── Recall: exception kaynaklı "gerçek" işaretler + onaylı vakalar ──
	exChecked := map[string]bool{}
	checkEx := func(fp, reason, caseID, sig string) {
		if exChecked[fp+"|"+reason] {
			return
		}
		exChecked[fp+"|"+reason] = true
		g, ok := in.Groups[fp]
		if !ok {
			recall.NotFound++
			return
		}
		recall.Checked++
		at := time.Unix(0, g.LastSeen)
		std := StandardizeException(g, StdFacts{Now: at, CalendarKind: in.Calendar.SpecialKindOn(at), Batch: isBatch(g.Service)})
		dec := NotifiableAt(std, firstNonEmpty(g.Priority, "P3"), NotifyHistory{}, pol, at.In(loc))
		if !dec.Notifiable {
			flag(NotifyReplayRecallCase{Reason: reason, ID: ExceptionGroupRulePrefix + fp, CaseID: caseID, Signature: sig,
				Family: StdFamilyException, Headline: std.Headline, Priority: dec.Priority, Hold: dec.Hold, StartedAt: g.FirstSeen})
		}
	}
	for sig := range real {
		switch {
		case strings.HasPrefix(sig, "e:"):
			checkEx(strings.TrimPrefix(sig, "e:"), "real_mark", "", sig)
		case strings.HasPrefix(sig, "a:"):
			recall.OutOfFunnel++
		case !seenReal[sig]:
			// Pencerede açılmamış imza: kontrol edilecek açılış yok — sayılmaz.
		}
	}
	for fp, cid := range confirmedException {
		checkEx(fp, "confirmed_case", cid, "e:"+fp)
	}
	recall.Pass = recall.Held == 0
	sort.Slice(recall.Cases, func(i, j int) bool { return recall.Cases[i].StartedAt > recall.Cases[j].StartedAt })
	if recall.Cases == nil {
		recall.Cases = []NotifyReplayRecallCase{}
	}

	names := make([]string, 0, len(acc.fams))
	for n := range acc.fams {
		names = append(names, n)
	}
	sort.Strings(names)
	tot := NotifyReplayFamily{Family: "all", Holds: map[string]int{}}
	for _, n := range names {
		f := *acc.fams[n]
		rep.Families = append(rep.Families, f)
		tot.Opened += f.Opened
		tot.SentBefore += f.SentBefore
		tot.SendRowsBefore += f.SendRowsBefore
		tot.WouldSendAfter += f.WouldSendAfter
		tot.FlapReopens += f.FlapReopens
		tot.NoiseMarked += f.NoiseMarked
		for k, v := range f.Holds {
			tot.Holds[k] += v
		}
	}
	if rep.Families == nil {
		rep.Families = []NotifyReplayFamily{}
	}
	rep.Totals = tot
	rep.Notes = []string{
		"sonra = önce ∩ yeni bar: yalnız gönderilmiş öğeler yeniden oynatılır; tutulanlar 'holds' altında kapı koduyla",
		"p3:digest tutması bildirimin kaybı değil — günde iki kez (" + strings.Join(pol.DigestTimes, ", ") + ") özet",
		"öncelik ilk gönderimin konu etiketinden ([P1]); yoksa açılış anında öncelik merdiveni",
		"trafik açılıştan önceki tamamlanmış 5 dk kovası + bir saat önceki kova; okunamayan = bilinmiyor (tutmaz)",
		"exception / incident aileleri yalnız gönderilmiş öğeleri sayar (opened = pencerede gönderilen)",
		"shadow = canlı gölge satırlarının kod başına sayısı (problem durumu başına saatte en çok bir satır; dağıtımdan sonra dolar)",
		"recall.pass=false ise listedeki her satır enforce öncesi düzeltilmeli (D8: %100)",
	}
	return rep
}

// NotifyReplayTrafficPairs — SAF: problemler için okunacak çiftler (tekrarsız,
// sıralı, tavanlı).
func NotifyReplayTrafficPairs(probs []Problem) []ServiceBucket {
	seen := map[ServiceBucket]bool{}
	var out []ServiceBucket
	for _, p := range probs {
		if p.Service == "" {
			continue
		}
		c, b := TrafficPairsFor(p.Service, time.Unix(0, p.StartedAt))
		for _, k := range []ServiceBucket{c, b} {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bucket != out[j].Bucket {
			return out[i].Bucket > out[j].Bucket // en yeniler tavanın içinde
		}
		return out[i].Service < out[j].Service
	})
	return out
}
