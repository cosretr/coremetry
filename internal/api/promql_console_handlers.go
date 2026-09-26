package api

// promql_console_handlers.go — v0.10.953 (Explore → PromQL konsolu, Thanos;
// spec C5 — kararlar 2, 3, 4, 9, 10, 11, 12; docs/promql-console/audit.md
// §5, §6, §9). Rotalar promql_console_routes.go'da; geçmiş (C6)
// promql_console_history.go'da.
//
// İstek akışı (query / query_range) — sıra BİLİNÇLİ:
//
//	1. parametreler (Prometheus HTTP API sözleşmesi: time/start/end RFC3339
//	   ya da unix saniye, step süre ya da saniye) — bozuk girdi 400,
//	   SESSİZ GERİ DÜŞÜŞ YOK (house parseFromTo'nun tersi, karar 10)
//	2. cluster (ClusterByRef: id ya da ad, yalnız etkin) — yoksa 404
//	3. aralık korkuluğu (maxRangeH üstü 400 guardrail) + etkin adım
//	4. kullanıcı başına eşzamanlılık + dakika bütçesi (429 + Retry-After)
//	5. Thanos (thanos.ConsoleQuery / ConsoleQueryRange, ayardan sınırlar)
//	6. geçmişe ekleme (yalnız KOŞAN sorgular) → yanıt
//
// Doğrulama limiterdan ÖNCE: bozuk bir istek kullanıcının bütçesini
// yakmasın. Her çağrı sonucu ne olursa olsun TEK audit satırı bırakır
// (ok / error / rejected — karar 2); metadata ve geçmiş uçları audit YAZMAZ
// (otomatik tamamlama gürültüsü; mcp_observe.go okuma duruşu).
//
// ── SIZINTI KURALI ───────────────────────────────────────────────────────
//
// writeErr'in varsayılan dalı ham err.Error() döner ve bir taşıma hatası
// yapılandırılmış uç nokta URL'sini taşır. Bu dosyada writeErr YOK: her
// hata ya *thanos.ConsoleError (Message URL/host taşımaz — thanos
// katmanının sözleşmesi) ya yerel *promqlGuardError; ikisi de değilse ham
// hata YALNIZ loga gider, istemciye genel bir "internal" mesajı döner.
//
// ── YANIT ŞEKLİ (karar 11) ───────────────────────────────────────────────
//
// Prometheus zarfı {status, data:{resultType, result}, warnings, infos} +
// meta {clusterId, effectiveQuery, step, stepRaised, series, totalSeries,
// truncated, durationMs}. data.result thanos katmanından VERBATİM gelir
// (json.RawMessage); writeJSON KULLANILMAZ çünkü sanitizeFloats bir
// RawMessage'ı bayt bayt reflect ile gezerdi (32 MiB'lık bir sonuçta
// saniyeler) — ve "NaN" zaten dize olarak taşınır, temizlenecek float yok.
// Hata {status:"error", errorType, error, position?, meta?}: koşan bir
// sorgunun hatası meta.effectiveQuery taşır (paylaşımlı querier'da hatanın
// enjekte matcher'lı ifadeye ait olduğunu UI gösterebilsin).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	promqlModeInstant = "instant"
	promqlModeRange   = "range"

	// promqlFormMaxBytes — POST form gövde tavanı. Sorgu ≤ 8 KiB
	// (maxPromQLQueryLen); URL kodlaması en kötü ×3 şişirir, üstüne diğer
	// parametreler: 64 KiB dürüst bir istemciyi asla reddetmez.
	promqlFormMaxBytes = 64 << 10
	// promqlMaxMatchers — metadata uçlarında match[] sayısı tavanı
	// (otomatik tamamlama tek seçici gönderir; tavan kötüye kullanıma).
	promqlMaxMatchers = 20
	// promqlLabelNameMax — label/{name}/values ad tavanı (bayt).
	promqlLabelNameMax = 1024
	// promqlClusterRefMax — ?cluster= tavanı (audit target_id'ye de girer).
	promqlClusterRefMax = 256
	// promqlMaxUnixSeconds — 9999-12-31T23:59:59Z; ötesi saçma ve
	// time.Unix'i taşırmaya yakın.
	promqlMaxUnixSeconds = 253402300799
)

// ── Hata eşlemesi ───────────────────────────────────────────────────────────

