package oracle

// client.go — Oracle bağlantısı + salt-okunur örnek sorgu (v0.10.580,
// Aşama 1; audit docs/audit/oracle-error-log-2026-09-09.md §1, §3).
//
// Sürücü: github.com/sijms/go-ora/v2 — TNS'in SAF Go uygulaması, CGO
// istemez. godror değerlendirildi ve REDDEDİLDİ: CGO + Oracle Instant
// Client ister, Instant Client'ın musl build'i yok, Dockerfile
// CGO_ENABLED=0 + alpine:3.20 ile doğrudan çelişir ve imaja 35-80 MB
// bindirir ("tek binary, tek imaj" kısıtı).
//
// SQL disiplini (audit §3), üçü de bu dosyada zorunlu:
//   1. YALNIZ SELECT üretilir — başka ifade türü kurulmaz.
//   2. DEĞERLER daima bind (:1, :2, …). String concat ile yüklem
//      kurulmaz; Grafana'daki LIKE '<operationcode>' kalıbı taşınmadı.
//   3. Identifier'lar (şema/tablo/kolon) bind EDİLEMEZ; interpolasyona
//      yalnız identRe'den geçmiş adlar girer ve builder Normalize'ın
//      koştuğuna GÜVENMEZ, kendisi de doğrular.
//
// Şifre ve DSN hiçbir log satırına, hiçbir hata metnine, hiçbir API
// cevabına girmez: dışarı çıkan her hata redactSecrets'tan geçer.

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/cilcenk/coremetry/internal/chstore"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	go_ora "github.com/sijms/go-ora/v2"
)

// sqlDB — database/sql seam'i. Üretimde *sql.DB; testler sahte verebilir
// (Service.openDB alanı).
type sqlDB interface {
	PingContext(ctx context.Context) error
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	SetMaxOpenConns(n int)
	SetMaxIdleConns(n int)
	SetConnMaxLifetime(d time.Duration)
	Close() error
}

// openOracleDB — go-ora sürücüsü ("oracle" adıyla kendi init'inde
// sql.Register'lanır). DSN LOG'A BASILMAZ.
func openOracleDB(dsn string) (sqlDB, error) {
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, err
	}
	return db, nil
}

const (
	// testSampleLimit — bağlantı testinin FETCH FIRST tavanı.
	testSampleLimit = 5
	// wideTestWindow — dar pencere boş dönerse ikinci deneme. "Trafik yok"
	// ile "TZ kaymış" ayrımını yapan tek şey bu (v0.10.335 Influx dersi).
	wideTestWindow = 24 * time.Hour
	// sampleValueMax — örnek hücre kırpması (CLOB'lu tabloda cevap şişmesin).
	sampleValueMax = 200
	// maxSampleLimit — builder'ın kabul ettiği en büyük satır tavanı.
	maxSampleLimit = 50
)

// buildSampleQuery — SAF ve testin doğrudan pinlediği seam. Üretilen metin
// TEK bir SELECT'tir; zaman ve tip değerleri bind, identifier'lar
// doğrulanmış. limit bir int'tir ve %d ile basılır — enjekte edilecek bir
// dize yoktur (bind edilmemesinin sebebi: FETCH FIRST bind'i sürücüden
// sürücüye değişiyor, sayıysa hiçbir riski yok).
func buildSampleQuery(cfg SourceConfig, from, to time.Time, limit int) (string, []any, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > maxSampleLimit {
		limit = maxSampleLimit
	}
	tsCol, typCol, types, err := queryParts(cfg)
	if err != nil {
		return "", nil, err
	}
	loc, err := ResolveLocation(cfg)
	if err != nil {
		return "", nil, err
	}

	// v0.10.601 — bind değerleri kaynağın duvar saatine (bindTime): go-ora
	// time.Time'ı BİLEŞENLERİYLE gönderir, UTC anı bağlamak dilimsiz kolonda
	// 3 saat kaydırırdı. Poll sorgusuyla aynı yol — iki gerçek yok.
	args := []any{bindTime(from, loc, cfg.TimestampHasZone), bindTime(to, loc, cfg.TimestampHasZone)}
	var b strings.Builder
	// Kolon listesi yerine * : Aşama 2'nin alan eşlemesini yazacak operatör
	// tablonun GERÇEK kolonlarını testte görmeli (FETCH FIRST tavanı zaten var).
	sel, err := selectList(cfg)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(&b, "SELECT %s FROM %s.%s\nWHERE %s >= :1 AND %s < :2", sel, cfg.Schema, cfg.Table, tsCol, tsCol)
	binds := make([]string, 0, len(types))
	for _, t := range types {
		args = append(args, t)
		binds = append(binds, fmt.Sprintf(":%d", len(args)))
	}
	fmt.Fprintf(&b, "\n  AND %s IN (%s)", typCol, strings.Join(binds, ", "))
	if cfg.ExtraWhere != "" {
		fmt.Fprintf(&b, "\n  AND (%s)", cfg.ExtraWhere)
	}
	fmt.Fprintf(&b, "\nORDER BY %s DESC\nFETCH FIRST %d ROWS ONLY", tsCol, limit)
	return b.String(), args, nil
}

