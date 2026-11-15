package api

// chat_resource_classify.go — v0.10.1442 (CoSRE kaynak incelemesi R3).
// Operatör: "CoSRE sorulan sorulara frontier model gibi anlamlandırarak gerekli
// toolset'lerini koşsun … bir pod'un CPU/memory metriklerini grafik olarak
// getirme; gerekirse GC'sine, JVM metriklerine bakıp LLM'e yorumlatma."
//
// Boşluk: R1/R2'nin jeton router'ı (routeResourceInvestigation) sözlüğüne
// oturmayan kaynak sorularını ("orders-api'nin işlemcisi zorlanıyor mu",
// "<pod> sürekli kapanıp açılıyor", "payments deployment'ı hafızayı şişirdi mi")
// kaçırıyor; soru niyet kademesine (copilot_intent.go) düşüp genel
// sınıflandırıcının kaba pod_health / service_health eşlemesine kalıyordu.
//
// Karar (R3): YENİ kademe YOK (kademe sırası pinli). Niyet kademesinin İÇİNDE,
// mesaj bir kaynak ismi taşıyorsa (hasResourceNoun) genel sınıflandırıcıdan
// ÖNCE dar bir JSON çağrısı (systemResourceClassify, yüzey
// chat-resource-classify, aisurface RoleSubCall): şema {intent ∈
// [resource_investigation, metric_chart, pod_health, none], target,
// targetKind, family, rangeS, confidence}. Kabul kapıları (SAF,
// decideResourceClassify):
//   - confidence ≥ 0.6; bozuk JSON / bilinmeyen niyet / none → reddet,
//   - hedef CANLI doğrulanır: servis → canlı katalog (matchLiveName) + soruda
//     geçiyor ya da ekran servisi; pod → soruda jeton olarak geçiyor + pod adı
//     şekli + Thanos kube_pod_info (son 24 sa; demetle AYNI 60 sn önbellek)
//     ya da Thanos yok/okunamadıysa sahibi canlı servis; workload → canlı
//     servis adı ya da Thanos'ta pod'u var. Doğrulanamayan hedef → reddet,
//   - grafik fiili metric_chart'ı kazandırır (R1 / 1386 D2); fiilsiz
//     metric_chart reddedilir; resource_investigation router'ın vetolarına
//     (resourceVetoed: takım/çift/topoloji/log/deploy/trace/db/…) uyar.
// Reddedilen her şey none'dır ve MEVCUT sınıflandırıcı yolu hiç değişmeden
// koşar. Serbest PromQL YOK: sınıflandırıcı yalnız beyaz listeli niyet + aile
// seçer; sorguları deterministik demet (resource_bundle.go / resource_jvm.go /
// metric_chart_templates.go) kurar. Önbellek 10 dk, anahtar tüm girdiler
// (normalize soru, ekran servisi, sistem istemi).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

const (
	resClassifyTool          = "resource_classify"
	resClassifyMinConfidence = 0.6
	resClassifyCacheTTL      = 10 * time.Minute
	resClassifyMaxRunes      = 1500
)

// resClassifyIntents — sınıflandırıcının seçebileceği niyetler (prompts.go
// systemResourceClassify listesiyle birebir; sıra şemada sabit).
var resClassifyIntents = []string{string(guidedResourceInvestigation), string(guidedMetricChart), string(guidedPodHealth), "none"}

// resClassifyFamilies — geçerli aileler (route.ResFamily).
var resClassifyFamilies = map[string]bool{resFamCPU: true, resFamMemory: true, resFamRestarts: true, resFamJVM: true, resFamAll: true}

// resClassifyChartTemplate — fiilli soruda aile → 1386 şablonu (beyaz liste;
// restart/all için grafik şablonu yok → reddet).
var resClassifyChartTemplate = map[string]string{resFamCPU: mcCPU, resFamMemory: mcMemory, resFamJVM: mcJVMHeap}

func resourceClassifySchema() map[string]any {
	return objSchema(map[string]any{
		"intent": enumProp(resClassifyIntents),
		"target": strProp(), "targetKind": strProp(), "family": strProp(),
		"rangeS": numProp(), "confidence": numProp(),
	})
}

type resClassifyJSON struct {
	Intent     string  `json:"intent"`
	Target     string  `json:"target"`
	TargetKind string  `json:"targetKind"`
	Family     string  `json:"family"`
	RangeS     float64 `json:"rangeS"`
	Confidence float64 `json:"confidence"`
}

