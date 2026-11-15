package api

// copilot_service_topology.go — v0.10.1388 (CoSRE topoloji). Operatör: "Bu
// arada topology soranlar da oluyor. Servis topolojisini CoSRE'den yönlendir
// ya da örnek bir trace'in topolojisini çiz o servise ait." + "Operasyon
// ismine göre de trace bulup topoloji çizebilirsin."
//
// İki router-içi guided niyet (sınıflandırıcı listesinde yok), ikisi de LLM'SİZ
// (anlatım çağrısı yok, ai_calls satırı yok; noteRoute yine etiketler):
//
//   - service_topology — "payment-service topolojisi", "kimler çağırıyor",
//     "what depends on X", "X'in upstream/downstream'i": topology_edges_5m
//     (ReadServiceTopologyAggForFocus, 1 hop varsayılan, "2 adım" → 2) →
//     gizli desen süzgeci → buildServiceGraphWith (Service Map ile AYNI kurucu)
//     → v0.10.1392: Topology sekmesinin görünümü (topoChatNeighbourhood —
//     yönlü sütunlar, yalnız akış kenarları, sütun başına 12 / "+N more";
//     copilot_topology_focus.go) → deterministik metin (ÇAĞIRANLAR /
//     BAĞIMLILIKLAR, sayılar çizilen kümeden) + `topology` bloğu (FE
//     ChatTopologyCard, sekmenin TopologyFlowGraph'ı, kompakt).
//   - trace_topology — "örnek trace topolojisi", "<OP> operasyonunun
//     topolojisi", "POST /api/payments topolojisi", "<traceId> topolojisi":
//     temsilci trace (en yavaş 10'un hatasızları içinde span sayısı medyanı;
//     "hatalı" → en yavaş hatalı; açık id kazanır) → resolveTraceSpans
//     (Tempo-önce) → span ağacından servis grafı (traceTopologyEdges) → aynı
//     blok + trace_list bloğu. Operasyon adı katalogla çözülür (opTokens /
//     CatalogCandidates / matchOperations / matchEndpoint — ön-getirmenin
//     eşleyicileri); Oracle-only operasyonda span yoksa Oracle satırlarının
//     trace id'leri (Tempo-önce) denenir.
//
// Operasyon ön-getirme kademesi (copilot_chat.go, guided'dan ÖNCE) topoloji
// sözcüğü taşıyan mesajı SAHİPLENMEZ (topologyVetoesOpPrefetch) — kademe sırası
// değişmedi (TestChatTierOrderIsPinned).
//
// Salt-okur: yalnız MV + trace okuması; yazma, denetim, model çağrısı yok.
// Önbellek anahtarları TÜM girdileri hash'ler (FNV, v0.5.187).

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// v0.10.1388 — iki router-içi niyet.
const (
	guidedServiceTopology guidedIntent = "service_topology"
	guidedTraceTopology   guidedIntent = "trace_topology"
)

// v0.10.1388 — bütçeler (onaylı varsayılanlar: 1 hop, 15 düğüm / 30 kenar,
// ⚠ ≥%1, kırmızı ≥%5, kenar gecikmesi p99).
// v0.10.1392 — 15/30 artık YALNIZ trace topolojisinde; servis komşuluğu
// Topology sekmesinin sütun tavanını kullanır (topoChatMaxPerCol).
// topoChatReadLimit 2000 → 20000: sekmenin okuma tavanıyla aynı (servicegraph.go)
// — hub serviste sohbet ile sekme farklı kenar kümesi görmesin.
const (
	topoChatDefaultRangeS = 3600
	topoChatMaxNodes      = 15
	topoChatMaxEdges      = 30
	topoChatReadLimit     = 20000
	topoChatTimeout       = 10 * time.Second
	topoChatCacheTTL      = 60 * time.Second
	topoTraceCacheTTL     = 5 * time.Minute
	topoTraceSampleN      = 10
	topoWarnPct           = 1.0
	topoErrPct            = 5.0
	topoNeighbourChips    = 2
	topoOracleLookback    = 24 * time.Hour
	topoOracleTraceIDs    = 10
	topoOracleTempoTries  = 3
	topoOpChoiceMax       = 5
)

// ─── Router (SAF) ───────────────────────────────────────────────────────

// topoStrongStems — açık topoloji sözcükleri: metrik grafiği ve çift
// ("A'dan B'ye") şekillerinden ÖNCE kazanır. "bağımlılı" bağımlılık /
// bağımlılığı / bağımlılıkları'nı birlikte yakalar (ğ/k yumuşaması).
var topoStrongStems = []string{"topolo", "topology", "upstream", "downstream", "bağımlılı", "bagimlili", "dependenc"}

// topoWeakStems — komşuluk fiilleri: metrik grafiğinden SONRA denenir ve
// çift şekli ("A'dan B'ye …") varsa kendi yolunda kalır.
var topoWeakStems = []string{"komşu", "komsu", "bağımlı", "bagimli", "çağıran", "cagiran", "çağırıyor", "cagiriyor",
	"çağırdı", "cagirdi", "çağrılan", "cagrilan", "caller", "callee", "depends"}

// hasStrongTopologySignal — SAF: topolo*/topology/upstream/downstream/
// bağımlılık/dependency ya da "servis haritası" / "service map".
func hasStrongTopologySignal(toks []string) bool {
	if tokenHasPrefix(toks, topoStrongStems...) {
		return true
	}
	for i := 0; i+1 < len(toks); i++ {
		if (toks[i] == "servis" || toks[i] == "service") && (strings.HasPrefix(toks[i+1], "harita") || strings.HasPrefix(toks[i+1], "map")) {
			return true
		}
	}
	return false
}

// hasTopologySignal — SAF: güçlü ya da zayıf topoloji sinyali ("kime bağlı"
// dahil; "neye bağlı" nedensellik sorusu olabildiği için DEĞİL).
func hasTopologySignal(toks []string) bool {
	if hasStrongTopologySignal(toks) || tokenHasPrefix(toks, topoWeakStems...) {
		return true
	}
	return anyToken(toks, "kime", "kimlere") && tokenHasPrefix(toks, "bağlı", "bagli")
}

// topologyVetoesOpPrefetch — SAF: operasyon ön-getirme kademesi bu mesajı
// sahiplenmesin mi (topoloji sözcüğü → guided trace_topology çözer).
func topologyVetoesOpPrefetch(raw string) bool {
	return hasTopologySignal(guidedTokens(normalizeGuidedMsg(raw)))
}

// topoHopsFrom — SAF: "2 adım" / "iki hop" / "3 seviye" / "genişlet" → 2
// (sohbet tavanı 2); "1 adım" / "tek hop" → 1; yoksa 1 (onaylı varsayılan).
func topoHopsFrom(toks []string) int {
	for i, t := range toks {
		if i+1 >= len(toks) || !tokenHasPrefix(toks[i+1:i+2], "adım", "adim", "hop", "seviye", "katman", "level", "derece") {
			continue
		}
		switch t {
		case "1", "tek", "one":
			return 1
		case "2", "3", "iki", "üç", "uc", "two", "three":
			return 2
		}
	}
	for _, t := range toks {
		if t == "2-hop" || t == "2hop" || t == "two-hop" {
			return 2
		}
	}
	if tokenHasPrefix(toks, "genişlet", "genislet", "expand") {
		return 2
	}
	return 1
}

// topoRouteHops — rotanın hop'u (1|2; sıfır değer = 1).
func topoRouteHops(r guidedRoute) int {
	if r.TopoHops >= 2 {
		return 2
	}
	return 1
}

// topoOpStop — operasyon adı sanılmaması gereken komşu sözcükler.
var topoOpStop = map[string]bool{"hangi": true, "örnek": true, "ornek": true, "sample": true, "which": true, "this": true,
	"that": true, "every": true, "bütün": true, "butun": true, "şunun": true, "bunun": true, "trace": true}

