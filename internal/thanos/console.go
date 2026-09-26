package thanos

// console.go — v0.10.951 — PromQL KONSOLU İSTEMCİSİ (Explore → PromQL,
// Thanos). Kaynak: docs/promql-console/audit.md §2 + operatör onaylı
// kararlar 4, 5, 6, 11, 14 (2026-09-26).
//
// ── NEDEN BU PAKETTE ─────────────────────────────────────────────────────
//
// effectiveTokenFor (TokenRef çözümü) paket-içi; konsol /clusters ile AYNI
// cluster kaydını (api katmanı ClusterByRef ile çözer: id ya da ad, yalnız
// etkin), AYNI auth'u (none/bearer + TokenRef) ve AYNI TLS taşımasını
// (thanosClientFor) kullanmalı — ikinci bir kopya zamanla kayar.
//
// ── NEDEN doQueryWith DEĞİL ─────────────────────────────────────────────
//
//   - Paylaşımlı 15 s istemciler serveCached singleflight slotlarını korur
//     (client.go); konsolun zaman aşımı ayardan gelir (5–120 s). Burada
//     istek başına ADANMIŞ http.Client: Timeout ayardan, Transport
//     paylaşımlınınki (bağlantı havuzu + TLS davranışı ortak). Paylaşımlı
//     istemcilere DOKUNULMAZ (TestConsoleLeavesSharedClientsAlone).
//   - 8 MiB sessiz LimitReader + tam Unmarshal + sessiz 1000-seri kesimi
//     yerine: açık response_too_large (gövde tavanı ayardan) ve AKIŞLI seri
//     tavanı — ilk N seri tutulur, kalanı yalnız SAYILIR (truncated +
//     totalSeries; promapi.DecodeSeriesMeta emsali).
//   - promEnvelope yalnız vector/matrix, warnings/infos yok. Burada dört
//     resultType + warnings/infos; seri nesneleri VERBATİM geçer (native
//     histogram "histogram(s)" alanları dahil, değerler dize kalır —
//     "NaN"/"+Inf" yeniden biçimlenmez).
//   - HTTP ≥300 "HTTP n: <gövde>" idi; burada JSON hata gövdesi (errorType,
//     error) çözülür, Prometheus ayrıştırıcı konumu ("satır:sütun")
//     çıkarılır ve paylaşımlı querier'da kullanıcının yazdığı sorguya geri
//     çevrilir (originalPosition).
//   - GET yerine POST form: sorgu 8 KiB'a kadar, URL'de taşınmaz. (Label
//     values ucu Prometheus'ta yalnız GET — orada match[] kısa.)
//
// ── SIZINTI KURALI ───────────────────────────────────────────────────────
//
// İstemciye giden HİÇBİR mesaj ham Go hatası taşımaz: *url.Error
// yapılandırılmış uç nokta URL'sini içerir. Taşıma hatası kaba bir sebebe
// indirgenir ("connection failed", "TLS certificate verification failed");
// tam hata yalnız sunucu loguna gider. Upstream hata/uyarı metinlerinde de
// yapılandırılmış URL ve host maskelenir (scrubEndpoint). ConsoleError
// ham hatayı Unwrap ile de vermez — yalnız context.Canceled /
// DeadlineExceeded nöbetçileri (writeErr'in 499 dalı çalışsın diye).
//
// ── THANOS PARAMETRELERİ (karar 5) ───────────────────────────────────────
//
// partial_response AÇIKÇA gönderilir (ayar, varsayılan false: bankada
// sessiz kısmi veri yerine hata); dedup querier varsayılanında kalır
// (gönderilmez); max_source_resolution=auto yalnız range'de;
// timeout=<ayar>s. Yerel son tarih = ayar + küçük pay (≤1 s): Thanos'un
// kendi yapılandırılmış "timeout" hatası genelde bizimkinden önce gelir ve
// daha açıklayıcıdır.
//
// Hata türleri ve HTTP eşlemesi (karar 11; api katmanı StatusCode ile
// okur): bad_data 400 · execution 422 · timeout 504 · canceled 499 ·
// unavailable 502 · internal 502 · response_too_large 413. guardrail ve
// rate_limited api katmanınındır (bu paket üretmez).

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Konsol hata türleri — ConsoleError.Type. Prometheus errorType adları
// korunur; bilinmeyen upstream türleri HTTP koduna göre bu kümeye iner.
const (
	ConsoleErrBadData          = "bad_data"           // 400 — ayrıştırma / geçersiz parametre
	ConsoleErrExecution        = "execution"          // 422 — değerlendirme hatası
	ConsoleErrTimeout          = "timeout"            // 504
	ConsoleErrCanceled         = "canceled"           // 499 — BİZİM istemcimiz vazgeçti (upstream canceled → unavailable)
	ConsoleErrUnavailable      = "unavailable"        // 502 — erişilemez / 5xx / kimlik reddi
	ConsoleErrInternal         = "internal"           // 502 — upstream iç hata / bozuk yanıt
	ConsoleErrResponseTooLarge = "response_too_large" // 413 — gövde tavanı
)

// Korumalı varsayılanlar ve tavanlar (karar 13 sınırları). Asıl doğrulama
// promql_console ayar katmanında; bunlar sıfır-değerli ya da taşan
// ConsoleLimits için derinlemesine savunma.
const (
	consoleDefaultTimeout   = 30 * time.Second
	consoleMaxTimeout       = 120 * time.Second
	consoleDefaultMaxSeries = 500
	consoleMaxSeriesCeil    = 2000
	consoleDefaultMaxBody   = int64(32 << 20)
	consoleMaxBodyCeil      = int64(128 << 20)
	consoleDefaultMetaLimit = 1000
	consoleMetaLimitCeil    = 10000
	consoleMetaWindow       = time.Hour // metadata start/end verilmezse son 1 saat
	consoleErrBodyMax       = 64 << 10  // hata gövdesi okuma tavanı
)

