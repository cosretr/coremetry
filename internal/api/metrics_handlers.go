package api

// metrics_handlers.go — v0.10.1835 (Metrics Explorer Faz 1, audit
// docs/metrics/metrics-explore-dashboards-audit.md §7 dilim 1.1).
//
// The six /api/metrics/* read handlers moved here VERBATIM from api.go
// (TestApiGoDoesNotGrow: api.go only shrinks; the baseline went down in the
// same commit). They moved rather than staying put because every Faz 1
// slice edits them — the meta envelope, server-side top-N, cluster scope —
// and each of those edits would otherwise have grown api.go. The routes
// moved with them into metrics_routes.go so the /api/metrics family is no
// longer registered from two places.
//
// What stayed the same on purpose: every cache key, every body field, every
// status code. The source-pin tests that scanned api.go for these handlers
// (metricsource_test.go, metricsource_param_test.go, metricsource_faz2_test.go)
// now scan api.go + this file through metricHandlerSources().

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// getMetricNames now accepts q + limit + offset (v0.5.181) for
// the MetricNamePicker debounced search. Legacy callers that
// pass no params get the unlimited behaviour the response used
// to have. New shape is {names, total, hasMore}; the response
// type is conditional on the presence of `q` so older clients
// (just calling /api/metrics/names?service=X) still receive a
// plain MetricInfo[] body — no breaking change for the existing
// /metrics page until it migrates to the new picker.
// v0.9.1150 — okuma artık metrik kaynak seam'inden geçiyor (CH ya da
// VictoriaMetrics) ve `src` cache anahtarının parçası. Backend işareti
// anahtarda OLMASAYDI operatör Settings'ten VM'yi açtığı an, TTL boyunca
// (60s / 30s) eski CH gövdeleri servis edilirdi ve tazelemeyi farklı
// anlarda yapan iki pod aynı katalogda farklı liste gösterirdi — v0.5.187
// çapraz-zehirlenme sınıfının aynısı, bu kez girdi bir AYAR.
//
// Zarf da `source` taşıyor: katalog sayfası rozeti bunu okur, ayrı bir
// /api/settings çağrısı YAPMAZ (viewer o ucu göremez, ve iki istek
// arasında ayar değişirse rozet gövdeye yalan söylerdi).
func (s *Server) getMetricNames(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	svc := q.Get("service")
	pattern := strings.TrimSpace(q.Get("q"))
	limit := parseInt(q.Get("limit"), 0)
	offset := parseInt(q.Get("offset"), 0)
	// v0.9.1151 — ?metricsrc=vm|ch bu İSTEK için backend'i seçer (deneme
	// modu, metricsource.go). Param yoksa davranış bayt-bayt v0.9.1150:
	// karar Settings'ten çıkar. Çözümleme cache anahtarından ÖNCE, çünkü
	// dönen kaynak hem anahtarı hem okumayı besliyor.
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Old shape — no pagination params → return MetricInfo[]
	// like pre-v0.5.181 callers expect.
	//
	// v0.9.833 — cache anahtarları `:v2`. MetricInfo zarfı büyüdü
	// (lastSeenNs + serviceCount); sürüm eklemeden deploy edilirse
	// Redis'teki eski gövdeler TTL boyunca (60s / 30s) yeni alanları
	// EKSİK döner ve arayüz "Son veri: —" gösterip sanki metrik
	// susmuş gibi yalan söyler. Zarf değişti = anahtar değişti.
	drop, dropTag := s.metricNameDrop() // v0.10.1809 — pipeline drop kuralı okuma tarafı (metric_name_drop.go)
	// v0.10.1837 — admin + açık includeHidden=1: gizli adlar süzülmez,
	// hiddenByRule ile işaretlenir (metric_names_meta.go). Başka her rolde
	// param YOK sayılır → anahtar çatallanmaz, gizli ad dönmez.
	pageDrop, hiddenMark, ihTag := namesDropPlan(drop, isAdminRequest(r) && q.Get("includeHidden") == "1")
	if pattern == "" && limit == 0 && offset == 0 {
		s.serveCached(w, r, "metric-names:v3:src="+src.Name()+":svc="+svc+dropTag+ihTag, 60*time.Second, func(ctx context.Context) (any, error) {
			// GetMetricNames, ListMetricNames(svc, "", 0, 0)'ın ta kendisi
			// (repo.go:4342) — seam'de ayrı bir metot tutmamak için doğrudan
			// o çağrı yapılıyor. CH tarafında SQL bayt-bayt aynı.
			names, _, err := src.ListMetricNames(ctx, svc, "", 0, 0)
			if err != nil {
				return nil, err
			}
			return s.ladderRows(ctx, src, filterDroppedMetricNames(names, pageDrop), hiddenMark), nil
		})
		return
	}
	if limit > 1000 {
		limit = 1000
	}
	key := fmt.Sprintf("metric-names:v3:src=%s:svc=%s:q=%s:limit=%d:offset=%d%s%s",
		src.Name(), svc, pattern, limit, offset, dropTag, ihTag)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		names, total, err := src.ListMetricNames(ctx, svc, pattern, limit, offset)
		if err != nil {
			return nil, err
		}
		page := metricNamesPage(names, total, offset, src.Name(), pageDrop) // zarf + kısa-sayfa notu: metric_name_drop.go
		if kept, ok := page["names"].([]chstore.MetricInfo); ok {
			page["names"] = s.ladderRows(ctx, src, kept, hiddenMark) // v0.10.1837 — metadata merdiveni
		}
		return page, nil
	})
}