// topoOpCleanWord — SAF: tırnak / markdown / noktalama ve Türkçe iyelik eki
// ("PAYMENT_CREATE'in") atılmış sözcük.
func topoOpCleanWord(w string) string {
	w = strings.Trim(w, "\"`“”‘’*()[]<>,.;:!?")
	if j := strings.IndexAny(w, "'’"); j > 0 {
		w = w[:j]
	}
	return strings.Trim(w, "'")
}

// topologyOpMention — SAF: topoloji sorusundaki operasyon anması. Sıra:
// endpoint ("POST /api/payments", URL yolu) → "operasyon/operation"
// sözcüğünün komşusu ("charge operasyonunun", "operation PAYMENT_CREATE") →
// ACM kod biçimi (PAYMENT_CREATE). Servis / ortam adı, kısa (<4) ve durak
// sözcükleri operasyon sayılmaz. "" = anma yok.
func topologyOpMention(raw string, services, envs []string) string {
	if method, path, ok := endpointMention(raw); ok {
		return strings.TrimSpace(method + " " + path)
	}
	isName := func(w string) bool {
		for _, n := range services {
			if strings.EqualFold(n, w) {
				return true
			}
		}
		for _, n := range envs {
			if strings.EqualFold(n, w) {
				return true
			}
		}
		return false
	}
	usable := func(w string) bool {
		lw := normalizeGuidedMsg(w)
		return len([]rune(w)) >= opTokenMin && !isName(w) && !topoOpStop[lw] &&
			!hasTopologySignal([]string{lw}) && !strings.HasPrefix(lw, "operasyon") && !strings.HasPrefix(lw, "operation")
	}
	words := strings.Fields(stripMarkdownWrapText(raw))
	for i, w := range words {
		lw := normalizeGuidedMsg(topoOpCleanWord(w))
		if !strings.HasPrefix(lw, "operasyon") && !strings.HasPrefix(lw, "operation") {
			continue
		}
		if i > 0 {
			if c := topoOpCleanWord(words[i-1]); usable(c) {
				return c
			}
		}
		if i+1 < len(words) {
			if c := topoOpCleanWord(words[i+1]); usable(c) && (opCodeShapeRe.MatchString(c) || identifierLike(c)) {
				return c
			}
		}
	}
	for _, w := range words {
		if c := topoOpCleanWord(w); opCodeShapeRe.MatchString(c) && usable(c) {
			return c
		}
	}
	return ""
}

// topologyYieldsToRootCause — SAF (v0.10.1479): "X neden yavaşladı, hangi
// bağımlılık" nedensellik sorusu; bağımlılık sözcüğü şüpheliyi adlandırır,
// harita istemez → root_cause. Kapı dar: neden/sebep + bozulma (yavaş/gecikme/
// hata) birlikte ve açık harita sözcüğü (topoloji/servis haritası/map) YOK.
// "X bağımlılıkları neler" / "X topolojisi" topolojide kalır.
func topologyYieldsToRootCause(toks []string) bool {
	if !hasWhySignal(toks) {
		return false
	}
	if !tokenHasPrefix(toks, "yavaş", "yavas", "slow", "gecik", "latenc") && !hasErrorSignal(toks) {
		return false
	}
	return !tokenHasPrefix(toks, "topolo", "topology", "harita", "map")
}

// routeTopologyAsk — SAF: topoloji rotası. Router'ın servis/ortam
// çıkarımından hemen sonra; güçlü sözcükte metrik grafiğinden ÖNCE, zayıf
// sözcükte SONRA çağrılır (copilot_guided.go). Fan-out/periyot/çift/endpoint
// dallarından önce: "kimler çağırıyor" çift/aday turuna, "POST /api/payments
// topolojisi" endpoint aday turuna savrulmasın. Takım-iyelik kendi yolunda;
// zayıf sözcükte çift şekli ("A'dan B'ye çağrılan …") çift dalına kalır.
//
// Trace kipi: operasyon anması (servis isteğe bağlı) ya da trace/örnek/sample
// kökü. Servis çözümü: mesaj → tek aday → çok aday (ask_service, seçenekli) →
// ekran servisi → ask_service.
func routeTopologyAsk(raw, msg string, toks []string, svc, env string, services, envs []string, ctxService string) (guidedRoute, bool) {
	if !hasTopologySignal(toks) || hasTeamSelfSignal(toks) || topologyYieldsToRootCause(toks) {
		return guidedRoute{}, false
	}
	if !hasStrongTopologySignal(toks) {
		if _, _, pair := splitPairFragments(raw); pair {
			return guidedRoute{}, false
		}
	}
	r := guidedRoute{Intent: guidedServiceTopology, Service: svc, Env: env, TopoHops: topoHopsFrom(toks), TopoErrorsOnly: hasErrorSignal(toks)}
	if op := topologyOpMention(raw, services, envs); op != "" {
		r.Intent, r.TopoOperation = guidedTraceTopology, op
		return r, true
	}
	if tokenHasPrefix(toks, "trace", "örnek", "ornek", "sample") {
		r.Intent = guidedTraceTopology
	}
	if svc != "" {
		return r, true
	}
	ask := guidedRoute{Intent: guidedAskService, AskIntent: r.Intent, Env: env, TopoHops: r.TopoHops, TopoErrorsOnly: r.TopoErrorsOnly}
	switch opts := serviceCandidates(msg, services, envs, guidedServiceAskMax); {
	case len(opts) == 1:
		r.Service = opts[0]
		return r, true
	case len(opts) > 1:
		ask.ServiceOptions = opts
		return ask, true
	}
	for _, s := range services {
		if ctxService != "" && s == ctxService {
			r.Service = ctxService
			return r, true
		}
	}
	return ask, true
}

// topologyInheritTrace — SAF: "bu trace'in topolojisi" ekrandaki trace'e
// oturur (trace_by_id'ye çevrilmez). ok=false → rota topoloji değil.
func topologyInheritTrace(r guidedRoute, ctxTrace string) (guidedRoute, bool) {
	if r.Intent != guidedTraceTopology && !(r.Intent == guidedAskService && r.AskIntent == guidedTraceTopology) {
		return r, false
	}
	return guidedRoute{Intent: guidedTraceTopology, Service: r.Service, Env: r.Env, TopoErrorsOnly: r.TopoErrorsOnly, TopoTraceID: ctxTrace}, true
}

// topologyAskChip — SAF: ask_service çipi (kendi başına aynı niyete yönlenir).
func topologyAskChip(route guidedRoute, svc string) string {
	if route.AskIntent == guidedTraceTopology {
		if route.TopoErrorsOnly {
			return svc + " hatalı örnek trace topolojisi?"
		}
		return svc + " örnek trace topolojisi?"
	}
	switch {
	case route.TopoErrorsOnly:
		return svc + " hatalı bağımlılıkları?"
	case topoRouteHops(route) == 2:
		return svc + " topolojisini 2 adım göster"
	}
	return svc + " topolojisi?"
}

// ─── Takip çipleri / linkler (SAF) ─────────────────────────────────────

// Takip türleri — v0.10.1388.
const (
	fuKindTopoHops      = "topo_hops"
	fuKindTopoTrace     = "topo_trace"
	fuKindTopoErrors    = "topo_errors"
	fuKindTopoService   = "topo_service"
	fuKindTopoNeighbour = "topo_neighbour"
	// fuKindOpTopology — operasyon ön-getirmesi cevabının "<op> örnek trace
	// topolojisi" çipi (chat_followup_chips.go renderFollowup).
	fuKindOpTopology = "op_topology"
)

