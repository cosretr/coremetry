package api

// copilot_resource_investigation.go — v0.10.1431 (CoSRE kaynak incelemesi R1).
// Operatör: "CoSRE sorulan sorulara frontier model gibi anlamlandırarak gerekli
// toolset'lerini koşsun … bir pod'un CPU/memory metriklerini grafik olarak
// getirme; gerekirse GC'sine, JVM metriklerine bakıp LLM'e yorumlatma."
//
// Boşluk: grafik FİİLİ olmayan kaynak soruları ("orders-api cpu kullanımı
// nasıl", "orders-api-6d4f8b9c7d-x2k9q neden restart ediyor", "pod'ların
// belleği limite yakın mı") metric_chart'a (1386 D2: fiil şart) giremiyor;
// pod_health'e ya da sağlık/serbest döngüye düşüp cAdvisor/kube-state verisini
// (Thanos) okumadan cevaplanıyordu.
//
// Karar (R1): router-içi guided niyet `resource_investigation` (sınıflandırıcı
// listesinde YOK — R3). Kapı SAF (routeResourceInvestigation): R1 kaynak
// sözcüğü (cpu | bellek/memory/ram/oom/working set | restart/crash/oomkilled |
// kaynak kullanımı/resource/doygunluk/limit) + hedef (pod adı | servis |
// deployment/workload adı) + grafik fiili YOK (fiil → metric_chart, 1386
// değişmedi) + takım/çift/topoloji/log/deploy/trace/db/messaging/nasıl-yapılır
// şekli YOK. Router'da routeMetricChart'tan HEMEN sonra (zayıf topoloji ve
// fan-out/periyot/çift/endpoint dallarından önce). heap/gc/jvm/thread
// sözcükleri R1'de bu niyete GİRMEZ (pod_health / metric_chart'ta kalır; R2
// taşır). Hedef çözümü demette (Thanos kube_pod_info, son 24 sa): belirsizlik →
// çip sorusu (pod önek eşleşmesi 2–8, birden çok cluster), yok → dürüst
// BULUNAMADI + denenen seçici; tahmin YOK. Demet resource_bundle.go'da.
//
// v0.10.1432 (R2) — heap / gc (tam jeton) / jvm / old gen / metaspace / leak /
// sızıntı / thread / executor / tomcat sözcükleri bu niyete taşındı (D2'nin
// ikinci eki; R1'de pod_health'teydi): tek başına → aile `jvm` (yalnız
// VictoriaMetrics JVM satırları, resource_jvm.go); cpu/bellek/restart/kaynak
// sözcüğüyle birlikte → aile `all` + ResDrillAsk "jvm" (Thanos demeti + JVM
// drill'i). "yavaş/latency" sözcüğü ResDrillAsk "slow" yazar. pod_health
// envanter sorusunu ("pod'ları nasıl") ve hedefsiz filo heap sıralamasını korur.

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// guidedResourceInvestigation — v0.10.1431: router-içi niyet.
const guidedResourceInvestigation guidedIntent = "resource_investigation"

// Hedef türleri (route.ResKind).
const (
	resKindPod      = "pod"
	resKindService  = "service"
	resKindWorkload = "workload"
)

// resDefaultRangeS — açık pencere yoksa son 1 sa (metric_chart D1 ile aynı).
const resDefaultRangeS = 3600

// Takip türleri.
const (
	fuKindResJVM   = "resource_jvm"
	fuKindResPods  = "resource_pods"
	fuKindResInfra = "resource_infra"
	fuKindResAll   = "resource_all" // v0.10.1432 — jvm ailesinden konteyner CPU/bellek/restart'a
)

// Drill istekleri (route.ResDrillAsk) — v0.10.1432.
const (
	resDrillAskJVM  = "jvm"
	resDrillAskSlow = "slow"
)

var (
	// resPodNameRe — Deployment pod adı: <ad>-<replicaset hash 8–10>-<5>.
	resPodNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?-[a-z0-9]{8,10}-[a-z0-9]{5}$`)
	// resStatefulPodRe — StatefulSet pod adı (<ad>-<sıra>); yalnız "pod" sözcüğüyle.
	resStatefulPodRe = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]-[0-9]{1,3}$`)
)

// ─── Router (SAF) ───────────────────────────────────────────────────────

func hasPodWord(toks []string) bool { return tokenHasPrefix(toks, "pod") }

func isWorkloadWord(t string) bool {
	for _, w := range []string{"deployment", "workload", "statefulset", "daemonset"} {
		if strings.HasPrefix(t, w) {
			return true
		}
	}
	return false
}

// hasResJVMWord — SAF (v0.10.1432): JVM/GC/thread/havuz sözcüğü. "gc" tam
// jeton ("gcp" tuzağı); "old gen" iki jeton; havuz/pool yalnız thread /
// executor / tomcat bağlamında (DB bağlantı havuzu db_health'te kalır).
func hasResJVMWord(toks []string) bool {
	if tokenHasPrefix(toks, "heap", "jvm", "metaspace", "leak", "sızıntı", "sizinti", "oldgen", "garbage",
		"thread", "executor", "tomcat") || anyToken(toks, "gc") {
		return true
	}
	return anyToken(toks, "old") && tokenHasPrefix(toks, "gen")
}

