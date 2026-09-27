package rollout

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// v2worker.go — v0.10.982 — ROLLOUTS v2 P2.2: CANLI KSM DEDEKTÖR İŞÇİSİ
// (docs/rollouts/v2-audit.md §3.3, §4, §10.4, §10.6; kararlar 8, 9, 10).
//
// Yalnız LİDERDE koşar (cache.LeaderHolder, kendi kilidi "rollout-detector";
// v1 "rollout-reconciler" kilidinden bağımsız). BAYRAK: system_settings
// ["rollouts"] enabled=true VE source="v2" — varsayılan (source=v1) ya da
// kapalı bayrakta Tick hiçbir şey yapmaz: sorgu yok, CH okuması yok, koşu
// satırı yok (N pod × N satır olmasın; v1 emsali). Kapalı/lider olmayan
// tikte bellek düşer: yeniden açılış ya da yeniden liderlik CH'den kurar.
//
// Tik (DetectorInterval, varsayılan 30 s; inFlight CAS örtüşmeyi keser):
//   her etkin Remote Cluster için — Argo hub'ları DAHİL (karar 5) — en çok
//   4 paralel (§3.3):
//     1. önceki durum: bellekte yoksa rollout_workload_state FINAL +
//        rollout_events (incarnation başına son satır) keyset okuması
//        (§10.6). Okuma hatası → küme bu tikte atlanır (PrevIncomplete yolu
//        yerine: boş durum "ilk-ever" sanılıp sahte 'initial' basılmasın).
//        lockDegraded sürerken (her pod lider) bu okuma HER tik yapılır:
//        §10.3.1 "mint'ten önce FINAL yeniden okuma" — iki pod aynı yeni iş
//        yüküne farklı incarnation basarsa bir sonraki tik ikisi de CH'deki
//        tek duruma (lost-state kuralıyla en yeniye) yakınsar. Tek yazıcıda
//        bellek zaten CH'nin kendi yazdığımız görüntüsüdür.
//     2. KSM: V2TickQueries → WorkerQuery (max by, nsMatcher, dedup=true),
//        kümenin bütün sorguları TEK değerlendirme zamanında (tik − 15 s,
//        time=): ardışık sorgular farklı scrape okuyup RS listesini nesil
//        ölçüsünden önce görmesin. Sorgu hatası → küme atlanır;
//        kesik/kısmi/uyarılı → Complete=false, fark ALINMAZ, bellek aynen
//        (§4.10), koşu partial.
//     2b. toplu yokluk koruması (V9 doğrulanmadı): son kabul edilen tam
//        okumada görünen iş yüklerinin yarıdan azı görünüyorsa o türün
//        YOKLUK işlemesi en çok 30 dk dondurulur (v2MassAbsence +
//        v2FreezeAbsence); görünen iş yükleri normal işlenir.
//     3. imajlar: açık olaylı / bekleyen / nesli değişmiş / ilk kez görülen
//        iş yükleri (sınırlı) + artan bütçeyle imajı boş durumlar (okuması
//        boş dönen aday 2^n tik geri çekilir); hata ya da kısmi imaj
//        okuması farkı atlatmaz, yalnız sayılır.
//     4. DetectV2 (P2.1 saf çekirdek).
//     5. liderlik YENİDEN doğrulanır (tik sırasında yeniden edinim de —
//        resetPending — yazımı keser) → önce rollout_events, sonra
//        rollout_workload_state (çekirdek sözleşmesi: arada düşerse sonraki
//        tik aynı anahtarı yeniden türetir). Yazım hatası → küme belleği
//        düşer, sonraki tik CH'den yeniden kurar.
//     6. başarıda bellek = V2Carry(önceki, çıktı), olaylar incarnation
//        başına son satıra budanır (sınırlı bellek).
//   tik başına TEK rollout_worker_runs satırı (ORDER BY (worker,
//   started_at, host): küme başına satır aynı started_at'te çökerdi):
//   scopes_total/ok = küme, series_read, truncated, partial_response,
//   rows_written, api_calls = gönderilen sorgu, error = notlar + teşhis
//   sayaçlarının özeti.
//
// Lider edinimi (OnAcquire) belleği düşürür → ilk tik durumu CH'den kurar
// (V2Memory.Absent DDL'de yok: çekirdek sözleşmesi gereği boş başlar,
// yokluk kuralı yeniden sayar). lockDegraded: dedektör ATLAMAZ (v1
// reconciler gibi; §3.3/§10.4 — idempotent RMT tabloları, anahtar
// deterministik) ama durumu her tik CH'den tazeler (yukarıda 1); kalan
// risk yeni iş yükü başına en çok bir yinelenen (eski incarnation'lı,
// sonraki tikte kapanan) 'initial' satırı, host kolonuyla görünür.

