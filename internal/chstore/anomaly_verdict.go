package chstore

// anomaly_verdict.go — v0.10.181: operatörün anomali olayına kararı
// («anomali» / «değil»). Tablo store.go `anomaly_verdicts` (RMT(version),
// ORDER BY event_id, FINAL). Susturmadan (anomaly_silence.go) AYRI: karar
// kayıt + görünümdür, akışı kapatmaz. Saf parçalar (ValidVerdict, verdictInPlaceholders)
// anomaly_verdict_test.go'da tablo-testli.

import (
	"context"
	"log"
	"strings"
	"time"
)

const (
	VerdictAnomaly    = "anomaly"
	VerdictNotAnomaly = "not_anomaly"
)

// ValidVerdict — yalnız iki değer; boş/başka → false.
func ValidVerdict(v string) bool { return v == VerdictAnomaly || v == VerdictNotAnomaly }

// v0.10.2256 — "Sorun değil" nedenleri (anomali kartı mockup §5). KAPALI
// küme; serbest metin `note`'ta. Yalnız karne etiketi: tespit, eşik,
// bastırma, bildirim, Problem terfisi ve eval etiketi bu alanı OKUMAZ
// (MLLabels / MLScorecard yalnız event_id + verdict okur).
const (
	VerdictReasonNoDeviation = "no_deviation"
	VerdictReasonNoImpact    = "no_impact"
	VerdictReasonExpected    = "expected"
	VerdictReasonOther       = "other"
)

// NormalizeVerdictReason — SAF: (neden, geçerli mi). Neden yalnız
// not_anomaly kararında anlamlı; "anomaly" kararında her zaman ” döner.
// Boş neden geçerlidir (eski istemci, eski kart).
func NormalizeVerdictReason(verdict, reason string) (string, bool) {
	reason = strings.TrimSpace(reason)
	if verdict != VerdictNotAnomaly || reason == "" {
		return "", true
	}
	switch reason {
	case VerdictReasonNoDeviation, VerdictReasonNoImpact, VerdictReasonExpected, VerdictReasonOther:
		return reason, true
	}
	return "", false
}

type AnomalyVerdict struct {
	EventID     string `json:"eventId"`
	Fingerprint string `json:"fingerprint"`
	Kind        string `json:"kind"`
	Pattern     string `json:"pattern"`
	Service     string `json:"service"`
	Verdict     string `json:"verdict"`
	Note        string `json:"note,omitempty"`
	Reason      string `json:"reason,omitempty"` // v0.10.2256 — "Sorun değil" nedeni
	CreatedBy   string `json:"createdBy"`
	CreatedAt   int64  `json:"createdAt"` // unix ns
}

// verdictCols — SAF: INSERT ve SELECT'in TEK kolon listesi (v0.10.2256);
// reason yalnız kolon görülmüşse (hasVerdictReasonCol) — eski replika /
// henüz ALTER inmemiş tablo eski biçimle yazılır ve okunur.
func verdictCols(withReason bool) []string {
	cols := []string{"event_id", "fingerprint", "kind", "pattern", "service", "verdict", "note", "created_by", "created_at"}
	if withReason {
		cols = append(cols, "reason")
	}
	return cols
}

func (s *Store) UpsertAnomalyVerdict(ctx context.Context, v AnomalyVerdict) error {
	withReason := s.hasVerdictReasonCol.Load()
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO anomaly_verdicts ("+strings.Join(verdictCols(withReason), ", ")+")")
	if err != nil {
		return err
	}
	args := []any{v.EventID, v.Fingerprint, v.Kind, v.Pattern, v.Service, v.Verdict, v.Note, v.CreatedBy, time.Unix(0, v.CreatedAt)}
	if withReason {
		args = append(args, v.Reason)
	}
	if err := batch.Append(args...); err != nil {
		return err
	}
	return batch.Send()
}

// probeVerdictReasonCol — anomaly_verdicts.reason var mı (v0.10.2256;
// system.columns metadata, ai_eval_runs.path probe'unun şekli).
func (s *Store) probeVerdictReasonCol(ctx context.Context) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = currentDatabase() AND table = 'anomaly_verdicts'
		   AND name = 'reason'
		 SETTINGS max_execution_time = 5`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// reprobeVerdictReasonCol — ertelenen DDL sonrası yeniden deneme; bayrak
// yalnız false → true döner, geçiş BİR KEZ loglanır. Dönüş: bayrağın son hâli.
func (s *Store) reprobeVerdictReasonCol(ctx context.Context) bool {
	if s.hasVerdictReasonCol.Load() {
		return true
	}
	if ok, _ := s.probeVerdictReasonCol(ctx); ok && s.hasVerdictReasonCol.CompareAndSwap(false, true) {
		log.Printf("[chstore] anomaly_verdicts.reason ertelenen DDL sonrası görüldü — karar nedeni yazımı ve okuması devrede (restart gerekmedi)")
	}
	return s.hasVerdictReasonCol.Load()
}

// verdictInPlaceholders — n adet `?` yer tutucu: "?,?,?" (n<=0 → "" — çağıran boş listeyi
// zaten eler). Dizgi birleştirmeyle değil bind ile: kimlikler kullanıcı verisi.
func verdictInPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ListAnomalyVerdicts — verilen olay kimlikleri için son karar (FINAL).
// Tavan 500 kimlik (events ucu 200 döner; çağıran keser).
func (s *Store) ListAnomalyVerdicts(ctx context.Context, ids []string) (map[string]AnomalyVerdict, error) {
	out := map[string]AnomalyVerdict{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 500 {
		ids = ids[:500]
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	withReason := s.hasVerdictReasonCol.Load()
	rows, err := s.conn.Query(ctx, `
		SELECT `+strings.Join(verdictCols(withReason), ", ")+`
		FROM anomaly_verdicts FINAL
		WHERE event_id IN (`+verdictInPlaceholders(len(ids))+`)
		LIMIT 500
		SETTINGS max_execution_time = 5`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v AnomalyVerdict
		var at time.Time
		dest := []any{&v.EventID, &v.Fingerprint, &v.Kind, &v.Pattern, &v.Service, &v.Verdict, &v.Note, &v.CreatedBy, &at}
		if withReason {
			dest = append(dest, &v.Reason)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		v.CreatedAt = at.UnixNano()
		out[v.EventID] = v
	}
	return out, rows.Err()
}

// EnrichAnomaliesWithVerdicts — events ucu (okuma zamanı); depo hatası
// sessizce atlanır (karar ikincil bağlam, liste kararsız da servis edilir).
func (s *Store) EnrichAnomaliesWithVerdicts(ctx context.Context, events []AnomalyEvent) []AnomalyEvent {
	if len(events) == 0 {
		return events
	}
	ids := make([]string, 0, len(events))
	for i := range events {
		ids = append(ids, events[i].ID)
	}
	vs, err := s.ListAnomalyVerdicts(ctx, ids)
	if err != nil {
		return events
	}
	for i := range events {
		if v, ok := vs[events[i].ID]; ok {
			events[i].Verdict, events[i].VerdictBy, events[i].VerdictAt = v.Verdict, v.CreatedBy, v.CreatedAt
		}
	}
	return events
}