// queryParts — SAF: iki sorgu üreticisinin (örnek + poll) ortak doğrulaması:
// identifier'lar, extraWhere kapısı, tip listesi. Metin üretmez.
// selectList — SAF (v0.10.843): SELECT listesi. Bayrak kapalıysa `*`
// (bugünkü davranış); açıksa eşlenen kolonlar fieldOrder sırasıyla, kapalı
// alan ("") atlanır, aynı kolon iki alana eşlenmişse bir kez. ResolveColumns
// identifier kapısını geçirir — listeye serbest metin girmez.
func selectList(cfg SourceConfig) (string, error) {
	if !cfg.SelectMappedOnly {
		return "*", nil
	}
	cols, err := ResolveColumns(cfg)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(fieldOrder))
	for _, f := range fieldOrder {
		c := cols[f]
		if c == "" || seen[strings.ToUpper(c)] {
			continue
		}
		seen[strings.ToUpper(c)] = true
		out = append(out, c)
	}
	return strings.Join(out, ", "), nil
}

func queryParts(cfg SourceConfig) (tsCol, typCol string, types []string, err error) {
	tsCol = cfg.TimestampColumn
	if tsCol == "" {
		tsCol = DefaultTimestampColumn
	}
	typCol = cfg.TypeColumn
	if typCol == "" {
		typCol = DefaultTypeColumn
	}
	for _, f := range []struct{ name, val string }{
		{"şema", cfg.Schema}, {"tablo", cfg.Table},
		{"timestampColumn", tsCol}, {"typeColumn", typCol},
	} {
		if !identRe.MatchString(f.val) {
			return "", "", nil, fmt.Errorf("%s adı Oracle identifier'ı olmalı: %q", f.name, f.val)
		}
	}
	if err := validateExtraWhere(cfg.ExtraWhere, "extraWhere"); err != nil {
		return "", "", nil, err
	}
	types = cfg.TypeFilter
	if len(types) == 0 {
		types = DefaultTypeFilter()
	}
	return tsCol, typCol, types, nil
}

// buildPollQuery — v0.10.601 (Aşama 2 poller). SAF. Örnek sorgudan farkı:
// pencere (from, to] — from DIŞARIDA (watermark'ın kendisi zaten yazıldı),
// ARTAN sıra (watermark en büyük görülen zamana ilerler), tavan pollRowCap
// (tavana çarpan tik "capped" ilan eder, bir sonraki granülde devam eder).
// Zaman/tip bind, identifier'lar doğrulanmış, limit %d ile basılan bir int.
func buildPollQuery(cfg SourceConfig, from, to time.Time, limit int) (string, []any, error) {
	if limit < 1 || limit > pollRowCap {
		limit = pollRowCap
	}
	tsCol, typCol, types, err := queryParts(cfg)
	if err != nil {
		return "", nil, err
	}
	loc, err := ResolveLocation(cfg)
	if err != nil {
		return "", nil, err
	}
	args := []any{bindTime(from, loc, cfg.TimestampHasZone), bindTime(to, loc, cfg.TimestampHasZone)}
	var b strings.Builder
	sel, err := selectList(cfg)
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(&b, "SELECT %s FROM %s.%s\nWHERE %s > :1 AND %s <= :2", sel, cfg.Schema, cfg.Table, tsCol, tsCol)
	binds := make([]string, 0, len(types))
	for _, t := range types {
		args = append(args, t)
		binds = append(binds, fmt.Sprintf(":%d", len(args)))
	}
	fmt.Fprintf(&b, "\n  AND %s IN (%s)", typCol, strings.Join(binds, ", "))
	if cfg.ExtraWhere != "" {
		fmt.Fprintf(&b, "\n  AND (%s)", cfg.ExtraWhere)
	}
	fmt.Fprintf(&b, "\nORDER BY %s ASC\nFETCH FIRST %d ROWS ONLY", tsCol, limit)
	return b.String(), args, nil
}

// dsnFor — bağlantı dizesi. ASLA log'lanmaz, ASLA API cevabına girmez;
// dönen dize yalnız sql.Open'a gider. İkinci dönüş şifre (redaksiyon için).
func (s *Service) dsnFor(src SourceConfig) (dsn, secret string, err error) {
	pass, err := s.passwordFor(src)
	if err != nil {
		return "", "", err
	}
	if src.DSN != "" {
		return src.DSN, pass, nil
	}
	port := src.Port
	if port == 0 {
		port = DefaultPort
	}
	return go_ora.BuildUrl(src.Host, port, src.ServiceName, src.User, pass, nil), pass, nil
}