// V2Store — dedektörün CH kapısı (*chstore.Store karşılar; testte sahte).
type V2Store interface {
	// RolloutV2States — rollout_workload_state FINAL WHERE cluster_id = ?,
	// keyset sayfalı; tavan aşımı HATA (kesik durumdan yazım yok).
	RolloutV2States(ctx context.Context, clusterID string) ([]V2WorkloadState, error)
	// RolloutV2LatestEvents — rollout_events FINAL, (iş yükü, incarnation)
	// başına generation'ı en büyük satır (çekirdeğin PrevEvents sözleşmesi).
	RolloutV2LatestEvents(ctx context.Context, clusterID string) ([]V2Event, error)
	RolloutV2WriteEvents(ctx context.Context, rows []V2Event) error
	RolloutV2WriteStates(ctx context.Context, rows []V2WorkloadState) error
	RecordRolloutWorkerRun(ctx context.Context, run WorkerRun) error
}

// V2Querier — işçi PromQL okuyucusu (thanos.WorkerQuery adaptörü, v2thanos.go).
// at: anlık sorgunun değerlendirme zamanı (kümenin bütün tik sorgularında aynı).
type V2Querier interface {
	Query(ctx context.Context, clusterID, expr string, at time.Time) (V2QueryResult, error)
}

// V2ClusterRef — hedef küme: EffectiveID, görünen ad (koşu notu), namespace
// kalkanı (thanos.NamespaceMatcher çıktısı).
type V2ClusterRef struct {
	ID        string
	Name      string
	NSMatcher string
}

// V2ClusterSource — etkin Remote Cluster'lar (Argo hub'ları dahil).
type V2ClusterSource interface {
	V2Clusters() []V2ClusterRef
}

const (
	v2DetectorParallel = 4    // §3.3: entity syncer ParallelClusters emsali
	v2RunErrorMax      = 2000 // error kolonuna yazılan rune tavanı
	v2RunNotesMax      = 5    // listelenen küme notu
	// v2EvalLag — kümenin tik sorgularının sabit değerlendirme zamanı tik
	// saatinden bu kadar geride: scrape zaman damgası scrape BAŞLANGICIDIR,
	// örnekler scrape bitince görünür (Prometheus varsayılan scrape_timeout
	// 10 s). Geride sabitlenen zamandaki her örnek ilk sorgudan önce
	// yazılmış olur → bir tikin bütün sorguları aynı scrape'i okur.
	v2EvalLag = 15 * time.Second
)

// v2ClusterMem — küme başına lider belleği.
type v2ClusterMem struct {
	loaded bool
	states []V2WorkloadState
	events []V2Event
	memory V2Memory
	// baseline — son KABUL edilen tam okumada görünen iş yükleri (toplu
	// yokluk tabanı; nil = henüz yok → son 48 sa canlı durumlar).
	baseline map[V2Key]bool
	// massSince — süren toplu-yokluk bekleyişinin başladığı tik (sıfır = yok).
	massSince time.Time
	// ticks — imaj aşamasına ulaşan tik sayacı; fillTries — imaj okuması boş
	// dönen fill adaylarının geri çekilmesi (v2PickFill / v2NoteFill).
	ticks     int
	fillTries map[V2Key]v2FillTry
}

// Toplu yokluk koruması (V9 doğrulanmadan fail-safe): tür başına tabanda
// (son kabul edilen tam okumada görünenler) en az v2MassAbsenceMinStates iş
// yükü varken okumada bunların yarıdan azı görünüyorsa (ama tür ailesi
// büsbütün yok değilse — o hâli çekirdek family_absent ile zaten korur) o
// türün yokluk işlemesi en çok v2MassAbsenceMaxHold dondurulur; süre dolarsa
// yokluk gerçektir (toplu silme), kabul edilir ve taban sıfırlanır. Taban
// "son okumada görünenler" olduğundan önceden silinmiş iş yükleri (durumu
// 400 gün yaşar) canlı sayılmaz; yalnız bu tikte kaybolan kitle tetikler.
const (
	v2MassAbsenceMinStates = 20
	// v2MassAbsenceRecent — taban yokken (yeniden kurulum) canlı sayılan
	// durum penceresi: gözlenen iş yükünün last_seen_at'i en geç v2TouchEvery
	// (24 sa) aralıkla dokunulur; +1 sa pay. 48 sa, dün silinmiş kitleyi de
	// sayıp her yeniden başlatmada sahte bekleyiş açıyordu (inceleme).
	v2MassAbsenceRecent  = v2TouchEvery + time.Hour
	v2MassAbsenceMaxHold = 30 * time.Minute
)

