package api

// services_metric_rank.go — v0.10.2444 (operatör onaylı kuyruk maddesi):
// Services listesinin İstek / Hata % / Avg / P99 sıralaması TÜM sayfalarda
// örneklenmemiş VictoriaMetrics değerinden.
//
// NEDEN: /api/services sayfalı (50) ve CH'de span değeriyle ORDER BY yapıyor;
// prod span'leri örneklendiği için (hatalar + %5) "en çok istek alan" ya da
// "en yüksek hata oranlı" sıra örneklemenin sırasıydı. v0.10.2437 metrikle
// yeniden sıralamayı yalnız tek sayfalık sonuca açmıştı (bilinen sınır).
//
// GET /api/services-metric-rank — /api/services ile AYNI parametreler
// (name, ownerTeam, sreTeam, namespace, cluster, env, errorsOnly, minSpans,
// minP99, compare, from/to, sort, dir, limit, offset) + infra=1. Yanıt aynı
// sayfa biçimi + ranked / metricRanked / notes.
//
// AKIŞ:
//  1. Evren: /api/services'in span sorgusu, aynı süzgeçler ve aynı span
//     sırası (sort/dir), LIMIT servicesMetricRankUniverseMax+1, OFFSET 0.
//     Liste neyi gösteriyorsa sıralanan küme odur (takım / namespace /
//     errorsOnly / minSpans / minP99 span süzgeçleri dahil).
//  2. Filo sıralaması: aile başına TEK anlık sorgu, servis seçicisi YOK
//     (env/cluster kapsamı + tesisat rotası dışlaması /services-red-metric
//     ile aynı). Sayı kısmı `sum by (svc, cm_s)` — durum kodları label_replace
//     ile ≤3 sınıfa indirilir (yok / ok / hata), yanıt servis başına ≤3 seri;
//     avg / p99 sütununda `or` ile cm_k etiketli servis başına 1 seri daha.
//     Servis İLK istek veren ailede kalır (servicesREDMetric önceliğiyle aynı,
//     aileler toplanmaz). 60 sn önbellek (anahtar TÜM girdiler) + singleflight.
//  3. Birleştirme (SAF servicesMetricRankMerge): evrende metrik değeri OLAN
//     servisler metrik değerine göre (dir), ardından metrik değeri olmayanlar
//     (metrik kaynağı yok ya da sütun ölçülemiyor — ör. durum etiketi yok)
//     evrenin span sırasıyla. Eşitlikte evren sırası. Sonra sayfa kesilir.
//     Metrik grubu dir'den bağımsız ÖNDE: örneklenmemiş değer ile örneklenmiş
//     değer tek eksende karşılaştırılmaz; ikinci grup "örneklenmiş" rozetini
//     taşımaya devam eder (rozet /services-red-metric cevabından, değişmedi).
//     v0.10.2451 — evrene span'i OLMAYAN metrik servisleri (MetricOnly)
//     eklenir, aynı süzgeçlerle (services_metric_only.go); toplam ≤ tavan.
//
// DÜRÜST GERİ DÜŞÜŞ (ranked=false + not; sayfa = /api/services'in span sırası):
// metrik dışı sütun, VM olmayan kaynak, pencere > 24 sa, evren > tavan, ya da
// herhangi bir ailenin okuma hatası / zaman aşımı (kısmi sıra, bir ailenin
// servislerini yanlış gruba atardı). Hata önbelleğe yazılmaz.
//
// MALİYET (DECISIONS 2026-11-22 v0.10.2444): yerel VM'de 1 sa sayı sınıfı 118
// seri 20 ms, p99 birleşik 2.068 kova serisi 8 ms; 24 sa 825 / 13.590 seri.
// Prod'a (binlerce servis) p99 kestirimi saniyeler mertebesi — sorgu başına 8 sn,
// toplam 15 sn tavan; aşan aile → geri düşüş. Anomali dedektörü aynı filo
// geneli `sum by (svc)` sorgularını prod'da her tik koşuyor (emsal).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() {
	registerRoutesExtra("services-metric-rank", (*Server).registerServicesMetricRankRoutes)
}

func (s *Server) registerServicesMetricRankRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services-metric-rank", s.getServicesMetricRank)
}