// resDrillAskOf — SAF (v0.10.1432): JVM drill isteği. JVM sözcüğü başka
// kaynak ailesiyle birlikte → "jvm"; yavaş/latency/gecikme → "slow".
func resDrillAskOf(toks []string, fam string) string {
	switch {
	case fam != resFamJVM && hasResJVMWord(toks):
		return resDrillAskJVM
	case tokenHasPrefix(toks, "yavaş", "yavas", "latency", "gecikm", "slow"):
		return resDrillAskSlow
	}
	return ""
}

// resourceFamilyOf — SAF: mesajın kaynak ailesi ("" = kaynak sorusu değil).
// v0.10.1432: JVM sözcüğü tek başına → jvm; R1 ailesiyle birlikte → all
// (drill isteğini resDrillAskOf yazar).
// Tek aile → o; ≥2 aile ya da genel kaynak sözcüğü (kaynak kullanımı / resource
// / doygunluk) → all. "limit" yalnız başına all; aile sözcüğüyle o aile.
// "ram" tam jeton ("rampa" tuzağı); "kaynak" nitelikle ("kaynak kullanımı",
// "kaynak durumu") ya da pod bağlamıyla — "kaynak kodu" / "veri kaynağı" değil.
// ölüyor/düşüyor/çöküyor yalnız pod bağlamında restart sayılır.
func resourceFamilyOf(toks []string, podCtx bool) string {
	cpu := tokenHasPrefix(toks, "cpu", "işlemci", "islemci", "throttl")
	// "belle": bellek / belleği (k→ğ yumuşaması).
	mem := tokenHasPrefix(toks, "belle", "memory", "hafıza", "hafiza") || anyToken(toks, "ram", "workingset") ||
		(anyToken(toks, "working") && anyToken(toks, "set"))
	rst := tokenHasPrefix(toks, "restart", "crash")
	for _, t := range toks {
		switch {
		case strings.HasPrefix(t, "oomkill"):
			mem, rst = true, true
		case strings.HasPrefix(t, "oom"):
			mem = true
		}
	}
	if anyToken(toks, "yeniden") && tokenHasPrefix(toks, "başla", "basla", "başlı", "başlat") {
		rst = true
	}
	if podCtx && tokenHasPrefix(toks, "ölüyor", "öldü", "düşüyor", "çöküyor", "çöktü") {
		rst = true
	}
	general := tokenHasPrefix(toks, "resource", "doygun", "saturation")
	if tokenHasPrefix(toks, "kaynak") && !tokenHasPrefix(toks, "kod", "code") &&
		(podCtx || tokenHasPrefix(toks, "kullan", "tüket", "tuket", "sıkın", "sikin", "yetersiz", "yetmi", "durum", "limit")) {
		general = true
	}
	n := 0
	for _, b := range []bool{cpu, mem, rst} {
		if b {
			n++
		}
	}
	if hasResJVMWord(toks) {
		if general || n > 0 {
			return resFamAll
		}
		return resFamJVM
	}
	switch {
	case general || n >= 2:
		return resFamAll
	case cpu:
		return resFamCPU
	case mem:
		return resFamMemory
	case rst:
		return resFamRestarts
	}
	if tokenHasPrefix(toks, "limit") && !anyToken(toks, "rate") && !tokenHasPrefix(toks, "ratelimit") {
		return resFamAll
	}
	return ""
}

// hasResourceSignal — SAF: hasGuidedSignal'ın ucuz ön kapısı (katalog
// okumasından önce): R1 kaynak sözcüğü.
func hasResourceSignal(toks []string) bool {
	return resourceFamilyOf(toks, hasPodWord(toks) || resPodToken(toks, nil, true) != "") != ""
}

