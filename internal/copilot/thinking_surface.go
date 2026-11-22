package copilot

// thinking_surface.go — v0.10.2458 (operatör kararı 2026-11-22: "Qwen düşünme
// kipi varsayılan KAPALI"; v0.10.2456 takibi "yüzey başına düşünme").
//
// Her LLM çağrısı bir DÜŞÜNME YÜZEYİNE düşer (niyet sınıflayıcı, guided
// anlatım, sağlık raporu, kısa ✨ açıklama, trace açıklaması, CoSRE serbest
// döngü / araştırma, doğrulayıcı yeniden üretimi, diğer). Yüzey başına ayar:
//
//	off      → enable_thinking=false (varsayılan, her yüzey)
//	on       → enable_thinking=true
//	inherit  → profilin `Thinking` alanı ("" = modele dokunma — v0.10.534'ün
//	           davranışı bayt bayt)
//
// Çözüm sırası: ctx'te AÇIK yüzey (WithThinkingSurface — çağıran yer bilir:
// sağlık raporu anlatımı, doğrulayıcı) > CallMeta.Surface etiketinden SAF
// sınıflama (ClassifyThinkSurface). Ayar tek yerde uygulanır: callSnapshot
// (buffered / akış / araçlı — bütün çağrılar oradan geçer).
//
// Gövdeye ne iner: modelcaps.ExtraBody — yalnız düşünme anahtarı BİLİNEN
// modelde (Qwen3 ailesi, ya da profilde `thinkingSwitch` beyanı: geçit takma
// adı ailesini saklıyorsa). gemma / llama / bilinmeyen modelde gövde
// değişmez; model dalı YOK, istem metinleri aynı.
//
// Neden ayrı system_settings anahtarı (ai_thinking_surfaces), ai_copilot
// blobu değil: o blob her profil yazımında yeniden yazılır ve eski binary
// rolling deploy'da düz alanları okur; bu ayar profillerden bağımsız. Yine de
// LoadPersisted içinde okunur — 30 sn'lik StartConfigRefresh ve
// publishConfigReload("ai") öteki pod'lara aynı yoldan taşır.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

// ThinkSurface — düşünme ayarının yüzey sınıfı (sabit küme; metrik etiketi).
type ThinkSurface string

const (
	ThinkIntent        ThinkSurface = "intent"        // niyet / yönlendirici sınıflayıcı
	ThinkGuided        ThinkSurface = "guided"        // guided anlatım (chat-guided / çekmece)
	ThinkHealthReport  ThinkSurface = "health_report" // servis sağlığı / servis raporu anlatımı
	ThinkExplain       ThinkSurface = "explain"       // kısa ✨ açıklamalar + arka plan açıklayıcıları
	ThinkTrace         ThinkSurface = "trace"         // trace açıklaması (✨ + sohbet)
	ThinkInvestigation ThinkSurface = "investigation" // CoSRE serbest döngü / araştırma / kök neden
	ThinkVerifier      ThinkSurface = "verifier"      // doğrulayıcı düzeltme notuyla yeniden üretim
	ThinkOther         ThinkSurface = "other"         // geri kalan (genel sohbet, wiki, NL sorgu, yoklama…)
)

// Thinking surface modes.
const (
	ThinkModeOff     = "off"
	ThinkModeOn      = "on"
	ThinkModeInherit = "inherit"
)

// ThinkingSettingsKey — system_settings anahtarı.
const ThinkingSettingsKey = "ai_thinking_surfaces"

// ThinkSurfaceInfo — admin görünümü için sabit sıra + açıklama (İngilizce).
type ThinkSurfaceInfo struct {
	ID    ThinkSurface `json:"id"`
	Label string       `json:"label"`
	Hint  string       `json:"hint"`
}

