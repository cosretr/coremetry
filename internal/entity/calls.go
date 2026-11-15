package entity

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"
)

// calls.go — v0.10.2240 (Entity S5, yatay eksen; plan §2.1–2.2, "S2–S6
// ayarı"): database / queue / external entity'leri ve servis düzeyi `calls`
// kenarları KALICI katmanda (CH referans satırları, aç/kapat + günde bir
// tazeleme). Kaynak topology_edges_5m (servis düzeyi MV), GROUP BY
// (parent_service, child_node); operasyon düzeyi kenarlar saklanmaz. Ham
// spans okunmaz. Host entity'leri bu dilimde YOK (S6: şema gerektirir).
//
// Kimlik topoloji düğüm id'siyle birebir: `db:<…>`, `queue:<…>`, `ext:<…>`
// verbatim; öneksiz düğüm servis → `svc:<ad>`. topology_edges_5m cluster
// taşımadığından satırlar sahte cluster_id TopologyClusterID altında durur
// (UnmappedClusterID emsali); tick'in cluster geçişleri bu aralığa dokunmaz.

// TopologyClusterID — yatay eksen satırlarının cluster_id'si (Remote
// Cluster id'si olamaz: parantez; span-only cluster da olamaz, SpanOnlyClusters).
const TopologyClusterID = "(topology)"

// Sabitler (ayar yok, K1).
const (
	// CallsEvery — geçiş kadansı (tick 60 s; geçiş en çok 15 dk'da bir).
	CallsEvery = 15 * time.Minute
	// CallsWindow — topology_edges_5m okuma penceresi (12 kova).
	CallsWindow = time.Hour
	// CallsCloseAfter — kenar / bağımlılık art arda başarılı geçişlerde bu
	// kadar görülmezse kapanır (saatlik / gecelik işlerin kenarı çırpınmasın).
	CallsCloseAfter = 24 * time.Hour
	// MaxCallEdges — tek okumanın LIMIT'i; dolarsa hiçbir şey kapanmaz.
	MaxCallEdges = 20_000
	// MaxExternalEntities — `ext:` kardinalite tavanı (çağrı sayısına göre
	// en yoğunlar); fazlasının entity'si ve kenarı yazılmaz, sayı günlükte.
	MaxExternalEntities = 5_000
)

// CallEdge — topology_edges_5m'in (parent_service, child_node) toplamı.
type CallEdge struct {
	Parent, Child string
	Calls         uint64
}

// CallSource — yatay eksen okuması (chstore.EntityCallEdges; sınırlı:
// zaman penceresi, LIMIT, max_execution_time). truncated: LIMIT doldu.
type CallSource interface {
	RecentCalls(ctx context.Context, since time.Time, limit int) (edges []CallEdge, truncated bool, err error)
}

// CHCalls — chstore.EntityCallEdges (yapısal tipleme; chadapter emsali).
type CHCalls interface {
	EntityCallEdges(ctx context.Context, since time.Time, limit int) ([]CallEdge, bool, error)
}

type chCalls struct{ ch CHCalls }

// CallsFromCH — main.go: *chstore.Store → CallSource.
func CallsFromCH(ch CHCalls) CallSource { return chCalls{ch: ch} }

func (a chCalls) RecentCalls(ctx context.Context, since time.Time, limit int) ([]CallEdge, bool, error) {
	return a.ch.EntityCallEdges(ctx, since, limit)
}

// SetCallSource — main.go chstore adaptörünü bağlar (nil = geçiş yok).
func (s *Syncer) SetCallSource(c CallSource) { s.calls = c }

// topologyPrefixes — topoloji düğüm önekleri → entity tipi (chstore
// TopologyNodeIDPrefixes'in entity tarafı; "node:" RUNS_ON çocuğu, çağrı
// grafiği değil → tip yok).
var topologyPrefixes = []struct{ Prefix, Type string }{
	{"db:", TypeDatabase},
	{"queue:", TypeQueue},
	{"ext:", TypeExternal},
}

// TopologyEntityID — SAF: topoloji düğüm id'si → entity id ("" = entity
// değil). Önekli altyapı düğümü verbatim; "node:" ve boş → ""; öneksiz →
// svc:<ad>. Topoloji yanıtının `entityId` alanı ve syncer AYNI fonksiyonu
// kullanır (gidiş-dönüş testi).
func TopologyEntityID(node string) string {
	node = strings.TrimSpace(node)
	if node == "" || strings.HasPrefix(node, "node:") {
		return ""
	}
	for _, p := range topologyPrefixes {
		if strings.HasPrefix(node, p.Prefix) {
			if len(node) == len(p.Prefix) {
				return ""
			}
			return node
		}
	}
	return ServiceID(node)
}

