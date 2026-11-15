package api

// endpoints_list.go — v0.10.2209: GET /api/endpoints işleyicisi api.go'dan
// kendi dosyasına taşındı (api.go büyümez) ve başlık hunisinin iki süzgecini
// aldı: ?method= (GET/POST/…) ve ?status= (2xx..5xx). İkisi de MV'de olmayan
// boyut → chstore sınırlı ham yola gider (cluster/env ile aynı sözleşme);
// önbellek anahtarı ikisini de taşır.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// endpointsMethodFilter — SAF: ?method= → bilinen HTTP fiili (büyük harf) ya da "".
func endpointsMethodFilter(v string) string {
	m := strings.ToUpper(strings.TrimSpace(v))
	switch m {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return m
	}
	return ""
}

// endpointsStatusFilter — SAF: ?status= → "2xx".."5xx" ya da "".
func endpointsStatusFilter(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "2xx", "3xx", "4xx", "5xx":
		return s
	}
	return ""
}

// getEndpoints — v0.5.365. Per-endpoint RED rollup driven from
// the OTel http.route attribute on server / consumer spans.
// Operator-asked: a /services-like top-level view, but keyed on
// the inbound request path instead of the service. Top N by the
// requested sort (v0.8.356 — server-side ORDER BY before the
// LIMIT; default calls DESC) so the table is a true global
// ranking, not a re-sorted top-N-by-calls page.
func (s *Server) getEndpoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := parseFromTo(r, time.Hour)
	service := q.Get("service")
	search := q.Get("search")
	cluster := q.Get("cluster")
	// Global env filter (v0.8.385, env-separation Phase 2) — the
	// Topbar picker's ?env=. Forces the raw-spans path (deploy_env
	// conjunct) exactly like cluster; the spanmetrics_1m MV has no
	// env dimension and stays untouched.
	env := strings.TrimSpace(q.Get("env"))
	limit := parseInt(q.Get("limit"), 500)
	// v0.5.389 — operator-reported: top-N was undercounting the
	// long tail. v0.5.395 — raised to 10000 + frontend exposes
	// an "All (10000)" option for the long-tail-heavy installs.
	// Payload is still bounded (10000 × 3 sparkline series × 30
	// buckets ≈ 7-8MB JSON worst case) but the operator opts
	// into the larger fetch by picking the explicit option, so
	// it can't sneak up on a casual page load.
	if limit > 10000 {
		limit = 10000
	}
	// v0.5.404 — optional prior-window comparison. When set to
	// "prior", a second GetEndpoints runs against the immediately-
	// preceding equal-length window (e.g. last 1h → previous 1h)
	// and the values are merged onto the current rows by
	// (service, path) key. Drives the "trend deltas" arrows on
	// the table. Opt-in (doubles CH cost) so the default page-
	// load stays a single scan.
	compare := q.Get("compare") == "prior"
	// v0.8.x — Uptrace-style "group by shape": cluster paths carrying IDs
	// (/orders/8421) into a normalized signature (/orders/:id) at read time.
	bySignature := q.Get("groupBy") == "signature"
	// v0.8.356 — server-side global sort (whitelisted in
	// chstore.endpointsOrderBy; unknown ids fall back to calls DESC).
	// Rides the cache key like every other input.
	sortBy := q.Get("sort")
	sortDir := q.Get("dir")
	// v0.9.313 (brief N1) — which inbound surface. Unknown values fall
	// back to http so a hand-edited URL lands on the familiar table
	// rather than an empty one.
	entry := chstore.EntryHTTP
	if strings.TrimSpace(r.URL.Query().Get("entry")) == "rpc" {
		entry = chstore.EntryRPC
	}
	// v0.10.2209 — başlık hunisi (yalnız HTTP yüzeyi): metot + durum sınıfı.
	method, statusClass := endpointsMethodFilter(q.Get("method")), endpointsStatusFilter(q.Get("status"))
	if entry == chstore.EntryRPC {
		method, statusClass = "", ""
	}
	key := endpointsListKey(cacheBucket(from, to), service, search, cluster, env, limit, compare, bySignature, sortBy, sortDir, string(entry)) +
		fmt.Sprintf(":m=%s:st=%s", method, statusClass)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		// v0.8.356 — default path reads the spanmetrics_1m MV; the raw
		// CTE survives only behind the cluster + env filters
		// (dimensions the MV lacks). Dispatch lives in
		// chstore.GetEndpoints.
		// v0.9.761 (mockup onayı: "kötüleşenler önce") — sort=p99Delta
		// SUNUCUDA sıralanır ama delta SQL'de yok: aday havuzu deseni.
		// Çağrıya göre ilk N*5 (500..1000) aday çekilir, prior birleşir,
		// delta hesaplanıp sıralanır, orijinal limit'e kesilir. Dürüstlük:
		// evren "en çok çağrılan ilk <havuz>" — UI notu bunu söyler.
		// compare bu sıralamada ZORUNLU (delta prior'suz tanımsız).
		deltaSort := sortBy == "p99Delta"
		if deltaSort {
			compare = true
		}
		eq := chstore.EndpointsQuery{
			From: from, To: to,
			Service: service, Search: search, Cluster: cluster, Env: env,
			Limit: limit, BySignature: bySignature,
			Sort: sortBy, Dir: sortDir,
			Entry:  entry,
			Method: method, StatusClass: statusClass,
		}
		// v0.9.812 — havuz artık limit'e saygılı (endpointsPool);
		// eski clamp(…, 1000) menünün üst üç seçeneğini ölü bırakıyordu.
		pool, poolWantCapped := 0, false
		if deltaSort {
			pool, poolWantCapped = endpointsPool(limit)
			eq.Limit = pool
			eq.Sort, eq.Dir = "calls", "desc" // aday havuzu tabanı
		}
		rows, err := s.store.GetEndpoints(ctx, eq)
		if err != nil {
			return nil, err
		}
		// Havuz uyarısı yalnız havuz GERÇEKTEN dolduğunda anlamlı:
		// evren tavandan darsa ama tüm endpoint'ler havuza sığdıysa
		// eksik bir şey yok.
		resp := endpointsListResponse{Rows: rows}
		if deltaSort {
			resp.Pool = pool
			resp.PoolCapped = endpointsPoolCapped(pool, poolWantCapped, len(rows))
		}
		if !compare || len(rows) == 0 {
			return resp, nil
		}
		// Prior window: chstore.EndpointsPriorWindow (v0.10.1028) — MV yolunda
		// ortak kova yok + eşit kova sayısı, ham yolda birebir süre kayması.
		// SkipStatus: the delta merge only reads calls/errors/avg/p99,
		// so the prior read skips the status/method sidecar.
		prior := eq
		prior.From, prior.To = chstore.EndpointsPriorWindow(eq)
		prior.SkipStatus = true
		priorRows, err := s.store.GetEndpoints(ctx, prior)
		if err != nil {
			// Prior failure is non-fatal — return current rows
			// without trends rather than 500'ing the page. Delta
			// sıralaması prior'suz yapılamaz: havuz satırları
			// (limit'ten büyük olabilir) yine limit'e kesilir,
			// yoksa "top 100" sessizce 500 satır dönerdi.
			if deltaSort && limit > 0 && len(rows) > limit {
				rows = rows[:limit]
			}
			resp.Rows = rows
			return resp, nil
		}
		// Index prior by (service, path) so we don't do an O(N²)
		// linear scan when merging.
		type key struct{ svc, path string }
		idx := make(map[key]*chstore.EndpointRow, len(priorRows))
		for i := range priorRows {
			idx[key{priorRows[i].Service, priorRows[i].Path}] = &priorRows[i]
		}
		for i := range rows {
			if p, ok := idx[key{rows[i].Service, rows[i].Path}]; ok {
				rows[i].PriorCalls = p.Calls
				rows[i].PriorErrors = p.Errors
				rows[i].PriorAvgMs = p.AvgMs
				rows[i].PriorP99Ms = p.P99Ms
			}
		}
		if deltaSort {
			rows = sortEndpointsByP99Delta(rows, limit)
		}
		resp.Rows = rows
		return resp, nil
	})
}