// resNameLike — SAF: ad olabilecek jeton (ascii, ≥3, harf taşır, stopword değil).
func resNameLike(t string) bool {
	if len(t) < 3 || guidedStopwords[t] || !asciiNameToken(t) || isWorkloadWord(t) {
		return false
	}
	return strings.IndexFunc(t, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
}

// resPodToken — SAF: pod adı şekilli jeton (canlı servis adı değil).
// StatefulSet biçimi yalnız podWord iken.
func resPodToken(toks []string, services []string, podWord bool) string {
	live := map[string]bool{}
	for _, s := range services {
		live[strings.ToLower(s)] = true
	}
	for _, t := range toks {
		if live[t] {
			continue
		}
		if resPodNameRe.MatchString(t) || (podWord && resStatefulPodRe.MatchString(t)) {
			return t
		}
	}
	return ""
}

// resNeighbourName — SAF: i'nci jetonun komşusu (önce sol, sonra sağ) ad ise o.
func resNeighbourName(toks []string, i int, skip map[string]bool) string {
	for _, j := range []int{i - 1, i + 1} {
		if j >= 0 && j < len(toks) && resNameLike(toks[j]) && !skip[toks[j]] {
			return toks[j]
		}
	}
	return ""
}

// resClusterMention — SAF: "<ad> cluster'ında" / "cluster <ad>" / "<ad> kümesinde".
func resClusterMention(toks []string) string {
	skip := map[string]bool{"hangi": true, "tüm": true, "tum": true, "her": true, "bütün": true, "butun": true}
	for i, t := range toks {
		if strings.HasPrefix(t, "cluster") || strings.HasPrefix(t, "küme") || strings.HasPrefix(t, "kume") {
			if n := resNeighbourName(toks, i, skip); n != "" {
				return n
			}
		}
	}
	return ""
}

// resWorkloadMention — SAF: "deployment <ad>" / "<ad> deployment'ı".
func resWorkloadMention(toks []string, skip map[string]bool) string {
	for i, t := range toks {
		if isWorkloadWord(t) {
			if n := resNeighbourName(toks, i, skip); n != "" {
				return n
			}
		}
	}
	return ""
}

// resourceVetoed — SAF: kaynak sözcüğü taşısa da başka yolun sorusu.
// Grafik fiili → metric_chart (1386 D2); takım/çift/topoloji kendi yollarında;
// log/deploy/trace/exception/db/messaging şekli spesifik niyetinde; nasıl-
// yapılır sorusu yol tarifi; derin tarama servis sağlığında; iki+ servis adı
// kıyas/aile yolunda.
func resourceVetoed(raw, msg string, toks []string, services []string) bool {
	var nonWorkload []string
	for _, t := range toks {
		if !isWorkloadWord(t) {
			nonWorkload = append(nonWorkload, t)
		}
	}
	switch {
	case hasChartVerb(toks), hasTeamSelfSignal(toks), hasTeamWord(toks), hasTopologySignal(toks),
		hasLogSignal(toks), hasDeploySignal(nonWorkload), hasSlowTraceSignal(msg), tokenHasPrefix(toks, "trace", "span"),
		hasExceptionWord(toks), hasDBSignal(toks), hasMessagingSignal(toks), hasHowToShape(toks), hasSweepSignal(toks):
		return true
	}
	if _, _, pair := splitPairFragments(raw); pair {
		return true
	}
	return len(extractServiceEntities(msg, services)) >= 2
}

// routeResourceInvestigation — SAF: resource_investigation rotası (router'da
// routeMetricChart'tan hemen sonra). Hedef sırası: pod adı → servis (mesaj) →
// deployment/workload adı → tek servis adayı → çok aday (ask_service, çip aynı
// niyete) → ekran servisi; hiçbiri yoksa rota DÖNMEZ (filo geneli kaynak
// sorusu bugünkü yolunda — pod_health filo heap sıralaması).
func routeResourceInvestigation(raw, msg string, toks []string, svc, env string, services, envs []string, ctxService string) (guidedRoute, bool) {
	podWord := hasPodWord(toks)
	pod := resPodToken(toks, services, podWord)
	fam := resourceFamilyOf(toks, podWord || pod != "")
	if fam == "" || resourceVetoed(raw, msg, toks, services) {
		return guidedRoute{}, false
	}
	cluster := resClusterMention(toks)
	r := guidedRoute{Intent: guidedResourceInvestigation, Env: env, ResFamily: fam, ResCluster: cluster, ResDrillAsk: resDrillAskOf(toks, fam)}
	if pod != "" {
		// v0.10.2239 (S4): sahibi servis (ekranda yoksa) demette resolver'dan
		// (podOwnerService); ad önekinden tahmin yok.
		r.Service, r.ResTarget, r.ResKind = svc, pod, resKindPod
		return r, true
	}
	if svc != "" {
		r.Service, r.ResTarget, r.ResKind = svc, svc, resKindService
		return r, true
	}
	if wl := resWorkloadMention(toks, map[string]bool{cluster: cluster != ""}); wl != "" {
		r.ResTarget, r.ResKind = wl, resKindWorkload
		return r, true
	}
	switch opts := serviceCandidates(msg, services, envs, guidedServiceAskMax); {
	case len(opts) == 1:
		r.Service, r.ResTarget, r.ResKind = opts[0], opts[0], resKindService
		return r, true
	case len(opts) > 1:
		return guidedRoute{Intent: guidedAskService, AskIntent: guidedResourceInvestigation, ServiceOptions: opts, Env: env,
			ResFamily: fam, ResCluster: cluster, ResDrillAsk: r.ResDrillAsk}, true
	}
	for _, s := range services {
		if ctxService != "" && s == ctxService {
			r.Service, r.ResTarget, r.ResKind = ctxService, ctxService, resKindService
			return r, true
		}
	}
	return guidedRoute{}, false
}

// resChipText — SAF: kendi başına aynı aileyle resource_investigation'a
// yönlenen çip cümlesi (ask_service, pod/cluster netleştirme, "Diğer pod'lar").
func resChipText(target, family, cluster string) string {
	var s string
	switch family {
	case resFamCPU:
		s = target + " CPU kullanımı nasıl?"
	case resFamMemory:
		s = target + " bellek kullanımı nasıl?"
	case resFamRestarts:
		s = target + " neden restart ediyor?"
	case resFamJVM: // v0.10.1432
		s = target + " JVM heap ve GC durumu nasıl?"
	default:
		s = target + " kaynak kullanımı nasıl?"
	}
	if cluster != "" {
		s += " (" + cluster + " cluster)"
	}
	return s
}

// resAskChipText — SAF (v0.10.1432): ask_service çipi; drill isteğini taşır
// ("… JVM heap/GC dahil" → all + jvm; "… (yavaşlık var)" → slow).
func resAskChipText(route guidedRoute, svc string) string {
	s := resChipText(svc, route.ResFamily, route.ResCluster)
	switch route.ResDrillAsk {
	case resDrillAskJVM:
		s += " JVM heap/GC dahil"
	case resDrillAskSlow:
		s += " (yavaşlık var)"
	}
	return s
}

// ─── Takip çipleri / linkler (SAF) ─────────────────────────────────────

// resourceSuggestions — SAF: eski istemcinin düz çipleri. Netleştirme turunda
// adaylar (pod / cluster), aksi hâlde servis adımları.
func resourceSuggestions(route guidedRoute) []string {
	switch {
	case len(route.ResPodOptions) > 0:
		out := make([]string, 0, len(route.ResPodOptions))
		for _, p := range route.ResPodOptions {
			out = append(out, resChipText(p, route.ResFamily, route.ResCluster))
		}
		return out
	case len(route.ResClusterOptions) > 0:
		out := make([]string, 0, len(route.ResClusterOptions))
		for _, c := range route.ResClusterOptions {
			out = append(out, resChipText(route.ResTarget, route.ResFamily, c))
		}
		return out
	}
	if svc := route.Service; svc != "" {
		return []string{svc + " hata logları?", svc + " son deploy etkisi?", svc + " sağlığı nasıl?"}
	}
	return []string{"Açık problemler?"}
}

// resInfraHref — SAF: servis → Infrastructure sekmesi; servissiz → Clusters.
func resInfraHref(route guidedRoute) string {
	if svc := route.Service; svc != "" && fuServiceRe.MatchString(svc) {
		return "/service?name=" + url.QueryEscape(svc) + "&tab=infra"
	}
	return "/clusters"
}

// resourceFollowups — SAF: yapısal takip adımları (≤4): hata logları, son
// deploy etkisi, "GC/heap'e bak" (v0.10.1432: JVM metriği VAR ama drill bu
// cevapta koşmadıysa; çip AYNI niyetin jvm ailesine döner — pod kipinde pod'a),
// jvm ailesinde "CPU/bellek/restart" (aynı hedef, all ailesi), "Diğer pod'lar"
// (pod kipinde servisin tüm pod'ları, aynı aile).
// Netleştirme turunda nil; BULUNAMADI'da yalnız Infrastructure link çipi.
func resourceFollowups(route guidedRoute, asked []string) []chatFollowup {
	if len(route.ResPodOptions) > 0 || len(route.ResClusterOptions) > 0 {
		return nil
	}
	svc := strings.TrimSpace(route.Service)
	if svc != "" && !fuServiceRe.MatchString(svc) {
		svc = ""
	}
	if route.ResNotFound {
		return []chatFollowup{{Text: "Infrastructure'da aç", Label: "Infrastructure'da aç", Kind: fuKindResInfra, Href: resInfraHref(route)}}
	}
	if svc == "" {
		return nil
	}
	sc := func() *chatScope { return &chatScope{Services: []string{svc}, Env: route.Env} }
	cands := []chatFollowup{
		{Text: svc + " hata logları?", Label: "Hata logları", Kind: fuKindErrors, Scope: sc()},
		serviceStepFollowup(fuKindDeploy, svc, route.Env),
	}
	target := svc
	if route.ResKind == resKindPod && route.ResTarget != "" {
		target = route.ResTarget
	}
	if route.ResJVM && route.ResFamily != resFamJVM {
		cands = append(cands, chatFollowup{Text: resChipText(target, resFamJVM, ""), Label: "GC/heap'e bak", Kind: fuKindResJVM, Scope: sc()})
	}
	if route.ResFamily == resFamJVM {
		cands = append(cands, chatFollowup{Text: resChipText(target, resFamAll, route.ResCluster), Label: "CPU/bellek/restart", Kind: fuKindResAll, Scope: sc()})
	}
	if route.ResKind == resKindPod {
		cands = append(cands, chatFollowup{Text: resChipText(svc, route.ResFamily, ""), Label: "Diğer pod'lar", Kind: fuKindResPods, Scope: sc()})
	}
	return dedupFollowupsAsked(cands, asked, guidedFollowupMax)
}

// resourceLinks — SAF: cevap linkleri (pencere tek çıkışta uygulanır).
func resourceLinks(route guidedRoute) []guidedAnswerLink {
	var out []guidedAnswerLink
	if href := route.ResHref; href != "" && strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		label := "Metrics'te aç (Thanos)"
		if !strings.Contains(href, "src=thanos") { // v0.10.1432 — VM kaynaklı ilk grafik (jvm ailesi / Thanos yok)
			label = "Metrics'te aç (VictoriaMetrics)"
		}
		out = append(out, guidedAnswerLink{Label: label, Href: href})
	}
	if route.ResKind == resKindPod && route.ResClusterID != "" && route.ResNamespace != "" {
		q := url.Values{}
		q.Set("cluster", route.ResClusterID)
		q.Set("namespace", route.ResNamespace)
		q.Set("pod", route.ResTarget)
		if route.Service != "" {
			q.Set("service", route.Service)
		}
		out = append(out, guidedAnswerLink{Label: route.ResTarget + " · Pod", Href: "/pod?" + q.Encode()})
	}
	label := "Infrastructure'da aç"
	if route.Service != "" {
		label = route.Service + " · Infrastructure"
	}
	return append(out, guidedAnswerLink{Label: label, Href: resInfraHref(route)})
}

