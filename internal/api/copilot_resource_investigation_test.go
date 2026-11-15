package api

// copilot_resource_investigation_test.go — v0.10.1431 (CoSRE kaynak incelemesi
// R1). Operatör: "CoSRE sorulan sorulara frontier model gibi anlamlandırarak
// gerekli toolset'lerini koşsun … bir pod'un CPU/memory metriklerini grafik
// olarak getirme; gerekirse GC'sine, JVM metriklerine bakıp LLM'e yorumlatma."
//
// Router TR/EN tablosu (pozitif + negatif: grafik fiili → metric_chart 1386
// pini, "pod'ları nasıl" → pod_health, heap → pod_health (R1), takım/çift/
// topoloji/log/deploy şekilleri), pod adı / statefulset / workload / cluster
// anması, ask_service çiplerinin gidiş-dönüşü, takip çiplerinin pinli
// niyetleri, linkler. Synthetic adlar yalnız (orders-api, kxzone).
// v0.10.1432: heap/gc pinleri pod_health'ten resource_investigation'a taşındı
// (resource_jvm_test.go); "GC/heap'e bak" çipi jvm ailesine döner.

import (
	"strings"
	"testing"
)

var riServices = []string{"orders-api", "payments-api", "checkout-service"}
var riEnvs = []string{"prod"}

const riPod = "orders-api-6d4f8b9c7d-x2k9q"