const (
	servicesMetricRankUniverseMax = 10000
	servicesMetricRankMaxWindow   = 24 * time.Hour
	servicesMetricRankQueryCap    = 8 * time.Second
	servicesMetricRankBudget      = 15 * time.Second
	servicesMetricRankTTL         = 60 * time.Second
	servicesMetricRankStatusLabel = "cm_s"
	servicesMetricRankKindLabel   = "cm_k"
)

// servicesMetricRankCols — metrikle sıralanabilen sütunlar (FE METRIC_SORT_COLS).
var servicesMetricRankCols = map[string]bool{"spanCount": true, "errorRate": true, "avg": true, "p99": true}

// serviceMetricRankEntry — bir servisin filo sıralamasındaki yeri.
type serviceMetricRankEntry struct {
	Source   string   `json:"source"`
	Requests float64  `json:"requests"`
	Value    *float64 `json:"value,omitempty"` // sütun değeri; ölçülemiyorsa nil (span grubuna düşer)
}

// servicesMetricRanking — filo sıralaması (önbellekte tutulan biçim).
type servicesMetricRanking struct {
	Services map[string]serviceMetricRankEntry `json:"services"`
	Queries  int                               `json:"queries"`
}

type servicesMetricRankResponse struct {
	Services     []chstore.ServiceSummary `json:"services"`
	HasMore      bool                     `json:"hasMore"`
	Offset       int                      `json:"offset"`
	Limit        int                      `json:"limit"`
	Total        *int                     `json:"total,omitempty"`
	Ranked       bool                     `json:"ranked"`       // true = filo geneli metrik sırası
	MetricRanked int                      `json:"metricRanked"` // evrende metrik değeriyle sıralanan servis sayısı
	WindowS      int                      `json:"windowSec"`
	Queries      int                      `json:"queries"`
	Notes        []string                 `json:"notes,omitempty"`
	MetricOnly   int                      `json:"metricOnly,omitempty"` // v0.10.2451 — listeye eklenen metrik-yalnız servis sayısı
}

// servicesMetricRankKey — SAF: filo sıralaması önbellek anahtarı; TÜM girdiler.
func servicesMetricRankKey(col, env, cluster string, from, to time.Time, lm vmetrics.LabelMap, showInfra bool) string {
	return "svc-metric-rank:v1:" + fnvStr(col, env, cluster, cacheBucket(from, to), lm.Service, lm.Env, lm.Cluster, fmt.Sprint(showInfra))
}

// servicesMetricRankEligible — SAF: filo sıralaması denenir mi; değilse neden.
func servicesMetricRankEligible(col string, win time.Duration) (bool, string) {
	if !servicesMetricRankCols[col] {
		return false, ""
	}
	if win > servicesMetricRankMaxWindow {
		return false, "pencere 24 saati aşıyor — filo geneli metrik sırası yapılmadı, sıra örneklenmiş span değerinden"
	}
	return true, ""
}

// servicesMetricRankQuery — SAF: ailenin TEK filo geneli anlık sorgusu.
// Sayı kısmı servis × durum sınıfı (cm_s: "" yok / ok / e hata); ilk durum
// etiketi kazanır (label_replace ters sırada uygulanır — servicesREDMetricFold
// ile aynı kural). avg / p99 sütununda cm_k etiketli gecikme serisi `or` ile.
func servicesMetricRankQuery(f redFamily, svcLabel, envMatcher string, winS int, col string, showInfra bool) string {
	count, p99, avg := servicesREDMetricQueries(f, svcLabel, nil, envMatcher, winS, showInfra)
	c := count
	for i := len(f.StatusLabels) - 1; i >= 0; i-- {
		l := vmetrics.PromStrTS(f.StatusLabels[i])
		st := vmetrics.PromStrTS(servicesMetricRankStatusLabel)
		c = "label_replace(label_replace(" + c + ", " + st + `, "ok", ` + l + `, ".+"), ` + st + `, "e", ` + l + ", " + vmetrics.PromStrTS(f.ServerErr) + ")"
	}
	c = "sum by (" + svcLabel + ", " + servicesMetricRankStatusLabel + ") (" + c + ")"
	tag := func(q, k string) string {
		return "label_replace(" + q + ", " + vmetrics.PromStrTS(servicesMetricRankKindLabel) + ", " + vmetrics.PromStrTS(k) + `, "", ".*")`
	}
	switch col {
	case "p99":
		return c + " or " + tag(p99, "p")
	case "avg":
		return c + " or " + tag(avg, "a")
	}
	return c
}

