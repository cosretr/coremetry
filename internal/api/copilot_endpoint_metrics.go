package api

// copilot_endpoint_metrics.go — CoSRE v2 araç seti dilim 3b: endpoint_metrics
// okuması (mcptools.Deps.EndpointMetricRED; tasarım onayı 2026-10-31).
//
// Servis RED'iyle AYNI çözüm (resolveSvcREDInputs: aile, servise kapsanmış
// etiket keşfi, ortam eşleyicisi, durum etiketi) ve AYNI histogram:
//   - route dolu → v0.10.1481 rota sorguları (svcRouteREDQueries): istek hızı ·
//     5xx % · p95/p99, http_route (+ etiket varsa http_request_method) eşleyici;
//   - route boş → top-10: topk(10, sum by (http_route)(rate)) + aynı kümeye
//     `and on (http_route)` ile bağlı 5xx %.
//
// Span'e geri düşüş YOK. VM yok / histogram yok / gRPC ailesi / rota etiketi
// yok → Skip + gerekçe (kart gri). Etiket ya da istek hızı okuma hatası →
// error ("okunamadı ≠ yok"). Sorgular svcHTTPREDRunQuery'nin 60 sn
// önbelleğinden, en çok 3 paralel, toplam svcRouteREDBudget. Salt-okur.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// endpointTopK — top kipinin rota sayısı (mcptools emTopK ile aynı).
const endpointTopK = 10

// endpointMetricSkip — SAF: çözümden sonra okunabilir mi. err → okuma hatası
// (araç hatası); skip → okunmadı gerekçesi; ikisi de boş → sorgulanır.
func endpointMetricSkip(probe svcHTTPREDProbe, in svcREDInputs, ok bool) (string, error) {
	if !ok {
		if probe.LabelErr != nil {
			return "", fmt.Errorf("VictoriaMetrics etiket okuması BAŞARISIZ (okunamadı ≠ yok): %w", probe.LabelErr)
		}
		_, reason := pickServiceChartSource(probe)
		return reason, nil
	}
	switch {
	case in.fam.Kind == redKindGRPC:
		return "servisin VM ailesi gRPC — uç noktalar http_route'a eşlenmez (gRPC yöntemleri Service → HTTP/GRPC sekmesinde)", nil
	case !in.routeLabelOK:
		return "HTTP histogramı http_route etiketini taşımıyor — uç nokta kırılımı yok", nil
	}
	return "", nil
}

// endpointTopQueries — SAF: top-10 kipinin PromQL'leri (istek hızı + 5xx %);
// gövdeler servis RED şablonlarının aynısı, kırılım ailenin rota etiketi.
// statusLabel boşsa 5xx sorgusu YOK (0 sanılmasın).
func endpointTopQueries(fam redFamily, service, svcLabel, envMatcher, statusLabel string, winS int) []svcHTTPREDQuery {
	rps, _ := metricChartTemplateByID(mcHTTPRPS)
	fiveXX, _ := metricChartTemplateByID(mcHTTP5xx)
	by := " by (" + fam.RouteLabel + ")"
	in := func(v metricChartVariant) metricChartQueryInput {
		v.Probe, v.Names = fam.Probe, []string{fam.names()}
		v.Expr = strings.ReplaceAll(v.Expr, "{by}", by)
		return metricChartQueryInput{Variant: v, Service: service, SvcLabel: svcLabel, EnvMatcher: envMatcher, StatusLabel: statusLabel, ErrMatch: fam.ServerErr, WinS: winS}
	}
	rate := fmt.Sprintf("topk(%d, %s)", endpointTopK, buildMetricChartPromQL(in(rps.Variants[0])))
	out := []svcHTTPREDQuery{{Kind: "ep_top_" + svcHTTPREDRate, Title: svcHTTPREDTitle(service, "uç nokta istek hızı (top 10)"), Unit: "req/s", Name: "req/s", Query: rate}}
	if statusLabel != "" {
		q := "(" + buildMetricChartPromQL(in(fiveXX.Variants[0])) + ") and on (" + fam.RouteLabel + ") " + rate
		out = append(out, svcHTTPREDQuery{Kind: "ep_top_" + svcHTTPRED5xx, Title: svcHTTPREDTitle(service, "uç nokta 5xx oranı (top 10)"), Unit: "%", Name: "5xx %", Query: q})
	}
	return out
}

