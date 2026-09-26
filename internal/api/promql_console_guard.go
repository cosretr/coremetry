package api

// promql_console_guard.go — v0.10.952 (PromQL konsolu, spec C4; karar 3, 4,
// 7, 9 — docs/promql-console/audit.md §6 "Guardrails").
//
// Konsolun SAF korkulukları; handler'lar (C5) bunları çağırır, burada I/O
// yok (tek istisna writePromQLGuardError: 429'un Retry-After başlığını tek
// yerde yazmak için). Saat enjekte edilir → pencere sıfırlanması testte
// uyumadan kanıtlanır.
//
//   promqlEffectiveStep   otomatik adım grafik genişliğini hedefler; elle
//                         adım 11k tavanına göre YÜKSELTİLİR (hata değil,
//                         stepRaised)
//   promqlCheckRange      maxRangeH üstü REDDEDİLİR (400 guardrail, limit
//                         gövdede) — /clusters'ın sessiz 30g clamp'i gibi
//                         DEĞİL: konsol ne koştuğunu dürüstçe söyler
//   promqlLimiter         kullanıcı başına eşzamanlılık (try-acquire) +
//                         sabit pencere hız sınırı; metadata ayrı, daha
//                         yüksek bütçe VE kendi eşzamanlılık kapısı
//                         (max(4, 2×perUserConcurrency)) → 429
//                         rate_limited + Retry-After
//   promqlMetaWindow /    otomatik tamamlama uçları: zaman sınırlı
//   promqlMetaLimit       (varsayılan son 1h, maxRange'e KELEPÇE), limit
//                         sunucuda zorlanır
//   promqlMetaCacheKey    60 s metadata önbellek anahtarı: TÜM girdiler,
//                         sıralı + FNV (v0.5.187), enjekte etiket adı/değeri
//                         DAHİL — clusterCfgDigest'i (thanos_handlers.go)
//                         olduğu gibi kullanmıyoruz çünkü etiketi dışarıda
//                         bırakıyor: paylaşımlı querier'da cluster-a ve
//                         cluster-b aynı URL'yi taşır ve birbirinin
//                         otomatik tamamlamasını servis ederdi.
//
// Limitler POD BAŞINA (karar 3, v1): paket düzeyinde durum. Redis INCR'li
// küme-geneli limit v2 işi (cache.Cache'te INCR/EXPIRE yok). Neden Server
// alanı değil: alanlar api.go'da, api.go büyümez.
//
// Neden x/time/rate token bucket değil: pakette kullanılmıyor (yeni
// bağımlılık) ve mcp_gate.go'nun sabit pencere deseni zaten burada; dakika
// başı sayaç operatöre "30/dk" diye birebir anlatılabiliyor.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// ── Hata tipi ───────────────────────────────────────────────────────────────

// promqlGuardError — korkuluk reddi; Prometheus hata zarfının
// ({status:"error", errorType, error}) üstüne limit alanlarını taşır ki UI
// "son 7g'yi kullan" gibi somut bir öneri çizebilsin (karar 7, 11).
type promqlGuardError struct {
	Status     int           // 400 (guardrail / bad_data) | 429 (rate_limited)
	ErrorType  string        // "guardrail" | "rate_limited" | "bad_data"
	Guardrail  string        // "maxRange" | "perUserConcurrency" | "perUserPerMin" | "metaPerUserConcurrency" | "metaPerUserPerMin" | ""
	Limit      int           // korkuluğun sınırı, Unit biriminde
	Unit       string        // "h" | "requests" | "requests/min"
	RetryAfter time.Duration // yalnız rate_limited; ≥1 s
	Msg        string
}

func (e *promqlGuardError) Error() string { return e.Msg }