// hasResourceNoun — SAF: dar sınıflandırıcının ucuz ön kapısı. R1/R2 kaynak
// sözlüğü (pod bağlamıyla — "ölüyor/düşüyor" da sayılır) + router'ın aile
// sözcüğü saymadığı kaynak isimleri (pod, konteyner, kapasite, replika, çekirdek).
func hasResourceNoun(toks []string) bool {
	return resourceFamilyOf(toks, true) != "" ||
		tokenHasPrefix(toks, "pod", "konteyner", "container", "kapasite", "capacity", "replika", "replica", "çekirdek", "cekirdek")
}

var resClassifySpaceRe = regexp.MustCompile(`\s+`)

// resClassifyNormalize — SAF: önbellek anahtarı VE modele giden metin aynı
// biçim (küçük harf, tek boşluk, 1500 rune) — anahtar modelin gördüğünü kapsar.
func resClassifyNormalize(q string) string {
	q = strings.TrimSpace(resClassifySpaceRe.ReplaceAllString(normalizeGuidedMsg(q), " "))
	if r := []rune(q); len(r) > resClassifyMaxRunes {
		q = string(r[:resClassifyMaxRunes])
	}
	return q
}

// resClassifyUser — SAF: modele giden kullanıcı bloğu.
func resClassifyUser(q, ctxService string) string {
	if ctxService != "" {
		return "Ekrandaki servis: " + ctxService + "\nSoru: " + q
	}
	return "Soru: " + q
}

// resClassifyCacheKey — SAF (CLAUDE.md "cache key hashes ALL inputs"): soru,
// ekran servisi ve sistem istemi (istem değişince eski kararlar kullanılmaz).
func resClassifyCacheKey(normQ, ctxService, system string) string {
	return "cosre-rclass:v1:" + fnvStr(normQ, ctxService, fnvStr(system))
}

// resClassifyParses — SAF: çıktı önbelleğe yazılmaya değer mi (bozuk JSON
// yazılmaz; sonraki soru modeli yeniden dener).
func resClassifyParses(raw string) bool {
	var in resClassifyJSON
	return json.Unmarshal([]byte(stripJSONFence(raw)), &in) == nil
}

// resClassifyEnv — doğrulama girdileri (test dikişi).
type resClassifyEnv struct {
	Services   []string
	CtxService string
	// PodsExist — Thanos kube_pod_info'da regex'e uyan pod var mı (son 24 sa);
	// readable=false ⇒ hiçbir cluster okunamadı. nil ⇒ Thanos yapılandırılmamış.
	PodsExist func(podRe string) (found, readable bool)
	// v0.10.2239 (Entity S4) — resolver komşuları (ad önekinden tahmin yok):
	// PodOwner pod adının sahibi servis ("" = bilinmiyor); WorkloadKnown iş
	// yükü adının depoda pod'u var mı. nil = bilinmiyor.
	PodOwner      func(pod string) string
	WorkloadKnown func(name string) bool
}

func tokenIn(toks []string, t string) bool {
	for _, x := range toks {
		if x == t {
			return true
		}
	}
	return false
}

