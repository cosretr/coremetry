package api

// cache_raw_query.go — v0.10.256 (perf profili §7 madde 2, C2 ⭐ + C4).
//
// Beş uç (`traces`, `traces-count`, `traces-agg`, `span-metric`, `exc-groups`)
// anahtarı `r.URL.RawQuery` ile kuruyordu. SPA her poll'da `to=Date.now()`
// (ns) gönderdiği için anahtar her istekte değişiyor, cache fiilen MISS —
// çok pod'da N× CH taraması. Ayrıca `?refresh=1` anahtara giriyordu (C4):
// BYPASS yazımı kimsenin okumadığı bir anahtara gidiyordu.
//
// cacheRawQuery: from/to (unix ns) 30 s grid'e (cacheBucket ile aynı
// basamak) yuvarlanır, `refresh` düşer, kalan parametreler ANAHTAR SIRALI
// yeniden kodlanır (url.Values.Encode). Yükleyici gerçek pencereyi kullanır;
// aynı grid'deki ikinci istek ilkinin gövdesini alır (≤30 s kayma — diğer
// uçların cacheBucket sözleşmesi). SAF; cache_key_test.go pinler.

import (
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const cacheRawQueryGrid = cacheKeyGrid

// cacheRawQueryString — sorgu dizesini anahtar için normalize eder.
func cacheRawQueryString(raw string) string { return cacheRawQueryStringAt(raw, time.Now()) }

// cacheRawQueryStringAt — SAF çekirdek. v0.10.2228 (Redis R2, O1): `to`
// canlıysa (cache_live_key.go) to="live", from=<süre> — 30 sn'de bir yeni
// anahtar yerine aynı anahtar, SWR devrede. Diğer parametreler aynen.
func cacheRawQueryStringAt(raw string, now time.Time) string {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	q.Del("refresh")
	if to, ok := parseNsParam(q.Get("to")); ok {
		from, fok := parseNsParam(q.Get("from"))
		if !fok {
			from = to.Add(-time.Nanosecond) // from yok: süre parçası yalnız "to canlı" der
		}
		if dur, live := cacheLiveWindow(from, to, now); live {
			q.Set("to", "live")
			if fok {
				q.Set("from", dur)
			}
			return q.Encode()
		}
	}
	for _, k := range []string{"from", "to"} {
		v := q.Get(k)
		if v == "" {
			continue
		}
		ns, err := strconv.ParseInt(v, 10, 64)
		if err != nil || ns <= 0 {
			continue
		}
		q.Set(k, strconv.FormatInt(time.Unix(0, ns).Truncate(cacheRawQueryGrid).UnixNano(), 10))
	}
	return q.Encode()
}

// cacheRawQuery — istek için anahtar parçası.
func cacheRawQuery(r *http.Request) string { return cacheRawQueryString(r.URL.RawQuery) }

// parseNsParam — pozitif unix-ns parametresi.
func parseNsParam(v string) (time.Time, bool) {
	ns, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ns <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, ns), true
}
