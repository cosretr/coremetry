package chstore

// oracle_op_search.go — v0.10.1002 — Oracle OPERASYON ADI araması (operatör:
// "operasyon ismiyle trace bulabilir miyim" → "yap"). Komut paletinin (⌘K)
// sunucu taraflı kaynağı: yazılan metni operasyon adında arar ve her isabet
// için trace'e giden iki köprüyü verir —
//
//	fonksiyon kodları  o operasyonun satırlarındaki kodlar; span'lerde aynı
//	                   değer FUNCTION_CODE attribute'u → Traces süzgeci (başarılı
//	                   + hatalı TÜM trace'ler)
//	son trace          hata satırındaki en yeni trace kimliği (kesin eşleşme,
//	                   yalnız hata vermiş istek)
//
// Okuma oracle_error_log'dan (poller'ın kopyası; canlı Oracle'a gidilmez):
// zaman sınırlı, LIMIT + max_execution_time. FINAL YOK — arama için yaklaşık
// satır sayısı yeter, RMT birleştirmesinin bedeli gereksiz. Fonksiyon kodunun
// yeri kaynağa göre değişir (oracle.FunctionCodeOf ile aynı kural): kaynakta
// `code` alanı fonksiyon koduna eşliyse error_code, değilse satır attribute'u.

import (
	"context"
	"strings"
	"time"
)

// OracleOpHit — bir operasyon adı isabeti.
type OracleOpHit struct {
	Operation     string
	Rows          uint64
	LastSeenMs    int64
	LastTraceID   string
	FunctionCodes []string
	SourceID      string // en çok satırın geldiği kaynak
}

const (
	oracleOpSearchDefault  = 6
	oracleOpSearchMax      = 20
	oracleOpSearchMaxCodes = 5
)

func clampOracleOpSearch(limit int) int {
	switch {
	case limit <= 0:
		return oracleOpSearchDefault
	case limit > oracleOpSearchMax:
		return oracleOpSearchMax
	}
	return limit
}

// oracleOpSearchSQL — SAF: nCodeSources = `code` alanı fonksiyon kodu olan
// kaynak sayısı (IN listesi yer tutucuları); 0 ise kod yalnız attribute'tan.
func oracleOpSearchSQL(nCodeSources int) string {
	fc := oracleFunctionCodeAttrExpr
	if nCodeSources > 0 {
		holders := strings.TrimSuffix(strings.Repeat("?,", nCodeSources), ",")
		fc = "if(source_id IN (" + holders + "), trimBoth(error_code), " + oracleFunctionCodeAttrExpr + ")"
	}
	return `
		SELECT operation_code, count() AS n, toUnixTimestamp64Milli(max(time)) AS last,
		       argMaxIf(trace_id, time, trace_id != '') AS last_trace,
		       arraySlice(arrayFilter(x -> x != '', groupUniqArray(8)(fc)), 1, ?) AS codes,
		       anyHeavy(source_id) AS src
		FROM (
			SELECT operation_code, time, trace_id, source_id, ` + fc + ` AS fc
			FROM oracle_error_log
			WHERE time >= ? AND time < ? AND operation_code != ''
			  AND positionCaseInsensitiveUTF8(operation_code, ?) > 0
		)
		GROUP BY operation_code
		ORDER BY n DESC, operation_code
		LIMIT ?
		SETTINGS max_execution_time = 5`
}

