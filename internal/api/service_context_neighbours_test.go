package api

// service_context_neighbours_test.go — v0.10.1410 regresyon testi.
//
// Operatör: "Downstream düzgün çalışmıyor". v0.10.1408 Bağlam panelini
// topoloji özetine (topology_edges_5m) taşıdı; CoSRE'nin LLM bağlamları —
// servis bağlamı (buildServiceContext → renderServiceSnapshot: guided sohbet,
// servis raporu) ve problem/anomali açıklamasının kanıtı
// (serviceCorrelationContext) — hâlâ chstore.ServiceNeighbors'tan (ham spans,
// en büyük 50 trace) okuyordu: downstream yalnız BAŞKA service_name'li çocuk
// span'lardan kuruluyordu, servisin kendi DB / dış HTTP / messaging CLIENT
// span'ları hiç görünmüyordu. Fixture: enstrümante callee'si SIFIR, ama db +
// dış HTTP (+ kuyruk) kenarı olan bir servis → kanıt downstream'de hepsini,
// Topology sekmesinin etiketi ve kenar sayılarıyla listelemeli.
//
// Sentetik adlar: orders-api, edge-gateway, db.example.test.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// orders-api: tek çağıran edge-gateway; callee'leri yalnız DB, dış HTTP ve
// kuyruk (enstrümante servis yok).
func ctxNeighbourEdges() []chstore.ServiceTopologyEdge {
	return []chstore.ServiceTopologyEdge{
		{ParentService: "edge-gateway", ChildNode: "orders-api", NodeKind: "service", Protocol: "http", Calls: 400},
		{ParentService: "orders-api", ChildNode: "db:oracle@db.example.test", NodeKind: "db", Protocol: "db", Calls: 938, Errors: 19, ErrorRate: 19.0 / 938 * 100, AvgMs: 4},
		{ParentService: "orders-api", ChildNode: "ext:payments.example.test", NodeKind: "external", Protocol: "http", Calls: 120},
		{ParentService: "orders-api", ChildNode: "queue:kafka:orders.created", NodeKind: "queue", Protocol: "kafka", Calls: 50},
	}
}

func TestServiceContextNeighboursFromRollup(t *testing.T) {
	many := []chstore.ServiceTopologyEdge{}
	for i := 0; i < serviceContextNeighbourCap+3; i++ {
		many = append(many, chstore.ServiceTopologyEdge{ParentService: "orders-api",
			ChildNode: "ext:h" + string(rune('a'+i)) + ".example.test", NodeKind: "external", Protocol: "http", Calls: uint64(100 - i)})
	}
	cases := []struct {
		name     string
		edges    []chstore.ServiceTopologyEdge
		wantDown []string // renderServiceSnapshot "Downstream" satırında, bu sırayla
		wantUp   []string
		noDown   bool
	}{
		{
			name:  "0 instrumented callees, db + external HTTP + queue → all downstream with Topology labels and edge counts",
			edges: ctxNeighbourEdges(),
			wantDown: []string{
				"oracle@db.example.test [database] (938 çağrı, %2.0 hata)",
				"payments.example.test [external] (120 çağrı)",
				"kafka:orders.created [queue] (50 çağrı)",
			},
			wantUp: []string{"edge-gateway (400 çağrı)"},
		},
		{
			name:     "hub: cap per side, rest counted not dropped",
			edges:    many,
			wantDown: []string{"ha.example.test [external] (100 çağrı)", "(+3 daha)"},
		},
		{
			name:   "no edges → no neighbour lines",
			noDown: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up, down := serviceNeighborsFromEdges(c.edges, "orders-api", nil)
			cx := &aiServiceContext{Service: "orders-api", RangeS: 1800, Current: aiRED{Spans: 10},
				Upstream: []string{}, Downstream: []string{}}
			setServiceContextNeighbours(cx, up, down)
			if len(cx.Downstream) > serviceContextNeighbourCap || len(cx.Upstream) > serviceContextNeighbourCap {
				t.Fatalf("cap aşıldı: up=%d down=%d", len(cx.Upstream), len(cx.Downstream))
			}
			got := renderServiceSnapshot(cx)
			line := func(prefix string) string {
				for _, l := range strings.Split(got, "\n") {
					if strings.HasPrefix(l, prefix) {
						return l
					}
				}
				return ""
			}
			dl := line("Downstream (bağımlılıklar): ")
			if c.noDown {
				if dl != "" || line("Upstream (çağıranlar): ") != "" {
					t.Fatalf("kenar yokken komşu satırı:\n%s", got)
				}
				return
			}
			assertInOrder(t, dl, c.wantDown)
			if len(c.wantUp) > 0 {
				assertInOrder(t, line("Upstream (çağıranlar): "), c.wantUp)
			}
		})
	}
}