// consoleNow — metadata varsayılan penceresinin saati (testler sabitler).
var consoleNow = time.Now

// ConsoleLimits — çağrı başına sınırlar; api katmanı promql_console
// ayarından doldurur. Sıfır/negatif alan → korumalı varsayılan (30 s, 500
// seri, 32 MiB); tavanın üstü → tavan (120 s, 2000 seri, 128 MiB).
type ConsoleLimits struct {
	Timeout         time.Duration // adanmış http.Client + bağlam son tarihi + Thanos timeout=
	MaxSeries       int           // query/query_range seri tavanı (akışlı; fazlası sayılır)
	MaxBodyBytes    int64         // yanıt gövdesi tavanı → response_too_large
	PartialResponse bool          // partial_response= (varsayılan false)
}

func (l ConsoleLimits) normalized() ConsoleLimits {
	switch {
	case l.Timeout <= 0:
		l.Timeout = consoleDefaultTimeout
	case l.Timeout > consoleMaxTimeout:
		l.Timeout = consoleMaxTimeout
	}
	switch {
	case l.MaxSeries <= 0:
		l.MaxSeries = consoleDefaultMaxSeries
	case l.MaxSeries > consoleMaxSeriesCeil:
		l.MaxSeries = consoleMaxSeriesCeil
	}
	switch {
	case l.MaxBodyBytes <= 0:
		l.MaxBodyBytes = consoleDefaultMaxBody
	case l.MaxBodyBytes > consoleMaxBodyCeil:
		l.MaxBodyBytes = consoleMaxBodyCeil
	}
	return l
}

// ConsoleInstantQuery — /api/v1/query girdisi.
type ConsoleInstantQuery struct {
	Query string
	Time  time.Time // sıfır → gönderilmez (Thanos "şimdi"yi kullanır)
}

// ConsoleRangeQuery — /api/v1/query_range girdisi. Step ETKİN adımdır:
// auto adım ve 11k-nokta yükseltmesi api katmanında hesaplanır (karar 4).
type ConsoleRangeQuery struct {
	Query      string
	Start, End time.Time
	Step       time.Duration
}

// ConsoleMetaQuery — labels / label values / series girdisi.
type ConsoleMetaQuery struct {
	Match      []string  // match[]; paylaşımlı querier'da her birine matcher, hiç yoksa sentez
	Start, End time.Time // sıfır → End=şimdi, Start=End-1h (metadata HER ZAMAN zaman-sınırlı)
	Limit      int       // sunucu-uygulamalı; ≤0 → 1000, tavan 10000
}

// ConsoleNotes — Prometheus zarfının warnings/infos alanları (URL maskeli).
type ConsoleNotes struct {
	Warnings []string
	Infos    []string
}

// ConsoleResult — query/query_range sonucu. Result, Prometheus'un
// data.result'ının AYNISIDIR (api katmanı zarfa olduğu gibi gömer):
// vector/matrix → tutulan seri nesnelerinin dizisi (verbatim, en fazla
// MaxSeries; boşsa []), scalar/string → [ts, "v"] çifti. Series/TotalSeries
// scalar/string için 0'dır.
type ConsoleResult struct {
	ResultType string // vector | matrix | scalar | string
	Result     json.RawMessage
	ConsoleNotes
	EffectiveQuery string // Thanos'a GERÇEKTEN giden ifade (meta.effectiveQuery)
	Series         int    // tutulan seri sayısı
	TotalSeries    int    // Thanos'un döndürdüğü seri sayısı (≥ Series)
	Truncated      bool   // TotalSeries > Series
}

// ConsoleLabelsResult — labels / label values sonucu.
type ConsoleLabelsResult struct {
	Values []string // en fazla Limit; asla nil
	ConsoleNotes
	EffectiveMatch []string // Thanos'a giden match[] (enjekte/sentez)
	// Total — upstream'in döndürdüğü öğe sayısı. Upstream limit'i
	// uyguluyorsa (limit+1 istenir) bir ALT sınırdır; Truncated her iki
	// durumda da kesindir.
	Total     int
	Truncated bool
}

// ConsoleSeriesResult — series sonucu (etiket setleri).
type ConsoleSeriesResult struct {
	Series []map[string]string // en fazla Limit; asla nil
	ConsoleNotes
	EffectiveMatch []string
	Total          int // bkz. ConsoleLabelsResult.Total
	Truncated      bool
}

// ConsoleError — konsol hatası. Message istemciye gösterilebilir: URL,
// host ya da ham Go hatası İÇERMEZ.
type ConsoleError struct {
	Type    string // ConsoleErr*
	Message string
	// Position — Prometheus ayrıştırıcı konumu "satır:sütun" (1-tabanlı,
	// sütun BAYT), KULLANICININ yazdığı sorguda: paylaşımlı querier'da
	// yorum silme + matcher enjeksiyonu geri çevrilmiştir. Editör alt
	// çizgisi bunu kullanır. Boş = konum yok.
	Position string
	// EffectivePosition — upstream'in bildirdiği ham konum (EffectiveQuery
	// üzerinde; Message'daki konum da budur).
	EffectivePosition string
	// UpstreamStatus — upstream HTTP kodu; 0 = yanıt alınamadı ya da yerel
	// doğrulama.
	UpstreamStatus int
	cause          error // yalnız context nöbetçileri; ham taşıma hatası ASLA
}