// promqlBadData — yerel parametre hatası (400 bad_data; Prometheus metni
// üslubu, İngilizce — thanos katmanının mesajlarıyla aynı dil).
func promqlBadData(format string, args ...any) *promqlGuardError {
	return &promqlGuardError{Status: http.StatusBadRequest, ErrorType: "bad_data", Msg: fmt.Sprintf(format, args...)}
}

// promqlConsoleErrorBody — koşan sorgunun hata gövdesi.
type promqlConsoleErrorBody struct {
	Status    string                  `json:"status"`
	ErrorType string                  `json:"errorType"`
	Error     string                  `json:"error"`
	Position  string                  `json:"position,omitempty"`
	Meta      *promqlConsoleErrorMeta `json:"meta,omitempty"`
}

type promqlConsoleErrorMeta struct {
	ClusterID      string `json:"clusterId"`
	EffectiveQuery string `json:"effectiveQuery,omitempty"`
	DurationMs     int64  `json:"durationMs"`
}

// promqlClassify — hata → (HTTP kodu, errorType). SAF; audit, geçmiş ve
// yanıt AYNI sınıfı görür.
func promqlClassify(err error) (int, string) {
	var ge *promqlGuardError
	if errors.As(err, &ge) {
		return ge.Status, ge.ErrorType
	}
	var ce *thanos.ConsoleError
	if errors.As(err, &ce) {
		return ce.StatusCode(), ce.Type
	}
	if errors.Is(err, context.Canceled) {
		return statusClientClosedRequest, thanos.ConsoleErrCanceled
	}
	return http.StatusBadGateway, thanos.ConsoleErrInternal
}

// writePromQLConsoleFailure — tek hata yazıcısı. 499'da gövde yok
// (istemci gitti; writeErr v0.7.13 duruşu — 5xx sayılıp kendi error_rate
// anomalimizi tetiklemesin). Tanınmayan hata ASLA ham yankılanmaz.
func writePromQLConsoleFailure(w http.ResponseWriter, err error, meta *promqlConsoleErrorMeta) {
	var ge *promqlGuardError
	if errors.As(err, &ge) {
		writePromQLGuardError(w, ge)
		return
	}
	code, typ := promqlClassify(err)
	if code == statusClientClosedRequest {
		w.WriteHeader(code)
		return
	}
	body := promqlConsoleErrorBody{Status: "error", ErrorType: typ, Meta: meta}
	var ce *thanos.ConsoleError
	if errors.As(err, &ce) {
		body.Error, body.Position = ce.Message, ce.Position
	} else {
		log.Printf("[promql] konsol: beklenmeyen hata: %v", err) // ham hata YALNIZ logda
		body.Error = "thanos console request failed"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// writePromQLConsoleJSON — 200 gövdesi; sanitizeFloats YOK (dosya başı).
func writePromQLConsoleJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ── Parametre ayrıştırma (karar 10) ─────────────────────────────────────────

// promqlParseTime — Prometheus parseTime: unix saniye (float, milisaniyeye
// yuvarlanır) ya da RFC3339(Nano). NaN/Inf/negatif/9999 ötesi reddedilir.
func promqlParseTime(s string) (time.Time, error) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > promqlMaxUnixSeconds {
			return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
		}
		sec, frac := math.Modf(f)
		ms := int64(math.Round(frac * 1000)) // tamsayı ms: float çarpımı 1 ns kaydırmasın
		return time.Unix(int64(sec), ms*int64(time.Millisecond)).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		if t.Unix() < 0 || t.Unix() > promqlMaxUnixSeconds {
			return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
		}
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
}

// promqlDurationRe — Prometheus model.ParseDuration sözdizimi (birimler
// büyükten küçüğe, her biri en çok bir kez: 1h30m, 5m, 90s, 1d, 500ms).
var promqlDurationRe = regexp.MustCompile(`^(?:([0-9]+)y)?(?:([0-9]+)w)?(?:([0-9]+)d)?(?:([0-9]+)h)?(?:([0-9]+)m)?(?:([0-9]+)s)?(?:([0-9]+)ms)?$`)

var promqlDurationUnits = [...]time.Duration{
	365 * 24 * time.Hour, 7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute, time.Second, time.Millisecond,
}

// promqlParseDuration — Prometheus parseDuration: önce float saniye
// ("15", "0.5"), sonra birimli süre ("30s", "5m", "1h30m"). Taşma hata.
// Pozitiflik kontrolü çağıranda (step için ayrı Prometheus mesajı).
func promqlParseDuration(s string) (time.Duration, error) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		ts := f * float64(time.Second)
		if math.IsNaN(ts) || math.IsInf(ts, 0) || ts >= math.MaxInt64 || ts <= math.MinInt64 {
			return 0, fmt.Errorf("cannot parse %q to a valid duration. It overflows int64", s)
		}
		return time.Duration(ts), nil
	}
	m := promqlDurationRe.FindStringSubmatch(s)
	if s == "" || m == nil {
		return 0, fmt.Errorf("cannot parse %q to a valid duration", s)
	}
	var total time.Duration
	for i, unit := range promqlDurationUnits {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(m[i+1], 10, 64)
		if err != nil || n > int64(math.MaxInt64/unit) || total > math.MaxInt64-time.Duration(n)*unit {
			return 0, fmt.Errorf("cannot parse %q to a valid duration. It overflows int64", s)
		}
		total += time.Duration(n) * unit
	}
	return total, nil
}