// retryAfterSeconds — Retry-After başlığı tam saniye ister; aşağı yuvarlamak
// istemciyi pencere bitmeden yeniden denetip bir 429 daha yedirirdi.
func (e *promqlGuardError) retryAfterSeconds() int {
	if e.RetryAfter <= 0 {
		return 0
	}
	s := int((e.RetryAfter + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

// body — JSON gövdesi. Alan adları FE promqlErrorOf sözleşmesi.
func (e *promqlGuardError) body() map[string]any {
	b := map[string]any{"status": "error", "errorType": e.ErrorType, "error": e.Msg}
	if e.Guardrail != "" {
		b["guardrail"] = e.Guardrail
		b["limit"] = e.Limit
		b["unit"] = e.Unit
	}
	if s := e.retryAfterSeconds(); s > 0 {
		b["retryAfterS"] = s
	}
	return b
}

// writePromQLGuardError — başlık WriteHeader'dan ÖNCE (writeJSONError
// sıralama dersi); 429 her zaman Retry-After taşır (karar 3).
func writePromQLGuardError(w http.ResponseWriter, e *promqlGuardError) {
	w.Header().Set("Content-Type", "application/json")
	if s := e.retryAfterSeconds(); s > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(s))
	}
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e.body())
}

// ── Adım bütçesi (karar 4) ──────────────────────────────────────────────────

// promqlMinPoints — `points` isteğinin alt kelepçesi (karar 4: 100..max).
const promqlMinPoints = 100

// promqlClampPoints — istek `points` → [100, maxPointsPerSeries]; ≤0 =
// ayarın targetPoints'i (o da normalize edilmiş, tavanı aşamaz).
func promqlClampPoints(points int, cfg promqlConsoleSettings) int {
	if points <= 0 {
		points = cfg.TargetPoints
	}
	if points < promqlMinPoints {
		points = promqlMinPoints
	}
	if cfg.MaxPointsPerSeries >= promqlMinPoints && points > cfg.MaxPointsPerSeries {
		points = cfg.MaxPointsPerSeries
	}
	return points
}

// promqlCeilStep — ceil(rng / n) TAM SANİYE (tamsayı aritmetiği; float
// yuvarlaması 11k sınırında bir saniye kaydırmasın).
func promqlCeilStep(rng time.Duration, n int) time.Duration {
	if rng <= 0 || n <= 0 {
		return 0
	}
	per := time.Duration(n) * time.Second
	return ((rng + per - 1) / per) * time.Second
}

// promqlEffectiveStep — SAF (karar 4).
//
//	requested ≤ 0 (otomatik): step = max(minStep, ceil(rng / points)),
//	  points = promqlClampPoints(points) — grafik genişliğini hedefler,
//	  11k'yı DEĞİL (500 seri × 11k nokta ≈ 165 MB; audit §6 bütçe çatışması).
//	elle: taban = max(minStep, ceil(rng / maxPointsPerSeries)); istek tabanın
//	  altındaysa tabana YÜKSELTİLİR ve raised=true — asla hata değil.
//
// Tavan Prometheus'un kendi kuralıyla aynı: rng/step ≤ 11000 geçerlidir
// (Prometheus "exceeded maximum resolution of 11,000 points" kontrolü
// bölümün kendisine bakar). Otomatik modda raised hep false.
func promqlEffectiveStep(rng, requested time.Duration, points int, cfg promqlConsoleSettings) (step time.Duration, raised bool) {
	minStep := cfg.minStep()
	if requested <= 0 {
		step = promqlCeilStep(rng, promqlClampPoints(points, cfg))
		if step < minStep {
			step = minStep
		}
		return step, false
	}
	floor := promqlCeilStep(rng, cfg.MaxPointsPerSeries)
	if floor < minStep {
		floor = minStep
	}
	if requested < floor {
		return floor, true
	}
	return requested, false
}

// ── Aralık kontrolü (karar 7) ───────────────────────────────────────────────

// promqlHumanHours — "168h (7d)" / "36h"; mesaj operatörün diliyle
// konuşsun, 604800 saniye değil.
func promqlHumanHours(h int) string {
	if h >= 24 && h%24 == 0 {
		return fmt.Sprintf("%dh (%dd)", h, h/24)
	}
	return fmt.Sprintf("%dh", h)
}