func (e *ConsoleError) Error() string { return "thanos console " + e.Type + ": " + e.Message }

// Unwrap — errors.Is(err, context.Canceled) → api writeErr 499 dalı.
func (e *ConsoleError) Unwrap() error { return e.cause }

// StatusCode — karar 11 eşlemesi (api katmanı yanıt kodu).
func (e *ConsoleError) StatusCode() int {
	switch e.Type {
	case ConsoleErrBadData:
		return http.StatusBadRequest
	case ConsoleErrExecution:
		return http.StatusUnprocessableEntity
	case ConsoleErrTimeout:
		return http.StatusGatewayTimeout
	case ConsoleErrCanceled:
		return 499
	case ConsoleErrResponseTooLarge:
		return http.StatusRequestEntityTooLarge
	default: // unavailable, internal
		return http.StatusBadGateway
	}
}

func consoleBadData(msg string) *ConsoleError {
	return &ConsoleError{Type: ConsoleErrBadData, Message: msg}
}

// ── Genel API ────────────────────────────────────────────────────────────

// ConsoleQuery — anlık sorgu (POST /api/v1/query). c, api katmanında
// ClusterByRef ile çözülmüş ETKİN kayıttır.
func (s *Service) ConsoleQuery(ctx context.Context, c ClusterConfig, q ConsoleInstantQuery, lim ConsoleLimits) (*ConsoleResult, error) {
	if strings.TrimSpace(q.Query) == "" {
		return nil, consoleBadData("query is empty")
	}
	eff := c.EffectiveQuery(q.Query)
	form := url.Values{"query": {eff}}
	if !q.Time.IsZero() {
		form.Set("time", formatPromTime(q.Time))
	}
	return s.consoleEval(ctx, c, "/api/v1/query", form, lim.normalized(), q.Query, eff)
}

// ConsoleQueryRange — aralık sorgusu (POST /api/v1/query_range,
// max_source_resolution=auto).
func (s *Service) ConsoleQueryRange(ctx context.Context, c ClusterConfig, q ConsoleRangeQuery, lim ConsoleLimits) (*ConsoleResult, error) {
	switch {
	case strings.TrimSpace(q.Query) == "":
		return nil, consoleBadData("query is empty")
	case q.Start.IsZero() || q.End.IsZero():
		return nil, consoleBadData("start and end are required")
	case q.End.Before(q.Start):
		return nil, consoleBadData("end must not be before start")
	case q.Step <= 0:
		return nil, consoleBadData("step must be positive")
	}
	eff := c.EffectiveQuery(q.Query)
	form := url.Values{
		"query":                 {eff},
		"start":                 {formatPromTime(q.Start)},
		"end":                   {formatPromTime(q.End)},
		"step":                  {formatPromStep(q.Step)},
		"max_source_resolution": {"auto"},
	}
	return s.consoleEval(ctx, c, "/api/v1/query_range", form, lim.normalized(), q.Query, eff)
}

// ConsoleLabels — etiket adları (POST /api/v1/labels).
func (s *Service) ConsoleLabels(ctx context.Context, c ClusterConfig, q ConsoleMetaQuery, lim ConsoleLimits) (*ConsoleLabelsResult, error) {
	lim = lim.normalized()
	form, match, limit, err := consoleMetaForm(c, q, lim)
	if err != nil {
		return nil, err
	}
	var vals []string
	var total int
	notes, err := s.consoleCall(ctx, c, http.MethodPost, "/api/v1/labels", form, lim, func(dec *json.Decoder) (derr error) {
		vals, total, derr = decodeStringList(dec, limit)
		return derr
	})
	if err != nil {
		return nil, err
	}
	return newLabelsResult(vals, total, notes, match), nil
}

// ConsoleLabelValues — bir etiketin değerleri (GET /api/v1/label/<ad>/values;
// Prometheus bu uçta POST kabul etmez). UTF-8 ad Prometheus 3 "U__"
// kaçışıyla yola yazılır.
func (s *Service) ConsoleLabelValues(ctx context.Context, c ClusterConfig, name string, q ConsoleMetaQuery, lim ConsoleLimits) (*ConsoleLabelsResult, error) {
	if name == "" || !utf8.ValidString(name) {
		return nil, consoleBadData("label name is empty or not valid UTF-8")
	}
	lim = lim.normalized()
	form, match, limit, err := consoleMetaForm(c, q, lim)
	if err != nil {
		return nil, err
	}
	path := "/api/v1/label/" + url.PathEscape(labelNamePathSegment(name)) + "/values"
	var vals []string
	var total int
	notes, err := s.consoleCall(ctx, c, http.MethodGet, path, form, lim, func(dec *json.Decoder) (derr error) {
		vals, total, derr = decodeStringList(dec, limit)
		return derr
	})
	if err != nil {
		return nil, err
	}
	return newLabelsResult(vals, total, notes, match), nil
}