// ─── Metin (SAF) ────────────────────────────────────────────────────────

func resKindTR(kind string) string {
	switch kind {
	case resKindPod:
		return "pod"
	case resKindWorkload:
		return "workload pod'ları"
	}
	return "servis pod'ları"
}

func resFamilyTR(f string) string {
	switch f {
	case resFamCPU:
		return "CPU"
	case resFamMemory:
		return "bellek"
	case resFamRestarts:
		return "restart"
	case resFamJVM:
		return "JVM heap/GC/thread"
	}
	return "CPU + bellek + restart"
}

// resNotFoundTR — SAF: dürüst BULUNAMADI (denenen seçici + cluster'lar).
func resNotFoundTR(target string, d resTargetDecision, match string) string {
	s := fmt.Sprintf("BULUNAMADI: %q için Thanos'ta pod yok (son 24 sa; denenen seçici %s; denenen cluster'lar: %s).",
		target, match, strings.Join(d.Searched, ", "))
	if len(d.Failed) > 0 {
		s += " Okunamayan cluster'lar (okunamadı ≠ yok): " + strings.Join(d.Failed, ", ") + "."
	}
	return s + " Ad doğruysa pod son 24 saatte çalışmamış olabilir — Infrastructure sayfasından kontrol edin."
}

// resAskPodTR / resAskClusterTR — SAF netleştirme soruları (anlatım çağrısı yok).
func resAskPodTR(target string, d resTargetDecision) string {
	s := fmt.Sprintf("%q birden çok pod'a uyuyor (Thanos, son 24 sa): %s.", target, strings.Join(d.Options, ", "))
	if d.More {
		s += fmt.Sprintf(" (ilk %d gösterildi; daha fazlası var — adı daraltın.)", resPrefixHitsMax)
	}
	return s + " Hangisine bakayım? Alttaki çiplerden birine tıkla ya da tam pod adını yaz."
}

