package logstore

// sort.go — v0.10.1833 (Log Search Faz 1 dilim 1.10; audit §3.3 "çoklu
// alan sıralama ≤3, yalnız sıralanabilir alan, zaman eşitlik bozucu").
//
// /logs listesi URL `sort=service:asc,level:desc` (≤ MaxSortKeys anahtar)
// ile SUNUCUDA sıralanır. Alan adları LogTable kolon kimlikleridir:
// yerleşikler (time / service / level / cluster / pod) arka uca göre
// çözülür, diğerleri belge alan yoludur. Sıralanamayan alan (ES text
// alanı, eşlemede olmayan alan, CH'de sabit kolon dışı) 500 DEĞİL,
// *SortError (ErrBadQuery sarmalı) → 400 {error, detail, field}.
// Zaman her zaman eşitlik bozucu olarak sona eklenir (listede yoksa);
// ardından belge kimliği (ES _shard_doc/_doc, CH rowkey).

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// MaxSortKeys — operatörün seçebileceği en çok anahtar (zaman eşitlik
// bozucu sayılmaz).
const MaxSortKeys = 3

// SortKey — bir sıralama anahtarı. Field LogTable kolon kimliği.
type SortKey struct {
	Field string
	Desc  bool
}

// SortFieldTime — zaman kolonu kimliği (LogTable 'time').
const SortFieldTime = "time"

// sortFieldRe — alan yolu karakter kümesi: harf/rakam/._-@ ve iki nokta yok
// (URL ayıracı). Sorgu gövdesine yalnız JSON anahtarı (ES) ya da beyaz liste
// kolonu (CH) olarak girer; yine de dar tutulur.
var sortFieldRe = regexp.MustCompile(`^[A-Za-z0-9_@][A-Za-z0-9_.@-]{0,199}$`)

// ParseSortParam — SAF: "a:asc,b:desc" → anahtarlar. Yön yoksa asc.
// Boş → nil (bugünkü zaman sırası). Hata: > MaxSortKeys, bozuk alan,
// bilinmeyen yön, tekrar eden alan.
func ParseSortParam(raw string) ([]SortKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > MaxSortKeys {
		return nil, &SortError{Msg: fmt.Sprintf("En çok %d sıralama anahtarı seçilebilir.", MaxSortKeys)}
	}
	seen := map[string]struct{}{}
	out := make([]SortKey, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		field, dir := p, "asc"
		if i := strings.LastIndexByte(p, ':'); i >= 0 {
			field, dir = p[:i], p[i+1:]
		}
		if !sortFieldRe.MatchString(field) {
			return nil, &SortError{Field: field, Msg: "Sıralama alanı adı geçersiz."}
		}
		if dir != "asc" && dir != "desc" {
			return nil, &SortError{Field: field, Msg: "Sıralama yönü asc ya da desc olmalı."}
		}
		if _, dup := seen[field]; dup {
			return nil, &SortError{Field: field, Msg: "Aynı alan iki kez sıralanamaz."}
		}
		seen[field] = struct{}{}
		out = append(out, SortKey{Field: field, Desc: dir == "desc"})
	}
	return out, nil
}

// SortSig — SAF: anahtarların kanonik dizgesi (sıra ANLAMLIDIR — birincil
// ikinciden farklıdır; küme değil liste). Önbellek anahtarı ve cursor
// imzası. Boş liste → "".
func SortSig(keys []SortKey) string {
	if len(keys) == 0 {
		return ""
	}
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k.Field)
		if k.Desc {
			b.WriteString(":desc")
		} else {
			b.WriteString(":asc")
		}
	}
	return b.String()
}

// SortError — sıralama isteği reddi (sıralanamaz alan / bozuk parametre).
// ErrBadQuery'yi sarar: çağıran 400 yazar, 500 değil.
type SortError struct {
	Field  string
	Msg    string
	Reason string
}

func (e *SortError) Error() string {
	if e.Reason != "" {
		return e.Msg + " " + e.Reason
	}
	return e.Msg
}

func (e *SortError) Unwrap() error { return ErrBadQuery }

// unsortable — ortak "bu alan sıralanamaz" iletisi.
func unsortable(field, reason string) error {
	return &SortError{
		Field:  field,
		Msg:    fmt.Sprintf("“%s” alanına göre sıralanamaz.", field),
		Reason: reason,
	}
}

// AsSortError — errors.As kısayolu (API katmanı).
func AsSortError(err error) (*SortError, bool) {
	var se *SortError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

// timeTiebreakNeeded — SAF: zaman listede yoksa eşitlik bozucu olarak
// eklenir. Yönü Filter.Ascending (eski `asc=1` düğmesi).
func timeTiebreakNeeded(keys []SortKey) bool {
	for _, k := range keys {
		if k.Field == SortFieldTime {
			return false
		}
	}
	return true
}

// chSortCols — SAF: kolon kimlikleri → CH beyaz liste kolonları. CH arka
// ucunda yalnız sabit kolonlar sıralanır (öznitelik dizileri değil —
// chstore/logs_sort.go gerekçesi); diğerleri SortError.
func chSortCols(keys []SortKey) ([]chstore.LogSortCol, error) {
	out := make([]chstore.LogSortCol, 0, len(keys))
	for _, k := range keys {
		col := ""
		switch k.Field {
		case SortFieldTime:
			col = "time"
		case "service":
			col = "service_name"
		case "level":
			col = "severity_num"
		default:
			return nil, unsortable(k.Field, "ClickHouse log arka ucunda yalnız Time, service ve level sıralanır.")
		}
		out = append(out, chstore.LogSortCol{Col: col, Desc: k.Desc})
	}
	return out, nil
}