// open — havuz ayarlı bağlantı. Havuz kelepçeleri Normalize'dan gelir;
// gelmediyse (çıplak cfg) varsayılana düşer.
func (s *Service) open(src SourceConfig) (sqlDB, string, error) {
	dsn, secret, err := s.dsnFor(src)
	if err != nil {
		return nil, "", err
	}
	openFn := s.openDB
	if openFn == nil {
		openFn = openOracleDB
	}
	db, err := openFn(dsn)
	if err != nil {
		return nil, secret, fmt.Errorf("bağlantı açılamadı: %s", redactSecrets(err.Error(), secret, dsn))
	}
	maxOpen := src.MaxOpenConns
	if maxOpen < MinMaxOpenConns || maxOpen > MaxMaxOpenConns {
		maxOpen = DefaultMaxOpenConns
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, secret, nil
}

// queryTimeout — kaynağın sorgu bütçesi (kelepçe dışıysa varsayılan).
func queryTimeout(src SourceConfig) time.Duration {
	sec := src.QueryTimeoutSec
	if sec < MinQueryTimeoutSec || sec > MaxQueryTimeoutSec {
		sec = DefaultQueryTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

// Ping — yalnız erişilebilirlik. Bağlantı kurulamazsa Coremetry'nin geri
// kalanı etkilenmez: hata döner, hiçbir global durum değişmez.
func (s *Service) Ping(ctx context.Context, src SourceConfig) error {
	if s == nil {
		return fmt.Errorf("oracle servisi yok")
	}
	db, secret, err := s.open(src)
	if err != nil {
		return err
	}
	defer db.Close()
	pctx, cancel := context.WithTimeout(ctx, queryTimeout(src))
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		return fmt.Errorf("bağlantı: %s", redactSecrets(err.Error(), secret))
	}
	return nil
}

// TestResult — POST /api/settings/oracle/test cevabı. Bağlantı denemesinin
// BAŞARISIZLIĞI operatörün sorusuna BAŞARILI bir cevaptır: uç 200 + ok:false
// döner (influx TestResult sözleşmesi).
type TestResult struct {
	OK               bool                `json:"ok"`
	Error            string              `json:"error,omitempty"`
	PasswordResolved bool                `json:"passwordResolved"`
	Columns          []string            `json:"columns"`
	Sample           []map[string]string `json:"sample,omitempty"`
	RowCount         int                 `json:"rowCount"`
	LatencyMs        int64               `json:"latencyMs"`
	// Query — koşan SELECT'in metni. Şifre/DSN içermez (yalnız identifier'lar
	// ve bind yer tutucuları); operatör Aşama 2'nin sorgusunu burada görür.
	Query string `json:"query,omitempty"`
	// WideWindow — örnek satırlar 24 saatlik ikinci denemeden geldi (dar
	// pencere boştu). Arayüz bunu SÖYLEMELİ: aksi hâlde operatör 15 dakikalık
	// pencerede veri var sanır.
	WideWindow bool `json:"wideWindow,omitempty"`
	// Hint — "hata yok + satır yok" ikircikliğinin okunabilir açıklaması.
	Hint string `json:"hint,omitempty"`
	// v0.10.768 — operatör "önce test edelim": pencere, poller'ın koşacağı
	// sorgu + bind değerleri, sözlükten tam-tarama kanıtı, pencere özeti.
	WindowMin int        `json:"windowMin"`
	PollQuery string     `json:"pollQuery,omitempty"`
	PollBinds []string   `json:"pollBinds,omitempty"`
	Scan      *ScanCheck `json:"scan,omitempty"`
	// v0.10.845 — LONG/LONG RAW kolon ön kontrolü; örnek sorgudan ÖNCE koşar
	// ki ORA-00997 düşse bile operatör sebebi görsün.
	Long    *LongCheck     `json:"long,omitempty"`
	Summary *WindowSummary `json:"summary,omitempty"`
}

// ── v0.10.768 — tam-tarama ön kontrolü, pencere özeti ────────────────────
//
// Operatör (2026-09-17): "Oracle full scan / kilitli sorgu atmasın, önce bir
// test edelim, gerekirse sorgu görünür olsun; test ederken hangi servis /
// operasyon / trace geldiğini göreyim (son 5 dk gibi)."
//
// Kilit: Oracle'da SELECT kilit almaz; kilit alan tek okuma FOR UPDATE —
// üreticiler yazmaz, konsol + extraWhere reddeder. Tam tarama: her sorgu
// zaman kolonuna bağlı pencere + FETCH FIRST taşır; tam tarama olup
// olmaması YALNIZ o kolonun indeksli (ilk kolon) ya da tablonun onunla
// partition'lı olmasına bağlıdır. Sözlük görünümleri (ALL_IND_COLUMNS,
// ALL_PART_KEY_COLUMNS, ALL_TABLES) bunu ucuz söyler; ScanCheck bu kanıttır.

// TestOptions — operatör seçimleri + CH tarafı bağımlılığı (oracle paketi
// chstore.Store'u bilmez; handler s.store.TraceServicesByIDs verir).
type TestOptions struct {
	// WindowMin — test penceresi: 5 | 15 | 60 (başkası → DefaultTestWindowMin).
	WindowMin int
	// TraceLookup — trace id → Coremetry servisi ([from, to] penceresinde).
	// nil → arama yapılmaz, özet bunu söyler.
	TraceLookup func(ctx context.Context, ids []string, from, to time.Time) (map[string]string, error)
}

const (
	DefaultTestWindowMin = 15
	// summaryRowCap — pencere özetinin FETCH FIRST tavanı (poller tavanı
	// 5000; test bir kez koşar, 500 yeter ve "capped" ilan eder).
	summaryRowCap = 500
	// summaryTopN — operasyon / servis listelerinin uzunluğu.
	summaryTopN = 10
	// summaryLookupIDs — CH'de aranan ayrık trace id tavanı.
	summaryLookupIDs = 200
	// summaryLookupPad — Oracle damgası ile span zamanı arasındaki pay.
	summaryLookupPad = 5 * time.Minute
)

// ClampTestWindow — SAF: 5 / 15 / 60; başkası varsayılan.
func ClampTestWindow(n int) int {
	switch n {
	case 5, 15, 60:
		return n
	default:
		return DefaultTestWindowMin
	}
}

// ScanCheck — sözlükten okunan tam-tarama kanıtı.
type ScanCheck struct {
	Checked      bool   `json:"checked"`
	Error        string `json:"error,omitempty"`
	TsColumn     string `json:"tsColumn"`
	Found        bool   `json:"found"` // ALL_TABLES'ta satır var (view/synonym değilse)
	Indexed      bool   `json:"indexed"`
	IndexName    string `json:"indexName,omitempty"`
	Partitioned  bool   `json:"partitioned"`
	PartitionKey string `json:"partitionKey,omitempty"`
	NumRows      int64  `json:"numRows"`
	LastAnalyzed string `json:"lastAnalyzed,omitempty"`
}

// FullScanRisk — SAF: tablo bulundu, zaman kolonu ne indeksli ne partition
// anahtarı → her poll tam tarama.
// LongCheck — v0.10.845 (operatör: "long data hatası"): tabloda LONG / LONG
// RAW kolon var mı? Oracle FETCH FIRST'ü inline view + ROW_NUMBER ile yeniden
// yazar ve select listesinde LONG kolon varken ORA-00997 verir. SELECT *
// (selectMappedOnly kapalı) her LONG kolonu listeye alır; açıkken yalnız
// EŞLENEN bir LONG kolon düşürür. Selected = sorgunun listesine giren LONG
// kolonlar; MappedOnly = kontrolün baktığı kip (hüküm forma değil koşan
// teste bağlı). Sözlük okunamazsa Checked=false, hüküm yok.
type LongCheck struct {
	Checked    bool     `json:"checked"`
	Error      string   `json:"error,omitempty"`
	MappedOnly bool     `json:"mappedOnly"`
	Columns    []string `json:"columns"`  // tablodaki LONG/LONG RAW kolonlar (büyük harf, COLUMN_ID sırası)
	Selected   []string `json:"selected"` // select listesine GİREN LONG kolonlar
}

func longColumnSQL() string {
	return `SELECT COLUMN_NAME FROM ALL_TAB_COLUMNS WHERE OWNER = :1 AND TABLE_NAME = :2 AND DATA_TYPE IN ('LONG', 'LONG RAW') ORDER BY COLUMN_ID`
}

// longCheckFromRows — SAF. rows: ALL_TAB_COLUMNS satırları (COLUMN_NAME).
func longCheckFromRows(cfg SourceConfig, rows []map[string]any) LongCheck {
	c := LongCheck{Checked: true, MappedOnly: cfg.SelectMappedOnly, Columns: []string{}, Selected: []string{}}
	inList := func(string) bool { return true } // SELECT *: her kolon listede
	if cfg.SelectMappedOnly {
		mapped := map[string]bool{}
		if cols, err := ResolveColumns(cfg); err == nil {
			for _, col := range cols {
				if col != "" {
					mapped[strings.ToUpper(col)] = true
				}
			}
		}
		inList = func(name string) bool { return mapped[name] }
	}
	for _, r := range rows {
		name := strings.ToUpper(strings.TrimSpace(cellString(firstCell(r))))
		if name == "" {
			continue
		}
		c.Columns = append(c.Columns, name)
		if inList(name) {
			c.Selected = append(c.Selected, name)
		}
	}
	return c
}

func runLongCheck(ctx context.Context, db sqlDB, cfg SourceConfig, budget time.Duration, secret string) *LongCheck {
	owner, table := strings.ToUpper(cfg.Schema), strings.ToUpper(cfg.Table)
	_, rows, err := runRows(ctx, db, longColumnSQL(), []any{owner, table}, budget, secret)
	if err != nil {
		return &LongCheck{Error: "sözlük (ALL_TAB_COLUMNS): " + err.Error(), MappedOnly: cfg.SelectMappedOnly, Columns: []string{}, Selected: []string{}}
	}
	c := longCheckFromRows(cfg, rows)
	return &c
}

func (c ScanCheck) FullScanRisk() bool {
	return c.Checked && c.Found && !c.Indexed && !c.Partitioned
}

// Sözlük sorguları — SAF, yalnız SELECT, identifier'lar bind (sözlük
// büyük harf saklar; identRe tırnaksız ad kabul ettiğinden ToUpper doğru).
func scanIndexSQL() string {
	return `SELECT INDEX_NAME FROM ALL_IND_COLUMNS WHERE TABLE_OWNER = :1 AND TABLE_NAME = :2 AND COLUMN_NAME = :3 AND COLUMN_POSITION = 1 FETCH FIRST 5 ROWS ONLY`
}
func scanPartitionSQL() string {
	return `SELECT COLUMN_NAME FROM ALL_PART_KEY_COLUMNS WHERE OWNER = :1 AND NAME = :2 AND OBJECT_TYPE = 'TABLE' AND COLUMN_POSITION = 1 FETCH FIRST 1 ROWS ONLY`
}
func scanTableSQL() string {
	return `SELECT NUM_ROWS, LAST_ANALYZED FROM ALL_TABLES WHERE OWNER = :1 AND TABLE_NAME = :2 FETCH FIRST 1 ROWS ONLY`
}

// scanCheckFromRows — SAF: üç sözlük cevabından hüküm.
func scanCheckFromRows(tsCol string, idxRows, partRows, tblRows []map[string]any) ScanCheck {
	c := ScanCheck{Checked: true, TsColumn: strings.ToUpper(tsCol)}
	if len(idxRows) > 0 {
		c.Indexed = true
		c.IndexName = cellString(firstCell(idxRows[0]))
	}
	if len(partRows) > 0 {
		c.PartitionKey = strings.ToUpper(cellString(firstCell(partRows[0])))
		c.Partitioned = c.PartitionKey == c.TsColumn
	}
	if len(tblRows) > 0 {
		c.Found = true
		for k, v := range tblRows[0] {
			switch strings.ToUpper(k) {
			case "NUM_ROWS":
				c.NumRows = cellInt64(v)
			case "LAST_ANALYZED":
				if t, ok := v.(time.Time); ok {
					c.LastAnalyzed = t.Format("2006-01-02")
				}
			}
		}
	}
	return c
}

func firstCell(row map[string]any) any {
	for _, v := range row {
		return v
	}
	return nil
}

func cellInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int32:
		return int64(t)
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	}
	return 0
}