// topologyFollowups — SAF: service_topology → hop genişlet/daralt, örnek trace
// topolojisi, hatalı bağımlılıklar (eonly ya da hatalı kenar yoksa YOK), ⚠
// komşuların (≤2) sağlığı; trace_topology → servis topolojisi, bu trace'i
// açıkla. Tavan guidedFollowupMax.
func topologyFollowups(route guidedRoute, asked []string) []chatFollowup {
	svc := strings.TrimSpace(route.Service)
	if svc != "" && !fuServiceRe.MatchString(svc) {
		svc = ""
	}
	scope := func(s string) *chatScope { return &chatScope{Services: []string{s}, Env: route.Env} }
	var cands []chatFollowup
	if route.Intent == guidedTraceTopology {
		if svc != "" {
			cands = append(cands, chatFollowup{Text: svc + " topolojisi?", Label: "Servis topolojisi", Kind: fuKindTopoService, Scope: scope(svc)})
		}
		if id := strings.ToLower(route.TopoTraceOut); fuTraceRe.MatchString(id) {
			cands = append(cands, chatFollowup{Text: id + " açıkla", Label: "Bu trace'i açıkla", Kind: fuKindTrace, Scope: &chatScope{Trace: id, Env: route.Env}})
		}
		if svc != "" && !route.TopoErrorsOnly {
			cands = append(cands, chatFollowup{Text: svc + " hatalı örnek trace topolojisi?", Label: "Hatalı örnek trace", Kind: fuKindTopoErrors, Scope: scope(svc)})
		}
		return dedupFollowupsAsked(cands, asked, guidedFollowupMax)
	}
	if svc == "" {
		return nil
	}
	if topoRouteHops(route) == 1 {
		cands = append(cands, chatFollowup{Text: svc + " topolojisini 2 adım göster", Label: "2 adım genişlet", Kind: fuKindTopoHops, Scope: scope(svc)})
	} else {
		cands = append(cands, chatFollowup{Text: svc + " topolojisini 1 adım göster", Label: "1 adıma daralt", Kind: fuKindTopoHops, Scope: scope(svc)})
	}
	cands = append(cands, chatFollowup{Text: svc + " örnek trace topolojisi?", Label: "Örnek trace topolojisi", Kind: fuKindTopoTrace, Scope: scope(svc)})
	if !route.TopoErrorsOnly && route.TopoHasErrEdge {
		cands = append(cands, chatFollowup{Text: svc + " hatalı bağımlılıkları?", Label: "Hatalı bağımlılıklar", Kind: fuKindTopoErrors, Scope: scope(svc)})
	}
	for _, n := range route.TopoNeighbours {
		if fuServiceRe.MatchString(n) && n != svc {
			cands = append(cands, chatFollowup{Text: n + " sağlığı nasıl?", Label: n + " sağlığı", Kind: fuKindTopoNeighbour, Scope: scope(n)})
		}
	}
	return dedupFollowupsAsked(cands, asked, guidedFollowupMax)
}

// topologySuggestions — SAF: eski istemcinin düz çipleri.
func topologySuggestions(route guidedRoute) []string {
	svc := route.Service
	if svc == "" {
		return []string{"Açık problemler?", "Takımımın servisleri nasıl?"}
	}
	if route.Intent == guidedTraceTopology {
		return []string{svc + " topolojisi?", svc + " en yavaş trace'ler?", svc + " sağlığı nasıl?"}
	}
	return []string{svc + " örnek trace topolojisi?", svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?"}
}

// topologyTabHref — SAF: servis Topology sekmesi (ServiceSignalTabs: hops
// varsayılanı 2 URL'e yazılmaz, eonly=1).
func topologyTabHref(svc string, hops int, eonly bool) string {
	h := "/service?name=" + url.QueryEscape(svc) + "&tab=topology"
	if hops != 2 {
		h += fmt.Sprintf("&hops=%d", hops)
	}
	if eonly {
		h += "&eonly=1"
	}
	return h
}

// serviceMapFocusHref — SAF: /service-map?focus= (ServiceMap.tsx aynı hops/eonly kodekleri).
func serviceMapFocusHref(svc string, hops int, eonly bool) string {
	h := "/service-map?focus=" + url.QueryEscape(svc)
	if hops != 2 {
		h += fmt.Sprintf("&hops=%d", hops)
	}
	if eonly {
		h += "&eonly=1"
	}
	return h
}

// topologyLinks — SAF: cevap linkleri. Önce Topology sekmesi, sonra Service
// Map, sonra Overview; trace kipinde trace sayfası önce. Otomatik açılış YOK.
// Pencere guidedAnswerLinks'in tek çıkışında (applyAll) yazılır.
func topologyLinks(route guidedRoute) []guidedAnswerLink {
	svc := strings.TrimSpace(route.Service)
	hops, eonly := topoRouteHops(route), route.TopoErrorsOnly && route.Intent == guidedServiceTopology
	var out []guidedAnswerLink
	if id := strings.ToLower(route.TopoTraceOut); route.Intent == guidedTraceTopology && fuTraceRe.MatchString(id) {
		out = append(out, guidedAnswerLink{Label: "Trace " + id[:12], Href: "/trace?id=" + id})
	}
	if svc == "" {
		return append(out, guidedAnswerLink{Label: "Service Map", Href: "/service-map"})
	}
	return append(out,
		guidedAnswerLink{Label: svc + " · Topology", Href: topologyTabHref(svc, hops, eonly)},
		guidedAnswerLink{Label: "Service Map", Href: serviceMapFocusHref(svc, hops, eonly)},
		guidedAnswerLink{Label: svc + " · Overview", Href: "/service?name=" + url.QueryEscape(svc)},
	)
}

// ─── Bütçe + metin (SAF) ───────────────────────────────────────────────

// topoTruncation — bütçenin dışarıda bıraktıkları (0 = kesilmedi).
// v0.10.1392 — servis komşuluğunda Callers/Callees: sütun tavanının (Topology
// sekmesi MAX_PER_COL) gizlediği çağıran / bağımlılık sayısı.
type topoTruncation struct {
	Nodes   int `json:"nodes"`
	Edges   int `json:"edges"`
	Callers int `json:"callers,omitempty"`
	Callees int `json:"callees,omitempty"`
}

// topoChatBudget — SAF: sohbet bütçesi. Odak düğümü kalır; önce odağa değen
// (doğrudan) kenarlar çağrı sayısına göre, sonra 2. adım kenarları; bir kenar
// ancak iki ucu düğüm bütçesine sığarsa girer. eonly: hatasız kenarlar düşer
// (bütçe kesimi sayılmaz). Sıralama tam eşitlik kırıcılı (önbellek kararlılığı).
func topoChatBudget(g ServiceGraphResponse, focus string, eonly bool, maxNodes, maxEdges int) (ServiceGraphResponse, topoTruncation) {
	edges := make([]GraphEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if eonly && e.Errors == 0 {
			continue
		}
		edges = append(edges, e)
	}
	direct := func(e GraphEdge) bool { return e.Source == focus || e.Target == focus }
	sort.SliceStable(edges, func(i, j int) bool {
		if di, dj := direct(edges[i]), direct(edges[j]); di != dj {
			return di
		}
		if edges[i].Calls != edges[j].Calls {
			return edges[i].Calls > edges[j].Calls
		}
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Target < edges[j].Target
	})
	all := map[string]bool{}
	for _, e := range edges {
		all[e.Source], all[e.Target] = true, true
	}
	keep := map[string]bool{}
	if focus != "" && len(edges) > 0 {
		keep[focus] = true
	}
	kept := make([]GraphEdge, 0, len(edges))
	for _, e := range edges {
		if len(kept) >= maxEdges {
			break
		}
		add := 0
		if !keep[e.Source] {
			add++
		}
		if !keep[e.Target] && e.Target != e.Source {
			add++
		}
		if len(keep)+add > maxNodes {
			continue
		}
		keep[e.Source], keep[e.Target] = true, true
		kept = append(kept, e)
	}
	nodes := make([]GraphNode, 0, len(keep))
	for _, n := range g.Nodes {
		if keep[n.ID] {
			nodes = append(nodes, n)
		}
	}
	out := g
	out.Nodes, out.Edges = nodes, kept
	out.TotalNodes, out.ShownNodes = len(all), len(nodes)
	tr := topoTruncation{Nodes: len(all) - len(keep), Edges: len(edges) - len(kept)}
	if tr.Nodes < 0 {
		tr.Nodes = 0
	}
	return out, tr
}

// topoKindTR — SAF: düğüm türünün kısa Türkçe etiketi ("" = servis).
func topoKindTR(kind string) string {
	switch kind {
	case "database":
		return " (veritabanı)"
	case "queue":
		return " (kuyruk)"
	case "external":
		return " (dış)"
	}
	return ""
}

// topoRateMark — SAF: hata oranı ≥%1 ⚠.
func topoRateMark(pct float64) string {
	if pct >= topoWarnPct {
		return " ⚠"
	}
	return ""
}

// topoEdgeLineTR — SAF: "- name → 1.200 çağrı · hata %0.40 · p99 320 ms (http) via host".
func topoEdgeLineTR(name string, e GraphEdge) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s → %s çağrı · hata %%%.2f%s · p99 %s", name, fmtCountTR(e.Calls), e.ErrorRate, topoRateMark(e.ErrorRate), tracechain.FmtMs(e.P99Ms))
	if e.Protocol != "" {
		fmt.Fprintf(&b, " (%s)", e.Protocol)
	}
	// v0.10.1392 — hedefin KENDİ adresi (k8s DNS adı) "via" diye basılmaz;
	// yalnız bilgi taşıyan host (GSLB/route) kalır (topoViaInformative).
	if via := topoInformativeVia(e); len(via) > 0 {
		b.WriteString(" via " + strings.Join(via, ", "))
	}
	return b.String()
}