// promqlEvalRequest — query / query_range girdisi (ayrıştırılmış).
type promqlEvalRequest struct {
	mode       string
	query      string
	clusterRef string
	time       time.Time     // instant: değerlendirme anı (verilmezse sunucu "şimdi")
	start, end time.Time     // range
	step       time.Duration // range: istenen adım; 0 = otomatik
	points     int           // range: otomatik adımın hedef nokta sayısı; 0 = ayar
}

// parsePromQLEvalForm — SAF. Hata olsa bile o ana kadar okunan alanlar
// döner (audit satırı sorguyu ve cluster'ı taşısın).
//
// instant'ta time verilmezse `now` AÇIKÇA gönderilir: audit ve geçmiş
// neyin ne zaman değerlendirildiğini kesin söylesin (Thanos'un "şimdi"si
// ile pod saatinin farkı saniyeler mertebesinde, lookback bunu örter).
func parsePromQLEvalForm(form url.Values, mode string, now time.Time) (promqlEvalRequest, error) {
	req := promqlEvalRequest{mode: mode, query: form.Get("query"), clusterRef: strings.TrimSpace(form.Get("cluster"))}
	switch {
	case strings.TrimSpace(req.query) == "":
		return req, promqlBadData("query parameter is required")
	case len(req.query) > maxPromQLQueryLen:
		return req, promqlBadData("query is %d bytes, the console limit is %d", len(req.query), maxPromQLQueryLen)
	case req.clusterRef == "":
		return req, promqlBadData("cluster parameter is required")
	case len(req.clusterRef) > promqlClusterRefMax:
		return req, promqlBadData("cluster parameter exceeds %d bytes", promqlClusterRefMax)
	}
	if mode == promqlModeInstant {
		req.time = now.UTC()
		if v := form.Get("time"); v != "" {
			t, err := promqlParseTime(v)
			if err != nil {
				return req, promqlBadData("invalid parameter \"time\": %v", err)
			}
			req.time = t
		}
		return req, nil
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"start", &req.start}, {"end", &req.end}} {
		v := form.Get(p.name)
		if v == "" {
			return req, promqlBadData("%s parameter is required", p.name)
		}
		t, err := promqlParseTime(v)
		if err != nil {
			return req, promqlBadData("invalid parameter %q: %v", p.name, err)
		}
		*p.dst = t
	}
	if v := form.Get("step"); v != "" {
		d, err := promqlParseDuration(v)
		if err != nil {
			return req, promqlBadData("invalid parameter \"step\": %v", err)
		}
		if d <= 0 {
			return req, promqlBadData("zero or negative query resolution step widths are not accepted. Try a positive integer")
		}
		req.step = d
	}
	if v := form.Get("points"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return req, promqlBadData("invalid parameter \"points\": must be an integer")
		}
		req.points = n
	}
	return req, nil
}

// readPromQLEvalRequest — gövde tavanı + form (GET: sorgu dizesi; POST:
// x-www-form-urlencoded gövde, URL parametreleriyle birleşik — Prometheus
// FormValue semantiği).
func readPromQLEvalRequest(w http.ResponseWriter, r *http.Request, mode string, now time.Time) (promqlEvalRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, promqlFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return promqlEvalRequest{mode: mode}, &promqlGuardError{Status: http.StatusRequestEntityTooLarge,
				ErrorType: "bad_data", Msg: fmt.Sprintf("request body exceeds %d KiB", promqlFormMaxBytes>>10)}
		}
		return promqlEvalRequest{mode: mode}, promqlBadData("invalid form body: %v", err)
	}
	return parsePromQLEvalForm(r.Form, mode, now)
}

