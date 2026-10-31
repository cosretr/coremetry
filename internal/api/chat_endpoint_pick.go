package api

// chat_endpoint_pick.go — v0.10.1737 (operatör onayı, mockup "Endpoint seç").
// CoSRE v2'de soru TEK bir servisi anıp operasyon / rota söylemiyorsa
// ("orders-api'de ne oluyor", "orders-api yavaş mı") CoSRE servis özetine
// atlamadan tek satır sorar ve cevabın altında sunucu-aramalı bir endpoint
// seçici kart çıkar (data-pick). 1725'in "Takımını seç" deseni (data-scope →
// data-pick); karar SAF needsEndpointPick'te, okuma tek (önbellekli).
//
//   - Servis: sorudaki canlı servis adı (extractServiceEntity — guided'ın
//     çapası), yoksa bağlam çipi (context.service / sayfa servisi).
//   - Pencere: sorunun açık penceresi (guidedRangeSExplicit), yoksa son 1 saat.
//   - Liste: chstore.GetEndpoints (spanmetrics_1m, yalnız giriş span'leri —
//     kind client/producer dışı + http_route; env doluysa /endpoints'in sınırlı
//     ham yolu, v0.8.385). Sıra hata % sonra p95; ilk endpointPickVisible
//     satır karta gider, gerisi kartın aramasıyla (/api/endpoints search,
//     katalog istemciye gitmez).
//   - Kart yok: soru rota / operasyon / trace içeriyor, servis yok, "Hepsini
//     özetle" bayrağı (context.skipEndpointPick), servisin ≤1 endpoint'i var
//     ya da okuma başarısız — akış bugünkü gibi sürer (servis özeti).
// LLM yok: cevap deterministik tek cümle; tur "clarify" kademesi sayılır.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

const (
	// chatPickEventKind — v1 olay türü; yalnız v2 yazıcısına ulaşır (data-pick).
	chatPickEventKind = "pick"
	// endpointPickVisible — kartta aramasız görünen satır sayısı (mockup: 4).
	endpointPickVisible = 4
	// endpointPickPool — sıralama evreni (servisin trafikli endpoint'leri).
	endpointPickPool = 200
	// endpointPickDefaultRangeS — soruda pencere yoksa son 1 saat (operatör).
	endpointPickDefaultRangeS = 3600
	// endpointPickTTL — liste önbelleği (/api/endpoints ile aynı 30 sn).
	endpointPickTTL = 30 * time.Second
)

// endpointPickCtx — needsEndpointPick girdisi (okuma yok).
type endpointPickCtx struct {
	Service   string // çözülmüş servis (soru > bağlam çipi)
	Operation string // context.operation
	Trace     string // context.trace / sayfa trace'i
	Explain   bool   // açıklama / özne bağlamı
	Skip      bool   // "Hepsini özetle" (context.skipEndpointPick)
}

// endpointPickCuePhrases — açık uçlu servis sorusu (katlanmış metinde içerir).
var endpointPickCuePhrases = []string{
	"ne oluyor", "neler oluyor", "ne var", "ne durumda", "iyi mi", "nasıl gidiyor",
	"what's going", "whats going", "what is going", "how is", "endpoint seç", "başka endpoint",
}

// endpointPickCueStems — jeton öneki (yavaş → yavaşladı, incele → inceler).
var endpointPickCueStems = []string{
	"yavaş", "yavas", "incele", "bak", "sorun", "problem", "durum", "sağlık", "saglik",
	"nasıl", "nasil", "slow", "issue", "health", "investigate", "check",
}

// endpointPickVetoStems — belirli bir boyut soran soru (log, pod, deploy…)
// endpoint seçimine çekilmez; bugünkü yolu korur.
var endpointPickVetoStems = []string{
	"log", "pod", "deploy", "trace", "exception", "istisna", "sql", "sorgu", "kafka",
	"metrik", "metric", "cpu", "bellek", "memory", "alarm", "slo", "topoloji", "topology",
	"bağımlı", "bagimli", "dependen", "runbook", "wiki", "jvm", "heap", "thread", "oracle",
	"veritaban", "database", "kuyruk", "queue", "host", "container", "konteyner",
	"sürüm", "versiyon", "version",
}

// endpointPickCue — SAF: soru açık uçlu bir servis sorusu mu (katlanmış metin).
func endpointPickCue(q string) bool {
	f := strings.ToLower(trFold.Replace(q))
	for _, p := range endpointPickCuePhrases {
		if strings.Contains(f, p) {
			return true
		}
	}
	return tokenHasPrefix(guidedTokens(f), endpointPickCueStems...)
}

