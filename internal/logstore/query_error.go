package logstore

// query_error.go — v0.10.1818 (Log Search Faz 1 dilim 1.2; audit §2 yan
// bulgu + §3.3). ES'in sorgu SÖZDİZİMİ reddi (400 query_shard_exception /
// parse_exception …) bugüne kadar düz bir hata dizesiydi; /api/logs onu 500
// ile döküyordu ve operatör "sunucu bozuk" görüyordu. Burada reddin üç
// yapısal parçası çıkarılır: gerekçe cümlesi, ES'in geri yankıladığı sorgu
// metni ve satır/sütun. API katmanı bunları 400 {error, pos, detail}'e
// çevirir (internal/api/logs_query_error.go).
//
// Error() metni BİLİNÇLİ olarak eski metinle bayt bayt aynı (log satırları,
// /admin/elastic halkası, CoSRE araç testleri o metne bakıyor); errors.Is
// (err, ErrBadQuery) da eskisi gibi doğru.

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// BadQueryError — ES'in sözdizimi reddi, yapısal parçalarıyla.
type BadQueryError struct {
	msg string
	// Reason — operatöre gösterilebilir tek cümle (ilk satır, ≤300 rune):
	// tercihen Lucene'in "Cannot parse '…': Encountered … at line 1, column
	// 9." cümlesi, yoksa root_cause gerekçesi. Ham ES gövdesi DEĞİL.
	Reason string
	// Echo — ES'in gerekçede geri yankıladığı sorgu metni ("Failed to parse
	// query [X]" / "Cannot parse 'X':"); bulunamazsa "".
	Echo string
	// Line, Col — Lucene'in 1 tabanlı konumu; bilinmiyorsa 0.
	Line, Col int
}

func (e *BadQueryError) Error() string { return e.msg }
func (e *BadQueryError) Unwrap() error { return ErrBadQuery }

var (
	esLineColRe   = regexp.MustCompile(`line (\d+), column (\d+)`)
	esFailedQryRe = regexp.MustCompile(`(?s)^Failed to parse query \[(.*)\]$`)
	esCannotRe    = regexp.MustCompile(`(?s)^Cannot parse '(.*)': `)
)

// esReasons — ES hata zarfındaki TÜM "reason" dizeleri (iç içe caused_by,
// failed_shards dahil), belge sırasıyla. Saf.
func esReasons(body []byte) []string {
	var root any
	if json.Unmarshal(body, &root) != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if r, ok := t["reason"].(string); ok && r != "" {
				out = append(out, r)
			}
			// Deterministik sıra: önce bilinen iç içe anahtarlar.
			for _, k := range []string{"root_cause", "caused_by", "failed_shards", "reason"} {
				if c, ok := t[k]; ok {
					if _, isStr := c.(string); !isStr {
						walk(c)
					}
				}
			}
			if e, ok := t["error"]; ok {
				walk(e)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(root)
	return out
}

// NewBadQueryError — ES gövdesinden yapısal reddi kurar. msg = eski Error()
// metni. Saf (tablo testli).
func NewBadQueryError(msg string, body []byte) *BadQueryError {
	e := &BadQueryError{msg: msg}
	var rootReason string
	for _, r := range esReasons(body) {
		if rootReason == "" {
			rootReason = r
		}
		if e.Line == 0 {
			if m := esLineColRe.FindStringSubmatch(r); m != nil {
				e.Line, _ = strconv.Atoi(m[1])
				e.Col, _ = strconv.Atoi(m[2])
				e.Reason = firstLine(r)
			}
		}
		if e.Echo == "" {
			if m := esFailedQryRe.FindStringSubmatch(r); m != nil {
				e.Echo = m[1]
			} else if m := esCannotRe.FindStringSubmatch(r); m != nil {
				e.Echo = m[1]
			}
		}
	}
	if e.Reason == "" {
		// Konumsuz red: en anlamlı gerekçe root_cause (üst seviye "all shards
		// failed" yalnız sarmalayıcı) — "Failed to parse query" varsa o.
		for _, r := range esReasons(body) {
			if strings.HasPrefix(r, "Failed to parse query") {
				e.Reason = firstLine(r)
				break
			}
		}
		if e.Reason == "" {
			e.Reason = firstLine(rootReason)
		}
	}
	return e
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 300 {
		r := []rune(s)
		s = string(r[:300]) + "…"
	}
	return s
}

// QueryErrorPos — reddin konumu, KULLANICININ yazdığı sorgu içinde 0 tabanlı
// KARAKTER (rune) indeksi olarak. Yalnız ES'in yankıladığı metin kullanıcının
// metniyle AYNIYSA güvenilir (kısayol açılımı / kanonik çeviri metni
// değiştirdiyse konum başka bir dizeye aittir → ok=false). Saf.
func QueryErrorPos(err error, userQuery string) (int, bool) {
	var be *BadQueryError
	if !errors.As(err, &be) || be.Line <= 0 || be.Col <= 0 || be.Echo == "" || be.Echo != userQuery {
		return 0, false
	}
	lines := strings.Split(userQuery, "\n")
	if be.Line > len(lines) {
		return 0, false
	}
	pos := 0
	for i := 0; i < be.Line-1; i++ {
		pos += utf8.RuneCountInString(lines[i]) + 1
	}
	col := be.Col - 1
	if n := utf8.RuneCountInString(lines[be.Line-1]); col > n {
		col = n
	}
	return pos + col, true
}

// QueryErrorReason — reddin operatöre gösterilebilir gerekçe cümlesi.
func QueryErrorReason(err error) string {
	var be *BadQueryError
	if errors.As(err, &be) {
		return be.Reason
	}
	return ""
}