// servicesMetricRankFold — SAF: bir ailenin filo sonucu → servis girdileri
// (yalnız istek > 0). Hata oranı yalnız durum sınıfı taşıyan seri varsa
// (servicesREDMetricFold ile aynı tanım: hata / TÜM istek, %).
func servicesMetricRankFold(f redFamily, svcLabel, col string, series []chstore.SpanMetricSeries) map[string]serviceMetricRankEntry {
	type acc struct {
		total, errs float64
		status      bool
		lat         *float64
	}
	per := map[string]*acc{}
	get := func(svc string) *acc {
		a := per[svc]
		if a == nil {
			a = &acc{}
			per[svc] = a
		}
		return a
	}
	for _, sr := range series {
		svc, ok := briefingGroupKey(sr.GroupKey, svcLabel)
		v := lastFinite(sr)
		if !ok || svc == "" || v == nil {
			continue
		}
		if k, ok := briefingGroupKey(sr.GroupKey, servicesMetricRankKindLabel); ok && k != "" {
			get(svc).lat = v
			continue
		}
		a := get(svc)
		a.total += *v
		switch cls, _ := briefingGroupKey(sr.GroupKey, servicesMetricRankStatusLabel); cls {
		case "e":
			a.status = true
			a.errs += *v
		case "ok":
			a.status = true
		}
	}
	out := map[string]serviceMetricRankEntry{}
	for svc, a := range per {
		if a.total <= 0 {
			continue
		}
		e := serviceMetricRankEntry{Source: f.ID, Requests: a.total}
		switch col {
		case "spanCount":
			v := a.total
			e.Value = &v
		case "errorRate":
			if a.status {
				v := math.Min(100, 100*a.errs/a.total)
				e.Value = &v
			}
		default:
			e.Value = a.lat
		}
		out[svc] = e
	}
	return out
}

// servicesMetricRankAssign — SAF: aile sonuçlarını katalog sırasıyla birleştirir;
// servis İLK istek veren ailede kalır (aileler toplanmaz).
func servicesMetricRankAssign(dst, fam map[string]serviceMetricRankEntry) {
	for svc, e := range fam {
		if _, taken := dst[svc]; !taken {
			dst[svc] = e
		}
	}
}

// servicesMetricRankMerge — SAF: evren (span sırasıyla) + filo sıralaması →
// birleşik sıra. Metrik değeri olanlar önde, değerine göre (dir); kalanlar
// evren sırasıyla. Eşitlikte evren sırası (kararlı). metricN = ilk grup boyu.
func servicesMetricRankMerge(universe []chstore.ServiceSummary, rank map[string]serviceMetricRankEntry, dir string) (out []chstore.ServiceSummary, metricN int) {
	type keyed struct {
		i int
		v float64
	}
	var withMetric []keyed
	var rest []int
	for i, s := range universe {
		if e, ok := rank[s.Name]; ok && e.Value != nil && !math.IsNaN(*e.Value) && !math.IsInf(*e.Value, 0) {
			withMetric = append(withMetric, keyed{i, *e.Value})
		} else {
			rest = append(rest, i)
		}
	}
	asc := strings.EqualFold(dir, "asc")
	sort.SliceStable(withMetric, func(a, b int) bool {
		if withMetric[a].v == withMetric[b].v {
			return withMetric[a].i < withMetric[b].i
		}
		if asc {
			return withMetric[a].v < withMetric[b].v
		}
		return withMetric[a].v > withMetric[b].v
	})
	out = make([]chstore.ServiceSummary, 0, len(universe))
	for _, k := range withMetric {
		out = append(out, universe[k.i])
	}
	for _, i := range rest {
		out = append(out, universe[i])
	}
	return out, len(withMetric)
}

