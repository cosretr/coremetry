package chstore

// function_code_services.go — v0.10.1000 — FONKSİYON KODU → servis dağılımı
// (Oracle özne çözücüsünün "fonksiyon kodundan" basamağı; operatör teyidi
// 2026-10-01: Oracle hata satırındaki fonksiyon kodu, span'lerdeki
// FUNCTION_CODE attribute'u ile AYNI değerdir).
//
// Soru: "bu fonksiyon kodunu taşıyan span'leri hangi servisler üretiyor?"
// Oracle satırının trace kimliği Coremetry'de yoksa (ya da hiç yoksa) satır
// servissiz kalıyordu; kod span tarafında da taşındığı için servis trace'e
// gerek kalmadan bulunabilir.
//
// İKİ okuma yolu, sırayla — üçüncüsü (attribute dizisini açan ham tarama)
// BİLİNÇLİ yok: servis süzgeci olmayan bir dizi taraması 1B span/gün'de
// poll başına koşulacak bir sorgu değil.
//
//	① rollup  GENİŞ rollup ailesi (migrations/0002: servis × endpoint ×
//	          function_code, bloom_filter(function_code)). Pencere ≤ 3 saat →
//	          1m, daha uzunu → 5m. MV-first.
//	② spans   terfi kolonu attr_function_code KAYITLIYSA (boot probe'u veriyle
//	          doğruladıysa) ham spans; set(0) skip index. Pencere son 1 saate
//	          kırpılır.
//	""        ikisi de yok → Source "" (çağıran "okuma yolu yok" der).
//
// Her iki yol da zaman sınırlı + LIMIT + max_execution_time; kodlar bind-arg.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FunctionCodeService — bir fonksiyon kodunun bir servisteki span sayıları.
type FunctionCodeService struct {
	Service string `json:"service"`
	Spans   uint64 `json:"spans"`
	Errors  uint64 `json:"errors"`
}

// FunctionCodeFacts — okuma sonucu. Source: "rollup" | "spans" | "" (okuma
// yolu yok). ByCode'da olmayan kod = pencerede o kodu taşıyan span yok.
type FunctionCodeFacts struct {
	Source string
	ByCode map[string][]FunctionCodeService
}

// Okuma yolu adları.
const (
	FunctionCodeSourceRollup = "rollup"
	FunctionCodeSourceSpans  = "spans"
)

const (
	functionCodeMaxCodes   = 200
	functionCodeRowLimit   = 5000
	functionCodeRollup1mTo = 3 * time.Hour // bundan uzun pencere 5m kademesinden
	functionCodeSpansMax   = time.Hour     // ham yol penceresi tavanı
	functionCodeAttrKey    = "FUNCTION_CODE"
)

// functionCodeRollupTable — SAF: pencereye göre geniş rollup kademesi.
func functionCodeRollupTable(window time.Duration) string {
	if window > functionCodeRollup1mTo {
		return "rollup_spans_wide_5m"
	}
	return "rollup_spans_wide_1m"
}

// functionCodeRollupSQL — SAF: geniş rollup'tan (kod, servis) sayıları.
// table yalnız functionCodeRollupTable'ın iki sabitinden gelir.
func functionCodeRollupSQL(table string, n int) string {
	holders := strings.TrimSuffix(strings.Repeat("?,", n), ",")
	return fmt.Sprintf(`
		SELECT function_code, service_name, sum(span_count) AS n, sum(error_count) AS e
		FROM %s
		WHERE ts >= ? AND ts < ?
		  AND function_code IN (%s)
		GROUP BY function_code, service_name
		ORDER BY e DESC, n DESC, function_code, service_name
		LIMIT %d
		SETTINGS max_execution_time = 5`, table, holders, functionCodeRowLimit)
}

// functionCodeSpansSQL — SAF: terfi kolonundan (kod, servis) sayıları. col
// yalnız promotedAttrResolve'un döndürdüğü kayıtlı kolon adıdır.
func functionCodeSpansSQL(col string, n int) string {
	holders := strings.TrimSuffix(strings.Repeat("?,", n), ",")
	return fmt.Sprintf(`
		SELECT %[1]s AS fc, service_name, count() AS n, countIf(status_code = 'error') AS e
		FROM spans
		WHERE time >= ? AND time < ?
		  AND %[1]s IN (%[2]s)
		GROUP BY fc, service_name
		ORDER BY e DESC, n DESC, fc, service_name
		LIMIT %[3]d
		SETTINGS max_execution_time = 5`, col, holders, functionCodeRowLimit)
}

// cleanFunctionCodes — SAF: boşlar atılır, tekrarsız, ≤ functionCodeMaxCodes
// (sıra korunur — çağıran en önemliyi öne koyar).
func cleanFunctionCodes(codes []string) []string {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
		if len(out) >= functionCodeMaxCodes {
			break
		}
	}
	return out
}

// FunctionCodeServices — kodların [from, to) penceresindeki servis dağılımı
// (dosya başı). Okuma yolu yoksa hata DEĞİL: Source "" + boş harita.
func (s *Store) FunctionCodeServices(ctx context.Context, codes []string, from, to time.Time) (FunctionCodeFacts, error) {
	out := FunctionCodeFacts{ByCode: map[string][]FunctionCodeService{}}
	codes = cleanFunctionCodes(codes)
	if len(codes) == 0 || !to.After(from) {
		return out, nil
	}
	args := make([]any, 0, len(codes)+2)
	var query string
	table := functionCodeRollupTable(to.Sub(from))
	if ok, err := s.rollupTableExists(ctx, table); err != nil {
		return out, err
	} else if ok {
		out.Source = FunctionCodeSourceRollup
		query = functionCodeRollupSQL(table, len(codes))
		args = append(args, from, to)
	} else if col, colArgs, found := promotedAttrResolve(functionCodeAttrKey); found && len(colArgs) == 0 {
		out.Source = FunctionCodeSourceSpans
		if floor := to.Add(-functionCodeSpansMax); from.Before(floor) {
			from = floor
		}
		query = functionCodeSpansSQL(col, len(codes))
		args = append(args, from, to)
	} else {
		return out, nil
	}
	for _, c := range codes {
		args = append(args, c)
	}
	// İki yol da SAF telemetri (rollup_spans_wide_* / spans) → okuma havuzu.
	rows, err := s.telemetryReadConn().Query(ctx, query, args...)
	if err != nil {
		return out, fmt.Errorf("fonksiyon kodu → servis (%s): %w", out.Source, err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var f FunctionCodeService
		if err := rows.Scan(&code, &f.Service, &f.Spans, &f.Errors); err != nil {
			return out, err
		}
		if f.Service == "" {
			continue
		}
		out.ByCode[code] = append(out.ByCode[code], f)
	}
	return out, rows.Err()
}