// ConsoleSeries — seri etiket setleri (POST /api/v1/series). En az bir
// seçici gerekir; paylaşımlı querier'da sentez bunu her zaman sağlar.
func (s *Service) ConsoleSeries(ctx context.Context, c ClusterConfig, q ConsoleMetaQuery, lim ConsoleLimits) (*ConsoleSeriesResult, error) {
	lim = lim.normalized()
	form, match, limit, err := consoleMetaForm(c, q, lim)
	if err != nil {
		return nil, err
	}
	if len(match) == 0 {
		return nil, consoleBadData("at least one match[] selector is required")
	}
	var sets []map[string]string
	var total int
	notes, err := s.consoleCall(ctx, c, http.MethodPost, "/api/v1/series", form, lim, func(dec *json.Decoder) (derr error) {
		sets, total, derr = decodeLabelSets(dec, limit)
		return derr
	})
	if err != nil {
		return nil, err
	}
	if sets == nil {
		sets = []map[string]string{}
	}
	return &ConsoleSeriesResult{Series: sets, ConsoleNotes: notes, EffectiveMatch: match,
		Total: total, Truncated: total > len(sets)}, nil
}

func newLabelsResult(vals []string, total int, notes ConsoleNotes, match []string) *ConsoleLabelsResult {
	if vals == nil {
		vals = []string{}
	}
	return &ConsoleLabelsResult{Values: vals, ConsoleNotes: notes, EffectiveMatch: match,
		Total: total, Truncated: total > len(vals)}
}

// consoleMetaForm — metadata ortak parametreleri: zaman sınırı (verilmezse
// son 1 saat), match[] enjeksiyonu/sentezi, limit (upstream'e limit+1:
// upstream limiti uygulasa bile kesilme KESİN bilinir).
func consoleMetaForm(c ClusterConfig, q ConsoleMetaQuery, lim ConsoleLimits) (url.Values, []string, int, error) {
	end := q.End
	if end.IsZero() {
		end = consoleNow()
	}
	start := q.Start
	if start.IsZero() {
		start = end.Add(-consoleMetaWindow)
	}
	if end.Before(start) {
		return nil, nil, 0, consoleBadData("end must not be before start")
	}
	limit := q.Limit
	switch {
	case limit <= 0:
		limit = consoleDefaultMetaLimit
	case limit > consoleMetaLimitCeil:
		limit = consoleMetaLimitCeil
	}
	match := c.EffectiveMatchers(q.Match)
	form := url.Values{
		"start":            {formatPromTime(start)},
		"end":              {formatPromTime(end)},
		"limit":            {strconv.Itoa(limit + 1)},
		"partial_response": {strconv.FormatBool(lim.PartialResponse)},
	}
	for _, m := range match {
		form.Add("match[]", m)
	}
	return form, match, limit, nil
}

// consoleEval — query/query_range ortak yolu: karar-5 parametreleri, akışlı
// sonuç çözümü, konum geri çevirisi.
func (s *Service) consoleEval(ctx context.Context, c ClusterConfig, path string, form url.Values, lim ConsoleLimits, orig, eff string) (*ConsoleResult, error) {
	form.Set("partial_response", strconv.FormatBool(lim.PartialResponse))
	form.Set("timeout", formatPromTimeout(lim.Timeout))
	var d queryData
	notes, err := s.consoleCall(ctx, c, http.MethodPost, path, form, lim, func(dec *json.Decoder) error {
		return decodeQueryData(dec, lim.MaxSeries, &d)
	})
	if err != nil {
		var ce *ConsoleError
		if errors.As(err, &ce) && ce.EffectivePosition != "" {
			ce.Position = consoleOriginalPosition(c, orig, ce.EffectivePosition)
		}
		return nil, err
	}
	if msg := d.check(); msg != "" {
		log.Printf("[thanos] console %s: %s", c.Name, msg)
		return nil, &ConsoleError{Type: ConsoleErrInternal, Message: "thanos returned an unexpected result: " + msg, UpstreamStatus: http.StatusOK}
	}
	return &ConsoleResult{
		ResultType: d.resultType, Result: d.result, ConsoleNotes: notes, EffectiveQuery: eff,
		Series: d.kept, TotalSeries: d.total, Truncated: d.total > d.kept,
	}, nil
}

// consoleOriginalPosition — etkin konum → kullanıcının sorgusundaki konum.
func consoleOriginalPosition(c ClusterConfig, orig, effPos string) string {
	label, value := c.EffectiveThanosLabel()
	if label == "" {
		return effPos
	}
	l, col, ok := parseLineCol(effPos)
	if !ok {
		return effPos
	}
	ol, oc, ok := originalPosition(orig, label, value, l, col)
	if !ok {
		return effPos
	}
	return strconv.Itoa(ol) + ":" + strconv.Itoa(oc)
}

// ── HTTP turu ────────────────────────────────────────────────────────────

// consoleHTTPClient — konsola ADANMIŞ istemci: Timeout ayardan (+pay).
// Transport paylaşımlı istemcininki (doğrulayan: DefaultTransport;
// skip-verify: tembel kurulan güvensiz ikiz) → TLS davranışı /clusters ile
// BİREBİR, bağlantı havuzu ortak. http.Client değeri ucuzdur (havuz
// Transport'ta); paylaşımlı 15 s istemcilerin KENDİSİNE dokunulmaz.
//
// Yönlendirme: metodu KORUYAN yönlendirme (307/308, GET'in her türü)
// paylaşımlı istemci gibi izlenir; POST'u gövdesiz GET'e çeviren 301/302/
// 303 izlenmez — aksi hâlde sorgu sessizce düşer ve kafa karıştırıcı bir
// "bad_data" döner (ya da oauth giriş sayfasına gidilir). O yanıt 3xx
// olarak kalır → "thanos redirected the request — check the cluster URL".
func consoleHTTPClient(skipVerify bool, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     thanosClientFor(skipVerify).Transport,
		Timeout:       timeout,
		CheckRedirect: consoleCheckRedirect,
	}
}

func consoleCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.Method != via[0].Method {
		return http.ErrUseLastResponse
	}
	return nil
}

// consoleGrace — yerel son tarihin Thanos timeout'u üstündeki payı
// (timeout/10, en çok 1 s): upstream'in kendi timeout hatası önce gelsin.
func consoleGrace(t time.Duration) time.Duration {
	if g := t / 10; g < time.Second {
		return g
	}
	return time.Second
}

// consoleCall — tek HTTP turu: istek kur, gönder, durum kodunu sınıfla,
// 2xx gövdesini AKIŞLA çöz ("data" alanı çağıranın çözücüsüne). Dönen hata
// her zaman *ConsoleError.
func (s *Service) consoleCall(ctx context.Context, c ClusterConfig, method, path string, form url.Values, lim ConsoleLimits, data func(*json.Decoder) error) (ConsoleNotes, error) {
	if !c.Enabled || strings.TrimSpace(c.URL) == "" {
		return ConsoleNotes{}, consoleBadData(fmt.Sprintf("cluster %q is not enabled", c.Name))
	}
	budget := lim.Timeout + consoleGrace(lim.Timeout)
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	endpoint := strings.TrimRight(c.URL, "/") + path
	var body io.Reader
	if method == http.MethodGet {
		endpoint += "?" + form.Encode()
	} else {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(cctx, method, endpoint, body)
	if err != nil {
		log.Printf("[thanos] console %s: istek kurulamadı: %v", c.Name, err)
		return ConsoleNotes{}, &ConsoleError{Type: ConsoleErrUnavailable, Message: fmt.Sprintf("thanos cluster %q has an invalid URL", c.Name)}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	if token := s.effectiveTokenFor(c); c.AuthType == "bearer" && token != "" { // v0.10.272 — ref > saklı
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := consoleHTTPClient(c.InsecureSkipVerify, budget).Do(req)
	if err != nil {
		if ce := consoleCtxError(ctx, cctx, lim, err); ce != nil {
			return ConsoleNotes{}, ce
		}
		log.Printf("[thanos] console %s: %v", c.Name, err) // tam hata YALNIZ logda
		return ConsoleNotes{}, &ConsoleError{Type: ConsoleErrUnavailable,
			Message: fmt.Sprintf("thanos cluster %q is unreachable (%s)", c.Name, transportReason(err))}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ConsoleNotes{}, consoleStatusError(ctx, cctx, c, lim, resp)
	}
	if resp.ContentLength > lim.MaxBodyBytes {
		return ConsoleNotes{}, consoleTooLarge(lim)
	}
	cr := &cappedReader{r: resp.Body, left: lim.MaxBodyBytes}
	head, err := decodeConsoleEnvelope(json.NewDecoder(cr), data)
	if err != nil {
		if cr.over || errors.Is(err, errConsoleBodyCap) {
			return ConsoleNotes{}, consoleTooLarge(lim)
		}
		if ce := consoleCtxError(ctx, cctx, lim, err); ce != nil {
			return ConsoleNotes{}, ce
		}
		log.Printf("[thanos] console %s: yanıt çözülemedi: %v", c.Name, err)
		return ConsoleNotes{}, &ConsoleError{Type: ConsoleErrInternal, Message: "thanos returned a malformed response", UpstreamStatus: resp.StatusCode}
	}
	// Bağlantı yeniden kullanılsın: zarftan sonraki küçük kuyruğu boşalt.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if head.Status != "success" {
		// v0.10.951 — başlıklar geldikten sonra istemci gittiyse gövdesiz
		// 499 korunur (consoleStatusError'daki kuralın aynası).
		if errors.Is(ctx.Err(), context.Canceled) {
			return ConsoleNotes{}, consoleCanceled()
		}
		return ConsoleNotes{}, consoleUpstreamError(c, resp.StatusCode, head.ErrorType, head.Error)
	}
	return ConsoleNotes{Warnings: scrubAll(c, head.Warnings), Infos: scrubAll(c, head.Infos)}, nil
}

// consoleCanceled — BİZİM iptalimiz (üst bağlam context.Canceled). v0.10.951
// — ConsoleErrCanceled YALNIZ buradan doğar ve her zaman
// cause=context.Canceled taşır; upstream'in "canceled" cevabı bu türe
// ÇEVRİLMEZ (normalizeConsoleErrorType).
func consoleCanceled() *ConsoleError {
	return &ConsoleError{Type: ConsoleErrCanceled, Message: "request canceled", cause: context.Canceled}
}

// consoleCtxError — iptal / son tarih sınıflaması; nil = bağlam kaynaklı
// değil. Üst bağlamın iptali (istemci gitti) → canceled; üst ya da yerel
// son tarih ya da ağ zaman aşımı → timeout.
func consoleCtxError(parent, cctx context.Context, lim ConsoleLimits, err error) *ConsoleError {
	switch {
	case errors.Is(parent.Err(), context.Canceled):
		return consoleCanceled()
	case parent.Err() != nil || errors.Is(cctx.Err(), context.DeadlineExceeded) || isTimeoutErr(err):
		return &ConsoleError{Type: ConsoleErrTimeout,
			Message: fmt.Sprintf("query exceeded the %s console timeout", lim.Timeout), cause: context.DeadlineExceeded}
	}
	return nil
}

func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// transportReason — ham taşıma hatası → host/URL taşımayan kaba sebep.
func transportReason(err error) string {
	var (
		certErr *tls.CertificateVerificationError
		uaErr   x509.UnknownAuthorityError
		hostErr x509.HostnameError
		invErr  x509.CertificateInvalidError
		recErr  tls.RecordHeaderError
		dnsErr  *net.DNSError
		opErr   *net.OpError
	)
	switch {
	case errors.As(err, &certErr), errors.As(err, &uaErr), errors.As(err, &hostErr), errors.As(err, &invErr):
		return "TLS certificate verification failed"
	case errors.As(err, &recErr):
		return "TLS handshake failed"
	case errors.As(err, &dnsErr):
		return "DNS lookup failed"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "connection failed"
	}
	return "request failed"
}

// consoleStatusError — 2xx dışı yanıt: JSON hata gövdesi (errorType,
// error) çözülür; JSON değilse gövde YANKILANMAZ (oauth-proxy / ingress
// HTML'i host taşıyabilir) — yalnız logda, mesaj HTTP koduna göre.
//
// v0.10.951 — başlıklar geldikten hemen sonra BİZİM bağlamımız iptal
// edildiyse (istemci gitti) upstream gövdesi değil iptal döner: gerçekten
// giden istemci gövdesiz 499'unu korur.
func consoleStatusError(parent, cctx context.Context, c ClusterConfig, lim ConsoleLimits, resp *http.Response) *ConsoleError {
	if errors.Is(parent.Err(), context.Canceled) {
		return consoleCanceled()
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, consoleErrBodyMax))
	if err != nil {
		if ce := consoleCtxError(parent, cctx, lim, err); ce != nil {
			return ce
		}
	}
	var e struct {
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && (e.ErrorType != "" || e.Error != "") {
		return consoleUpstreamError(c, resp.StatusCode, e.ErrorType, e.Error)
	}
	log.Printf("[thanos] console %s: HTTP %d, JSON olmayan gövde: %s", c.Name, resp.StatusCode,
		firstN(strings.TrimSpace(string(raw)), 200))
	return consoleUpstreamError(c, resp.StatusCode, "", "")
}

// consoleUpstreamError — upstream (errorType, error, HTTP kodu) → ConsoleError.
func consoleUpstreamError(c ClusterConfig, code int, errorType, msg string) *ConsoleError {
	if msg == "" {
		msg = consoleStatusMessage(code)
	}
	msg = scrubEndpoint(c, msg)
	ce := &ConsoleError{Type: normalizeConsoleErrorType(errorType, code), Message: msg, UpstreamStatus: code}
	if p := parsePromPosition(msg); p != "" {
		ce.Position, ce.EffectivePosition = p, p
	}
	return ce
}

// normalizeConsoleErrorType — bilinen Prometheus türü aynen; bilinmeyen /
// boş (not_found, not_acceptable…) HTTP koduna göre.
//
// v0.10.951 — upstream "canceled" (errorType ya da çıplak HTTP 499) →
// unavailable (502), canceled DEĞİL: bir upstream yanıtı OKUYABİLDİYSEK
// bizim istemcimiz vazgeçmemiştir (kendi iptalimizi consoleCtxError /
// consoleStatusError daha önce yakalar). canceled'a çevirmek canlı istemciye
// gövdesiz 499 yazdırır ve koşuyu geçmişten düşürürdü. Kural kod
// switch'inden ÖNCE: canceled taşıyan bir 400/422 bad_data/execution'a
// düşmesin.
func normalizeConsoleErrorType(t string, code int) string {
	switch t {
	case ConsoleErrCanceled:
		return ConsoleErrUnavailable
	case ConsoleErrBadData, ConsoleErrExecution, ConsoleErrTimeout, ConsoleErrUnavailable, ConsoleErrInternal:
		return t
	}
	switch code {
	case http.StatusBadRequest:
		return ConsoleErrBadData
	case http.StatusUnprocessableEntity:
		return ConsoleErrExecution
	case 499:
		return ConsoleErrUnavailable
	case http.StatusGatewayTimeout:
		return ConsoleErrTimeout
	}
	return ConsoleErrUnavailable
}

// consoleStatusMessage — gövdesiz/JSON'suz yanıt için güvenli mesaj.
func consoleStatusMessage(code int) string {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Sprintf("thanos rejected the cluster credentials (HTTP %d)", code)
	case code == http.StatusNotFound:
		return "thanos query endpoint not found (HTTP 404) — check the cluster URL"
	case code == http.StatusTooManyRequests:
		return "thanos is rate limiting requests (HTTP 429)"
	case code == http.StatusGatewayTimeout:
		return "thanos gateway timed out (HTTP 504)"
	case code == 499:
		return "thanos canceled the query (HTTP 499)"
	case code >= 500:
		return fmt.Sprintf("thanos is unavailable (HTTP %d)", code)
	case code >= 300 && code < 400:
		return fmt.Sprintf("thanos redirected the request (HTTP %d) — check the cluster URL", code)
	}
	return fmt.Sprintf("thanos returned HTTP %d", code)
}

func consoleTooLarge(lim ConsoleLimits) *ConsoleError {
	return &ConsoleError{Type: ConsoleErrResponseTooLarge, Message: fmt.Sprintf(
		"response exceeds the %s console limit — use a larger step or aggregate the query (e.g. sum by, topk) to shrink the result",
		humanBytes(lim.MaxBodyBytes))}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + " MiB"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + " KiB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

// scrubEndpoint — upstream metnindeki yapılandırılmış URL / host maskelenir
// (derinlemesine savunma: Thanos kendi adresini nadiren yazar ama yazarsa
// istemciye gitmesin). Noktasız/portsuz tek kelimelik host maskelenmez —
// sıradan kelimeleri ("thanos") bozmasın; tam URL yine maskelenir.
func scrubEndpoint(c ClusterConfig, msg string) string {
	if u := strings.TrimRight(strings.TrimSpace(c.URL), "/"); u != "" {
		msg = strings.ReplaceAll(msg, u, "<thanos>")
	}
	if pu, err := url.Parse(strings.TrimSpace(c.URL)); err == nil && pu.Host != "" {
		if strings.ContainsAny(pu.Host, ".:") {
			msg = strings.ReplaceAll(msg, pu.Host, "<thanos>")
		}
		if h := pu.Hostname(); h != pu.Host && strings.Contains(h, ".") {
			msg = strings.ReplaceAll(msg, h, "<thanos>")
		}
	}
	return msg
}

func scrubAll(c ClusterConfig, in []string) []string {
	for i := range in {
		in[i] = scrubEndpoint(c, in[i])
	}
	return in
}

// Prometheus ayrıştırıcı konumu: ≥2.x "1:14: parse error: …" (ParseErr);
// eski "parse error at char 7:" / "parse error at line 2, char 7:".
var (
	promPosRe  = regexp.MustCompile(`(?:^|[^0-9.:])(\d+):(\d+): parse error`)
	promCharRe = regexp.MustCompile(`parse error at (?:line (\d+), )?char (\d+)`)
)

// parsePromPosition — mesajdaki İLK konum "satır:sütun"; yoksa "".
func parsePromPosition(msg string) string {
	if m := promPosRe.FindStringSubmatch(msg); m != nil {
		return m[1] + ":" + m[2]
	}
	if m := promCharRe.FindStringSubmatch(msg); m != nil {
		line := m[1]
		if line == "" {
			line = "1"
		}
		return line + ":" + m[2]
	}
	return ""
}

// ── Akışlı zarf çözümü ──────────────────────────────────────────────────

var (
	errConsoleBodyCap   = errors.New("thanos console: response body cap exceeded")
	errConsoleMalformed = errors.New("thanos console: malformed prometheus envelope")
)

// cappedReader — gövde tavanı: tavana kadar okur, tavandan SONRA tek bayt
// daha gelirse errConsoleBodyCap (tam tavan boyutundaki gövde geçer).
type cappedReader struct {
	r    io.Reader
	left int64
	over bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var one [1]byte
		n, err := c.r.Read(one[:])
		if n > 0 {
			c.over = true
			return 0, errConsoleBodyCap
		}
		return 0, err
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

type consoleHead struct {
	Status, ErrorType, Error string
	Warnings, Infos          []string
}

// decodeConsoleEnvelope — Prometheus zarfı: alan sırası serbest; "data"
// çağıranın çözücüsüne, warnings/infos toplanır, bilinmeyen alanlar
// (stats…) atlanır.
func decodeConsoleEnvelope(dec *json.Decoder, data func(*json.Decoder) error) (consoleHead, error) {
	var h consoleHead
	if err := expectDelim(dec, '{'); err != nil {
		return h, err
	}
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return h, err
		}
		switch key {
		case "status":
			err = dec.Decode(&h.Status)
		case "errorType":
			err = dec.Decode(&h.ErrorType)
		case "error":
			err = dec.Decode(&h.Error)
		case "warnings":
			h.Warnings, err = decodeNotes(dec)
		case "infos":
			h.Infos, err = decodeNotes(dec)
		case "data":
			err = data(dec)
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return h, err
		}
	}
	return h, expectDelim(dec, '}')
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return errConsoleMalformed
	}
	return nil
}

func readKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	k, ok := tok.(string)
	if !ok {
		return "", errConsoleMalformed
	}
	return k, nil
}

func skipValue(dec *json.Decoder) error {
	var skip json.RawMessage
	return dec.Decode(&skip)
}

// decodeNotes — warnings/infos: dize dizisi; dize olmayan öğe ham JSON
// metni olarak korunur (kaybolmasın).
func decodeNotes(dec *json.Decoder) ([]string, error) {
	var raw []json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			out = append(out, s)
		} else {
			out = append(out, string(r))
		}
	}
	return out, nil
}

type resultShape int

const (
	shapeNone   resultShape = iota // null ya da []
	shapeSeries                    // [{…}, …] — vector / matrix
	shapePair                      // [ts, "v"] — scalar / string
)

// queryData — query/query_range "data" alanı.
type queryData struct {
	resultType  string
	result      json.RawMessage
	shape       resultShape
	kept, total int
}

// decodeQueryData — data nesnesi; alan sırası serbest (result resultType'tan
// önce gelebilir — şekil ilk öğeden anlaşılır, check doğrular).
func decodeQueryData(dec *json.Decoder, maxSeries int, d *queryData) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if dl, ok := tok.(json.Delim); !ok || dl != '{' {
		return errConsoleMalformed
	}
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return err
		}
		switch key {
		case "resultType":
			err = dec.Decode(&d.resultType)
		case "result":
			err = decodeResultArray(dec, maxSeries, d)
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return err
		}
	}
	return expectDelim(dec, '}')
}