// getMetrics — v0.8.456: serveCached'e alındı (kardeş queryMetric'in
// dakika-bucket'lı anahtar deseni). Cache'siz her izleyici/poll raw
// metric_points taraması koşuyordu; 30s bayatlık operatörce görünmez.
func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name, svc := q.Get("name"), q.Get("service")
	from, to := parseTime(q.Get("from")), parseTime(q.Get("to"))
	limit := parseInt(q.Get("limit"), 500)
	// v0.9.464 (dürüstlük A12) — zarf {points, truncated}: anahtar v2.
	key := fmt.Sprintf("metric-points:v2:name=%s:svc=%s:limit=%d:from=%d:to=%d",
		name, svc, limit, from.Unix()/60, to.Unix()/60)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		points, truncated, err := s.store.GetMetricPoints(ctx, name, svc, from, to, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"points": points, "truncated": truncated}, nil
	})
}

// ── Metric query (multi-series, attribute filters, group-by) ─────────────────

// queryMetric backs the Grafana-style MetricQueryEditor / MetricsExplorer
// (one fetch per enabled query row, on every debounced keystroke + poll).
// Cached 30s (v0.8.4 — was an uncached raw metric_points GROUP BY scan per
// viewer/keystroke/tab, breaking the /api/* p99 budget under fan-out). Key
// carries every result-affecting input; the window is bucketed to the minute
// so concurrent polls + Dashboards MQE panels share a round-trip, and the raw
// groupBy + filter strings go in verbatim (no length-only collapse, cf.
// v0.5.187). Mirrors getMetricHistogram.
func (s *Server) queryMetric(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	step, _ := strconv.Atoi(q.Get("step"))
	// v0.9.105 (F1) — panel px width ≈ target bucket count; drives pixel-
	// adaptive step when step is auto. Bounded [0,4000] so a rogue value can't
	// blow up the bucket count / cache-key cardinality.
	maxDP, _ := strconv.Atoi(q.Get("maxDataPoints"))
	if maxDP < 0 {
		maxDP = 0
	} else if maxDP > 4000 {
		maxDP = 4000
	}
	name := q.Get("name")
	// v0.9.1152 — isim yokluğu İSTEMCİ hatasıdır: store katmanının
	// "metric name required" hatası VM yolunda errUpstream sarmalına
	// girip 502 görünüyordu (1150 canlı doğrulama bulgusu) — operatörü
	// sağlıklı VM'yi kontrol etmeye gönderir. Kaynağa hiç inmeden 400.
	// v0.9.1155 — gövde JSON zarfına geçti: bu yüzeydeki diğer hatalar
	// (çevrilemeyen agg 400'ü, VM-down 502'si) writeErr'in {"error":…}
	// zarfını kullanıyor; düz metin, hata gövdesini JSON parse eden
	// istemciyi boğuyordu (1153 doğrulama notu).
	if strings.TrimSpace(name) == "" {
		writeErr(w, fmt.Errorf("%w: name (metrik adı) parametresi zorunlu", errBadRequest))
		return
	}
	svc := q.Get("service")
	agg := q.Get("agg")
	groupByRaw := q.Get("groupBy")
	filtersRaw := q.Get("filters")
	// v0.9.279 — DB receiver drill scoping. Not expressible as an ordinary
	// filter: the receivers disagree about where the instance identity lives,
	// so it compiles to a per-engine OR (dbInstanceScopeClause). Both go in
	// the cache key — without them one instance's chart would be served for
	// another's request, the cross-poisoning class CLAUDE.md calls out.
	inst := strings.TrimSpace(q.Get("instance"))
	engine := strings.TrimSpace(q.Get("engine"))
	from := parseTime(q.Get("from"))
	to := parseTime(q.Get("to"))
	// v0.9.458 (dürüstlük A1) — zarf {series, rowsCapped}: anahtar v2
	// (gövde şekli değişti, v0.9.443 dersi).
	// v0.9.776 → v3: histogram+delta agg=avg artık gözlem-ağırlıklı gerçek
	// ortalama (sum(sum_value)/sum(count)). Gövde ŞEKLİ aynı ama SAYILAR
	// değişti — anahtar bumplanmazsa rolling deploy'da 30 sn boyunca eski
	// (ortalamaların ortalaması) değerler servis edilirdi ve iki pod aynı
	// panelde farklı sayı gösterirdi (v0.9.443/458 dersi).
	// v0.9.797 — DIŞLAMA SETİNİN ÖZETİ ANAHTARDA. Kural seti sorgunun
	// SONUCUNU değiştiriyor (WHERE'e NOT match ekleniyor, rollup kademesi
	// kapanıyor); anahtarda olmasaydı kural eklenir eklenmez 30 sn boyunca
	// filtresiz sonuç servis edilir, ve yenilemeyi farklı anlarda yapan
	// iki pod aynı panelde farklı sayı gösterirdi. Özet SIRADAN BAĞIMSIZ
	// FNV (v0.5.187 kuralı: len() değil, tüm girdiler); kural yokken sabit
	// "0" — kuralsız kurulumda anahtar kardinalitesi artmaz.
	// v0.9.1150 — `src` (ch|vm) anahtarın parçası. Bu uç aynı gövde
	// şeklini İKİ farklı store'dan üretebiliyor; işaret olmadan Settings
	// toggle'ı 30 saniye boyunca eski backend'in SAYILARINI servis eder.
	// Şekil aynı, sayılar farklı — v0.9.776 anahtar-bump'ının tam olarak
	// bu gerekçesi.
	// v0.9.1151 — ?metricsrc= deneme modu: seçim istek başına olabilir.
	src, srcErr := s.metricSourceFor(r)
	if srcErr != nil {
		writeErr(w, srcErr)
		return
	}
	// v0.10.1838 — sunucu tarafı top-N (metric_topn.go). Param yoksa kapalı:
	// anahtar ve gövde bayt-aynı.
	topn, tnErr := parseTopNQuery(q)
	if tnErr != nil {
		writeErr(w, tnErr)
		return
	}
	// v0.9.1157 → v4: zarfa `note` eklendi (VM yolunda BOŞ dönen bir
	// yüzdeliğin SEBEBİ). Alan `omitempty` — CH gövdesi bayt-bayt eski —
	// ama anahtar yine de bumplanır: rolling deploy'da eski pod'un
	// yazdığı notsuz gövde 30 sn boyunca servis edilirse operatör tam da
	// bu özelliğin engellemek için var olduğu şeyi görür, sessiz boş
	// grafiği (v0.9.443/458 dersi).
	// v0.9.1159 — metricNameRuleTag: VM'de ADIN çözümlendiği seri değişti
	// (aday alternation'ı), istek ise bayt-aynı. Damga YALNIZ VM anahtarına
	// giriyor; CH gövdesi etkilenmediği için onun anahtarı da değişmiyor.
	key := fmt.Sprintf("metric-query:v4%s:src=%s:name=%s:svc=%s:agg=%s:step=%d:mdp=%d:gb=%s:f=%s:inst=%s:eng=%s:from=%d:to=%d:mx=%s",
		s.metricNameRuleTag(src),
		src.Name(), name, svc, agg, step, maxDP, groupByRaw, filtersRaw, inst, engine, from.Unix()/60, to.Unix()/60,
		s.store.MetricExclusions().Digest()) + topn.keyTag()
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		// queryMetricNoted, kaynağın açıklayabildiği durumda notu da
		// getirir (metricNoteSource); CH kaynağı bu yeteneği taşımıyor ve
		// "" döner — yani bu satır CH davranışını değiştirmez.
		mfilters, ferr := parseFilters(filtersRaw)
		if ferr != nil {
			return nil, ferr
		}
		ctx, mrun := beginMetricMeta(ctx, src) // v0.10.1835 — meta zarfı (metric_meta.go)
		rd, qerr := readMetric(ctx, src, chstore.MetricQueryFilter{
			Name:          name,
			Service:       svc,
			Instance:      inst,
			Engine:        engine,
			Filters:       mfilters,
			GroupBy:       splitNonEmpty(groupByRaw, ','),
			Aggregation:   agg,
			From:          from,
			To:            to,
			StepSeconds:   step,
			MaxDataPoints: maxDP,
		}, topn)
		if qerr != nil {
			return nil, qerr
		}
		series, note := rd.Series, rd.Note
		capped := rd.Capped
		meta := mrun.finish(len(series), chMetaHints{Truncated: capped, StepSec: observedStepSec(series)}, note)
		applyTopNMeta(meta, topn, rd.Total, capped)
		out := map[string]any{
			"series":     series,
			"rowsCapped": capped || chstore.SeriesRowsCapped(series), // v0.10.1838 — kesimden ÖNCEKİ tavan da sayılır
			// v0.10.1835 — EK alan; eski alanlar bayt-aynı (golden test).
			"meta": meta,
		}
		// Yalnız DOLU notta alan eklenir: boş bir "note":"" her gövdeyi
		// büyütür ve arayüzde "not var mı" kontrolünü değersizleştirir.
		if note != "" {
			out["note"] = note
		}
		return out, nil
	})
}

