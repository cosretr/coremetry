package chstore

// trace_root_verify_raw.go — v0.10.755 (trace bütünlüğü denetimi Q1-#2;
// operatör onayı 2026-09-17): "Root traces only" + servis daraltması kök
// varlığını trace_summary_5m'den doğrular (v0.10.107: kök başka serviste
// olabilir, daraltılmış ham kümede aranamaz). MV-GAP gününde (TraceMVGap:
// pencere MV'de boş bir güne değiyor — backfill öncesi / MV yeniden
// kurulumu) MV'de o güne ait satır YOK → her aday elenir → boş liste,
// üstelik MVGap yalnız LİSTEYİ ham yola çeviriyordu, kök doğrulamasını
// değil. Şimdi gap gününde doğrulama ham spans'tan: id-sınırlı ve
// zaman-sınırlı (maliyet id sayısından), tek-geçiş alt sorgusu ise
// pencere-sınırlı (241-sınıfı maliyet — yalnız gap gününde, açıkça).
//
// Arg sözleşmesi MV formuyla AYNI (ilk iki arg unix sn): probeHavingArgs
// basamak penceresini out[0]'a yazar; tip değişseydi rung'lar kırılırdı.

import (
	"context"
	"time"
)

// rootVerifyRawSQL — SAF: aday id'ler için ham kök-varlığı.
func rootVerifyRawSQL(nIDs int, def TraceRootDef) string {
	return `
			SELECT trace_id FROM spans
			WHERE time >= toDateTime(?, 'UTC') AND time < toDateTime(?, 'UTC')
			  AND trace_id IN (` + chPlaceholders(nIDs) + `)
			GROUP BY trace_id
			HAVING ` + rootHavingRaw(def) + `
			SETTINGS max_execution_time = 10`
}

// rootSubqueryRawSQL — SAF: tek-geçiş HAVING'inin gap-günü formu.
func rootSubqueryRawSQL(def TraceRootDef) string {
	return `trace_id GLOBAL IN (
			SELECT trace_id FROM spans
			WHERE time >= toDateTime(?, 'UTC') AND time < toDateTime(?, 'UTC')
			GROUP BY trace_id
			HAVING ` + rootHavingRaw(def) + `)`
}

// filterRootTracesRaw — filterRootTraces'in ham spans ikizi (gap günü).
func (s *Store) filterRootTracesRaw(ctx context.Context, ids []string, from, to time.Time) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	def := s.TraceRootDef()
	for start := 0; start < len(ids); start += traceStage2MaxIDs {
		end := start + traceStage2MaxIDs
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		args := []any{from.Unix(), to.Unix()}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.telemetryReadConn().Query(ctx, rootVerifyRawSQL(len(chunk), def), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