// promqlCluster — etkin cluster kaydı; yoksa 404 not_found (Prometheus
// errorType sözlüğü). /clusters handler'larıyla aynı çözüm (ClusterByRef).
func (s *Server) promqlCluster(ref string) (thanos.ClusterConfig, error) {
	if s.thanos == nil || !s.thanos.HasEnabledClusters() {
		return thanos.ClusterConfig{}, &promqlGuardError{Status: http.StatusNotFound, ErrorType: "not_found",
			Msg: "no thanos clusters are configured"}
	}
	c, ok := s.thanos.ClusterByRef(ref)
	if !ok {
		return thanos.ClusterConfig{}, &promqlGuardError{Status: http.StatusNotFound, ErrorType: "not_found",
			Msg: fmt.Sprintf("unknown or disabled cluster %q", ref)}
	}
	return c, nil
}

// promqlConsoleLimits — ayar → thanos istemci sınırları (birim çarpımı
// ayar yardımcılarında; burada el yazımı saniye/MiB yok).
func promqlConsoleLimits(cfg promqlConsoleSettings) thanos.ConsoleLimits {
	return thanos.ConsoleLimits{
		Timeout: cfg.timeout(), MaxSeries: cfg.MaxSeries,
		MaxBodyBytes: cfg.maxBodyBytes(), PartialResponse: cfg.PartialResponse,
	}
}

// promqlUser — kimlik; boş UserID 401 (aiChatOwner duruşu: limiter ve
// geçmiş anahtarı boş olamaz — hepsi tek kovaya düşerdi).
func promqlUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	c := auth.FromContext(r.Context())
	if c == nil || strings.TrimSpace(c.UserID) == "" {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return "", false
	}
	return c.UserID, true
}

// ── Audit (karar 2) ─────────────────────────────────────────────────────────

// promqlAuditDetails — audit_log.details JSON'u. Alanlar SABİT ve
// omitempty YOK: raporlar JSONExtract ile okur, şema satırdan satıra
// değişmesin. instant'ta start = end = değerlendirme anı, step 0.
type promqlAuditDetails struct {
	Cluster    string  `json:"cluster"`
	Mode       string  `json:"mode"`
	Query      string  `json:"query"`
	Start      string  `json:"start"`
	End        string  `json:"end"`
	Step       float64 `json:"step"` // saniye (etkin adım)
	DurationMs int64   `json:"durationMs"`
	Series     int     `json:"series"`
	Truncated  bool    `json:"truncated"`
	Status     string  `json:"status"`    // ok | error | rejected
	ErrorType  string  `json:"errorType"` // ok'ta ""
}

// promqlAuditTime — RFC3339 UTC, milisaniye; sıfır → "".
func promqlAuditTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// promqlClip — bayt tavanı, rune sınırında (audit satırı reddedilen dev
// bir gövdeyi taşımasın).
func promqlClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (s *Server) auditPromQLQuery(r *http.Request, targetID string, d promqlAuditDetails) {
	raw, err := json.Marshal(d)
	if err != nil {
		log.Printf("[promql] audit details: %v", err)
		return
	}
	s.audit(r, "promql.query", "thanos_cluster", targetID, string(raw))
}

// ── query / query_range ─────────────────────────────────────────────────────

// promqlQueryMeta — karar 11 meta'sı. step/requestedStep saniye; step
// yalnız range'de. series = tutulan seri sayısı (≤ maxSeries).
type promqlQueryMeta struct {
	ClusterID      string  `json:"clusterId"`
	EffectiveQuery string  `json:"effectiveQuery"`
	Step           float64 `json:"step,omitempty"`
	StepRaised     bool    `json:"stepRaised"`
	RequestedStep  float64 `json:"requestedStep,omitempty"`
	Series         int     `json:"series"`
	TotalSeries    int     `json:"totalSeries"`
	Truncated      bool    `json:"truncated"`
	DurationMs     int64   `json:"durationMs"`
}

type promqlQueryData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

type promqlQueryResponse struct {
	Status   string          `json:"status"`
	Data     promqlQueryData `json:"data"`
	Warnings []string        `json:"warnings,omitempty"`
	Infos    []string        `json:"infos,omitempty"`
	Meta     promqlQueryMeta `json:"meta"`
}