// topoEmptyReasonsTR — SAF: kenar yokken dürüst olası nedenler
// (mcptools topologyEmptyReasons'ın Türkçe ikizi).
func topoEmptyReasonsTR(focus string) []string {
	return []string{
		fmt.Sprintf("%q servis adının birebir yazımı değil olabilir (topoloji service.name'e harfiyen bağlı)", focus),
		"servisin bu pencerede enstrümante komşusu yok: giriş servisinin çağıranı olmaz, span üretmeyen bağımlılık kenar üretmez",
		"pencere çok dar ya da çok yeni — topoloji 5 dakikalık kovalarla yazılır",
		"çağıran tarafta trace-context yayılımı yoksa kenar oluşmaz (iki uç da aynı trace'te olmalı)",
	}
}

// renderTopologyEvidenceTR — SAF: service_topology cevabı (LLM'siz, metin = cevap).
//
// v0.10.1392 — sayılar ÇİZİLEN kümeden (topoChatNeighbourhood): başlıktaki
// komşu/kenar toplamları sütun tavanından ÖNCEKİ akış kümesi, tavan notu
// yalnız gerçekten kesildiyse ve gerçek gizlenen sayılarla; sabit bütçe
// metni yok. "+N more" düğümleri satır olarak basılmaz. totalEdges = eonly
// süzgecinden önceki kenar sayısı (boş eonly cevabı için).
func renderTopologyEvidenceTR(focus string, nb topoNeighbourhood, hops int, rangeS int64, env string, eonly bool, totalEdges int) string {
	g := nb.Graph
	names := map[string]GraphNode{}
	for _, n := range g.Nodes {
		names[n.ID] = n
	}
	label := func(id string) string {
		n, ok := names[id]
		if !ok {
			return id
		}
		return n.Name + topoKindTR(n.Kind)
	}
	var up, down, far []GraphEdge
	for _, e := range g.Edges {
		if topoIsMore(e.Source) || topoIsMore(e.Target) {
			continue
		}
		switch {
		case e.Target == focus && e.Source != focus:
			up = append(up, e)
		case e.Source == focus:
			down = append(down, e)
		default:
			far = append(far, e)
		}
	}
	var b strings.Builder
	kind := "topolojisi"
	if eonly {
		kind = "hatalı bağımlılıkları"
	}
	fmt.Fprintf(&b, "%s %s (son %s, %d hop · %d komşu: %d çağıran, %d bağımlılık · %d kenar)\n",
		focus, kind, fmtAgoTR(rangeS), hops, nb.Neighbours, nb.Callers, nb.Callees, nb.Edges)
	if len(g.Edges) == 0 {
		if eonly && totalEdges > 0 {
			fmt.Fprintf(&b, "Bu pencerede hatalı bağımlılık YOK: %d kenarın hiçbirinde hata görülmedi.\n", totalEdges)
		} else {
			fmt.Fprintf(&b, "Bu pencerede %s için topoloji kenarı bulunamadı. Olası nedenler:\n", focus)
			for _, r := range topoEmptyReasonsTR(focus) {
				b.WriteString("- " + r + "\n")
			}
		}
		return strings.TrimRight(b.String(), "\n")
	}
	section := func(title string, es []GraphEdge, name func(GraphEdge) string) {
		if len(es) == 0 {
			return
		}
		b.WriteString(title + "\n")
		for _, e := range es {
			b.WriteString(topoEdgeLineTR(name(e), e) + "\n")
		}
	}
	section("ÇAĞIRANLAR (upstream):", up, func(e GraphEdge) string { return label(e.Source) })
	if nb.Callers == 0 {
		b.WriteString("ÇAĞIRANLAR (upstream): enstrümante çağıran yok (giriş servisi olabilir).\n")
	}
	section("BAĞIMLILIKLAR (downstream):", down, func(e GraphEdge) string { return label(e.Target) })
	if nb.Callees == 0 {
		b.WriteString("BAĞIMLILIKLAR (downstream): bu pencerede çağrı yok.\n")
	}
	section("2. ADIM:", far, func(e GraphEdge) string { return label(e.Source) + " → " + label(e.Target) })
	if tr := nb.Truncated; tr.Nodes > 0 {
		var cut []string
		if tr.Callers > 0 {
			cut = append(cut, fmt.Sprintf("+%d çağıran", tr.Callers))
		}
		if tr.Callees > 0 {
			cut = append(cut, fmt.Sprintf("+%d bağımlılık", tr.Callees))
		}
		fmt.Fprintf(&b, "Tavan (Topology sekmesiyle aynı): sütun başına en çok %d komşu; aşan sütunda ilk %d (önce hata >%%1, sonra çağrı sayısı) — %s (+%d kenar) \"+N more\" altında, tümü Topology sekmesinde.\n",
			topoChatMaxPerCol, topoChatMaxPerCol-1, strings.Join(cut, ", "), tr.Edges)
	}
	if nb.SideLinks > 0 {
		fmt.Fprintf(&b, "Komşular arası %d yan bağlantı (aynı sütun / sütun atlayan / döngü) çizilmedi ve sayılmadı — Topology sekmesinde \"Yan bağlantılar\".\n", nb.SideLinks)
	}
	note := "Not: kenar gecikmesi p99 (topology_edges_5m); (dış) hedefler enstrümante değil"
	if env != "" {
		note += fmt.Sprintf("; ortam (%s) topolojide süzgeç DEĞİL — kenarlar tüm ortamları kapsar", env)
	}
	b.WriteString(note + ".")
	return b.String()
}

// topoHasErrEdge — SAF: grafta hata taşıyan kenar var mı.
func topoHasErrEdge(g ServiceGraphResponse) bool {
	for _, e := range g.Edges {
		if e.Errors > 0 {
			return true
		}
	}
	return false
}

// topoWarnNeighbours — SAF: hata oranı ≥%1 servis komşuları (en kötü önce, ≤max).
func topoWarnNeighbours(g ServiceGraphResponse, focus string, max int) []string {
	var ns []GraphNode
	for _, n := range g.Nodes {
		if n.ID != focus && n.Kind == "service" && !topoIsMore(n.ID) && n.ErrorRate >= topoWarnPct { // v0.10.1392 — "+N more" komşu değil
			ns = append(ns, n)
		}
	}
	sort.SliceStable(ns, func(i, j int) bool {
		if ns[i].ErrorRate != ns[j].ErrorRate {
			return ns[i].ErrorRate > ns[j].ErrorRate
		}
		return ns[i].ID < ns[j].ID
	})
	var out []string
	for _, n := range ns {
		if len(out) >= max {
			break
		}
		out = append(out, n.ID)
	}
	return out
}

