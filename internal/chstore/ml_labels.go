package chstore

// ml_labels.go — kalite çerçevesi ortak etiket seti (üç sinyal). Etiket =
// operatörün bir ML olayına verdiği karar (anomaly_verdicts 'ml:' öneki) +
// olayın servis ve penceresi. YALNIZ backtest puanlamasında (isabet /
// yakalama; ml/backtest.go) kullanılır — sinyal fark etmez: log olayına
// verilen "gerçek" kararı aynı servisin trace/metrik dedektörü için de
// etikettir. v0.10.2484 düzeltmesi (precision programı audit'i): eski yorum
// taban hijyeni ve geri bildirim ayarı da sayıyordu; ikisi v0.10.2269'da
// kalktı — oylar hiçbir çalışma zamanı kararını (tespit, taban, eşik,
// bildirim) sürmez (bekçi: ml/vote_runtime_guard_test.go
// TestAnomalyVotesDoNotReachRuntime). Dedektör karnesi ayrı ve tek bir CH
// toplamasıdır (ml_precision.go). İki okuma (JOIN/alt sorgu yok: audit 5/5b).

import (
	"context"
	"strings"
	"time"
)

// MLLabel — bir etiket.
type MLLabel struct {
	EventID  string `json:"eventId"`
	Detector string `json:"detector"`
	Signal   string `json:"signal"`
	Service  string `json:"service"`
	From     int64  `json:"from"` // unix sn
	To       int64  `json:"to"`
	Positive bool   `json:"positive"` // verdict = anomaly
}

// MLLabelsFromRows — SAF: karar (verdict id → verdict) × olay → etiket.
// Olayı TTL'le düşmüş karar etiket olmaz (penceresi bilinmiyor).
func MLLabelsFromRows(verdicts map[string]string, events map[string]MLEvent) []MLLabel {
	var out []MLLabel
	for vid, v := range verdicts {
		if !IsMLVerdictID(vid) || (v != VerdictAnomaly && v != VerdictNotAnomaly) {
			continue
		}
		e, ok := events[strings.TrimPrefix(vid, mlVerdictPref)]
		if !ok {
			continue
		}
		out = append(out, MLLabel{EventID: e.ID, Detector: e.Detector, Signal: e.Signal, Service: e.Service,
			From: e.StartedAt / 1e9, To: e.LastSeen / 1e9, Positive: v == VerdictAnomaly})
	}
	return out
}

// MLLabels — `since`'den beri verilmiş ML kararları, olay penceresiyle.
func (s *Store) MLLabels(ctx context.Context, since time.Time) ([]MLLabel, error) {
	vrows, err := s.conn.Query(ctx, `
		SELECT event_id, verdict FROM anomaly_verdicts FINAL
		WHERE event_id LIKE 'ml:%' AND created_at >= ?
		LIMIT 10000
		SETTINGS max_execution_time = 5`, chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	verdicts := map[string]string{}
	var ids []string
	for vrows.Next() {
		var id, v string
		if err := vrows.Scan(&id, &v); err != nil {
			vrows.Close()
			return nil, err
		}
		verdicts[id] = v
		ids = append(ids, strings.TrimPrefix(id, mlVerdictPref))
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT id, detector, signal, service, started_at, last_seen
		FROM ml_events FINAL
		WHERE id IN (?)
		LIMIT 10000
		SETTINGS max_execution_time = 5`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := map[string]MLEvent{}
	for rows.Next() {
		var e MLEvent
		var st, ls time.Time
		if err := rows.Scan(&e.ID, &e.Detector, &e.Signal, &e.Service, &st, &ls); err != nil {
			return nil, err
		}
		e.StartedAt, e.LastSeen = st.UnixNano(), ls.UnixNano()
		events[e.ID] = e
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return MLLabelsFromRows(verdicts, events), nil
}
