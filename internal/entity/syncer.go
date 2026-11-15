package entity

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// syncer.go — THANOS SENKRONİZASYONU (design §3).
//
// Her tick: Clusters() (Remote Cluster listesi — her tick okunur, yeni
// cluster restart'sız gelir, kaldırılan cluster sync edilmez ama kayıtları
// kalır) → cluster başına bounded paralel `syncCluster` → altı seçicili
// anlık sorgu (SnapshotQueries; matcher doQuery'de) → Normalize + span
// geçişi → (1) canlı dizin blob'u Redis'e (live.go; pod/container ve pod
// uçlu kenarlar YALNIZ burada) → (2) KALICI katman (cluster, node,
// namespace, workload, service + parent kenarları) açık ömürlerle Diff →
// değişimde + günde bir tam-satır INSERT → sync_runs satırı
// (v0.10.2225, entity-storage-cost-note C + R1b). Bir cluster'ın hatası
// diğerlerini etkilemez; hatalı cluster'da HİÇBİR ömür kapanmaz (yokluk
// bilinmiyor). Silme yok.
//
// Lider kilidi ve ticker'ı çağıran (main.go) kurar: cache.LeaderHolder
// key "entity-sync"; Tick, IsLeader() true iken çağrılır. Redis kilidi
// degraded iken (Noop kilit: her replika lider) tick HİÇ koşmaz.

// ClusterRef — Remote Cluster kaydının syncer'a gereken kısmı.
type ClusterRef struct {
	ID               string
	Name             string
	NamespaceFilter  string // thanos.nsMatcher çıktısı DEĞİL, ham regex
	SpanClusterValue string // span `cluster` kolonundaki birincil değer (boş = Name)
	// SpanClusterValues — v0.10.139: tüm değerler (teklik Settings'te).
	SpanClusterValues []string
	// SpanOnly — v0.10.2214 (K2): Remote Cluster kaydı olmayan span cluster
	// değeri; ID = değer, Thanos sorgusu yok, yalnız span geçişi. Kimlik
	// bilgisi taşıyan Remote Cluster kaydı ASLA otomatik yaratılmaz.
	SpanOnly bool
}

// Source — Thanos kapısı (thanos.Service adaptörü; testte sahte).
type Source interface {
	Clusters() []ClusterRef
	Fetch(ctx context.Context, c ClusterRef, queries map[string]string) (SampleSets, error)
}

// EntityRow / RelationRow — chstore'a giden tam satırlar.
type EntityRow struct {
	Type, ClusterID, ID, Namespace, Name, UID, ParentID string
	ValidFrom, ValidTo, FirstSeen, LastSeen             time.Time
	LabelKeys, LabelValues                              []string
	Source                                              string
	Stale                                               bool
}

type RelationRow struct {
	Type, ClusterID, ParentID, ChildID string
	ValidFrom, ValidTo, LastSeen       time.Time
	Source                             string
}

// Run — entity_sync_runs satırı.
type Run struct {
	ClusterID, Status string
	StartedAt         time.Time
	FinishedAt        time.Time
	EntitiesWritten   int
	RelationsWritten  int
	Closed            int
	UnmappedKeys      []string
	UnmappedCounts    []uint32
	ThanosMs, CHMs    int
	Error             string
}

const (
	RunOK      = "ok"
	RunPartial = "partial"
	RunFailed  = "failed"
	RunSkipped = "skipped"
)

// OpenSet — kalıcı katmanın açık ömürleri + tam alanları. Invariant #4:
// tazeleme ve kapanış satırları alanlarını CH'den taşır (bellekten değil;
// lider devrinde yeni lider aynı satırı yazar).
type OpenSet struct {
	Lifetimes map[string]Lifetime
	Entities  map[string]Entity   // varlık ömürlerinin alanları
	Relations map[string]Relation // ilişki ömürlerinin alanları (anahtar relKey)
	// RowsScanned — CH'nin gerçekten taradığı satır (sorgu ilerlemesi);
	// döndürülen satır sayısı değil.
	RowsScanned int64
}