// topologyCacheKey — SAF: servis, hop, eonly, 5 dk'ya oturtulmuş uçlar,
// gizli desen özeti — hepsi FNV'de (v0.5.187).
func topologyCacheKey(svc string, hops int, eonly bool, from, to time.Time, hidDigest string) string {
	// v0.10.1392 — v2: önbellek gövdesi topoNeighbourhood (sekme görünümü).
	return "copilot:topo:v2:" + fnvStr(svc, fmt.Sprint(hops), fmt.Sprint(eonly), fmt.Sprint(from.Unix()/300), fmt.Sprint(to.Unix()/300), hidDigest)
}

// topologyTraceCacheKey — SAF: trace id + odak (bütçe odağa göre).
func topologyTraceCacheKey(traceID, focus string) string {
	return "copilot:topo-trace:v1:" + fnvStr(strings.ToLower(traceID), focus)
}

// ─── Trace grafı (SAF) ─────────────────────────────────────────────────

// topoSpanProtocol — SAF: span'in protokolü (db | kafka | messaging | grpc | http | "").
func topoSpanProtocol(sp chstore.SpanRow) string {
	switch {
	case sp.DBSystem != "" || tracechain.SpanAttr(sp, "db.system", "db.system.name") != "":
		return "db"
	case tracechain.SpanAttr(sp, "messaging.system") != "":
		if strings.EqualFold(tracechain.SpanAttr(sp, "messaging.system"), "kafka") {
			return "kafka"
		}
		return "messaging"
	case strings.EqualFold(tracechain.SpanAttr(sp, "rpc.system"), "grpc"):
		return "grpc"
	case sp.HTTPMethod != "" || tracechain.SpanAttr(sp, "http.request.method", "http.method") != "":
		return "http"
	}
	return ""
}

// topoPeerNode — SAF: yaprak CLIENT/PRODUCER span'in hedef düğümü,
// topology_edges_5m'in kimlik dilinde: db:<sistem>, queue:<sistem>:<hedef>,
// ext:<host|peer.service> (bilinen servise ext-merge katlar). "" = hedef yok.
func topoPeerNode(sp chstore.SpanRow) (id, kind string) {
	p := tracechain.PeerOf(sp)
	switch p.Kind {
	case "db":
		sys, _, _ := strings.Cut(p.Target, "/")
		return "db:" + sys, "db"
	case "messaging":
		sys, dest, ok := strings.Cut(p.Target, "/")
		if ok && dest != "" {
			return "queue:" + sys + ":" + dest, "queue"
		}
		return "queue:" + sys, "queue"
	case "http", "peer.service":
		if p.Target != "" {
			return "ext:" + p.Target, "external"
		}
	}
	return "", ""
}

// topoSpanFailed — SAF: span hatalı mı (status error ya da HTTP ≥500).
func topoSpanFailed(sp chstore.SpanRow) bool {
	f := tracechain.SpanFailure(sp)
	return f.Status == "error" || f.HTTPCode >= 500
}

// traceTopologyEdges — SAF: span ağacından servis kenarları. Ebeveynle servisi
// farklı her span bir çağrı (ebeveyn servis → span servisi); başka servise
// çocuğu olmayan CLIENT/PRODUCER span dış hedefe (db/kuyruk/ext) kenardır.
// Kenar: çağrı = span sayısı, hata = hatalı span, AvgMs ortalama, P99Ms en
// büyük süre (tek trace'te yüzdelik anlamsız — en uzun çağrı).
func traceTopologyEdges(spans []chstore.SpanRow) []chstore.ServiceTopologyEdge {
	byID := make(map[string]int, len(spans))
	for i, sp := range spans {
		if sp.SpanID != "" {
			if _, dup := byID[sp.SpanID]; !dup {
				byID[sp.SpanID] = i
			}
		}
	}
	crossChild := map[string]bool{}
	for _, sp := range spans {
		if pi, ok := byID[sp.ParentSpanID]; ok && sp.ParentSpanID != "" && spans[pi].ServiceName != sp.ServiceName {
			crossChild[sp.ParentSpanID] = true
		}
	}
	type acc struct {
		e     chstore.ServiceTopologyEdge
		sumMs float64
	}
	agg := map[string]*acc{}
	var order []string
	add := func(parent, child, kind, proto string, sp chstore.SpanRow) {
		k := parent + "\x00" + child
		a := agg[k]
		if a == nil {
			a = &acc{e: chstore.ServiceTopologyEdge{ParentService: parent, ChildNode: child, NodeKind: kind, Protocol: proto}}
			agg[k] = a
			order = append(order, k)
		}
		ms := float64(tracechain.SpanDurNs(sp)) / 1e6
		a.e.Calls++
		if topoSpanFailed(sp) {
			a.e.Errors++
		}
		a.sumMs += ms
		if ms > a.e.P99Ms {
			a.e.P99Ms = ms
		}
		if a.e.Protocol == "" {
			a.e.Protocol = proto
		}
	}
	for _, sp := range spans {
		svc := strings.TrimSpace(sp.ServiceName)
		if svc == "" {
			continue
		}
		if pi, ok := byID[sp.ParentSpanID]; ok && sp.ParentSpanID != "" {
			p := spans[pi]
			if ps := strings.TrimSpace(p.ServiceName); ps != "" && ps != svc {
				proto := topoSpanProtocol(p)
				if proto == "" {
					proto = topoSpanProtocol(sp)
				}
				add(ps, svc, "service", proto, sp)
			}
		}
		if tracechain.KindRank(sp.Kind) == 1 && !crossChild[sp.SpanID] {
			if id, kind := topoPeerNode(sp); id != "" && id != "ext:"+svc {
				add(svc, id, kind, topoSpanProtocol(sp), sp)
			}
		}
	}
	out := make([]chstore.ServiceTopologyEdge, 0, len(order))
	for _, k := range order {
		a := agg[k]
		if a.e.Calls > 0 {
			a.e.AvgMs = a.sumMs / float64(a.e.Calls)
			a.e.ErrorRate = float64(a.e.Errors) / float64(a.e.Calls) * 100
		}
		out = append(out, a.e)
	}
	return out
}

// topoTraceRoot — SAF: kök span (ebeveyni kümede olmayan en erken span).
func topoTraceRoot(spans []chstore.SpanRow) (chstore.SpanRow, bool) {
	ids := make(map[string]bool, len(spans))
	for _, sp := range spans {
		ids[sp.SpanID] = true
	}
	best, found := chstore.SpanRow{}, false
	for _, sp := range spans {
		if sp.ParentSpanID != "" && ids[sp.ParentSpanID] {
			continue
		}
		if !found || sp.StartTime < best.StartTime {
			best, found = sp, true
		}
	}
	return best, found
}

// traceServiceGraph — SAF: trace kenarları → ServiceGraphResponse (Service
// Map'in kurucusu; ext-merge bilinen servis kümesiyle), Scope "trace".
func traceServiceGraph(spans []chstore.SpanRow, focus string, known *chstore.ServiceNameSet) ServiceGraphResponse {
	g := buildServiceGraphWith(traceTopologyEdges(spans), focus, "trace", 0, nil, 1, known)
	g.Scope = "trace"
	return g
}

