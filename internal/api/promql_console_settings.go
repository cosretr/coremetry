package api

// promql_console_settings.go — v0.10.952 (PromQL konsolu, spec C3; karar 3,
// 4, 7, 13 — docs/promql-console/audit.md §6 "Where configuration lives").
//
// Thanos PromQL konsolunun korkulukları TEK system_settings blobunda:
// anahtar `promql_console` (CLAUDE.md invariant 6). Boot'ta hidre edilir,
// 30 s'de bir yenilenir (trace_root_def kablosu, main.go), PUT anında bu
// pod'da canlı değiştirilir ve publishConfigReload ile peer pod'lara <50 ms
// yayılır (reloadConfigOnSignal "promql_console" case'i, cache.go —
// TestEveryConfigReloadTopicHasAListener bu eşleşmeyi kapıyor). Config
// import'u da konuyu yeniden yayınlar (config_iox.go).
//
//   GET /api/settings/promql-console   (tüm roller: konsol sınırlarını
//                                       GÖSTERİR — viewer boş sayfa değil
//                                       değer görür; token yok, gizli yok)
//   PUT /api/settings/promql-console   (admin; audit
//                                       settings.promql_console.update)
//
// Rotalar BU dosyada KAYITLI DEĞİL: C5'in rota dosyası
// (promql_console_routes.go) bağlar — GET → s.getPromQLConsoleSettings
// (kapısız), PUT → auth.RequireRole(auth.RoleAdmin, s.putPromQLConsoleSettings).
// Kayıt satırını buraya YORUM olarak bile yazmıyoruz: make audit CHECK 7
// `mux.Handle…("METHOD /path"` kalıbını yorum dahil sayar ve iki dosyada
// görünce "duplicate route" 🔴 basar.
//
// Neden `thanos_clusters` blobuna alan eklenmedi: o blobun GET'i admin-only
// (token taşıyor) ve PUT'u bütün blobu değiştiriyor — konsol sınırlarını
// editor'e göstermek için token'lı blobu açmak ya da bir sınır değişikliği
// için bütün cluster listesini yeniden yazmak zorunda kalırdık.
//
// Neden durum Server alanında DEĞİL de paket düzeyinde: Server alanları
// api.go'da bildiriliyor ve api.go BÜYÜMEZ (TestApiGoDoesNotGrow). Emsal:
// exceptionTriageCfg (exception_triage.go), var traceBackfill.
//
// Sıfır / aralık dışı / bozuk blob → KORUNAN varsayılan (VictoriaMetrics
// RateWindowFloorS emsali): okuma yolu asla sıfır timeout'lu ya da sınırsız
// seri tavanlı bir konsol görmez. PUT ise aralık dışını 400 ile REDDEDER —
// okuma yolundaki kelepçe eski/bozuk satırlar içindir, operatörün yazım
// hatasını gizlemek için değil (exception_triage.go duruşu).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// promqlConsoleSettingKey — system_settings anahtarı (tek JSON blob).
const promqlConsoleSettingKey = "promql_console"

// promqlConsoleSettings — `promql_console` blobu. JSON alan adları FE
// (lib/types.ts) ve Settings → Remote clusters paneliyle sözleşmedir.
type promqlConsoleSettings struct {
	TimeoutS           int  `json:"timeoutS"`           // 5–120, vars. 30
	MaxRangeH          int  `json:"maxRangeH"`          // 1–720, vars. 168 (7g)
	MinStepS           int  `json:"minStepS"`           // 1–3600, vars. 15
	MaxPointsPerSeries int  `json:"maxPointsPerSeries"` // 100–11000, vars. 11000
	TargetPoints       int  `json:"targetPoints"`       // 100–11000, vars. 1000 (≤ maxPointsPerSeries)
	MaxSeries          int  `json:"maxSeries"`          // 10–2000, vars. 500
	MaxBodyMiB         int  `json:"maxBodyMiB"`         // 1–128, vars. 32
	PerUserConcurrency int  `json:"perUserConcurrency"` // 1–10, vars. 2
	PerUserPerMin      int  `json:"perUserPerMin"`      // 1–600, vars. 30
	MetaPerUserPerMin  int  `json:"metaPerUserPerMin"`  // 1–6000, vars. 240
	PartialResponse    bool `json:"partialResponse"`    // vars. false (banka: kısmi veri yerine hata)
}

