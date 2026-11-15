package api

// mcp_trace_pods.go — v0.10.1275 (CoSRE adım 6): pivot_from_trace'in pod
// halkasının (H1) Thanos adaptörü.
//
// Kural GET /api/trace-pods/metrics ile TEK: span cluster değeri sunucuda
// eşlenir (resolveSpanCluster — SpanClusterOwner + Enabled), eşlenmeyen değer
// Thanos'a gitmez; okuma aynı önbellek anahtarına (tracePodMetricsKey) aynı
// dolum fonksiyonuyla (computeTracePodMetrics) yazar — Trace › Metrics sekmesi
// ile sohbet pivotu birbirinin önbelleğini ısıtır. Pencere 6 sa'e kırpılır
// (tracePodMaxWindow), seçici tavanı aşılırsa Thanos'a gidilmez.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// mcpTracePodMDP — pivot pod grafiğinin nokta hedefi (basamağa yuvarlanır).
const mcpTracePodMDP = 60

type mcpTracePods struct{ s *Server }

// mcpTracePodsOrNil — Thanos yoksa nil (halka dürüst skipped).
func (s *Server) mcpTracePodsOrNil() mcptools.TracePodReader {
	if s.thanos == nil {
		return nil
	}
	return mcpTracePods{s}
}

func (m mcpTracePods) TracePods(ctx context.Context, cv string, refs []thanos.TracePodRef, from, to time.Time) (mcptools.TracePodHop, error) {
	sc := m.s.resolveSpanCluster(cv)
	c, thanosOn, reason := sc.Cfg, sc.ThanosOn, sc.Reason
	hop := mcptools.TracePodHop{Thanos: thanosOn, ClusterValue: cv, UnmappedReason: reason}
	if !thanosOn || reason != "" {
		return hop, nil
	}
	if len(refs) == 0 {
		return hop, fmt.Errorf("en az bir pod gerekli")
	}
	if len(refs) > thanos.TracePodChunkMax {
		refs = refs[:thanos.TracePodChunkMax]
	}
	if _, truncated := thanos.PodNamesRegex(thanos.TracePodNames(refs)); truncated {
		return hop, fmt.Errorf("pod listesi seçici tavanını (%d karakter) aşıyor", thanos.TracePodRegexMax)
	}
	if to.Sub(from) > tracePodMaxWindow {
		from = to.Add(-tracePodMaxWindow)
	}
	mdp := thanos.TrendMaxDataPointsRung(mcpTracePodMDP)
	now := time.Now()
	key := tracePodMetricsKey(c.EffectiveID(), tracePodCfgDigest(c), refs, from, to, mdp, tracePodWindowClosed(to, now))
	body, err := m.s.mcpMemo(ctx, key, tracePodMetricsTTL(to, now), func(ctx context.Context) (any, bool, error) {
		v, err := m.s.computeTracePodMetrics(ctx, c, cv, refs, from, to, mdp)
		return v, err == nil, err
	})
	if err != nil {
		return hop, err
	}
	var resp tracePodMetricsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return hop, fmt.Errorf("trace pod gövdesi çözülemedi: %w", err)
	}
	hop.Mapped = true
	hop.ClusterID, hop.ClusterName = c.EffectiveID(), c.Name
	if resp.Cluster != nil {
		hop.ClusterID, hop.ClusterName = resp.Cluster.ID, resp.Cluster.Name
	}
	hop.Query = thanos.TracePodQueryText(refs, c.NamespaceFilter)
	hop.Start, hop.Step, hop.Points, hop.Instant, hop.Pods = resp.Start, resp.Step, resp.Points, resp.Instant, resp.Pods
	return hop, nil
}

// mcpMemo — v0.10.1275: Deps.Memo (bileşik araç önbelleği) = cachedJSON.
// fn'in bool'u false → uncacheable (gövde döner, hiçbir katmana yazılmaz).
func (s *Server) mcpMemo(ctx context.Context, key string, ttl time.Duration, fn func(context.Context) (any, bool, error)) ([]byte, error) {
	if s.l1 == nil || s.cache == nil {
		// Önbelleksiz kurulum (test sunucusu): doğrudan hesapla.
		v, _, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}
	body, _, err := s.cachedJSON(ctx, key, ttl, false, func(c context.Context) (any, error) {
		v, store, err := fn(c)
		if err != nil {
			return nil, err
		}
		if !store {
			return uncacheable(v), nil
		}
		return v, nil
	})
	return body, err
}
