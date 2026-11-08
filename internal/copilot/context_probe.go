package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
)

// context_probe.go — v0.10.1991 (operatör: "Coremetry modelin context değerini sunucudan okusun, ona
// göre göndersin; Settings'te otomatik görünsün").
//
// Bağlam penceresi önce MODELİ ÇALIŞTIRAN SUNUCUDAN okunur, olmazsa model adından tahmin edilir
// (modelcaps.ContextWindow):
//   - vLLM / OpenAI uyumlu: GET {baseURL}/models → data[].max_model_len (ya da context_length /
//     max_context_length).
//   - Ollama: POST {kök}/api/show → parameters içindeki num_ctx (yürürlükteki pencere) ve
//     model_info "*.context_length" (modelin üst sınırı). num_ctx yoksa Ollama varsayılanı
//     (ollamaDefaultNumCtx) — uzun prompt sessizce kırpılır; Settings bunu uyarı olarak gösterir.
// Sonuç (baseURL, model) başına 10 dk önbellekte; istek 2 sn'de vazgeçer, hata → ad tahmini.

// ContextWindowInfo — çözülen pencere ve nereden geldiği.
type ContextWindowInfo struct {
	// Tokens — yürürlükteki pencere (0 = bilinmiyor).
	Tokens int `json:"tokens"`
	// Source — "server" (sunucudan okundu) | "name" (model adından tahmin) | "" (bilinmiyor).
	Source string `json:"source"`
	// ModelMax — modelin desteklediği üst sınır (Ollama model_info; biliniyorsa).
	ModelMax int `json:"modelMax,omitempty"`
	// Detail — kısa açıklama (ör. "vLLM max_model_len", "Ollama num_ctx ayarlı değil …").
	Detail string `json:"detail,omitempty"`
}

const (
	contextProbeTTL     = 10 * time.Minute
	contextProbeTimeout = 2 * time.Second
	// ollamaDefaultNumCtx — num_ctx verilmemiş Ollama modelinin varsayılan penceresi (yeni sürümler).
	ollamaDefaultNumCtx = 4096
)

type ctxProbeEntry struct {
	info ContextWindowInfo
	at   time.Time
}

var (
	ctxProbeMu    sync.Mutex
	ctxProbeCache = map[string]ctxProbeEntry{}
)

// ContextWindowFor — çağrının çözülen profilinin yürürlükteki penceresi (jeton; 0 = bilinmiyor).
func (s *Service) ContextWindowFor(ctx context.Context) int {
	return s.ContextWindowInfoFor(ctx).Tokens
}

// ContextWindowInfoFor — ContextWindowFor + kaynak.
func (s *Service) ContextWindowInfoFor(ctx context.Context) ContextWindowInfo {
	if s == nil {
		return ContextWindowInfo{}
	}
	s.mu.RLock()
	rt := s.resolveProfileLocked(ctx)
	var cfg ModelProfile
	var cli *http.Client
	if rt != nil {
		cfg, cli = rt.cfg, s.clientForLocked(rt)
	}
	s.mu.RUnlock()
	if rt == nil {
		return ContextWindowInfo{}
	}
	return probeContextWindow(ctx, cfg, cli, false)
}

// ProfileContextWindow — Settings: profil başına pencere.
type ProfileContextWindow struct {
	ProfileID string            `json:"profileId"`
	Model     string            `json:"model"`
	Window    ContextWindowInfo `json:"window"`
}

// ProbeContextWindows — Settings için bütün profiller; fresh=true önbelleği atlar.
func (s *Service) ProbeContextWindows(ctx context.Context, fresh bool) []ProfileContextWindow {
	if s == nil {
		return nil
	}
	type pc struct {
		cfg ModelProfile
		cli *http.Client
	}
	s.mu.RLock()
	list := make([]pc, 0, len(s.profileOrder))
	for _, id := range s.profileOrder {
		if rt := s.profiles[id]; rt != nil {
			list = append(list, pc{rt.cfg, s.clientForLocked(rt)})
		}
	}
	s.mu.RUnlock()
	out := make([]ProfileContextWindow, 0, len(list))
	for _, p := range list {
		m := p.cfg.Model
		if m == "" {
			m = s.DefaultModels()[p.cfg.Provider]
			p.cfg.Model = m
		}
		out = append(out, ProfileContextWindow{ProfileID: p.cfg.ID, Model: m, Window: probeContextWindow(ctx, p.cfg, p.cli, fresh)})
	}
	return out
}