// promqlCheckRange — SAF: end < start → bad_data; end-start > maxRange →
// guardrail 400 (limit gövdede). Tam sınır GEÇERLİ (7g = izin, 7g+1s = ret).
// Anlık sorgu (range=0) her zaman geçer. Sessiz clamp YOK: kullanıcının
// gördüğü grafik istediği aralık değilse bunu bilmesi gerekir.
func promqlCheckRange(start, end time.Time, cfg promqlConsoleSettings) error {
	if end.Before(start) {
		return &promqlGuardError{Status: http.StatusBadRequest, ErrorType: "bad_data",
			Msg: "end timestamp must not be before start time"}
	}
	if rng := end.Sub(start); rng > cfg.maxRange() {
		return &promqlGuardError{
			Status: http.StatusBadRequest, ErrorType: "guardrail",
			Guardrail: "maxRange", Limit: cfg.MaxRangeH, Unit: "h",
			Msg: fmt.Sprintf("query range %s exceeds the console maximum of %s — narrow the time range or use the last %s",
				rng.Round(time.Second), promqlHumanHours(cfg.MaxRangeH), promqlHumanHours(cfg.MaxRangeH)),
		}
	}
	return nil
}

// ── Kullanıcı başına limitler (karar 3) ─────────────────────────────────────

// promqlRateWindow — sabit pencere, unix dakikasına hizalı (mcp_gate.go).
const promqlRateWindow = time.Minute

type promqlRateBucket struct {
	windowStart int64 // unix saniye
	count       int
}

// promqlLimiter — pod-yerel. Tek mutex: iki eşzamanlılık + iki hız
// haritası aynı kritik bölgede karar verir (yarım tüketim yok).
type promqlLimiter struct {
	now func() time.Time

	mu           sync.Mutex
	inflight     map[string]int               // kullanıcı → uçuştaki query/query_range
	metaInflight map[string]int               // kullanıcı → uçuştaki metadata (upstream çağrısı)
	queryRate    map[string]*promqlRateBucket // kullanıcı → query penceresi
	metaRate     map[string]*promqlRateBucket // kullanıcı → metadata penceresi
	sweptWin     int64                        // son süpürülen pencere başı
}

// newPromQLLimiter — inflight / metaInflight süpürme istemez: release sayaç
// sıfıra inince girdiyi siler (harita yalnız uçuştaki kullanıcıları taşır).
func newPromQLLimiter(now func() time.Time) *promqlLimiter {
	if now == nil {
		now = time.Now
	}
	return &promqlLimiter{
		now:          now,
		inflight:     map[string]int{},
		metaInflight: map[string]int{},
		queryRate:    map[string]*promqlRateBucket{},
		metaRate:     map[string]*promqlRateBucket{},
	}
}

// promqlLimits — süreç-genelinde tek limiter (C5 kullanır).
var promqlLimits = newPromQLLimiter(time.Now)

// window — şimdiki pencere başı + bitişe kalan süre. Pencere değiştiyse
// bayat kovalar PENCERE BAŞINA BİR KEZ süpürülür (mcp_gate her yeni
// kovada tüm haritayı geziyordu; burada harita boyu kullanıcı sayısıyla
// sınırlı kalır, maliyet dakikada bir tur). Çağıran mu'yu tutar.
func (l *promqlLimiter) window() (winStart int64, remaining time.Duration) {
	now := l.now()
	sec := now.Unix()
	w := int64(promqlRateWindow / time.Second)
	winStart = sec - sec%w
	if winStart != l.sweptWin {
		for k, b := range l.queryRate {
			if b.windowStart != winStart {
				delete(l.queryRate, k)
			}
		}
		for k, b := range l.metaRate {
			if b.windowStart != winStart {
				delete(l.metaRate, k)
			}
		}
		l.sweptWin = winStart
	}
	remaining = time.Unix(winStart+w, 0).Sub(now)
	if remaining < time.Second {
		remaining = time.Second
	}
	return winStart, remaining
}

