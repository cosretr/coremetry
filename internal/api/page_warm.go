package api

// page_warm.go — v0.10.2469 (operatör 2026-11-22): Services, Endpoints ve
// Databases sayfalarının varsayılan görünümü L2 Redis'ten HEP sıcak gelir;
// kullanıcı ilk açılışta soğuk VM / ClickHouse hesabı beklemez.
//
// ÜÇ PARÇA:
//
//  1. KAYIT (pageWarmRegistry) — sayfa başına ilk yüklemede atılan isteklerin
//     varsayılan parametreleri (pencere 3 sa = FE DEFAULT_RANGE_PRESET, sıra,
//     sayfa 0, limit). Isıtıcı anahtarı KENDİSİ kurmaz: isteği sayfanın
//     çağırdığı handler'ın KENDİSİNE verir; anahtar aynı fonksiyondan çıkar.
//     Eski api.go ısıtıcısının üç girdisi tam bu yüzden ölüydü (ölçüldü,
//     2026-11-22): services-landing 15 dk/errorRate/withTotal=false
//     (sayfa 3 sa + spanCount → /services-metric-rank), databases 1 sa
//     (sayfa 3 sa), clusters 24 sa (Services/Endpoints 3 sa) — hepsi kimsenin
//     okumadığı anahtara yazıyordu. Bağımlı istekler (sparkline /
//     red-metric / spark, sayfanın ilk yanıttan sonra attığı) Next ile
//     ilk yanıtın gövdesinden türetilir — FE'nin yaptığı gibi.
//
//  2. ÖNDEN TAZELEME — ısıtıcının isteği ctx'te pageWarmOpts taşır;
//     cachedJSON (cache.go) bunu görünce pageWarmCompute'a döner: L2'deki
//     zarf max(ttl − tik, 10 sn)'den gençse hesap YOK (başka replika ya da
//     kullanıcı yeni tazeledi), değilse singleflight + dağıtık claim (dsfSkip:
//     başka replika hesaplıyorsa atla) altında hesap + L2/L1 yazımı. 30 sn
//     TTL'li anahtar her tikte (25 sn) tazelenir → taze kalma ≤ 30 sn.
//
//  3. UZUN BAYAT UFKU (staleHorizon) — bu ailelerin anahtarları 10 dk'ya
//     kadar bayat servis edilir (+ arka plan tazeleme, cache.go SWR); ısıtıcı
//     düşse ya da Redis'te lider el değiştirse bile kullanıcı bloklanmaz.
//     Diğer aileler eskisi gibi staleFactor × TTL.
//
// ORTAM VARYANTLARI (yalnız Services, bkz. pageWarmPage.Envs): Topbar env
// seçimi isteği env'e daraltır. Son 24 sa'te Services'te İSTENMİŞ env değerleri Redis'te
// izlenir (coremetry:warm:env:<fnv>, 24 sa TTL, pod başına dakikada ≤ 1
// yazım); ısıtıcı en yeni ≤ 10 tanesini alır ve KADEMELİ ısıtır: env i,
// (tik + i) % 4 == 0 olan tikte (~100 sn'de bir; tik başına ≤ 3 env).
// Arada kullanıcı isteği bayat-ama-anında gelir + arka plan tazeleme.
// "Benim takımım" varsayılanı YOK (Services ownerTeam/sreTeam yalnız URL'den).
//
// LİDER: tik başına filo çapında tek SETNX claim (coremetry:warm:pages,
// TTL tik − 1 sn; warmClaim deseni). Redis hatası açık kapı: paylaşılan L2
// yoksa her pod kendi L1'ini ısıtır.
//
// MALİYET (prod ölçeği, DECISIONS 2026-11-22 v0.10.2469): tik başına
// varsayılan görünümler ≈ CH ~9 MV sorgusu + VM ≤ 5 filo + ≤ 15 kapsamlı + ≤ 5
// aralık sorgusu; Services env varyantı tik başına ≤ 3 × (CH 1-2 + VM ≤ 25).
// Gözlem: worker.job.duration{job=page-warm-*} + cache.requests{tier=WARM|
// WARM-FRESH|WARM-PEER} (kullanıcı isabet oranı = HIT-L1+HIT+STALE ÷
// +MISS, WARM katmanları dahil DEĞİL).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

