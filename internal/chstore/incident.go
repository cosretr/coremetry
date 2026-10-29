package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Incident is the high-level "something is going wrong" container — a
// declared event the oncall acknowledges, drives to resolution, and
// optionally writes a postmortem against. Multiple Problems / monitor
// flips auto-attach to one Incident when they share the same service +
// severity in a short window, so the oncall has a single page to drive
// the response from.

type Incident struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Severity   string `json:"severity"` // info | warning | critical
	Status     string `json:"status"`   // open | acknowledged | resolved
	Service    string `json:"service,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Assignee   string `json:"assignee,omitempty"`
	Postmortem string `json:"postmortem,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	AckAt      *int64 `json:"ackAt,omitempty"`
	ResolvedAt *int64 `json:"resolvedAt,omitempty"`
	UpdatedAt  int64  `json:"updatedAt"`
	// Clusters — same pattern as Problem.Clusters: enriched
	// at read time from recent span activity for the
	// incident's primary service. Empty when the service
	// isn't tagged with cluster attrs. Multi-cluster
	// incidents render with multiple chips.
	Clusters []string `json:"clusters,omitempty"`
	// RootCause (v0.10.698) — bağlı problemlerin kalıcı hipotezlerinden
	// en yüksek güvenli TopSuspect; okuma-anı (incident_rootcause.go).
	// nil = adı olan / eşiği aşan hipotez yok → FE "—".
	RootCause *RootCauseSummary `json:"rootCause,omitempty"`
	// Priority/PriorityReason (v0.10.796) — okuma-anı (incident_priority.go):
	// ilan edilen şiddet → P1/P2/P3, acknowledged bir basamak düşer. Inbox
	// ile aynı işlev; DB kolonu yok.
	Priority       string `json:"priority,omitempty"`
	PriorityReason string `json:"priorityReason,omitempty"`
	// ProblemCount/UnresolvedProblems (v0.10.797) — okuma-anı, yalnız liste
	// (incident_problem_counts.go): bağlı problem toplamı ve çözülmemişi;
	// nil = zenginleştirilmedi (detay/eski sunucu) → FE "—".
	ProblemCount       *int `json:"problemCount,omitempty"`
	UnresolvedProblems *int `json:"unresolvedProblems,omitempty"`
}

type IncidentEvent struct {
	IncidentID string `json:"incidentId"`
	Time       int64  `json:"time"` // unix ns
	Kind       string `json:"kind"` // created | ack | resolved | note | problem_attached | problem_resolved
	Actor      string `json:"actor,omitempty"`
	Body       string `json:"body,omitempty"`
	RefID      string `json:"refId,omitempty"`
}

type IncidentFilter struct {
	// ID narrows to exactly one incident (v0.9.332). Exists so GetIncident
	// can ask the database for the row it wants instead of paging the newest
	// N and scanning in Go — see the comment there.
	ID     string
	Status string
	// Services (v0.9.353) — strict service allowlist, in SQL. Same contract
	// as the anomaly filter: nil = no constraint, EMPTY = match nothing.
	// STRICT IN: under a team filter a service-less (global) incident does
	// not belong to any team — matching the Go pass it backs. (The env
	// narrow's `service='' OR …` escape is a DIFFERENT contract: env keeps
	// global rows, team does not.)
	Services []string
	// NotStatuses — statuses to EXCLUDE, applied in SQL so the LIMIT bites on
	// the rows that will be shown. See ProblemFilter.NotStatuses: on an
	// install whose incident history is 99% resolved (local: 994 of the
	// newest 1000), ORDER BY started_at DESC LIMIT 300 returned almost
	// nothing open and the inbox list disagreed with its own badge — 2 vs 29.
	NotStatuses []string
	Service     string
	Severity    string
	Limit       int
}

