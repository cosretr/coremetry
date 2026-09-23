package oracle

import (
	"context"
	"strings"
	"time"
)

// tsbind.go — v0.10.885 (operatör-bildirimli, prod 2026-09-23): zaman
// penceresi bind'i KOLONUN TİPİYLE.
//
// go-ora v2.9.0 düz time.Time'ı TIMESTAMP WITH TIME ZONE olarak bağlar
// (parameter_encode.go:40-46). Kolon TIMESTAMP(3) / DATE olunca Oracle
// karşılaştırma için KOLONU çevirir (INTERNAL_FUNCTION(MCA_ERR_TIMESTAMP))
// → partition budaması ve indeks düşer → 167 M satırlık tablo baştan sona
// taranır → 30 sn'de zaman aşımı. Operatör aynı pencereyi ek koşula
// TO_DATE('…') literaliyle yazınca sorgu 132 ms'de döndü: yüklem kolonu
// çevirmeden bind tarafında tipe oturunca budama geri geliyor.
//
// Çözüm sürücüden bağımsız ve metinde görünür: değer DİZE olarak bağlanır,
// tipi SQL tarafındaki fonksiyon verir — TO_DATE / TO_TIMESTAMP /
// TO_TIMESTAMP_TZ. Hangisi olduğu sözlükten (ALL_TAB_COLUMNS.DATA_TYPE)
// okunur; okunamazsa TimestampHasZone kutusu karar verir. Operatör poll
// önizlemesinde tam ifadeyi görür ("öncelik düşebilir, cümle yalan olamaz"
// kuralının sorgu hâli).

type tsKind int

const (
	tsKindTimestamp tsKind = iota // TIMESTAMP(n) — TO_TIMESTAMP, FF6, duvar saati
	tsKindDate                    // DATE — TO_DATE, saniye, duvar saati
	tsKindTZ                      // TIMESTAMP WITH [LOCAL] TIME ZONE — TO_TIMESTAMP_TZ, UTC anı
)

const (
	tsFmtTimestamp = "2006-01-02 15:04:05.000000"
	tsFmtDate      = "2006-01-02 15:04:05"
	tsFmtTZ        = "2006-01-02 15:04:05.000000 -07:00"
)

// tsKindOf — SAF: sözlük DATA_TYPE'ından tür. Boş/bilinmeyen tip →
// TimestampHasZone kutusu (dilimli → TZ, değilse TIMESTAMP). Sözlük
// dolu ise kutuyu EZER: kolon DATE iken TO_TIMESTAMP kullanmak kolonu
// yine çevirtirdi.
func tsKindOf(dataType string, hasZone bool) tsKind {
	t := strings.ToUpper(strings.TrimSpace(dataType))
	switch {
	case t == "DATE":
		return tsKindDate
	case strings.HasPrefix(t, "TIMESTAMP") && strings.Contains(t, "TIME ZONE"):
		return tsKindTZ
	case strings.HasPrefix(t, "TIMESTAMP"):
		return tsKindTimestamp
	case hasZone:
		return tsKindTZ
	}
	return tsKindTimestamp
}

// bindExpr — SAF: n numaralı bind için SQL ifadesi. Biçim literali sabit
// (operatör girdisi yok); kolon ifadenin DIŞINDA kalır.
func (k tsKind) bindExpr(n int) string {
	p := ":" + itoa(n)
	switch k {
	case tsKindDate:
		return "TO_DATE(" + p + ", 'YYYY-MM-DD HH24:MI:SS')"
	case tsKindTZ:
		return "TO_TIMESTAMP_TZ(" + p + ", 'YYYY-MM-DD HH24:MI:SS.FF6 TZH:TZM')"
	}
	return "TO_TIMESTAMP(" + p + ", 'YYYY-MM-DD HH24:MI:SS.FF6')"
}

// bindValue — SAF: bağlanacak dize. Dilimsiz türlerde kaynağın duvar saati
// (bindTime'ın v0.10.601 sözleşmesi aynen), TZ türünde UTC anı "+00:00".
func (k tsKind) bindValue(t time.Time, loc *time.Location) string {
	switch k {
	case tsKindDate:
		return bindTime(t, loc, false).Format(tsFmtDate)
	case tsKindTZ:
		return t.UTC().Format(tsFmtTZ)
	}
	return bindTime(t, loc, false).Format(tsFmtTimestamp)
}

// label — arayüz/önizleme için kısa ad.
func (k tsKind) label() string {
	switch k {
	case tsKindDate:
		return "TO_DATE"
	case tsKindTZ:
		return "TO_TIMESTAMP_TZ"
	}
	return "TO_TIMESTAMP"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// tsTypeSQL — sözlük: zaman kolonunun DATA_TYPE'ı (identifier'lar bind,
// büyük harf; scanIndexSQL ile aynı duruş).
func tsTypeSQL() string {
	return `SELECT DATA_TYPE FROM ALL_TAB_COLUMNS WHERE OWNER = :1 AND TABLE_NAME = :2 AND COLUMN_NAME = :3 FETCH FIRST 1 ROWS ONLY`
}

// runTsTypeProbe — açık bağlantıda sözlük okuması; "" = kolon sözlükte yok.
func runTsTypeProbe(ctx context.Context, db sqlDB, cfg SourceConfig, budget time.Duration, secret string) (string, error) {
	tsCol, _, _, err := queryParts(cfg)
	if err != nil {
		return "", err
	}
	owner, table, col := strings.ToUpper(cfg.Schema), strings.ToUpper(cfg.Table), strings.ToUpper(tsCol)
	_, rows, err := runRows(ctx, db, tsTypeSQL(), []any{owner, table, col}, budget, secret)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return strings.ToUpper(strings.TrimSpace(cellString(firstCell(rows[0])))), nil
}

// probeTsType — poller yolu: kendi bağlantısını açar (defaultQueryRows deseni).
func (s *Service) probeTsType(ctx context.Context, src SourceConfig) (string, error) {
	db, secret, err := s.open(src)
	if err != nil {
		return "", err
	}
	defer db.Close()
	return runTsTypeProbe(ctx, db, src, queryTimeout(src), secret)
}
