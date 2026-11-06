package chstore

// ml_events.go — ES log ML katmanı dilim 0 (docs/audits/ml-audit.md).
// Tablo store.go `ml_events` (RMT(version), ORDER BY id, FINAL).
// anomaly_events'ten AYRI: hiçbir eski okuyucu (Inbox, fusion, rootcause,
// terfi) bu satırları görmez (K1). Geri bildirim mevcut anomaly_verdicts
// tablosunda, event_id = MLVerdictID(id) ("ml:" öneki) — ayrı tablo
// açılmadı (operatör: mevcut altyapı varsa onu kullan). Karne = bu iki
// tablonun 30 günlük birleşimi, dedektör başına.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MLSignalLog    = "log"
	MLSignalTrace  = "trace"
	MLSignalMetric = "metric"
	MLStageShadow  = "shadow"
	MLStagePromote = "promoted"
	MLStageClosed  = "closed"
	MLStageChronic = "chronic" // B2: tekrarlayan desen tek kayıt (epizot açmaz) // epizot kapandı (B3, v0.10.1768); last_seen = bitiş
	mlVerdictPref  = "ml:"
	// MLListCap — liste ucu tavanı (events ucuyla aynı: 200).
	MLListCap = 200
)

// MLVerdictID — anomaly_verdicts.event_id anahtarı. Önek, eski
// anomali kararlarıyla çakışmayı (aynı sha1 türevi) keser.
func MLVerdictID(eventID string) string { return mlVerdictPref + eventID }

// IsMLVerdictID — SAF; karne SQL'i LIKE ile aynı öneki süzer.
func IsMLVerdictID(id string) bool { return strings.HasPrefix(id, mlVerdictPref) }

