package oracle

// mapping.go — v0.10.600 (Oracle Aşama 2, audit §5). SAF: Oracle satırı
// (kolon adı → hücre) → chstore.OracleErrorRow. Hiç I/O yok; poller
// (Aşama 2 devamı) bunu çağırır, testler doğrudan pinler.
//
// Sözleşmeler:
//   - Kolon eşlemesi AYARDAN: DefaultColumns() audit §5'in listesi; kaynak
//     başına `Columns{alan: KOLON}` geçersiz kılar, "" = "bu alan tabloda
//     yok" (alan atlanır). TimestampColumn/TypeColumn (Aşama 1) aynı yerden
//     beslenir — iki ayrı gerçek yok.
//   - Zaman: ERR_TIMESTAMP çoğu kurulumda düz TIMESTAMP (dilimsiz).
//     Sürücü onu bir time.Time'a koyar ama hangi Location'la koyduğu
//     GÜVENİLMEZ; dilimsiz kipte DUVAR SAATİ bileşenleri alınıp kaynağın
//     Timezone'unda (varsayılan Europe/Istanbul) yeniden yorumlanır.
//     TimestampHasZone=true ise sürücünün verdiği an olduğu gibi UTC'ye
//     çevrilir (TIMESTAMP WITH TIME ZONE). Yanlış kip = sabit 3 saat kayma;
//     Settings testinin geniş-pencere ipucu (emptyProbeHint) bunu yakalar.
//   - trace_id yazma anında normalize (audit §5 tuzağı): 32 hex, küçük
//     harf, tire/0x soyulur; geçmeyen değer BOŞ bırakılır, SAYILIR ve ham
//     hâli attribute olarak KALIR (kaybolmaz, yalnız pivotlanmaz).
//   - Tüketilmeyen her kolon attr_keys/attr_values'a verbatim (boş hücreler
//     atlanır — boş attribute bilgi taşımaz). Anahtarlar sıralı → row_id
//     harita sırasından bağımsız.
//   - row_id: FNV-1a 64 (source_id + zaman + tüm alanlar + ekstralar).
//     Timezone değişirse aynı Oracle satırı yeni kimlik alır — bilinçli:
//     eski satırlar TTL ile gider, yeni kip doğru zamanla yazar.
//
// tzdata gömülü: alpine imajında zoneinfo yok; `time/tzdata` olmadan
// LoadLocation("Europe/Istanbul") prod'da düşer, lokalde geçerdi.

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// DefaultTimezone — dilimsiz TIMESTAMP'in yorumlandığı yer (operatör 2026-09-10:
// ayarlanabilir, varsayılan İstanbul).
const DefaultTimezone = "Europe/Istanbul"

// Alan anahtarları — SourceConfig.Columns'ın SOL tarafı (ayar JSON'unda
// görünür, FE eşleme formu bunları listeler).
const (
	FieldTimestamp    = "timestamp"
	FieldSeverity     = "severity"
	FieldMessage      = "message"
	FieldTraceID      = "traceId"
	FieldHost         = "host"
	FieldInstance     = "instance"
	FieldService      = "service"
	FieldCode         = "code"
	FieldExternalCode = "externalCode"
	FieldType         = "type"
	FieldChannel      = "channel"
	FieldTask         = "task"
	FieldRequestID    = "requestId"
	FieldCustomerID   = "customerId"
	FieldTellerID     = "tellerId"
	FieldLocation     = "location"
	// v0.10.902 (özel SQL kipi, custom.go) — ön-toplanmış satır alanları:
	// count = sayaç ağırlığı (Adet; attribute olarak da kalır), traceIds =
	// ayırıcılı trace id LİSTESİ (TRACEIDS) → trace başına satıra patlatılır.
	// Varsayılan kolonları BOŞ (kapalı): tablo kipi satırları değişmez.
	FieldCount    = "count"
	FieldTraceIDs = "traceIds"
)

// fieldOrder — row_id ve doğrulama için SABİT sıra (harita sırası değil).
var fieldOrder = []string{
	FieldTimestamp, FieldSeverity, FieldMessage, FieldTraceID, FieldHost, FieldInstance,
	FieldService, FieldCode, FieldExternalCode, FieldType, FieldChannel, FieldTask,
	FieldRequestID, FieldCustomerID, FieldTellerID, FieldLocation,
	FieldCount, FieldTraceIDs, // v0.10.902 — sona eklendi (row_id sırası korunur)
}