func probeContextWindow(ctx context.Context, cfg ModelProfile, cli *http.Client, fresh bool) ContextWindowInfo {
	byName := func() ContextWindowInfo {
		if n := modelcaps.ContextWindow(cfg.Model); n > 0 {
			return ContextWindowInfo{Tokens: n, Source: "name", Detail: "model adından tahmin"}
		}
		return ContextWindowInfo{}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Provider != ProviderOpenAI || base == "" || cfg.Model == "" || cli == nil {
		return byName()
	}
	key := base + "|" + cfg.Model
	ctxProbeMu.Lock()
	if e, ok := ctxProbeCache[key]; ok && !fresh && time.Since(e.at) < contextProbeTTL {
		ctxProbeMu.Unlock()
		return e.info
	}
	ctxProbeMu.Unlock()

	pctx, cancel := context.WithTimeout(ctx, contextProbeTimeout)
	defer cancel()
	info, ok := probeOpenAIModels(pctx, cli, base, cfg)
	if !ok {
		info, ok = probeOllamaShow(pctx, cli, base, cfg)
	}
	if !ok {
		info = byName()
	}
	ctxProbeMu.Lock()
	ctxProbeCache[key] = ctxProbeEntry{info: info, at: time.Now()}
	ctxProbeMu.Unlock()
	return info
}

func probeGet(ctx context.Context, cli *http.Client, method, url string, body []byte, cfg ModelProfile) ([]byte, bool) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, false
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	res, err := cli.Do(req)
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return b, err == nil
}

// probeOpenAIModels — vLLM / OpenAI uyumlu /models.
func probeOpenAIModels(ctx context.Context, cli *http.Client, base string, cfg ModelProfile) (ContextWindowInfo, bool) {
	b, ok := probeGet(ctx, cli, http.MethodGet, base+"/models", nil, cfg)
	if !ok {
		return ContextWindowInfo{}, false
	}
	return parseModelsWindow(b, cfg.Model)
}

// parseModelsWindow — SAF: /models yanıtından modelin penceresi.
func parseModelsWindow(b []byte, model string) (ContextWindowInfo, bool) {
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(b, &env) != nil {
		return ContextWindowInfo{}, false
	}
	for _, m := range env.Data {
		if id, _ := m["id"].(string); id != model {
			continue
		}
		for _, k := range []string{"max_model_len", "context_length", "max_context_length", "context_window"} {
			if n := numOf(m[k]); n > 0 {
				return ContextWindowInfo{Tokens: n, Source: "server", Detail: "sunucu /models: " + k}, true
			}
		}
	}
	return ContextWindowInfo{}, false
}

var numCtxRe = regexp.MustCompile(`(?m)^\s*num_ctx\s+(\d+)`)

// probeOllamaShow — Ollama /api/show (kök = baseURL'den "/v1" atılmış).
func probeOllamaShow(ctx context.Context, cli *http.Client, base string, cfg ModelProfile) (ContextWindowInfo, bool) {
	root := strings.TrimSuffix(base, "/v1")
	body, _ := json.Marshal(map[string]string{"model": cfg.Model})
	b, ok := probeGet(ctx, cli, http.MethodPost, root+"/api/show", body, cfg)
	if !ok {
		return ContextWindowInfo{}, false
	}
	return parseOllamaShow(b)
}

// parseOllamaShow — SAF: num_ctx (yürürlükteki) + model_info *.context_length (üst sınır).
func parseOllamaShow(b []byte) (ContextWindowInfo, bool) {
	var env struct {
		Parameters string         `json:"parameters"`
		ModelInfo  map[string]any `json:"model_info"`
	}
	if json.Unmarshal(b, &env) != nil {
		return ContextWindowInfo{}, false
	}
	max := 0
	for k, v := range env.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			max = numOf(v)
		}
	}
	if m := numCtxRe.FindStringSubmatch(env.Parameters); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return ContextWindowInfo{Tokens: n, Source: "server", ModelMax: max, Detail: "Ollama num_ctx"}, true
		}
	}
	if max == 0 && env.Parameters == "" && len(env.ModelInfo) == 0 {
		return ContextWindowInfo{}, false
	}
	eff := ollamaDefaultNumCtx
	if max > 0 && max < eff {
		eff = max
	}
	return ContextWindowInfo{Tokens: eff, Source: "server", ModelMax: max,
		Detail: fmt.Sprintf("Ollama num_ctx ayarlı değil — varsayılan %d varsayıldı; uzun prompt kırpılır (num_ctx'i modelin üst sınırına çekin)", eff)}, true
}

func numOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}