// servicesMetricRankPage — SAF: birleşik sıradan sayfa + hasMore.
func servicesMetricRankPage(rows []chstore.ServiceSummary, offset, limit int) ([]chstore.ServiceSummary, bool) {
	if offset >= len(rows) {
		return []chstore.ServiceSummary{}, false
	}
	end := offset + limit
	if end >= len(rows) {
		return rows[offset:], false
	}
	return rows[offset:end], true
}

// errServicesMetricRankPartial — bir aile okunamadı: kısmi sıra yapılmaz.
var errServicesMetricRankPartial = errors.New("metric rank partial")

// buildServicesMetricRanking — filo sıralaması (dosya başı, adım 2). Herhangi
// bir ailenin okuma hatası → hata (kısmi sıra yok); notlar nedeni söyler.
func (s *Server) buildServicesMetricRanking(ctx context.Context, rd servicesREDMetricReader, col, env, cluster string, from, to time.Time, showInfra bool) (servicesMetricRanking, []string, error) {
	winS := servicesMetricRankWinS(from, to)
	out := servicesMetricRanking{Services: map[string]serviceMetricRankEntry{}}
	lm := rd.MetricLabelMap().Normalized()
	svcLabel := redFamilySvcLabel(lm)
	terms := httpEnvScopeTerms(env, cluster)
	ctx, cancel := context.WithTimeout(ctx, servicesMetricRankBudget)
	defer cancel()
	type job struct {
		f   redFamily
		q   string
		res []chstore.SpanMetricSeries
		err error
	}
	var jobs []*job
	for _, f := range redFamilies {
		envMatcher := ""
		if len(terms) > 0 {
			sc, err := s.resolveMetricEnvScope(ctx, rd, f.Probe, terms, to)
			switch {
			case err != nil:
				return out, []string{f.ID + ": ortam etiketi okunamadı (" + string(sourcestate.Classify(err)) + ")"}, errServicesMetricRankPartial
			case !sc.Applied || sc.Matcher == "":
				continue // bu ailede istenen ortam yok
			}
			envMatcher = sc.Matcher
		}
		jobs = append(jobs, &job{f: f, q: servicesMetricRankQuery(f, svcLabel, envMatcher, winS, col, showInfra)})
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j *job) {
			defer wg.Done()
			qctx, qcancel := context.WithTimeout(ctx, servicesMetricRankQueryCap)
			defer qcancel()
			j.res, j.err = rd.QueryPromQLInstant(qctx, j.q, to)
		}(j)
	}
	wg.Wait()
	out.Queries = len(jobs)
	var notes []string
	for _, j := range jobs {
		if j.err != nil {
			notes = append(notes, j.f.ID+": filo sırası okunamadı ("+string(sourcestate.Classify(j.err))+")")
		}
	}
	if len(notes) > 0 {
		return out, notes, errServicesMetricRankPartial
	}
	for _, j := range jobs { // katalog sırası = öncelik
		servicesMetricRankAssign(out.Services, servicesMetricRankFold(j.f, svcLabel, col, j.res))
	}
	return out, nil, nil
}

// servicesMetricRankWinS — /services-red-metric ile aynı pencere kıskacı.
func servicesMetricRankWinS(from, to time.Time) int {
	win := to.Sub(from)
	win = max(win, servicesRedMetricMinWindow)
	win = min(win, servicesRedMetricMaxWindow)
	return int(win / time.Second)
}

// servicesMetricRankingCached — 60 sn önbellek + singleflight (sayfa / süzgeç
// farklı istekler aynı filo sırasını paylaşır). Hata önbelleğe yazılmaz.
func (s *Server) servicesMetricRankingCached(ctx context.Context, vm servicesREDMetricReader, col, env, cluster string, from, to time.Time, showInfra bool) (servicesMetricRanking, []string, error) {
	key := servicesMetricRankKey(col, env, cluster, from, to, vm.MetricLabelMap().Normalized(), showInfra)
	var cached servicesMetricRanking
	if s.metricChartCacheGet(ctx, key, &cached) && cached.Services != nil {
		return cached, nil, nil
	}
	type result struct {
		r     servicesMetricRanking
		notes []string
	}
	v, err, _ := s.sf.Do(key, func() (any, error) {
		r, notes, err := s.buildServicesMetricRanking(context.WithoutCancel(ctx), vm, col, env, cluster, from, to, showInfra)
		if err == nil {
			s.metricChartCacheSet(ctx, key, r, servicesMetricRankTTL)
		}
		return result{r, notes}, err
	})
	res, _ := v.(result)
	return res.r, res.notes, err
}