// DefaultColumns — jenerik ERR_* varsayılanları (v0.10.641: kurum tablosunun
// adları ürün varsayılanı olamaz; gerçek eşleme Settings → Oracle → sütunlar).
// Her çağrı yeni harita (çağıran değiştirebilir).
func DefaultColumns() map[string]string {
	return map[string]string{
		FieldTimestamp:    DefaultTimestampColumn,
		FieldSeverity:     "ERR_SEVERITY",
		FieldMessage:      "ERR_MESSAGE",
		FieldTraceID:      "ERR_TRACEID",
		FieldHost:         "ERR_HOSTNAME",
		FieldInstance:     "ERR_INSTANCE_ID",
		FieldService:      "ERR_SERVICE",
		FieldCode:         "ERR_CODE",
		FieldExternalCode: "ERR_EXTERNAL_CODE",
		FieldType:         DefaultTypeColumn,
		FieldChannel:      "ERR_CHANNELCODE",
		FieldTask:         "ERR_TASKCODE",
		FieldRequestID:    "ERR_REQUESTID",
		FieldCustomerID:   "ERR_CUSTOMERID",
		FieldTellerID:     "ERR_TELLERID",
		FieldLocation:     "ERR_LOCATION",
		FieldCount:        "", // v0.10.902 — kapalı; özel SQL kipinde ADET gibi
		FieldTraceIDs:     "", // v0.10.902 — kapalı; özel SQL kipinde TRACEIDS gibi
	}
}

// ResolveColumns — SAF: varsayılan + Aşama 1 kolonları + Columns geçersiz
// kılmaları. Bilinmeyen alan anahtarı ya da identifier olmayan kolon adı
// HATA (ayar kaydında yakalanır, poller'da değil). "" değer alanı KAPATIR.
// timestamp kapatılamaz — zamanı olmayan satır yazılamaz.
func ResolveColumns(src SourceConfig) (map[string]string, error) {
	cols := DefaultColumns()
	if src.TimestampColumn != "" {
		cols[FieldTimestamp] = src.TimestampColumn
	}
	if src.TypeColumn != "" {
		cols[FieldType] = src.TypeColumn
	}
	for field, col := range src.Columns {
		if _, known := cols[field]; !known {
			return nil, fmt.Errorf("columns: bilinmeyen alan %q (geçerli: %s)", field, strings.Join(fieldOrder, ", "))
		}
		cols[field] = strings.TrimSpace(col)
	}
	if cols[FieldTimestamp] == "" {
		return nil, fmt.Errorf("columns: %s kapatılamaz", FieldTimestamp)
	}
	for _, field := range fieldOrder {
		if c := cols[field]; c != "" && !identRe.MatchString(c) {
			return nil, fmt.Errorf("columns.%s: Oracle identifier'ı olmalı: %q", field, c)
		}
	}
	return cols, nil
}

// ResolveLocation — SAF: Timezone boşsa DefaultTimezone; yüklenemeyen ad HATA.
func ResolveLocation(src SourceConfig) (*time.Location, error) {
	name := strings.TrimSpace(src.Timezone)
	if name == "" {
		name = DefaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timezone %q: %v", name, err)
	}
	return loc, nil
}

// MapStats — bir partinin eşleme özeti; durum satırına ve log'a gider.
// NoTimestamp satır DÜŞER (zamansız satır yazılamaz); BadTraceID satır
// KALIR (trace pivotu olmadan).
type MapStats struct {
	Rows        int
	Mapped      int
	NoTimestamp int
	BadTraceID  int
	// Expanded — v0.10.902: trace listesinden patlatılan satır sayısı (Mapped
	// kaynak satırı sayar; çıktı dilimi Mapped − listeli + Expanded uzunlukta).
	Expanded int
}

// Mapper — bir kaynağın çözülmüş eşlemesi. NewMapper ayarı bir kez çözer;
// Map/MapAll saf.
type Mapper struct {
	sourceID string
	loc      *time.Location
	hasZone  bool
	cols     map[string]string // alan → KOLON (boş = kapalı)
	byCol    map[string]string // upper(kolon) → alan
	// aggregated — v0.10.902: count ya da traceIds eşlenmiş (ön-toplanmış
	// satır). Kimlik o zaman DEĞİŞKEN toplamları (ADET, TRACEIDADET, DURATION
	// attribute'ları, Weight) içermez: her poll aynı 15 dk'yı yeniden okur, geç
	// commit'lenen hata Adet'i değiştirince eski kimlikle yeni kimlik iki ayrı
	// CH satırı olurdu (Trace › Logs'ta aynı satır iki kez).
	aggregated bool
}