// Store — CH kapısı (chstore adaptörü; testte bellek).
type Store interface {
	OpenLifetimes(ctx context.Context, cid string) (OpenSet, error)
	Apply(ctx context.Context, cid string, rows []EntityRow, rels []RelationRow) error
	RecordRun(ctx context.Context, run Run) error
}

// StableTypes — CH'de satır olarak duran kalıcı katman (E1). Pod ve
// container yalnız canlı dizinde + entity_seen_5m'de. v0.10.2240 (S5):
// database / queue / external (topology_edges_5m; calls.go).
var StableTypes = []string{TypeCluster, TypeNode, TypeNamespace, TypeWorkload, TypeService, TypeDatabase, TypeQueue, TypeExternal}

// IsStableType — tip kalıcı katmanda mı.
func IsStableType(t string) bool {
	for _, x := range StableTypes {
		if x == t {
			return true
		}
	}
	return false
}

// IsStableRelation — iki ucu da kalıcı olan parent kenarı (cluster→node,
// cluster→ns, ns→wl), S2 köprüleri (serves, same_as) ve S5 calls. runs /
// runs_on / wl→pod / pod→ctr dizinde.
func IsStableRelation(rl Relation) bool {
	if rl.Type != RelParent && rl.Type != RelServes && rl.Type != RelSameAs && rl.Type != RelCalls {
		return false
	}
	p, ok1 := ParseID(rl.ParentID)
	c, ok2 := ParseID(rl.ChildID)
	return ok1 && ok2 && IsStableType(p.Type) && IsStableType(c.Type)
}

// Observability — /api/admin/entities/sync + SystemStats.
type Observability struct {
	Ticks            int64
	ClustersOK       int64
	ClustersFailed   int64
	EntitiesWritten  int64
	RelationsWritten int64
	LastTickMs       int64
	LastTickAt       time.Time
	// v0.10.2214 (S1) — yazım diyeti öz-ölçümü: okunan satır (entity_seen +
	// açık ömürler) ve kısılan (yazılmayan) tazeleme sayısı, kümülatif.
	RowsRead       int64
	RefreshSkipped int64
}

type clusterState struct {
	absent  map[string]Absence // art arda başarılı görüntülerde süren yokluklar (DiffLifetimes)
	lastOK  time.Time
	lastErr string
	merged  map[string]bool // S2: süreçte birleştirilmiş span-only değerler (mergeSpanOnly)
}

// Syncer — durumlu işçi.
type Syncer struct {
	src      Source
	store    Store
	seen     SeenReader // nil = span geçişi yok
	settings func() Resolved
	now      func() time.Time

	mu    sync.Mutex
	state map[string]*clusterState

	ticks, okN, failN, entsN, relsN, lastMs atomic.Int64
	lastAt                                  atomic.Int64
	readN, skipN, noLockN                   atomic.Int64
	// v0.10.141 — TryTick: aynı pod'da örtüşen tick yok (inFlight) ve yalnız
	// lider (leader nil = kontrol yok, tek-binary kipi).
	inFlight atomic.Bool
	leader   func() bool
	// v0.10.2225 — canlı dizin yazıcısı (cache.Cache; nil = dizin yok) ve
	// kilit degraded sorgusu (cache.DegradedFlag.Load; nil = hep sağlam).
	live         LiveStore
	lockDegraded func() bool
	// v0.10.2240 (S5) — yatay eksen kaynağı (nil = geçiş yok) ve son
	// geçişin anı (UnixNano; CallsEvery kadansı, calls.go).
	calls     CallSource
	callsLast atomic.Int64
}

// SetLive — canlı dizinin Redis kapısı (main.go cacheImpl).
func (s *Syncer) SetLive(kv LiveStore) { s.live = kv }

// SetLockDegraded — Redis kilidi Noop'a düştüğünde (her replika lider olur)
// tick yazmaz; atlanır ve günlüğe düşer.
func (s *Syncer) SetLockDegraded(f func() bool) { s.lockDegraded = f }

