package copilot

// model_tuning.go — v0.10.2375: model-farkında bütçe ve örnekleme
// (operatör 2026-11-19: CoSRE varsayılanı Qwen3.8 Flash Next, "Modelin
// limitlerini kullanalım", "GPU sınırımız yok … en iyi performans ayarlarını
// default yap"). Model adı burada ARANMAZ: her karar modelcaps.Caps'ten gelir;
// yetenek kaydı olmayan model (gemma, bilinmeyen) için her fonksiyon bugünkü
// değeri döndürür — gövde bayt bayt aynı.
//
// ÖNCELİK (bütçe ve sıcaklık aynı sıra): profilin açık değeri > Ayarlar'ın
// küresel değeri (operatör elle verdiyse) > modelin önerisi > yerleşik
// varsayılan (4096 / 0.2). Küresel değer yerleşik varsayılandan AYIRT
// edilebilir (s.maxTokens 0 / s.temperature nil = verilmedi), o yüzden
// operatörün açık seçimi hiçbir zaman sessizce ezilmez.
//
// DETERMİNİSTİK ALT GÖREV: çağıran katı JSON istediyse (WithJSONMode /
// WithJSONSchema — sınıflayıcı, yönlendirici, karar, nl→sorgu) öneri
// UYGULANMAZ: sıcaklık bugünkü yoldan gelir (explainOpenAI 0'a çeker) ve
// örnekleme alanları inmez (presence_penalty açgözlü çözümlemede de logit
// değiştirir; yapısal çıktıyı bozabilir). Sıcaklığı çağrı başına ayarlayan
// başka yer yok; token tavanlı yardımcı çağrılar (brifing başlığı, adım
// özeti) sıradan kuralı izler.

import (
	"context"
	"strings"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
)

// resolveMaxTokens — SAF: çağrının TABAN completion bütçesi (Derin çarpanı ve
// çağrı tavanları sonra uygulanır).
//
//	profil > 0      → profilin değeri; 32768'i aşıyorsa (yalnız çıktı tavanı
//	                  bilinen modelde kaydedilebilir) model tavanına kelepçeli
//	küresel > 0     → operatörün küresel değeri
//	model varsayılanı (düşünme kapalı değilken) → tavanla sınırlı, 4096'dan az değil
//	aksi            → defaultMaxTokens
func resolveMaxTokens(profileMax, globalMax int, caps modelcaps.Caps, thinking string, window int) int {
	switch {
	case profileMax > maxMaxTokens:
		return min(profileMax, modelcaps.MaxTokensCeiling(caps, window, maxMaxTokens))
	case profileMax > 0:
		return profileMax
	case globalMax > 0:
		return globalMax
	case caps.DefaultMaxTokens > 0 && modelcaps.Thinking(thinking) != modelcaps.ThinkingOff:
		return max(defaultMaxTokens, min(caps.DefaultMaxTokens, modelcaps.MaxTokensCeiling(caps, window, maxMaxTokens)))
	}
	return defaultMaxTokens
}

// profileMaxTokensLimit — SAF: profilde kaydedilebilecek en büyük maxTokens.
// Çıktı tavanı bilinmeyen modelde maxMaxTokens (32768) aynen.
func profileMaxTokensLimit(model string) int {
	return modelcaps.MaxTokensCeiling(modelcaps.For(model), 0, maxMaxTokens)
}

// recommendedSampling — çağrı modelin önerilen örneklemesini alır mı. Katı
// JSON isteyen çağrı almaz (dosya başı).
func recommendedSampling(ctx context.Context, caps modelcaps.Caps, thinking string) (modelcaps.Sampling, bool) {
	if jsonLevelRequested(ctx) > jsonNone {
		return modelcaps.Sampling{}, false
	}
	return modelcaps.SamplingFor(caps, modelcaps.Thinking(thinking))
}

// resolveTemperature — SAF: profil > küresel (operatör) > model önerisi >
// defaultTemperature.
func resolveTemperature(profileTemp, globalTemp *float64, rec modelcaps.Sampling, hasRec bool) float64 {
	switch {
	case profileTemp != nil:
		return *profileTemp
	case globalTemp != nil:
		return *globalTemp
	case hasRec:
		return rec.Temperature
	}
	return defaultTemperature
}

