package api

// mcp_trace_value_dims.go — v0.10.2465 (operator-reported, prod 2026-11-22):
// traces_search değer çözümünün aday listeleri. Picker'ların okumalarıyla
// aynı, ucuz ve sınırlı: span cluster değerleri (ListClusters — son 1 sa,
// LC kolon, ≤200), değeri içeren deploy_env'ler (ListEnvironments q=değer —
// LC kolon, arama 24 sa'e genişler, ≤20), görülen namespace'ler
// (NamespacesSeen — entity_seen MV, 24 sa). Ham öznitelik taraması yok.
// Bileşik okuma Deps.Memo ile 60 sn; anahtar değeri FNV ile taşır (cluster /
// namespace listeleri değerden bağımsız, ayrı anahtarda).

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

const traceValueDimsTTL = 60 * time.Second

func (s *Server) mcpTraceValueDims(ctx context.Context, value string) (mcptools.TraceValueDims, error) {
	var out mcptools.TraceValueDims
	if s.store == nil {
		return out, fmt.Errorf("telemetry store not configured")
	}
	value = strings.TrimSpace(value)
	type fleet struct {
		Clusters   []string `json:"c"`
		Namespaces []string `json:"n"`
	}
	body, err := s.mcpMemo(ctx, "mcp:trace_value_dims:fleet:v1", traceValueDimsTTL, func(c context.Context) (any, bool, error) {
		now := time.Now()
		var f fleet
		cl, cerr := s.store.ListClusters(c, now.Add(-time.Hour), now)
		ns, nerr := s.store.NamespacesSeen(c, now.Add(-24*time.Hour), now)
		f.Clusters, f.Namespaces = cl, ns
		return f, cerr == nil && nerr == nil, nil
	})
	var f fleet
	if err == nil {
		_ = json.Unmarshal(body, &f)
	}
	out.Clusters, out.Namespaces = f.Clusters, f.Namespaces
	if value != "" {
		h := fnv.New64a()
		_, _ = h.Write([]byte(strings.ToLower(value)))
		key := fmt.Sprintf("mcp:trace_value_dims:env:v1:%x", h.Sum64())
		eb, eerr := s.mcpMemo(ctx, key, traceValueDimsTTL, func(c context.Context) (any, bool, error) {
			now := time.Now()
			envs, _, lerr := s.store.ListEnvironments(c, now.Add(-24*time.Hour), now, value, 20)
			return envs, lerr == nil, lerr
		})
		if eerr == nil {
			_ = json.Unmarshal(eb, &out.Envs)
		} else if err == nil {
			err = eerr
		}
	}
	return out, err
}