// SetLeaderCheck — main.go LeaderHolder.IsLeader'ı bağlar.
func (s *Syncer) SetLeaderCheck(f func() bool) { s.leader = f }

// TryTick — API tarafından tetiklenen anlık tick: lider değilse koşmaz
// (inceleme: lider olmayan pod'un tick'i liderin tick'iyle yarışıp aynı
// ömrü iki kez açıyordu). Örtüşme koruması Tick'in kendisinde — periyodik
// Run, SetOnAcquire ve "Run now" ucu da aynı kapıdan geçer.
func (s *Syncer) TryTick(ctx context.Context) (ran bool, reason string) {
	if s.leader != nil && !s.leader() {
		return false, "not-leader"
	}
	if !s.Tick(ctx) {
		return false, "in-flight"
	}
	return true, ""
}

func NewSyncer(src Source, store Store, settings func() Resolved) *Syncer {
	return &Syncer{src: src, store: store, settings: settings, now: time.Now, state: map[string]*clusterState{}}
}

// SetSeenReader — span-türevli geçiş kaynağı (chstore.EntitySeenRecent).
func (s *Syncer) SetSeenReader(r SeenReader) { s.seen = r }

func (s *Syncer) Observability() Observability {
	return Observability{
		Ticks: s.ticks.Load(), ClustersOK: s.okN.Load(), ClustersFailed: s.failN.Load(),
		EntitiesWritten: s.entsN.Load(), RelationsWritten: s.relsN.Load(), LastTickMs: s.lastMs.Load(),
		LastTickAt: time.Unix(0, s.lastAt.Load()), RowsRead: s.readN.Load(), RefreshSkipped: s.skipN.Load(),
	}
}

// Run — lider olduğu sürece periyodik tick (çağıran IsLeader ile sarar).
func (s *Syncer) Run(ctx context.Context, isLeader func() bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(SyncInterval):
		}
		if isLeader != nil && !isLeader() {
			continue
		}
		s.Tick(ctx)
	}
}

// Tick — bir senkronizasyon turu; aynı pod'da örtüşen tur koşmaz (false
// döner). Tüm çağıranlar (Run, SetOnAcquire, Run now, TryTick) bu kapıdan
// geçer — inceleme: kapı yalnız TryTick'teyken API tick'i periyodik tick'le
// çakışıp aynı ömrü iki kez açabiliyordu.
func (s *Syncer) Tick(ctx context.Context) bool {
	if !s.inFlight.CompareAndSwap(false, true) {
		log.Printf("[entity] tick atlandı: bir tur zaten koşuyor")
		return false
	}
	defer s.inFlight.Store(false)
	s.tick(ctx)
	return true
}

