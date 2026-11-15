package entity

import (
	"context"
	"sort"
	"time"
)

// service_pods.go — v0.10.2239 (Entity S4; plan §4 "CoSRE / MCP" satırı ve
// "S2–S6 ayarı"): svc → pod komşuluğu. CoSRE araçlarının pod kümesi artık ad
// önekinden (eski thanos.ServicePodRegex: deployment | servis | ortam eksiz ad
// + "-.*") DEĞİL, buradan gelir:
//
//   - "şimdi": lider tick'inin canlı dizini (cluster başına Redis blob'u) —
//     pod'un span'lerinde servis var YA DA pod'un iş yükü servise `serves`
//     kenarıyla bağlı (span basmayan CrashLoop pod'u da gelir);
//   - ıska / Redis kesintisi / geçmiş an: entity_seen_5m'nin servis öneki
//     ([at−SeenWindow, at], LIMIT 500) — tek okuma, ıskalı cluster'lar birlikte.
//
// Depo boşsa küme boş döner; çağıran "pod bilinmiyor" der ve Thanos'a
// cluster-geneli seçiciyle GİTMEZ (shim yok).

// MaxServicePods — tek servis için dönen pod tavanı (Thanos seçicisi zaten
// 200 ad / 4000 karakterde keser; fazlası Truncated ile ilan edilir).
const MaxServicePods = 500

// ServicePodSet — servisin pod'ları ve kaynağı.
type ServicePodSet struct {
	Pods      []ResolvedPod
	Truncated bool
	// Source — "live" (canlı dizin), "mv" (entity_seen_5m), "live+mv", "" (yok).
	Source string
}

// ByCluster — cluster id → pod adları (sıralı, tekil).
func (s ServicePodSet) ByCluster() map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, p := range s.Pods {
		k := p.ClusterID + "/" + p.Name
		if seen[k] {
			continue
		}
		seen[k] = true
		out[p.ClusterID] = append(out[p.ClusterID], p.Name)
	}
	for _, v := range out {
		sort.Strings(v)
	}
	return out
}

func hasService(names []string, svc string) bool {
	for _, n := range names {
		if n == svc {
			return true
		}
	}
	return false
}

// ServicePods — svc → pod komşuları; cids bakılacak cluster id'leri.
func (r *Resolver) ServicePods(ctx context.Context, service string, cids []string, at time.Time) (ServicePodSet, error) {
	var set ServicePodSet
	if service == "" || len(cids) == 0 {
		return set, nil
	}
	wls := map[string]bool{}
	ids, firstErr := r.ServiceWorkloads(ctx, service, at, 200)
	for _, id := range ids {
		wls[CanonicalID(id)] = true
	}
	var missValues []string
	srcLive, srcMV := false, false
	for _, cid := range cids {
		if v, ok := r.view(ctx, cid, at); ok {
			srcLive = true
			for _, p := range v.Pods {
				pi := liveResolvedPod(cid, v.Gen, p)
				if hasService(pi.Services, service) || (pi.Workload != "" && wls[CanonicalID(pi.Workload)]) {
					set.Pods = append(set.Pods, pi)
				}
			}
			continue
		}
		if r.clusters != nil {
			missValues = append(missValues, r.clusters.Values(cid)...)
		}
	}
	if len(missValues) > 0 && r.src != nil {
		to := at
		if to.IsZero() {
			to = r.now()
		}
		rows, err := r.src.SeenForService(ctx, service, missValues, to.Add(-SeenWindow), to)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		byCID := map[string][]SeenEdge{}
		for _, e := range rows {
			if cid, ok := r.clusters.ID(e.Cluster); ok {
				byCID[cid] = append(byCID[cid], e)
			}
		}
		for cid, es := range byCID {
			for _, pi := range seenResolvedPods(cid, es) {
				set.Pods = append(set.Pods, *pi)
				srcMV = true
			}
		}
	}
	switch {
	case srcLive && srcMV:
		set.Source = "live+mv"
	case srcLive && len(set.Pods) > 0:
		set.Source = "live"
	case srcMV:
		set.Source = "mv"
	}
	sort.Slice(set.Pods, func(i, j int) bool {
		a, b := set.Pods[i], set.Pods[j]
		if a.ClusterID != b.ClusterID {
			return a.ClusterID < b.ClusterID
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	if len(set.Pods) > MaxServicePods {
		set.Pods, set.Truncated = set.Pods[:MaxServicePods], true
	}
	return set, firstErr
}

// WorkloadPodsNamed — iş yükü ADIYLA (tür/namespace bilinmeden; metin
// sorusundan gelen ad) pod'lar: canlı dizinde pod'un iş yükü kanonik adı
// eşit olanlar. Önek eşleşmesi yok ("api" "api-gateway"in pod'larını almaz).
// Canlı dizin ıskasında MV pod listesi (wlkind.FromPod türetmesi).
func (r *Resolver) WorkloadPodsNamed(ctx context.Context, name string, cids []string, at time.Time) (ServicePodSet, error) {
	var set ServicePodSet
	if name == "" {
		return set, nil
	}
	match := func(p ResolvedPod) bool {
		ref, ok := ParseID(CanonicalID(p.Workload))
		return ok && ref.Name == name
	}
	var firstErr error
	for _, cid := range cids {
		pods, err := r.Pods(ctx, cid, PodQuery{}, at)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, p := range pods {
			if match(p) {
				set.Pods = append(set.Pods, p)
				if p.Live {
					set.Source = "live"
				} else if set.Source == "" {
					set.Source = "mv"
				}
			}
		}
	}
	if len(set.Pods) > MaxServicePods {
		set.Pods, set.Truncated = set.Pods[:MaxServicePods], true
	}
	return set, firstErr
}

// PodsNamed — tam pod adı (namespace bilinmeden) → cluster'lardaki pod'lar
// (yol: iş yükü + servisler). Metin sorusundaki pod jetonunun sahibi için.
func (r *Resolver) PodsNamed(ctx context.Context, pod string, cids []string, at time.Time) ([]ResolvedPod, error) {
	var out []ResolvedPod
	if pod == "" {
		return out, nil
	}
	var firstErr error
	for _, cid := range cids {
		if v, ok := r.view(ctx, cid, at); ok {
			for _, p := range v.Pods {
				if p.Name == pod {
					out = append(out, liveResolvedPod(cid, v.Gen, p))
				}
			}
			continue
		}
		if r.src == nil || r.clusters == nil {
			continue
		}
		values := r.clusters.Values(cid)
		if len(values) == 0 {
			continue
		}
		to := at
		if to.IsZero() {
			to = r.now()
		}
		rows, err := r.src.SeenForPods(ctx, values, "", []string{pod}, to.Add(-SeenWindow), to)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, pi := range seenResolvedPods(cid, rows) {
			out = append(out, *pi)
		}
	}
	return out, firstErr
}
