package chstore

// logs_sort.go — v0.10.1833 (Log Search Faz 1 dilim 1.10): /logs listesi
// kullanıcı sıralaması, CH arka ucu. Sözleşme ES ile aynı (≤3 anahtar,
// zaman eşitlik bozucu, sıralanamaz alan 400 — eşleme logstore'da).
//
// Kolonlar BEYAZ LİSTE (logsSortCols): yalnız sabit kolonlar SQL'e girer,
// kullanıcı metni asla. Öznitelik dizileri (attr_keys/values) sıralanmaz:
// pencere boyunca dizi açmak + rowkey sıralaması her sayfada tüm pencereyi
// okur; ilk dilim için en küçük güvenli seçenek (DECISIONS).
//
// Sayfalama: zaman-keyset'i birincil anahtar ZAMAN değilken geçersiz;
// "Daha fazla yükle" ofset token'ıyla (ofset ≤ logsSortedMaxOffset —
// istemci tamponu 10.000). Sorgu her zaman zaman-sınırlı WHERE + LIMIT +
// max_execution_time taşır (CLAUDE.md kısıtı).

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// errUnknownSortCol — beyaz liste dışı kolon (logstore zaten reddeder;
// savunma katmanı — SQL'e asla girmez).
var errUnknownSortCol = errors.New("logs: unknown sort column")

// LogSortCol — beyaz listeden bir kolon + yön.
type LogSortCol struct {
	Col  string
	Desc bool
}

// logsSortCols — izinli ORDER BY kolonları (logs tablosu, sabit şema).
var logsSortCols = map[string]bool{"time": true, "service_name": true, "severity_num": true}

// logsSortedMaxOffset — ofset sayfalamasının tavanı (istemci tamponu).
const logsSortedMaxOffset = 10000

// logsSortOrderSQL — SAF: ORDER BY gövdesi. Bilinmeyen kolon → ok=false.
// Zaman listede yoksa eşitlik bozucu (ascending yönünde), en sona rowkey.
func logsSortOrderSQL(cols []LogSortCol, ascending bool) (string, bool) {
	tie := "DESC"
	if ascending {
		tie = "ASC"
	}
	parts := make([]string, 0, len(cols)+2)
	hasTime := false
	for _, c := range cols {
		if !logsSortCols[c.Col] {
			return "", false
		}
		dir := "ASC"
		if c.Desc {
			dir = "DESC"
		}
		if c.Col == "time" {
			hasTime = true
		}
		parts = append(parts, c.Col+" "+dir)
	}
	if !hasTime {
		parts = append(parts, "time "+tie)
	}
	parts = append(parts, logsRowKeyExpr+" "+tie)
	return strings.Join(parts, ", "), true
}

// EncodeLogsSortCursor / DecodeLogsSortCursor — SAF: "chs|<ofset>|<imza>".
// İmza uyuşmazsa (başka sıralamayla basılmış) ok=false → ilk sayfa.
func EncodeLogsSortCursor(offset int, sig string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("chs|" + strconv.Itoa(offset) + "|" + sig))
}

func DecodeLogsSortCursor(tok, sig string) (int, bool) {
	if tok == "" {
		return 0, false
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return 0, false
	}
	parts := strings.SplitN(string(b), "|", 3)
	if len(parts) != 3 || parts[0] != "chs" || parts[2] != sig {
		return 0, false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 || n > logsSortedMaxOffset {
		return 0, false
	}
	return n, true
}

// getLogsSorted — GetLogs'un kullanıcı sıralamalı kolu (wc: aynı WHERE).
func (s *Store) getLogsSorted(ctx context.Context, f LogFilter, wc whereClause) ([]LogRow, string, error) {
	order, ok := logsSortOrderSQL(f.Sort, f.Ascending)
	if !ok {
		return nil, "", errUnknownSortCol
	}
	offset := f.Offset
	if off, ok := DecodeLogsSortCursor(f.Cursor, f.SortSig); ok {
		offset = off
	}
	if offset > logsSortedMaxOffset {
		offset = logsSortedMaxOffset
	}
	args := append(wc.args, f.Limit, offset)
	rows, err := s.telemetryReadConn().Query(ctx, `
		SELECT time, severity_num, severity_text, body,
		       service_name, trace_id, span_id,
		       attr_keys, attr_values, res_keys, res_values,
		       `+logsRowKeyExpr+` AS _rowkey
		FROM logs `+wc.sql()+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?
		SETTINGS max_execution_time = 25`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []LogRow
	for rows.Next() {
		var lr LogRow
		var t time.Time
		var rowKey uint64
		var attrK, attrV, resK, resV []string
		if err := rows.Scan(
			&t, &lr.SeverityNumber, &lr.SeverityText, &lr.Body,
			&lr.ServiceName, &lr.TraceID, &lr.SpanID,
			&attrK, &attrV, &resK, &resV, &rowKey,
		); err != nil {
			return nil, "", err
		}
		lr.Timestamp = t.UnixNano()
		lr.ID = rowKey
		lr.Attributes = arraysToMap(attrK, attrV)
		lr.ResourceAttributes = arraysToMap(resK, resV)
		out = append(out, lr)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == f.Limit && offset+len(out) < logsSortedMaxOffset {
		next = EncodeLogsSortCursor(offset+len(out), f.SortSig)
	}
	return out, next, nil
}