// promqlConsoleIntField — tamsayı alanlarının TEK tablosu: normalize,
// PUT doğrulaması ve GET'in `bounds` alanı buradan okur; sınır kodda ve
// UI'da ayrışamaz (correlation_link.go "listeyi SUNUCU verir" dersi).
type promqlConsoleIntField struct {
	name     string
	min, max int
	def      int
	ptr      func(*promqlConsoleSettings) *int
}

// promqlConsoleIntFields — sıra = GET `bounds` ve hata mesajı sırası.
//
// maxPointsPerSeries alt ucu 100: `points` isteği 100..maxPointsPerSeries
// aralığına kelepçelenir (karar 4); tavan 100'ün altına inerse aralık
// ters döner. perUserPerMin / metaPerUserPerMin üst uçları spec'te yok —
// cömert ama SONSUZ DEĞİL (exception_triage.go "100.000/dk kapıyı fiilen
// kapatır" gerekçesi): 600/dk tek kullanıcıya saniyede 10 sorgu, 6000/dk
// otomatik tamamlamaya saniyede 100 istek.
var promqlConsoleIntFields = []promqlConsoleIntField{
	{"timeoutS", 5, 120, 30, func(c *promqlConsoleSettings) *int { return &c.TimeoutS }},
	{"maxRangeH", 1, 720, 168, func(c *promqlConsoleSettings) *int { return &c.MaxRangeH }},
	{"minStepS", 1, 3600, 15, func(c *promqlConsoleSettings) *int { return &c.MinStepS }},
	{"maxPointsPerSeries", 100, 11000, 11000, func(c *promqlConsoleSettings) *int { return &c.MaxPointsPerSeries }},
	{"targetPoints", 100, 11000, 1000, func(c *promqlConsoleSettings) *int { return &c.TargetPoints }},
	{"maxSeries", 10, 2000, 500, func(c *promqlConsoleSettings) *int { return &c.MaxSeries }},
	{"maxBodyMiB", 1, 128, 32, func(c *promqlConsoleSettings) *int { return &c.MaxBodyMiB }},
	{"perUserConcurrency", 1, 10, 2, func(c *promqlConsoleSettings) *int { return &c.PerUserConcurrency }},
	{"perUserPerMin", 1, 600, 30, func(c *promqlConsoleSettings) *int { return &c.PerUserPerMin }},
	{"metaPerUserPerMin", 1, 6000, 240, func(c *promqlConsoleSettings) *int { return &c.MetaPerUserPerMin }},
}

// defaultPromQLConsoleSettings — korunan varsayılanlar (spec karar 13).
// partialResponse=false: Thanos'a açıkça gönderilir (karar 5) — bir store
// düştüğünde sessiz kısmi veri yerine hata.
func defaultPromQLConsoleSettings() promqlConsoleSettings {
	var c promqlConsoleSettings
	for _, f := range promqlConsoleIntFields {
		*f.ptr(&c) = f.def
	}
	return c
}

// normalizePromQLConsoleSettings — SAF okuma-yolu kelepçesi: aralık dışı
// (sıfır dahil) her alan varsayılanına döner; targetPoints tavanı aşamaz.
func normalizePromQLConsoleSettings(c promqlConsoleSettings) promqlConsoleSettings {
	for _, f := range promqlConsoleIntFields {
		if v := f.ptr(&c); *v < f.min || *v > f.max {
			*v = f.def
		}
	}
	// Çapraz alan: operatör tavanı 500'e çekip hedefi varsayılanda (1000)
	// bıraktıysa otomatik adım tavandan fazla nokta istemesin.
	if c.TargetPoints > c.MaxPointsPerSeries {
		c.TargetPoints = c.MaxPointsPerSeries
	}
	return c
}