// v2AbsenceBase — SAF: toplu yokluk tabanı. baseline varsa o (kopya); yoksa
// (yeniden kurulum sonrası ilk okuma) last_seen_at'i v2MassAbsenceRecent
// içindeki durumlar — güncel incarnation'ının son olayı superseded olanlar
// HARİÇ (yokluk kuralının "gone" kapanışı: iş yükü silinmiş; güncel
// incarnation'ın son olayını başka yol superseded bırakmaz — START eski
// nesli, yeni incarnation eski incarnation'ı kapatır).
//
// Kalan bilinen payı: son ~25 sa içinde AÇIK OLAYSIZ silinen iş yükleri de
// sayılır (silme durum satırına iz bırakmaz). Bu yönde hata güvenlidir
// (yanlış satır yok, kapanış ≤ 30 dk gecikir; teşhis ayrı sayılır); ters
// yön — yeniden kurulumda tabanı ilk okumadan tohumlamak — kısmi okumayla
// açılan yeniden kurulumda korumayı tamamen kapatıp sahte "gone" yazardı.
func v2AbsenceBase(baseline map[V2Key]bool, clusterID string, states []V2WorkloadState, events []V2Event, now time.Time) map[V2Key]bool {
	out := map[V2Key]bool{}
	if baseline != nil {
		for k := range baseline {
			out[k] = true
		}
		return out
	}
	type ik struct {
		k   V2Key
		inc int64
	}
	latest := map[ik]V2Event{}
	for _, e := range events {
		if e.ClusterID != clusterID {
			continue
		}
		k := ik{e.Key(), v2ms(e.IncarnationAt).UnixMilli()}
		if cur, ok := latest[k]; !ok || e.Generation > cur.Generation {
			latest[k] = e
		}
	}
	for _, s := range states {
		if s.ClusterID != clusterID || now.Sub(s.LastSeenAt) > v2MassAbsenceRecent {
			continue
		}
		if e, ok := latest[ik{s.Key(), v2ms(s.IncarnationAt).UnixMilli()}]; ok && e.Status == V2StatusSuperseded {
			continue
		}
		out[s.Key()] = true
	}
	return out
}

// v2NextBaseline — SAF: kabul edilen okumadan sonraki toplu yokluk tabanı:
// görünenler + tabanda olup (a) dondurulan türde ya da (b) okumada HİÇ iş
// yükü görünmeyen etkin türde kalanlar. (b) inceleme düzeltmesi: tür ailesi
// büsbütün yokken çekirdek yokluğu işlemez (family_absent); o türün tabanı
// düşürülseydi sonraki tikte kısmi geri dönüş (ör. KSM shard'ının biri önce
// döner — V9) tabansız kalır, koruma tetiklenmez, kaybolanlar K tik sonra
// "gone" kapanırdı. Taban, çekirdeğin o türün yokluğunu son GERÇEKTEN
// işlediği okumada kalır.
func v2NextBaseline(seen, base map[V2Key]bool, frozenKinds, enabledKinds []string) map[V2Key]bool {
	kindSeen := map[string]bool{}
	nb := map[V2Key]bool{}
	for k := range seen {
		nb[k] = true
		kindSeen[k.Kind] = true
	}
	keep := map[string]bool{}
	for _, k := range frozenKinds {
		keep[k] = true
	}
	for _, k := range enabledKinds {
		if kind, ok := canonicalV2Kind(k); ok && !kindSeen[kind] {
			keep[kind] = true
		}
	}
	for k := range base {
		if keep[k.Kind] {
			nb[k] = true
		}
	}
	return nb
}

// İmaj fill geri çekilmesi (inceleme): okuması başarılı ama güncel revizyonun
// imajı BOŞ dönen aday (ör. STS'de kube_pod_labels controller_revision_hash
// etiketi allowlist'te değil — V8/K2.4 doğrulanmadı; pod'lar pending) her
// tik yeniden seçilir, anahtar sırasındaki ilk 20 kalıcı olarak bütçeyi
// tutar, sonrakilerin imajı hiç dolmazdı. Boş okumadan sonra aday 2^n tik
// (en çok 2^v2FillBackoffMaxShift) atlanır; imaj gelince kayıt silinir.
// Presence.PodRevisionHash'e göre STS/DS'yi baştan dışlamak yerine geri
// çekilme: o bayrak DS hash sorgusundan türer, DS türü kapalıyken ya da
// namespace kalkanı DS'leri gizlerken etiket varken de false okunur.
const v2FillBackoffMaxShift = 6 // 64 tik ≈ 32 dk (30 s aralıkta)

// v2FillTry — bir fill adayının ardışık boş okuma sayısı ve yeniden
// denenebileceği tik.
type v2FillTry struct {
	empty int
	next  int
}

// v2PickFill — SAF: sıralı adaylardan geri çekilmede olmayan ilk limit
// tanesi; deferred = geri çekilme yüzünden atlanan. tries içinde aday
// olmayan anahtarlar budanır (bellek aday kümesiyle sınırlı).
func v2PickFill(cands []V2Key, tries map[V2Key]v2FillTry, tick, limit int) (pick []V2Key, deferred int) {
	in := make(map[V2Key]bool, len(cands))
	for _, k := range cands {
		in[k] = true
		if t, ok := tries[k]; ok && tick < t.next {
			deferred++
			continue
		}
		if len(pick) < limit {
			pick = append(pick, k)
		}
	}
	for k := range tries {
		if !in[k] {
			delete(tries, k)
		}
	}
	return pick, deferred
}