func (s *Syncer) tick(ctx context.Context) {
	// Degraded kilit: Noop kilitte her replika "lider"; aynı tablolara N
	// yazıcı vurmasın (entity-storage-cost-note §4 A/3). Tick atlanır.
	if s.lockDegraded != nil && s.lockDegraded() {
		log.Printf("[entity] tick: skipped_no_lock=%d reason=redis-lock-degraded (yazım yok)", s.noLockN.Add(1))
		return
	}
	r := s.settings()
	start := s.now()
	clusters := s.src.Clusters()
	var tickRead, tickScan, tickEnts, tickRels, tickSkip, tickClosed, tickOK, tickFail atomic.Int64
	var snapMu sync.Mutex
	snapBytes := map[string]int{}
	// Span geçişi: tek okuma (MV, zaman sınırlı + LIMIT + max_execution_time),
	// cluster'a bölünür; eşlenemeyenler ayrı koşu satırı + K2 span-only cluster.
	// Pencere = SeenWindow (1 saat): canlı dizinin pod → servis kenarı.
	spanByCID := map[string][]SeenRow{}
	if s.seen != nil {
		rows, err := s.seen.RecentSeen(ctx, start.Add(-SeenWindow))
		if err != nil {
			log.Printf("[entity] entity_seen okunamadı (span geçişi atlandı): %v", err)
		} else {
			// v0.10.141 — atama sonrası geriye dönük geçiş: YALNIZ atanan değer;
			// canlılar diff'e ve dizine. Ölüler yazılmaz (geçmiş MV'de).
			if r.BackfillUntil.After(start) && r.BackfillValue != "" {
				// Değer süzgeci SQL'de (RecentSeenFor): 200k satır kesimi yalnız bu
				// değerin satırlarına uygulanır.
				if extra, berr := s.seen.RecentSeenFor(ctx, start.Add(-BackfillLookback), r.BackfillValue); berr == nil {
					tickRead.Add(int64(len(extra)))
					rows = append(rows, BackfillLiveRows(extra, r.BackfillValue, start.Add(-SeenWindow))...)
				} else {
					log.Printf("[entity] backfill entity_seen okunamadı: %v", berr)
				}
			}
			tickRead.Add(int64(len(rows)))
			var unmapped map[string]int
			spanByCID, unmapped = GroupSeenByCluster(rows, clusters)
			if len(unmapped) > 0 {
				keys, counts := sortedUnmapped(unmapped)
				run := Run{ClusterID: UnmappedClusterID, StartedAt: start, FinishedAt: s.now(), Status: RunOK,
					UnmappedKeys: keys, UnmappedCounts: counts}
				if err := s.store.RecordRun(ctx, run); err != nil {
					log.Printf("[entity] unmapped koşu satırı yazılamadı: %v", err)
				}
				// K2 — kaydı olmayan değer kendi cluster entity'sini üretir.
				for _, c := range SpanOnlyClusters(unmapped, clusters, MaxSpanOnlyClusters) {
					clusters = append(clusters, c)
					for _, row := range rows {
						if row.ClusterValue == c.ID {
							spanByCID[c.ID] = append(spanByCID[c.ID], row)
						}
					}
				}
			}
		}
	}
	sem := make(chan struct{}, ParallelClusters)
	var wg sync.WaitGroup
	for _, c := range clusters {
		if c.ID == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c ClusterRef, extra []SeenRow) {
			defer wg.Done()
			defer func() { <-sem }()
			st := s.syncCluster(ctx, c, extra)
			tickRead.Add(int64(st.read))
			tickScan.Add(st.scanned)
			tickEnts.Add(int64(st.ents))
			tickRels.Add(int64(st.rels))
			tickSkip.Add(int64(st.skipped))
			tickClosed.Add(int64(st.closed))
			if st.snapBytes > 0 {
				snapMu.Lock()
				snapBytes[c.ID] = st.snapBytes
				snapMu.Unlock()
			}
			if st.ok {
				tickOK.Add(1)
			} else {
				tickFail.Add(1)
			}
		}(c, spanByCID[c.ID])
	}
	wg.Wait()
	// S5 — yatay eksen (db/queue/ext + calls): ayrı, yavaş kadans; cluster
	// geçişlerinden bağımsız (topology_edges_5m cluster'sız).
	if s.calls != nil && s.now().Sub(time.Unix(0, s.callsLast.Load())) >= CallsEvery {
		cst := s.syncCalls(ctx)
		tickRead.Add(int64(cst.read))
		tickScan.Add(cst.scanned)
		tickEnts.Add(int64(cst.ents))
		tickRels.Add(int64(cst.rels))
		tickSkip.Add(int64(cst.skipped))
		tickClosed.Add(int64(cst.closed))
	}
	dur := s.now().Sub(start)
	s.ticks.Add(1)
	s.readN.Add(tickRead.Load())
	s.lastMs.Store(int64(dur / time.Millisecond))
	s.lastAt.Store(s.now().UnixNano())
	// Tick başına tek satır: okunan / taranan / yazılan (yalnız kalıcı katman)
	// / kısılan / kapanan / cluster başına blob baytı / kilitsiz atlanan.
	log.Printf("[entity] tick: clusters=%d ok=%d failed=%d rows_read=%d rows_scanned=%d stable_written=%d relations_written=%d refresh_skipped=%d closed=%d snapshot_bytes=%s skipped_no_lock=%d duration_ms=%d",
		tickOK.Load()+tickFail.Load(), tickOK.Load(), tickFail.Load(), tickRead.Load(), tickScan.Load(), tickEnts.Load(), tickRels.Load(),
		tickSkip.Load(), tickClosed.Load(), formatSnapBytes(snapBytes), s.noLockN.Load(), dur.Milliseconds())
}