// MLEvent — bir dedektör olayı (servis × pencere). Evidence JSON:
// dedektöre özgü (influencer, taban kovaları…); şema sabit değil.
type MLEvent struct {
	ID        string  `json:"id"`
	Detector  string  `json:"detector"`
	Signal    string  `json:"signal"` // log | trace
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

// MLEventID — SAF: dedektör × servis × bölüm (level/pod/code) parmak izi.
// Aynı sürekli olay her tikte aynı id'ye yazar → RMT tek satır tutar.
func MLEventID(detector, service, part string) string {
	return FingerprintAnomaly("ml:"+detector, part, service)
}

// MLEpisodeID — SAF: taban kimlik (MLEventID) + epizot başlangıcı → epizot
// kimliği. Taban kimlik zamansızdı ve tekrar eden olayları üstüne yazıyordu
// (B3); her epizot kendi satırı, karar/karne/replay epizot başına.
func MLEpisodeID(baseID string, start time.Time) string {
	return FingerprintAnomaly("ml-ep", baseID, strconv.FormatInt(start.Unix(), 10))
}

// UpsertMLEvent — tam satır yazımı (RMT: tüm alanlar taşınır).
func (s *Store) UpsertMLEvent(ctx context.Context, e MLEvent) error {
	if e.Signal == "" {
		e.Signal = MLDetectorSignal(e.Detector)
	}
	if e.Stage == "" {
		e.Stage = MLStageShadow
	}
	if e.Direction == "" {
		e.Direction = "up"
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO ml_events (id, detector, signal, stage, service, level, pod, code, started_at, last_seen, window_sec, baseline, observed, score, direction, evidence, dup_of)`)
	if err != nil {
		return err
	}
	if err := batch.Append(e.ID, e.Detector, e.Signal, e.Stage, e.Service, e.Level, e.Pod, e.Code,
		time.Unix(0, e.StartedAt), time.Unix(0, e.LastSeen), e.WindowSec, e.Baseline, e.Observed, e.Score, e.Direction, e.Evidence, e.DupOf); err != nil {
		return err
	}
	return batch.Send()
}

// MLEventsFilter — liste süzgeci; boş alan = süzme yok.
type MLEventsFilter struct {
	Since time.Time
	// ID — tek olay (detay kartı, v0.10.1843); ORDER BY id → birincil anahtar araması.
	ID       string
	Detector string
	Signal   string
	Service  string
	Stage    string
	Limit    int
}

// ListMLEvents — son olaylar (FINAL), last_seen DESC; kararlar eklenir.
func (s *Store) ListMLEvents(ctx context.Context, f MLEventsFilter) ([]MLEvent, error) {
	if f.Limit <= 0 || f.Limit > MLListCap {
		f.Limit = MLListCap
	}
	if f.Since.IsZero() {
		f.Since = time.Now().Add(-24 * time.Hour)
	}
	where := []string{"last_seen >= ?"}
	args := []any{chDateTime64Arg(f.Since)}
	if f.ID != "" {
		where = append(where, "id = ?")
		args = append(args, f.ID)
	}
	if f.Detector != "" {
		where = append(where, "detector = ?")
		args = append(args, f.Detector)
	}
	if f.Signal != "" {
		where = append(where, "signal = ?")
		args = append(args, f.Signal)
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
		SELECT id, detector, signal, stage, service, level, pod, code, started_at, last_seen,
		       window_sec, baseline, observed, score, direction, evidence, dup_of
		FROM ml_events FINAL
		WHERE %s
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`, strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLEvent
	var ids []string
	for rows.Next() {
		var e MLEvent
		var st, ls time.Time
		if err := rows.Scan(&e.ID, &e.Detector, &e.Signal, &e.Stage, &e.Service, &e.Level, &e.Pod, &e.Code, &st, &ls,
			&e.WindowSec, &e.Baseline, &e.Observed, &e.Score, &e.Direction, &e.Evidence, &e.DupOf); err != nil {
			return nil, err
		}
		e.StartedAt, e.LastSeen = st.UnixNano(), ls.UnixNano()
		out = append(out, e)
		ids = append(ids, MLVerdictID(e.ID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if vs, verr := s.ListAnomalyVerdicts(ctx, ids); verr == nil {
			for i := range out {
				if v, ok := vs[MLVerdictID(out[i].ID)]; ok {
					out[i].Verdict, out[i].VerdictBy, out[i].VerdictAt, out[i].Note = v.Verdict, v.CreatedBy, v.CreatedAt, v.Note
				}
			}
		}
	}
	return out, nil
}

// MLDetectorScore — dedektör karnesi satırı.
type MLDetectorScore struct {
	Detector string  `json:"detector"`
	Signal   string  `json:"signal"`
	Events   int64   `json:"events"`  // penceredeki olay sayısı
	Samples  int64   `json:"samples"` // karar verilmiş olay sayısı
	Hits     int64   `json:"hits"`    // verdict = anomaly
	HitRate  float64 `json:"hitRate"` // Hits / Samples (Samples=0 → 0)
}

// MLScorecardRate — SAF: karar girilmemiş satırlar paydaya GİRMEZ (S4).
func MLScorecardRate(hits, samples int64) float64 {
	if samples <= 0 {
		return 0
	}
	return float64(hits) / float64(samples)
}

// MLScorecardFromRows — SAF: olay (id→detector) × karar (verdict id→verdict)
// → dedektör başına olay / karar / isabet. Kararsız satır paydaya girmez (S4).
func MLScorecardFromRows(events map[string]string, verdicts map[string]string) []MLDetectorScore {
	acc := map[string]*MLDetectorScore{}
	for id, det := range events {
		r, ok := acc[det]
		if !ok {
			r = &MLDetectorScore{Detector: det, Signal: MLDetectorSignal(det)}
			acc[det] = r
		}
		r.Events++
		if v, ok := verdicts[MLVerdictID(id)]; ok && v != "" {
			r.Samples++
			if v == VerdictAnomaly {
				r.Hits++
			}
		}
	}
	out := make([]MLDetectorScore, 0, len(acc))
	for _, r := range acc {
		r.HitRate = MLScorecardRate(r.Hits, r.Samples)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	return out
}

// MLSignalScores — SAF: dedektör satırlarından sinyal başına karne (log,
// trace, metric; olay yoksa satır yok).
func MLSignalScores(rows []MLDetectorScore) []MLDetectorScore {
	acc := map[string]*MLDetectorScore{}
	for _, r := range rows {
		sig := r.Signal
		if sig == "" {
			sig = MLDetectorSignal(r.Detector)
		}
		a := acc[sig]
		if a == nil {
			a = &MLDetectorScore{Detector: sig, Signal: sig}
			acc[sig] = a
		}
		a.Events += r.Events
		a.Samples += r.Samples
		a.Hits += r.Hits
	}
	out := make([]MLDetectorScore, 0, len(acc))
	for _, sig := range []string{MLSignalLog, MLSignalTrace, MLSignalMetric} {
		if a := acc[sig]; a != nil {
			a.HitRate = MLScorecardRate(a.Hits, a.Samples)
			out = append(out, *a)
		}
	}
	return out
}

// MLScorecard — son `since`'den beri dedektör başına olay / karar /
// isabet. İki ayrı okuma (JOIN/alt sorgu yok: audit 5/5b, dağıtık-güvenli),
// birleştirme Go'da (MLScorecardFromRows). Olay tavanı 5000 (30 g × gün
// tavanı), karar okuması 'ml:' önekiyle sınırlı.
func (s *Store) MLScorecard(ctx context.Context, since time.Time) ([]MLDetectorScore, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT id, detector FROM ml_events FINAL
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
		WHERE event_id LIKE 'ml:%' AND created_at >= ?
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
	return MLScorecardFromRows(events, verdicts), nil
}

// MLTableReady — sre pod'u DDL koşmaz (v0.10.1282); tablo api/worker
// boot'unda iner. Lider tik öncesi bunu yoklar, yoksa bekler (çökmez).
func (s *Store) MLTableReady(ctx context.Context) (bool, error) {
	return s.tableExists(ctx, "ml_events")
}

// MLEpisodeCounts — dedektörün servis × bölüm (code) başına epizot sayısı
// (`since`'den beri başlayan satırlar; FINAL). B2 kronik kararı.
func (s *Store) MLEpisodeCounts(ctx context.Context, detector string, since time.Time) (map[string]int, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT service, code, count() FROM ml_events FINAL
		WHERE detector = ? AND started_at >= ? AND stage != 'chronic'
		GROUP BY service, code
		LIMIT 20000
		SETTINGS max_execution_time = 5`, detector, chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var svc, code string
		var n uint64
		if err := rows.Scan(&svc, &code, &n); err != nil {
			return nil, err
		}
		out[MLSeriesKey(svc, code)] = int(n)
	}
	return out, rows.Err()
}