// dependencySystem — SAF: db/queue düğüm adının sistem bileşeni
// ("postgresql@orders-db" → postgresql, "kafka:orders.created" → kafka).
func dependencySystem(typ, name string) string {
	if typ != TypeDatabase && typ != TypeQueue {
		return ""
	}
	if i := strings.IndexAny(name, "@:"); i > 0 {
		return name[:i]
	}
	return name
}

// CallSnapshot — tek geçişin kalıcı görüntüsü.
type CallSnapshot struct {
	Entities   map[string]Entity   // db/queue/ext (servis satırı yazılmaz: span geçişinin)
	Relations  map[string]Relation // relKey → calls kenarı
	DroppedExt int                 // MaxExternalEntities dışında kalan ext düğümü
}

// BuildCallSnapshot — SAF: kenarlar → entity + calls kenarı. Ebeveyn servis
// ya da kuyruk (tüketici kenarı queue→svc); çocuk servis/db/queue/ext.
// Kendine çağrı ve entity olmayan uç atlanır. ext tavanı çağrı sayısıyla.
func BuildCallSnapshot(edges []CallEdge, maxExt int) CallSnapshot {
	out := CallSnapshot{Entities: map[string]Entity{}, Relations: map[string]Relation{}}
	extCalls := map[string]uint64{}
	for _, e := range edges {
		for _, n := range []string{e.Parent, e.Child} {
			if id := TopologyEntityID(n); strings.HasPrefix(id, "ext:") {
				extCalls[id] += e.Calls
			}
		}
	}
	keepExt := map[string]bool{}
	ext := make([]string, 0, len(extCalls))
	for id := range extCalls {
		ext = append(ext, id)
	}
	sort.Slice(ext, func(i, j int) bool {
		if extCalls[ext[i]] != extCalls[ext[j]] {
			return extCalls[ext[i]] > extCalls[ext[j]]
		}
		return ext[i] < ext[j]
	})
	for i, id := range ext {
		if i < maxExt {
			keepExt[id] = true
		} else {
			out.DroppedExt++
		}
	}
	ensure := func(id string) bool {
		ref, ok := ParseID(id)
		if !ok {
			return false
		}
		switch ref.Type {
		case TypeService:
			return true
		case TypeExternal:
			if !keepExt[id] {
				return false
			}
		case TypeDatabase, TypeQueue:
		default:
			return false
		}
		if _, ok := out.Entities[id]; !ok {
			labels := map[string]string{}
			if sys := dependencySystem(ref.Type, ref.Name); sys != "" {
				labels["system"] = sys
			}
			out.Entities[id] = Entity{Type: ref.Type, ClusterID: TopologyClusterID, ID: id, Name: ref.Name, Labels: labels, Source: SourceTopology}
		}
		return true
	}
	for _, e := range edges {
		p, c := TopologyEntityID(e.Parent), TopologyEntityID(e.Child)
		if p == "" || c == "" || p == c {
			continue
		}
		if !strings.HasPrefix(p, "svc:") && !strings.HasPrefix(p, "queue:") {
			continue
		}
		if strings.HasPrefix(p, "queue:") && !strings.HasPrefix(c, "svc:") {
			continue
		}
		if !ensure(p) || !ensure(c) {
			continue
		}
		out.Relations[relKey(RelCalls, p, c)] = Relation{Type: RelCalls, ClusterID: TopologyClusterID, ParentID: p, ChildID: c, Source: SourceTopology}
	}
	return out
}

// syncCalls — yatay eksen geçişi (lider tick'inde, CallsEvery kadansı).
// Okuma hatasında hiçbir yazım/kapanış yok; LIMIT dolduysa kapanış yok
// (eksik görüntü yokluk değildir). Yazım: değişimde + günde bir tazeleme.
func (s *Syncer) syncCalls(ctx context.Context) (st clusterTickStats) {
	now := s.now()
	s.callsLast.Store(now.UnixNano())
	edges, truncated, err := s.calls.RecentCalls(ctx, now.Add(-CallsWindow), MaxCallEdges)
	if err != nil {
		s.recordPassFailure(ctx, TopologyClusterID, now, "calls", err)
		return
	}
	snap := BuildCallSnapshot(edges, MaxExternalEntities)
	open, err := s.store.OpenLifetimes(ctx, TopologyClusterID)
	if err != nil {
		s.recordPassFailure(ctx, TopologyClusterID, now, "calls", err)
		return
	}
	st, err = s.applyStable(ctx, TopologyClusterID, now, open, snap.Entities, snap.Relations, CallsCloseAfter, truncated)
	if err != nil {
		return st
	}
	st.read += len(edges)
	log.Printf("[entity] calls: edges=%d truncated=%v deps=%d relations=%d ext_dropped=%d written=%d/%d closed=%d",
		len(edges), truncated, len(snap.Entities), len(snap.Relations), snap.DroppedExt, st.ents, st.rels, st.closed)
	return st
}

