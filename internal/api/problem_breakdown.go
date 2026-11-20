package api

// problem_breakdown.go — v0.10.2388 (triage mockup dilim 3; operatör onaylı
// "Problem detay" mockup'ındaki "Endpoint'e / Sürüme / Exception'a göre"
// kırılımları, hedef: Problem açıldığında hatanın NEREDE olduğunu sayfanın
// kendisi söylesin — Dynatrace Davis emsali, model çağrısı yok):
//
//	GET /api/problems/{id}/breakdown
//
// Hata oranı Problem'inin (servis özneli, metrik error_rate; açık ya da son
// 7 günde kapanmış) deterministik kırılımları. Her blok YALNIZ verisi gerçekse
// döner; verisi olmayan blok gövdede yoktur (sıfırlarla dolu tablo çizilmez),
// okunamayan blok `failed: true` taşır (sayfa "okunamadı" yazar, öteki bloklar
// çizilir). Blokların her biri kendi kaynağını söyler; ikisi karıştırılmaz:
//
//	endpoints     ÖRNEKLENMEMİŞ sunucu metriği (VictoriaMetrics RED ailesi,
//	              rota etiketi): penceredeki istek / başarısız istek. Olağan
//	              oran dedektörün mevsimsel tabanından (ml_series, aynı takvim
//	              sınıfı günler × ±1 saat); tabanı olmayan rotada YAZILMAZ.
//	versions      aynı VM satırlarından, YALNIZ seride sürüm etiketi varsa
//	              (etiket haritasının `version` rolü; konvansiyon
//	              service_version) ve trafiği olan ≥ 2 sürüm varsa. Span'den
//	              sürüm ORANI üretilmez: başarılı izler örneklenir, başarısızlar
//	              tam tutulur — oran yeni sürümü olduğundan kötü gösterirdi.
//	exceptions    hata durumlu span'lerdeki exception kayıtlarının türe göre
//	              SAYIMI (hata span'leri örneklenmez → sayım tam) + türün en
//	              yeni exception grubu (mesaj, grup sayfası).
//	dependencies  servisin bağımlılık dedektörü olayları (hata oranı / p95),
//	              Problem'in yaşam penceresiyle örtüşenler. Zamanda birliktelik;
//	              neden iddiası değil.
//
// Neden ayrı uç: kardeş /events, /graph gibi yalnız detay açıkken istenir
// (liste satırına taşınmaz); VM'e ve ham span'e giden okumalar Problem
// okumasının gövdesine girmez. Neden /api/problems/{id} altında: girdinin
// tamamı Problem'in kendisidir (servis, pencere), istemci pencere yollamaz.
//
// SORGULAR ve sınırları (istek başına, önbellek ıskasında):
//
//	VM   aile başına 1 etiket keşfi (servise kapsanmış /api/v1/labels, 24 sa,
//	     2 sn, 5 dk önbellek: redFamilyLabelSet) + rota (ve varsa sürüm)
//	     kırılımlı 2 anlık sorgu: sum by (…) (increase(<aile>_count{servis}[W]))
//	     ve aynısı sunucu-hatası durum süzgeciyle. En çok 3 aile → ≤ 6 anlık
//	     sorgu; tipik servis tek aile yazar → 2. Aralık sorgusu, bucket, topk yok.
//	CH   ml_series (signal, service) öneki, ≤ 2000 rota, 5 sn — rota tabanı
//	     spans  tek servis × pencere (≤ 30 dk açık / ≤ 6 sa kapalı) × hata
//	            durumlu × ex_match, tür başına sayım, ≤ 101 satır, 5 sn
//	     exception_groups FINAL servis × ≤ 5 tür × last_seen, ≤ 100 satır, 5 sn
//	     ml_events FINAL servis × 2 dedektör × pencere, hafif kolonlar, ≤ 50, 5 sn
//	     + Problem satırı ve ML ayarı (anahtarlı okumalar).
//
// Bütçe: üç blok PARALEL koşar, her biri breakdownBlockBudget ile sınırlı —
// bütçeyi aşan blok "okunamadı" döner, yanıt bekletilmez (soğuk p99 < 1 sn).
// serveCached 30 sn; anahtar tek girdiyi (kimlik) taşır. Rol kapısı YOK —
// salt-okunur, viewer GÖRÜR (Problem detayı ve kardeş uçların duruşu). {id}
// "P-…" görüntü kimliğini de kabul eder. api.go BÜYÜMEZ: route defteri.

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() { registerRoutesExtra("problem-breakdown", (*Server).registerProblemBreakdownRoutes) }

