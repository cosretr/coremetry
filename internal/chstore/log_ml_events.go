package chstore

// log_ml_events.go — ES log ML katmanı dilim 0 (docs/audits/log-ml-audit.md).
// Tablo store.go `log_ml_events` (RMT(version), ORDER BY id, FINAL).
// anomaly_events'ten AYRI: hiçbir eski okuyucu (Inbox, fusion, rootcause,
// terfi) bu satırları görmez (K1). Geri bildirim mevcut anomaly_verdicts
// tablosunda, event_id = LogMLVerdictID(id) ("logml:" öneki) — ayrı tablo
// açılmadı (operatör: mevcut altyapı varsa onu kullan). Karne = bu iki
// tablonun 30 günlük birleşimi, dedektör başına.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	LogMLSourceName   = "log_ml"
	LogMLStageShadow  = "shadow"
	LogMLStagePromote = "promoted"
	logMLVerdictPref  = "logml:"
	// LogMLListCap — liste ucu tavanı (events ucuyla aynı: 200).
	LogMLListCap = 200
)

// LogMLVerdictID — anomaly_verdicts.event_id anahtarı. Önek, eski
// anomali kararlarıyla çakışmayı (aynı sha1 türevi) keser.
func LogMLVerdictID(eventID string) string { return logMLVerdictPref + eventID }

// IsLogMLVerdictID — SAF; karne SQL'i LIKE ile aynı öneki süzer.
func IsLogMLVerdictID(id string) bool { return strings.HasPrefix(id, logMLVerdictPref) }

// LogMLEvent — bir dedektör olayı (servis × pencere). Evidence JSON:
// dedektöre özgü (influencer, taban kovaları…); şema sabit değil.
type LogMLEvent struct {
	ID        string  `json:"id"`
	Detector  string  `json:"detector"`
	Source    string  `json:"source"`
	Stage     string  `json:"stage"`
	Service   string  `json:"service"`
	Level     string  `json:"level,omitempty"`
	Pod       string  `json:"pod,omitempty"`
	Code      string  `json:"code,omitempty"`
	StartedAt int64   `json:"startedAt"` // unix ns
	LastSeen  int64   `json:"lastSeen"`  // unix ns
	WindowSec uint32  `json:"windowSec"`
	Baseline  float64 `json:"baseline"`
	Observed  float64 `json:"observed"`
	Score     float64 `json:"score"`
	Direction string  `json:"direction"` // up | down
	Evidence  string  `json:"evidence,omitempty"`
	DupOf     string  `json:"dupOf,omitempty"`
	// Karar (anomaly_verdicts'ten, listede zenginleştirilir).
	Verdict   string `json:"verdict,omitempty"`
	VerdictBy string `json:"verdictBy,omitempty"`
	VerdictAt int64  `json:"verdictAt,omitempty"`
	Note      string `json:"note,omitempty"`
}

// LogMLEventID — SAF: dedektör × servis × bölüm (level/pod/code) parmak izi.
// Aynı sürekli olay her tikte aynı id'ye yazar → RMT tek satır tutar.
func LogMLEventID(detector, service, part string) string {
	return FingerprintAnomaly("log_ml:"+detector, part, service)
}