// resolveResClassifyTarget — SAF: modelin hedefini canlı veriye karşı
// doğrular. ok=false ⇒ veto (why). Boş hedef: ekran servisi ya da hedefsiz.
func resolveResClassifyTarget(in resClassifyJSON, norm string, toks []string, env resClassifyEnv) (svc, target, kind, why string, ok bool) {
	t := strings.ToLower(strings.TrimSpace(in.Target))
	k := strings.ToLower(strings.TrimSpace(in.TargetKind))
	if t == "" {
		if env.CtxService != "" {
			return env.CtxService, env.CtxService, resKindService, "", true
		}
		return "", "", "", "", true
	}
	if len(t) > 253 {
		return "", "", "", "hedef adı çok uzun", false
	}
	liveSvc := matchLiveName(t, env.Services)
	if liveSvc == "" && strings.Contains(t, " ") {
		liveSvc = matchLiveName(strings.ReplaceAll(t, " ", "-"), env.Services)
	}
	podShape := resPodNameRe.MatchString(t) || resStatefulPodRe.MatchString(t)
	switch {
	case k == resKindPod || (podShape && liveSvc == "" && k != resKindWorkload):
		if !tokenIn(toks, t) {
			return "", "", "", "pod adı soruda geçmiyor", false
		}
		if !podShape {
			return "", "", "", "pod adı şekli değil", false
		}
		owner := ""
		if env.PodOwner != nil {
			owner = env.PodOwner(t)
		}
		if env.PodsExist != nil {
			if found, readable := env.PodsExist(regexp.QuoteMeta(t) + ".*"); readable {
				if !found {
					return "", "", "", "pod Thanos'ta yok (son 24 sa)", false
				}
				return owner, t, resKindPod, "", true
			}
		}
		if owner == "" {
			return "", "", "", "pod doğrulanamadı (Thanos yok/okunamadı, sahibi servis yok)", false
		}
		return owner, t, resKindPod, "", true
	case k == resKindWorkload:
		if !tokenIn(toks, t) {
			return "", "", "", "workload adı soruda geçmiyor", false
		}
		if liveSvc != "" {
			return liveSvc, liveSvc, resKindService, "", true
		}
		if env.WorkloadKnown != nil && env.WorkloadKnown(t) {
			return "", t, resKindWorkload, "", true
		}
		return "", "", "", "workload canlı değil", false
	}
	if liveSvc == "" {
		return "", "", "", "servis canlı katalogda yok", false
	}
	if !strings.Contains(norm, t) && !strings.Contains(norm, strings.ToLower(liveSvc)) && liveSvc != env.CtxService {
		return "", "", "", "servis soruda geçmiyor", false
	}
	return liveSvc, liveSvc, resKindService, "", true
}

// decideResourceClassify — SAF: model çıktısı → kabul edilen rota. ok=false
// ⇒ none (why panelde); çağıran mevcut sınıflandırıcıya düşer.
func decideResourceClassify(raw, question string, env resClassifyEnv) (route guidedRoute, rangeS int64, why string, ok bool) {
	var in resClassifyJSON
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &in); err != nil {
		return guidedRoute{}, 0, "bozuk JSON", false
	}
	intent := strings.ToLower(strings.TrimSpace(in.Intent))
	switch intent {
	case string(guidedResourceInvestigation), string(guidedMetricChart), string(guidedPodHealth):
	case "none", "":
		return guidedRoute{}, 0, "none", false
	default:
		return guidedRoute{}, 0, "bilinmeyen niyet " + intent, false
	}
	if math.IsNaN(in.Confidence) || in.Confidence < resClassifyMinConfidence {
		return guidedRoute{}, 0, fmt.Sprintf("düşük güven (%.2f < %.1f)", in.Confidence, resClassifyMinConfidence), false
	}
	norm := normalizeGuidedMsg(question)
	toks := guidedTokens(norm)
	svc, target, kind, why, ok := resolveResClassifyTarget(in, norm, toks, env)
	if !ok {
		return guidedRoute{}, 0, "hedef vetosu: " + why, false
	}
	fam := strings.ToLower(strings.TrimSpace(in.Family))
	if !resClassifyFamilies[fam] {
		if fam = resourceFamilyOf(toks, true); fam == "" {
			fam = resFamAll
		}
	}
	if in.RangeS > 0 && in.RangeS < 1e9 {
		rangeS = snapRangeS(int64(in.RangeS))
	}
	// R1 kuralı: grafik fiili metric_chart'ı kazandırır; fiilsiz metric_chart yok.
	if hasChartVerb(toks) {
		intent = string(guidedMetricChart)
	} else if intent == string(guidedMetricChart) {
		return guidedRoute{}, 0, "metric_chart ama grafik fiili yok", false
	}
	switch guidedIntent(intent) {
	case guidedMetricChart:
		id := pickMetricChartTemplateID(toks)
		if id == "" {
			id = resClassifyChartTemplate[fam]
		}
		if id == "" {
			return guidedRoute{}, 0, "ailenin grafik şablonu yok (" + fam + ")", false
		}
		if svc == "" {
			return guidedRoute{}, 0, "grafik için servis yok", false
		}
		return guidedRoute{Intent: guidedMetricChart, Service: svc, ChartTemplate: id, ChartGroupBy: metricChartGroupBy(toks)}, rangeS, "", true
	case guidedPodHealth:
		return guidedRoute{Intent: guidedPodHealth, Service: svc}, rangeS, "", true
	}
	if target == "" {
		return guidedRoute{}, 0, "hedef yok", false
	}
	if resourceVetoed(question, norm, toks, env.Services) {
		return guidedRoute{}, 0, "router vetosu (takım/çift/topoloji/log/deploy/trace/db şekli)", false
	}
	return guidedRoute{Intent: guidedResourceInvestigation, Service: svc, ResTarget: target, ResKind: kind, ResFamily: fam,
		ResCluster: resClusterMention(toks), ResDrillAsk: resDrillAskOf(toks, fam)}, rangeS, "", true
}