// promqlQuery — GET|POST /api/promql/query (query, time, cluster).
func (s *Server) promqlQuery(w http.ResponseWriter, r *http.Request) {
	s.servePromQLEval(w, r, promqlModeInstant)
}

// promqlQueryRange — GET|POST /api/promql/query_range (query, start, end,
// step, cluster, points).
func (s *Server) promqlQueryRange(w http.ResponseWriter, r *http.Request) {
	s.servePromQLEval(w, r, promqlModeRange)
}

func (s *Server) servePromQLEval(w http.ResponseWriter, r *http.Request, mode string) {
	began := time.Now()
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	// Tek audit satırı, HER çıkışta (karar 2): varsayılan "rejected";
	// koşan sorgu ok/error'a çevirir.
	rec := promqlAuditDetails{Mode: mode, Status: "rejected"}
	targetID := ""
	defer func() {
		if rec.DurationMs == 0 {
			rec.DurationMs = time.Since(began).Milliseconds()
		}
		s.auditPromQLQuery(r, targetID, rec)
	}()
	reject := func(err error) {
		_, rec.ErrorType = promqlClassify(err)
		writePromQLConsoleFailure(w, err, nil)
	}

	req, err := readPromQLEvalRequest(w, r, mode, began)
	rec.Query = promqlClip(req.query, maxPromQLQueryLen)
	rec.Cluster = promqlClip(req.clusterRef, promqlClusterRefMax)
	targetID = rec.Cluster
	if mode == promqlModeInstant {
		rec.Start, rec.End = promqlAuditTime(req.time), promqlAuditTime(req.time)
	} else {
		rec.Start, rec.End = promqlAuditTime(req.start), promqlAuditTime(req.end)
	}
	if err != nil {
		reject(err)
		return
	}
	cluster, err := s.promqlCluster(req.clusterRef)
	if err != nil {
		reject(err)
		return
	}
	targetID, rec.Cluster = cluster.EffectiveID(), cluster.Name

	cfg := currentPromQLConsoleSettings()
	var step time.Duration
	raised := false
	if mode == promqlModeRange {
		if err := promqlCheckRange(req.start, req.end, cfg); err != nil {
			reject(err)
			return
		}
		step, raised = promqlEffectiveStep(req.end.Sub(req.start), req.step, req.points, cfg)
		rec.Step = step.Seconds()
	}
	release, err := promqlLimits.acquireQuery(uid, cfg)
	if err != nil {
		reject(err)
		return
	}
	defer release()

	// ── koşan sorgu ──
	eff := cluster.EffectiveQuery(req.query)
	lim := promqlConsoleLimits(cfg)
	var res *thanos.ConsoleResult
	if mode == promqlModeInstant {
		res, err = s.thanos.ConsoleQuery(r.Context(), cluster, thanos.ConsoleInstantQuery{Query: req.query, Time: req.time}, lim)
	} else {
		res, err = s.thanos.ConsoleQueryRange(r.Context(), cluster,
			thanos.ConsoleRangeQuery{Query: req.query, Start: req.start, End: req.end, Step: step}, lim)
	}
	took := time.Since(began).Milliseconds()
	rec.DurationMs = took
	hist := promqlHistoryEntry{
		Query: req.query, ClusterID: cluster.EffectiveID(), Cluster: cluster.Name, Mode: mode,
		DurationMs: took, At: began.UnixMilli(),
	}

	if err != nil {
		_, typ := promqlClassify(err)
		rec.Status, rec.ErrorType = "error", typ
		// İptal edilen koşu geçmişe girmez: istemci gitti (çoğunlukla
		// editördeki yeni bir koşu onu geçersiz kıldı, o koşu kaydedilir).
		if typ != thanos.ConsoleErrCanceled {
			hist.Status, hist.ErrorType = "error", typ
			s.appendPromQLHistory(r.Context(), uid, hist)
		}
		writePromQLConsoleFailure(w, err, &promqlConsoleErrorMeta{
			ClusterID: cluster.EffectiveID(), EffectiveQuery: eff, DurationMs: took})
		return
	}

	rec.Status, rec.Series, rec.Truncated = "ok", res.Series, res.Truncated
	hist.Status, hist.Series = "ok", res.Series
	s.appendPromQLHistory(r.Context(), uid, hist)

	meta := promqlQueryMeta{
		ClusterID: cluster.EffectiveID(), EffectiveQuery: res.EffectiveQuery,
		Series: res.Series, TotalSeries: res.TotalSeries, Truncated: res.Truncated, DurationMs: took,
	}
	warnings := res.Warnings
	if mode == promqlModeRange {
		meta.Step, meta.StepRaised = step.Seconds(), raised
		if raised {
			// Yükseltme HATA DEĞİL (karar 4): meta + görünür uyarı; UI
			// "istenen 5s → kullanılan 60s" diyebilsin.
			meta.RequestedStep = req.step.Seconds()
			warnings = append(append([]string(nil), warnings...), fmt.Sprintf(
				"step raised from %s to %s: the console allows at most %d points per series with a minimum step of %s",
				req.step, step, cfg.MaxPointsPerSeries, cfg.minStep()))
		}
	}
	writePromQLConsoleJSON(w, promqlQueryResponse{
		Status: "success", Data: promqlQueryData{ResultType: res.ResultType, Result: res.Result},
		Warnings: warnings, Infos: res.Infos, Meta: meta,
	})
}