func (s *Server) registerProblemBreakdownRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/breakdown", s.getProblemBreakdown)
}

const (
	problemBreakdownTTL = 30 * time.Second
	// breakdownBlockBudget — bir bloğun toplam süresi (VM + CH). Aşan blok
	// "okunamadı"; yanıt en geç bu kadar sürer.
	breakdownBlockBudget = 900 * time.Millisecond
)

// problemBreakdownStore — ucun ClickHouse'tan istedikleri (*chstore.Store
// karşılar; testler sahte verir).
type problemBreakdownStore interface {
	GetProblem(ctx context.Context, id string) (*chstore.Problem, error)
	GetProblemByDisplayID(ctx context.Context, displayID string) (*chstore.Problem, error)
	GetMLConfig(ctx context.Context) chstore.MLConfig
	AnomalySensitivityForDetectors() chstore.AnomalySensitivityConfig
	MLRouteErrorBaseline(ctx context.Context, req chstore.MLRouteBaselineRequest) (map[string]chstore.MLRouteBaseline, bool, error)
	ServiceErrorExceptionTypes(ctx context.Context, service string, from, to time.Time) ([]chstore.ExceptionTypeCount, bool, error)
	NewestExceptionGroupsByType(ctx context.Context, service string, types []string, since time.Time) (map[string]chstore.ExceptionTypeGroup, error)
	ServiceDependencyEvents(ctx context.Context, service string, from, to time.Time) ([]chstore.DependencyEvent, error)
}

// problemBreakdownVM — ucun VictoriaMetrics'ten istedikleri (vmMetricSource
// karşılar): servise kapsanmış etiket keşfi, etiket haritası, anlık sorgu.
type problemBreakdownVM interface {
	redFamilyLabelReader
	MetricLabelMap() vmetrics.LabelMap
	QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error)
}

var (
	problemBreakdownStoreOf = func(s *Server) problemBreakdownStore { return s.store }
	// problemBreakdownVMOf — metrik kaynağı VM değilse ok=false: örneklenmemiş
	// rota serisi yok, endpoint / sürüm blokları üretilmez (uydurulmaz).
	problemBreakdownVMOf = func(s *Server) (problemBreakdownVM, bool) {
		if vm, ok := s.metricSource().(vmMetricSource); ok {
			return vm, true
		}
		return nil, false
	}
	problemBreakdownNow = time.Now
)

// breakdownEndpointsBlock — "Endpoint'e göre".
type breakdownEndpointsBlock struct {
	Failed bool                `json:"failed,omitempty"` // okunamadı
	Rows   []breakdownRouteRow `json:"rows"`
	// Other — "Diğer N endpoint" (OtherCount = N); rota alanı boştur.
	Other      *breakdownRouteRow `json:"other,omitempty"`
	OtherCount int                `json:"otherCount,omitempty"`
	// Pencere toplamı (dakikada).
	FailedPerMin   float64 `json:"failedPerMin"`
	RequestsPerMin float64 `json:"requestsPerMin"`
	// NormalFailed — rota tabanı okunamadı: satırlar olağansız gelir ve sayfa
	// bunu "bilinmiyor"dan AYRI söyler (okunamadı ≠ yok).
	NormalFailed bool `json:"normalFailed,omitempty"`
}

// breakdownVersionsBlock — "Sürüme göre" (yalnız sürüm etiketli seride).
type breakdownVersionsBlock struct {
	Rows []breakdownVersionRow `json:"rows"`
	More int                   `json:"more,omitempty"`
	// Label — sürümün okunduğu metrik etiketi (kaynak notu).
	Label string `json:"label"`
}