const (
	pageWarmTick       = 25 * time.Second
	pageWarmClaimKey   = "coremetry:warm:pages"
	pageWarmWindow     = 3 * time.Hour // FE DEFAULT_RANGE_PRESET (pin: page_warm_test.go)
	pageWarmMinAge     = 10 * time.Second
	pageWarmBudget     = 20 * time.Second // istek başına (eski ısıtıcının queryBudg'u)
	pageWarmStale      = 10 * time.Minute
	pageWarmEnvMax     = 10
	pageWarmEnvEvery   = 4 // env varyantı her 4. tikte (kademeli)
	pageWarmEnvTTL     = 24 * time.Hour
	pageWarmEnvPrefix  = "coremetry:warm:env:"
	pageWarmEnvNoteGap = time.Minute
	pageWarmEnvSeenCap = 256 // pod başına izlenen ayrık env (bellek sınırı)
)

// pageWarmStaleFamilies — uzun bayat ufkun (pageWarmStale) uygulandığı
// anahtar önekleri. Kayıttaki her isteğin anahtarı bunlardan biriyle başlar
// (TestPageWarmKeysHaveLongStaleHorizon).
var pageWarmStaleFamilies = []string{
	"svc-metric-rank-page:", "services-spark:", "svc-red-metric:", "svc-red-spark:",
	"all-service-runtimes", "clusters:", "namespaces:",
	"endpoints:v2:",
	"databases:v2:", "slow-queries-global:", "db-trends:", "db-problems:",
}

// staleHorizon — SWR'nin bayat servis ufku ve L2 değerinin Redis TTL'i.
func staleHorizon(key string, ttl time.Duration) time.Duration {
	h := ttl * staleFactor
	for _, p := range pageWarmStaleFamilies {
		if strings.HasPrefix(key, p) {
			return max(h, pageWarmStale)
		}
	}
	return h
}

// ── istek bağlamı ────────────────────────────────────────────────────

type pageWarmCtxKey struct{}

// pageWarmOpts — ısıtıcı isteğinin cachedJSON'a mesajı.
type pageWarmOpts struct {
	tick  time.Duration // yaş ≥ max(ttl − tick, pageWarmMinAge) → tazele
	probe *string       // test: anahtarı yakala, hesaplama YOK
}

func withPageWarm(ctx context.Context, o *pageWarmOpts) context.Context {
	return context.WithValue(ctx, pageWarmCtxKey{}, o)
}

func pageWarmFrom(ctx context.Context) *pageWarmOpts {
	o, _ := ctx.Value(pageWarmCtxKey{}).(*pageWarmOpts)
	return o
}

// pageWarmCompute — cachedJSON'un ısıtıcı dalı (dosya başı, madde 2).
func (s *Server) pageWarmCompute(ctx context.Context, pw *pageWarmOpts, key string, ttl time.Duration, fn func(ctx context.Context) (any, error)) ([]byte, string, error) {
	if pw.probe != nil {
		*pw.probe = key
		return []byte("{}"), "PROBE", nil
	}
	raw, gen, ok := s.l2GetGen(ctx, key)
	written, old, eg, envOK := unwrapEnvelopeGen(raw)
	have := ok && envOK && genMatches(gen, eg)
	if have && time.Since(written) < max(ttl-pw.tick, pageWarmMinAge) {
		s.stats.record("WARM-FRESH", key)
		return old, "WARM-FRESH", nil
	}
	body, noStore, _, err := s.computeBodyStore(ctx, key, gen, dsfSkip, ttl, fn)
	if errors.Is(err, errDSFSkipped) && have {
		s.stats.record("WARM-PEER", key) // başka replika hesaplıyor
		return old, "WARM-PEER", nil
	}
	if err != nil {
		return nil, "", err
	}
	if !noStore {
		s.storeCached(ctx, key, body, ttl, gen)
	}
	s.stats.record("WARM", key)
	return body, "WARM", nil
}

// ── kayıt ─────────────────────────────────────────────────────────────

type pageWarmHandler func(*Server, http.ResponseWriter, *http.Request)

// pageWarmReq — sayfanın attığı bir istek (varsayılan parametrelerle).
type pageWarmReq struct {
	Route  string // mux deseni "GET /api/…" (log_comment etiketi)
	Query  url.Values
	Handle pageWarmHandler
	// Next — bu yanıttan türeyen bağımlı istekler (FE'nin ikinci dalgası).
	Next func(body []byte) []pageWarmReq
}