// v2NoteFill — SAF: okuması kullanılabilir dönen fill adaylarının sonucunu
// işler: güncel revizyonun imajı geldiyse kayıt silinir, gelmediyse geri
// çekilme büyür. Döner: boş dönen aday sayısı.
func v2NoteFill(snap V2Snapshot, clusterID string, read []V2Key, states []V2WorkloadState, tries map[V2Key]v2FillTry, tick int) int {
	cur := map[V2Key]string{}
	for _, s := range states {
		if s.ClusterID == clusterID {
			cur[s.Key()] = s.CurrentRevision
		}
	}
	obs := map[V2Key]V2Observation{}
	for _, o := range snap.Workloads {
		if kind, ok := canonicalV2Kind(o.Kind); ok {
			obs[V2Key{clusterID, o.Namespace, kind, o.Name}] = o
		}
	}
	empty := 0
	for _, k := range read {
		if len(v2ImagesOf(obs[k], cur[k])) > 0 {
			delete(tries, k)
			continue
		}
		empty++
		t := tries[k]
		t.empty++
		t.next = tick + 1<<min(t.empty, v2FillBackoffMaxShift)
		tries[k] = t
	}
	return empty
}

// v2SnapKeys — SAF: okumada görünen iş yükü anahtarları.
func v2SnapKeys(snap V2Snapshot, clusterID string) map[V2Key]bool {
	seen := map[V2Key]bool{}
	for _, o := range snap.Workloads {
		if kind, ok := canonicalV2Kind(o.Kind); ok {
			seen[V2Key{clusterID, o.Namespace, kind, o.Name}] = true
		}
	}
	return seen
}

// v2MassAbsence — SAF: toplu yokluk gösteren türler (kanonik sırada). base:
// v2AbsenceBase çıktısı.
func v2MassAbsence(snap V2Snapshot, clusterID string, base map[V2Key]bool) []string {
	seen := v2SnapKeys(snap, clusterID)
	kindSeen := map[string]bool{}
	for k := range seen {
		kindSeen[k.Kind] = true
	}
	live, present := map[string]int{}, map[string]int{}
	for k := range base {
		if k.ClusterID != clusterID {
			continue
		}
		live[k.Kind]++
		if seen[k] {
			present[k.Kind]++
		}
	}
	var out []string
	for _, kind := range v2Kinds {
		if kindSeen[kind] && live[kind] >= v2MassAbsenceMinStates && present[kind]*2 < live[kind] {
			out = append(out, kind)
		}
	}
	return out
}

// v2FreezeAbsence — SAF: dondurulan türlerde okumada görünmeyen iş
// yüklerinin yokluk sonucunu geri alır: olay satırları (yokluk kuralının
// "gone" kapanışı — görünmeyen iş yükünün başka satırı olmaz) ve durum
// satırları atılır, Memory.Absent önceki değerine döner. Döner: dondurulan
// iş yükü sayısı. Görünen iş yüklerinin satırlarına dokunmaz.
func v2FreezeAbsence(out *V2Output, seen map[V2Key]bool, kinds []string, prev V2Memory) int {
	frozen := map[string]bool{}
	for _, k := range kinds {
		frozen[k] = true
	}
	hit := func(k V2Key) bool { return frozen[k.Kind] && !seen[k] }
	evs := out.Events[:0:0]
	for _, e := range out.Events {
		if !hit(e.Key()) {
			evs = append(evs, e)
		}
	}
	sts := out.States[:0:0]
	for _, s := range out.States {
		if !hit(s.Key()) {
			sts = append(sts, s)
		}
	}
	out.Events, out.States = evs, sts
	n := 0
	mem := map[V2Key]int{}
	for k, v := range out.Memory.Absent {
		if !hit(k) {
			mem[k] = v
			continue
		}
		n++
		if p := prev.Absent[k]; p > 0 {
			mem[k] = p
		}
	}
	for k, p := range prev.Absent {
		if hit(k) {
			if _, ok := out.Memory.Absent[k]; !ok && p > 0 {
				mem[k] = p
			}
		}
	}
	if len(mem) == 0 {
		mem = nil
	}
	out.Memory.Absent = mem
	return n
}

// V2Detector — rollout_events / rollout_workload_state'in TEK yazıcısı.
type V2Detector struct {
	store    V2Store
	querier  V2Querier
	clusters V2ClusterSource
	settings func() Settings
	leader   func() bool
	degraded func() bool // lockDegraded canlı değeri (nil = hiç)
	host     string
	now      func() time.Time
	parallel int

	inFlight     atomic.Bool
	resetPending atomic.Bool
	mem          map[string]*v2ClusterMem // yalnız inFlight altında
	lastRun      atomic.Pointer[WorkerRun]
	lastStatus   atomic.Pointer[string]
}

