package api

// entity_service_pods.go — v0.10.2239 (Entity S4; plan §4 "CoSRE / MCP"):
// CoSRE / MCP pod adaptörlerinin pod kümesi entity.Resolver'ın svc → pod
// komşuluğundan (canlı dizin, ıskada entity_seen_5m). Eski kural —
// thanos.ServicePodRegex: deployment | servis | ortam eksiz ad + "-.*" —
// adı deployment'tan farklı serviste pod bulamıyor, kardeş önekte ("api" /
// "api-gateway") başkasının pod'unu alıyordu; silindi (shim yok).
//
// Seçici cluster başına TAM AD listesi (thanos.PodNamesRegex: ≤200 ad, ≤4000
// karakter, fazlası Truncated ile ilan). Küme boşsa seçici "" ve ÇAĞIRAN
// Thanos'a gitmez: podMatcher("") cluster geneli demektir — "pod bilinmiyor"
// cevabı verilir.

import (
	"context"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// podSelector — çözülmüş pod kümesinin Thanos seçicileri.
type podSelector struct {
	ByCluster map[string]string // cid → ^(a|b)$
	Union     string            // tüm cluster'ların adları (tek seçicili yollar)
	Pods      int
	Truncated bool
	Source    string // live | mv | live+mv | ""
}

// Unknown — depoda bu hedefin pod'u yok.
func (p podSelector) Unknown() bool { return p.Pods == 0 }

// podSelectorFrom — SAF: pod kümesi → seçiciler.
func podSelectorFrom(set entity.ServicePodSet) podSelector {
	ps := podSelector{ByCluster: map[string]string{}, Pods: len(set.Pods), Truncated: set.Truncated, Source: set.Source}
	by := set.ByCluster()
	var all []string
	seen := map[string]bool{}
	for cid, names := range by {
		re, tr := thanos.PodNamesRegex(names)
		if re != "" {
			ps.ByCluster[cid] = re
		}
		ps.Truncated = ps.Truncated || tr
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				all = append(all, n)
			}
		}
	}
	sort.Strings(all)
	re, tr := thanos.PodNamesRegex(all)
	ps.Union, ps.Truncated = re, ps.Truncated || tr
	return ps
}

// entityClusterIDs — Thanos'u etkin cluster'ların entity cid'leri.
func (s *Server) entityClusterIDs() []string {
	var out []string
	for _, c := range s.thanosEnabledClusters() {
		out = append(out, c.EffectiveID())
	}
	return out
}

// servicePodSelector — servis → pod komşuları → seçiciler (at sıfır = şimdi).
func (s *Server) servicePodSelector(ctx context.Context, service string, at time.Time) podSelector {
	if service == "" || s.thanos == nil {
		return podSelector{}
	}
	set, _ := s.entityResolver().ServicePods(ctx, service, s.entityClusterIDs(), at)
	return podSelectorFrom(set)
}

// workloadPodSelector — metin sorusundaki iş yükü adı → pod'ları (tam
// kanonik ad; önek yok).
func (s *Server) workloadPodSelector(ctx context.Context, name string, at time.Time) podSelector {
	if name == "" || s.thanos == nil {
		return podSelector{}
	}
	set, _ := s.entityResolver().WorkloadPodsNamed(ctx, name, s.entityClusterIDs(), at)
	return podSelectorFrom(set)
}

// podOwnerService — pod adının sahibi servis (resolver yolu; birden çok
// cluster'da farklı sahip → "", belirsizlikte kenar yok).
func (s *Server) podOwnerService(ctx context.Context, pod string, at time.Time) string {
	if pod == "" || s.thanos == nil {
		return ""
	}
	pods, _ := s.entityResolver().PodsNamed(ctx, pod, s.entityClusterIDs(), at)
	owner := ""
	for _, p := range pods {
		if len(p.Services) == 0 {
			continue
		}
		if owner != "" && owner != p.Services[0] {
			return ""
		}
		owner = p.Services[0]
	}
	return owner
}

// podsUnknownTR — depo boşken dürüst cümle (Thanos'a gidilmedi).
func podsUnknownTR(target string) string {
	return "entity deposunda " + target + " için pod bilinmiyor (span ya da Kubernetes eşlemesi yok) — Thanos sorgulanmadı"
}