// ── Metadata: labels / label values / series (karar 9) ─────────────────────

const (
	promqlMetaKindLabels      = "labels"
	promqlMetaKindLabelValues = "label-values"
	promqlMetaKindSeries      = "series"
)

// promqlMetaRequest — metadata girdisi (ayrıştırılmış; pencere ve limit
// henüz korkuluktan geçmemiş).
type promqlMetaRequest struct {
	matches    []string
	clusterRef string
	start, end time.Time // sıfır = verilmedi (promqlMetaWindow: son 1h)
	limit      int       // 0 = verilmedi (promqlMetaLimit: 1000)
	refresh    bool
}

// parsePromQLMetaQuery — SAF.
func parsePromQLMetaQuery(q url.Values) (promqlMetaRequest, error) {
	req := promqlMetaRequest{clusterRef: strings.TrimSpace(q.Get("cluster")), refresh: q.Get("refresh") == "1"}
	if req.clusterRef == "" {
		return req, promqlBadData("cluster parameter is required")
	}
	if len(req.clusterRef) > promqlClusterRefMax {
		return req, promqlBadData("cluster parameter exceeds %d bytes", promqlClusterRefMax)
	}
	for _, m := range q["match[]"] {
		if strings.TrimSpace(m) == "" {
			continue
		}
		if len(m) > maxPromQLQueryLen {
			return req, promqlBadData("match[] selector is %d bytes, the console limit is %d", len(m), maxPromQLQueryLen)
		}
		req.matches = append(req.matches, m)
	}
	if len(req.matches) > promqlMaxMatchers {
		return req, promqlBadData("at most %d match[] selectors are allowed", promqlMaxMatchers)
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"start", &req.start}, {"end", &req.end}} {
		if v := q.Get(p.name); v != "" {
			t, err := promqlParseTime(v)
			if err != nil {
				return req, promqlBadData("invalid parameter %q: %v", p.name, err)
			}
			*p.dst = t
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return req, promqlBadData("invalid parameter \"limit\": must be an integer")
		}
		req.limit = n
	}
	return req, nil
}

type promqlMetaMeta struct {
	ClusterID      string   `json:"clusterId"`
	EffectiveMatch []string `json:"effectiveMatch"`
	Limit          int      `json:"limit"`
	Total          int      `json:"total"` // upstream limit uyguladıysa alt sınır
	Truncated      bool     `json:"truncated"`
	Start          int64    `json:"start"` // unix s — uygulanan (ızgaralı, kelepçeli) pencere
	End            int64    `json:"end"`
}

type promqlMetaResponse struct {
	Status   string         `json:"status"`
	Data     any            `json:"data"` // []string | []map[string]string
	Warnings []string       `json:"warnings,omitempty"`
	Infos    []string       `json:"infos,omitempty"`
	Meta     promqlMetaMeta `json:"meta"`
}

// promqlMetaChargeKey — bağlam değeri: bu hesaplama bir ÖN PLAN isteğine
// ait, bütçe ve eşzamanlılık slotu o kullanıcıdan düşülür. SWR arka plan
// tazelemesi (cache.go refreshKey) kendi bağlamını kurar → değer yok →
// bütçe yanmaz, slot tutulmaz: bayat isabet de bir isabettir (karar 3
// "cache hits do not count").
type promqlMetaChargeKey struct{}