func TestRouteResourceInvestigation(t *testing.T) {
	type want struct {
		intent guidedIntent
		kind   string
		target string
		svc    string
		family string
		clus   string
	}
	ri := guidedResourceInvestigation
	cases := []struct {
		msg, ctx string
		w        want
	}{
		// ── pozitif: servis
		{"orders-api cpu kullanımı nasıl", "", want{ri, resKindService, "orders-api", "orders-api", resFamCPU, ""}},
		{"orders-api belleği limite yakın mı", "", want{ri, resKindService, "orders-api", "orders-api", resFamMemory, ""}},
		{"orders-api RAM kullanımı", "", want{ri, resKindService, "orders-api", "orders-api", resFamMemory, ""}},
		{"orders-api pod'ları neden restart ediyor", "", want{ri, resKindService, "orders-api", "orders-api", resFamRestarts, ""}},
		{"orders-api pod'ları ölüyor", "", want{ri, resKindService, "orders-api", "orders-api", resFamRestarts, ""}},
		{"orders-api crashloop'ta mı", "", want{ri, resKindService, "orders-api", "orders-api", resFamRestarts, ""}},
		{"orders-api kaynak kullanımı nasıl", "", want{ri, resKindService, "orders-api", "orders-api", resFamAll, ""}},
		{"orders-api OOMKilled oluyor mu", "", want{ri, resKindService, "orders-api", "orders-api", resFamAll, ""}},
		{"orders-api cpu ve bellek durumu", "", want{ri, resKindService, "orders-api", "orders-api", resFamAll, ""}},
		{"orders-api cpu throttling var mı", "", want{ri, resKindService, "orders-api", "orders-api", resFamCPU, ""}},
		{"orders-api neden yüksek cpu kullanıyor", "", want{ri, resKindService, "orders-api", "orders-api", resFamCPU, ""}},
		{"what is the memory usage of orders-api", "", want{ri, resKindService, "orders-api", "orders-api", resFamMemory, ""}},
		{"is orders-api saturated? resource usage", "", want{ri, resKindService, "orders-api", "orders-api", resFamAll, ""}},
		// ── pozitif: pod adı (sahip servis Infrastructure regex'iyle)
		// v0.10.2239 (S4): pod'un sahibi servis router'da ad önekinden DEĞİL,
		// demette resolver'dan (resolveResourcePods) — burada boş.
		{riPod + " neden restart ediyor", "", want{ri, resKindPod, riPod, "", resFamRestarts, ""}},
		{riPod + " pod'unun cpu ve belleği", "", want{ri, resKindPod, riPod, "", resFamAll, ""}},
		{"payments-worker-0 pod'u restart ediyor mu", "", want{ri, resKindPod, "payments-worker-0", "", resFamRestarts, ""}},
		// ── pozitif: workload + cluster anması
		{"deployment orders-worker cpu kullanımı", "", want{ri, resKindWorkload, "orders-worker", "", resFamCPU, ""}},
		{"orders-api cpu kullanımı nasıl (kxzone cluster)", "", want{ri, resKindService, "orders-api", "orders-api", resFamCPU, "kxzone"}},
		{"orders-api kxzone cluster'ında bellek kullanımı", "", want{ri, resKindService, "orders-api", "orders-api", resFamMemory, "kxzone"}},
		// ── pozitif: ekran servisi
		{"cpu kullanımı nasıl", "orders-api", want{ri, resKindService, "orders-api", "orders-api", resFamCPU, ""}},
		// ── çok aday → ask_service (AskIntent resource_investigation)
		{"api cpu kullanımı nasıl", "", want{guidedAskService, "", "", "", resFamCPU, ""}},

		// ── negatif: grafik fiili metric_chart'ta (1386 D2 değişmedi)
		{"orders-api cpu grafiği", "", want{intent: guidedMetricChart}},
		{"orders-api bellek kullanımını çiz", "", want{intent: guidedMetricChart}},
		{"orders-api cpu trendi", "", want{intent: guidedMetricChart}},
		// ── negatif: pod envanteri pod_health'te (JVM sözcükleri v0.10.1432'de taşındı —
		// resource_jvm_test.go TestRouteResourceJVM)
		{"orders-api pod'ları nasıl", "", want{intent: guidedPodHealth}},
		// ── negatif: spesifik şekiller kendi yolunda
		{"orders-api restart sonrası hata logları", "", want{intent: guidedLogErrors}},
		{"orders-api deploy sonrası bellek arttı mı", "", want{intent: guidedDeployImpact}},
		{"orders-api topolojisi cpu", "", want{intent: guidedServiceTopology}},
		{"takımımın servislerinin cpu kullanımı", "", want{intent: guidedMyServices}},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.msg, riServices, riEnvs, nil, c.ctx)
		if r.Intent != c.w.intent {
			t.Errorf("%q → %s, want %s", c.msg, r.Intent, c.w.intent)
			continue
		}
		if c.w.intent == guidedAskService {
			if r.AskIntent != guidedResourceInvestigation || len(r.ServiceOptions) < 2 || r.ResFamily != c.w.family {
				t.Errorf("%q ask: %+v", c.msg, r)
			}
			continue
		}
		if c.w.intent != ri {
			continue
		}
		if r.ResKind != c.w.kind || r.ResTarget != c.w.target || r.Service != c.w.svc || r.ResFamily != c.w.family || r.ResCluster != c.w.clus {
			t.Errorf("%q → kind=%q target=%q svc=%q fam=%q cluster=%q; want %+v", c.msg, r.ResKind, r.ResTarget, r.Service, r.ResFamily, r.ResCluster, c.w)
		}
		if !hasGuidedSignal(normalizeGuidedMsg(c.msg)) {
			t.Errorf("%q hasGuidedSignal false — katalog okumadan serbest döngüye düşer", c.msg)
		}
	}
	// Hedefsiz filo sorusu ve kaynak-dışı "kaynak"/"limit" rota ÜRETMEZ.
	for _, msg := range []string{"cpu kullanımı yüksek mi", "kaynak kodu nerede", "orders-api rate limit aşıldı mı", "veri kaynağı ne"} {
		if r := routeGuidedIntent(msg, riServices, riEnvs, nil, ""); r.Intent == guidedResourceInvestigation {
			t.Errorf("%q kaynak incelemesine düşmemeli: %+v", msg, r)
		}
	}
	// Pod adı (sahip servis çözülmese de) somut özne: çekmece bağlamı bastırmaz.
	if r := routeGuidedIntent("payments-worker-0 pod'u restart ediyor mu", riServices, riEnvs, nil, ""); r.Service != "" || drawerSuppressesGuided("ekrandaki açıklama", r, "payments-worker-0 pod'u restart ediyor mu") {
		t.Errorf("pod adı çekmece bağlamında bastırıldı: %+v", r)
	}
	// İki servis adı → kıyas/aile yolu (kaynak paketi tek hedeflidir).
	if r := routeGuidedIntent("orders-api ve payments-api cpu kıyasla", riServices, riEnvs, nil, ""); r.Intent == guidedResourceInvestigation {
		t.Errorf("iki servis kaynak incelemesine düştü: %+v", r)
	}
}

