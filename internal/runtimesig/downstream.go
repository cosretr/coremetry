package runtimesig

// downstream.go — follow-up 2026-11-21 (operatör onayı): giden çağrıların
// HEDEF başına RED'i, OTel HTTP client histogramından
// (http_client_request_duration_seconds_{bucket,count,sum}; etiketler
// server_address, http_response_status_code, error_type,
// http_request_method). Neden metrik: prod span'leri upstream örneklenmiş
// (~70 milyar span / 30 gün) — topology_edges_5m oranları örneklemeyle
// eğilir; client histogramı ÖRNEKLEME ÖNCESİ tam sayımdır.
//
// Bütçe (MV-first'ün metrik ikizi): servis başına ≤ 4 anlık sorgu, hepsi
// sınırlı — (1) topk(20) hedef hızı, (2) yalnız o 20 hedefin hata türleri
// (topk 100), (3) yalnız o 20 hedefin p95'i, (4) HttpClient havuzu (3 skaler).
// Hata türü etiketi yoksa (eski SDK) 5xx durum koduna tek yedek sorgu.
// Pencere = istenen aralık, [5 dk, 1 sa] içine kıstırılır (rate penceresi;
// adım kavramı yok, anlık değerlendirme `to`'da). API 60 s önbellekler.

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

const (
	// DownstreamMetric — OTel http.client.request.duration'ın Prometheus adı.
	DownstreamMetric = "http_client_request_duration_seconds"
	// DownstreamTopK — tablo / kanıt hedef tavanı.
	DownstreamTopK      = 20
	downstreamErrTopK   = 100
	downstreamMinWinSec = 300
	downstreamMaxWinSec = 3600
	downstreamAddrLabel = "server_address"
	downstreamErrLabel  = "error_type"
	downstreamCodeLabel = "http_response_status_code"
)

// PromInstant — VM anlık sorgu yüzü (vmetrics.Service).
type PromInstant interface {
	QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error)
}

// TargetRED — tek hedefin giden çağrı RED'i (pencere ortalaması).
type TargetRED struct {
	Address string `json:"address"`
	// Service — topolojinin bildiği servis (ResolveExtPeer); yoksa "".
	Service  string  `json:"service,omitempty"`
	RPS      float64 `json:"rps"`
	P95Ms    float64 `json:"p95Ms"` // < 0 = ölçülemedi
	ErrorPct float64 `json:"errorPct"`
	// DominantError — en sık hata türü (error_type ya da 5xx kodu) ve
	// hatalar içindeki payı (%).
	DominantError    string  `json:"dominantError,omitempty"`
	DominantErrorPct float64 `json:"dominantErrorPct,omitempty"`
}

// HTTPPoolState — Apache HttpClient havuzu (servis düzeyi; hedef etiketi yok).
type HTTPPoolState struct {
	Leased  float64 `json:"leased"`
	Max     float64 `json:"max"`
	Pending float64 `json:"pending"`
}

// DownstreamReport — API + CoSRE ortak gövde.
type DownstreamReport struct {
	Service   string         `json:"service"`
	Metric    string         `json:"metric"`
	WindowSec int            `json:"windowSec"`
	AtNs      int64          `json:"atNs"`
	Targets   []TargetRED    `json:"targets"`
	Pool      *HTTPPoolState `json:"pool,omitempty"`
	// ErrorSource — error_type | status_code | none (hata kırılımının kaynağı).
	ErrorSource string   `json:"errorSource"`
	Scope       string   `json:"scope,omitempty"` // uygulanan ortam eşleyicisi
	Notes       []string `json:"notes,omitempty"`
}

// DownstreamWindowSec — SAF: aralık → rate penceresi (sn).
func DownstreamWindowSec(from, to time.Time) int {
	w := int(to.Sub(from).Seconds())
	if w < downstreamMinWinSec {
		return downstreamMinWinSec
	}
	if w > downstreamMaxWinSec {
		return downstreamMaxWinSec
	}
	return w
}

// downstreamSel — SAF: seçici gövdesi (servis + isteğe bağlı ortam eşleyicisi + ek).
func downstreamSel(svcLabel, service, scope string, extra ...string) string {
	parts := []string{svcLabel + "=" + vmetrics.PromStrTS(service)}
	if s := strings.TrimSpace(scope); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, extra...)
	return "{" + strings.Join(parts, ", ") + "}"
}