func NewMapper(src SourceConfig) (*Mapper, error) {
	cols, err := ResolveColumns(src)
	if err != nil {
		return nil, err
	}
	loc, err := ResolveLocation(src)
	if err != nil {
		return nil, err
	}
	m := &Mapper{sourceID: src.ID, loc: loc, hasZone: src.TimestampHasZone, cols: cols, byCol: map[string]string{},
		aggregated: cols[FieldCount] != "" || cols[FieldTraceIDs] != ""}
	for field, col := range cols {
		if col != "" {
			m.byCol[strings.ToUpper(col)] = field
		}
	}
	return m, nil
}

// Map — tek satır. ok=false → satır düşer (zaman yok/çözülemedi).
// badTrace → trace_id geçersizdi, boş yazıldı, ham değer attribute'ta.
// Trace LİSTESİ (v0.10.902) burada patlatılmaz — MapAll yapar.
func (m *Mapper) Map(row map[string]any) (r chstore.OracleErrorRow, ok bool, badTrace bool) {
	r, _, ok, badTrace = m.mapRow(row)
	return r, ok, badTrace
}

// mapRow — Map'in gövdesi; traceList = eşlenmiş `traceIds` kolonunun ayrılmış
// ham parçaları (patlatma MapAll'da, ayrıştırma expandTraceList'te).
func (m *Mapper) mapRow(row map[string]any) (r chstore.OracleErrorRow, traceList []string, ok bool, badTrace bool) {
	// Sürücü kolon adlarını büyük harf döndürür; ayar herhangi bir yazımda
	// olabilir → tek tarafta normalize.
	vals := map[string]any{}
	for k, v := range row {
		vals[strings.ToUpper(strings.TrimSpace(k))] = v
	}
	get := func(field string) (string, bool) {
		col := m.cols[field]
		if col == "" {
			return "", false
		}
		v, present := vals[strings.ToUpper(col)]
		if !present {
			return "", false
		}
		return cellString(v), true
	}

	tsRaw, present := vals[strings.ToUpper(m.cols[FieldTimestamp])]
	if !present {
		return r, nil, false, false
	}
	ts, tok := m.parseTime(tsRaw)
	if !tok {
		return r, nil, false, false
	}

	r.SourceID = m.sourceID
	r.Time = ts
	sevRaw, _ := get(FieldSeverity)
	r.SeverityNum, r.SeverityText = mapSeverity(sevRaw)
	r.Body, _ = get(FieldMessage)
	extras := map[string]string{}
	if traceRaw, has := get(FieldTraceID); has {
		id, valid := normalizeTraceID(traceRaw)
		r.TraceID = id
		if !valid {
			badTrace = true
			extras[m.cols[FieldTraceID]] = strings.TrimSpace(traceRaw)
		}
	}
	r.HostName, _ = get(FieldHost)
	r.InstanceID, _ = get(FieldInstance)
	r.OperationCode, _ = get(FieldService)
	r.ErrorCode, _ = get(FieldCode)
	r.ExternalCode, _ = get(FieldExternalCode)
	r.ErrorType, _ = get(FieldType)
	r.ChannelCode, _ = get(FieldChannel)
	r.TaskCode, _ = get(FieldTask)
	r.RequestID, _ = get(FieldRequestID)
	r.CustomerID, _ = get(FieldCustomerID)
	r.TellerID, _ = get(FieldTellerID)
	r.Location, _ = get(FieldLocation)
	// v0.10.902 — ön-toplanmış satır: count ağırlık (kolon ATTRIBUTE olarak da
	// kalır — Trace › Logs'ta "ADET: 2" görünsün), traceIds ham liste.
	if cRaw, has := get(FieldCount); has {
		r.Weight = cellCount(cRaw)
	}
	if lRaw, has := get(FieldTraceIDs); has {
		traceList = splitTraceList(lRaw)
	}

	for k, v := range vals {
		if field, consumed := m.byCol[k]; consumed && field != FieldCount {
			continue
		}
		if s := cellString(v); s != "" {
			extras[k] = s
		}
	}
	keys := make([]string, 0, len(extras))
	for k := range extras {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	r.AttrKeys = keys
	r.AttrValues = make([]string, len(keys))
	for i, k := range keys {
		r.AttrValues[i] = extras[k]
	}
	r.RowID = m.identity(r)
	return r, traceList, true, badTrace
}

// identity — satır kimliği: tablo kipinde tüm içerik (rowID); ön-toplanmış
// kipte yalnız tipli alanlar + zaman + trace (rowIDTyped).
func (m *Mapper) identity(r chstore.OracleErrorRow) uint64 {
	if m.aggregated {
		return rowIDTyped(r)
	}
	return rowID(r)
}

// MapAll — parti; istatistik KAYNAK satır sayımlarıdır. v0.10.902: trace
// listesi taşıyan satır trace başına satıra patlatılır (expandTraceList).
func (m *Mapper) MapAll(rows []map[string]any) ([]chstore.OracleErrorRow, MapStats) {
	out := make([]chstore.OracleErrorRow, 0, len(rows))
	st := MapStats{Rows: len(rows)}
	for _, row := range rows {
		r, list, ok, bad := m.mapRow(row)
		if !ok {
			st.NoTimestamp++
			continue
		}
		if bad {
			st.BadTraceID++
		}
		st.Mapped++
		if len(list) == 0 {
			out = append(out, r)
			continue
		}
		ex, badN := expandTraceList(r, list, m.cols[FieldTraceIDs] != "" && traceListTruncated(row, m.cols[FieldTraceIDs]))
		st.BadTraceID += badN
		st.Expanded += len(ex)
		out = append(out, ex...)
	}
	return out, st
}

// maxTraceListLen — bir satırdan patlatılan trace tavanı (4000 karakterlik
// XMLAGG listesi ≈ 120 id; tavan sel freni).
const maxTraceListLen = 500

// splitTraceList — SAF: ayırıcılı liste → kırpılmış parçalar (virgül, noktalı
// virgül, boşluk, satır sonu, boru). Boş parça atılır; tavan maxTraceListLen.
func splitTraceList(raw string) []string {
	f := func(c rune) bool {
		return c == ',' || c == ';' || c == '|' || c == ' ' || c == '\t' || c == '\n' || c == '\r'
	}
	parts := strings.FieldsFunc(raw, f)
	if len(parts) > maxTraceListLen {
		parts = parts[:maxTraceListLen]
	}
	return parts
}

// expandTraceList — SAF: ön-toplanmış satır + trace listesi → trace başına
// satır (Weight 1) + artan sayı için trace'siz tek satır (Weight = count −
// geçerli id). Geçersiz id'ler (32 hex'e inmeyen) ayrı satır AÇMAZ, sayıya
// artan olarak kalır ve badN ile döner. Tekrarlanan id tek satır. Count
// eşlenmemişse (0) yalnız trace satırları (her biri 1). row_id her satır için
// yeniden — trace farkı kimliğe girer; artan satır her poll'da aynı kimlik.
func expandTraceList(base chstore.OracleErrorRow, list []string, truncated bool) (out []chstore.OracleErrorRow, badN int) {
	seen := map[string]bool{}
	valid := make([]string, 0, len(list))
	for i, raw := range list {
		id, ok := normalizeTraceID(raw)
		if !ok {
			// v0.10.902 — DBMS_LOB.SUBSTR(…, 4000) listeyi keser; son parça
			// yarım bir id'dir, "geçersiz trace id" değil (sayısı artan satırda).
			if truncated && i == len(list)-1 {
				continue
			}
			badN++
			continue
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		valid = append(valid, id)
	}
	out = make([]chstore.OracleErrorRow, 0, len(valid)+1)
	for _, id := range valid {
		r := base
		r.TraceID = id
		r.Weight = 1
		r.RowID = rowIDTyped(r)
		out = append(out, r)
	}
	if base.Weight > uint32(len(valid)) {
		r := base
		r.TraceID = ""
		r.Weight = base.Weight - uint32(len(valid))
		r.RowID = rowIDTyped(r)
		out = append(out, r)
	} else if len(valid) == 0 {
		// Liste tamamen geçersiz/boş: satır trace'siz, ağırlığı olduğu gibi.
		r := base
		r.TraceID = ""
		r.RowID = rowIDTyped(r)
		out = append(out, r)
	}
	return out, badN
}

// traceListMaxChars — operatör sorgusunun DBMS_LOB.SUBSTR kesim uzunluğu.
const traceListMaxChars = 4000

// traceListTruncated — SAF: ham liste hücresi kesim uzunluğuna ulaştı mı.
func traceListTruncated(row map[string]any, col string) bool {
	for k, v := range row {
		if strings.EqualFold(strings.TrimSpace(k), col) {
			return len(cellString(v)) >= traceListMaxChars
		}
	}
	return false
}

// cellCount — SAF: sayı hücresi → ağırlık (≤ 0 ya da çözülemeyen → 0 = 1).
func cellCount(v any) uint32 {
	var n float64
	switch t := v.(type) {
	case nil:
		return 0
	case float64:
		n = t
	case float32:
		n = float64(t)
	case int64:
		n = float64(t)
	case int:
		n = float64(t)
	case int32:
		n = float64(t)
	case uint64:
		n = float64(t)
	case string:
		p, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0
		}
		n = p
	case []byte:
		return cellCount(string(t))
	default:
		return 0
	}
	if n <= 0 || n != n {
		return 0
	}
	if n > 1e9 {
		n = 1e9
	}
	return uint32(n + 0.5)
}

// timeLayouts — sürücü zamanı time.Time verir; string yalnız yedek
// (test fixture'ları, CLOB'a yazılmış zamanlar). Dilimli düzenler önce.
var timeLayouts = []struct {
	layout string
	zoned  bool
}{
	{time.RFC3339Nano, true},
	{"2006-01-02T15:04:05.999999999", false},
	{"2006-01-02 15:04:05.999999999", false},
	{"2006-01-02 15:04:05", false},
	// v0.10.902 — TO_CHAR(…, 'DD.MM.YYYY HH24:MI[:SS]') (özel SQL "Zaman").
	{"02.01.2006 15:04:05", false},
	{"02.01.2006 15:04", false},
}

// epochToTime — SAF (v0.10.902): sayısal zaman = MUTLAK an (özel SQL'in
// TimeSlice'ı TZ'yi kendisi düzeltir; localize edilmez). Ölçek büyüklükten:
// ≥1e17 ns, ≥1e14 µs, ≥1e11 ms, yoksa saniye. 1e9 s (2001-09) altı sayı epoch
// DEĞİL (12345 gibi çöp bir sayı zamansız düşer — TestMapTimezoneOverride…).
// Sonuç 2001-09-09'dan önce ya da şimdi+48 sa'ten sonraysa da DEĞİL (inceleme): NUMBER(14)
// YYYYMMDDHH24MISS (2.0e13) ms sayılıp 2612 yılına gider ve tablo kipinde
// kalıcı watermark'ı geleceğe iterek poll'u sonsuza dek boşaltırdı.
func epochToTime(n float64) (time.Time, bool) {
	if n < 1e9 || n != n {
		return time.Time{}, false
	}
	var t time.Time
	switch {
	case n >= 1e17:
		t = time.Unix(0, int64(n)).UTC()
	case n >= 1e14:
		t = time.UnixMicro(int64(n)).UTC()
	case n >= 1e11:
		t = time.UnixMilli(int64(n)).UTC()
	default:
		sec := int64(n)
		t = time.Unix(sec, int64((n-float64(sec))*1e9)).UTC()
	}
	// Hata satırı gelecekten gelmez: şimdi + 48 sa üstü epoch değil (10 haneli
	// YYYYMMDDHH24 = 2034 yılı da böyle elenir).
	if t.After(time.Now().Add(48 * time.Hour)) {
		return time.Time{}, false
	}
	return t, true
}

func (m *Mapper) parseTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		if t.IsZero() {
			return time.Time{}, false
		}
		return m.localize(t), true
	case float64:
		return epochToTime(t)
	case float32:
		return epochToTime(float64(t))
	case int64:
		return epochToTime(float64(t))
	case int:
		return epochToTime(float64(t))
	case int32:
		return epochToTime(float64(t))
	case uint64:
		return epochToTime(float64(t))
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}, false
		}
		// Yalnız rakam (NUMBER'ı dize veren sürücü) → epoch; yalnız s/ms/µs/ns
		// uzunlukları (10/13/16/19) — 14 haneli YYYYMMDDHH24MISS epoch değil.
		if isDigits(s) && (len(s) == 10 || len(s) == 13 || len(s) == 16 || len(s) == 19) {
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				return epochToTime(n)
			}
		}
		for _, l := range timeLayouts {
			if l.zoned {
				if p, err := time.Parse(l.layout, s); err == nil {
					return p.UTC(), true
				}
				continue
			}
			if p, err := time.ParseInLocation(l.layout, s, m.loc); err == nil {
				return p.UTC(), true
			}
		}
		return time.Time{}, false
	case []byte:
		return m.parseTime(string(t))
	default:
		return time.Time{}, false
	}
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// localize — dilimsiz kipte duvar saati bileşenleri kaynağın diliminde
// yeniden yorumlanır; dilimli kipte an olduğu gibi UTC'ye.
func (m *Mapper) localize(t time.Time) time.Time {
	if m.hasZone {
		return t.UTC()
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), m.loc).UTC()
}