// formatSnapBytes — "cid:bayt,…" (cid sırasında; boş = "-").
func formatSnapBytes(m map[string]int) string {
	if len(m) == 0 {
		return "-"
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id + ":" + strconv.Itoa(m[id])
	}
	return strings.Join(parts, ",")
}

// SpanOnlyClusters — K2 (saf): eşlenemeyen span cluster değerlerinden
// span-only ClusterRef'ler; boş değer ve bir kaydın id'siyle çakışan değer
// atlanır, en çok satırı olan `max` değer (eşitlikte ada göre) alınır.
func SpanOnlyClusters(unmapped map[string]int, refs []ClusterRef, max int) []ClusterRef {
	taken := map[string]bool{}
	for _, r := range refs {
		taken[r.ID] = true
	}
	vals := make([]string, 0, len(unmapped))
	for v := range unmapped {
		if v != "" && v != UnmappedClusterID && v != TopologyClusterID && !taken[v] {
			vals = append(vals, v)
		}
	}
	sort.Slice(vals, func(i, j int) bool {
		if unmapped[vals[i]] != unmapped[vals[j]] {
			return unmapped[vals[i]] > unmapped[vals[j]]
		}
		return vals[i] < vals[j]
	})
	if len(vals) > max {
		vals = vals[:max]
	}
	out := make([]ClusterRef, 0, len(vals))
	for _, v := range vals {
		out = append(out, ClusterRef{ID: v, Name: v, SpanClusterValue: v, SpanOnly: true})
	}
	return out
}

// clusterTickStats — syncCluster'ın tick günlüğüne katkısı.
type clusterTickStats struct {
	ok                                bool
	read, ents, rels, skipped, closed int
	snapBytes                         int
	scanned                           int64
}

func (s *Syncer) clusterState(cid string) *clusterState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[cid]
	if !ok {
		st = &clusterState{absent: map[string]Absence{}}
		s.state[cid] = st
	}
	return st
}