// NewV2Detector — v0.10.982. settings: canlı rollouts blobu (SettingsService.Current).
func NewV2Detector(store V2Store, q V2Querier, clusters V2ClusterSource, settings func() Settings) *V2Detector {
	host, _ := os.Hostname()
	if strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	return &V2Detector{store: store, querier: q, clusters: clusters, settings: settings,
		leader: func() bool { return true }, host: host, now: time.Now, parallel: v2DetectorParallel}
}

func (d *V2Detector) SetLeaderCheck(f func() bool) { d.leader = f }

// SetDegradedCheck — v0.10.982 — lockDegraded (Redis tanımlı ama erişilemez →
// her pod lider) canlı değeri: doğruyken durum her tik CH'den tazelenir.
func (d *V2Detector) SetDegradedCheck(f func() bool) { d.degraded = f }

// WaitActive — v0.10.982 — bayrak (V2DetectorActive) ilk kez açılana dek
// bekler; true = açıldı, false = bağlam kapandı. main kilidi ve döngüyü
// bundan SONRA başlatır: varsayılan kurulumda "rollout-detector" Redis
// anahtarı, kalp atışı ya da günlük satırı olmaz. Bir kez açıldıktan sonra
// kapanan bayrakta Tick zaten iş yapmaz (v1 emsali).
func (d *V2Detector) WaitActive(ctx context.Context, poll time.Duration) bool {
	if poll <= 0 {
		poll = 30 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return false
		}
		if V2DetectorActive(d.settings()) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
	}
}

// OnAcquire — liderlik edinimi: bellek bir sonraki tikte düşer ve durum
// CH'den yeniden kurulur. Heartbeat goroutine'inden çağrılır; belleğe
// dokunmaz (bayrak), tik goroutine'i uygular.
func (d *V2Detector) OnAcquire() { d.resetPending.Store(true) }

// LastRun — son tikin KOPYASI.
func (d *V2Detector) LastRun() (WorkerRun, bool) {
	if p := d.lastRun.Load(); p != nil {
		return *p, true
	}
	return WorkerRun{}, false
}

// V2DetectorActive — v0.10.982 — SAF bayrak kapısı: enabled VE source=v2.
func V2DetectorActive(s Settings) bool { return s.Enabled && s.ResolvedV2().Source == SourceV2 }

// Run — periyodik döngü; ilk tik SetOnAcquire'dan (v1 emsali). Uyku ≤ 30 s
// adımlarla, ayar değişince kalan süre yeniden hesaplanır.
func (d *V2Detector) Run(ctx context.Context) {
	for {
		if !d.sleepInterval(ctx) {
			return
		}
		d.Tick(ctx)
	}
}

func (d *V2Detector) interval() time.Duration {
	iv := d.settings().ResolvedV2().DetectorInterval
	if iv <= 0 {
		iv = 30 * time.Second
	}
	return iv
}

func (d *V2Detector) sleepInterval(ctx context.Context) bool {
	start := d.now()
	for {
		if ctx.Err() != nil {
			return false
		}
		rem := start.Add(d.interval()).Sub(d.now())
		if rem <= 0 {
			return true
		}
		if rem > 30*time.Second {
			rem = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(rem):
		}
	}
}

// Tick — bir tur; örtüşen çağrı atlanır. Döner: koştu mu.
func (d *V2Detector) Tick(ctx context.Context) bool {
	if !d.inFlight.CompareAndSwap(false, true) {
		return false
	}
	defer d.inFlight.Store(false)
	s := d.settings()
	if !V2DetectorActive(s) || !d.leader() {
		// Kapalı ya da lider değil: sorgu yok, satır yok; bellek düşer ki
		// yeniden açılış/liderlik başka pod'un yazdıklarını CH'den okusun.
		d.mem = nil
		d.resetPending.Store(false)
		return false
	}
	if d.resetPending.Swap(false) {
		d.mem = nil
	}
	if d.mem == nil {
		d.mem = map[string]*v2ClusterMem{}
	}
	set := s.ResolvedV2()
	tctx, cancel := context.WithTimeout(ctx, tickDeadline(set.DetectorInterval))
	defer cancel()
	d.tick(ctx, tctx, set)
	return true
}

// v2ClusterResult — bir kümenin tik sonucu.
type v2ClusterResult struct {
	ok         bool
	hard       bool // okuma/sorgu/yazım hatası (kısmi okuma değil)
	calls      int
	series     int
	truncated  bool
	partial    bool
	rows       int
	note       string
	diag       map[string]int
	lostLeader bool
}