func promqlRateBucketFor(m map[string]*promqlRateBucket, user string, winStart int64) *promqlRateBucket {
	b := m[user]
	if b == nil || b.windowStart != winStart {
		b = &promqlRateBucket{windowStart: winStart}
		m[user] = b
	}
	return b
}

// acquireQuery — query / query_range kapısı: eşzamanlılık try-acquire
// (BEKLEMEZ) + dakika bütçesi. Reddedilen çağrı İKİSİNDEN DE tüketmez
// (mcp_gate.go dersi: ret bütçe yakarsa istemci kendini sebepsiz kilitler).
// Başarıda release döner; handler defer eder. release idempotent.
//
// Eşzamanlılık reddi Retry-After 1 s taşır (slotun ne zaman boşalacağı
// bilinemez); hız reddi pencere bitişine kalan süreyi.
func (l *promqlLimiter) acquireQuery(user string, cfg promqlConsoleSettings) (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	winStart, remaining := l.window()

	maxConc := cfg.PerUserConcurrency
	if maxConc < 1 {
		maxConc = 1
	}
	if l.inflight[user] >= maxConc {
		return nil, &promqlGuardError{
			Status: http.StatusTooManyRequests, ErrorType: "rate_limited",
			Guardrail: "perUserConcurrency", Limit: maxConc, Unit: "requests",
			RetryAfter: time.Second,
			Msg:        fmt.Sprintf("too many concurrent queries: at most %d per user may run at once — wait for a running query to finish", maxConc),
		}
	}
	perMin := cfg.PerUserPerMin
	if perMin < 1 {
		perMin = 1
	}
	b := promqlRateBucketFor(l.queryRate, user, winStart)
	if b.count >= perMin {
		return nil, &promqlGuardError{
			Status: http.StatusTooManyRequests, ErrorType: "rate_limited",
			Guardrail: "perUserPerMin", Limit: perMin, Unit: "requests/min",
			RetryAfter: remaining,
			Msg:        fmt.Sprintf("query rate limit reached: %d queries per minute per user", perMin),
		}
	}
	b.count++
	l.inflight[user]++

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if n := l.inflight[user] - 1; n > 0 {
				l.inflight[user] = n
			} else {
				delete(l.inflight, user)
			}
		})
	}, nil
}

// promqlMetaMinConcurrency — metadata eşzamanlılık kapısının tabanı.
// v0.10.952 — önceden metadata'da eşzamanlılık kapısı YOKTU: bir kullanıcı
// önbellek ıskasında dakikalık bütçenin tamamını (240) aynı anda uçurabilir,
// pod başına 240 Thanos metadata taraması (her biri 7g pencereli) açabilirdi.
// Yeni ayar alanı YOK (karar 13 blob alanlarını sabitler): kapı
// perUserConcurrency'den türetilir.
const promqlMetaMinConcurrency = 4

// promqlMetaConcurrency — max(4, 2×perUserConcurrency): otomatik tamamlama
// patlamalı gelir (metrik adı + etiket + değer aynı tuş vuruşunda), sorgu
// kapısından geniş ama sınırlı.
func promqlMetaConcurrency(cfg promqlConsoleSettings) int {
	return max(promqlMetaMinConcurrency, 2*cfg.PerUserConcurrency)
}