var thinkSurfaceInfos = []ThinkSurfaceInfo{
	{ThinkIntent, "Intent / router classifier", "chat-intent, resource classify, wiki page select"},
	{ThinkGuided, "Guided narration", "chat-guided, drawer and prefetch narration"},
	{ThinkHealthReport, "Health report narration", "guided service health / service report answers"},
	{ThinkExplain, "Short ✨ explains", "explain-* buttons, runbook, insight cards, auto-explain workers"},
	{ThinkTrace, "Trace explain", "explain-trace (button and chat), compare-traces"},
	{ThinkInvestigation, "CoSRE free loop / investigation", "chat agent loop, research, AI SRE investigation, root-cause verdicts"},
	{ThinkVerifier, "Verifier regeneration", "one buffered re-answer after a verifier mismatch (Deep + enforce)"},
	{ThinkOther, "Other", "general chat, wiki / RAG chat, NL query, settings probe, replay judge"},
}

// ThinkSurfaces — sabit sıralı yüzey listesi.
func ThinkSurfaces() []ThinkSurfaceInfo { return append([]ThinkSurfaceInfo(nil), thinkSurfaceInfos...) }

func knownThinkSurface(s ThinkSurface) bool {
	for _, i := range thinkSurfaceInfos {
		if i.ID == s {
			return true
		}
	}
	return false
}

// ValidThinkMode — off | on | inherit.
func ValidThinkMode(m string) bool {
	return m == ThinkModeOff || m == ThinkModeOn || m == ThinkModeInherit
}

// ValidateThinkingModes — bilinmeyen yüzey ya da kip reddedilir.
func ValidateThinkingModes(m map[string]string) error {
	for k, v := range m {
		if !knownThinkSurface(ThinkSurface(k)) {
			return fmt.Errorf("unknown thinking surface: %s", k)
		}
		if !ValidThinkMode(v) {
			return fmt.Errorf("invalid thinking mode for %s: %q (off | on | inherit)", k, v)
		}
	}
	return nil
}

type thinkSurfaceKey struct{}

// WithThinkingSurface — çağıran yer yüzeyi açıkça bildirir (etiket sınıflaması
// yerine). Boş değer ctx'i aynen döndürür.
func WithThinkingSurface(ctx context.Context, s ThinkSurface) context.Context {
	if s == "" {
		return ctx
	}
	return context.WithValue(ctx, thinkSurfaceKey{}, s)
}

// ThinkSurfaceOf — ctx'teki açık yüzey, yoksa CallMeta.Surface sınıflaması.
func ThinkSurfaceOf(ctx context.Context) ThinkSurface {
	if s, ok := ctx.Value(thinkSurfaceKey{}).(ThinkSurface); ok && s != "" {
		return s
	}
	return ClassifyThinkSurface(MetaFromContext(ctx).Surface)
}

// ClassifyThinkSurface — SAF: ai_calls yüzey etiketi → düşünme yüzeyi.
func ClassifyThinkSurface(label string) ThinkSurface {
	l := strings.TrimSpace(label)
	switch l {
	case "chat-intent", "chat-intent-none", "chat-offtopic", "chat-resource-classify", "wiki-select":
		return ThinkIntent
	case "chat-guided", "chat-drawer", "chat-prefetch":
		return ThinkGuided
	case "chat", SurfaceChatResearch, "rootcause-verdict", "rootcause-auto":
		return ThinkInvestigation
	case "compare-traces":
		return ThinkTrace
	case "runbook", "runbook-update", "analyze-service", "deploy-impact", "draft-postmortem",
		"problem-auto-explain", "exception-auto-explain", "cosre-briefing", "anomaly-detail-log-chart",
		"ch-optimize", "suggest-service-tags":
		return ThinkExplain
	case "sre-replay-judge":
		return ThinkOther
	}
	base, _, _ := strings.Cut(l, ":") // "explain-trace:chat", "runbook:chat"
	switch {
	case base == "explain-trace":
		return ThinkTrace
	case strings.HasPrefix(base, "explain-"), base == "runbook", strings.HasPrefix(base, "insight-"):
		return ThinkExplain
	case strings.HasPrefix(base, "sre-"):
		return ThinkInvestigation
	}
	return ThinkOther
}