// cellString — SAF: hücre → dize; KIRPMA YOK (formatCell'in 200'lük kırpması
// yalnız Settings örneği içindir; burada tam fidelity).
func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}

// normalizeTraceID — logstore.normalizeHexID(v, 32) ile aynı sözleşme (o
// paket-içi; kopya bilinçli, iki paket birbirini import etmez). Boş = "yok"
// (geçerli, pivotsuz); dolu ama 32 hex'e inmeyen = GEÇERSİZ.
func normalizeTraceID(v string) (string, bool) {
	s := strings.TrimSpace(v)
	if s == "" {
		return "", true
	}
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return s, true
}

// mapSeverity — OTel log severity (1..24) eşlemesi. Boş → ERROR (bu bir hata
// tablosu; sessiz INFO yanlış olurdu). Sayı 1..24 → olduğu gibi. Bilinmeyen
// metin → numara ERROR, metin HAM (bilgi kaybolmaz).
func mapSeverity(raw string) (uint8, string) {
	u := strings.ToUpper(strings.TrimSpace(raw))
	if u == "" {
		return 17, "ERROR"
	}
	if n, err := strconv.Atoi(u); err == nil && n >= 1 && n <= 24 {
		return uint8(n), severityText(n)
	}
	switch u {
	case "FATAL", "CRITICAL", "F", "C", "SEVERE":
		return 21, "FATAL"
	case "ERROR", "ERR", "E":
		return 17, "ERROR"
	case "WARN", "WARNING", "W":
		return 13, "WARN"
	case "INFO", "INFORMATION", "I":
		return 9, "INFO"
	case "DEBUG", "D", "TRACE", "T":
		return 5, "DEBUG"
	}
	return 17, u
}