// runScanCheck — üç sözlük okuması; herhangi biri düşerse Checked=false +
// hata (redaksiyonlu). Sözlük okuması yetkisizse de dürüstçe "kontrol
// edilemedi" der, tabloyu taramaz.
func runScanCheck(ctx context.Context, db sqlDB, cfg SourceConfig, budget time.Duration, secret string) *ScanCheck {
	tsCol, _, _, err := queryParts(cfg)
	if err != nil {
		return &ScanCheck{Error: err.Error(), TsColumn: strings.ToUpper(tsCol)}
	}
	owner, table, col := strings.ToUpper(cfg.Schema), strings.ToUpper(cfg.Table), strings.ToUpper(tsCol)
	_, idx, err := runRows(ctx, db, scanIndexSQL(), []any{owner, table, col}, budget, secret)
	if err != nil {
		return &ScanCheck{Error: "sözlük (ALL_IND_COLUMNS): " + err.Error(), TsColumn: col}
	}
	_, part, err := runRows(ctx, db, scanPartitionSQL(), []any{owner, table}, budget, secret)
	if err != nil {
		return &ScanCheck{Error: "sözlük (ALL_PART_KEY_COLUMNS): " + err.Error(), TsColumn: col}
	}
	_, tbl, err := runRows(ctx, db, scanTableSQL(), []any{owner, table}, budget, secret)
	if err != nil {
		return &ScanCheck{Error: "sözlük (ALL_TABLES): " + err.Error(), TsColumn: col}
	}
	c := scanCheckFromRows(tsCol, idx, part, tbl)
	return &c
}