// promqlLabels — GET /api/promql/labels.
func (s *Server) promqlLabels(w http.ResponseWriter, r *http.Request) {
	s.servePromQLMeta(w, r, promqlMetaKindLabels, "")
}

// promqlLabelValues — GET /api/promql/label/{name}/values.
func (s *Server) promqlLabelValues(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || len(name) > promqlLabelNameMax || !utf8.ValidString(name) {
		writePromQLGuardError(w, promqlBadData("label name must be 1-%d bytes of valid UTF-8", promqlLabelNameMax))
		return
	}
	s.servePromQLMeta(w, r, promqlMetaKindLabelValues, name)
}

// promqlSeries — GET /api/promql/series.
func (s *Server) promqlSeries(w http.ResponseWriter, r *http.Request) {
	s.servePromQLMeta(w, r, promqlMetaKindSeries, "")
}

// servePromQLMeta — zaman-sınırlı (varsayılan son 1h, maxRange'e
// kelepçe), limit sunucuda zorlanır, 60 s önbellek. Kapı (acquireMeta:
// eşzamanlılık slotu + dakika bütçesi) cachedJSON'un MISS kapanışının
// İÇİNDE: isabet kapanışı çalıştırmaz; slot upstream çağrısı boyunca
// tutulur. Singleflight takipçisi ve SWR tazelemesi slot almaz (takipçinin
// kapanışı koşmaz; refreshKey bağlamı ücret işareti taşımaz).
// serveCached DEĞİL cachedJSON: serveCached hatayı writeErr'e verir, 429
// 500'e dönerdi ve ham hata yankılanırdı.
func (s *Server) servePromQLMeta(w http.ResponseWriter, r *http.Request, kind, labelName string) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	req, err := parsePromQLMetaQuery(r.URL.Query())
	if err != nil {
		writePromQLConsoleFailure(w, err, nil)
		return
	}
	cluster, err := s.promqlCluster(req.clusterRef)
	if err != nil {
		writePromQLConsoleFailure(w, err, nil)
		return
	}
	cfg := currentPromQLConsoleSettings()
	start, end, err := promqlMetaWindow(req.start, req.end, time.Now(), cfg)
	if err != nil {
		writePromQLConsoleFailure(w, err, nil)
		return
	}
	limit := promqlMetaLimit(req.limit)
	// Cluster başına URL modelinde seçicisiz /series'i Thanos reddeder;
	// burada kesilir ki bütçe ve önbellek yuvası yanmasın.
	if kind == promqlMetaKindSeries && len(cluster.EffectiveMatchers(req.matches)) == 0 {
		writePromQLConsoleFailure(w, promqlBadData("at least one match[] selector is required"), nil)
		return
	}
	key := promqlMetaKeyFor(cluster, kind, labelName, req.matches, start, end, limit, cfg)
	mq := thanos.ConsoleMetaQuery{Match: req.matches, Start: start, End: end, Limit: limit}
	lim := promqlConsoleLimits(cfg)

	var ran atomic.Bool
	fn := func(ctx context.Context) (any, error) {
		ran.Store(true)
		if user, ok := ctx.Value(promqlMetaChargeKey{}).(string); ok {
			// v0.10.953 — slot upstream çağrısı BOYUNCA tutulur (defer):
			// bir kullanıcı ıskada 240 taramayı aynı anda uçuramaz.
			rel, err := promqlLimits.acquireMeta(user, cfg)
			if err != nil {
				return nil, err
			}
			defer rel()
		}
		// Kapanan ctx KULLANILIR (r.Context() değil): SWR tazelemesi kendi
		// bağlamıyla koşar (v0.8.319 dersi).
		return s.fetchPromQLMeta(ctx, cluster, kind, labelName, mq, lim)
	}
	ctx := context.WithValue(r.Context(), promqlMetaChargeKey{}, uid)
	body, tier, err := s.cachedJSON(ctx, key, promqlMetaCacheTTL, req.refresh, fn)
	// Yuvayı paylaşan birinin hatası (başka kullanıcının 429'u / liderin
	// iptali) bu isteğe geçmesin: kendi bağlamı ve bütçesiyle bir kez daha.
	if promqlMetaShouldRetry(err, ran.Load(), r.Context().Err()) {
		body, tier, err = s.cachedJSON(ctx, key, promqlMetaCacheTTL, req.refresh, fn)
	}
	if err != nil {
		writePromQLConsoleFailure(w, err, nil)
		return
	}
	writeCacheHit(w, tier, body)
}