// ResolveSurfaceThinking — SAF: yüzey kipi + profil ayarı → gövdeye giden
// modelcaps değeri. Kayıtsız / bilinmeyen kip = off (operatör varsayılanı).
func ResolveSurfaceThinking(mode, profileThinking string) string {
	switch mode {
	case ThinkModeOn:
		return string(modelcaps.ThinkingOn)
	case ThinkModeInherit:
		return profileThinking
	}
	return string(modelcaps.ThinkingOff)
}

// thinkingModeFor — ayar tablosundan (yoksa off).
func (s *Service) thinkingModeFor(surface ThinkSurface) string {
	s.thinkMu.RLock()
	defer s.thinkMu.RUnlock()
	if m, ok := s.thinkModes[surface]; ok && ValidThinkMode(m) {
		return m
	}
	return ThinkModeOff
}

// ThinkingModes — her yüzeyin yürürlükteki kipi (kayıtsız = off).
func (s *Service) ThinkingModes() map[string]string {
	out := make(map[string]string, len(thinkSurfaceInfos))
	for _, i := range thinkSurfaceInfos {
		out[string(i.ID)] = s.thinkingModeFor(i.ID)
	}
	return out
}

// SetThinkingModes — canlı tabloyu değiştirir (doğrulama çağıranda).
func (s *Service) SetThinkingModes(m map[string]string) {
	next := make(map[ThinkSurface]string, len(m))
	for k, v := range m {
		next[ThinkSurface(k)] = v
	}
	s.thinkMu.Lock()
	s.thinkModes = next
	s.thinkMu.Unlock()
}

// loadThinkingPersisted — LoadPersisted'ın parçası (açılış + 30 sn yenileme).
func (s *Service) loadThinkingPersisted(ctx context.Context, store SettingsStore) {
	raw, err := store.GetSetting(ctx, ThinkingSettingsKey)
	if err != nil || len(raw) == 0 {
		return
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Printf("[copilot] %s: %v", ThinkingSettingsKey, err)
		return
	}
	if err := ValidateThinkingModes(m); err != nil {
		log.Printf("[copilot] %s: %v — ignored", ThinkingSettingsKey, err)
		return
	}
	s.SetThinkingModes(m)
}