// recordPassFailure — sahte cluster_id'li geçişin (calls / hosts / metrik)
// hatası: failed koşu satırı + günlük; hiçbir yazım / kapanış yok.
func (s *Syncer) recordPassFailure(ctx context.Context, cid string, started time.Time, pass string, err error) {
	run := Run{ClusterID: cid, StartedAt: started, Status: RunFailed, Error: err.Error(), FinishedAt: s.now()}
	if rerr := s.store.RecordRun(ctx, run); rerr != nil {
		log.Printf("[entity] %s sync_runs yazılamadı: %v", pass, rerr)
	}
	log.Printf("[entity] %s geçişi: %v", pass, err)
}

// applyStable — sahte cluster_id'li kalıcı aralığın ortak gövdesi (S5 calls,
// S6 hosts / metrik servisler): açık ömürlerle diff, değişimde + günde bir
// tazeleme (ThrottleRefresh), tam satır yazımı, koşu satırı. partial: eksik
// görüntü — yokluk sayılır ama kapanış yok. Yazım hatası → failed koşu.
func (s *Syncer) applyStable(ctx context.Context, cid string, now time.Time, open OpenSet, ents map[string]Entity, rels map[string]Relation, closeAfter time.Duration, partial bool) (st clusterTickStats, err error) {
	run := Run{ClusterID: cid, StartedAt: now}
	st.read, st.scanned = len(open.Lifetimes), open.RowsScanned
	seen := stableSeen(ents, rels)
	cs := s.clusterState(cid)
	s.mu.Lock()
	absent, lastOK := cs.absent, cs.lastOK
	s.mu.Unlock()
	if partial {
		closeAfter = 1<<63 - 1
	}
	ch, nextAbsent := DiffLifetimes(now, open.Lifetimes, seen, absent, lastOK, closeAfter)
	ch.Refresh, st.skipped = ThrottleRefresh(now, open.Lifetimes, ch.Refresh, RefreshEvery)
	s.skipN.Add(int64(st.skipped))
	rows := RowsForChange(cid, ch, ents, open.Entities)
	relRows := relationRowsForChange(cid, ch, rels, open.Relations)
	if len(rows)+len(relRows) > 0 {
		t1 := s.now()
		if err := s.store.Apply(ctx, cid, rows, relRows); err != nil {
			s.recordPassFailure(ctx, cid, now, cid, err)
			return st, err
		}
		run.CHMs = int(s.now().Sub(t1) / time.Millisecond)
	}
	for _, l := range ch.Close {
		if !isRelKey(l.ID) {
			run.Closed++
		}
	}
	run.Status, run.FinishedAt = RunOK, s.now()
	if partial {
		run.Status = RunPartial
	}
	run.EntitiesWritten, run.RelationsWritten = len(rows), len(relRows)
	s.mu.Lock()
	cs.absent, cs.lastOK, cs.lastErr = nextAbsent, now, ""
	s.mu.Unlock()
	s.entsN.Add(int64(len(rows)))
	s.relsN.Add(int64(len(relRows)))
	st.ok, st.ents, st.rels, st.closed = true, len(rows), len(relRows), run.Closed
	if err := s.store.RecordRun(ctx, run); err != nil {
		log.Printf("[entity] %s sync_runs yazılamadı: %v", cid, err)
	}
	return st, nil
}

// ── okuma (resolver) ────────────────────────────────────────────────

// MaxCallNeighbours — Callees/Callers cevap tavanı (fazlası Truncated).
const MaxCallNeighbours = 100

// CallNeighbours — calls kenarının bir yönü (entity id'leri, sıralı).
type CallNeighbours struct {
	IDs       []string
	Truncated bool
}

func (r *Resolver) callsDir(ctx context.Context, id string, byChild bool, at time.Time) (CallNeighbours, error) {
	var out CallNeighbours
	if r.rels == nil || id == "" {
		return out, nil
	}
	edges, err := r.rels.RelationsFor(ctx, RelCalls, []string{id}, byChild, at, MaxCallNeighbours+1)
	seen := map[string]bool{}
	for _, e := range edges {
		n := e.ChildID
		if byChild {
			n = e.ParentID
		}
		if !seen[n] {
			seen[n] = true
			out.IDs = append(out.IDs, n)
		}
	}
	sort.Strings(out.IDs)
	if len(out.IDs) > MaxCallNeighbours {
		out.IDs, out.Truncated = out.IDs[:MaxCallNeighbours], true
	}
	return out, err
}

// Callees — id'nin çağırdıkları (svc → svc/db/queue/ext; queue → tüketici
// servisler). Kalıcı katmandan, tek sınırlı okuma.
func (r *Resolver) Callees(ctx context.Context, id string, at time.Time) (CallNeighbours, error) {
	return r.callsDir(ctx, id, false, at)
}

// Callers — id'yi çağıranlar (db/queue/ext/svc ← servisler; svc ← queue).
func (r *Resolver) Callers(ctx context.Context, id string, at time.Time) (CallNeighbours, error) {
	return r.callsDir(ctx, id, true, at)
}