// cachedContextWindow — AĞSIZ yürürlükteki pencere: sunucudan daha önce
// okunmuş değer (context_probe.go önbelleği; Ayarlar, wiki bütçesi ve trace
// drill ısıtır) varsa o, yoksa ad tablosu. callSnapshot kilit altında ve her
// çağrıda koşar; burada ağ çağrısı YAPILMAZ. Bayat kayıt da kullanılır:
// sunucunun eski okuması ad tahmininden iyidir.
func cachedContextWindow(cfg ModelProfile) int {
	n, _ := cachedContextWindowInfo(cfg)
	return n
}

// cachedContextWindowInfo — v0.10.2378: cachedContextWindow + pencere
// SUNUCUDAN mı okundu (önbellekteki kayıt "server" kaynaklı). Ad tahmini ve
// önbelleğe düşmüş ad tahmini probed=false.
func cachedContextWindowInfo(cfg ModelProfile) (tokens int, probed bool) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Provider == ProviderOpenAI && base != "" && cfg.Model != "" {
		ctxProbeMu.Lock()
		e, ok := ctxProbeCache[base+"|"+cfg.Model]
		ctxProbeMu.Unlock()
		if ok && e.info.Tokens > 0 {
			return e.info.Tokens, e.info.Source == "server"
		}
	}
	return modelcaps.ContextWindow(cfg.Model), false
}

// ── v0.10.2378 — kanıt bütçesi ölçeği ──────────────────────────────────────

// EvidenceBudget — çağrının çözülen profilinde kanıt bütçesinin girdileri.
type EvidenceBudget struct {
	// Scale — kanıt tavanlarının çarpanı (≥1; modelcaps.EvidenceScale).
	Scale int
	// Window — ölçeğin hesaplandığı yürürlükteki pencere (jeton; 0 = bilinmiyor).
	Window int
	// WindowProbed — pencere geçitten okundu (ad tahmini değil). Ölçeği 1 olan
	// profilde bütün-istem koruması yalnız bu doğruyken pencereye güvenir: ad
	// tablosu belirsiz adlarda bilinçli küçüktür (8192) ve bugün çalışan
	// istemi kesmemeli.
	WindowProbed bool
	// MaxScale — yapılandırılmış profillerin en büyük ölçeği. >1 ise bu
	// kurulumda ölçekli kanıtla kurulmuş istem OLABİLİR; bütün-istem koruması
	// (api/evidence_scale.go) yalnız o zaman devreye girer. Yeteneksiz
	// kurulumda (yalnız gemma vb.) hep 1 — hiçbir istem değişmez.
	MaxScale int
}

// EvidenceBudgetFor — AĞSIZ (cachedContextWindow): çağrının profili için
// kanıt ölçeği. Pencere geçitten daha önce okunduysa o (küçük açılmışsa ölçek
// iner), yoksa ad tablosu. nil servis → ölçek 1.
func (s *Service) EvidenceBudgetFor(ctx context.Context) EvidenceBudget {
	out := EvidenceBudget{Scale: 1, MaxScale: 1}
	if s == nil {
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.resolveProfileLocked(ctx).cfg
	out.Window, out.WindowProbed = cachedContextWindowInfo(p)
	out.Scale = modelcaps.EvidenceScale(modelcaps.For(p.Model), out.Window)
	out.MaxScale = out.Scale
	for _, rt := range s.profiles {
		if rt == nil {
			continue
		}
		if n := modelcaps.EvidenceScale(modelcaps.For(rt.cfg.Model), cachedContextWindow(rt.cfg)); n > out.MaxScale {
			out.MaxScale = n
		}
	}
	return out
}

// EvidenceScaleFor — çağrının profilinde kanıt bütçesi çarpanı (≥1).
// ContextWindowFor / MaxTokensFor'un kardeşi; model adı çağıranda ARANMAZ.
func (s *Service) EvidenceScaleFor(ctx context.Context) int {
	return s.EvidenceBudgetFor(ctx).Scale
}