// getServicesMetricRank — GET /api/services-metric-rank (dosya başı).
func (s *Server) getServicesMetricRank(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := parseDuration(q.Get("since"), 24*time.Hour)
	from, to := parseTime(q.Get("from")), parseTime(q.Get("to"))
	if from.IsZero() {
		from = time.Now().Add(-since)
	}
	if to.IsZero() {
		to = time.Now()
	}
	limit := min(max(parseInt(q.Get("limit"), 50), 1), 500)
	offset := max(parseInt(q.Get("offset"), 0), 0)
	nameMatch := strings.TrimSpace(q.Get("name"))
	col, dir := strings.TrimSpace(q.Get("sort")), strings.TrimSpace(q.Get("dir"))
	ownerTeam, sreTeam := strings.TrimSpace(q.Get("ownerTeam")), strings.TrimSpace(q.Get("sreTeam"))
	cluster, env := strings.TrimSpace(q.Get("cluster")), strings.TrimSpace(q.Get("env"))
	namespace := strings.TrimSpace(q.Get("namespace"))
	s.notePageWarmEnv(r, env) // v0.10.2469 — env varyantı ısıtma adayı (page_warm.go)
	if len(env) > httpEnvScopeTermMax || len(cluster) > httpEnvScopeTermMax {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("env/cluster en çok %d karakter", httpEnvScopeTermMax))
		return
	}
	display := chstore.ServiceDisplayFilters{
		ErrorsOnly: q.Get("errorsOnly") == "1" || q.Get("errorsOnly") == "true",
		MinSpans:   uint64(max(parseInt(q.Get("minSpans"), 0), 0)),
		MinP99Ms:   parseFloat(strings.TrimSpace(q.Get("minP99"))),
	}
	compare := q.Get("compare") == "prior"
	showInfra := q.Get("infra") == "1"
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	vm, isVM := src.(vmMetricSource)
	lmKey := ""
	if isVM {
		lm := vm.MetricLabelMap().Normalized()
		lmKey = lm.Service + "\x00" + lm.Env + "\x00" + lm.Cluster
	}
	useMV := servicesUseMV(to.Sub(from), cluster, env, (cluster != "" || env != "") && s.store.EnvSummaryCovers(r.Context(), from))
	key := "svc-metric-rank-page:v1:" + fnvStr(fmt.Sprint(useMV, limit, offset, isVM, compare, showInfra, display.ErrorsOnly, display.MinSpans, display.MinP99Ms),
		cacheBucket(from, to), nameMatch, col, dir, ownerTeam, sreTeam, cluster, env, namespace, lmKey)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		resp := servicesMetricRankResponse{Offset: offset, Limit: limit, WindowS: servicesMetricRankWinS(from, to), Services: []chstore.ServiceSummary{}}
		serviceIn, filtered, ferr := s.servicesFilterAllowlist(ctx, from, to, cluster, ownerTeam, sreTeam, namespace)
		if ferr != nil {
			return nil, ferr
		} else if filtered && len(serviceIn) == 0 {
			return resp, nil
		}
		fetch := func(lim, off int) ([]chstore.ServiceSummary, error) {
			if useMV {
				return s.store.GetServicesEnvAggFiltered(ctx, from, to, nameMatch, serviceIn, col, dir, lim, off, display, cluster, env)
			}
			return s.store.GetServicesQuery(ctx, chstore.ServicesQuery{
				Since: since, From: from, To: to, NameMatch: nameMatch, ServiceIn: serviceIn,
				Sort: col, Dir: dir, Limit: lim, Offset: off, Cluster: cluster, Env: env, Display: display,
			})
		}
		var page []chstore.ServiceSummary
		ok, why := servicesMetricRankEligible(col, to.Sub(from))
		if !isVM {
			ok, why = false, "metrik kaynağı VictoriaMetrics değil — sıra örneklenmiş span değerinden"
		}
		if ok {
			universe, err := fetch(servicesMetricRankUniverseMax+1, 0)
			if err != nil {
				return nil, err
			}
			switch {
			case len(universe) > servicesMetricRankUniverseMax:
				ok, why = false, fmt.Sprintf("%d'den fazla servis eşleşiyor — filo geneli metrik sırası yapılmadı, sıra örneklenmiş span değerinden", servicesMetricRankUniverseMax)
			default:
				ranking, notes, rerr := s.servicesMetricRankingCached(ctx, vm, col, env, cluster, from, to, showInfra)
				resp.Queries = ranking.Queries
				ordered := universe
				if rerr != nil {
					resp.Notes = append(notes, "filo geneli metrik sırası okunamadı — sıra örneklenmiş span değerinden")
				} else {
					// v0.10.2451 — span'i olmayan metrik servisleri (services_metric_only.go).
					extra := servicesMetricOnlyRows(universe, ranking.Services, servicesMetricOnlyFilter{
						NameMatch: nameMatch, Allow: servicesMetricOnlyAllow(serviceIn, filtered),
						SpanDisplay: display.ErrorsOnly || display.MinSpans > 0 || display.MinP99Ms > 0,
					})
					all, cut := servicesWithMetricOnly(universe, extra, servicesMetricRankUniverseMax)
					if cut > 0 {
						resp.Notes = append(resp.Notes, fmt.Sprintf("%d metrik-yalnız servis %d servis tavanını aştığı için listelenmedi", cut, servicesMetricRankUniverseMax))
					}
					resp.MetricOnly = len(extra) - cut
					ordered, resp.MetricRanked = servicesMetricRankMerge(all, ranking.Services, dir)
					resp.Ranked = true
				}
				total := len(ordered)
				resp.Total = &total
				page, resp.HasMore = servicesMetricRankPage(ordered, offset, limit)
			}
		}
		if !ok {
			if why != "" {
				resp.Notes = append(resp.Notes, why)
			}
			rows, err := fetch(limit+1, offset)
			if err != nil {
				return nil, err
			}
			page, resp.HasMore = rows, len(rows) > limit
			if resp.HasMore {
				page = rows[:limit]
			}
			if useMV {
				if total, terr := s.store.CountServicesEnvAgg(ctx, from, to, nameMatch, serviceIn, cluster, env); terr == nil {
					resp.Total = &total
				}
			}
		}
		page = append([]chstore.ServiceSummary(nil), page...) // önbellekteki evreni değiştirme
		s.enrichServicesMetricRankPage(ctx, page, compare, useMV, since, from, to, cluster, env, namespace)
		resp.Services = page
		return resp, nil
	})
}