// questionNamesRoute — SAF: soru bir rota / "FİİL /yol" / 32-hex trace taşıyor mu.
func questionNamesRoute(q string) bool {
	if endpointPathRe.MatchString(q) {
		return true
	}
	for _, f := range strings.Fields(q) {
		if traceIDRe.MatchString(strings.Trim(f, ".,;:!?()[]'\"`")) {
			return true
		}
	}
	return false
}

// needsEndpointPick — SAF: bu v2 sorusunun cevabı endpoint seçici kart mı.
// Servis var + operasyon / rota / trace / açıklama yok + açık uçlu soru + boyut
// sormuyor + "Hepsini özetle" seçilmemiş.
func needsEndpointPick(ctx endpointPickCtx, question string) bool {
	q := strings.TrimSpace(question)
	if ctx.Skip || q == "" || strings.TrimSpace(ctx.Service) == "" || strings.TrimSpace(ctx.Operation) != "" ||
		strings.TrimSpace(ctx.Trace) != "" || ctx.Explain {
		return false
	}
	if questionNamesRoute(q) || !endpointPickCue(q) {
		return false
	}
	f := strings.ToLower(trFold.Replace(q))
	return !tokenHasPrefix(guidedTokens(f), endpointPickVetoStems...)
}

// endpointPickItem — kart satırı (lib/types.ts CosreEndpointPickItem).
type endpointPickItem struct {
	Route  string  `json:"route"`
	RPS    float64 `json:"rps"`
	P95Ms  float64 `json:"p95Ms"`
	ErrPct float64 `json:"errPct"`
}

// endpointPickPayload — data-pick yükü (lib/types.ts CosreEndpointPick).
type endpointPickPayload struct {
	Kind    string             `json:"kind"`
	Service string             `json:"service"`
	Env     string             `json:"env,omitempty"`
	RangeS  int64              `json:"rangeS"`
	Total   int                `json:"total"`
	Capped  bool               `json:"capped,omitempty"`
	Items   []endpointPickItem `json:"items"`
}

// endpointRouteLabel — SAF: satırın rota etiketi ("FİİL /yol" ya da "/yol").
func endpointRouteLabel(r chstore.EndpointRow) string {
	return strings.TrimSpace(r.Method + " " + r.Path)
}

// endpointPickItems — SAF: satırlar → kart öğeleri, hata % azalan, sonra p95
// azalan, sonra rota (kararlı). windowS req/s böleni (≥1).
func endpointPickItems(rows []chstore.EndpointRow, windowS int64) []endpointPickItem {
	if windowS < 1 {
		windowS = 1
	}
	out := make([]endpointPickItem, 0, len(rows))
	for _, r := range rows {
		route := endpointRouteLabel(r)
		if route == "" || r.Calls == 0 {
			continue
		}
		out = append(out, endpointPickItem{Route: route, RPS: float64(r.Calls) / float64(windowS), P95Ms: r.P95Ms, ErrPct: r.ErrorRate})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ErrPct != out[j].ErrPct {
			return out[i].ErrPct > out[j].ErrPct
		}
		if out[i].P95Ms != out[j].P95Ms {
			return out[i].P95Ms > out[j].P95Ms
		}
		return out[i].Route < out[j].Route
	})
	return out
}

// buildEndpointPick — SAF: sıralı öğeler → yük; ≤1 endpoint → ok=false (kart yok).
func buildEndpointPick(service, env string, rangeS int64, items []endpointPickItem, capped bool) (endpointPickPayload, bool) {
	if len(items) < 2 {
		return endpointPickPayload{}, false
	}
	vis := items
	if len(vis) > endpointPickVisible {
		vis = vis[:endpointPickVisible]
	}
	return endpointPickPayload{Kind: "endpoint", Service: service, Env: env, RangeS: rangeS,
		Total: len(items), Capped: capped, Items: append([]endpointPickItem(nil), vis...)}, true
}

// trGenitive — SAF: Türkçe tamlayan eki kesmeyle ("orders-api'nin",
// "checkout'un"). Son ünlüye göre ı/i/u/ü; ünlüyle biten ada "n" kaynaştırması.
// Ünlü yoksa "'in".
func trGenitive(name string) string {
	last, endsVowel := rune(0), false
	rs := []rune(strings.ToLower(trFold.Replace(name)))
	for i := len(rs) - 1; i >= 0; i-- {
		if strings.ContainsRune("aeıioöuü", rs[i]) {
			last = rs[i]
			endsVowel = i == len(rs)-1
			break
		}
	}
	v := map[rune]string{'a': "ı", 'ı': "ı", 'e': "i", 'i': "i", 'o': "u", 'u': "u", 'ö': "ü", 'ü': "ü"}[last]
	if v == "" {
		v = "i"
	}
	if endsVowel {
		return name + "'n" + v + "n"
	}
	return name + "'" + v + "n"
}

