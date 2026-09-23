package oracle

// custom.go — v0.10.902 (operatör 2026-09-23: "Oracle tarafındaki sorgu son
// hali bu şekilde, ona göre gerekli güncellemeleri yap"). ÖZEL SQL sorgu kipi.
//
// Operatörün son sorgusu üretilmiş tablo sorgusuna sığmıyor: master log ile
// hata kodu sözlüğünü JOIN'liyor, teknik hataları süzüyor, DAKİKA × kanal ×
// fonksiyon × operasyon × host başına ÖN-TOPLUYOR (Adet), trace id'leri
// XMLAGG ile tek listeye (TRACEIDS, ≤4000 karakter) topluyor, HAVING ile
// tekil hataları eliyor ve penceresini SYSDATE'ten kendisi tanımlıyor (son 15
// tam dakika). Bind yok, FETCH FIRST yok, zaman kolonu yok.
//
// Bu kipte poller sorguyu ÜRETMEZ, operatörün metnini konsolla (v0.10.742)
// aynı salt-okunur sarmalayıcıya alır: tek SELECT/WITH ifadesi (IsSafeConsoleSQL),
// `SELECT * FROM (<q>) FETCH FIRST n ROWS ONLY` (WrapConsoleSQL). Pencere
// sorgunun kendi işidir; Coremetry yalnız SAYAÇ ve ÖZET için "son WindowMin
// dakika" varsayar — operatör bu sayıyı sorgusundaki INTERVAL ile aynı tutar
// (form ipucu). Watermark bind edilmez; her poll aynı pencereyi yeniden okur,
// aynı satır aynı row_id ile RMT'de tek satıra iner, sayaç dakika başına
// aynı sayıyı yeniden yazar (idempotent).
//
// Ön-toplanmış satır eşlemesi mapping.go'da: `count` alanı (Adet) sayaç
// ağırlığı, `traceIds` alanı (TRACEIDS) trace başına satıra PATLATILIR
// (Trace › Logs birleşimi, özne oyları ve kanıt trace listesi çalışsın diye),
// artan sayı trace'siz tek satırda kalır; TimeSlice (epoch sn, sorgu TZ'yi
// kendisi düzeltmiş) ya da Zaman ("DD.MM.YYYY HH24:MI", kaynağın dilimi)
// zaman olarak okunur.
//
// Test/önizleme (TestWith): sözlük kontrolleri (LONG / tam tarama / eşleme
// önekleri) tablo kipine aittir ve burada KOŞMAZ; eşleme kontrolü sorgunun
// döndürdüğü kolon adlarına karşı yapılır (mappingCheckFromColumns).

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Sorgu kipleri. Boş = tablo (eski bloblar değişmez; JSON'da görünmez).
const (
	QueryModeTable  = "table"
	QueryModeCustom = "custom"

	// MaxCustomSQLLen — operatör metni tavanı (XMLAGG'lı sorgu ~1.5 KB).
	MaxCustomSQLLen = 8000
	// WindowMin kelepçeleri: sorgunun kendi geriye bakışı (dakika).
	DefaultWindowMin = 15
	MinWindowMin     = 1
	MaxWindowMin     = 240
)

// normalizeQueryMode — SAF: "custom" → custom; diğer her şey tablo ("").
func normalizeQueryMode(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == QueryModeCustom {
		return QueryModeCustom
	}
	return ""
}

// IsCustom — kaynak özel SQL kipinde mi.
func IsCustom(src SourceConfig) bool { return normalizeQueryMode(src.QueryMode) == QueryModeCustom }

// QueryModeOf — görünen kip adı (boş → "table").
func QueryModeOf(src SourceConfig) string {
	if IsCustom(src) {
		return QueryModeCustom
	}
	return QueryModeTable
}

// validateCustomSQL — SAF: uzunluk + konsol allow-list'i (tek SELECT/WITH,
// FOR UPDATE yok, iç ';' yok). Boş metin burada hata değil (etkin kaynakta
// Normalize ister).
func validateCustomSQL(q, label string) error {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	if len(q) > MaxCustomSQLLen {
		return fmt.Errorf("%s: customSql en çok %d karakter (şu an %d)", label, MaxCustomSQLLen, len(q))
	}
	if !IsSafeConsoleSQL(q) {
		return fmt.Errorf("%s: customSql tek bir SELECT / WITH ifadesi olmalı (FOR UPDATE ve ikinci ifade reddedilir)", label)
	}
	return nil
}

// windowMinOf — SAF: kelepçeli pencere (0 → varsayılan).
func windowMinOf(src SourceConfig) int {
	if src.WindowMin < MinWindowMin || src.WindowMin > MaxWindowMin {
		return DefaultWindowMin
	}
	return src.WindowMin
}

// customWindow — SAF: sayaç/özet penceresi [now − WindowMin, now]. Sorgunun
// kendi penceresiyle aynı olması operatörün işi (form ipucu).
func customWindow(src SourceConfig, now time.Time) (from, to time.Time) {
	return now.Add(-time.Duration(windowMinOf(src)) * time.Minute), now
}

// buildCustomPollQuery — SAF: operatör metni salt-okunur sarmalayıcıda,
// tavan pollRowCap. Bind YOK (dönen args nil).
func buildCustomPollQuery(src SourceConfig, limit int) string {
	if limit < 1 || limit > pollRowCap {
		limit = pollRowCap
	}
	return WrapConsoleSQL(src.CustomSQL, limit)
}

// mappingCheckFromColumns — SAF: eşlenen kolonlar sorgunun ÇIKTI kolonları
// arasında mı (sözlük yok; Oracle takma adları büyük harfe çevirir, iki taraf
// da upper). Yalnız operatörün AÇIKÇA eşlediği alanlar + zaman/tip kutuları
// denetlenir: ERR_* varsayılanları özel sorguda beklenmez, "eksik" listesi
// onlarla dolsaydı gerçek eksik (FUNCTIONCODE gibi) gözden kaçardı. Öneri yok
// (önek mantığı tablo kipine ait).
func mappingCheckFromColumns(cfg SourceConfig, outCols []string) MappingCheck {
	c := MappingCheck{Checked: true, Missing: []MappingMiss{}, Source: "query"}
	cols, err := ResolveColumns(cfg)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	// Tip kutusu boş bırakıldıysa Normalize ERR_TYPE basar — o varsayılan
	// özel sorguda beklenmez, denetlenmez.
	explicit := map[string]bool{FieldTimestamp: true, FieldType: cfg.TypeColumn != "" && !strings.EqualFold(cfg.TypeColumn, DefaultTypeColumn)}
	for f, col := range cfg.Columns {
		if strings.TrimSpace(col) != "" {
			explicit[f] = true
		}
	}
	have := make(map[string]bool, len(outCols))
	for _, name := range outCols {
		if u := strings.ToUpper(strings.TrimSpace(name)); u != "" {
			have[u] = true
		}
	}
	if len(have) == 0 {
		c.Checked = false
		c.Error = "sorgu kolon döndürmedi"
		return c
	}
	for _, f := range fieldOrder {
		col := strings.ToUpper(cols[f])
		if col == "" || !explicit[f] {
			continue
		}
		if have[col] {
			c.Present++
			continue
		}
		c.Missing = append(c.Missing, MappingMiss{Field: f, Column: col, Suggest: suggestFromOutput(f, have)})
	}
	// v0.10.907 (operatör-bildirimli, prod: "0/0 trace, operasyon kodu (boş)"):
	// HİÇ eşlenmemiş kritik alanlar da eksiktir — yalnız açık eşlemeleri
	// denetlemek, operatör eşlemeyi hiç doldurmadığında testi SUSTURUYORDU.
	// Kolon boş ("eşlenmedi"); sorgu çıktısında tanıdık bir takma ad varsa öneri.
	for _, f := range customRequiredFields(explicit) {
		c.Missing = append(c.Missing, MappingMiss{Field: f, Suggest: suggestFromOutput(f, have)})
	}
	// Kanal/host zorunlu değil ama eşlenmemişse ve çıktıda karşılığı varsa
	// öneri listesine girer (buton hepsini tek seferde doldursun).
	for _, f := range []string{FieldChannel, FieldHost, FieldInstance} {
		if sg := suggestFromOutput(f, have); !explicit[f] && sg != "" {
			c.Missing = append(c.Missing, MappingMiss{Field: f, Suggest: sg})
		}
	}
	return c
}

// customRequiredFields — SAF: özel SQL kipinde eşlenmemişse satırların işe
// yaramadığı alanlar. Trace: traceIds (liste) YA DA traceId (tekil) yeter.
func customRequiredFields(explicit map[string]bool) []string {
	var out []string
	if !explicit[FieldTraceIDs] && !explicit[FieldTraceID] {
		out = append(out, FieldTraceIDs)
	}
	for _, f := range []string{FieldService, FieldCode, FieldCount} {
		if !explicit[f] {
			out = append(out, f)
		}
	}
	return out
}

// outputAliases — alan → sorgu çıktısında aranan tanıdık takma adlar (sıra =
// öncelik). Yalnız çıktıda VAR olan ad önerilir; uydurma yok.
var outputAliases = map[string][]string{
	FieldTraceIDs: {"TRACEIDS", "TRACE_IDS", "TRACEID_LIST"},
	FieldTraceID:  {"TRACEID", "TRACE_ID"},
	FieldService:  {"OPERATIONCODE", "OPERATION_CODE", "OPCODE", "SERVICE"},
	FieldCode:     {"ERRORCODE", "ERROR_CODE", "ERRCODE", "FUNCTIONCODE", "FUNCTION_CODE"},
	FieldChannel:  {"KANALKOD", "CHANNELCODE", "CHANNEL_CODE", "CHANNEL"},
	FieldHost:     {"HOSTNAME", "HOST_NAME", "HOST"},
	FieldInstance: {"INSTANCEID", "INSTANCE_ID", "PODNAME", "POD_NAME", "POD"}, // v0.10.908 — pod adından servis
	FieldCount:    {"ADET", "COUNT", "CNT", "ERRORCOUNT", "ERROR_COUNT"},
}

func suggestFromOutput(field string, have map[string]bool) string {
	for _, a := range outputAliases[field] {
		if have[a] {
			return a
		}
	}
	return ""
}

// testCustom — TestWith'in özel kip dalı (db açılmış, şifre çözülmüş).
// Sözlük kontrolleri yok; örnek satırlar + çıktı kolonlarına karşı eşleme
// kontrolü + poller sorgusu önizlemesi + pencere özeti (trace araması dâhil).
func (s *Service) testCustom(ctx context.Context, src SourceConfig, opt TestOptions, db sqlDB, secret string, budget time.Duration, res TestResult) TestResult {
	res.WindowMin = windowMinOf(src)
	res.Query = WrapConsoleSQL(src.CustomSQL, testSampleLimit)

	start := time.Now()
	pctx, pcancel := context.WithTimeout(ctx, budget)
	err := db.PingContext(pctx)
	pcancel()
	if err != nil {
		res.LatencyMs = time.Since(start).Milliseconds()
		res.Error = "bağlantı: " + redactSecrets(err.Error(), secret)
		return res
	}
	// v0.10.902 (inceleme) — TEK koşu: aggregate sorguda FETCH FIRST işi
	// azaltmaz; örnek + özet için iki kez koşmak banka DB'sinde iki tam
	// aggregate demekti. Özet sorgusu (tavan summaryRowCap) örneği de verir.
	res.Query = buildCustomPollQuery(src, summaryRowCap)
	cols, raw, qerr := runRows(ctx, db, res.Query, nil, budget, secret)
	res.LatencyMs = time.Since(start).Milliseconds()
	if qerr != nil {
		res.Error = qerr.Error()
		return res
	}
	res.Columns = cols
	for i, r := range raw {
		if i >= testSampleLimit {
			break
		}
		row := make(map[string]string, len(r))
		for c, v := range r {
			row[c] = formatCell(v)
		}
		res.Sample = append(res.Sample, row)
	}
	res.RowCount = len(res.Sample)
	mc := mappingCheckFromColumns(src, cols)
	res.Mapping = &mc
	if len(raw) == 0 {
		res.Hint = fmt.Sprintf("Özel sorgu satır döndürmedi — pencereyi (SYSDATE aralığı, son %d dk) ve süzgeçleri (HAVING, hata tipi) sorgunun kendisi belirler; "+
			"Coremetry buraya bind eklemez.", res.WindowMin)
	}
	now := time.Now()
	from, to := customWindow(src, now)
	res.PollQuery = buildCustomPollQuery(src, pollRowCap)
	res.Summary = summarizeCustomRows(ctx, src, raw, budget, from, to, opt)
	res.OK = true
	return res
}

// summarizeCustomRows — runWindowSummary'nin özel kip ikizi: testin TEK
// koşusunun satırları (tavan summaryRowCap), eşleme (trace listesi
// patlatılmış), CH trace araması.
func summarizeCustomRows(ctx context.Context, src SourceConfig, raw []map[string]any, budget time.Duration, from, to time.Time, opt TestOptions) *WindowSummary {
	wm := windowMinOf(src)
	m, err := NewMapper(src)
	if err != nil {
		return &WindowSummary{WindowMin: wm, Rows: len(raw), Error: err.Error()}
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
	out := summarizeWindow(wm, rows, st, len(raw) >= summaryRowCap, lookup, done, lerr)
	attachPodServices(ctx, &out, rows, opt, budget)
	out.Expanded = st.Expanded
	return &out
}
