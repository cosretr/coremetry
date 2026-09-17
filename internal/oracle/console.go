package oracle

// console.go — v0.10.742 (operatör 2026-09-16: "Oracle database için de
// SQL console olsa güzel olur, ona da sorgu atabilmek isterim").
//
// Admin SQL konsolunun Oracle ayağı; ClickHouse playground'un üç katmanlı
// duruşu (sql_playground.go) burada da geçerli — bankacılık düzeyi ops tek
// katmanı sınır kabul etmez:
//
//  1. Sunucu: yalnız admin rolü uca ulaşır (api/oracle_console.go).
//  2. Uygulama: allow-list — TEK ifade, yalnız SELECT / WITH. Oracle'da
//     DESCRIBE SQL değil (SQL*Plus), EXPLAIN PLAN plan_table'a YAZAR
//     (DML!), PL/SQL bloğu (BEGIN/DECLARE) her şeyi yapabilir → hepsi
//     reddedilir. Noktalı virgülle ikinci ifade reddedilir.
//  3. Depo: operatörün metni bir ALT SORGUYA sarılır —
//        SELECT * FROM ( <q> ) FETCH FIRST n ROWS ONLY
//     Çalışan ifade HER ZAMAN bir SELECT'tir; alt sorguda DML/DDL sözdizimi
//     olarak imkânsızdır. Satır tavanı da aynı sarmalayıcıdan gelir
//     (ClickHouse'un max_result_rows'una karşılık). Zaman bütçesi kaynağın
//     kendi queryTimeoutSec'i (context iptali → sürücü sorguyu keser).
//
// Yan etkili fonksiyon çağrısı (autonomous transaction) hiçbir katmanla
// kesilemez — ClickHouse'ta da öyle; sınır DB kullanıcısının GRANT'ıdır
// (salt-okunur hesap). Şifre/DSN hiçbir hata metnine girmez (redactSecrets).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ConsoleMaxRows — satır tavanı (CH playground ile aynı sayı).
const ConsoleMaxRows = 10_000

// ConsoleResult — {columns, rows, rowCount, tookMs}; api SQLResult şekline
// birebir eşlenir. Rows hücreleri JSON-dostu (time → RFC3339, []byte →
// string, sayı/dize/bool aynen, diğerleri Sprint).
type ConsoleResult struct {
	Columns  []string `json:"columns"`
	Rows     [][]any  `json:"rows"`
	RowCount int      `json:"rowCount"`
	TookMs   int64    `json:"tookMs"`
	// Capped — sonuç tavana dayandı (daha fazla satır olabilir).
	Capped bool `json:"capped,omitempty"`
}

