package chstore

// oracle_op_coverage.go — v0.10.999 — Oracle hata satırlarının OPERASYON
// başına dökümü: "satırların ne kadarı bir servise bağlanıyor, bağlanmayan
// neden bağlanmıyor" raporunun (Ayarlar › Oracle › özne kapsamı;
// internal/oracle/coverage.go) ham girdisi.
//
// Okuma oracle_error_log'dan (poller'ın kopyası; canlı Oracle'a gidilmez):
// PK öneki (source_id, time) taraması, zaman sınırlı, GROUP BY operation_code
// (LowCardinality), LIMIT + max_execution_time. FINAL: özel SQL kipinde aynı
// satır her poll'da yeniden yazılabildiği için (RMT tekilleştirir).

import (
	"context"
	"time"
)

// OracleOpObs — bir operasyon kodunun pencere içi gözlemi.
type OracleOpObs struct {
	Operation string
	Rows      uint64
	WithTrace uint64 // trace_id dolu satır
	Traces    uint64 // tekil trace (yaklaşık)
	Instances uint64 // tekil instance_id (yaklaşık)
	// TopInstance — en sık görülen instance_id (anyHeavy); pod adından servis
	// türetmesinin girdisi. TopHost aynı şekilde host_name.
	TopInstance string
	TopHost     string
}

// OracleOpTotals — pencerenin tamamı (LIMIT'ten bağımsız).
type OracleOpTotals struct {
	Rows      uint64
	WithTrace uint64
	Ops       uint64
}

const (
	oracleOpCoverageDefault = 200
	oracleOpCoverageMax     = 500
)

func clampOracleOpCoverage(limit int) int {
	switch {
	case limit <= 0:
		return oracleOpCoverageDefault
	case limit > oracleOpCoverageMax:
		return oracleOpCoverageMax
	}
	return limit
}

// OracleOpCoverage — kaynağın [from, to) penceresinde operasyon başına satır
// dökümü (en çok satırlı önce, ≤limit) + pencere toplamları.
func (s *Store) OracleOpCoverage(ctx context.Context, sourceID string, from, to time.Time, limit int) ([]OracleOpObs, OracleOpTotals, error) {
	var totals OracleOpTotals
	if sourceID == "" || !to.After(from) {
		return []OracleOpObs{}, totals, nil
	}
	if err := s.conn.QueryRow(ctx, `
		SELECT count(), countIf(trace_id != ''), uniq(operation_code)
		FROM oracle_error_log FINAL
		WHERE source_id = ? AND time >= ? AND time < ?
		SETTINGS max_execution_time = 10`, sourceID, from, to).Scan(&totals.Rows, &totals.WithTrace, &totals.Ops); err != nil {
		return nil, totals, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT operation_code, count() AS n, countIf(trace_id != ''), uniqIf(trace_id, trace_id != ''),
		       uniqIf(instance_id, instance_id != ''), anyHeavy(instance_id), anyHeavy(host_name)
		FROM oracle_error_log FINAL
		WHERE source_id = ? AND time >= ? AND time < ?
		GROUP BY operation_code
		ORDER BY n DESC, operation_code
		LIMIT ?
		SETTINGS max_execution_time = 10`, sourceID, from, to, clampOracleOpCoverage(limit))
	if err != nil {
		return nil, totals, err
	}
	defer rows.Close()
	out := []OracleOpObs{}
	for rows.Next() {
		var o OracleOpObs
		if err := rows.Scan(&o.Operation, &o.Rows, &o.WithTrace, &o.Traces, &o.Instances, &o.TopInstance, &o.TopHost); err != nil {
			return nil, totals, err
		}
		out = append(out, o)
	}
	return out, totals, rows.Err()
}

// OracleOpCode — v0.10.1000: bir (operasyon, fonksiyon kodu) çiftinin
// pencere içi satır sayısı. Özne kapsamı raporunun "fonksiyon kodundan
// bağlanabilen satır" ölçümünün girdisi (internal/oracle/coverage.go).
type OracleOpCode struct {
	Operation string
	Code      string
	Rows      uint64
}

const oracleOpCodeLimit = 2000

// oracleFunctionCodeAttrExpr — v0.10.1001: satırın fonksiyon kodu, eşlenmeyen
// kolonlardan (attribute). Anahtar adı kaynağa göre değişir (FUNCTIONCODE,
// FUNCTION_CODE, MCA_ERR_FUNCTIONCODE…); kural internal/oracle
// isFunctionCodeColumn ile AYNI: büyük harf, alt çizgisiz, "FUNCTIONCODE" ile
// biter. Eşleşme yoksa arrayFirstIndex 0 döner → attr_values[0] = ”.
const oracleFunctionCodeAttrExpr = `trimBoth(attr_values[arrayFirstIndex(k -> endsWith(replaceAll(upper(k), '_', ''), 'FUNCTIONCODE'), attr_keys)])`

// oracleOpCodesSQL — SAF: (operasyon, fonksiyon kodu) dökümü. fromCode: kod
// error_code kolonunda (kaynakta code ← FUNCTIONCODE); değilse attribute'tan.
func oracleOpCodesSQL(fromCode bool) string {
	expr := oracleFunctionCodeAttrExpr
	if fromCode {
		expr = "trimBoth(error_code)"
	}
	return `
		SELECT operation_code, ` + expr + ` AS fc, count() AS n
		FROM oracle_error_log FINAL
		WHERE source_id = ? AND time >= ? AND time < ? AND fc != ''
		GROUP BY operation_code, fc
		ORDER BY n DESC, operation_code, fc
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// OracleOpCodes — kaynağın [from, to) penceresinde fonksiyon kodu DOLU
// satırların (operasyon, kod) dökümü; en çok satırlı önce, ≤ oracleOpCodeLimit
// çift. fromCode: oracleOpCodesSQL.
func (s *Store) OracleOpCodes(ctx context.Context, sourceID string, from, to time.Time, fromCode bool) ([]OracleOpCode, error) {
	out := []OracleOpCode{}
	if sourceID == "" || !to.After(from) {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, oracleOpCodesSQL(fromCode), sourceID, from, to, oracleOpCodeLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o OracleOpCode
		if err := rows.Scan(&o.Operation, &o.Code, &o.Rows); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