// mcpEndpointMetricRED — Deps.EndpointMetricRED (api/mcp_deps.go).
func (s *Server) mcpEndpointMetricRED(ctx context.Context, service, env, route, method string, from, to time.Time) (mcptools.EndpointREDResult, error) {
	rd0, isVM := s.metricChartReaderFor()
	rd, _ := rd0.(redFamilyReader)
	return s.readEndpointMetricRED(ctx, rd, isVM && rd != nil, service, env, route, method, from, to)
}

// readEndpointMetricRED — VM tarafı (okuyucu dışarıdan: test dikişi).
func (s *Server) readEndpointMetricRED(ctx context.Context, rd redFamilyReader, isVM bool, service, env, route, method string, from, to time.Time) (mcptools.EndpointREDResult, error) {
	var out mcptools.EndpointREDResult
	if !isVM || rd == nil {
		out.Skip = "VictoriaMetrics yapılandırılmamış"
		return out, nil
	}
	cctx, cancel := context.WithTimeout(ctx, svcRouteREDBudget)
	defer cancel()
	qTo := to.Truncate(time.Minute)
	qFrom := qTo.Add(-to.Sub(from))
	step := mcptools.EndpointMetricStep(int(to.Sub(from).Seconds()))
	probe := svcHTTPREDProbe{VM: true, GRPCChecked: true}
	in, ok := s.resolveSvcREDInputs(cctx, rd, service, env, qTo, &probe)
	skip, err := endpointMetricSkip(probe, in, ok)
	if err != nil {
		return out, err
	}
	if skip != "" {
		out.Skip = skip
		return out, nil
	}
	out.Env, out.Notes = in.env, in.notes
	var qs []svcHTTPREDQuery
	if route != "" {
		if method != "" && !in.methodLabelOK {
			out.Notes = append(out.Notes, fmt.Sprintf("histogram %s etiketini taşımıyor — %s süzgeci UYGULANMADI, rota tüm fiilleri kapsıyor", in.fam.MethodLabel, method))
			method = ""
		}
		qs = svcRouteREDQueries(in.fam, service, in.svcLabel, in.envMatcher, in.statusLabel, route, method, metricChartWinS(step))
	} else {
		qs = endpointTopQueries(in.fam, service, in.svcLabel, in.envMatcher, in.statusLabel, metricChartWinS(step))
	}
	charts := make([]svcHTTPREDChart, len(qs))
	var wg sync.WaitGroup
	for i := range qs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			charts[i] = s.svcHTTPREDRunQuery(cctx, rd, qs[i], service, in.envMatcher, qFrom, qTo, step)
		}(i)
	}
	wg.Wait()
	return endpointMetricFinish(out, charts)
}

// endpointMetricFinish — SAF: grafikler → sonuç. İstek hızı okunamadı → hata
// (okunamadı ≠ yok); 5xx / gecikme okunamadı → o alan yok + not.
func endpointMetricFinish(out mcptools.EndpointREDResult, charts []svcHTTPREDChart) (mcptools.EndpointREDResult, error) {
	for _, c := range charts {
		if c.Query != "" {
			out.Queries = append(out.Queries, c.Query)
		}
		if c.DurationMs > out.DurationMs {
			out.DurationMs = c.DurationMs
		}
		kind := c.Kind[strings.LastIndex(c.Kind, "_")+1:]
		if c.Err != nil {
			if kind == svcHTTPREDRate {
				return out, fmt.Errorf("VictoriaMetrics sorgusu BAŞARISIZ (okunamadı ≠ yok): %w", c.Err)
			}
			out.Notes = append(out.Notes, fmt.Sprintf("%s okunamadı (okunamadı ≠ yok): %v", c.Name, c.Err))
			continue
		}
		switch kind {
		case svcHTTPREDRate:
			out.Rate = c.Series
		case svcHTTPRED5xx:
			out.Err5xx = c.Series
		case svcHTTPREDLatency:
			out.Latency = c.Series
		}
	}
	return out, nil
}