// pickTopologyTrace — SAF: temsilci trace. Açık id (explicit) kazanır.
// errorsOnly → en yavaş hatalı (eşitlikte en erken); hatalı yoksa ok=false.
// Aksi → hatasızlar (yoksa hepsi) içinde span sayısı medyanı (alt medyan;
// eşitlikte en erken başlangıç).
func pickTopologyTrace(rows []chstore.TraceRow, errorsOnly bool) (chstore.TraceRow, bool) {
	if errorsOnly {
		var best chstore.TraceRow
		found := false
		for _, r := range rows {
			if !r.HasError {
				continue
			}
			if !found || r.DurationMs > best.DurationMs || (r.DurationMs == best.DurationMs && r.StartTime < best.StartTime) {
				best, found = r, true
			}
		}
		return best, found
	}
	var cands []chstore.TraceRow
	for _, r := range rows {
		if !r.HasError {
			cands = append(cands, r)
		}
	}
	if len(cands) == 0 {
		cands = append(cands, rows...)
	}
	if len(cands) == 0 {
		return chstore.TraceRow{}, false
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].SpanCount != cands[j].SpanCount {
			return cands[i].SpanCount < cands[j].SpanCount
		}
		return cands[i].StartTime < cands[j].StartTime
	})
	return cands[(len(cands)-1)/2], true
}

// topoTraceFacts — trace grafının önbelleğe giren gövdesi.
type topoTraceFacts struct {
	Graph      ServiceGraphResponse `json:"graph"`
	Truncated  topoTruncation       `json:"truncated"`
	Source     string               `json:"source"`
	RootOp     string               `json:"rootOp"`
	RootSvc    string               `json:"rootSvc"`
	DurationMs float64              `json:"durationMs"`
	SpanCount  int                  `json:"spanCount"`
	Services   int                  `json:"services"`
	StartNs    int64                `json:"startNs"`
}

// topoTraceFactsFrom — SAF: span'lerden trace grafı + künye (bütçe uygulanmış).
func topoTraceFactsFrom(spans []chstore.SpanRow, focus, source string, known *chstore.ServiceNameSet) topoTraceFacts {
	f := topoTraceFacts{Source: source, SpanCount: len(spans)}
	root, ok := topoTraceRoot(spans)
	if ok {
		f.RootOp, f.RootSvc, f.StartNs = root.Name, root.ServiceName, root.StartTime
		if root.HTTPRoute != "" && !strings.Contains(root.Name, "/") {
			f.RootOp = strings.TrimSpace(root.Name + " " + root.HTTPRoute)
		}
	}
	var minS, maxE int64
	svcs := map[string]bool{}
	for i, sp := range spans {
		if sp.ServiceName != "" {
			svcs[sp.ServiceName] = true
		}
		end := sp.StartTime + tracechain.SpanDurNs(sp)
		if i == 0 || sp.StartTime < minS {
			minS = sp.StartTime
		}
		if end > maxE {
			maxE = end
		}
	}
	f.Services = len(svcs)
	if maxE > minS {
		f.DurationMs = float64(maxE-minS) / 1e6
	}
	if focus == "" || !svcs[focus] {
		focus = f.RootSvc
	}
	g := traceServiceGraph(spans, focus, known)
	f.Graph, f.Truncated = topoChatBudget(g, focus, false, topoChatMaxNodes, topoChatMaxEdges)
	f.Graph.Focus = focus
	return f
}

// renderTraceTopologyTR — SAF: trace_topology cevabı.
func renderTraceTopologyTR(traceID, op string, f topoTraceFacts, pickNote string) string {
	id12 := traceID
	if len(id12) > 12 {
		id12 = id12[:12]
	}
	src := "CH"
	if f.Source == traceSourceTempo {
		src = "Tempo"
	}
	head := "Örnek trace"
	if op != "" {
		head += " (" + op + ")"
	}
	var b strings.Builder
	rootOp := f.RootOp
	if rootOp == "" {
		rootOp = "kök span yok"
	}
	fmt.Fprintf(&b, "%s: %s · %s · %s · %d span · %d servis (kaynak: %s)\n", head, id12, rootOp, tracechain.FmtMs(f.DurationMs), f.SpanCount, f.Services, src)
	if pickNote != "" {
		b.WriteString(pickNote + "\n")
	}
	names := map[string]string{}
	for _, n := range f.Graph.Nodes {
		names[n.ID] = n.Name + topoKindTR(n.Kind)
	}
	label := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return id
	}
	if len(f.Graph.Edges) == 0 {
		b.WriteString("Bu trace tek serviste kalıyor: servisler arası çağrı ya da dış hedef yok.")
		return b.String()
	}
	for _, e := range f.Graph.Edges {
		fmt.Fprintf(&b, "- %s → %s  %d çağrı · %d hata%s · %s toplam", label(e.Source), label(e.Target), e.Calls, e.Errors, topoRateMark(e.ErrorRate), tracechain.FmtMs(e.AvgMs*float64(e.Calls)))
		if e.Protocol != "" {
			fmt.Fprintf(&b, " (%s)", e.Protocol)
		}
		b.WriteString("\n")
	}
	if f.Truncated.Nodes > 0 || f.Truncated.Edges > 0 {
		fmt.Fprintf(&b, "Bütçe: ilk %d düğüm / %d kenar (+%d düğüm, +%d kenar trace sayfasında).\n", topoChatMaxNodes, topoChatMaxEdges, f.Truncated.Nodes, f.Truncated.Edges)
	}
	b.WriteString("Not: kenar = bu TEK trace'teki çağrılar (sayı span adedi; süre toplamı), filo geneli değil — filo topolojisi Topology sekmesinde.")
	return b.String()
}

// ─── Blok ───────────────────────────────────────────────────────────────

// topologyBlockPayload — `topology` bloğu (blocks.TypeTopology; FE
// ChatTopologyPayload / ChatTopologyCard). Düğüm/kenar Service Map tipleri.
type topologyBlockPayload struct {
	Focus      string          `json:"focus"`
	Scope      string          `json:"scope"` // neighborhood | trace
	Hops       int             `json:"hops"`
	Window     traceListWindow `json:"window"`
	RangeS     int64           `json:"rangeS"`
	Nodes      []GraphNode     `json:"nodes"`
	Edges      []GraphEdge     `json:"edges"`
	DeepLink   string          `json:"deepLink"`
	TraceID    string          `json:"traceId,omitempty"`
	TraceHref  string          `json:"traceHref,omitempty"`
	Operation  string          `json:"operation,omitempty"`
	Truncated  topoTruncation  `json:"truncated"`
	ErrorsOnly bool            `json:"errorsOnly,omitempty"`
	Note       string          `json:"note,omitempty"`
}

// topoOpChoiceChip — SAF: operasyon belirsizlik çipi (topoloji akışında kalır).
func topoOpChoiceChip(op string) string {
	if strings.Contains(op, "/") {
		return op + " örnek trace topolojisi"
	}
	return op + " operasyonunun örnek trace topolojisi"
}

// fuOpTopoRe — operasyon topolojisi çipinin operasyon biçimi.
var fuOpTopoRe = regexp.MustCompile(`^[A-Za-z0-9/][A-Za-z0-9 ._:/{}*-]{0,159}$`)

// ─── Demet ──────────────────────────────────────────────────────────────

// guidedTopologyAnswer — iki niyetin LLM'siz cevabı. Okuma hatası serbest
// döngüye bırakmaz: dürüst metin + linkler (Topology sekmesi her zaman var).
func (s *Server) guidedTopologyAnswer(ctx context.Context, emit func(string, any), route guidedRoute, msgs []string, from, to time.Time, rangeS int64) (bool, bool) {
	tctx, cancel := context.WithTimeout(ctx, topoChatTimeout)
	defer cancel()
	var text string
	var block *topologyBlockPayload
	var traceList *traceListPayload
	stop := false
	if route.Intent == guidedServiceTopology {
		text, block = s.serviceTopologyRun(tctx, emit, &route, from, to, rangeS)
	} else {
		text, block, traceList, stop = s.traceTopologyRun(tctx, emit, &route, from, to, rangeS)
	}
	if stop { // belirsizlik çipleri yayınlandı
		return true, true
	}
	if traceList != nil && len(traceList.Traces) > 0 {
		emit("trace_list", *traceList)
	}
	if block != nil {
		emit(blocks.TypeTopology, *block)
	}
	ans := map[string]any{"text": text, "suggestions": guidedSuggestions(route),
		"links": dedupLinksByHref(guidedAnswerLinks(route, linkWindowBetween(from, to)))}
	emit("answer", withGuidedFollowups(ans, guidedFollowups(route, msgs)))
	return true, true
}