// acquireMeta — metadata (labels / label values / series) kapısı:
// eşzamanlılık try-acquire (BEKLEMEZ, sorgu slotlarından AYRI sayaç) + ayrı
// ve daha yüksek dakika bütçesi. acquireQuery'nin aynası: eşzamanlılık
// reddi hız kovasından ÖNCE bakılır → ret metaRate'i YAKMAZ. Başarıda
// idempotent release döner; çağıran upstream çağrısı bitince bırakır.
//
// "Önbellek isabeti sayılmaz" (karar 3): C5 bunu cachedJSON'un MISS
// kapanışının İÇİNDEN çağırır — isabet kapanışı hiç çalıştırmaz, yani
// bütçe ve slot yalnız gerçek upstream çağrısında tutulur. Ret
// *promqlGuardError olarak döner; kapanıştan dönen hata errors.As ile
// 429'a çevrilir (singleflight hatayı önbelleğe yazmaz).
func (l *promqlLimiter) acquireMeta(user string, cfg promqlConsoleSettings) (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	winStart, remaining := l.window()

	maxConc := promqlMetaConcurrency(cfg)
	if l.metaInflight[user] >= maxConc {
		return nil, &promqlGuardError{
			Status: http.StatusTooManyRequests, ErrorType: "rate_limited",
			Guardrail: "metaPerUserConcurrency", Limit: maxConc, Unit: "requests",
			RetryAfter: time.Second,
			Msg:        fmt.Sprintf("too many concurrent metadata requests: at most %d per user may run at once", maxConc),
		}
	}
	perMin := cfg.MetaPerUserPerMin
	if perMin < 1 {
		perMin = 1
	}
	b := promqlRateBucketFor(l.metaRate, user, winStart)
	if b.count >= perMin {
		return nil, &promqlGuardError{
			Status: http.StatusTooManyRequests, ErrorType: "rate_limited",
			Guardrail: "metaPerUserPerMin", Limit: perMin, Unit: "requests/min",
			RetryAfter: remaining,
			Msg:        fmt.Sprintf("metadata rate limit reached: %d requests per minute per user", perMin),
		}
	}
	b.count++
	l.metaInflight[user]++

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if n := l.metaInflight[user] - 1; n > 0 {
				l.metaInflight[user] = n
			} else {
				delete(l.metaInflight, user)
			}
		})
	}, nil
}

// ── Metadata uçları (karar 9) ───────────────────────────────────────────────

const (
	// promqlMetaDefaultWindow — start/end verilmezse son 1 saat.
	promqlMetaDefaultWindow = time.Hour
	// promqlMetaMaxLimit — sunucuda zorlanan tavan ve varsayılan; Thanos
	// `limit`'i onurlandırsa da onurlandırmasa da (audit Q12) kesilir.
	promqlMetaMaxLimit = 1000
	// promqlMetaCacheTTL — labels / values / series önbelleği.
	promqlMetaCacheTTL = 60 * time.Second
	// promqlMetaGrid — pencere uçları bu ızgaraya oturur; anahtar
	// (cacheBucket) ve Thanos'a giden sorgu AYNI pencereyi taşır.
	promqlMetaGrid = 30 * time.Second
	// promqlMetaMaxCachedBytes — v0.10.952 — tutulan metadata değer baytı
	// tavanı: upstream gövde tavanı (maxBodyMiB) limit'i yok sayan Thanos'ta
	// sunucu-kesimi için büyük kalır; önbelleğe (L1 bayt tavansız, Redis
	// 180 s) giren gövde ise bununla sınırlı.
	promqlMetaMaxCachedBytes = 4 << 20
)

// promqlTrimMetaStrings — SAF. labels / label values: JSON maliyeti
// (değer + tırnaklar + virgül) budget'ı aşan İLK değerden itibaren kesilir.
// Tek değer bile sığmıyorsa boş liste + true (hata değil: UI "kısmi liste"
// der). Girdi dilimi paylaşılır, kopyalanmaz.
func promqlTrimMetaStrings(vals []string, budget int) ([]string, bool) {
	n := 0
	for i, v := range vals {
		n += len(v) + 3 // tırnaklar + virgül
		if n > budget {
			return vals[:i], true
		}
	}
	return vals, false
}

// promqlTrimMetaSeries — SAF. series: etiket setinin JSON maliyeti
// (her çift için ad + değer + 2×tırnak + iki nokta + virgül) budget'ı aşan
// ilk setten itibaren kesilir.
func promqlTrimMetaSeries(sets []map[string]string, budget int) ([]map[string]string, bool) {
	n := 0
	for i, m := range sets {
		for k, v := range m {
			n += len(k) + len(v) + 6
		}
		if n > budget {
			return sets[:i], true
		}
	}
	return sets, false
}