func severityText(n int) string {
	switch {
	case n >= 21:
		return "FATAL"
	case n >= 17:
		return "ERROR"
	case n >= 13:
		return "WARN"
	case n >= 9:
		return "INFO"
	case n >= 5:
		return "DEBUG"
	default:
		return "TRACE"
	}
}

// rowID — içerik hash'i; dedup anahtarının üçüncü bileşeni. Alan sırası
// SABİT, ekstralar sıralı → aynı Oracle satırı her tikte aynı kimlik.
// rowIDTyped — v0.10.902: ön-toplanmış satırın kimliği — attribute'lar ve
// Weight HARİÇ (değişken toplamlar); tipli alanlar + zaman + trace.
func rowIDTyped(r chstore.OracleErrorRow) uint64 {
	r.AttrKeys, r.AttrValues = nil, nil
	return rowID(r)
}

func rowID(r chstore.OracleErrorRow) uint64 {
	h := fnv.New64a()
	w := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	w(r.SourceID)
	w(strconv.FormatInt(r.Time.UnixNano(), 10))
	w(r.SeverityText)
	w(r.Body)
	w(r.TraceID)
	w(r.HostName)
	w(r.InstanceID)
	w(r.OperationCode)
	w(r.ErrorCode)
	w(r.ExternalCode)
	w(r.ErrorType)
	w(r.ChannelCode)
	w(r.TaskCode)
	w(r.RequestID)
	w(r.CustomerID)
	w(r.TellerID)
	w(r.Location)
	for i, k := range r.AttrKeys {
		w(k)
		w(r.AttrValues[i])
	}
	return h.Sum64()
}