// problemBreakdownResponse — sayfanın gövdesi. Applicable=false: bölüm bu
// Problem için yok (başka tür, eski kapanış); bloklar hiç dönmez.
type problemBreakdownResponse struct {
	ProblemID  string `json:"problemId"`
	Applicable bool   `json:"applicable"`
	Service    string `json:"service,omitempty"`
	Open       bool   `json:"open,omitempty"`
	// Pencere (unix ns) — endpoint / sürüm / exception sayımlarının aralığı.
	WindowFrom int64 `json:"windowFrom,omitempty"`
	WindowTo   int64 `json:"windowTo,omitempty"`

	Endpoints    *breakdownEndpointsBlock `json:"endpoints,omitempty"`
	Versions     *breakdownVersionsBlock  `json:"versions,omitempty"`
	Exceptions   *breakdownExceptions     `json:"exceptions,omitempty"`
	Dependencies *breakdownDependencies   `json:"dependencies,omitempty"`
}

func (s *Server) getProblemBreakdown(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "problem id required")
		return
	}
	s.serveCached(w, r, problemBreakdownKey(id), problemBreakdownTTL, func(ctx context.Context) (any, error) {
		st := problemBreakdownStoreOf(s)
		var p *chstore.Problem
		var err error
		if chstore.IsProblemDisplayID(id) {
			p, err = st.GetProblemByDisplayID(ctx, id)
		} else {
			p, err = st.GetProblem(ctx, id)
		}
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, notFoundf("problem %q", id)
		}
		return s.buildProblemBreakdown(ctx, st, *p, problemBreakdownNow()), nil
	})
}

// buildProblemBreakdown — üç bloğu PARALEL okur; hiçbir blok hatası yanıtı
// düşürmez (blok `failed` olur). Sayfa asla bu uç yüzünden kırılmaz.
func (s *Server) buildProblemBreakdown(ctx context.Context, st problemBreakdownStore, p chstore.Problem, now time.Time) problemBreakdownResponse {
	out := problemBreakdownResponse{ProblemID: p.ID}
	if !breakdownSubject(p) {
		return out
	}
	cfg := st.GetMLConfig(ctx)
	win, ok := breakdownWindow(p, now, time.Duration(cfg.IngestLagSec)*time.Second)
	if !ok {
		return out
	}
	out.Applicable, out.Service, out.Open = true, p.Service, win.Open
	out.WindowFrom, out.WindowTo = win.From.UnixNano(), win.To.UnixNano()

	var wg sync.WaitGroup
	block := func(fn func(ctx context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bctx, cancel := context.WithTimeout(ctx, breakdownBlockBudget)
			defer cancel()
			fn(bctx)
		}()
	}
	block(func(bctx context.Context) {
		out.Endpoints, out.Versions = s.breakdownEndpoints(bctx, st, p, win, cfg)
	})
	block(func(bctx context.Context) { out.Exceptions = breakdownExceptionsBlock(bctx, st, p, win) })
	block(func(bctx context.Context) { out.Dependencies = breakdownDependenciesBlock(bctx, st, p, win, now) })
	wg.Wait()
	return out
}

// breakdownFamilyRead — bir RED ailesinin pencere sayımı.
type breakdownFamilyRead struct {
	fam          redFamily
	count, errs  []chstore.SpanMetricSeries
	versionLabel string
	present      bool // aile bu servis için rota + durum etiketiyle yazılıyor
	err          error
}