// pollPreview — SAF: poller'ın bu pencere için koşacağı TAM sorgu + bind
// değerleri (operatör/DBA gözüyle inceleme için; şifre/DSN yok).
func pollPreview(cfg SourceConfig, from, to time.Time) (string, []string) {
	q, args, err := buildPollQuery(cfg, from, to, pollRowCap)
	if err != nil {
		return "", nil
	}
	binds := make([]string, 0, len(args))
	for _, a := range args {
		binds = append(binds, fmt.Sprint(a))
	}
	return q, binds
}

// NameCount — ad + sayı (operasyon kodu / servis listeleri).
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// WindowSummary — pencerede ne geldi: satır, eşleme sayıları, operasyon
// kodları, ayrık trace id sayısı ve kaçının Coremetry'de bulunduğu +
// o trace'lerin servisleri. "Servis" Oracle satırında yoktur; eşleşen
// trace'in Coremetry servisidir (operatör onayı 2026-09-17).
type WindowSummary struct {
	WindowMin   int         `json:"windowMin"`
	Rows        int         `json:"rows"`
	Capped      bool        `json:"capped"`
	Mapped      int         `json:"mapped"`
	NoTimestamp int         `json:"noTimestamp"`
	BadTraceID  int         `json:"badTraceId"`
	Operations  []NameCount `json:"operations"`
	ErrorCodes  []NameCount `json:"errorCodes"`
	TraceIDs    int         `json:"traceIds"`
	LookupDone  bool        `json:"lookupDone"`
	LookupError string      `json:"lookupError,omitempty"`
	TracesFound int         `json:"tracesFound"`
	Services    []NameCount `json:"services"`
	Error       string      `json:"error,omitempty"`
}