// UpsertLogMLEvent — tam satır yazımı (RMT: tüm alanlar taşınır).
func (s *Store) UpsertLogMLEvent(ctx context.Context, e LogMLEvent) error {
	if e.Source == "" {
		e.Source = LogMLSourceName
	}
	if e.Stage == "" {
		e.Stage = LogMLStageShadow
	}
	if e.Direction == "" {
		e.Direction = "up"
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO log_ml_events (id, detector, source, stage, service, level, pod, code, started_at, last_seen, window_sec, baseline, observed, score, direction, evidence, dup_of)`)
	if err != nil {
		return err
	}
	if err := batch.Append(e.ID, e.Detector, e.Source, e.Stage, e.Service, e.Level, e.Pod, e.Code,
		time.Unix(0, e.StartedAt), time.Unix(0, e.LastSeen), e.WindowSec, e.Baseline, e.Observed, e.Score, e.Direction, e.Evidence, e.DupOf); err != nil {
		return err
	}
	return batch.Send()
}

// LogMLEventsFilter — liste süzgeci; boş alan = süzme yok.
type LogMLEventsFilter struct {
	Since    time.Time
	Detector string
	Service  string
	Stage    string
	Limit    int
}

// ListLogMLEvents — son olaylar (FINAL), last_seen DESC; kararlar eklenir.
func (s *Store) ListLogMLEvents(ctx context.Context, f LogMLEventsFilter) ([]LogMLEvent, error) {
	if f.Limit <= 0 || f.Limit > LogMLListCap {
		f.Limit = LogMLListCap
	}
	if f.Since.IsZero() {
		f.Since = time.Now().Add(-24 * time.Hour)
	}
	where := []string{"last_seen >= ?"}
	args := []any{chDateTime64Arg(f.Since)}
	if f.Detector != "" {
		where = append(where, "detector = ?")
		args = append(args, f.Detector)
	}
	if f.Service != "" {
		where = append(where, "service = ?")
		args = append(args, f.Service)
	}
	if f.Stage != "" {
		where = append(where, "stage = ?")
		args = append(args, f.Stage)
	}
	args = append(args, f.Limit)
	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT id, detector, source, stage, service, level, pod, code, started_at, last_seen,
		       window_sec, baseline, observed, score, direction, evidence, dup_of
		FROM log_ml_events FINAL
		WHERE %s
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`, strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogMLEvent
	var ids []string
	for rows.Next() {
		var e LogMLEvent
		var st, ls time.Time
		if err := rows.Scan(&e.ID, &e.Detector, &e.Source, &e.Stage, &e.Service, &e.Level, &e.Pod, &e.Code, &st, &ls,
			&e.WindowSec, &e.Baseline, &e.Observed, &e.Score, &e.Direction, &e.Evidence, &e.DupOf); err != nil {
			return nil, err
		}
		e.StartedAt, e.LastSeen = st.UnixNano(), ls.UnixNano()
		out = append(out, e)
		ids = append(ids, LogMLVerdictID(e.ID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if vs, verr := s.ListAnomalyVerdicts(ctx, ids); verr == nil {
			for i := range out {
				if v, ok := vs[LogMLVerdictID(out[i].ID)]; ok {
					out[i].Verdict, out[i].VerdictBy, out[i].VerdictAt, out[i].Note = v.Verdict, v.CreatedBy, v.CreatedAt, v.Note
				}
			}
		}
	}
	return out, nil
}

// LogMLDetectorScore — dedektör karnesi satırı.
type LogMLDetectorScore struct {
	Detector string  `json:"detector"`
	Events   int64   `json:"events"`  // penceredeki olay sayısı
	Samples  int64   `json:"samples"` // karar verilmiş olay sayısı
	Hits     int64   `json:"hits"`    // verdict = anomaly
	HitRate  float64 `json:"hitRate"` // Hits / Samples (Samples=0 → 0)
}

// LogMLScorecardRate — SAF: karar girilmemiş satırlar paydaya GİRMEZ (S4).
func LogMLScorecardRate(hits, samples int64) float64 {
	if samples <= 0 {
		return 0
	}
	return float64(hits) / float64(samples)
}

// LogMLScorecardFromRows — SAF: olay (id→detector) × karar (verdict id→verdict)
// → dedektör başına olay / karar / isabet. Kararsız satır paydaya girmez (S4).
func LogMLScorecardFromRows(events map[string]string, verdicts map[string]string) []LogMLDetectorScore {
	acc := map[string]*LogMLDetectorScore{}
	for id, det := range events {
		r, ok := acc[det]
		if !ok {
			r = &LogMLDetectorScore{Detector: det}
			acc[det] = r
		}
		r.Events++
		if v, ok := verdicts[LogMLVerdictID(id)]; ok && v != "" {
			r.Samples++
			if v == VerdictAnomaly {
				r.Hits++
			}
		}
	}
	out := make([]LogMLDetectorScore, 0, len(acc))
	for _, r := range acc {
		r.HitRate = LogMLScorecardRate(r.Hits, r.Samples)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	return out
}

// LogMLScorecard — son `since`'den beri dedektör başına olay / karar /
// isabet. İki ayrı okuma (JOIN/alt sorgu yok: audit 5/5b, dağıtık-güvenli),
// birleştirme Go'da (LogMLScorecardFromRows). Olay tavanı 5000 (30 g × gün
// tavanı), karar okuması 'logml:' önekiyle sınırlı.
func (s *Store) LogMLScorecard(ctx context.Context, since time.Time) ([]LogMLDetectorScore, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT id, detector FROM log_ml_events FINAL
		WHERE last_seen >= ?
		LIMIT 5000
		SETTINGS max_execution_time = 5`, chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	events := map[string]string{}
	for rows.Next() {
		var id, det string
		if err := rows.Scan(&id, &det); err != nil {
			rows.Close()
			return nil, err
		}
		events[id] = det
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	vrows, err := s.conn.Query(ctx, `
		SELECT event_id, verdict FROM anomaly_verdicts FINAL
		WHERE event_id LIKE 'logml:%' AND created_at >= ?
		LIMIT 10000
		SETTINGS max_execution_time = 5`, chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	verdicts := map[string]string{}
	for vrows.Next() {
		var id, v string
		if err := vrows.Scan(&id, &v); err != nil {
			return nil, err
		}
		verdicts[id] = v
	}
	if err := vrows.Err(); err != nil {
		return nil, err
	}
	return LogMLScorecardFromRows(events, verdicts), nil
}

// LogMLTableReady — sre pod'u DDL koşmaz (v0.10.1282); tablo api/worker
// boot'unda iner. Lider tik öncesi bunu yoklar, yoksa bekler (çökmez).
func (s *Store) LogMLTableReady(ctx context.Context) (bool, error) {
	return s.tableExists(ctx, "log_ml_events")
}