func TestResourceFamilyOf(t *testing.T) {
	cases := []struct {
		msg    string
		podCtx bool
		want   string
	}{
		{"rampa yapıldı", false, ""},
		{"ram", false, resFamMemory},
		{"oom oldu", false, resFamMemory},
		{"oomkilled", false, resFamAll},
		{"working set yüksek", false, resFamMemory},
		{"yeniden başlıyor", false, resFamRestarts},
		{"düşüyor", false, ""},
		{"düşüyor", true, resFamRestarts},
		{"kaynak kodu", true, ""},
		{"kaynak", false, ""},
		{"kaynak", true, resFamAll},
		{"cpu limiti", false, resFamCPU},
		{"limitler yeterli mi", false, resFamAll},
		{"rate limit", false, ""},
		{"doygunluk", false, resFamAll},
	}
	for _, c := range cases {
		if got := resourceFamilyOf(guidedTokens(normalizeGuidedMsg(c.msg)), c.podCtx); got != c.want {
			t.Errorf("%q (pod=%v) → %q, want %q", c.msg, c.podCtx, got, c.want)
		}
	}
}

// Çip cümleleri kendi başına AYNI aileye, hedefe ve cluster'a yönlenir
// (ask_service, pod/cluster netleştirmesi, "Diğer pod'lar").
func TestResourceChipsRouteBack(t *testing.T) {
	for _, fam := range []string{resFamCPU, resFamMemory, resFamRestarts, resFamAll} {
		for _, tc := range []struct{ target, kind, clus string }{
			{"orders-api", resKindService, ""},
			{"orders-api", resKindService, "kxzone"},
			{riPod, resKindPod, ""},
			{riPod, resKindPod, "kxzone"},
		} {
			chip := resChipText(tc.target, fam, tc.clus)
			r := routeGuidedIntent(chip, riServices, riEnvs, nil, "")
			if r.Intent != guidedResourceInvestigation || r.ResFamily != fam || r.ResTarget != tc.target || r.ResKind != tc.kind || r.ResCluster != tc.clus {
				t.Errorf("çip %q → %+v", chip, r)
			}
		}
	}
	ask := routeGuidedIntent("api bellek kullanımı nasıl", riServices, riEnvs, nil, "")
	chips := guidedSuggestions(ask)
	if len(chips) < 2 {
		t.Fatalf("ask çipleri: %v", chips)
	}
	for _, c := range chips {
		r := routeGuidedIntent(c, riServices, riEnvs, nil, "")
		if r.Intent != guidedResourceInvestigation || r.ResFamily != resFamMemory {
			t.Errorf("ask çipi %q → %+v", c, r)
		}
	}
	if disambigAskRescuable(guidedResourceInvestigation) {
		t.Error("kaynak incelemesinin servis sorusu wiki kurtarmasına gitmemeli")
	}
	if !serviceScopedIntents[guidedResourceInvestigation] {
		t.Error("kapsam servisi boş rotaya yazılabilmeli")
	}
}