// topCounts — SAF: sayıya göre azalan, eşitlikte ada göre; ilk n.
func topCounts(m map[string]int, n int) []NameCount {
	out := make([]NameCount, 0, len(m))
	for k, v := range m {
		out = append(out, NameCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// distinctTraceIDs — SAF: eşlenmiş satırlardan boş olmayan ayrık id'ler,
// ilk görülme sırasıyla, tavanlı.
func distinctTraceIDs(rows []chstore.OracleErrorRow, capN int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range rows {
		if r.TraceID == "" || seen[r.TraceID] {
			continue
		}
		seen[r.TraceID] = true
		if len(out) < capN {
			out = append(out, r.TraceID)
		}
	}
	return out
}

// summarizeWindow — SAF: eşlenmiş satırlar + eşleme istatistiği + CH arama
// sonucundan özet. lookup nil → LookupDone=false.
func summarizeWindow(windowMin int, rows []chstore.OracleErrorRow, st MapStats, capped bool, lookup map[string]string, lookupDone bool, lookupErr error) WindowSummary {
	out := WindowSummary{
		WindowMin: windowMin, Rows: st.Rows, Capped: capped, Mapped: st.Mapped,
		NoTimestamp: st.NoTimestamp, BadTraceID: st.BadTraceID,
		Operations: []NameCount{}, ErrorCodes: []NameCount{}, Services: []NameCount{},
		LookupDone: lookupDone,
	}
	ops, codes := map[string]int{}, map[string]int{}
	for _, r := range rows {
		op := r.OperationCode
		if op == "" {
			op = "(boş)"
		}
		ops[op]++
		if r.ErrorCode != "" {
			codes[r.ErrorCode]++
		}
	}
	out.Operations, out.ErrorCodes = topCounts(ops, summaryTopN), topCounts(codes, summaryTopN)
	ids := distinctTraceIDs(rows, len(rows)+1)
	out.TraceIDs = len(ids)
	if lookupErr != nil {
		out.LookupError = lookupErr.Error()
	}
	if lookupDone && lookup != nil {
		svc := map[string]int{}
		for _, id := range ids {
			if s, ok := lookup[id]; ok {
				out.TracesFound++
				if s == "" {
					s = "(servissiz)"
				}
				svc[s]++
			}
		}
		out.Services = topCounts(svc, summaryTopN)
	}
	return out
}

// runWindowSummary — poller sorgusunun aynısı (tavan summaryRowCap), eşleme
// (mapping.go), CH arama; hata özetin içinde, testi düşürmez.
func runWindowSummary(ctx context.Context, db sqlDB, cfg SourceConfig, budget time.Duration, secret string, from, to time.Time, opt TestOptions) *WindowSummary {
	q, args, err := buildPollQuery(cfg, from, to, summaryRowCap)
	if err != nil {
		return &WindowSummary{WindowMin: opt.WindowMin, Error: err.Error()}
	}
	_, raw, err := runRows(ctx, db, q, args, budget, secret)
	if err != nil {
		return &WindowSummary{WindowMin: opt.WindowMin, Error: err.Error()}
	}
	m, err := NewMapper(cfg)
	if err != nil {
		return &WindowSummary{WindowMin: opt.WindowMin, Rows: len(raw), Error: err.Error()}
	}
	rows, st := m.MapAll(raw)
	var lookup map[string]string
	var lerr error
	done := false
	if opt.TraceLookup != nil {
		ids := distinctTraceIDs(rows, summaryLookupIDs)
		if len(ids) > 0 {
			lctx, cancel := context.WithTimeout(ctx, budget)
			lookup, lerr = opt.TraceLookup(lctx, ids, from.Add(-summaryLookupPad), to.Add(summaryLookupPad))
			cancel()
		} else {
			lookup = map[string]string{}
		}
		done = lerr == nil
	}
	out := summarizeWindow(opt.WindowMin, rows, st, len(raw) >= summaryRowCap, lookup, done, lerr)
	return &out
}

// Test — formdaki kaynağı KAYDETMEDEN dener: şifre çözümü → Ping → son 15
// dakikadan en çok 5 satır. Dönen kolon listesi ve örnek satırlar Aşama 2'nin
// alan eşlemesini yazacak operatörün elindeki tek gerçek kanıt.
func (s *Service) Test(ctx context.Context, src SourceConfig) TestResult {
	return s.TestWith(ctx, src, TestOptions{})
}

// TestWith — v0.10.768: pencere seçimi + tam-tarama ön kontrolü + pencere
// özeti (poller sorgusu, tavan summaryRowCap) + CH'de trace/servis araması.
func (s *Service) TestWith(ctx context.Context, src SourceConfig, opt TestOptions) TestResult {
	opt.WindowMin = ClampTestWindow(opt.WindowMin)
	testWindow := time.Duration(opt.WindowMin) * time.Minute
	res := TestResult{Columns: []string{}, WindowMin: opt.WindowMin}
	if s == nil {
		res.Error = "oracle servisi yok"
		return res
	}
	defer func() { s.recordCheck(src, res) }()

	// Şifre çözümü ÖNCE ve ayrı: passwordResolved rozeti "env yok / dosya
	// yok"u sürücü hatasından ayırt etmek için var; open'ın içinde kalsaydı
	// bozuk bir DSN de çözülmemiş şifre gibi görünürdü.
	if _, err := s.passwordFor(src); err != nil {
		res.Error = err.Error()
		return res
	}
	res.PasswordResolved = true

	db, secret, err := s.open(src)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer db.Close()

	sqlText, args, err := buildSampleQuery(src, time.Now().Add(-testWindow), time.Now(), testSampleLimit)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Query = sqlText

	budget := queryTimeout(src)
	start := time.Now()
	pctx, pcancel := context.WithTimeout(ctx, budget)
	err = db.PingContext(pctx)
	pcancel()
	if err != nil {
		res.LatencyMs = time.Since(start).Milliseconds()
		res.Error = "bağlantı: " + redactSecrets(err.Error(), secret)
		return res
	}

	// v0.10.845 — örnek sorgudan ÖNCE: ORA-00997 düşerse sebebi yanında dursun.
	res.Long = runLongCheck(ctx, db, src, budget, secret)

	cols, sample, qerr := runSample(ctx, db, sqlText, args, budget, secret)
	res.LatencyMs = time.Since(start).Milliseconds()
	if qerr != nil {
		res.Error = qerr.Error()
		return res
	}
	res.Columns, res.Sample, res.RowCount = cols, sample, len(sample)

	// v0.10.580 — GENİŞ PENCERE İKİNCİ DENEMESİ. Hata yok + satır yok
	// İKİRCİKLİ bir cevaptır: trafik mi yok, zaman kolonu başka bir dilimde
	// mi (TZ), tip süzgeci mi tutmadı — operatör ayıramaz. Influx bunu
	// prod'da öğrendi (v0.10.335). İkinci deneme ayrımı METNE döker ve
	// örnek satırları da getirir: alan eşlemesini yazacak operatör
	// tablonun gerçek zaman damgası biçimini burada görür.
	if len(sample) == 0 {
		wideSQL, wideArgs, werr := buildSampleQuery(src, time.Now().Add(-wideTestWindow), time.Now(), testSampleLimit)
		if werr == nil {
			wcols, wsample, werr2 := runSample(ctx, db, wideSQL, wideArgs, budget, secret)
			hint, useWide := emptyProbeHint(opt.WindowMin, len(wsample), werr2)
			res.Hint = hint
			if useWide {
				res.Columns, res.Sample, res.WideWindow = wcols, wsample, true
			}
		}
	}

	// v0.10.768 — operatör: "full scan / kilit olmasın, önce test edelim,
	// sorgu görünür olsun; hangi servis/operasyon/trace geldiğini göreyim".
	now := time.Now()
	res.Scan = runScanCheck(ctx, db, src, budget, secret)
	res.PollQuery, res.PollBinds = pollPreview(src, now.Add(-testWindow), now)
	res.Summary = runWindowSummary(ctx, db, src, budget, secret, now.Add(-testWindow), now, opt)

	res.OK = true
	return res
}

// emptyProbeHint — SAF: dar pencere boş dönünce ne söyleneceğine karar verir.
// İkinci dönüş, geniş pencerenin örneklerinin KULLANILACAĞINI söyler.
//
// Üç durumun üçü de FARKLI bir eylem gerektirir ve operatör bunları ayırt
// edemezse yanlış yerde arar: geniş pencerede veri VARSA sorun zaman
// dilimi ya da seyrek trafiktir; geniş pencerede de yoksa sorun süzgeç ya
// da ad eşleşmesidir; geniş deneme HATA verirse ortada bir teşhis yoktur.
func emptyProbeHint(windowMin, wideRows int, wideErr error) (string, bool) {
	if windowMin <= 0 {
		windowMin = DefaultTestWindowMin
	}
	switch {
	case wideErr != nil:
		return fmt.Sprintf("son %d dakikada satır yok; geniş pencere denemesi de başarısız (%s)", windowMin, wideErr.Error()), false
	case wideRows > 0:
		return fmt.Sprintf("Son %d dakikada satır YOK ama son 24 saatte var — trafik seyrek olabilir ya da ", windowMin) +
			"zaman kolonu beklenenden farklı bir dilimde (TZ). Aşağıdaki örnek satırlar 24 saatlik pencereden.", true
	default:
		return "Son 24 saatte de satır yok — tip süzgeci, şema/tablo adı ya da zaman kolonu eşleşmiyor olabilir.", false
	}
}

// runSample — SORGUYU KOŞAR ve satırları dizeye çevirir. Test iki kez
// çağırıyor (dar pencere, sonra geniş); tek gövde olması iki denemenin
// aynı kırpma ve redaksiyon kurallarını paylaşmasını garanti eder.
func runSample(ctx context.Context, db sqlDB, sqlText string, args []any, budget time.Duration, secret string) ([]string, []map[string]string, error) {
	cols, raw, err := runRows(ctx, db, sqlText, args, budget, secret)
	if err != nil {
		return nil, nil, err
	}
	var out []map[string]string
	for _, r := range raw {
		row := make(map[string]string, len(r))
		for c, v := range r {
			row[c] = formatCell(v)
		}
		out = append(out, row)
	}
	return cols, out, nil
}

// runRows — v0.10.601: HAM hücreler (time.Time, []byte, sayı) — poller'ın
// eşlemesi tipe göre karar verir; kırpma yok (formatCell'in 200'ü yalnız
// Settings örneği için). Hata metinleri redaksiyondan geçer.
func runRows(ctx context.Context, db sqlDB, sqlText string, args []any, budget time.Duration, secret string) ([]string, []map[string]any, error) {
	qctx, qcancel := context.WithTimeout(ctx, budget)
	defer qcancel()
	rows, err := db.QueryContext(qctx, sqlText, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("sorgu: %s", redactSecrets(err.Error(), secret))
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("kolonlar: %s", redactSecrets(err.Error(), secret))
	}
	var out []map[string]any
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, fmt.Errorf("satır: %s", redactSecrets(err.Error(), secret))
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = cells[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("satır akışı: %s", redactSecrets(err.Error(), secret))
	}
	return cols, out, nil
}

// formatCell — SAF: bir hücreyi görüntülenebilir dizeye çevirir ve kırpar.
func formatCell(v any) string {
	var s string
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		s = string(t)
	case time.Time:
		s = t.UTC().Format(time.RFC3339Nano)
	case string:
		s = t
	default:
		s = fmt.Sprint(t)
	}
	if len(s) > sampleValueMax {
		return s[:sampleValueMax] + "…"
	}
	return s
}

// dsnCredRe — `oracle://kullanıcı:şifre@host` içindeki şifreyi yakalar.
var dsnCredRe = regexp.MustCompile(`(?i)(oracle://[^:@/\s]*):[^@\s]*@`)

// redactSecrets — SAF: dışarı çıkan her hata metninden şifreyi ve DSN'i
// siler. go-ora bazı hatalara bağlantı dizesini ekliyor; o dize kullanıcı
// adı ve şifre taşır. "Şifre log'a ASLA girmez" bir temenni değil, bu
// fonksiyonun sözleşmesi.
func redactSecrets(msg string, secrets ...string) string {
	out := dsnCredRe.ReplaceAllString(msg, "$1:***@")
	for _, sec := range secrets {
		if len(sec) < 3 {
			continue // çok kısa: metnin her yerine rastlar, mesajı okunmaz eder
		}
		out = strings.ReplaceAll(out, sec, "***")
	}
	return out
}