// serviceTopologyRun — service_topology: MV okuması (60 sn önbellek) → bütçe → metin + blok.
func (s *Server) serviceTopologyRun(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, *topologyBlockPayload) {
	svc, hops, eonly := route.Service, topoRouteHops(*route), route.TopoErrorsOnly
	n := emitGuidedStep(emit, "get_topology", withEnvArg(fmt.Sprintf(`{"service":%s,"hops":%d,"range_s":%d}`, jsonStr(svc), hops, rangeS), route.Env))
	hid := s.topologyHiddenPatterns(ctx)
	key := topologyCacheKey(svc, hops, eonly, from, to, hiddenDigest(hid))
	// v0.10.1392 — gövde Topology sekmesinin görünümü (topoChatNeighbourhood):
	// çizilen küme ile metindeki sayılar aynı kaynaktan.
	type cached struct {
		Nb         topoNeighbourhood `json:"nb"`
		TotalEdges int               `json:"totalEdges"`
	}
	var c cached
	if s.metricChartCacheGet(ctx, key, &c) {
		emitGuidedContextStep(emit, "önbellek: aynı topoloji (≤60 sn)")
	} else {
		edges, err := s.store.ReadServiceTopologyAggForFocus(ctx, from, to, svc, hops, topoChatReadLimit)
		if err != nil {
			text := fmt.Sprintf("%s topolojisi OKUNAMADI (okunamadı ≠ kenar yok): %v. Topology sekmesinden bakabilirsin.", svc, err)
			emitGuidedStepResult(emit, n, "get_topology", text, err)
			return text, nil
		}
		edges = filterHiddenTopologyEdges(edges, hid)
		dbNames, _ := s.store.DbNamesBySystem(ctx, from, to)
		g := buildServiceGraphWith(edges, svc, "neighborhood", hops, dbNames, serviceGraphWindowMinutes(from, to), s.knownServiceSet(ctx))
		c.TotalEdges = len(g.Edges)
		c.Nb = topoChatNeighbourhood(g, svc, hops, eonly, topoChatMaxPerCol)
		s.metricChartCacheSet(ctx, key, c, topoChatCacheTTL)
	}
	graph := c.Nb.Graph
	route.TopoHasErrEdge = topoHasErrEdge(graph)
	route.TopoNeighbours = topoWarnNeighbours(graph, svc, topoNeighbourChips)
	text := renderTopologyEvidenceTR(svc, c.Nb, hops, rangeS, route.Env, eonly, c.TotalEdges)
	emitGuidedStepResult(emit, n, "get_topology", text, nil)
	if len(graph.Edges) == 0 {
		return text, nil
	}
	win := linkWindowBetween(from, to)
	note := "kenar gecikmesi p99 · topology_edges_5m"
	if route.Env != "" {
		note += " · ortam süzgeç değil"
	}
	return text, &topologyBlockPayload{Focus: svc, Scope: "neighborhood", Hops: hops,
		Window: traceListWindow{FromNs: from.UnixNano(), ToNs: to.UnixNano()}, RangeS: rangeS,
		Nodes: graph.Nodes, Edges: graph.Edges, DeepLink: win.apply(topologyTabHref(svc, hops, eonly)),
		Truncated: c.Nb.Truncated, ErrorsOnly: eonly, Note: note}
}

// topoOpResolve — operasyon anmasını katalogla hedefe çevirir. choices dolu →
// belirsizlik; ok=false + choices boş → bulunamadı (msg dürüst metin).
func (s *Server) topoOpResolve(ctx context.Context, emit func(string, any), op, raw, svcHint string) (t opTarget, oracleSources []string, choices []string, msg string, ok bool) {
	if method, path, isEP := endpointMention(op); isEP {
		toks := endpointCatalogTokens(path)
		cands, err := s.store.CatalogCandidates(ctx, toks, opCandidateLimit)
		emitCatalogStep(emit, toks, len(cands), err)
		if err == nil && len(cands) > 0 {
			hint := svcHint
			if hint == "" {
				hint = endpointServiceHint(raw, path, cands)
			}
			m := matchEndpoint(method, path, cands, hint)
			switch {
			case m.Pick != nil:
				return endpointTargetFor(*m.Pick, method), nil, nil, "", true
			case len(m.Choices) > 1:
				for i, g := range m.Choices {
					if i >= topoOpChoiceMax {
						break
					}
					choices = append(choices, g.Service+" "+topoOpChoiceChip(endpointLabel(g, method)))
				}
				return t, nil, choices, fmt.Sprintf("%s %d serviste eşleşti ve hiçbiri baskın değil; hangisinin örnek trace topolojisini çizeyim?", op, len(m.Choices)), false
			}
		}
		// Katalogda yok → yol-yalnız hedef (trace'ler yol metniyle aranır, v0.10.1373).
		pt := endpointPathOnlyTarget(raw, method, path, s.guidedServiceNames(ctx), s.guidedEnvNames(ctx))
		return pt, nil, nil, "", true
	}
	toks := opTokens(op)
	if len(toks) == 0 {
		return t, nil, nil, fmt.Sprintf("%q operasyon adı olarak çözülemedi (en az %d karakter).", op, opTokenMin), false
	}
	cands, err := s.store.CatalogCandidates(ctx, toks, opCandidateLimit)
	emitCatalogStep(emit, toks, len(cands), err)
	if err != nil {
		return t, nil, nil, fmt.Sprintf("Operasyon kataloğu OKUNAMADI (okunamadı ≠ yok): %v.", err), false
	}
	m := matchOperations(toks, cands)
	switch {
	case len(m.Ops) == 0:
		return t, nil, nil, fmt.Sprintf("%q operasyon kataloğunda (son 24 sa span adı / http route / Oracle operasyonu) BULUNAMADI — örnek trace topolojisi çizilmedi. Yazımı kontrol et ya da servis adıyla sor (\"<servis> örnek trace topolojisi\").", op), false
	case len(m.Ops) > 1:
		for i, o := range m.Ops {
			if i >= topoOpChoiceMax {
				break
			}
			choices = append(choices, topoOpChoiceChip(o))
		}
		return t, nil, choices, fmt.Sprintf("%q %d operasyonla eşleşti; hangisinin örnek trace topolojisini çizeyim?", op, len(m.Ops)), false
	}
	t = opTargetFor(m.Ops[0], cands)
	if svcHint != "" && len(t.Services) > 1 {
		for _, sv := range t.Services {
			if sv == svcHint {
				t.Services = []string{svcHint}
				break
			}
		}
	}
	for _, c := range cands {
		if c.Operation == t.Name && c.Source == "oracle" && c.Service != "" {
			oracleSources = append(oracleSources, c.Service)
		}
	}
	return t, oracleSources, nil, "", true
}