func (d *V2Detector) tick(parent, ctx context.Context, set V2Resolved) {
	run := WorkerRun{Worker: WorkerRolloutDetector, StartedAt: d.now(), Host: d.host, Status: RunOK}
	diag := map[string]int{}
	var notes []string
	var canceled bool
	defer func() { d.finish(parent, &run, notes, diag, canceled) }()

	refs := d.clusters.V2Clusters()
	live := map[string]bool{}
	for _, r := range refs {
		live[r.ID] = true
	}
	for id := range d.mem {
		if !live[id] {
			delete(d.mem, id) // kapatılan / kaldırılan küme
		}
	}
	run.ScopesTotal = len(refs)
	now := d.now()
	results := make([]v2ClusterResult, len(refs))
	par := d.parallel
	if par <= 0 {
		par = 1
	}
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for i, ref := range refs {
		mem := d.mem[ref.ID]
		if mem == nil {
			mem = &v2ClusterMem{}
			d.mem[ref.ID] = mem
		}
		wg.Add(1)
		go func(i int, ref V2ClusterRef, mem *v2ClusterMem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = d.processCluster(ctx, set, ref, now, mem)
		}(i, ref, mem)
	}
	wg.Wait()

	okN, hardN, softN := 0, 0, 0
	for i, r := range results {
		run.APICalls += r.calls
		run.SeriesRead += r.series
		run.Truncated = run.Truncated || r.truncated
		run.PartialResponse = run.PartialResponse || r.partial
		run.RowsWritten += r.rows
		for k, v := range r.diag {
			diag[k] += v
		}
		switch {
		case r.ok:
			okN++
		case r.hard:
			hardN++
		default:
			softN++
		}
		if r.note != "" {
			name := refs[i].Name
			if name == "" {
				name = refs[i].ID
			}
			notes = append(notes, name+": "+r.note)
		}
	}
	run.ScopesOK = okN
	run.Status = v2RunStatus(len(refs), okN, hardN, softN)
	canceled = errors.Is(ctx.Err(), context.Canceled) && parent.Err() != nil
}

// v2RunStatus — SAF: küme sonuçları → koşu durumu. Hepsi tamam → ok; hiçbiri
// tamam değil ve hepsi sert hata → failed; aksi (kısmi okuma, lider kaybı,
// karışık) → partial. Küme yoksa ok (koşu satırı "0 hedef" der).
func v2RunStatus(total, okN, hardN, softN int) string {
	switch {
	case total == 0 || okN == total:
		return RunOK
	case okN == 0 && softN == 0 && hardN > 0:
		return RunFailed
	default:
		return RunPartial
	}
}

func (d *V2Detector) finish(parent context.Context, run *WorkerRun, notes []string, diag map[string]int, canceled bool) {
	run.FinishedAt = d.now()
	if run.FinishedAt.Before(run.StartedAt) {
		run.FinishedAt = run.StartedAt
	}
	run.DurationMs = int(run.FinishedAt.Sub(run.StartedAt) / time.Millisecond)
	if canceled && run.Status != RunOK {
		// Kapanışta yarıda kesilen tik arıza değil (v1 emsali).
		run.Status = RunSkipped
		notes = append([]string{"kapanış — tik yarıda kesildi"}, notes...)
	}
	if run.ScopesTotal == 0 {
		notes = append(notes, "hedef küme yok (etkin, URL'li Remote Cluster)")
	}
	run.Error = v2RunErrorText(notes, diag)
	cp := *run
	d.lastRun.Store(&cp)
	prev := ""
	if p := d.lastStatus.Load(); p != nil {
		prev = *p
	}
	if run.Status != prev && run.Status != RunOK {
		log.Printf("[rollout-v2] tik %s: %s", run.Status, run.Error)
	}
	st := run.Status
	d.lastStatus.Store(&st)
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer rcancel()
	if err := d.store.RecordRolloutWorkerRun(rctx, *run); err != nil {
		log.Printf("[rollout-v2] koşu kaydı: %v", err)
	}
}

// v2RunErrorText — SAF: küme notları (en çok v2RunNotesMax) + teşhis
// sayaçları ("teşhis: kod=n …", kod sırasında) → error kolonu (rune tavanlı).
func v2RunErrorText(notes []string, diag map[string]int) string {
	var parts []string
	if len(notes) > 0 {
		shown := notes
		if len(shown) > v2RunNotesMax {
			shown = append(append([]string(nil), notes[:v2RunNotesMax]...), fmt.Sprintf("… (+%d)", len(notes)-v2RunNotesMax))
		}
		parts = append(parts, strings.Join(shown, " | "))
	}
	if len(diag) > 0 {
		codes := make([]string, 0, len(diag))
		for k, v := range diag {
			if v != 0 {
				codes = append(codes, k)
			}
		}
		sort.Strings(codes)
		kv := make([]string, len(codes))
		for i, k := range codes {
			kv[i] = fmt.Sprintf("%s=%d", k, diag[k])
		}
		if len(kv) > 0 {
			parts = append(parts, "teşhis: "+strings.Join(kv, " "))
		}
	}
	out := strings.Join(parts, " || ")
	if r := []rune(out); len(r) > v2RunErrorMax {
		out = string(r[:v2RunErrorMax-1]) + "…"
	}
	return out
}