// enrichServicesMetricRankPage — /api/services'in sayfa zenginleştirmesi
// (önceki pencere, sağlık, yaşam döngüsü, namespace sayısı) aynı sırayla.
func (s *Server) enrichServicesMetricRankPage(ctx context.Context, rows []chstore.ServiceSummary, compare, useMV bool, since time.Duration, from, to time.Time, cluster, env, namespace string) {
	if len(rows) == 0 {
		return
	}
	if compare {
		names := make([]string, len(rows))
		for i := range rows {
			names[i] = rows[i].Name
		}
		pfrom, pto := servicesPriorWindow(from, to, useMV)
		var prior []chstore.ServiceSummary
		var err error
		if useMV {
			prior, err = s.store.GetServicesAggFiltered2(ctx, pfrom, pto, "", names, "spanCount", "desc", len(names), 0, chstore.ServiceDisplayFilters{})
		} else {
			prior, err = s.store.GetServicesQuery(ctx, chstore.ServicesQuery{Since: since, From: pfrom, To: pto, ServiceIn: names, Limit: len(names), Cluster: cluster, Env: env})
		}
		if err == nil {
			mergePriorServices(rows, prior)
		}
	}
	if counts, err := s.openProblemCountsCached(ctx); err == nil {
		for i := range rows {
			c := counts[rows[i].Name]
			rows[i].OpenProblems = c.Critical + c.Warning + c.Info
			rows[i].Health, rows[i].HealthReason = problemHealth(c)
		}
	}
	s.applyServiceSeen(ctx, rows)
	s.applyServiceNamespaceCounts(ctx, rows, namespace, from, to)
}