// breakdownEndpoints — "Endpoint'e göre" + "Sürüme göre". VM yapılandırılmamışsa
// ikisi de yok. Aileler paralel; HERHANGİ bir ailenin okuması düşerse blok
// "okunamadı" (eksik aileyle sıralama yanlış olurdu — okunamadı ≠ yok). İki
// gRPC ailesi de yazılıyorsa yalnız ilki sayılır (aynı istek iki metrikte;
// red_family çözücüsünün kuralı). Rota tabanı AYNI anda ClickHouse'tan; düşerse
// tablo olağansız çizilir (NormalFailed).
func (s *Server) breakdownEndpoints(ctx context.Context, st problemBreakdownStore, p chstore.Problem, win breakdownWin, cfg chstore.MLConfig) (*breakdownEndpointsBlock, *breakdownVersionsBlock) {
	vm, ok := problemBreakdownVMOf(s)
	if !ok {
		return nil, nil
	}
	lm := vm.MetricLabelMap().Normalized()
	reads := make([]breakdownFamilyRead, len(redFamilies))
	var wg sync.WaitGroup
	for i, f := range redFamilies {
		wg.Add(1)
		go func(i int, f redFamily) {
			defer wg.Done()
			reads[i] = s.breakdownReadFamily(ctx, vm, lm, f, p.Service, win)
		}(i, f)
	}
	var base map[string]chstore.MLRouteBaseline
	var baseErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		sl := ml.SeasonalBaselineSlice(win.To, cfg, st.AnomalySensitivityForDetectors())
		base, _, baseErr = st.MLRouteErrorBaseline(ctx, chstore.MLRouteBaselineRequest{Service: p.Service, Dates: sl.Dates,
			HourFrom: sl.HourFrom, HourTo: sl.HourTo, TargetSec: cfg.BucketSec, TZ: sl.TZ})
	}()
	wg.Wait()

	routes, versions := map[string]breakdownCounts{}, map[string]breakdownCounts{}
	versionLabel, versioned, grpcSeen := "", true, false
	found := false
	for _, rd := range reads {
		if rd.err != nil {
			log.Printf("[problem-breakdown] %s %s ailesi okunamadı (blok okunamadı): %v", p.ID, rd.fam.ID, rd.err)
			return &breakdownEndpointsBlock{Failed: true, Rows: []breakdownRouteRow{}}, nil
		}
		if !rd.present || len(rd.count) == 0 {
			continue
		}
		if rd.fam.Kind == redKindGRPC {
			if grpcSeen {
				continue
			}
			grpcSeen = true
		}
		found = true
		if rd.versionLabel == "" {
			versioned = false // bir aile sürüm taşımıyor: sürüm kırılımı eksik kalır
		} else {
			versionLabel = rd.versionLabel
		}
		foldBreakdownSeries(routes, versions, rd.count, rd.errs, rd.fam.RouteLabel, rd.versionLabel)
	}
	if !found {
		return nil, nil
	}
	if baseErr != nil {
		log.Printf("[problem-breakdown] %s rota tabanı okunamadı (olağan sütunu yok): %v", p.ID, baseErr)
		base = nil
	}
	ranked := rankBreakdownRoutes(routes, base, win.minutes(), cfg.MinBaselineSamples, breakdownTopRoutes, ml.FailureRateImpactThreshold)
	if len(ranked.Rows) == 0 {
		return nil, nil // pencerede başarısız istek yok: tablo yok, sürüm kıyası da yok
	}
	eb := &breakdownEndpointsBlock{Rows: ranked.Rows, Other: ranked.Other, OtherCount: ranked.OtherCount,
		FailedPerMin: ranked.FailedPerMin, RequestsPerMin: ranked.RequestsPerMin, NormalFailed: baseErr != nil}
	var vb *breakdownVersionsBlock
	if versioned && versionLabel != "" {
		if rows, more := breakdownVersionRows(versions, win.minutes(), breakdownTopVersions, ml.FailureRateImpactThreshold); rows != nil {
			vb = &breakdownVersionsBlock{Rows: rows, More: more, Label: versionLabel}
		}
	}
	return eb, vb
}