var (
	ErrConsoleUnsafeSQL   = errors.New("yalnız tek bir SELECT / WITH ifadesi kabul edilir")
	ErrConsoleNoSource    = errors.New("oracle kaynağı bulunamadı")
	ErrConsoleNoService   = errors.New("oracle servisi yok")
	consoleLineCommentRe  = regexp.MustCompile(`--[^\n]*`)
	consoleBlockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// stripSQLComments — SAF: `--` ve /* */ yorumları kaldırır ki önek denetimi
// gerçek ilk sözcüğü görsün ("-- x\nDELETE" hilesi).
func stripSQLComments(q string) string {
	q = consoleBlockCommentRe.ReplaceAllString(q, " ")
	q = consoleLineCommentRe.ReplaceAllString(q, " ")
	return strings.TrimSpace(q)
}

// IsSafeConsoleSQL — SAF (tablo testi): temizlenmiş metin tek ifade ve
// SELECT ya da WITH ile başlıyor. Tek bir SON noktalı virgül hoş görülür
// (operatör dokümandan yapıştırır); içeride ';' = çoklu ifade → ret.
func IsSafeConsoleSQL(q string) bool {
	clean := stripSQLComments(q)
	if clean == "" {
		return false
	}
	clean = strings.TrimRight(clean, "; \t\r\n")
	if strings.Contains(clean, ";") {
		return false
	}
	cu := strings.ToUpper(clean)
	// v0.10.768 — FOR UPDATE: sarmalayıcı zaten iç sorguda söz dizimi hatasına
	// çevirir; açık ret, sözleşmeyi metinde de görünür kılar.
	if forUpdateRe.MatchString(cu) {
		return false
	}
	for _, p := range []string{"SELECT", "WITH"} {
		if strings.HasPrefix(cu, p) {
			next := byte(' ')
			if len(cu) > len(p) {
				next = cu[len(p)]
			}
			if next == ' ' || next == '\t' || next == '\n' || next == '\r' || next == '(' {
				return true
			}
		}
	}
	return false
}

// WrapConsoleSQL — SAF: operatör metnini salt-okunur sarmalayıcıya alır.
// Sondaki ';' düşer (Oracle alt sorguda kabul etmez). n ≤ 0 ya da tavan
// üstü → ConsoleMaxRows.
func WrapConsoleSQL(q string, n int) string {
	if n <= 0 || n > ConsoleMaxRows {
		n = ConsoleMaxRows
	}
	clean := strings.TrimRight(stripSQLComments(q), "; \t\r\n")
	return fmt.Sprintf("SELECT * FROM (\n%s\n) FETCH FIRST %d ROWS ONLY", clean, n)
}

// consoleCell — SAF: sürücü değerini JSON-dostu hâle çevirir; kırpma YOK
// (formatCell'in 200 karakteri Settings örneği içindi).
func consoleCell(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// sourceByID — kaynak ID'siyle (enabled şartı YOK: konsol, poller
// kapalıyken de bağlanabilmeli — operatör tam bunun için istiyor).
func (s *Service) sourceByID(id string) (SourceConfig, bool) {
	if s == nil || strings.TrimSpace(id) == "" {
		return SourceConfig{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, src := range s.cfg.Sources {
		if src.ID == id {
			return src, true
		}
	}
	return SourceConfig{}, false
}

// Query — konsol sorgusu. Hata metinleri redaksiyonludur; şifre/DSN asla
// çıkmaz. maxRows tavan; sonuç tavana dayandıysa Capped=true.
func (s *Service) Query(ctx context.Context, sourceID, sqlText string, maxRows int) (ConsoleResult, error) {
	if s == nil {
		return ConsoleResult{}, ErrConsoleNoService
	}
	src, ok := s.sourceByID(sourceID)
	if !ok {
		return ConsoleResult{}, ErrConsoleNoSource
	}
	if !IsSafeConsoleSQL(sqlText) {
		return ConsoleResult{}, ErrConsoleUnsafeSQL
	}
	if maxRows <= 0 || maxRows > ConsoleMaxRows {
		maxRows = ConsoleMaxRows
	}
	db, secret, err := s.open(src)
	if err != nil {
		return ConsoleResult{}, err
	}
	defer db.Close()

	start := time.Now()
	qctx, cancel := context.WithTimeout(ctx, queryTimeout(src))
	defer cancel()
	rows, err := db.QueryContext(qctx, WrapConsoleSQL(sqlText, maxRows))
	if err != nil {
		return ConsoleResult{}, fmt.Errorf("sorgu: %s", redactSecrets(err.Error(), secret))
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return ConsoleResult{}, fmt.Errorf("kolonlar: %s", redactSecrets(err.Error(), secret))
	}
	out := ConsoleResult{Columns: cols, Rows: make([][]any, 0, 64)}
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return ConsoleResult{}, fmt.Errorf("satır: %s", redactSecrets(err.Error(), secret))
		}
		row := make([]any, len(cols))
		for i := range cells {
			row[i] = consoleCell(cells[i])
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return ConsoleResult{}, fmt.Errorf("satır akışı: %s", redactSecrets(err.Error(), secret))
	}
	out.RowCount = len(out.Rows)
	out.Capped = out.RowCount >= maxRows
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
