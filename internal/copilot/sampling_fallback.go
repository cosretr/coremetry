package copilot

// sampling_fallback.go — v0.10.2375: model-farkında örnekleme alanlarının
// (top_p / top_k / presence_penalty; modelcaps/tuning.go) GÜVENLİ DÜŞÜŞÜ.
//
// Alanlar model kartından geliyor ve prod geçidine karşı doğrulanamadı;
// openai-uyumlu geçit bilinmeyen alanı 400/422 ile reddedebilir. Kural: bir
// çağrı bu eklemeler yüzünden ASLA düşmez.
//
//	1. Alan taşıyan /chat/completions isteği 400 ya da 422 alırsa aynı istek
//	   alanlar SÖKÜLEREK bir kez yeniden denenir.
//	2. Yeniden deneme başarırsa (base URL, model) softFieldTTL boyunca
//	   "desteklemiyor" diye hatırlanır, tek satır loglanır; callSnapshot o
//	   süre alanları hiç eklemez. Süre dolunca yeniden yoklanır (geçit
//	   güncellenmiş olabilir) — karar kalıcı değil.
//	3. Yeniden deneme de başarısızsa hata alanlarla ilgili değildi (bağlam
//	   taşması, bozuk şema…): İLK yanıt aynen döner, karar yazılmaz. JSON
//	   merdiveninin kuralı (provider_calls.go explainOpenAIAtLevel) aynen.
//
// Taşıyıcıda (tracedTransport.RoundTrip) durur: eşzamanlılık kapısının emsali
// (provider_gate.go) — tek atış, akış ve araçlı döngü aynı cfg.HTTPClient'tan
// geçer, kural tek yerde ve atlanamaz. Sağlayıcı paketi durum tutmaz, değişmedi.
//
// temperature burada YOK: çekirdek alan, her openai-uyumlu uç tanır.

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/ai/provider"
)

const (
	// softFieldTTL — "desteklemiyor" kararının ömrü. Kota kesicisinin 1 saatlik
	// penceresiyle aynı: en kötü durumda (base, model) başına saatte bir boşa
	// istek.
	softFieldTTL = time.Hour
	// softFieldMaxKeys — hafıza tavanı (anahtar = yapılandırılmış profil ucu;
	// pratikte bir avuç). Dolarsa süresi geçenler, yetmezse hepsi atılır.
	softFieldMaxKeys = 256
	// softFieldRespBytes — saklanan ilk yanıt gövdesi tavanı (sağlayıcının
	// maxRespBytes'ı ile aynı).
	softFieldRespBytes = 1 << 20
)

// softFieldMemory — (base URL, model) → kararın bittiği an.
type softFieldMemory struct {
	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time // testte sahte saat
}

func newSoftFieldMemory() *softFieldMemory {
	return &softFieldMemory{until: map[string]time.Time{}, now: time.Now}
}

// softFields — süreç-genelinde tek hafıza (gate ile aynı desen).
var softFields = newSoftFieldMemory()

// blocked — bu uç alanları şu an reddediyor sayılıyor mu.
func (m *softFieldMemory) blocked(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.until[key]
	if !ok {
		return false
	}
	if !m.now().Before(t) {
		delete(m.until, key)
		return false
	}
	return true
}

func (m *softFieldMemory) mark(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if len(m.until) >= softFieldMaxKeys {
		for k, t := range m.until {
			if !now.Before(t) {
				delete(m.until, k)
			}
		}
		if len(m.until) >= softFieldMaxKeys {
			m.until = map[string]time.Time{}
		}
	}
	m.until[key] = now.Add(softFieldTTL)
}

// softFieldKey — profilin base URL'sinden hafıza anahtarı. Uç tabanı
// sağlayıcının URL kuralından (provider/endpoint.go) gelir ki callSnapshot'ın
// sorduğu anahtar taşıyıcının isteğin gerçek URL'sinden yazdığıyla ayrışmasın.
func softFieldKey(base, model string) string {
	return provider.OpenAIChatEndpointKeyForBase(base) + "|" + model
}

// softFieldVerdictStatus — "bu parametreyi anlamadım" ailesi: 400 (bilinmeyen
// parametre) ve 422 (gövde doğrulaması). 401/403/404/413/429 ve 5xx başka
// şeyler söyler; yeniden deneme onları düzeltmez.
func softFieldVerdictStatus(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusUnprocessableEntity
}

// softFieldRoundTrip — kapı + düşüş. Alan taşımayan istek (bugün: yetenek
// kaydı olmayan her model) için gatedRoundTrip'in aynısı.
func softFieldRoundTrip(rt http.RoundTripper, r *http.Request) (*http.Response, error) {
	resp, err := gatedRoundTrip(rt, r)
	if err != nil || !softFieldVerdictStatus(resp.StatusCode) || r.Method != http.MethodPost || r.GetBody == nil {
		return resp, err
	}
	endpoint, ok := provider.OpenAIChatEndpointKey(r.URL)
	if !ok {
		return resp, nil
	}
	stripped, model, ok := strippedRequestBody(r)
	if !ok {
		return resp, nil
	}
	// İlk yanıtı sakla ve KAPAT: kapı slotu gövde kapanınca bırakılır; açık
	// tutup yeniden denemek arka plan sınıfında (tavan 1) kendini kilitlerdi.
	first, _ := io.ReadAll(io.LimitReader(resp.Body, softFieldRespBytes))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(first))

	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(stripped))
	r2.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(stripped)), nil }
	r2.ContentLength = int64(len(stripped))
	resp2, err2 := gatedRoundTrip(rt, r2)
	if err2 != nil {
		return resp, nil
	}
	if resp2.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp2.Body, softFieldRespBytes))
		_ = resp2.Body.Close()
		return resp, nil
	}
	softFields.mark(endpoint + "|" + model)
	log.Printf("[copilot] %s model %q örnekleme alanlarını (%s) reddetti (%d) — alansız yeniden denendi, %s boyunca gönderilmeyecek",
		r.URL.Host, model, strings.Join(modelcaps.SamplingKeys(), ", "), resp.StatusCode, softFieldTTL)
	return resp2, nil
}

// strippedRequestBody — isteğin gövdesinden örnekleme alanları sökülmüş hâli;
// ok=false: gövde okunamadı ya da alan taşımıyor.
func strippedRequestBody(r *http.Request) ([]byte, string, bool) {
	rc, err := r.GetBody()
	if err != nil {
		return nil, "", false
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, "", false
	}
	return modelcaps.StripSampling(raw)
}