// syncCluster — tek cluster; hata = sync_runs failed, hiçbir yazım yok.
// SpanOnly cluster (K2): Thanos sorgusu yok; tek Thanos varlığı yerine
// source=span cluster entity'si + span geçişi.
func (s *Syncer) syncCluster(ctx context.Context, c ClusterRef, spanRows []SeenRow) (st clusterTickStats) {
	now := s.now()
	run := Run{ClusterID: c.ID, StartedAt: now}
	fail := func(err error) {
		s.failN.Add(1)
		run.Status, run.Error, run.FinishedAt = RunFailed, err.Error(), s.now()
		cs := s.clusterState(c.ID)
		s.mu.Lock()
		cs.lastErr = err.Error()
		s.mu.Unlock()
		if rerr := s.store.RecordRun(ctx, run); rerr != nil {
			log.Printf("[entity] sync_runs yazılamadı (%s): %v", c.ID, rerr)
		}
	}
	var ents []Entity
	var rels []Relation
	if c.SpanOnly {
		ents = []Entity{{Type: TypeCluster, ClusterID: c.ID, ID: ClusterID(c.ID), Name: c.ID, Labels: map[string]string{}, Source: SourceSpan}}
	} else {
		tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		t0 := s.now()
		sets, err := s.src.Fetch(tctx, c, SnapshotQueries(nsMatcher(c.NamespaceFilter)))
		cancel()
		run.ThanosMs = int(s.now().Sub(t0) / time.Millisecond)
		if err != nil {
			fail(err)
			return
		}
		ents, rels = Normalize(c.ID, SnapshotFromSamples(sets))
	}
	entByID := make(map[string]Entity, len(ents))
	for _, e := range ents {
		entByID[e.ID] = e
	}
	// Span geçişi AYNI diff'e girer (ayrı diff, görülmeyen her şeyi kapatırdı).
	if len(spanRows) > 0 {
		se, sr := SpanSeenToEntities(c.ID, spanRows, entByID)
		for _, e := range se {
			if _, ok := entByID[e.ID]; !ok {
				entByID[e.ID] = e
			}
		}
		rels = append(rels, sr...)
	}
	relByKey := make(map[string]Relation, len(rels))
	for _, rl := range rels {
		k := relKey(rl.Type, rl.ParentID, rl.ChildID)
		if _, dup := relByKey[k]; dup {
			continue // Thanos'unki önce geldi, kazanır
		}
		relByKey[k] = rl
	}
	// (1) Canlı dizin: pod/container dahil TÜM görüntü, CH'den bağımsız —
	// CH kesintisinde de "şimdi" taze kalır.
	snap := BuildLiveSnapshot(c.ID, now, entByID, relByKey, spanRows)
	st.snapBytes = s.publishLive(ctx, c.ID, snap)
	// (2) Kalıcı katman: yalnız uzun ömürlü varlıklar + parent kenarları + köprüler.
	open, err := s.store.OpenLifetimes(ctx, c.ID)
	if err != nil {
		fail(err)
		return
	}
	st.read, st.scanned = len(open.Lifetimes), open.RowsScanned
	// S2 köprüleri (serves, same_as) — aynı görüntüden, bellekte.
	for _, rl := range BridgeRelations(c.ID, entByID, snap, open) {
		relByKey[relKey(rl.Type, rl.ParentID, rl.ChildID)] = rl
	}
	if !c.SpanOnly {
		for _, rl := range s.mergeSpanOnly(ctx, c) {
			relByKey[relKey(rl.Type, rl.ParentID, rl.ChildID)] = rl
		}
	}
	seen := stableSeen(entByID, relByKey)
	cs := s.clusterState(c.ID)
	s.mu.Lock()
	absent, lastOK := cs.absent, cs.lastOK
	s.mu.Unlock()
	ch, nextAbsent := DiffLifetimes(now, open.Lifetimes, seen, absent, lastOK, PodGap)
	ch.Refresh, st.skipped = ThrottleRefresh(now, open.Lifetimes, ch.Refresh, RefreshEvery)
	s.skipN.Add(int64(st.skipped))
	rows := RowsForChange(c.ID, ch, entByID, open.Entities)
	relRows := relationRowsForChange(c.ID, ch, relByKey, open.Relations)
	if len(rows)+len(relRows) > 0 {
		t1 := s.now()
		if err := s.store.Apply(ctx, c.ID, rows, relRows); err != nil {
			fail(err)
			return
		}
		run.CHMs = int(s.now().Sub(t1) / time.Millisecond)
	}
	run.Status, run.FinishedAt = RunOK, s.now()
	run.EntitiesWritten, run.RelationsWritten = len(rows), len(relRows)
	for _, l := range ch.Close {
		if !isRelKey(l.ID) {
			run.Closed++
		}
	}
	s.mu.Lock()
	cs.absent, cs.lastOK, cs.lastErr = nextAbsent, now, ""
	s.mu.Unlock()
	s.okN.Add(1)
	s.entsN.Add(int64(len(rows)))
	s.relsN.Add(int64(len(relRows)))
	st.ok, st.ents, st.rels, st.closed = true, len(rows), len(relRows), run.Closed
	if err := s.store.RecordRun(ctx, run); err != nil {
		log.Printf("[entity] sync_runs yazılamadı (%s): %v", c.ID, err)
	}
	return st
}