// resClassifyPodsExist — Thanos kube_pod_info varlık kontrolü (demetin
// resourceLookupPods'u: aynı seçici, aynı 60 sn önbellek, 4 sn). nil ⇒ Thanos yok.
func (s *Server) resClassifyPodsExist(ctx context.Context, to time.Time) func(string) (bool, bool) {
	if s.thanos == nil || !s.thanos.HasEnabledClusters() {
		return nil
	}
	clusters := s.thanosEnabledClusters()
	return func(podRe string) (bool, bool) {
		readable := false
		for _, l := range s.resourceLookupPods(ctx, s.thanos, clusters, podRe, to) {
			if l.Err != nil {
				continue
			}
			readable = true
			if len(l.Pods) > 0 {
				return true, true
			}
		}
		return false, readable
	}
}

// resourceClassifyFallback — niyet kademesinin İÇİNDE, genel sınıflandırıcıdan
// önce (bkz. dosya başlığı). ok=false ⇒ çağıran mevcut yolda devam eder;
// sınıflandırıcı hatası cevabı asla kesmez.
func (s *Server) resourceClassifyFallback(ctx context.Context, emit func(string, any), question, ctxService string, svcNames []string, anchorTo time.Time) (guidedRoute, int64, bool) {
	if !hasResourceNoun(guidedTokens(normalizeGuidedMsg(question))) {
		return guidedRoute{}, 0, false
	}
	q := resClassifyNormalize(question)
	system := copilot.SystemPromptResourceClassify()
	key := resClassifyCacheKey(q, ctxService, system)
	i := emitStepChipOrigin(emit, resClassifyTool, intentStepArgs(q), "intent")
	t0 := time.Now()
	var raw string
	cached := s.metricChartCacheGet(ctx, key, &raw)
	if !cached {
		// Yardımcı çağrı: çocuk exchange kimliği (geri bildirim JOIN'ini ikiye katlamaz).
		m := copilot.MetaFromContext(ctx)
		m.ExchangeID = aisurface.ChildExchangeID(m.ExchangeID, aisurface.ChatResourceClassify)
		cctx, cancel := context.WithTimeout(copilot.WithMeta(ctx, m), intentClassifyTimeout(s.copilot.ClientTimeout()))
		out, err := s.copilotExplainJSONSurface(cctx, aisurface.ChatResourceClassify, system, resClassifyUser(q, ctxService), resourceClassifySchema())
		cancel()
		if err != nil {
			emitStepEvidence(emit, i, resClassifyTool, "", err)
			return guidedRoute{}, 0, false
		}
		raw = out
		if resClassifyParses(raw) {
			s.metricChartCacheSet(ctx, key, raw, resClassifyCacheTTL)
		}
	}
	to := anchorTo
	if to.IsZero() {
		to = time.Now()
	}
	route, rangeS, why, ok := decideResourceClassify(raw, question, resClassifyEnv{Services: svcNames, CtxService: ctxService,
		PodsExist: s.resClassifyPodsExist(ctx, to.Truncate(time.Minute)),
		PodOwner:  func(pod string) string { return s.podOwnerService(ctx, pod, time.Time{}) },
		WorkloadKnown: func(name string) bool {
			return !s.workloadPodSelector(ctx, name, time.Time{}).Unknown()
		}})
	if i > 0 {
		text := intentSummary(route, rangeS, ok)
		if ok && route.ResTarget != "" {
			text += fmt.Sprintf(" · hedef: %s (%s) · aile: %s", route.ResTarget, route.ResKind, route.ResFamily)
		}
		if !ok {
			text += " · kaynak sınıflandırıcısı reddetti: " + why + " → genel sınıflandırıcı"
		}
		if cached {
			text += " · önbellek"
		}
		text += "\n" + strings.TrimSpace(raw)
		preview, trunc := clipStepPreview(text)
		emit("step-result", map[string]any{
			"i": i, "tool": resClassifyTool, "ok": true, "preview": preview, "truncated": trunc,
			"bytes": len(text), "durationMs": time.Since(t0).Milliseconds(),
		})
	}
	return route, rangeS, ok
}