// SaveThinkingModes — kısmi gövde mevcut tabloya birleşir, doğrulanır,
// system_settings'e yazılır, canlı tablo güncellenir. Dönen = tam tablo.
func (s *Service) SaveThinkingModes(ctx context.Context, store SettingsStore, patch map[string]string) (map[string]string, error) {
	if err := ValidateThinkingModes(patch); err != nil {
		return nil, err
	}
	merged := s.ThinkingModes()
	for k, v := range patch {
		merged[k] = v
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	if err := store.PutSetting(ctx, ThinkingSettingsKey, b); err != nil {
		return nil, err
	}
	s.SetThinkingModes(merged)
	return merged, nil
}

// effectiveThinking — callSnapshot'ın tek kararı: profil + yüzey → thinking
// değeri; fromProfile = kip inherit (bilinmeyen aile uyarısı yalnız onda —
// varsayılan off gemma'da her çağrıda uyarı basmasın).
func (s *Service) effectiveThinking(ctx context.Context, p ModelProfile) (thinking string, fromProfile bool) {
	mode := s.thinkingModeFor(ThinkSurfaceOf(ctx))
	return ResolveSurfaceThinking(mode, p.Thinking), mode == ThinkModeInherit
}

// capsForProfile — modelcaps + profil beyanı: `thinkingSwitch` işaretli
// profilde ad tanınmasa da Qwen3 şablon anahtarı gönderilir.
func capsForProfile(p ModelProfile) modelcaps.Caps {
	c := modelcaps.For(p.Model)
	if p.ThinkingSwitch && c.ThinkingSwitch == "" {
		c.ThinkingSwitch = modelcaps.SwitchChatTemplate
	}
	return c
}

// thinkingLabel — SAF: metrik etiketi. on | off | model_default (gövde
// değişmedi, profil boş) | unsupported (modelin anahtarı yok, gövde değişmedi).
func thinkingLabel(c modelcaps.Caps, thinking string) string {
	if thinking == "" {
		return "model_default"
	}
	if c.ThinkingSwitch == "" {
		return "unsupported"
	}
	return thinking
}

// ── öz-gözlem ───────────────────────────────────────────────────────

type thinkStatKey struct {
	Surface  ThinkSurface
	Thinking string
}

type thinkStatVal struct {
	Calls, Errors int64
	TotalMs       int64
}

// ThinkingStat — admin kartının kıyas satırı (bu pod, açılıştan beri).
type ThinkingStat struct {
	Surface  string  `json:"surface"`
	Thinking string  `json:"thinking"`
	Calls    int64   `json:"calls"`
	Errors   int64   `json:"errors"`
	AvgMs    float64 `json:"avgMs"`
}

// thinkingObserve — dağıtıcılar (Explain / StreamText / ChatWithTools /
// ChatWithToolsStream) çağrıdan önce alır, bitince hatayla çağırır.
// Kardinalite: 8 yüzey × 4 düşünme etiketi × 2 durum.
func (s *Service) thinkingObserve(ctx context.Context) func(error) {
	surface := ThinkSurfaceOf(ctx)
	s.mu.RLock()
	p := s.resolveProfileLocked(ctx).cfg
	s.mu.RUnlock()
	th, _ := s.effectiveThinking(ctx, p)
	label := thinkingLabel(capsForProfile(p), th)
	started := time.Now()
	return func(err error) {
		d := time.Since(started)
		selfobs.RecordAIThinkingCall(string(surface), label, err != nil, d)
		k := thinkStatKey{surface, label}
		s.thinkStatsMu.Lock()
		if s.thinkStats == nil {
			s.thinkStats = map[thinkStatKey]*thinkStatVal{}
		}
		v := s.thinkStats[k]
		if v == nil {
			v = &thinkStatVal{}
			s.thinkStats[k] = v
		}
		v.Calls++
		if err != nil {
			v.Errors++
		}
		v.TotalMs += d.Milliseconds()
		s.thinkStatsMu.Unlock()
	}
}

// ThinkingStats — yüzey sırası, sonra düşünme etiketi.
func (s *Service) ThinkingStats() []ThinkingStat {
	s.thinkStatsMu.Lock()
	out := make([]ThinkingStat, 0, len(s.thinkStats))
	for k, v := range s.thinkStats {
		st := ThinkingStat{Surface: string(k.Surface), Thinking: k.Thinking, Calls: v.Calls, Errors: v.Errors}
		if v.Calls > 0 {
			st.AvgMs = float64(v.TotalMs) / float64(v.Calls)
		}
		out = append(out, st)
	}
	s.thinkStatsMu.Unlock()
	order := map[string]int{}
	for i, inf := range thinkSurfaceInfos {
		order[string(inf.ID)] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Surface != out[j].Surface {
			return order[out[i].Surface] < order[out[j].Surface]
		}
		return out[i].Thinking < out[j].Thinking
	})
	return out
}

// thinkState — Service'e gömülü alanlar (kendi kilitleri: callSnapshot s.mu
// RLock'u altında okunur, kilit sırası thinkMu her zaman içte).
type thinkState struct {
	thinkMu      sync.RWMutex
	thinkModes   map[ThinkSurface]string
	thinkStatsMu sync.Mutex
	thinkStats   map[thinkStatKey]*thinkStatVal
}