// mergeSpanOnly — K2 + S2: kaydın sahiplendiği span değerinin eski
// span-only ömürleri (S1 notu: "same_as'a kadar açık kalır, last_seen
// donar") kapanır ve cluster:<değer> → cluster:<cid> same_as kenarı döner
// (cid'nin diff'ine girer; hedef görüldükçe korunur). Değer başına süreçte
// bir okuma (OpenLifetimes, sınırlı); boş küme de "birleşti" sayılır.
func (s *Syncer) mergeSpanOnly(ctx context.Context, c ClusterRef) []Relation {
	cs := s.clusterState(c.ID)
	var out []Relation
	for _, v := range append([]string{c.SpanClusterValue}, c.SpanClusterValues...) {
		s.mu.Lock()
		done := v == "" || v == c.ID || cs.merged[v]
		s.mu.Unlock()
		if done {
			continue
		}
		open, err := s.store.OpenLifetimes(ctx, v)
		if err != nil {
			log.Printf("[entity] span-only birleştirme okunamadı (%s←%s): %v", c.ID, v, err)
			continue
		}
		if len(open.Lifetimes) > 0 {
			var ch Change
			for _, l := range open.Lifetimes {
				l.ValidTo = l.LastSeen
				ch.Close = append(ch.Close, l)
			}
			sort.Slice(ch.Close, func(i, j int) bool { return ch.Close[i].ID < ch.Close[j].ID })
			if err := s.store.Apply(ctx, v, RowsForChange(v, ch, nil, open.Entities), relationRowsForChange(v, ch, nil, open.Relations)); err != nil {
				log.Printf("[entity] span-only birleştirme yazılamadı (%s←%s): %v", c.ID, v, err)
				continue
			}
			if _, had := open.Entities[ClusterID(v)]; had {
				out = append(out, ClusterSameAs(c.ID, v))
			}
			log.Printf("[entity] span-only cluster %s → %s birleşti: closed=%d", v, c.ID, len(ch.Close))
		}
		s.mu.Lock()
		if cs.merged == nil {
			cs.merged = map[string]bool{}
		}
		cs.merged[v] = true
		s.mu.Unlock()
	}
	return out
}

// stableSeen — SAF: görüntünün kalıcı katmanı (diff girdisi). İlişkiler
// relKey anahtarıyla, yalnız ID taşıyan Entity olarak.
func stableSeen(ents map[string]Entity, rels map[string]Relation) map[string]Entity {
	out := make(map[string]Entity, len(ents))
	for id, e := range ents {
		if IsStableType(e.Type) {
			out[id] = e
		}
	}
	for k, rl := range rels {
		if IsStableRelation(rl) {
			out[k] = Entity{ID: k}
		}
	}
	return out
}

// publishLive — blob'u lider yazar; hata yalnız günlük (okuyucu MV'ye
// düşer, bir sonraki tick yeniden yazar). Dönüş: yazılan bayt.
func (s *Syncer) publishLive(ctx context.Context, cid string, snap LiveSnapshot) int {
	if s.live == nil {
		return 0
	}
	b, err := EncodeLive(snap)
	if err != nil {
		log.Printf("[entity] canlı dizin kodlanamadı (%s): %v", cid, err)
		return 0
	}
	if err := s.live.Set(ctx, LiveKey(cid), b, LiveTTL); err != nil {
		log.Printf("[entity] canlı dizin yazılamadı (%s): %v — okuyucular MV yoluna düşer", cid, err)
		return 0
	}
	return len(b)
}

// relKey — ilişki ömrünün diff anahtarı (entity_id uzayıyla çakışmaz:
// "rel|" öneki hiçbir tip öneki değil).
func relKey(typ, parent, child string) string { return "rel|" + typ + "|" + parent + "|" + child }

// nsMatcher — thanos.nsMatcher'ın aynası (import döngüsü olmasın): boş →
// boş; doluysa `,namespace=~"<re>"` (tırnak/ters bölü kaçışlı).
func nsMatcher(re string) string {
	if re == "" {
		return ""
	}
	esc := ""
	for _, ch := range re {
		switch ch {
		case '\\':
			esc += `\\`
		case '"':
			esc += `\"`
		default:
			esc += string(ch)
		}
	}
	return `,namespace=~"` + esc + `"`
}

