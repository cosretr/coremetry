package ml

// log_service_identity.go — v0.10.2045 (Operator-reported: "Burada Api diyor
// ama Api diye izlenen bir servis yok; logtan buluyor."). Log ML olayının
// servisi Elasticsearch servis alanının terms kovasıydı (log_red, şablon
// spike, nadir kod, pod aykırısı): katalogla eşleme yoktu. Kural: log ML
// olayı yalnız Coremetry'nin bildiği bir servise yazılır.
//   1. ES adı katalogda birebir varsa → aynen.
//   2. Yoksa o adın penceredeki pod'larından (en çok 3; tik başına TEK ek ES
//      sorgusu, ML ES bütçesinde) clusters katmanının pod ↔ servis eşlemesi
//      (buildPodIndex = thanos.ServicePodKeys anlamı + metadata deployment).
//      Tek servise çözülürse olay O servise yazılır; kanıtta logService
//      kalır, anlatım "orders-api (logda: Api)".
//   3. Çözülemezse olay YAZILMAZ; tik durumunda sayılır.
// ml_series ES adıyla anahtarlı KALIR (taban öksüzlüğü yok): yalnız olayın
// gösterilen servisi değişir; seri / örnek / Logs okumaları logService ile.

import (
	"context"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	logSvcPodsPerName = 3
	logSvcMaxNames    = 50
)

// resolveLogService — SAF. ok=false: çözülemedi (yazılmaz).
func resolveLogService(name string, known map[string]bool, pods []string, ix podIndex) (svc string, viaPod, ok bool) {
	if name == "" {
		return "", false, false
	}
	if known[name] {
		return name, false, true
	}
	got := ""
	for _, p := range pods {
		s, found := ix.serviceOf(p)
		if !found || !known[s] {
			continue
		}
		if got != "" && got != s {
			return "", false, false // iki servise birden uyuyor
		}
		got = s
	}
	if got == "" {
		return "", false, false
	}
	return got, true, true
}

// logSvc — log okumalarının (Logs bağlantısı, seri) servis adı.
func (in CardInput) logSvc() string {
	if in.LogService != "" {
		return in.LogService
	}
	return in.Service
}

// SeriesService — ml_series anahtarı olan servis adı (api course okuması).
func (in CardInput) SeriesService() string { return in.logSvc() }

// logServiceOf — SAF: olayın log tarafındaki servis adı (seri / ES okumaları).
func logServiceOf(ev chstore.MLEvent) string {
	if s := evStr(evidenceMap(ev.Evidence), "logService"); s != "" {
		return s
	}
	return ev.Service
}

type logSvcStats struct{ Unmatched, ViaPod int }

// resolveLogServices — log sinyalli olayların servisini katalogla çözer.
// Katalog okunamazsa olaylar AYNEN geçer (fail-open: bugünkü davranış).
func resolveLogServices(ctx context.Context, deps Deps, evs []chstore.MLEvent, now time.Time, window time.Duration) ([]chstore.MLEvent, logSvcStats) {
	var st logSvcStats
	hasLog := false
	for _, ev := range evs {
		if ev.Signal == chstore.MLSignalLog {
			hasLog = true
			break
		}
	}
	if !hasLog || deps.Store == nil {
		return evs, st
	}
	names, err := deps.Store.ListActiveServiceNames(ctx, 24*time.Hour)
	if err != nil || len(names) == 0 {
		return evs, st
	}
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	var unmatched []string
	seen := map[string]bool{}
	for _, ev := range evs {
		if ev.Signal == chstore.MLSignalLog && !known[ev.Service] && !seen[ev.Service] && ev.Service != "" {
			seen[ev.Service] = true
			unmatched = append(unmatched, ev.Service)
		}
	}
	sort.Strings(unmatched)
	if len(unmatched) > logSvcMaxNames {
		unmatched = unmatched[:logSvcMaxNames]
	}
	pods := map[string][]string{}
	var ix podIndex
	if len(unmatched) > 0 && (deps.ESLeft == nil || deps.ESLeft() > 0) {
		if pr, ok := logstore.Unwrap(deps.Logs).(logstore.MLPodResolver); ok {
			t0 := time.Now()
			if p, perr := pr.MLServicePods(ctx, unmatched, now.Add(-window), now, logSvcPodsPerName); perr == nil && p != nil {
				pods = p
			}
			if deps.ObserveES != nil {
				deps.ObserveES(time.Since(t0))
			}
			meta, merr := deps.Store.ListServiceMetadata(ctx)
			if merr != nil {
				meta = nil
			}
			ix = buildPodIndex(names, meta)
		}
	}
	out := evs[:0]
	for _, ev := range evs {
		if ev.Signal != chstore.MLSignalLog || known[ev.Service] {
			out = append(out, ev)
			continue
		}
		svc, viaPod, ok := resolveLogService(ev.Service, known, pods[ev.Service], ix)
		if !ok {
			st.Unmatched++
			continue
		}
		if viaPod {
			st.ViaPod++
			ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"logService": ev.Service})
			ev.Service = svc
		}
		out = append(out, ev)
	}
	return out, st
}