// decodeResultArray — AKIŞLI seri tavanı: her öğe tek tampona (yeniden
// kullanılan RawMessage) okunur; ilk maxSeries seri çıktıya kopyalanır,
// kalanı yalnız sayılır. scalar/string çifti ayrı toplanır.
func decodeResultArray(dec *json.Decoder, maxSeries int, d *queryData) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if dl, ok := tok.(json.Delim); !ok || dl != '[' {
		return errConsoleMalformed
	}
	var out bytes.Buffer
	out.WriteByte('[')
	var elem json.RawMessage
	var pair [][]byte
	for dec.More() {
		elem = elem[:0]
		if err := dec.Decode(&elem); err != nil {
			return err
		}
		isObj := len(elem) > 0 && elem[0] == '{'
		switch {
		case d.shape == shapeNone && isObj:
			d.shape = shapeSeries
		case d.shape == shapeNone:
			d.shape = shapePair
		case (d.shape == shapeSeries) != isObj:
			return errConsoleMalformed
		}
		if d.shape == shapeSeries {
			d.total++
			if d.kept < maxSeries {
				if d.kept > 0 {
					out.WriteByte(',')
				}
				out.Write(elem)
				d.kept++
			}
			continue
		}
		if len(pair) == 2 {
			return errConsoleMalformed
		}
		pair = append(pair, append([]byte(nil), elem...))
	}
	if err := expectDelim(dec, ']'); err != nil {
		return err
	}
	if d.shape == shapePair {
		if len(pair) != 2 {
			return errConsoleMalformed
		}
		d.result = json.RawMessage("[" + string(pair[0]) + "," + string(pair[1]) + "]")
		return nil
	}
	out.WriteByte(']')
	d.result = out.Bytes()
	return nil
}