// sortedLabels — Array çifti, anahtara göre sıralı (deterministik satır).
func sortedLabels(m map[string]string) ([]string, []string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]string, len(keys))
	for i, k := range keys {
		vals[i] = m[k]
	}
	return keys, vals
}

// RowsForChange — ömür değişikliği → tam satırlar (invariant #4: her satır
// TÜM alanları taşır). Açılış/tazeleme bu tick'in görüntüsünden (cur);
// kapanış CH'den okunan açık satırın alanlarıyla (prev) — uid değişiminde
// kapanan eski ömür yeni nesnenin alanlarını almaz.
func RowsForChange(cid string, ch Change, cur, prev map[string]Entity) []EntityRow {
	out := make([]EntityRow, 0, len(ch.Open)+len(ch.Close)+len(ch.Refresh))
	mk := func(e Entity, l Lifetime) EntityRow {
		lk, lv := sortedLabels(e.Labels)
		if lk == nil {
			lk, lv = []string{}, []string{}
		}
		uid := l.UID
		if uid == "" {
			uid = e.UID
		}
		return EntityRow{
			Type: e.Type, ClusterID: cid, ID: l.ID, Namespace: e.Namespace, Name: e.Name, UID: uid,
			ParentID: e.ParentID, ValidFrom: l.ValidFrom, ValidTo: l.ValidTo, FirstSeen: l.ValidFrom,
			LastSeen: l.LastSeen, LabelKeys: lk, LabelValues: lv, Source: e.Source,
		}
	}
	pick := func(id string, first, second map[string]Entity) (Entity, bool) {
		if e, ok := first[id]; ok {
			return e, true
		}
		e, ok := second[id]
		return e, ok
	}
	for _, l := range ch.Open {
		if e, ok := cur[l.ID]; ok {
			out = append(out, mk(e, l))
		}
	}
	for _, l := range ch.Refresh {
		if e, ok := pick(l.ID, cur, prev); ok {
			out = append(out, mk(e, l))
		}
	}
	for _, l := range ch.Close {
		if e, ok := pick(l.ID, prev, cur); ok {
			out = append(out, mk(e, l))
		}
	}
	return out
}

// relationRowsForChange — ilişki ömürleri (anahtar "rel|…"); alanlar bu
// tick'ten, yoksa CH'deki açık satırdan, o da yoksa anahtardan.
func relationRowsForChange(cid string, ch Change, cur, prev map[string]Relation) []RelationRow {
	var out []RelationRow
	mk := func(l Lifetime) (RelationRow, bool) {
		r, ok := cur[l.ID]
		if !ok {
			r, ok = prev[l.ID]
		}
		if !ok {
			typ, parent, child, pok := parseRelKey(l.ID)
			if !pok {
				return RelationRow{}, false
			}
			r = Relation{Type: typ, ClusterID: cid, ParentID: parent, ChildID: child, Source: SourceThanos}
		}
		return RelationRow{Type: r.Type, ClusterID: cid, ParentID: r.ParentID, ChildID: r.ChildID,
			ValidFrom: l.ValidFrom, ValidTo: l.ValidTo, LastSeen: l.LastSeen, Source: r.Source}, true
	}
	for _, group := range [][]Lifetime{ch.Open, ch.Refresh, ch.Close} {
		for _, l := range group {
			if !isRelKey(l.ID) {
				continue
			}
			if row, ok := mk(l); ok {
				out = append(out, row)
			}
		}
	}
	return out
}

func isRelKey(k string) bool { return len(k) > 4 && k[:4] == "rel|" }

func parseRelKey(k string) (typ, parent, child string, ok bool) {
	if !isRelKey(k) {
		return "", "", "", false
	}
	rest := k[4:]
	i := indexByte(rest, '|')
	if i < 0 {
		return "", "", "", false
	}
	typ = rest[:i]
	rest = rest[i+1:]
	j := indexByte(rest, '|')
	if j < 0 {
		return "", "", "", false
	}
	return typ, rest[:j], rest[j+1:], true
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