// Pod / cluster netleştirmesinin çipleri; takip çipleri pinli niyetlere.
func TestResourceFollowupsAndLinks(t *testing.T) {
	podAsk := guidedRoute{Intent: guidedResourceInvestigation, ResTarget: "orders-api-6d4f", ResKind: resKindPod, ResFamily: resFamCPU,
		ResPodOptions: []string{riPod, "orders-api-6d4f8b9c7d-b7m4p"}}
	for _, c := range guidedSuggestions(podAsk) {
		r := routeGuidedIntent(c, riServices, riEnvs, nil, "")
		if r.Intent != guidedResourceInvestigation || r.ResKind != resKindPod || r.ResFamily != resFamCPU {
			t.Errorf("pod çipi %q → %+v", c, r)
		}
	}
	if guidedFollowups(podAsk, nil) != nil {
		t.Error("netleştirme turunda takip adımı olmamalı")
	}
	clAsk := guidedRoute{Intent: guidedResourceInvestigation, Service: "orders-api", ResTarget: "orders-api", ResKind: resKindService, ResFamily: resFamAll,
		ResClusterOptions: []string{"kxzone", "kxzone-b"}}
	for i, c := range guidedSuggestions(clAsk) {
		r := routeGuidedIntent(c, riServices, riEnvs, nil, "")
		if r.Intent != guidedResourceInvestigation || r.ResCluster != clAsk.ResClusterOptions[i] || r.ResFamily != resFamAll {
			t.Errorf("cluster çipi %q → %+v", c, r)
		}
	}

	route := guidedRoute{Intent: guidedResourceInvestigation, Service: "orders-api", ResTarget: riPod, ResKind: resKindPod, ResFamily: resFamMemory,
		ResJVM: true, ResClusterID: "c-0001", ResNamespace: "shop", ResCluster: "kxzone", ResHref: "/metrics?editor=1"}
	fus := guidedFollowups(route, nil)
	pinned := map[string]guidedIntent{fuKindErrors: guidedLogErrors, fuKindDeploy: guidedDeployImpact, fuKindResJVM: guidedResourceInvestigation, fuKindResPods: guidedResourceInvestigation}
	if len(fus) != 4 {
		t.Fatalf("4 takip adımı bekleniyordu: %+v", fus)
	}
	for _, f := range fus {
		want, ok := pinned[f.Kind]
		if !ok || f.Label == "" || f.Href != "" {
			t.Errorf("takip %+v", f)
			continue
		}
		r := routeGuidedIntent(f.Text, riServices, riEnvs, nil, "")
		// v0.10.2239 (S4): pod kipli JVM çipinin servisi demette resolver'dan.
		if r.Intent != want || (r.Service != "orders-api" && !(f.Kind == fuKindResJVM && r.Service == "")) {
			t.Errorf("%q → %s/%q, want %s", f.Text, r.Intent, r.Service, want)
		}
		if f.Kind == fuKindResJVM && (f.Label != "GC/heap'e bak" || r.ResFamily != resFamJVM || r.ResKind != resKindPod || r.ResTarget != riPod) {
			t.Errorf("JVM çipi: %+v → %+v", f, r)
		}
		if f.Kind == fuKindResPods && (r.ResKind != resKindService || r.ResFamily != resFamMemory) {
			t.Errorf("diğer pod'lar: %+v", r)
		}
	}
	// JVM yoksa drill çipi yok.
	route.ResJVM = false
	for _, f := range guidedFollowups(route, nil) {
		if f.Kind == fuKindResJVM {
			t.Error("JVM metriği yokken GC/heap çipi sunuldu")
		}
	}
	links := guidedAnswerLinkTargets(route)
	var hrefs []string
	for _, l := range links {
		hrefs = append(hrefs, l.Href)
	}
	all := strings.Join(hrefs, " ")
	for _, w := range []string{"/metrics?editor=1", "/pod?cluster=c-0001&namespace=shop&pod=" + riPod + "&service=orders-api", "/service?name=orders-api&tab=infra"} {
		if !strings.Contains(all, w) {
			t.Errorf("link %q yok: %v", w, hrefs)
		}
	}
	route.ResHref = "//evil.example/x"
	for _, l := range guidedAnswerLinkTargets(route) {
		if strings.HasPrefix(l.Href, "//") {
			t.Errorf("protokol-göreli href geçti: %v", l)
		}
	}
	// BULUNAMADI: yalnız Infrastructure link çipi (mesaj göndermez).
	nf := guidedRoute{Intent: guidedResourceInvestigation, ResTarget: "nope", ResKind: resKindService, Service: "orders-api", ResNotFound: true}
	got := guidedFollowups(nf, nil)
	if len(got) != 1 || got[0].Href != "/service?name=orders-api&tab=infra" || got[0].Kind != fuKindResInfra {
		t.Errorf("bulunamadı takibi: %+v", got)
	}
}