func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var wc whereClause
	if f.ID != "" {
		wc.add("id = ?", f.ID)
	}
	if f.Status != "" {
		wc.add("status = ?", f.Status)
	}
	if len(f.NotStatuses) > 0 {
		wc.add("status NOT IN ("+chPlaceholders(len(f.NotStatuses))+")", toAnySlice(f.NotStatuses)...)
	}
	if f.Services != nil {
		if len(f.Services) == 0 {
			wc.add("1 = 0")
		} else {
			wc.add("service IN ("+chPlaceholders(len(f.Services))+")", toAnySlice(f.Services)...)
		}
	}
	if f.Service != "" {
		wc.add("service = ?", f.Service)
	}
	if f.Severity != "" {
		wc.add("severity = ?", f.Severity)
	}
	rows, err := s.conn.Query(ctx, `
		SELECT id, title, severity, status, service, summary, assignee, postmortem,
		       toUnixTimestamp64Nano(started_at),
		       toUnixTimestamp64Nano(ack_at),
		       toUnixTimestamp64Nano(resolved_at),
		       toUnixTimestamp64Nano(updated_at)
		FROM incidents FINAL `+wc.sql()+`
		ORDER BY started_at DESC
		LIMIT ?`, append(wc.args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var i Incident
		var ack, resolved *int64
		if err := rows.Scan(&i.ID, &i.Title, &i.Severity, &i.Status, &i.Service,
			&i.Summary, &i.Assignee, &i.Postmortem,
			&i.StartedAt, &ack, &resolved, &i.UpdatedAt); err != nil {
			return nil, err
		}
		if ack != nil && *ack > 0 {
			i.AckAt = ack
		}
		if resolved != nil && *resolved > 0 {
			i.ResolvedAt = resolved
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// CountIncidentsByStatus (v0.9.456, dürüstlük A4) — /incidents sayfa
// başlığındaki open/ack/resolved sayıları SQL'den: eskiden en-yeni-200
// penceresinden sayılıyordu; 200'ün dışında kalan eski açık incident
// hem listeden hem sayımdan kayboluyor, /inbox rozetiyle çelişiyordu
// (inbox'ın v0.9.321'de kapattığı "2 vs 29" sınıfının sayfa kalıntısı).
// Service daraltması listeyle aynı conjunct; status daraltması BİLEREK
// yok — sayılar sekme chip'leridir, tüm durumları kapsar.
func (s *Store) CountIncidentsByStatus(ctx context.Context, service string) (map[string]uint64, error) {
	var wc whereClause
	if service != "" {
		wc.add("service = ?", service)
	}
	rows, err := s.conn.Query(ctx, `
		SELECT status, count()
		FROM incidents FINAL `+wc.sql()+`
		GROUP BY status
		SETTINGS max_execution_time = 5`, wc.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var st string
		var n uint64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// CountIncidentsNotInStatuses returns how many incidents are NOT in the given
// statuses — the /inbox badge's fourth term (v0.9.321).
//
// v0.9.322: exclusion rather than an allow-list, so it classifies every
// status exactly as the list's Go keeper does. See ProblemFilter.NotStatuses.
//
// The badge has to count the same rows the list shows or the two disagree,
// which is the bug this codebase keeps re-fixing (v0.9.219 did it for env).
// COUNT-only on a small state table with FINAL, run in parallel with the
// other three terms.
//
// envServices follows the shared contract: nil = unscoped, empty = the env
// resolved to no services (only service-less rows count), otherwise
// membership. Incidents CAN be service-less — an incident declared across
// several services has no single owner — so unlike exception groups the
// empty case is a real query, not a short-circuit to zero.
func (s *Store) CountIncidentsNotInStatuses(ctx context.Context, exclude []string, envServices []string) (uint64, error) {
	args := toAnySlice(exclude)
	statusSQL := "1"
	if len(exclude) > 0 {
		statusSQL = "status NOT IN (" + chPlaceholders(len(exclude)) + ")"
	}
	envSQL := ""
	if envServices != nil {
		if len(envServices) == 0 {
			envSQL = " AND service = ''"
		} else {
			envSQL = " AND (service = '' OR service IN (?))"
			args = append(args, envServices)
		}
	}
	var n uint64
	row := s.conn.QueryRow(ctx,
		`SELECT count() FROM incidents FINAL WHERE `+statusSQL+envSQL+
			` SETTINGS max_execution_time = 5`, args...)
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// GetIncident returns one incident by id, or (nil, nil) when it genuinely
// does not exist.
//
// v0.9.332 — this used to fetch the newest 1000 incidents and linear-scan
// them in Go. Any incident outside that window was INVISIBLE and the function
// returned (nil, nil) — indistinguishable from "deleted". Locally there are
// 7,539 incidents, so ~87% of them could not be fetched at all; prod is
// deeper still.
//
// Everything that reads one incident was affected, and all of it failed
// quietly: the auto-resolve cascade skipped old incidents (`inc == nil →
// continue`), which is why the orphan fix in this same release closed them
// one at a time instead of all at once; Acknowledge / Resolve / Update
// answered 404 on anything older; the detail page opened empty.
//
// It now asks the database for the row it wants. Same scan code as the list
// (one filter field) rather than a second hand-rolled query that could drift.
func (s *Store) GetIncident(ctx context.Context, id string) (*Incident, error) {
	if id == "" {
		return nil, nil
	}
	rows, err := s.ListIncidents(ctx, IncidentFilter{ID: id, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// UpsertIncident takes a pointer so auto-generated IDs flow back to the
// caller (matches the monitor/dashboard pattern).
func (s *Store) UpsertIncident(ctx context.Context, i *Incident) error {
	if i.ID == "" {
		i.ID = randHex(8)
	}
	if i.Status == "" {
		i.Status = "open"
	}
	if i.Severity == "" {
		i.Severity = "warning"
	}
	now := time.Now()
	startedAt := time.Unix(0, i.StartedAt).UTC()
	if i.StartedAt == 0 {
		startedAt = now.UTC()
		i.StartedAt = startedAt.UnixNano()
	}
	var ack, resolved *time.Time
	if i.AckAt != nil {
		t := time.Unix(0, *i.AckAt).UTC()
		ack = &t
	}
	if i.ResolvedAt != nil {
		t := time.Unix(0, *i.ResolvedAt).UTC()
		resolved = &t
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO incidents
		(id, title, severity, status, service, summary, assignee, postmortem,
		 started_at, ack_at, resolved_at, updated_at, version)`)
	if err != nil {
		return err
	}
	if err := batch.Append(i.ID, i.Title, i.Severity, i.Status, i.Service,
		i.Summary, i.Assignee, i.Postmortem,
		startedAt, ack, resolved,
		now.UTC(), uint64(now.UnixNano())); err != nil {
		return err
	}
	i.UpdatedAt = now.UnixNano()
	return batch.Send()
}

func (s *Store) AppendIncidentEvent(ctx context.Context, e IncidentEvent) error {
	batch, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO incident_events (incident_id, time, kind, actor, body, ref_id)`)
	if err != nil {
		return err
	}
	t := time.Unix(0, e.Time).UTC()
	if e.Time == 0 {
		t = time.Now().UTC()
	}
	if err := batch.Append(e.IncidentID, t, e.Kind, e.Actor, e.Body, e.RefID); err != nil {
		return err
	}
	return batch.Send()
}

func (s *Store) IncidentTimeline(ctx context.Context, incidentID string) ([]IncidentEvent, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT incident_id, toUnixTimestamp64Nano(time), kind, actor, body, ref_id
		FROM incident_events
		WHERE incident_id = ?
		ORDER BY time ASC`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IncidentEvent{}
	for rows.Next() {
		var e IncidentEvent
		if err := rows.Scan(&e.IncidentID, &e.Time, &e.Kind, &e.Actor, &e.Body, &e.RefID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// NeighborProvider is a 1-hop adjacency lookup the correlator
// passes in. Optional — when nil, only same-service grouping
// applies (legacy behaviour).
type NeighborProvider interface {
	Neighbors(service string) []string
}

// SetNeighborProvider registers the topology-aware lookup the
// auto-attach path uses for rule 3 (1-hop neighbour grouping).
// Call once at startup with the correlator.Correlator instance;
// nil disables topological clustering, leaving same-service
// grouping intact. The store keeps its own reference so the
// existing AttachProblemToIncident call sites (evaluator,
// anomaly, monitor) don't need to thread the provider through
// their constructors.
func (s *Store) SetNeighborProvider(np NeighborProvider) {
	s.neighborProvider = np
}

// AttachProblemToIncident either finds an existing OPEN incident
// matching this problem's service or a topological neighbour
// within `groupingWindow`, OR creates a new incident headed by
// this problem. Idempotent.
//
// Grouping rules (in priority order):
//
//  1. Already attached to a non-resolved incident? Return it
//     (v0.10.1580: a binding to a resolved incident is rewritten).
//  2. Open incident with matching service ACTIVE in the window
//     (v0.10.1581: greatest(started_at, updated_at); attaching
//     refreshes updated_at) — even
//     if the severities differ. Critical p99 and warning
//     error_rate on the same service are one incident, not two.
//  3. Open incident on a 1-hop topological neighbour of this
//     problem's service (caller or callee in sampled traces) in
//     the window. Catches cascading failures: an upstream
//     saturation alert and a downstream timeout alert end up
//     under one incident the oncall drives end-to-end.
//  4. Else: create a new incident.
//
// Topology (3) requires a NeighborProvider — pass nil to fall
// back to the rule-2 same-service-only behaviour.
func (s *Store) AttachProblemToIncident(ctx context.Context, p Problem) (*Incident, error) {
	return s.AttachProblemToIncidentWith(ctx, p, s.neighborProvider)
}

// AttachProblemToIncidentWith is the topology-aware variant — the
// correlator is wired here so non-correlator call sites stay
// untouched. The base AttachProblemToIncident calls this with
// nil for the provider.
func (s *Store) AttachProblemToIncidentWith(ctx context.Context, p Problem, np NeighborProvider) (*Incident, error) {

	// Already attached?
	row := s.conn.QueryRow(ctx,
		`SELECT incident_id FROM incident_problems FINAL WHERE problem_id = ? LIMIT 1`, p.ID)
	var existingID string
	if err := row.Scan(&existingID); err == nil && existingID != "" {
		// v0.10.1580 — bağ yalnız incident çözülmemişse yeniden kullanılır.
		// Sabit kimlikli aile (anomaly-auto:, anomaly-cluster: …) yeniden
		// açıldığında bağ ÇÖZÜLMÜŞ incident'ı gösterebilir; o zaman kural
		// 2/3/yeni incident'a düşülür ve aşağıdaki bağ yazımı (yeni version)
		// eski bağın yerini alır. Çözülmüş incident yeniden AÇILMAZ.
		inc, err := s.GetIncident(ctx, existingID)
		if err != nil {
			return nil, err
		}
		if reuseIncidentBinding(inc) {
			return inc, nil
		}
	}

	// Rule 2: same service, ignore severity. Multiple severity
	// levels on the same service share an incident.
	//
	// v0.10.1581 — pencere başlangıca değil ETKİNLİĞE bakar: çözülmemiş VE
	// son 30 dk'da etkin (greatest(started_at, updated_at)). Öncesi
	// started_at >= now−30m idi; 30 dk'dan uzun süren bir incident yeni
	// aynı-servis / komşu problemleri alamıyor, her biri yeni incident
	// açıyordu. Etkinlik sinyali updated_at: bağlanma (aşağıda) onu
	// tazeler, operatör eylemleri (ack/atama/postmortem) de; 30 dk sessiz
	// incident yeni problemi emmez (sonsuz kova olmaz). Yeni şema yok.
	cutoff := time.Now().Add(-incidentGroupingWindow).UTC()
	matched := s.matchIncidentSameService(ctx, p.Service, cutoff)

	// topologicallyMatched marks rule-3 wins so the timeline event
	// records "clustered via service topology" instead of the
	// generic same-service note.
	var topologicallyMatched bool

	// Rule 3: 1-hop topological neighbour. Only consult when no
	// same-service incident matched, since same-service is
	// strictly more specific.
	if matched == "" && np != nil {
		matched = s.matchIncidentAmong(ctx, np.Neighbors(p.Service), cutoff)
		if matched != "" {
			topologicallyMatched = true
		}
	}

	var inc Incident
	if matched != "" {
		got, err := s.GetIncident(ctx, matched)
		if err != nil || got == nil {
			matched = ""
		} else {
			refreshed, err := s.refreshIncidentOnAttach(ctx, *got, p)
			if err != nil {
				return nil, err
			}
			inc = refreshed
		}
	}
	if matched == "" {
		created, err := s.createIncidentFor(ctx, p)
		if err != nil {
			return nil, err
		}
		inc = *created
	}
	if err := s.bindProblemToIncident(ctx, p, inc.ID, topologicallyMatched); err != nil {
		return &inc, err
	}
	return &inc, nil
}

// incidentGroupingWindow — kural 2/3'ün etkinlik penceresi (v0.10.1581).
const incidentGroupingWindow = 30 * time.Minute

// matchIncidentSameService — kural 2'nin eşleme okuması (v0.10.1588'de
// AttachProblemToIncidentWith'ten çıkarıldı; SQL metni aynı). "" = eşleşme yok
// (okuma hatası da yok sayılır — bugünkü `_ = Scan` davranışı).
func (s *Store) matchIncidentSameService(ctx context.Context, service string, cutoff time.Time) string {
	matchRow := s.conn.QueryRow(ctx, `
		SELECT id FROM incidents FINAL
		WHERE status != 'resolved'
		  AND service  = ?
		  AND greatest(started_at, updated_at) >= ?
		ORDER BY greatest(started_at, updated_at) DESC
		LIMIT 1`, service, cutoff)
	var matched string
	_ = matchRow.Scan(&matched)
	return matched
}

// matchIncidentAmong — kural 3'ün eşleme okuması (v0.10.1588'de çıkarıldı;
// SQL metni aynı). Boş liste → okuma yok, "".
func (s *Store) matchIncidentAmong(ctx context.Context, services []string, cutoff time.Time) string {
	if len(services) == 0 {
		return ""
	}
	holders := make([]string, len(services))
	args := make([]any, 0, len(services)+1)
	for i, n := range services {
		holders[i] = "?"
		args = append(args, n)
	}
	args = append(args, cutoff)
	ngRow := s.conn.QueryRow(ctx, `
				SELECT id FROM incidents FINAL
				WHERE status != 'resolved'
				  AND service IN (`+strings.Join(holders, ",")+`)
				  AND greatest(started_at, updated_at) >= ?
				ORDER BY greatest(started_at, updated_at) DESC
				LIMIT 1`, args...)
	var matched string
	_ = ngRow.Scan(&matched)
	return matched
}

// refreshIncidentOnAttach — eşleşen incident'a bağlanma: şiddet yalnız yukarı,
// satır her bağlanmada yeniden yazılır (updated_at = etkinlik). v0.10.1588'de
// çıkarıldı; davranış aynı.
func (s *Store) refreshIncidentOnAttach(ctx context.Context, inc Incident, p Problem) (Incident, error) {
	// v0.10.802 (dış skill denetimi I1, oncall-irm / incident-response:
	// incident şiddeti alert grubuyla yaşar, SEV yeniden değerlendirilir;
	// operatör onayı). Öncesi şiddet ilk problemden bir kez yazılıyor,
	// sonra bağlanan critical problem warning incident'ı YÜKSELTMİYORDU:
	// bildirim P2'de, Inbox P2'de, liste warning pilinde kalıyordu.
	// Yalnız yukarı: düşürme yok (P1 yapışkanlık ilkesiyle aynı yön).
	// Tam-satır upsert (inc GetIncident kopyası — invariant #4).
	// isKnownSeverity: severityRank bilinmeyeni warning sayar (2) — boş
	// ya da yabancı bir şiddet info incident'ı "" ile ezmesin.
	//
	// v0.10.1581 — şiddet yükselmese de satır yeniden yazılır: bağlanma
	// incident'ın ETKİNLİĞİDİR ve updated_at'i tazeler (kural 2/3'ün
	// eşleme penceresi buna bakar). Tam-satır kopya, tek yazım.
	old := inc.Severity
	raised := isKnownSeverity(p.Severity) && severityRank(p.Severity) > severityRank(inc.Severity)
	if raised {
		inc.Severity = p.Severity
	}
	if err := s.UpsertIncident(ctx, &inc); err != nil {
		return inc, fmt.Errorf("refresh incident on attach: %w", err)
	}
	if raised {
		_ = s.AppendIncidentLifecycle(ctx, inc, IncidentEvent{
			IncidentID: inc.ID, Kind: "severity_raised", Actor: "system",
			Body:  old + " → " + p.Severity + " (" + p.RuleName + ")",
			RefID: p.ID,
		})
	}
	return inc, nil
}

// createIncidentFor — kural 4: problemin başlattığı yeni incident (v0.10.1588'de
// çıkarıldı; davranış aynı).
func (s *Store) createIncidentFor(ctx context.Context, p Problem) (*Incident, error) {
	// Create new incident headed by this problem.
	title := p.RuleName
	if p.Service != "" {
		title = p.Service + " — " + p.RuleName
	}
	inc := Incident{
		Title:     title,
		Severity:  p.Severity,
		Status:    "open",
		Service:   p.Service,
		Summary:   p.Description,
		StartedAt: p.StartedAt,
	}
	if err := s.UpsertIncident(ctx, &inc); err != nil {
		return nil, fmt.Errorf("create incident: %w", err)
	}
	// v0.10.748 — yaşam döngüsü kancası (incident bildirimi).
	_ = s.AppendIncidentLifecycle(ctx, inc, IncidentEvent{
		IncidentID: inc.ID, Kind: "created", Actor: "system",
		Body: "Auto-created from " + p.RuleName,
	})
	return &inc, nil
}

// bindProblemToIncident — problem → incident bağı + zaman çizelgesi satırı
// (v0.10.1588'de çıkarıldı; SQL ve metin aynı).
func (s *Store) bindProblemToIncident(ctx context.Context, p Problem, incidentID string, topological bool) error {
	// Bind problem → incident.
	batch, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO incident_problems (problem_id, incident_id, attached_at, version)`)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := batch.Append(p.ID, incidentID, now, uint64(now.UnixNano())); err != nil {
		return err
	}
	if err := batch.Send(); err != nil {
		return err
	}

	body := p.RuleName
	if topological {
		// Tag the timeline so the operator sees this incident
		// absorbed a problem from a *neighbour* service, not the
		// same one. Useful when triaging a cascading outage:
		// "incident on api-gateway, but the third event was a
		// payment-service alert clustered in via topology."
		body = p.RuleName + " [clustered via service topology — " + p.Service + "]"
	}
	_ = s.AppendIncidentEvent(ctx, IncidentEvent{
		IncidentID: incidentID,
		Kind:       "problem_attached",
		Actor:      "system",
		Body:       body,
		RefID:      p.ID,
	})
	return nil
}

// reuseIncidentBinding — SAF (v0.10.1580): kural 1'in mevcut bağı
// döndürme koşulu. Satırı okunamayan (silinmiş) ya da çözülmüş incident'a
// bağ yeniden kullanılmaz.
func reuseIncidentBinding(inc *Incident) bool {
	return inc != nil && inc.Status != "resolved"
}

// isKnownSeverity — SAF (v0.10.802): yalnız üç bilinen seviye; şiddet
// yükseltme kapısı bunu şart koşar (severityRank bilinmeyeni warning sayar).
func isKnownSeverity(s string) bool {
	return s == "critical" || s == "warning" || s == "info"
}

// IncidentProblems lists all problem ids attached to an incident.
func (s *Store) IncidentProblems(ctx context.Context, incidentID string) ([]string, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT problem_id FROM incident_problems FINAL WHERE incident_id = ?`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// getProblemIncidentSQL — v0.10.1288 (AI SRE Faz 2, zaman çizelgesi):
// problemin bağlı olduğu incident ve bağlanma anı. ORDER BY problem_id nokta
// okuması (FINAL), LIMIT 1 + 3 s tavan.
const getProblemIncidentSQL = `SELECT incident_id, toUnixTimestamp64Nano(attached_at)
		FROM incident_problems FINAL
		WHERE problem_id = ?
		LIMIT 1
		SETTINGS max_execution_time = 3`

// GetProblemIncident — problemin incident'ı ("" = bağlı değil) + bağlanma anı
// (unix ns). Salt okuma; AI SRE zaman çizelgesinin "incident'a bağlandı" olayı.
func (s *Store) GetProblemIncident(ctx context.Context, problemID string) (string, int64, error) {
	rows, err := s.conn.Query(ctx, getProblemIncidentSQL, problemID)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	var id string
	var at int64
	if rows.Next() {
		if err := rows.Scan(&id, &at); err != nil {
			return "", 0, err
		}
	}
	return id, at, rows.Err()
}

// OpenIncidentRollup summarises one open incident's attached-problem state for
// the cascade-resolution sweep (v0.7.33).
type OpenIncidentRollup struct {
	ID            string
	ProblemCount  int
	Unresolved    int
	MaxResolvedAt int64 // unix ns of the latest attached-problem resolution; 0 if none
	// StartedAt (v0.9.332) lets the cascade tell a freshly-created incident
	// whose problem has not attached YET from one that has been sitting
	// unattached for days. Without it, resolving on problemCount == 0 would
	// race every creation.
	StartedAt int64
}

// OpenIncidentRollups returns, for every OPEN incident, the number of attached
// problems, how many are NOT yet resolved, and the latest problem-resolution
// time. The evaluator's cascade sweep uses this to auto-resolve incidents whose
// problems have ALL cleared — operator-reported: problems auto-resolve but
// incidents stayed open forever (CH ground truth: 214 problems resolved / 0
// open, yet 57 incidents open / 0 resolved). One aggregate query, not N
// per-incident lookups, keeps the 1-minute sweep cheap; the state tables are
// small and the joins are LIMIT- + time-bounded.
func (s *Store) OpenIncidentRollups(ctx context.Context) ([]OpenIncidentRollup, error) {
	// v0.9.332 — LEFT JOIN, and acknowledged incidents included.
	//
	// Both joins were INNER, so an incident with no attached problem — or one
	// whose attached problems had aged out of `problems` — produced NO ROW
	// here at all. The cascade never saw it, so it could never resolve it,
	// so it stayed open forever. Measured locally: 26 of 32 open incidents
	// were in exactly that state, the oldest four days old. That is the
	// mechanism behind the operator's "çok fazla incident var, yanıltıcı
	// oluyor" — they accumulate and nothing can ever close them.
	//
	// `status = 'open'` also excluded ACKNOWLEDGED incidents: someone picking
	// one up made it immortal too. Acknowledging says "I'm looking", not
	// "never close this"; the set now matches inboxKeepsIncident and the
	// badge (anything not resolved).
	rows, err := s.conn.Query(ctx, `
		SELECT i.id AS id,
		       toInt64(toUnixTimestamp64Nano(i.started_at))                  AS started,
		       toInt32(countIf(p.id != ''))                                  AS n,
		       toInt32(countIf(p.id != '' AND p.status != 'resolved'))        AS unresolved,
		       ifNull(max(toUnixTimestamp64Nano(p.resolved_at)), toInt64(0)) AS max_resolved
		FROM incidents i FINAL
		LEFT JOIN incident_problems ip FINAL ON ip.incident_id = i.id
		LEFT JOIN problems p FINAL ON p.id = ip.problem_id
		WHERE i.status != 'resolved'
		GROUP BY i.id, i.started_at
		LIMIT 5000
		SETTINGS max_execution_time = 15, distributed_product_mode = 'global'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpenIncidentRollup
	for rows.Next() {
		var ro OpenIncidentRollup
		var n, unresolved int32
		if err := rows.Scan(&ro.ID, &ro.StartedAt, &n, &unresolved, &ro.MaxResolvedAt); err != nil {
			return nil, err
		}
		ro.ProblemCount = int(n)
		ro.Unresolved = int(unresolved)
		out = append(out, ro)
	}
	return out, rows.Err()
}

// JoinOpenIncident — v0.10.1588 (Davis paritesi FAZ 0.5 / D, operatör kararı
// 7: "tekil exception grubu yalnız aynı servis/komşuda AÇIK incident varsa
// katılır, kendi incident'ını açmaz"). AttachProblemToIncidentWith'in
// açmayan kardeşi; aynı kurallar, aynı okumalar:
//
//  1. mevcut bağ çözülmemiş incident'ı gösteriyorsa o (reuseIncidentBinding);
//  2. services'ten herhangi birinde çözülmemiş VE son 30 dk'da etkin incident;
//  3. services'in 1-hop komşularında (joinCandidateNeighbours) aynısı;
//  4. değilse HİÇBİR ŞEY — yeni incident AÇILMAZ, (nil, false, nil).
//
// services boş → okuma yok. Eşleşmede şiddet yalnız yukarı + updated_at
// tazelenir ve bağ yazılır (refreshIncidentOnAttach + bindProblemToIncident).
func (s *Store) JoinOpenIncident(ctx context.Context, p Problem, services []string) (*Incident, bool, error) {
	return s.JoinOpenIncidentWith(ctx, p, services, s.neighborProvider)
}

// JoinOpenIncidentWith — komşu sağlayıcısı enjekte edilebilir hâli (test).
func (s *Store) JoinOpenIncidentWith(ctx context.Context, p Problem, services []string, np NeighborProvider) (*Incident, bool, error) {
	selves := dedupNonEmpty(services)
	if len(selves) == 0 {
		return nil, false, nil
	}
	row := s.conn.QueryRow(ctx,
		`SELECT incident_id FROM incident_problems FINAL WHERE problem_id = ? LIMIT 1`, p.ID)
	var existingID string
	if err := row.Scan(&existingID); err == nil && existingID != "" {
		inc, err := s.GetIncident(ctx, existingID)
		if err != nil {
			return nil, false, err
		}
		if reuseIncidentBinding(inc) {
			return inc, true, nil
		}
	}
	cutoff := time.Now().Add(-incidentGroupingWindow).UTC()
	matched := s.matchIncidentAmong(ctx, selves, cutoff)
	topological := false
	if matched == "" {
		matched = s.matchIncidentAmong(ctx, joinCandidateNeighbours(selves, np), cutoff)
		topological = matched != ""
	}
	if matched == "" {
		return nil, false, nil
	}
	got, err := s.GetIncident(ctx, matched)
	if err != nil || got == nil {
		return nil, false, err
	}
	inc, err := s.refreshIncidentOnAttach(ctx, *got, p)
	if err != nil {
		return nil, false, err
	}
	if err := s.bindProblemToIncident(ctx, p, inc.ID, topological); err != nil {
		return &inc, false, err
	}
	return &inc, true, nil
}

// joinCandidateNeighbours — SAF (v0.10.1588): servislerin 1-hop komşularının
// birleşimi, ilk görülme sırasıyla, tekrarsız, servislerin kendileri ve boş
// adlar hariç. Sağlayıcı yoksa nil.
func joinCandidateNeighbours(services []string, np NeighborProvider) []string {
	if np == nil {
		return nil
	}
	selves := dedupNonEmpty(services)
	self := make(map[string]bool, len(selves))
	for _, s := range selves {
		self[s] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range selves {
		for _, n := range np.Neighbors(s) {
			if n == "" || self[n] || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// dedupNonEmpty — SAF: boş adlar atılır, ilk görülme sırası korunur.
func dedupNonEmpty(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