// check — resultType ile sonuç şekli tutarlı mı; "" = tamam. vector/matrix
// null sonucu [] olur.
func (d *queryData) check() string {
	switch d.resultType {
	case "vector", "matrix":
		if d.shape == shapePair {
			return fmt.Sprintf("%s result is not a series list", d.resultType)
		}
		if d.result == nil {
			d.result = json.RawMessage("[]")
		}
	case "scalar", "string":
		if d.shape != shapePair {
			return fmt.Sprintf("%s result is not a [time, value] pair", d.resultType)
		}
	case "":
		return "resultType missing"
	default:
		return fmt.Sprintf("unknown resultType %q", d.resultType)
	}
	return ""
}

// decodeStringList — labels / label values: ilk limit dize tutulur, kalanı
// sayılır.
func decodeStringList(dec *json.Decoder, limit int) ([]string, int, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, 0, err
	}
	out := []string{}
	if tok == nil {
		return out, 0, nil
	}
	if dl, ok := tok.(json.Delim); !ok || dl != '[' {
		return nil, 0, errConsoleMalformed
	}
	total := 0
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, 0, err
		}
		v, ok := tok.(string)
		if !ok {
			return nil, 0, errConsoleMalformed
		}
		total++
		if len(out) < limit {
			out = append(out, v)
		}
	}
	return out, total, expectDelim(dec, ']')
}

// decodeLabelSets — series: ilk limit etiket seti çözülür, kalanı tek
// tampona okunup sayılır (map ayırmadan).
func decodeLabelSets(dec *json.Decoder, limit int) ([]map[string]string, int, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, 0, err
	}
	out := []map[string]string{}
	if tok == nil {
		return out, 0, nil
	}
	if dl, ok := tok.(json.Delim); !ok || dl != '[' {
		return nil, 0, errConsoleMalformed
	}
	total := 0
	var skip json.RawMessage
	for dec.More() {
		total++
		if len(out) < limit {
			var m map[string]string
			if err := dec.Decode(&m); err != nil {
				return nil, 0, err
			}
			if m == nil {
				return nil, 0, errConsoleMalformed
			}
			out = append(out, m)
			continue
		}
		skip = skip[:0]
		if err := dec.Decode(&skip); err != nil {
			return nil, 0, err
		}
	}
	return out, total, expectDelim(dec, ']')
}

// ── Biçimleme ────────────────────────────────────────────────────────────

// formatPromTime — unix saniye, milisaniye hassasiyetli (Prometheus float
// saniye kabul eder).
func formatPromTime(t time.Time) string {
	ms := t.UnixMilli()
	if ms%1000 == 0 {
		return strconv.FormatInt(ms/1000, 10)
	}
	return strconv.FormatFloat(float64(ms)/1e3, 'f', 3, 64)
}

// formatPromStep — adım saniye cinsinden ("15", "0.5").
func formatPromStep(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

// formatPromTimeout — Thanos timeout= parametresi ("30s"; saniye altı "ms").
func formatPromTimeout(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	ms := d.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return strconv.FormatInt(ms, 10) + "ms"
}

// labelNamePathSegment — /api/v1/label/<ad>/values yol parçası. Eski
// sözdizimli ad (Prometheus IsValidLegacyMetricName: [a-zA-Z_:][a-zA-Z0-9_:]*)
// aynen; UTF-8 ad Prometheus 3 değer kaçışıyla (model.EscapeName,
// ValueEncodingEscaping): "U__" öneki, '_' → "__", [a-zA-Z0-9:] aynen
// (rakam ilk konumda değil), diğer her rune → "_<hex>_".
func labelNamePathSegment(name string) string {
	legacy := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')) {
			legacy = false
			break
		}
	}
	if legacy {
		return name
	}
	var b strings.Builder
	b.WriteString("U__")
	for i, r := range name {
		switch {
		case r == '_':
			b.WriteString("__")
		case r == ':' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
			b.WriteString(strconv.FormatInt(int64(r), 16))
			b.WriteByte('_')
		}
	}
	return b.String()
}