// trRangeLocative — SAF: "son 1 saatte" / "son 30 dakikada" / "son 2 günde".
func trRangeLocative(rangeS int64) string {
	switch {
	case rangeS >= 86400 && rangeS%86400 == 0:
		return fmt.Sprintf("son %d günde", rangeS/86400)
	case rangeS >= 3600 && rangeS%3600 == 0:
		return fmt.Sprintf("son %d saatte", rangeS/3600)
	}
	m := rangeS / 60
	if m < 1 {
		m = 1
	}
	return fmt.Sprintf("son %d dakikada", m)
}

// endpointPickText — SAF: kartın üstündeki tek cümle.
func endpointPickText(p endpointPickPayload) string {
	n := fmt.Sprintf("%d", p.Total)
	if p.Capped {
		n += "+"
	}
	return fmt.Sprintf("%s %s trafiği olan %s endpoint'i var. Hangisini inceleyeyim?", trGenitive(p.Service), trRangeLocative(p.RangeS), n)
}

// endpointPickRangeS — SAF: sorunun açık penceresi, yoksa son 1 saat.
func endpointPickRangeS(question string) int64 {
	if v, ok := guidedRangeSExplicit(normalizeGuidedMsg(question)); ok && v > 0 {
		return v
	}
	return endpointPickDefaultRangeS
}

// endpointPickKey — SAF: tüm girdiler tek FNV özetinde (NUL ayraçlı; dakika
// kovası), v0.5.187 çapraz zehirlenme sınıfı yok.
func endpointPickKey(service, env string, fromMin, toMin int64) string {
	h := fnv.New64a()
	for _, p := range []string{service, env, fmt.Sprint(fromMin), fmt.Sprint(toMin)} {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("cosre:eppick:v1:%x", h.Sum64())
}

// endpointPickRows — servisin trafikli endpoint'leri (önbellekli, dakika kovası).
func (s *Server) endpointPickRows(ctx context.Context, service, env string, from, to time.Time) ([]chstore.EndpointRow, error) {
	from, to = from.Truncate(time.Minute), to.Truncate(time.Minute)
	key := endpointPickKey(service, env, from.Unix()/60, to.Unix()/60)
	body, _, err := s.cachedJSON(ctx, key, endpointPickTTL, false, func(ctx context.Context) (any, error) {
		return s.store.GetEndpoints(ctx, chstore.EndpointsQuery{From: from, To: to, Service: service, Env: env,
			Limit: endpointPickPool, Sort: "calls", Dir: "desc", SkipStatus: true})
	})
	if err != nil {
		return nil, err
	}
	var rows []chstore.EndpointRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// endpointPickTierReq — kademenin istek alanları.
type endpointPickTierReq struct {
	Msgs     []copilot.ChatMessage
	Pick     endpointPickCtx // Service = bağlam servisi (sorudaki ad önce gelir)
	Env      string
	AnchorTo time.Time
}

// chatEndpointPickTier — v2: kart gerekiyorsa tek cümle + data-pick yayınlar
// (handled=true). Gerekmiyorsa / okunamadıysa handled=false, akış aynen.
func (s *Server) chatEndpointPickTier(ctx context.Context, emit func(string, any), rq endpointPickTierReq) bool {
	q := strings.TrimSpace(lastUserText(rq.Msgs))
	if q == "" || rq.Pick.Skip || !endpointPickCue(q) { // ucuz ön kapı: katalog okunmadan
		return false
	}
	pc := rq.Pick
	if svc := extractServiceEntity(normalizeGuidedMsg(q), s.guidedServiceNames(ctx), s.guidedEnvNames(ctx)); svc != "" {
		pc.Service = svc
	}
	if !needsEndpointPick(pc, q) {
		return false
	}
	rangeS := endpointPickRangeS(q)
	to := rq.AnchorTo
	if to.IsZero() {
		to = time.Now()
	}
	from := to.Add(-time.Duration(rangeS) * time.Second)
	rows, err := s.endpointPickRows(ctx, pc.Service, rq.Env, from, to)
	if err != nil {
		return false // okunamadı → bugünkü servis özeti (kart uydurulmaz)
	}
	items := endpointPickItems(rows, rangeS)
	p, ok := buildEndpointPick(pc.Service, rq.Env, rangeS, items, len(rows) >= endpointPickPool)
	if !ok {
		return false
	}
	emit("answer", map[string]any{"text": endpointPickText(p)})
	emit("pick", p)
	return true
}