// OracleOperationSearch — q'yu (harf duyarsız alt dize) operasyon adında arar;
// en çok satırlı önce, ≤limit. codeSources: `code` alanı fonksiyon kodu olan
// kaynak kimlikleri.
func (s *Store) OracleOperationSearch(ctx context.Context, q string, codeSources []string, from, to time.Time, limit int) ([]OracleOpHit, error) {
	out := []OracleOpHit{}
	q = strings.TrimSpace(q)
	if q == "" || !to.After(from) {
		return out, nil
	}
	// Yer tutucu sırası SQL'deki sırayla birebir: dilim tavanı, [kaynaklar], pencere, q, limit.
	args := make([]any, 0, len(codeSources)+5)
	args = append(args, oracleOpSearchMaxCodes)
	for _, id := range codeSources {
		args = append(args, id)
	}
	args = append(args, from, to, q, clampOracleOpSearch(limit))
	rows, err := s.conn.Query(ctx, oracleOpSearchSQL(len(codeSources)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h OracleOpHit
		if err := rows.Scan(&h.Operation, &h.Rows, &h.LastSeenMs, &h.LastTraceID, &h.FunctionCodes, &h.SourceID); err != nil {
			return nil, err
		}
		if h.FunctionCodes == nil {
			h.FunctionCodes = []string{}
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// PromotedAttrSpelling — v0.10.1002: adaylardan terfi kolonu haritasında
// KAYITLI olan ilk yazım (boot probe'u veriyle doğrulamış); hiçbiri kayıtlı
// değilse "". Kullanıcı süzgeci anahtarı harf duyarlıdır (filterexpr.go) —
// dışarıya link kuran yüzey prod'un gerçekten yazdığı yazımı buradan öğrenir.
func PromotedAttrSpelling(candidates ...string) string {
	pm := promotedCols()
	for _, c := range candidates {
		if _, ok := pm[c]; ok {
			return c
		}
	}
	return ""
}

// OracleFnOp — v0.10.1003: bir (fonksiyon kodu, operasyon) çiftinin satır sayısı.
// Trace / endpoint tarafında görülen fonksiyon kodunu Oracle operasyon ADINA
// çeviren sözlüğün ham girdisi (GET /api/oracle/function-codes).
type OracleFnOp struct {
	Code      string
	Operation string
	Rows      uint64
}

const oracleFnOpLimit = 5000

// oracleFnOpsSQL — SAF: (kod, operasyon) dökümü; kod kaynağı oracleOpSearchSQL
// ile aynı kural (nCodeSources = `code` alanı fonksiyon kodu olan kaynaklar).
func oracleFnOpsSQL(nCodeSources int) string {
	fc := oracleFunctionCodeAttrExpr
	if nCodeSources > 0 {
		holders := strings.TrimSuffix(strings.Repeat("?,", nCodeSources), ",")
		fc = "if(source_id IN (" + holders + "), trimBoth(error_code), " + oracleFunctionCodeAttrExpr + ")"
	}
	return `
		SELECT fc, operation_code, count() AS n
		FROM (
			SELECT operation_code, ` + fc + ` AS fc
			FROM oracle_error_log
			WHERE time >= ? AND time < ? AND operation_code != ''
		)
		WHERE fc != ''
		GROUP BY fc, operation_code
		ORDER BY n DESC, fc, operation_code
		LIMIT ?
		SETTINGS max_execution_time = 5`
}

// OracleFunctionOperations — [from, to) penceresinde görülen (fonksiyon kodu,
// operasyon) çiftleri, en çok satırlı önce, ≤ oracleFnOpLimit. İkinci dönüş:
// tavan doldu (sözlük eksik olabilir).
func (s *Store) OracleFunctionOperations(ctx context.Context, codeSources []string, from, to time.Time) ([]OracleFnOp, bool, error) {
	out := []OracleFnOp{}
	if !to.After(from) {
		return out, false, nil
	}
	args := make([]any, 0, len(codeSources)+3)
	for _, id := range codeSources {
		args = append(args, id)
	}
	args = append(args, from, to, oracleFnOpLimit)
	rows, err := s.conn.Query(ctx, oracleFnOpsSQL(len(codeSources)), args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var o OracleFnOp
		if err := rows.Scan(&o.Code, &o.Operation, &o.Rows); err != nil {
			return nil, false, err
		}
		out = append(out, o)
	}
	return out, len(out) >= oracleFnOpLimit, rows.Err()
}
