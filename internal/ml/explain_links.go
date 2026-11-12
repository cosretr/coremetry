package ml

// explain_links.go — v0.10.1842: ExplainCard'ın filtreli pivotları. FE
// üreticilerinin SUNUCU eşdeğeri (aynı param adları, aynı pencere kuralı):
//   tracesPivotHref / operationTracesHref  lib/pivotHref.ts
//   logsHref                               lib/logsUrl.ts
//   serviceHref                            lib/serviceHref.ts  (/service?name=)
//   endpointDetailHref                     pages/endpoints/endpointParam.ts
//   /endpoints?service=                    pages/Endpoints.tsx
// Pencere: windowRangeParam (lib/urlState.ts) — ns → ms, from TABAN, to
// TAVAN; ters/boş pencere yazılmaz. Olay penceresi [başlangıç − 15 dk,
// bitiş + 5 dk] (FE detay sayfasının eski `win` öncülü 15 dk; kuyruk 5 dk
// alım gecikmesi). Rollouts sayfasında servis süzgeci YOK (yalnız
// workload) → yayın linki servis sayfasının GitOps sekmesine gider.

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

type cardLinks struct {
	rangeParam string
	service    string
	rollouts   string
	subject    *ExplainLink
	route      string
}

// rangeParamOf — SAF: windowRangeParam'ın Go ikizi ("custom:<fromMs>-<toMs>").
func rangeParamOf(from, to time.Time) string {
	f := int64(math.Floor(float64(from.UnixNano()) / 1e6))
	t := int64(math.Ceil(float64(to.UnixNano()) / 1e6))
	if f <= 0 || t <= f {
		return ""
	}
	return fmt.Sprintf("custom:%d-%d", f, t)
}

func withRange(q url.Values, r string) url.Values {
	if r != "" {
		q.Set("range", r)
	}
	return q
}

func serviceLink(svc, tab, rng string) string {
	q := url.Values{}
	q.Set("name", svc)
	if tab != "" {
		q.Set("tab", tab)
	}
	return "/service?" + withRange(q, rng).Encode()
}

type tracesOpts struct {
	route    string
	minMs    float64
	hasError bool
}

func tracesLink(svc, rng string, o tracesOpts) string {
	q := url.Values{}
	q.Set("service", svc)
	if strings.HasPrefix(o.route, "/") { // http.route değeri; rpc/diğer etiket süzgeçsiz kalır
		b, _ := json.Marshal([]map[string]any{{"k": "http.route", "op": "=", "v": []string{o.route}}})
		q.Set("filters", string(b))
	}
	if o.minMs > 0 {
		q.Set("minMs", fmt.Sprintf("%g", o.minMs))
	}
	if o.hasError {
		q.Set("hasError", "true")
	}
	q.Set("rootOnly", "false")
	q.Set("view", "list")
	return "/traces?" + withRange(q, rng).Encode()
}

func logsLink(svc, query string, errorsOnly bool, rng string) string {
	q := url.Values{}
	q.Set("service", svc)
	if query != "" {
		q.Set("q", query)
	}
	if errorsOnly {
		q.Set("severity", "17")
	}
	return "/logs?" + withRange(q, rng).Encode()
}

func endpointsLink(svc, rng string) string {
	q := url.Values{}
	q.Set("service", svc)
	return "/endpoints?" + withRange(q, rng).Encode()
}

func endpointLink(svc, path, rng string) string {
	q := url.Values{}
	q.Set("service", svc)
	q.Set("path", path)
	return "/endpoint?" + withRange(q, rng).Encode()
}

// eventRoute — SAF: olayın endpoint'i (trace RED dedektörlerinde Code).
func eventRoute(in CardInput) string {
	switch in.Detector {
	case "op_latency", "op_error_rate", "throughput_drop", "trace_change_point":
		return in.Code
	}
	return ""
}

func buildCardLinks(in CardInput, open bool, end time.Time) cardLinks {
	to := end.Add(5 * time.Minute)
	if open && to.After(in.Now) && in.Now.After(in.StartedAt) {
		to = in.Now
	}
	rng := rangeParamOf(in.StartedAt.Add(-15*time.Minute), to)
	l := cardLinks{rangeParam: rng, service: serviceLink(in.Service, "", rng), rollouts: serviceLink(in.Service, "gitops", rng), route: eventRoute(in)}
	switch {
	case l.route != "":
		s := &ExplainLink{Label: l.route}
		if strings.HasPrefix(l.route, "/") {
			s.Href = endpointLink(in.Service, l.route, rng)
		}
		l.subject = s
	case in.Detector == "dependency_latency" && in.Code != "":
		l.subject = &ExplainLink{Label: "→ " + in.Code}
	case in.Pod != "":
		l.subject = &ExplainLink{Label: "pod " + in.Pod}
	}
	return l
}