func resAskClusterTR(target string, d resTargetDecision) string {
	return fmt.Sprintf("%s pod'ları birden çok cluster'da var: %s. Hangi cluster'a bakayım? Alttaki çiplerden birine tıkla.",
		target, strings.Join(d.Options, ", "))
}

// ─── Demet ──────────────────────────────────────────────────────────────

// resourceDeps — demetin okuyucuları (test dikişi).
type resourceDeps struct {
	Thanos   resourceThanosReader
	Clusters []thanos.ClusterConfig
	VM       metricChartReader
	IsVM     bool
	// Runtime — v0.10.1432: operatörün JVM alarm eşikleri (GC payı); nil = varsayılan.
	Runtime *chstore.RuntimeAlertConfig
}

// guidedResourceBundle — resource_investigation rotasının kanıtı.
func (s *Server) guidedResourceBundle(ctx context.Context, emit func(string, any), route *guidedRoute, to time.Time, rangeS int64, loc *time.Location) (string, string, error) {
	var d resourceDeps
	if s.thanos != nil && s.thanos.HasEnabledClusters() {
		d.Thanos, d.Clusters = s.thanos, s.thanosEnabledClusters()
	}
	d.VM, d.IsVM = s.metricChartReaderFor()
	if d.IsVM {
		d.Runtime = s.resourceRuntimeAlerts(ctx)
	}
	s.resolveResourcePods(ctx, route, to)
	return s.resourceRun(ctx, emit, route, d, to, rangeS, loc)
}