// pageWarmPage — kayıt girdisi. Yeni sayfa = yeni girdi (+ ailesi
// pageWarmStaleFamilies'e, + page_warm_test.go'da sayfa URL'si).
type pageWarmPage struct {
	Name string
	Job  string // selfobs worker.job iş adı (sabit)
	// Envs — Topbar env varyantları da ısıtılır mı. Yalnız MV yolu env'i
	// taşıyan sayfa (Services: service_env_summary). Endpoints / Databases
	// env'de ham span taramasına düşer (3 sa × prod ~27k span/sn ≈ 290 M
	// satır) — 100 sn'de bir ısıtmak kullanıcı başına açılıştan pahalı.
	Envs bool
	// Requests — env == "" varsayılan görünüm; env != "" Topbar varyantı.
	Requests func(from, to time.Time, env string) []pageWarmReq
}

var pageWarmRegistry = []pageWarmPage{
	{Name: "services", Job: "page-warm-services", Envs: true, Requests: servicesWarmReqs},
	{Name: "endpoints", Job: "page-warm-endpoints", Requests: endpointsWarmReqs},
	{Name: "databases", Job: "page-warm-databases", Requests: databasesWarmReqs},
}

// pageWarmRange — FE timeRangeToNs ile aynı biçim (ns).
func pageWarmRange(from, to time.Time, extra ...string) url.Values {
	v := url.Values{"from": {strconv.FormatInt(from.UnixNano(), 10)}, "to": {strconv.FormatInt(to.UnixNano(), 10)}}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] != "" {
			v.Set(extra[i], extra[i+1])
		}
	}
	return v
}

// servicesWarmSlots — FE sparkMaxSlotsForWidth(SPARK_DEFAULT_WIDTH = 80).
const servicesWarmSlots = "80"

// servicesWarmReqs — Services.tsx ilk yükleme: varsayılan sıra spanCount ↓
// (METRIC_SORT_COLS → /api/services-metric-rank), sayfa 0, 50, withTotal=1;
// ardından görünen adlarla sparkline (env yokken) + red-metric → spark.
// Filtre seçenekleri (clusters / namespaces / runtimes) env'siz.
func servicesWarmReqs(from, to time.Time, env string) []pageWarmReq {
	rank := pageWarmReq{
		Route:  "GET /api/services-metric-rank",
		Query:  pageWarmRange(from, to, "limit", "50", "offset", "0", "sort", "spanCount", "dir", "desc", "withTotal", "1", "env", env),
		Handle: (*Server).getServicesMetricRank,
		Next: func(body []byte) []pageWarmReq {
			var resp struct {
				Services []struct {
					Name string `json:"name"`
				} `json:"services"`
			}
			if json.Unmarshal(body, &resp) != nil || len(resp.Services) == 0 {
				return nil
			}
			names := make([]string, len(resp.Services))
			for i, sv := range resp.Services {
				names[i] = sv.Name
			}
			joined := strings.Join(names, ",")
			var out []pageWarmReq
			if env == "" {
				out = append(out, pageWarmReq{
					Route:  "GET /api/services/sparklines",
					Query:  pageWarmRange(from, to, "services", joined, "maxSlots", servicesWarmSlots),
					Handle: (*Server).getServiceSparklines,
				})
			}
			return append(out, pageWarmReq{
				Route:  "GET /api/services-red-metric",
				Query:  pageWarmRange(from, to, "services", joined, "env", env),
				Handle: (*Server).getServicesREDMetric,
				Next:   func(b []byte) []pageWarmReq { return servicesWarmSparkReq(from, to, env, b) },
			})
		},
	}
	out := []pageWarmReq{rank}
	if env == "" {
		out = append(out,
			pageWarmReq{Route: "GET /api/services-runtimes", Query: url.Values{}, Handle: (*Server).getAllServiceRuntimes},
			pageWarmReq{Route: "GET /api/clusters", Query: pageWarmRange(from, to), Handle: (*Server).getClusters},
			pageWarmReq{Route: "GET /api/namespaces", Query: pageWarmRange(from, to), Handle: (*Server).getNamespaces},
		)
	}
	return out
}

// servicesWarmSparkReq — red-metric yanıtından spark isteği (FE: kaynağı
// çözülmüş servisler, sıralı, sources paralel).
func servicesWarmSparkReq(from, to time.Time, env string, body []byte) []pageWarmReq {
	var rm struct {
		Services map[string]struct {
			Source string `json:"source"`
		} `json:"services"`
	}
	if json.Unmarshal(body, &rm) != nil || len(rm.Services) == 0 {
		return nil
	}
	ms := make([]string, 0, len(rm.Services))
	for n := range rm.Services {
		ms = append(ms, n)
	}
	sort.Strings(ms)
	srcs := make([]string, len(ms))
	for i, n := range ms {
		srcs[i] = rm.Services[n].Source
	}
	return []pageWarmReq{{
		Route:  "GET /api/services-red-metric-spark",
		Query:  pageWarmRange(from, to, "services", strings.Join(ms, ","), "sources", strings.Join(srcs, ","), "slots", servicesWarmSlots, "env", env),
		Handle: (*Server).getServicesREDSpark,
	}}
}