// validatePromQLConsoleSettings — SAF PUT doğrulaması. 0 = "varsayılanı
// kullan" (geçerli); sıfır olmayan aralık dışı değer 400 olur. Bütün
// hatalar tek mesajda (operatör formu bir kerede düzeltsin). Dönen değer
// normalize EDİLMİŞ blobdur — kalıcı yazılan ve canlı yayınlanan budur.
func validatePromQLConsoleSettings(in promqlConsoleSettings) (promqlConsoleSettings, error) {
	var bad []string
	for _, f := range promqlConsoleIntFields {
		if v := *f.ptr(&in); v != 0 && (v < f.min || v > f.max) {
			bad = append(bad, fmt.Sprintf("%s %d–%d olmalı (0 = varsayılan %d)", f.name, f.min, f.max, f.def))
		}
	}
	if len(bad) > 0 {
		return in, fmt.Errorf("geçersiz promql_console ayarı: %s", strings.Join(bad, "; "))
	}
	out := normalizePromQLConsoleSettings(in)
	// Çapraz alan PUT'ta REDDEDİLİR (okuma yolu kelepçeler): açıkça yazılmış
	// bir hedefin sessizce kırpılması operatöre yalan söylerdi.
	if in.TargetPoints != 0 && in.TargetPoints > out.MaxPointsPerSeries {
		return in, fmt.Errorf("geçersiz promql_console ayarı: targetPoints (%d) maxPointsPerSeries (%d) değerini aşamaz",
			in.TargetPoints, out.MaxPointsPerSeries)
	}
	return out, nil
}

// decodePromQLConsoleSettings — SAF: system_settings ham değeri → normalize
// blob. Yok / boş / bozuk JSON → varsayılanlar (boot'u bir ayar okumasına
// bağlamıyoruz; operatör UI'dan yeniden yazar — ai_budget.go duruşu).
func decodePromQLConsoleSettings(raw []byte) promqlConsoleSettings {
	var c promqlConsoleSettings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			c = promqlConsoleSettings{}
		}
	}
	return normalizePromQLConsoleSettings(c)
}

// Birim yardımcıları — C5 el yazımı çarpım yapmasın (s/h/MiB karışıklığı).
func (c promqlConsoleSettings) timeout() time.Duration {
	return time.Duration(c.TimeoutS) * time.Second
}
func (c promqlConsoleSettings) maxRange() time.Duration {
	return time.Duration(c.MaxRangeH) * time.Hour
}
func (c promqlConsoleSettings) minStep() time.Duration {
	return time.Duration(c.MinStepS) * time.Second
}
func (c promqlConsoleSettings) maxBodyBytes() int64 { return int64(c.MaxBodyMiB) << 20 }

// promqlConsoleCfg — süreç-genelinde tek kopya, atomik (exceptionTriageCfg
// emsali). Konsol her istekte okur; CH okuması hot-path'e girmez.
var promqlConsoleCfg atomic.Pointer[promqlConsoleSettings]

// currentPromQLConsoleSettings — hiç hidre edilmemişse varsayılanlar
// (test ikilisi Store'a hiç bağlanmaz; saf testler bu daldan geçer).
func currentPromQLConsoleSettings() promqlConsoleSettings {
	if c := promqlConsoleCfg.Load(); c != nil {
		return *c
	}
	return defaultPromQLConsoleSettings()
}

// setPromQLConsoleSettings — normalize EDİLMİŞ hâlini yayınlar; okuyucu
// asla aralık dışı bir değer görmez.
func setPromQLConsoleSettings(c promqlConsoleSettings) {
	n := normalizePromQLConsoleSettings(c)
	promqlConsoleCfg.Store(&n)
}

// LoadPromQLConsoleSettings — boot hidrasyonu + yenileme tiki + peer
// sinyali (reloadConfigOnSignal). Okuma HATASINDA son iyi değer korunur:
// geçici bir CH kesintisi limitleri sessizce varsayılana döndürmesin
// (operatör sıkılaştırdıysa gevşerdi). Anahtar YOKSA varsayılanlar.
func (s *Server) LoadPromQLConsoleSettings(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	raw, err := s.store.GetSetting(ctx, promqlConsoleSettingKey)
	if err != nil {
		log.Printf("[promql] promql_console ayar okuması: %v (son değer korunuyor)", err)
		return
	}
	setPromQLConsoleSettings(decodePromQLConsoleSettings(raw))
}