func (d *V2Detector) processCluster(ctx context.Context, set V2Resolved, ref V2ClusterRef, now time.Time, mem *v2ClusterMem) v2ClusterResult {
	res := v2ClusterResult{diag: map[string]int{}}
	refresh := mem.loaded && d.degraded != nil && d.degraded()
	if !mem.loaded || refresh {
		states, err := d.store.RolloutV2States(ctx, ref.ID)
		if err != nil {
			res.hard, res.note = true, "önceki durum okunamadı: "+err.Error()
			return res
		}
		events, err := d.store.RolloutV2LatestEvents(ctx, ref.ID)
		if err != nil {
			res.hard, res.note = true, "önceki olaylar okunamadı: "+err.Error()
			return res
		}
		mem.states, mem.events = states, v2LatestPerIncarnation(events)
		if refresh {
			// lockDegraded: başka pod'un yazdıkları okunur (§10.3.1 mint öncesi
			// FINAL okuma); yokluk sayaçları yerel bilgidir, korunur.
			res.diag["state_refreshed_degraded"]++
		} else {
			mem.memory, mem.loaded = V2Memory{}, true
			res.diag["state_rebuilt"]++
		}
	}

	// Kümenin bütün sorguları tek değerlendirme zamanında (v2EvalLag).
	at := now.Add(-v2EvalLag)
	results := map[string]V2QueryResult{}
	for _, q := range V2TickQueries(set.Kinds, ref.NSMatcher) {
		r, err := d.querier.Query(ctx, ref.ID, q.Expr, at)
		res.calls++
		if err != nil {
			res.hard, res.note = true, "KSM sorgusu "+q.Name+": "+err.Error()
			res.diag["fetch_error"]++
			return res
		}
		res.series += r.Series
		res.truncated = res.truncated || r.Truncated
		res.partial = res.partial || r.Partial
		results[q.Name] = r
	}
	snap, fdiag := BuildV2Snapshot(set.Kinds, results)
	for k, v := range fdiag {
		res.diag[k] += v
	}
	if !snap.Complete {
		// §4.10: kısmi okumadan yokluk çıkarılmaz; DetectV2 çağrılmaz, bellek aynen.
		res.diag["skipped_partial"]++
		res.note = "kesik/kısmi KSM okuması — bu tikte fark alınmadı"
		return res
	}

	// V9 doğrulanmadı (tek, tam KSM): bir KSM shard'ı / ikinci KSM düşerse iş
	// yüklerinin bir kısmı "tam" okumada kaybolur; K tik sonra açık olaylar
	// "gone" kapanır, geri dönüşte sahte yeni incarnation + initial basılırdı.
	// O türün yokluk işlemesi sınırlı süre dondurulur; görünenler işlenir.
	seen := v2SnapKeys(snap, ref.ID)
	rebuiltBase := mem.baseline == nil
	base := v2AbsenceBase(mem.baseline, ref.ID, mem.states, mem.events, now)
	var frozenKinds []string
	if kinds := v2MassAbsence(snap, ref.ID, base); len(kinds) > 0 {
		if mem.massSince.IsZero() {
			mem.massSince = now
		}
		if now.Sub(mem.massSince) < v2MassAbsenceMaxHold {
			frozenKinds = kinds
			res.diag["mass_absence_hold"]++
			res.note = "toplu yokluk (" + strings.Join(kinds, ",") + ": son kabul edilen okumadaki iş yüklerinin yarıdan fazlası yok) — yokluk işlemesi donduruldu"
			if rebuiltBase {
				// Taban yeniden kurulumdan (son ~25 sa durumları): yakında
				// açık olaysız silinenler de sayılmış olabilir — teşhis ayrı.
				res.diag["mass_absence_rebuilt_base"]++
				res.note += " (taban yeniden kurulumdan: son ~25 sa durumları; yakında silinenler dahil olabilir)"
			}
		} else {
			// Süre doldu: yokluk gerçektir (toplu silme); taban aşağıda bu
			// okumaya sıfırlanır, sonraki düşüş korumayı yeniden tetikler.
			mem.massSince = time.Time{}
			res.diag["mass_absence_accepted"]++
		}
	} else {
		mem.massSince = time.Time{}
	}

	// fill: bütün adaylar (sıralı), geri çekilmede olmayan ilk v2ImageFillMax.
	mem.ticks++
	if mem.fillTries == nil {
		mem.fillTries = map[V2Key]v2FillTry{}
	}
	primary, fillAll, capped := V2ImageTargets(snap, ref.ID, mem.states, mem.events, v2ImageMaxTargets, len(snap.Workloads))
	fill, deferred := v2PickFill(fillAll, mem.fillTries, mem.ticks, v2ImageFillMax)
	if deferred > 0 {
		res.diag["images_fill_deferred"] += deferred
	}
	iqs, dropped := V2ImageQueries(snap, ref.ID, primary, ref.NSMatcher)
	if capped+dropped > 0 {
		res.diag["images_capped"] += capped + dropped
	}
	primaryN := len(iqs)
	if room := v2ImageMaxQueries - len(iqs); room > 0 && len(fill) > 0 {
		// Artan bütçe: imajı hiç okunmamış durumlar (bootstrap baseline'ı) —
		// ilk START'ın prev_images'i ve change_type'ı eski pod'lar gitmiş olsa
		// da dolsun. Tavanın kestiği dolgu sayılmaz (fırsatçı).
		fq, _ := V2ImageQueries(snap, ref.ID, fill, ref.NSMatcher)
		if len(fq) > room {
			fq = fq[:room]
		}
		iqs = append(iqs, fq...)
	}
	var fillRead []V2Key
	for qi, iq := range iqs {
		r, err := d.querier.Query(ctx, ref.ID, iq.Expr, at)
		res.calls++
		if err != nil {
			if ctx.Err() != nil {
				res.hard, res.note = true, "imaj sorgusu: "+err.Error()
				return res
			}
			res.diag["images_failed"] += len(iq.Workloads)
			continue
		}
		res.series += r.Series
		if !r.usable() {
			res.diag["images_partial"] += len(iq.Workloads)
			continue
		}
		ApplyV2Images(&snap, ref.ID, iq, r)
		res.diag["images_read"] += len(iq.Workloads)
		if qi >= primaryN {
			fillRead = append(fillRead, iq.Workloads...)
		}
	}
	if n := v2NoteFill(snap, ref.ID, fillRead, mem.states, mem.fillTries, mem.ticks); n > 0 {
		res.diag["images_fill_empty"] += n
	}

	out := DetectV2(set, V2Input{ClusterID: ref.ID, Now: now, Snapshot: snap, PrevStates: mem.states, PrevEvents: mem.events, Memory: mem.memory})
	for k, v := range out.Diag.Counts {
		res.diag[k] += v
	}
	for _, e := range out.Diag.Entries {
		if e.Code == "rejected" {
			log.Printf("[rollout-v2] %s %s/%s/%s satırları reddedildi: %s", ref.ID, e.Key.Namespace, e.Key.Kind, e.Key.Workload, e.Detail)
		}
	}
	if out.Diag.Skipped != "" {
		res.note = "dedektör tiki atladı: " + out.Diag.Skipped
		return res
	}
	if len(frozenKinds) > 0 {
		if n := v2FreezeAbsence(&out, seen, frozenKinds, mem.memory); n > 0 {
			res.diag["mass_absence_frozen"] += n
		}
	}
	if len(out.Events)+len(out.States) > 0 {
		// Liderlik yazımdan hemen önce yeniden doğrulanır (v1 emsali). Tik
		// sırasında kaybedilip YENİDEN edinildiyse (resetPending) arada başka
		// pod yazmış olabilir: bellek bayat, yazım atlanır, sonraki tik CH'den kurar.
		if !d.leader() || d.resetPending.Load() {
			mem.loaded = false
			res.lostLeader, res.note = true, "liderlik tik sırasında kaybedildi/yeniden edinildi — yazım atlandı"
			return res
		}
		if len(out.Events) > 0 {
			if err := d.store.RolloutV2WriteEvents(ctx, out.Events); err != nil {
				mem.loaded = false
				res.hard, res.note = true, "rollout_events yazılamadı: "+err.Error()
				return res
			}
		}
		if len(out.States) > 0 {
			if err := d.store.RolloutV2WriteStates(ctx, out.States); err != nil {
				mem.loaded = false
				res.rows = len(out.Events)
				res.hard, res.note = true, "rollout_workload_state yazılamadı (olaylar yazıldı; sonraki tik yeniden türetir): "+err.Error()
				return res
			}
		}
		res.rows = len(out.Events) + len(out.States)
	}
	states, events := V2Carry(mem.states, mem.events, out)
	mem.states, mem.events, mem.memory = states, v2LatestPerIncarnation(events), out.Memory
	// Yeni taban: görünenler + dondurulan ya da bu okumada ailesi büsbütün
	// yok türlerde tabanda olup görünmeyenler (v2NextBaseline).
	mem.baseline = v2NextBaseline(seen, base, frozenKinds, set.Kinds)
	// Dondurma kısmi sonuçtur (yokluk işlenmedi): koşu partial, satırlar yazıldı.
	res.ok = len(frozenKinds) == 0
	return res
}

// v2LatestPerIncarnation — SAF: (iş yükü, incarnation) başına generation'ı
// en büyük olay (eşitlikte büyük version) — çekirdeğin PrevEvents alt
// sınırı; lider belleği bu kadarla sınırlı kalır. Çıktı sıralı.
func v2LatestPerIncarnation(events []V2Event) []V2Event {
	type ik struct {
		k   V2Key
		inc int64
	}
	m := map[ik]V2Event{}
	for _, e := range events {
		k := ik{e.Key(), v2ms(e.IncarnationAt).UnixMilli()}
		cur, ok := m[k]
		if !ok || e.Generation > cur.Generation || (e.Generation == cur.Generation && e.Version >= cur.Version) {
			m[k] = e
		}
	}
	out := make([]V2Event, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	v2SortEvents(out)
	return out
}