// resolveResourcePods — v0.10.2239 (Entity S4): hedefin pod kümesi
// entity.Resolver'dan. Servis → svc → pod komşuları; iş yükü → kanonik adı
// eşit pod'lar; pod → sahibi servis (ekranda servis yoksa).
func (s *Server) resolveResourcePods(ctx context.Context, route *guidedRoute, to time.Time) {
	if route.ResTarget == "" && route.Service != "" {
		route.ResTarget, route.ResKind = route.Service, resKindService
	}
	at := to
	if time.Since(to) < 2*time.Minute {
		at = time.Time{} // "şimdi": canlı dizin
	}
	switch route.ResKind {
	case resKindPod:
		if route.Service == "" {
			route.Service = s.podOwnerService(ctx, route.ResTarget, at)
		}
	case resKindWorkload:
		route.ResPodRe = s.workloadPodSelector(ctx, route.ResTarget, at).Union
	case resKindService, "":
		route.ResPodRe = s.servicePodSelector(ctx, route.ResTarget, at).Union
	}
}

// resourcePodRegex — SAF: hedefin pod regex'i (PromQL =~ tam eşleşir). Pod
// kipi tam ad öneki; servis / iş yükü kipi resolver seçicisi ("" = bilinmiyor).
func resourcePodRegex(route *guidedRoute) string {
	if route.ResKind == resKindPod {
		return regexp.QuoteMeta(route.ResTarget) + ".*" // önek araması; koşuda tam ada daralır
	}
	return route.ResPodRe
}