// assertInOrder — her parça satırda, verilen sırayla.
func assertInOrder(t *testing.T, line string, parts []string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(line[at:], p)
		if i < 0 {
			t.Fatalf("%q yok (ya da sıra yanlış) — satır: %q", p, line)
		}
		at += i + len(p)
	}
}

// ncStore — problem/anomali kanıtı için sahte okuma yüzeyi: xrStore'un
// geri kalanı, kenarlar tablodan; okunan pencere kaydedilir.
type ncStore struct {
	xrStore
	edges    []chstore.ServiceTopologyEdge
	err      error
	from, to *time.Time
}

func (n ncStore) ReadServiceTopologyAggForFocus(_ context.Context, from, to time.Time, focus string, hops, limit int) ([]chstore.ServiceTopologyEdge, error) {
	*n.from, *n.to = from, to
	if focus != "orders-api" || hops != 1 || limit <= 0 {
		return nil, nil
	}
	return n.edges, n.err
}

func TestProblemExplainEvidenceListsDBAndExternalCallees(t *testing.T) {
	anchor := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		edges   []chstore.ServiceTopologyEdge
		err     error
		callees []string // "callees:" satırında, bu sırayla
		callers []string
		absent  []string
		noBlock bool
	}{
		{
			name:    "0 instrumented callees, db + external HTTP → callees lists both with edge calls + error rate",
			edges:   ctxNeighbourEdges(),
			callees: []string{"oracle@db.example.test [database] (938 calls, 2.0% err)", "payments.example.test [external] (120 calls)", "kafka:orders.created [queue] (50 calls)"},
			callers: []string{"edge-gateway (400 calls)"},
		},
		{
			name: "Topology hidden patterns apply (store'suz yolda kod varsayılanı)",
			edges: append(ctxNeighbourEdges(), chstore.ServiceTopologyEdge{
				ParentService: "orders-api", ChildNode: "queue:kafka:noise.app", NodeKind: "queue", Protocol: "kafka", Calls: 5000}),
			callees: []string{"oracle@db.example.test [database]"},
			absent:  []string{"kafka:noise.app"},
		},
		{
			name:    "read error → block omitted (best-effort), explain still built",
			err:     context.DeadlineExceeded,
			noBlock: true,
		},
		{
			name:    "no edges → block omitted",
			noBlock: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var from, to time.Time
			explainStoreSeam = ncStore{edges: c.edges, err: c.err, from: &from, to: &to}
			t.Cleanup(func() { explainStoreSeam = nil })
			// v0.10.2268 — kod varsayılanı boş; gizleme yolunu nötr bir desenle sür.
			prevHidden := defaultTopologyHiddenPatterns
			defaultTopologyHiddenPatterns = []string{"kafka:noise*"}
			t.Cleanup(func() { defaultTopologyHiddenPatterns = prevHidden })
			s := &Server{}
			got := s.serviceCorrelationContext(context.Background(), "orders-api", anchor)
			if !from.Equal(anchor.Add(-30*time.Minute)) || !to.Equal(anchor.Add(5*time.Minute)) {
				t.Errorf("komşu penceresi [anchor-30m, anchor+5m] olmalı, okunan [%v, %v]", from, to)
			}
			if c.noBlock {
				if strings.Contains(got, "Topology neighbours") {
					t.Fatalf("blok beklenmiyordu:\n%s", got)
				}
				return
			}
			line := func(prefix string) string {
				for _, l := range strings.Split(got, "\n") {
					if strings.HasPrefix(strings.TrimSpace(l), prefix) {
						return l
					}
				}
				return ""
			}
			assertInOrder(t, line("callees: "), c.callees)
			if len(c.callers) > 0 {
				assertInOrder(t, line("callers: "), c.callers)
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("%q gizli olmalıydı:\n%s", a, got)
				}
			}
		})
	}
}