// getMetricHistogram renders an explicit-histogram metric as a time×bucket
// heatmap + per-bucket p50/p95/p99 (v0.6.56). Cached 30s; the key carries
// every result-affecting input — the window is bucketed to the minute so
// concurrent polls share a round-trip, and the full filter string is in the
// key (no length-only collapse, cf. v0.5.187).
//
// v0.9.1157 (VM Faz 2) — SEAM'e girdi. ClickHouse yolu aynı
// MetricQueryFilter'ı aynı store metoduna veriyor (chMetricSource sadece
// delegasyon), yani SQL ve gövde şekli değişmedi; değişen tek şey, VM
// açıkken/`?metricsrc=vm` ile bu ucun `sum by (le) (increase(<ad>_bucket))`
// üzerinden aynı zarfı kurabilmesi. Uçlardan biri seam'de kalıp öteki
// kalmasaydı, aynı sayfadaki çizgi grafiği VM'den, ısı haritası
// ClickHouse'tan okuyacaktı — iki store, tek panel.
func (s *Server) getMetricHistogram(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	step, _ := strconv.Atoi(q.Get("step"))
	name := q.Get("name")
	svc := q.Get("service")
	filtersRaw := q.Get("filters")
	from := parseTime(q.Get("from"))
	to := parseTime(q.Get("to"))
	// v0.9.1157 — isim yokluğu İSTEMCİ hatasıdır (kardeş queryMetric'in
	// v0.9.1152 düzeltmesinin ta kendisi): store katmanının "metric name
	// required" hatası VM yolunda errUpstream sarmalına girip 502
	// görünürdü ve operatörü sapasağlam bir VictoriaMetrics'i kontrol
	// etmeye yollardı. CH yolunda bu 500 → 400 demek; bilinçli, çünkü
	// eksik zorunlu parametre hiçbir yolda sunucu arızası değil.
	if strings.TrimSpace(name) == "" {
		writeErr(w, fmt.Errorf("%w: name (metrik adı) parametresi zorunlu", errBadRequest))
		return
	}
	// v0.9.1157 — src anahtarda (bkz. getMetricNames'in gerekçesi):
	// gövde ŞEKLİ iki store'da aynı, SAYILAR farklı. İşaret olmadan
	// Settings toggle'ı ya da `?metricsrc=` denemesi 30 sn boyunca öteki
	// backend'in ısı haritasını servis eder (v0.5.187 sınıfı). Anahtar
	// dizesinin kendisi de değiştiği için eski gövdeler zaten erişilemez —
	// zarfa eklenen `note` alanı için ayrı bir sürüm damgası gerekmiyor.
	src, srcErr := s.metricSourceFor(r)
	if srcErr != nil {
		writeErr(w, srcErr)
		return
	}
	// mx: dışlama seti özeti — ısı haritası da NOT match'li WHERE'den
	// okuyor (metrichist.go), yani aynı çapraz-zehirlenme kapısı burada da
	// açıktı.
	// v0.9.1159 — aday alternation'ı ısı haritasının kova adını da değiştirdi
	// (bkz. metricNameRuleTag); damga yalnız VM tarafına.
	key := fmt.Sprintf("metric-hist%s:src=%s:name=%s:svc=%s:step=%d:f=%s:from=%d:to=%d:mx=%s",
		s.metricNameRuleTag(src),
		src.Name(), name, svc, step, filtersRaw, from.Unix()/60, to.Unix()/60,
		s.store.MetricExclusions().Digest())
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		hfilters, ferr := parseFilters(filtersRaw)
		if ferr != nil {
			return nil, ferr
		}
		ctx, mrun := beginMetricMeta(ctx, src) // v0.10.1835 — meta zarfı (metric_meta.go)
		h, herr := src.QueryMetricHistogram(ctx, chstore.MetricQueryFilter{
			Name:        name,
			Service:     svc,
			Filters:     hfilters,
			From:        from,
			To:          to,
			StepSeconds: step,
		})
		if herr != nil || h == nil {
			return h, herr // nil gövde eskisi gibi `null`
		}
		hstep := 0
		if len(h.Times) > 1 {
			hstep = int((h.Times[1] - h.Times[0]) / int64(time.Second))
		}
		return histogramResponse{HistogramSeries: h,
			Meta: mrun.finish(histogramMetaShown(h), chMetaHints{Truncated: h.RowCapped, StepSec: hstep}, h.Note)}, nil
	})
}