// resourceRun — demetin çekirdeği. Hiçbir okuma hatası demeti düşürmez:
// dürüst metin (okunamadı ≠ yok) döner.
func (s *Server) resourceRun(ctx context.Context, emit func(string, any), route *guidedRoute, d resourceDeps, to time.Time, rangeS int64, loc *time.Location) (string, string, error) {
	if loc == nil {
		loc = time.UTC
	}
	if route.ResTarget == "" && route.Service != "" { // kapsamdan gelen servis (applyChatScopeRoute)
		route.ResTarget, route.ResKind = route.Service, resKindService
	}
	if route.ResFamily == "" {
		route.ResFamily = resFamAll
	}
	clamped := ""
	switch {
	case rangeS < resMinRangeS:
		rangeS, clamped = resMinRangeS, "pencere en az 5 dk'ya genişletildi"
	case rangeS > resMaxRangeS:
		rangeS, clamped = resMaxRangeS, "pencere en çok 24 sa'e daraltıldı"
	}
	route.ChartRangeS = rangeS
	qTo := to.Truncate(time.Minute)
	qFrom := qTo.Add(-time.Duration(rangeS) * time.Second)
	step := metricChartStepS(rangeS)
	winS := metricChartWinS(step)
	args := fmt.Sprintf(`{"target":%s,"kind":%s,"family":%s,"cluster":%s,"range_s":%d}`,
		jsonStr(route.ResTarget), jsonStr(route.ResKind), jsonStr(route.ResFamily), jsonStr(route.ResCluster), rangeS)
	n := emitGuidedStep(emit, "resource_investigation", withEnvArg(args, route.Env))
	head := fmt.Sprintf("KAYNAK İNCELEMESİ (%s · %s · %s; son %s",
		route.ResTarget, resKindTR(route.ResKind), resFamilyTR(route.ResFamily), fmtAgoTR(rangeS))
	direct := func(text, src string) (string, string, error) {
		route.DirectAnswer = text
		emitGuidedStepResult(emit, n, "resource_investigation", text, nil)
		return text, src, nil
	}
	if route.ResTarget == "" {
		return direct("Kaynak incelemesi için hedef (pod, servis ya da deployment adı) çözülemedi — adı yazar mısın?", "hedef yok")
	}
	if route.ResFamily == resFamJVM { // v0.10.1432 — yalnız VictoriaMetrics JVM satırları
		var notes []string
		if clamped != "" {
			notes = append(notes, clamped)
		}
		return s.resourceJVMOnly(ctx, emit, route, d, n, head, qFrom, qTo, step, winS, rangeS, notes, loc)
	}
	if d.Thanos == nil || len(d.Clusters) == 0 {
		text := s.resourceNoThanos(ctx, emit, route, d, qFrom, qTo, step, winS, rangeS, loc, head+"):\n")
		emitGuidedStepResult(emit, n, "resource_investigation", text, nil)
		return text, "VictoriaMetrics process metriği (Thanos yok)", nil
	}

	// Hedef çözümü: istenen cluster (varsa ve etkinse) ya da tüm etkin cluster'lar.
	var notes []string
	if clamped != "" {
		notes = append(notes, clamped)
	}
	clusters := d.Clusters
	if want := strings.TrimSpace(route.ResCluster); want != "" {
		var only []thanos.ClusterConfig
		for _, c := range d.Clusters {
			if strings.EqualFold(c.Name, want) {
				only = append(only, c)
			}
		}
		if len(only) == 0 {
			notes = append(notes, fmt.Sprintf("sorudaki cluster %q etkin Thanos cluster'ları arasında yok — tüm etkin cluster'lar denendi", want))
		} else {
			clusters = only
		}
	}
	podRe := resourcePodRegex(route)
	if podRe == "" { // boş seçici cluster geneli olurdu
		route.ResNotFound = true
		return direct(podsUnknownTR(route.ResTarget)+".", "entity (pod bilinmiyor)")
	}
	looks := s.resourceLookupPods(ctx, d.Thanos, clusters, podRe, qTo)
	dec := decideResourceTarget(route.ResKind, route.ResTarget, looks)
	match := resourcePodLookupMatch(podRe, "")
	switch dec.Kind {
	case resDecideUnreadable:
		var errs []string
		for _, l := range looks {
			if l.Err != nil {
				errs = append(errs, l.Cluster+": "+l.Err.Error())
			}
		}
		return direct("Thanos pod etiketi okunamadı (okunamadı ≠ yok; seçici "+match+"): "+strings.Join(errs, "; ")+".", "Thanos (okunamadı)")
	case resDecideNotFound:
		route.ResNotFound = true
		return direct(resNotFoundTR(route.ResTarget, dec, match), "Thanos (pod bulunamadı)")
	case resDecideAskPod:
		route.ResPodOptions = dec.Options
		return direct(resAskPodTR(route.ResTarget, dec), "Thanos (pod adayları)")
	case resDecideAskCluster:
		route.ResClusterOptions = dec.Options
		return direct(resAskClusterTR(route.ResTarget, dec), "Thanos (cluster adayları)")
	}
	var c thanos.ClusterConfig
	for _, cc := range clusters {
		if cc.Name == dec.Cluster {
			c = cc
		}
	}
	route.ResCluster, route.ResClusterID = c.Name, c.EffectiveID()
	if route.ResKind == resKindPod {
		route.ResTarget = dec.Pod
		podRe = regexp.QuoteMeta(dec.Pod)
	}
	if len(dec.Failed) > 0 {
		notes = append(notes, "okunamayan cluster'lar (okunamadı ≠ yok): "+strings.Join(dec.Failed, ", "))
	}

	qs := resourceQueries(route.ResFamily, podRe, c.NamespaceFilter, winS)
	res := s.resourceRunQueries(ctx, d.Thanos, c, qs, podRe, qFrom, qTo, step)
	results := map[string][]resSeries{}
	failed := 0
	for i, q := range qs {
		r := res[i]
		if r.Err != nil {
			failed++
			notes = append(notes, fmt.Sprintf("OKUNAMADI (okunamadı ≠ yok): %s — %v", q.ID, r.Err))
			continue
		}
		results[q.ID] = r.Series
		if r.Truncated {
			notes = append(notes, fmt.Sprintf("%s: seri tavanı (≤%d) uygulandı", q.ID, resMaxSeries))
		}
		if q.ID == resQLimits && len(r.Series) == 0 && route.ResFamily != resFamRestarts {
			notes = append(notes, "kube_pod_container_resource_limits seri döndürmedi — limit tanımsız, limit yüzdesi YOK")
		}
	}

	// Grafikler: range sorgusu başına bir metric_chart adımı + veri bloğu.
	for i, q := range qs {
		r := res[i]
		if !q.Range || r.Err != nil || len(r.Series) == 0 {
			continue
		}
		series, trunc := resChartSeries(r.Series, q.Unit)
		if len(series) == 0 {
			continue
		}
		href := mcptools.PromQLEditorHref(q.Expr, "thanos", c.Name, qFrom.UnixMilli(), qTo.UnixMilli())
		if route.ResHref == "" {
			route.ResHref = href
		}
		cargs := fmt.Sprintf(`{"source":"thanos","cluster":%s,"query":%s,"range_s":%d}`, jsonStr(c.Name), jsonStr(q.ID), rangeS)
		cn := emitGuidedStep(emit, "metric_chart", cargs)
		title := route.ResTarget + " · " + q.Title + " · Thanos " + c.Name
		ns := len(series)
		ev := mcptools.ChatEvidence{Tool: "metric_chart", Source: "Thanos · " + c.Name, Query: q.Expr, QueryKind: "promql",
			Window: mcptools.EvidenceWindow{FromMs: qFrom.UnixMilli(), ToMs: qTo.UnixMilli()}, Cluster: c.Name, Series: &ns,
			DurationMs: r.DurationMs, Href: href}
		chart := &mcptools.EvChartPayload{Title: title, DeepLink: href,
			Window: mcptools.EvBlockWindow{FromNs: qFrom.UnixNano(), ToNs: qTo.UnixNano()}, Series: series, Truncated: trunc || r.Truncated}
		emitGuidedEvidenceResult(emit, cn, "metric_chart", fmt.Sprintf("%s: %d seri.\nPromQL: %s\n", title, ns, q.Expr), ev, chart)
	}

	stats := resBuildStats(results, step)
	if route.ResKind == resKindPod {
		for _, p := range stats {
			if p.Pod == route.ResTarget && p.Namespace != "" {
				route.ResNamespace = p.Namespace
			}
		}
	}
	// v0.10.1432 — R2 drill: JVM metriği VAR ve (bellek ≥%80 | restart | OOMKilled |
	// yavaşlık/JVM sorusu) → JVM satırları aynı cevapta; aksi hâlde çip.
	stats, jvmLines, jvmSrc := s.resourceJVMStage(ctx, emit, route, d, stats, qFrom, qTo, step, winS, rangeS, false)

	var b strings.Builder
	fmt.Fprintf(&b, "%s; kaynak Thanos · %s — cAdvisor + kube-state-metrics; adım %ds, rate penceresi %ds):\n", head, c.Name, step, winS)
	fmt.Fprintf(&b, "Pod seçici: pod=~%q%s.\n", podRe, thanos.NamespaceMatcher(c.NamespaceFilter))
	for _, nt := range notes {
		b.WriteString("Not: " + nt + ".\n")
	}
	if failed == len(qs) {
		b.WriteString("Thanos sorgularının HİÇBİRİ okunamadı (okunamadı ≠ yok) — sayı YOK, eşik değerlendirilemedi.\n")
	} else {
		b.WriteString(renderResourceSummaryTR(stats, loc))
		if route.ResHref != "" {
			b.WriteString("Grafikler cevabın altında kanıt çiplerinde çizildi (sunucu verisi; grafik bloğu YAZMA).\n")
		}
	}
	for _, l := range jvmLines {
		b.WriteString(l + "\n")
	}
	text := b.String()
	emitGuidedStepResult(emit, n, "resource_investigation", text, nil)
	return text, fmt.Sprintf("Thanos · %s (cAdvisor + kube-state-metrics)%s · resource_investigation/%s", c.Name, jvmSrc, route.ResFamily), nil
}