// promqlMetaKeyFor — servePromQLMeta'nın önbellek anahtarı. v0.10.953 —
// TEK kurulum yeri: singleflight regresyon testi anahtarı aynı fonksiyonla
// yeniden kurar, iki kopya birbirinden kaymasın.
func promqlMetaKeyFor(c thanos.ClusterConfig, kind, labelName string, matches []string, start, end time.Time, limit int, cfg promqlConsoleSettings) string {
	injName, injValue := c.EffectiveThanosLabel()
	return promqlMetaCacheKey(promqlMetaKeyInput{
		Kind: kind, LabelName: labelName, ClusterID: c.EffectiveID(), URL: c.URL,
		InjectName: injName, InjectValue: injValue, NamespaceFilter: c.NamespaceFilter,
		Matches: matches, Start: start, End: end, Limit: limit, PartialResponse: cfg.PartialResponse,
	})
}

// promqlMetaShouldRetry — v0.10.953 — SAF. singleflight hatayı yuvayı
// paylaşan herkese dağıtır. Kapanış bu istekte KOŞMADIYSA (takipçi) ve
// isteğin kendi bağlamı canlıysa, iki paylaşılan hata sınıfı bu isteğe ait
// değildir: başka bir kullanıcının korkuluk reddi (429 — onun bütçesi) YA DA
// liderin iptali (otomatik tamamlama tuş vuruşunda vazgeçti;
// ConsoleError{canceled} / sarılı context.Canceled). O zaman kendi bağlamı
// ve bütçesiyle bir kez daha denenir (yuva artık boş). Kendi bağlamı
// bittiyse yeniden deneme anlamsız: hata olduğu gibi yazılır.
func promqlMetaShouldRetry(err error, ran bool, ownCtxErr error) bool {
	if err == nil || ran || ownCtxErr != nil {
		return false
	}
	var ge *promqlGuardError
	if errors.As(err, &ge) {
		return true
	}
	_, typ := promqlClassify(err)
	return typ == thanos.ConsoleErrCanceled
}

// fetchPromQLMeta — upstream çağrısı + yanıt şekli (önbelleğe giren değer;
// durationMs YOK — isabette bayat olurdu). v0.10.953 — tutulan değerler
// promqlMetaMaxCachedBytes'a kırpılır (L1 bayt tavansız, Redis 180 s):
// meta.total upstream sayısı kalır, kırpma truncated'a OR'lanır.
func (s *Server) fetchPromQLMeta(ctx context.Context, c thanos.ClusterConfig, kind, labelName string, mq thanos.ConsoleMetaQuery, lim thanos.ConsoleLimits) (any, error) {
	meta := promqlMetaMeta{ClusterID: c.EffectiveID(), Limit: mq.Limit, Start: mq.Start.Unix(), End: mq.End.Unix()}
	resp := promqlMetaResponse{Status: "success"}
	switch kind {
	case promqlMetaKindSeries:
		res, err := s.thanos.ConsoleSeries(ctx, c, mq, lim)
		if err != nil {
			return nil, err
		}
		sets, cut := promqlTrimMetaSeries(res.Series, promqlMetaMaxCachedBytes)
		resp.Data, resp.Warnings, resp.Infos = sets, res.Warnings, res.Infos
		meta.EffectiveMatch, meta.Total, meta.Truncated = res.EffectiveMatch, res.Total, res.Truncated || cut
	default:
		var res *thanos.ConsoleLabelsResult
		var err error
		if kind == promqlMetaKindLabelValues {
			res, err = s.thanos.ConsoleLabelValues(ctx, c, labelName, mq, lim)
		} else {
			res, err = s.thanos.ConsoleLabels(ctx, c, mq, lim)
		}
		if err != nil {
			return nil, err
		}
		vals, cut := promqlTrimMetaStrings(res.Values, promqlMetaMaxCachedBytes)
		resp.Data, resp.Warnings, resp.Infos = vals, res.Warnings, res.Infos
		meta.EffectiveMatch, meta.Total, meta.Truncated = res.EffectiveMatch, res.Total, res.Truncated || cut
	}
	resp.Meta = meta
	return resp, nil
}