// promqlMetaWindow — SAF. Sıfır start/end = verilmemiş: end=now,
// start=end-1h. Pencere 30 s ızgarasına oturtulur, sonra maxRange'e
// KELEPÇELENİR (metadata için ret değil: otomatik tamamlama hatası
// editörde gürültüdür; sorgu uçlarındaki ret kuralı sonuçları
// değiştirdiği için vardır). end < start → bad_data.
func promqlMetaWindow(start, end, now time.Time, cfg promqlConsoleSettings) (time.Time, time.Time, error) {
	if end.IsZero() {
		end = now
	}
	if start.IsZero() {
		start = end.Add(-promqlMetaDefaultWindow)
	}
	if end.Before(start) {
		return start, end, &promqlGuardError{Status: http.StatusBadRequest, ErrorType: "bad_data",
			Msg: "end timestamp must not be before start time"}
	}
	start, end = start.Truncate(promqlMetaGrid), end.Truncate(promqlMetaGrid)
	if maxR := cfg.maxRange(); end.Sub(start) > maxR {
		start = end.Add(-maxR)
	}
	return start, end, nil
}

// promqlMetaLimit — ≤0 → 1000; 1000 üstü → 1000. Kelepçe anahtarın
// ÖNÜNDE (api-route SKILL: crafted ?limit= sınırsız ayrı cache girdisi
// basmasın).
func promqlMetaLimit(requested int) int {
	if requested <= 0 || requested > promqlMetaMaxLimit {
		return promqlMetaMaxLimit
	}
	return requested
}

// promqlMetaKeyInput — metadata cevabını değiştiren HER girdi.
type promqlMetaKeyInput struct {
	Kind            string   // "labels" | "label-values" | "series"
	LabelName       string   // yalnız label/{name}/values
	ClusterID       string   // ClusterConfig.EffectiveID()
	URL             string   // ClusterConfig.URL (admin düzenlemesi anahtarı değiştirsin)
	InjectName      string   // EffectiveThanosLabel() adı; "" = enjeksiyon yok
	InjectValue     string   // EffectiveThanosLabel() değeri
	NamespaceFilter string   // bugün konsola uygulanmıyor; uygulanırsa anahtar zaten hazır
	Matches         []string // istemcinin ham match[]'leri (sıra önemsiz)
	Start, End      time.Time
	Limit           int
	PartialResponse bool
}

// promqlMetaCacheKey — SAF (cache_key_test.go deseni: ayrıklık, kararlılık,
// permütasyon değişmezliği). Her alan uzunluk-önekli yazılır: ayraç baytı
// taşıyan bir match[] iki farklı girdiyi aynı akışa katlayamaz. match[]
// sıralanır + tekilleştirilir (küme semantiği; {a,a} ≡ {a}). Token KASITLI
// dışarıda (clusterCfgDigest emsali: dönen token aynı veriyi verir).
func promqlMetaCacheKey(in promqlMetaKeyInput) string {
	h := fnv.New64a()
	var lenBuf [binary.MaxVarintLen64]byte
	put := func(s string) {
		h.Write(lenBuf[:binary.PutUvarint(lenBuf[:], uint64(len(s)))])
		h.Write([]byte(s))
	}
	put(in.Kind)
	put(in.LabelName)
	put(in.ClusterID)
	put(in.URL)
	put(in.InjectName)
	put(in.InjectValue)
	put(in.NamespaceFilter)

	ms := slices.Compact(slices.Sorted(slices.Values(in.Matches))) // kopya; çağıranın dilimi değişmez
	put(strconv.Itoa(len(ms)))
	for _, m := range ms {
		put(m)
	}
	put(cacheBucket(in.Start, in.End))
	put(strconv.Itoa(in.Limit))
	put(strconv.FormatBool(in.PartialResponse))
	return fmt.Sprintf("promql-meta:v947:%s:%s:%016x", in.Kind, in.ClusterID, h.Sum64())
}