// traceTopologyRun — trace_topology: temsilci trace → Tempo-önce span'ler → graf.
// stop=true → belirsizlik çipli cevap yayınlandı.
func (s *Server) traceTopologyRun(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, *topologyBlockPayload, *traceListPayload, bool) {
	eonly := route.TopoErrorsOnly
	var rows []chstore.TraceRow
	var listHref, listQuery, pickNote, op string
	traceID := strings.ToLower(strings.TrimSpace(route.TopoTraceID))
	var oracleIDs []string
	switch {
	case traceID != "":
		pickNote = "Seçim: açık trace kimliği."
	case route.TopoOperation != "":
		t, oraSrc, choices, msg, ok := s.topoOpResolve(ctx, emit, route.TopoOperation, route.TopoOperation, route.Service)
		if len(choices) > 0 {
			emit("answer", map[string]any{"text": msg, "suggestions": choices})
			return "", nil, nil, true
		}
		if !ok {
			return msg, nil, nil, false
		}
		op = route.TopoOperation
		if t.Route != "" && !strings.Contains(op, "/") {
			op = strings.TrimSpace(t.Method + " " + t.Route)
		} else if t.SearchPath == "" && t.Name != "" && !strings.Contains(op, "/") {
			op = t.Name
		}
		route.TopoOperation = op
		svc := ""
		if len(t.Services) == 1 {
			svc = t.Services[0]
			if route.Service == "" {
				route.Service = svc
			}
		}
		sc := s.oracleOpScope()
		search := ""
		if t.SearchPath != "" {
			search = pathSearchText(t.SearchPath)
		}
		n := emitGuidedStep(emit, "search_traces", withEnvArg(fmt.Sprintf(`{"operation":%s,"sort":"duration","limit":%d,"errors_only":%v,"range_s":%d}`, jsonStr(op), topoTraceSampleN, eonly, rangeS), route.Env))
		seen := map[string]bool{}
		for _, fx := range opTraceFilterSets(t, search, sc.SpanKey) {
			f := traceSearchFilter(svc, route.Env, search, false, from, to)
			f.Filters, f.HasError, f.Limit = fx, eonly, topoTraceSampleN
			got, _, _, err := s.store.GetTraces(ctx, f)
			if err != nil {
				continue
			}
			for _, r := range got {
				if !seen[r.TraceID] {
					seen[r.TraceID] = true
					rows = append(rows, r)
				}
			}
		}
		emitGuidedStepResult(emit, n, "search_traces", fmt.Sprintf("%d trace", len(rows)), nil)
		listHref, listQuery = opTracesHref(t, sc.SpanKey, from, to), op
		if len(rows) == 0 && t.HasOra && len(oraSrc) > 0 {
			// Oracle-only operasyon: span'i örneklemeye girmemiş olabilir —
			// Oracle satırlarının trace id'leri (son 24 sa, en yeni önce).
			on := emitGuidedStep(emit, "oracle_trace_ids", fmt.Sprintf(`{"operation":%s,"range_s":%d}`, jsonStr(t.Name), int64(topoOracleLookback/time.Second)))
			for _, src := range oraSrc {
				ids, err := s.store.OracleTraceIDsForOperation(ctx, src, t.Name, to.Add(-topoOracleLookback), to, topoOracleTraceIDs)
				if err == nil {
					oracleIDs = append(oracleIDs, ids...)
				}
			}
			emitGuidedStepResult(emit, on, "oracle_trace_ids", fmt.Sprintf("%d trace id", len(oracleIDs)), nil)
		}
	default:
		n := emitGuidedStep(emit, "search_traces", withEnvArg(fmt.Sprintf(`{"service":%s,"sort":"duration","limit":%d,"errors_only":%v,"range_s":%d}`, jsonStr(route.Service), topoTraceSampleN, eonly, rangeS), route.Env))
		f := guidedTraceFilter(route.Service, route.Env, from, to)
		f.HasError = eonly
		got, _, _, err := s.store.GetTraces(ctx, f)
		if err != nil {
			text := fmt.Sprintf("%s trace'leri OKUNAMADI (okunamadı ≠ yok): %v.", route.Service, err)
			emitGuidedStepResult(emit, n, "search_traces", text, err)
			return text, nil, nil, false
		}
		rows = got
		emitGuidedStepResult(emit, n, "search_traces", fmt.Sprintf("%d trace", len(rows)), nil)
		q := url.Values{}
		q.Set("service", route.Service)
		q.Set("sort", "duration")
		if eonly {
			q.Set("hasError", "true")
		}
		listHref = linkWindowBetween(from, to).apply("/traces?" + q.Encode())
		listQuery = route.Service
	}
	if traceID == "" {
		if pick, ok := pickTopologyTrace(rows, eonly); ok {
			traceID = pick.TraceID
			if eonly {
				pickNote = fmt.Sprintf("Seçim: son %s'in en yavaş hatalı trace'i (%d aday).", fmtAgoTR(rangeS), len(rows))
			} else {
				pickNote = fmt.Sprintf("Seçim: son %s'in en yavaş %d trace'i içinde hatasızların span sayısı medyanı.", fmtAgoTR(rangeS), len(rows))
			}
		}
	}
	var spans []chstore.SpanRow
	source := ""
	gn := 0
	if traceID != "" {
		gn = emitGuidedStep(emit, "get_trace", fmt.Sprintf(`{"trace_id":%s}`, jsonStr(traceID)))
	}
	key := topologyTraceCacheKey(traceID, route.Service)
	var facts topoTraceFacts
	haveFacts := traceID != "" && s.metricChartCacheGet(ctx, key, &facts)
	if traceID != "" && !haveFacts {
		var err error
		spans, source, err = s.resolveTraceSpans(ctx, traceID)
		if err != nil || len(spans) == 0 {
			emitGuidedStepResult(emit, gn, "get_trace", "trace bulunamadı", err)
			traceID = ""
		}
	}
	if traceID == "" && len(oracleIDs) > 0 {
		pickNote = "Seçim: operasyonun span'i örneklemeye girmemiş; Oracle satırlarının en yeni trace id'si (Tempo-önce)."
		for i, id := range oracleIDs {
			if i >= topoOracleTempoTries {
				break
			}
			sp, src, err := s.resolveTraceSpans(ctx, id)
			if err == nil && len(sp) > 0 {
				traceID, spans, source = strings.ToLower(id), sp, src
				gn = emitGuidedStep(emit, "get_trace", fmt.Sprintf(`{"trace_id":%s}`, jsonStr(traceID)))
				key = topologyTraceCacheKey(traceID, route.Service)
				break
			}
		}
	}
	if traceID == "" {
		subject := route.Service
		if op != "" {
			subject = op + " operasyonu"
		}
		kind := "trace"
		if eonly {
			kind = "hatalı trace"
		}
		return fmt.Sprintf("%s için son %s içinde örnek %s bulunamadı (Coremetry'de ve Tempo'da) — trace topolojisi çizilmedi. Servis topolojisi için \"%s topolojisi\" diyebilirsin.", subject, fmtAgoTR(rangeS), kind, strings.TrimSpace(route.Service)), nil, nil, false
	}
	if !haveFacts {
		facts = topoTraceFactsFrom(spans, route.Service, source, s.knownServiceSet(ctx))
		s.metricChartCacheSet(ctx, key, facts, topoTraceCacheTTL)
	}
	if route.Service == "" {
		route.Service = facts.Graph.Focus
	}
	route.TopoTraceOut = traceID
	text := renderTraceTopologyTR(traceID, op, facts, pickNote)
	if gn > 0 {
		emitGuidedStepResult(emit, gn, "get_trace", text, nil)
	}
	var tl *traceListPayload
	if len(rows) > 0 && listHref != "" {
		p := endpointTraceListPayload(listQuery, rows, from, to, listHref)
		p.Truncated = len(rows) >= topoTraceSampleN
		tl = &p
	}
	win := linkWindowBetween(from, to)
	block := &topologyBlockPayload{Focus: facts.Graph.Focus, Scope: "trace", Hops: 0,
		Window: traceListWindow{FromNs: from.UnixNano(), ToNs: to.UnixNano()}, RangeS: rangeS,
		Nodes: facts.Graph.Nodes, Edges: facts.Graph.Edges, DeepLink: win.apply(topologyTabHref(facts.Graph.Focus, 1, false)),
		TraceID: traceID, TraceHref: "/trace?id=" + traceID, Operation: op, Truncated: facts.Truncated, ErrorsOnly: eonly,
		Note: "tek trace · kenar = span adedi · süre toplamı"}
	if facts.Graph.Focus == "" {
		block.DeepLink = "/service-map"
	}
	if len(facts.Graph.Edges) == 0 {
		block = nil
	}
	return text, block, tl, false
}