// getMetricLabelValues — v0.8.456: serveCached'e alındı. Her (metric,
// key) çifti izleyici başına 24h DISTINCT metric_points taraması
// koşuyordu (Explore compare 2x fetch atar); etiket setleri yavaş
// değişir, 60s bayatlık görünmez. since anahtarın parçası.
func (s *Server) getMetricLabelValues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := parseDuration(q.Get("since"), 24*time.Hour)
	// v0.9.275 — ceiling on the window. This is a SUGGESTION list for a filter
	// picker, not a measurement: a label value that fell out of the window is
	// one the operator types by hand, it is not a wrong number on a chart.
	// That is what makes narrowing acceptable HERE and not in, say, the audit
	// log (v0.9.261), where the same silence hid records.
	//
	// Clamped in the handler rather than the store so the cache key reflects
	// the window that was actually queried — otherwise `since=30d` and
	// `since=7d` would occupy two keys holding identical bodies.
	if since > 7*24*time.Hour {
		since = 7 * 24 * time.Hour
	}
	metric, lkey := q.Get("metric"), q.Get("key")
	// v0.9.1150 — src anahtarda (bkz. getMetricNames'in gerekçesi); v0.9.1151 — ?metricsrc= deneme modu.
	lq, llimit := labelValuesParams(q) // v0.10.868 — sunucu tarafı alt dize + tavan (anahtarda)
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if s.serveHiddenMetricEmpty(w, metric) { // v0.10.1809 — drop kuralıyla gizli ad: backend'e inmeden []
		return
	}
	// v0.9.1159 — etiket keşfi de aday alternation'ıyla kapsamlanıyor, yani
	// VM'de dönen liste değişti; damga yalnız VM tarafına (CH'nin DISTINCT
	// taraması bu anahtarın var olma sebebi, boşuna ısıtılmaz).
	key := fmt.Sprintf("metric-labels%s:src=%s:m=%s:k=%s:since=%s:q=%s:limit=%d",
		s.metricNameRuleTag(src), src.Name(), metric, lkey, since, lq, llimit)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		return src.MetricLabelValues(ctx, metric, lkey, since, lq, llimit)
	})
}