// StartPromQLConsoleSettingsRefresh — çok-pod yakınsaması (pub/sub
// kaçırılırsa ≤30 s). Leader gerekmez: bu bir OKUMA.
func (s *Server) StartPromQLConsoleSettingsRefresh(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.LoadPromQLConsoleSettings(ctx)
		}
	}
}

// promqlSettingBound — GET `bounds` girdisi (UI input min/max).
type promqlSettingBound struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// promqlConsoleSettingsResponse — GET/PUT cevabı: ayar alanları DÜZ
// (gömülü; PUT'a geri gönderilebilir — decoder bilinmeyen `defaults` /
// `bounds` alanlarını yok sayar) + varsayılanlar + sınırlar.
type promqlConsoleSettingsResponse struct {
	promqlConsoleSettings
	Defaults promqlConsoleSettings         `json:"defaults"`
	Bounds   map[string]promqlSettingBound `json:"bounds"`
}

func newPromQLConsoleSettingsResponse(c promqlConsoleSettings) promqlConsoleSettingsResponse {
	b := make(map[string]promqlSettingBound, len(promqlConsoleIntFields))
	for _, f := range promqlConsoleIntFields {
		b[f.name] = promqlSettingBound{Min: f.min, Max: f.max}
	}
	return promqlConsoleSettingsResponse{promqlConsoleSettings: c, Defaults: defaultPromQLConsoleSettings(), Bounds: b}
}

// getPromQLConsoleSettings — KAYITLI blob (ayar yüzeyi kaydedileni
// göstermeli; trace_root_def duruşu). Store yok / okuma hatası → bu pod'un
// CANLI değeri: ham CH hatası viewer'a yansıtılmaz (writeErr varsayılan
// dalı host/port sızdırır — api-route SKILL "Sızıntı uyarısı").
func (s *Server) getPromQLConsoleSettings(w http.ResponseWriter, r *http.Request) {
	c := currentPromQLConsoleSettings()
	if s != nil && s.store != nil {
		raw, err := s.store.GetSetting(r.Context(), promqlConsoleSettingKey)
		if err == nil {
			c = decodePromQLConsoleSettings(raw)
		} else {
			log.Printf("[promql] promql_console GET okuması: %v (canlı değer dönüyor)", err)
		}
	}
	writeJSON(w, newPromQLConsoleSettingsResponse(c))
}

// promqlConsoleSettingsMaxBody — PUT gövde tavanı; blob ~300 bayt.
const promqlConsoleSettingsMaxBody = 64 << 10

// putPromQLConsoleSettings — doğrula → kalıcı yaz → bu pod'da canlı değiştir
// → peer'lara yayınla → audit. Yazım isteğin iptaline bağlı DEĞİL
// (context.WithoutCancel, thanos_identity.go emsali): istemci koparsa yarım
// kalmış bir "kaydedildi mi?" durumu olmasın.
func (s *Server) putPromQLConsoleSettings(w http.ResponseWriter, r *http.Request) {
	var in promqlConsoleSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, promqlConsoleSettingsMaxBody)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c, err := validatePromQLConsoleSettings(in)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu bağlı değil")
		return
	}
	raw, err := json.Marshal(c)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.PutSetting(context.WithoutCancel(r.Context()), promqlConsoleSettingKey, raw); err != nil {
		writeErr(w, err)
		return
	}
	setPromQLConsoleSettings(c) // bu pod anında; peer'lar sinyalde (≤30 s tikte yedek)
	s.publishConfigReload(r.Context(), "promql_console")
	s.audit(r, "settings.promql_console.update", "settings", promqlConsoleSettingKey, string(raw))
	log.Printf("[settings] promql_console: %s", raw)
	writeJSON(w, newPromQLConsoleSettingsResponse(c))
}