// endpointsWarmReqs — Endpoints.tsx varsayılanı: span kaynağı (servis
// seçilmeden metrik kipi yok), limit 100, compare=prior (varsayılan açık),
// calls ↓.
func endpointsWarmReqs(from, to time.Time, env string) []pageWarmReq {
	return []pageWarmReq{{
		Route:  "GET /api/endpoints",
		Query:  pageWarmRange(from, to, "limit", "100", "compare", "prior", "sort", "calls", "dir", "desc", "env", env),
		Handle: (*Server).getEndpoints,
	}}
}

// databasesWarmReqs — Databases.tsx: liste (env'li), en pahalı ifadeler
// (limit 10, env'siz), açık problemler, trend şeridi (env'siz).
func databasesWarmReqs(from, to time.Time, env string) []pageWarmReq {
	out := []pageWarmReq{{
		Route:  "GET /api/databases",
		Query:  pageWarmRange(from, to, "env", env),
		Handle: (*Server).getDatabases,
	}}
	if env == "" {
		out = append(out,
			pageWarmReq{Route: "GET /api/databases/slow-queries", Query: pageWarmRange(from, to, "limit", "10"), Handle: (*Server).getSlowQueriesGlobal},
			pageWarmReq{Route: "GET /api/databases/problems", Query: url.Values{}, Handle: (*Server).getDatabaseProblems},
			pageWarmReq{Route: "GET /api/databases/trends", Query: pageWarmRange(from, to), Handle: (*Server).getDBTrends},
		)
	}
	return out
}

// ── çalıştırma ───────────────────────────────────────────────────────

// pageWarmWriter — handler'ın yanıtını tutan en küçük ResponseWriter.
type pageWarmWriter struct {
	h      http.Header
	status int
	buf    bytes.Buffer
}

func (w *pageWarmWriter) Header() http.Header {
	if w.h == nil {
		w.h = http.Header{}
	}
	return w.h
}
func (w *pageWarmWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *pageWarmWriter) WriteHeader(c int)           { w.status = c }

// pageWarmRequest — handler'a verilecek *http.Request (desen + sorgu).
func pageWarmRequest(ctx context.Context, req pageWarmReq) (*http.Request, error) {
	path := strings.TrimPrefix(req.Route, "GET ")
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, path+"?"+req.Query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	r.Pattern = req.Route
	return r, nil
}

// pageWarmDo — bir isteği (ve bağımlılarını) ısıtır; tier sayaçları döner.
func (s *Server) pageWarmDo(req pageWarmReq, tiers map[string]int) {
	ctx, cancel := context.WithTimeout(context.Background(), pageWarmBudget)
	r, err := pageWarmRequest(withPageWarm(ctx, &pageWarmOpts{tick: pageWarmTick}), req)
	if err != nil {
		cancel()
		return
	}
	w := &pageWarmWriter{}
	req.Handle(s, w, r)
	cancel()
	tier := w.Header().Get("X-Cache")
	if tier == "" {
		tier = "DIRECT" // ör. VM yok → red-metric önbelleksiz boş yanıt
	}
	tiers[tier]++
	if w.status != 0 && w.status != http.StatusOK {
		if w.status != http.StatusGatewayTimeout { // zaman aşımı sessiz (eski ısıtıcı kuralı)
			log.Printf("[page-warm] %s: HTTP %d %s", req.Route, w.status, strings.TrimSpace(w.buf.String()))
		}
		return
	}
	if req.Next != nil {
		for _, n := range req.Next(w.buf.Bytes()) {
			s.pageWarmDo(n, tiers)
		}
	}
}

// pageWarmClaim — tik başına filo çapında tek ısıtıcı (warmClaim deseni,
// kendi anahtarı). Redis hatası açık kapı.
func pageWarmClaim(ctx context.Context, c cache.Cache) bool {
	ok, err := c.SetNX(ctx, pageWarmClaimKey, []byte("1"), pageWarmTick-time.Second)
	return err != nil || ok
}