// getMetricAttrKeys — v0.9.771: the KEY half of the pair above. The store
// method (MetricAttrKeys, v0.9.124) already existed for PromQL `without(L)`
// but was never reachable over HTTP; the PromQL editor's label autocomplete
// (Faz 2) needs it to answer "what can I write inside {}?".
//
// Same posture as getMetricLabelValues: suggestion list, not a measurement —
// 60s staleness is invisible, and the window is clamped in the HANDLER so the
// cache key names the window actually queried (v0.9.275). Key hashes ALL
// inputs (metric, service, since) — v0.5.187.
func (s *Server) getMetricAttrKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := parseDuration(q.Get("since"), 24*time.Hour)
	if since > 7*24*time.Hour {
		since = 7 * 24 * time.Hour
	}
	metric, service := q.Get("metric"), q.Get("service")
	// v0.9.1150 — src anahtarda (bkz. getMetricNames'in gerekçesi).
	// v0.9.1151 — ?metricsrc= deneme modu.
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if s.serveHiddenMetricEmpty(w, metric) { // v0.10.1809 — aynı kapı (metric_name_drop.go)
		return
	}
	// v0.9.1159 — aynı gerekçe getMetricLabelValues'takiyle.
	key := fmt.Sprintf("metric-attr-keys%s:src=%s:m=%s:svc=%s:since=%s",
		s.metricNameRuleTag(src), src.Name(), metric, service, since)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		return src.MetricAttrKeys(ctx, metric, service, since)
	})
}