// breakdownReadFamily — tek ailenin okuması: etiket keşfi (önbellekli) → iki
// anlık sorgu (paralel). Servis bu aileyi yazmıyorsa ya da rota / durum etiketi
// yoksa present=false (hata değil: bu aileden kırılım çıkmaz).
func (s *Server) breakdownReadFamily(ctx context.Context, vm problemBreakdownVM, lm vmetrics.LabelMap, f redFamily, service string, win breakdownWin) breakdownFamilyRead {
	out := breakdownFamilyRead{fam: f}
	labels, err := s.redFamilyLabelSet(ctx, vm, f.Probe, redFamilySvcLabel(lm), service, win.To)
	if err != nil {
		out.err = err
		return out
	}
	if len(labels) == 0 {
		return out
	}
	sr := vmetrics.ResolveLabelRole(vmetrics.RoleService, lm, labels, true)
	status := redFamilyStatusLabel(f, labels)
	if !sr.Applied() || status == "" || !labels[f.RouteLabel] {
		return out
	}
	by := []string{f.RouteLabel}
	if vr := vmetrics.ResolveLabelRole(vmetrics.RoleVersion, lm, labels, true); vr.Applied() && vr.Present {
		out.versionLabel = vr.Label
		by = append(by, vr.Label)
	}
	cq, eq := breakdownVMQueries(f, sr.Label, service, status, by, win.seconds())
	var wg sync.WaitGroup
	var cerr, eerr error
	wg.Add(2)
	go func() { defer wg.Done(); out.count, cerr = vm.QueryPromQLInstant(ctx, cq, win.To) }()
	go func() { defer wg.Done(); out.errs, eerr = vm.QueryPromQLInstant(ctx, eq, win.To) }()
	wg.Wait()
	if cerr != nil {
		out.err = cerr
	} else if eerr != nil {
		out.err = eerr
	}
	out.present = out.err == nil
	return out
}

// breakdownExceptionsBlock — "Exception'a göre": sayım okuması düşerse blok
// okunamadı; grup araması düşerse satırlar bağlantısız / mesajsız çizilir
// (sayımlar doğru, yalnız süs eksik).
func breakdownExceptionsBlock(ctx context.Context, st problemBreakdownStore, p chstore.Problem, win breakdownWin) *breakdownExceptions {
	types, capped, err := st.ServiceErrorExceptionTypes(ctx, p.Service, win.From, win.To)
	if err != nil {
		log.Printf("[problem-breakdown] %s exception sayımı okunamadı: %v", p.ID, err)
		return &breakdownExceptions{Failed: true, Rows: []breakdownExceptionRow{}}
	}
	var groups map[string]chstore.ExceptionTypeGroup
	if top := breakdownExceptionTypes(types, breakdownTopExceptions); len(top) > 0 {
		if groups, err = st.NewestExceptionGroupsByType(ctx, p.Service, top, win.From); err != nil {
			log.Printf("[problem-breakdown] %s exception grupları okunamadı (satırlar bağlantısız): %v", p.ID, err)
			groups = nil
		}
	}
	return buildBreakdownExceptions(types, capped, groups, breakdownTopExceptions)
}

// breakdownDependenciesBlock — "Bağımlılığa göre": Problem'in YAŞAM
// penceresiyle (başlangıç − ProblemEventSlack … bitiş / şimdi) örtüşen
// bağımlılık olayları. Sayım penceresi değil: köken ipucu Problem'in
// başındaki yükseliştir, uzun süren Problem'de son 30 dakikanın dışında kalır.
func breakdownDependenciesBlock(ctx context.Context, st problemBreakdownStore, p chstore.Problem, win breakdownWin, now time.Time) *breakdownDependencies {
	from := time.Unix(0, p.StartedAt).Add(-chstore.ProblemEventSlack)
	to := now
	if !win.Open && p.ResolvedAt != nil {
		to = time.Unix(0, *p.ResolvedAt)
	}
	evs, err := st.ServiceDependencyEvents(ctx, p.Service, from, to)
	if err != nil {
		log.Printf("[problem-breakdown] %s bağımlılık olayları okunamadı: %v", p.ID, err)
		return &breakdownDependencies{Failed: true, Rows: []breakdownDependencyRow{}}
	}
	return buildBreakdownDependencies(evs, now.UnixNano(), breakdownTopDeps)
}