func addrRegex(addrs []string) string {
	q := make([]string, 0, len(addrs))
	for _, a := range addrs {
		q = append(q, regexp.QuoteMeta(a))
	}
	sort.Strings(q)
	return downstreamAddrLabel + "=~" + vmetrics.PromStrTS(strings.Join(q, "|"))
}

// DownstreamRateQuery — SAF: hedef başına istek/sn, en yoğun K.
func DownstreamRateQuery(svcLabel, service, scope string, winSec int) string {
	return fmt.Sprintf("topk(%d, sum by (%s) (rate(%s_count%s[%ds])))", DownstreamTopK, downstreamAddrLabel,
		DownstreamMetric, downstreamSel(svcLabel, service, scope), winSec)
}

// DownstreamErrQuery — SAF: seçili hedeflerin hata türü başına hızı.
// byCode=true → error_type yoksa 5xx durum kodu yedeği.
func DownstreamErrQuery(svcLabel, service, scope string, addrs []string, winSec int, byCode bool) string {
	lbl, m := downstreamErrLabel, downstreamErrLabel+`!=""`
	if byCode {
		lbl, m = downstreamCodeLabel, downstreamCodeLabel+`=~"5.."`
	}
	return fmt.Sprintf("topk(%d, sum by (%s, %s) (rate(%s_count%s[%ds])))", downstreamErrTopK, downstreamAddrLabel, lbl,
		DownstreamMetric, downstreamSel(svcLabel, service, scope, m, addrRegex(addrs)), winSec)
}

// DownstreamP95Query — SAF: seçili hedeflerin p95'i (yalnız o hedeflerin kovaları).
func DownstreamP95Query(svcLabel, service, scope string, addrs []string, winSec int) string {
	return fmt.Sprintf("histogram_quantile(0.95, sum by (%s, le) (rate(%s_bucket%s[%ds])))", downstreamAddrLabel,
		DownstreamMetric, downstreamSel(svcLabel, service, scope, addrRegex(addrs)), winSec)
}

// DownstreamPoolQuery — SAF: HttpClient havuzu üç skaler (label_set ile k=).
func DownstreamPoolQuery(svcLabel, service, scope string) string {
	arm := func(metric, k string, extra ...string) string {
		return fmt.Sprintf(`label_set(sum(last_over_time(%s%s[5m])), "k", %q)`, metric, downstreamSel(svcLabel, service, scope, extra...), k)
	}
	return arm("httpcomponents_httpclient_pool_total_pending", "pending") + " or " +
		arm("httpcomponents_httpclient_pool_total_connections", "leased", `state="leased"`) + " or " +
		arm("httpcomponents_httpclient_pool_total_max", "max")
}

// GroupKeyLabels — SAF: vmetrics GroupKey ("k=\"v\"" sıralı) → harita.
func GroupKeyLabels(gk []string) map[string]string {
	out := make(map[string]string, len(gk))
	for _, kv := range gk {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		}
		out[k] = v
	}
	return out
}