// rolloutInWindow — SAF: [başlangıç − 2 sa, bitiş] içindeki en yeni yayın.
func rolloutInWindow(in CardInput, end time.Time) *CardRollout {
	var ro *CardRollout
	for i := range in.Rollouts {
		r := in.Rollouts[i]
		if r.At.Before(in.StartedAt.Add(-2*time.Hour)) || r.At.After(end) {
			continue
		}
		if ro == nil || r.At.After(ro.At) {
			ro = &r
		}
	}
	return ro
}

// whereToLookOf — SAF: 2–4 filtreli link (audit §7.3; mockup görünüm 2–4).
func whereToLookOf(in CardInput, l cardLinks) []ExplainLink {
	end := cardEnd(in, cardOpen(in))
	ro := rolloutInWindow(in, end)
	compare := func() ExplainLink {
		from := ro.At.Add(-30 * time.Minute)
		return ExplainLink{Label: "Sürüm karşılaştırması", Href: serviceLink(in.Service, "", rangeParamOf(from, end.Add(5*time.Minute)))}
	}
	svc := ExplainLink{Label: "Servis sayfası", Href: l.service}
	switch in.Signal {
	case "trace":
		var first ExplainLink
		switch in.Detector {
		case "op_latency":
			slow := in.SlowSec
			if slow <= 0 {
				slow = 1
			}
			first = ExplainLink{Label: "Yavaş trace'ler", Href: tracesLink(in.Service, l.rangeParam, tracesOpts{route: l.route, minMs: slow * 1000})}
		case "op_error_rate", "trace_error_count":
			first = ExplainLink{Label: "Hatalı trace'ler", Href: tracesLink(in.Service, l.rangeParam, tracesOpts{route: l.route, hasError: true})}
		default:
			first = ExplainLink{Label: "Trace'ler", Href: tracesLink(in.Service, l.rangeParam, tracesOpts{route: l.route})}
		}
		out := []ExplainLink{first}
		if l.route != "" {
			out = append(out, ExplainLink{Label: "Endpoint dağılımı", Href: endpointsLink(in.Service, l.rangeParam)})
		}
		if ro != nil {
			return append(out, compare())
		}
		return append(out, svc)
	case "log":
		var first ExplainLink
		switch {
		case in.NewTemplate != nil && in.NewTemplate.Text != "":
			qq := in.NewTemplate.Query
			if qq == "" {
				qq = quoteQ(strings.TrimSuffix(in.NewTemplate.Text, "…"))
			}
			first = ExplainLink{Label: "Bu mesajı içeren loglar", Href: logsLink(in.Service, qq, false, l.rangeParam)}
		case in.Detector == "template_spike" || in.Detector == "rare_template":
			first = ExplainLink{Label: "Bu mesajı içeren loglar", Href: logsLink(in.Service, quoteQ(in.Code), false, l.rangeParam)}
		case in.Detector == "log_volume" || in.Detector == "service_silent":
			first = ExplainLink{Label: "Loglar", Href: logsLink(in.Service, "", false, l.rangeParam)}
		default:
			first = ExplainLink{Label: "Hata logları", Href: logsLink(in.Service, "", true, l.rangeParam)}
		}
		return []ExplainLink{first, {Label: "Hatalı trace'ler", Href: tracesLink(in.Service, l.rangeParam, tracesOpts{hasError: true})}, svc}
	}
	tl := ExplainLink{Label: "Trace'ler", Href: tracesLink(in.Service, l.rangeParam, tracesOpts{})}
	if in.MetricUnit == "s" || in.MetricUnit == "ms" {
		tl.Label = "Yavaş trace'ler"
	}
	out := []ExplainLink{tl, {Label: "Endpoint dağılımı", Href: endpointsLink(in.Service, l.rangeParam)}}
	if ro != nil {
		out = append(out, compare())
	}
	return append(out, svc)
}

// quoteQ — SAF: serbest metin aramasında tümcecik ("…"); iç tırnak atılır.
func quoteQ(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), `"`, "")
	if s == "" {
		return ""
	}
	return `"` + s + `"`
}