// pageWarmTickRun — tek tik: lider değilse hiçbir şey; lider ise varsayılan
// görünümler + bu tikin env varyantları. Dönüş: koşuldu mu.
func (s *Server) pageWarmTickRun(tick int, now time.Time) bool {
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	claimed := pageWarmClaim(cctx, s.cache)
	var envs []string
	if claimed {
		envs = s.pageWarmEnvs(cctx, now)
	}
	cancel()
	if !claimed {
		return false
	}
	from := now.Add(-pageWarmWindow)
	for _, p := range pageWarmRegistry {
		started := time.Now()
		tiers := map[string]int{}
		pe := []string{""}
		if p.Envs {
			pe = pageWarmTickEnvs(tick, envs)
		}
		for _, env := range pe {
			for _, req := range p.Requests(from, now, env) {
				s.pageWarmDo(req, tiers)
			}
		}
		selfobs.ObserveJob(p.Job, started, nil)
		if d := time.Since(started); d > pageWarmTick/2 {
			log.Printf("[page-warm] %s tik %s sürdü %v", p.Name, d.Round(time.Millisecond), tiers)
		}
	}
	return true
}

// pageWarmTickEnvs — SAF: bu tikte ısıtılacak env'ler: "" (varsayılan) her
// tik; env i yalnız (tick + i) % pageWarmEnvEvery == 0 iken.
func pageWarmTickEnvs(tick int, envs []string) []string {
	out := []string{""}
	for i, e := range envs {
		if (tick+i)%pageWarmEnvEvery == 0 {
			out = append(out, e)
		}
	}
	return out
}

// pageWarmLoop — api rolünde başlar (api.go registerRoutes).
func (s *Server) pageWarmLoop() {
	if s == nil || s.cache == nil || s.store == nil {
		return // warmDependenciesCache ile aynı nil sözleşmesi (testler)
	}
	time.Sleep(7 * time.Second) // açılış DDL'iyle yarışma
	for tick := 0; ; tick++ {
		s.pageWarmTickRun(tick, time.Now())
		time.Sleep(pageWarmTick)
	}
}

// ── env izleme ───────────────────────────────────────────────────────

var (
	pageWarmEnvSeen  sync.Map // env → son not (unix sn), pod içi
	pageWarmEnvSeenN atomic.Int32
)

// notePageWarmEnv — bu sayfalardan biri env'le istendi: Redis'e 24 sa
// izi (pod başına env başına dakikada ≤ 1 yazım, ≤ 256 ayrık env).
// Isıtıcının kendi istekleri iz bırakmaz (yoksa env asla düşmezdi).
func (s *Server) notePageWarmEnv(r *http.Request, env string) {
	if env == "" || len(env) > httpEnvScopeTermMax || s.cache == nil || pageWarmFrom(r.Context()) != nil {
		return
	}
	now := time.Now()
	if v, ok := pageWarmEnvSeen.Load(env); ok && now.Unix()-v.(int64) < int64(pageWarmEnvNoteGap/time.Second) {
		return
	} else if !ok && pageWarmEnvSeenN.Load() >= pageWarmEnvSeenCap {
		return
	} else if !ok {
		pageWarmEnvSeenN.Add(1)
	}
	pageWarmEnvSeen.Store(env, now.Unix())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.cache.Set(ctx, pageWarmEnvPrefix+fnvStr(env), []byte(strconv.FormatInt(now.Unix(), 10)+"\t"+env), pageWarmEnvTTL)
	}()
}

// pageWarmEnvs — son 24 sa'te istenmiş env'ler, en yeni ≤ pageWarmEnvMax,
// ada göre sıralı (kademe sırası kararlı kalsın).
func (s *Server) pageWarmEnvs(ctx context.Context, now time.Time) []string {
	raw, err := s.cache.ScanPrefix(ctx, pageWarmEnvPrefix)
	if err != nil {
		return nil
	}
	return pageWarmPickEnvs(raw, now)
}

// pageWarmPickEnvs — SAF: "<unix>\t<env>" değerleri → env listesi.
func pageWarmPickEnvs(raw [][]byte, now time.Time) []string {
	type seen struct {
		env string
		at  int64
	}
	best := map[string]int64{}
	for _, b := range raw {
		ts, env, ok := strings.Cut(string(b), "\t")
		at, err := strconv.ParseInt(ts, 10, 64)
		if !ok || err != nil || env == "" || now.Unix()-at > int64(pageWarmEnvTTL/time.Second) {
			continue
		}
		best[env] = max(best[env], at)
	}
	all := make([]seen, 0, len(best))
	for e, at := range best {
		all = append(all, seen{e, at})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].at != all[j].at {
			return all[i].at > all[j].at
		}
		return all[i].env < all[j].env
	})
	if len(all) > pageWarmEnvMax {
		all = all[:pageWarmEnvMax]
	}
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = a.env
	}
	sort.Strings(out)
	return out
}