func lastVal(s chstore.SpanMetricSeries) (float64, bool) {
	if len(s.Points) == 0 {
		return 0, false
	}
	v := s.Points[len(s.Points)-1].Value
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// AssembleDownstream — SAF: dört okumanın sonucu → hedef satırları (hıza göre
// azalan; eşitlikte adres). errLabel: hata serilerinin tür etiketi.
func AssembleDownstream(rates, errs, p95 []chstore.SpanMetricSeries, errLabel string, resolve func(string) string) []TargetRED {
	by := map[string]*TargetRED{}
	order := []string{}
	for _, s := range rates {
		addr := GroupKeyLabels(s.GroupKey)[downstreamAddrLabel]
		v, ok := lastVal(s)
		if addr == "" || !ok || by[addr] != nil {
			continue
		}
		t := &TargetRED{Address: addr, RPS: round3(v), P95Ms: -1}
		if resolve != nil {
			t.Service = resolve(addr)
		}
		by[addr] = t
		order = append(order, addr)
	}
	type errAcc struct {
		total float64
		kinds map[string]float64
	}
	ea := map[string]*errAcc{}
	for _, s := range errs {
		l := GroupKeyLabels(s.GroupKey)
		addr := l[downstreamAddrLabel]
		v, ok := lastVal(s)
		if by[addr] == nil || !ok || v <= 0 {
			continue
		}
		a := ea[addr]
		if a == nil {
			a = &errAcc{kinds: map[string]float64{}}
			ea[addr] = a
		}
		a.total += v
		a.kinds[l[errLabel]] += v
	}
	for addr, a := range ea {
		t := by[addr]
		if t.RPS > 0 {
			t.ErrorPct = round3(math.Min(100, a.total/t.RPS*100))
		}
		best, bv := "", -1.0
		for k, v := range a.kinds {
			if v > bv || (v == bv && k < best) {
				best, bv = k, v
			}
		}
		if best != "" && a.total > 0 {
			t.DominantError, t.DominantErrorPct = best, round3(bv/a.total*100)
		}
	}
	for _, s := range p95 {
		addr := GroupKeyLabels(s.GroupKey)[downstreamAddrLabel]
		if v, ok := lastVal(s); ok && by[addr] != nil {
			by[addr].P95Ms = round3(v * 1000)
		}
	}
	out := make([]TargetRED, 0, len(order))
	for _, a := range order {
		out = append(out, *by[a])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RPS != out[j].RPS {
			return out[i].RPS > out[j].RPS
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// assemblePool — SAF: havuz sorgusu → durum; üç skalerin hiçbiri yoksa nil.
func assemblePool(series []chstore.SpanMetricSeries) *HTTPPoolState {
	if len(series) == 0 {
		return nil
	}
	p := &HTTPPoolState{}
	seen := false
	for _, s := range series {
		v, ok := lastVal(s)
		if !ok {
			continue
		}
		switch GroupKeyLabels(s.GroupKey)["k"] {
		case "pending":
			p.Pending, seen = v, true
		case "leased":
			p.Leased, seen = v, true
		case "max":
			p.Max, seen = v, true
		}
	}
	if !seen {
		return nil
	}
	return p
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// ReadDownstream — servisin hedef başına giden RED'i (VM). scope: uygulanmış
// ortam eşleyicisi ("" = yok). Hız sorgusu düşerse hata; ikincil okumalar
// (hata, p95, havuz) düşerse not düşülür, satırlar kalır.
func ReadDownstream(ctx context.Context, r PromInstant, svcLabel, service, scope string, from, to time.Time, resolve func(string) string) (DownstreamReport, error) {
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	win := DownstreamWindowSec(from, to)
	rep := DownstreamReport{Service: service, Metric: DownstreamMetric, WindowSec: win, AtNs: to.UnixNano(),
		Targets: []TargetRED{}, ErrorSource: "none", Scope: scope}
	rates, err := r.QueryPromQLInstant(ctx, DownstreamRateQuery(svcLabel, service, scope, win), to)
	if err != nil {
		return rep, fmt.Errorf("giden çağrı hızı okunamadı: %w", err)
	}
	addrs := make([]string, 0, len(rates))
	for _, s := range rates {
		if a := GroupKeyLabels(s.GroupKey)[downstreamAddrLabel]; a != "" {
			addrs = append(addrs, a)
		}
	}
	if pool, perr := r.QueryPromQLInstant(ctx, DownstreamPoolQuery(svcLabel, service, scope), to); perr == nil {
		rep.Pool = assemblePool(pool)
	} else {
		rep.Notes = append(rep.Notes, "HttpClient havuzu okunamadı: "+perr.Error())
	}
	if len(addrs) == 0 {
		return rep, nil
	}
	errLabel := downstreamErrLabel
	errs, eerr := r.QueryPromQLInstant(ctx, DownstreamErrQuery(svcLabel, service, scope, addrs, win, false), to)
	if eerr == nil && len(errs) > 0 {
		rep.ErrorSource = "error_type"
	} else if eerr == nil {
		errLabel = downstreamCodeLabel
		errs, eerr = r.QueryPromQLInstant(ctx, DownstreamErrQuery(svcLabel, service, scope, addrs, win, true), to)
		if eerr == nil {
			rep.ErrorSource = "status_code"
		}
	}
	if eerr != nil {
		rep.Notes = append(rep.Notes, "hata kırılımı okunamadı: "+eerr.Error())
	}
	p95, perr := r.QueryPromQLInstant(ctx, DownstreamP95Query(svcLabel, service, scope, addrs, win), to)
	if perr != nil {
		rep.Notes = append(rep.Notes, "p95 okunamadı: "+perr.Error())
	}
	rep.Targets = AssembleDownstream(rates, errs, p95, errLabel, resolve)
	return rep, nil
}