// resourceNoThanos — Thanos yok: pod konteyner CPU/bellek ve restart
// OKUNAMAZ (cAdvisor / kube-state yalnız Thanos'ta, D5). VictoriaMetrics
// varsa ve servis biliniyorsa 1386'nın process şablonları (cpu / memory)
// çizilir + dürüst not; yoksa dürüst "veri yok".
func (s *Server) resourceNoThanos(ctx context.Context, emit func(string, any), route *guidedRoute, d resourceDeps, from, to time.Time, step, winS int, rangeS int64, loc *time.Location, head string) string {
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("Thanos (cAdvisor + kube-state-metrics) yapılandırılmamış — pod KONTEYNER CPU/bellek, limit yüzdesi ve restart sayısı OKUNAMADI (yok demek değil).\n")
	var tmpls []string
	switch route.ResFamily {
	case resFamCPU:
		tmpls = []string{mcCPU}
	case resFamMemory:
		tmpls = []string{mcMemory}
	case resFamAll:
		tmpls = []string{mcCPU, mcMemory}
	}
	svc := route.Service
	switch {
	case len(tmpls) == 0:
	case svc == "":
		b.WriteString("Hedefin servisi çözülemedi — uygulamanın process metriği de çizilemedi.\n")
	case !d.IsVM || d.VM == nil:
		b.WriteString("VictoriaMetrics de yapılandırılmamış — uygulamanın process metriği de okunamadı; kaynak verisi YOK.\n")
	default:
		b.WriteString("Yerine uygulamanın KENDİ process metriği (VictoriaMetrics, metric_chart cpu/bellek şablonu, pod kırılımlı) çizildi — konteyner limitine göre yüzde YOK.\n")
		for _, id := range tmpls {
			r := guidedRoute{Intent: guidedMetricChart, Service: svc, Env: route.Env, ChartTemplate: id}
			t, _, _ := s.metricChartRun(ctx, emit, &r, d.VM, true, from, to, rangeS, loc)
			b.WriteString(t)
			if route.ResHref == "" {
				route.ResHref = r.ChartHref
			}
		}
	}
	if route.ResFamily == resFamRestarts || route.ResFamily == resFamAll {
		b.WriteString("Restart sayısı/sebebi kube-state-metrics ister (Thanos) — okunamadı, \"restart yok\" DENEMEZ.\n")
	}
	// v0.10.1432 — Thanos yokken drill yalnız soru tetikleriyle (yavaşlık/JVM);
	// bellek yüzdesi / restart okunamadı.
	stats, jvmLines, _ := s.resourceJVMStage(ctx, emit, route, d, nil, from, to, step, winS, rangeS, false)
	if len(stats) > 0 {
		b.WriteString(renderResourceSummaryTR(stats, loc))
	} else {
		b.WriteString("EŞİK AŞIMI: değerlendirilemedi (konteyner limiti okunamadı).\n")
	}
	for _, l := range jvmLines {
		b.WriteString(l + "\n")
	}
	return b.String()
}
