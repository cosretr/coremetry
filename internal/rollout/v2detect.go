package rollout

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// v2detect.go — v0.10.963 — ROLLOUTS v2 P2.1: SAF KSM DEDEKTÖR ÇEKİRDEĞİ
// (docs/rollouts/v2-audit.md §4.3–§4.6, §4.11, §10.3.1–10.3.2, §10.2 yazıcı
// sözleşmesi; operatör kararları 8, 9, 10 — 2026-09-26; "4 onay" 2026-09-27).
//
// DetectV2(ayarlar, girdi) → (rollout_events satırları, rollout_workload_state
// satırları, bellek, teşhis). SAF: I/O yok, saat okunmaz (Now ve KSM örnek
// zamanları argümandır), girdi DEĞİŞTİRİLMEZ, çıktı sırası deterministik.
// Bu committe OKUYUCU / YAZICI / İŞÇİ YOK — P2.2 bağlar (WorkerQuery, lider
// rollout-detector, rollout_worker_runs). v1 dosyalarına dokunulmadı.
//
// ── KURALLAR ──────────────────────────────────────────────────────────────
//
//   İlk-ever koşu: bir TÜR için hiç durum satırı yoksa (yeni küme, yeni
//     etkinleştirilen tür) o türün her iş yükü YALNIZ baseline yazar; olay
//     yok (karar 10; v1'in "haftalık RS'ye sahte completed" dersi).
//   Bootstrap sonrası ilk kez görülen iş yükü / yeni incarnation →
//     change_type='initial' (initialEvents=false ise yalnız baseline).
//   Kısmi okuma (Snapshot.Complete=false: truncated, partial, uyarılı, KSM
//     down): HİÇBİR satır, yokluk sayılmaz, bellek aynen (§4.10).
//
//   NESİL KAPISI (§4.4): generation > max(generation, pending_generation) →
//     pending_generation = G, pending_started_at = o örneğin KSM zamanı
//     (dayanıklı: failover START zamanını değiştirmez). Karar:
//       kanıt (aşağıda) → START; generation = pending, started_at =
//         pending_started_at.
//       kanıt yok + açık olay var → artış olayla birlikte bekler; olay
//         kapanınca emilir ("ride along": rollout ortasında HPA).
//       kanıt yok + paused → bekler, zaman aşımı yok (§4.4 adım 5).
//       kanıt yok + observed ≥ pending + tutma sinyali yok → ÖLÇEK /
//         ANNOTATION: generation ilerler, OLAY YAZILMAZ (adım 4).
//       tutma sinyali (Deployment: status_replicas > updated = eski şablonlu
//         pod var; DaemonSet: updated < desired; StatefulSet YALNIZ
//         update_revision yoksa: status_replicas > updated — spec değil) +
//         daha ince kanıt mümkün değil (RS owner / pod hash ailesi yok, STS
//         o tikte update_revision yok) → hemen GENEL START (new_revision
//         boş; §4.11 DS kuralı).
//       observedGenWaitTicks × tik dolunca: tutma varsa GENEL START
//         (Deployment + RS owner: yeni RS görünmedi → bilinen RS'ye dönüş =
//         rollback) — ama hedef bilinmiyor + RS spec ailesi varsa emilir
//         (bootstrap'ta rollout ortası + HPA: gerçek geri alma Known
//         kanıtıyla zaten görülür); tutma yoksa emilir (gate_timeout).
//   KANIT (revizyon kümesi farkı; known_revisions ÖNCEKİ durumdan):
//       Deployment: bilinmeyen RS adı → rollout/config (a); RS spec ailesi
//         varsa: açık olay YOKKEN hedef dışı TEK etkin (spec>0) bilinen RS →
//         rollback (b: undo; c: GC sonrası aynı pod-template-hash ile
//         yeniden kurulan RS — adı known_revisions'ta); açık olay varken
//         hedef dışı etkin RS'ler boşalan eski RS'lerdir (superseded
//         zincirinde birden çok) → yalnız etkin küme hedef dışı TEK bilinen
//         RS'ye yakınsadı → rollback (rollout ortasında undo).
//       StatefulSet: update_revision ≠ hedef → bilinmiyorsa rollout, biliniyorsa
//         rollback (§4.6).
//       DaemonSet: pod controller_revision_hash kümesi; Deployment ile aynı
//         küme mantığı (hash ailesi yoksa yalnız genel START, rollback görünmez).
//     Hedef bilinmiyorsa (current_revision boş: bootstrap'ta rollout ortası)
//     kanıt olay DEĞİL; iş yükü yerleşince hedef sessizce benimsenir.
//     Artışsız YENİ revizyon (emilen artıştan sonra geç gelen RS, informer
//     gecikmesi) → güncel generation'da olay yoksa geç START (kendi örnek
//     zamanıyla, notlu). Artışsız yeniden etkin bilinen RS olay DEĞİLDİR.
//   change_type: rollback; aksi hâlde imajlar (RevisionImages) aynıysa
//     config, farklı ya da bilinmiyorsa rollout — açık olayda imaj sonradan
//     okununca yeniden hesaplanır.
//   SUCCEEDED (§4.5, kubectl rollout status): observed ≥ generation VE
//     Deployment: updated ≥ spec, status_replicas ≤ updated, available ≥
//     updated, ProgressDeadlineExceeded yok; StatefulSet: ready ≥ replicas,
//     update_revision == current_revision; DaemonSet: updated ≥ desired,
//     available ≥ desired. Ek olarak olayın revizyonu hâlâ HEDEF olmalı
//     (geri alınan rollout "succeeded" sayılmaz).
//   STUCK: Progressing=false (yalnız observed ≥ generation ve START'tan sonraki
//     örnekte — önceki başarısız rollout'tan kalan bayat koşul sayılmaz) →
//     progress_deadline; aksi hâlde örnek − started_at ≥ stuckAfter → timeout.
//     paused iken stuck yok. stuck → succeeded olabilir; stuck_at/stuck_reason
//     tarih olarak kalır.
//   ROLLBACK START'ı en son olayın revizyonunu terk ediyorsa (initial hariç)
//     o olay rolled_back (açık ya da succeeded; finished_at boşsa = geri
//     almanın started_at'i). Başka START açık olayı superseded yapar
//     (finished_at = yeni started_at). İş yükünde en fazla bir açık olay.
//   YENİ INCARNATION (§4.3, karar 9): _created varsa ve ≥ incarnation_at+1dk
//     → yeni; ≥ K ardışık TAM okumada yoktu → yeni; generation <
//     max(generation, pending) → yeni (ama _created varsa ve incarnation_at'ten
//     SONRA değilse — aynı ya da daha eski nesne — bu bir replika/dedup
//     sapmasıdır: tik atlanır, generation_regressed; SONRAysa 1 dk içinde
//     yeniden kurulmuş yeni nesnedir → yeni, "created").
//     incarnation_at = _created, yoksa ilk tam okumanın örnek zamanı dakikaya
//     kesik; eskisinden sonra değilse eski+1 dk (anahtar çakışmasın). Eski
//     incarnation'ın açık olayı superseded (notlu). K'ya ulaşan yokluk açık
//     olayı da kapatır. Bir türün BÜTÜN ailesi okumada yoksa (KSM yeniden
//     başladı) yokluk sayılmaz (family_absent).
//   known_revisions: önceki + mevcut revizyonlar (+ hedef), ilk görülme
//     sırası; knownRevisionsMax aşılınca EN ESKİ, mevcut olmayan ve hedef
//     olmayan atılır — mevcut RS asla atılmaz (atılsa her tik "yeni RS" olurdu).
//   Durum satırı yalnız değişince + günde bir dokunuş (last_seen_at TTL
//     çapası); olay satırı yalnız değişince. version = max(tik ns, önceki+1),
//     updated_at = Now (SSE kursörü). Her satır ValidateV2*'dan geçer; bir
//     satır düşerse iş yükünün BÜTÜN satırları düşer (yarım durum yok).
//
// ── ÇAĞIRAN SÖZLEŞMESİ (P2.2) ─────────────────────────────────────────────
//
//   - Küme başına bir çağrı. Snapshot.Complete yalnız bütün seriler
//     truncated/partial/uyarı OLMADAN geldiyse true.
//   - PrevStates: rollout_workload_state FINAL WHERE cluster_id = ?.
//     PrevEvents: en azından her (iş yükü, incarnation) için generation'ı en
//     büyük rollout_events satırı (fazlası zararsız). state.incarnation_at'e
//     SÜZÜLMEZ: durumu olmayan iş yüklerinin ve durumdakinden YENİ
//     incarnation'ların olayları da gelmeli (kayıp durum yeniden kurulumu).
//   - PrevStates/PrevEvents okuması (keyset sayfalı) hata verir ya da yarım
//     kalırsa DetectV2 çağrılmaz ya da PrevIncomplete=true verilir; koşu
//     'partial' işaretlenir. Boş PrevStates "bu küme için hiç durum yok"
//     demektir (ilk-ever) — okuma hatasının yerine geçemez (tüm
//     incarnation'lar yeniden basılır, açık olaylar superseded olur, yarım
//     okumada sahte 'initial' yazılır).
//   - Memory: yalnız bellekte (DDL'de kolonu yok); failover'da boş başlar.
//   - Yazım sırası: önce rollout_events, sonra rollout_workload_state — ikisi
//     arasında düşerse sonraki tik aynı olay anahtarını yeniden türetir:
//     bekleyen START ve geç START start_reused ile (hedef ilerler), mint
//     (initial / yeni incarnation) durumun olaydan yeniden kurulmasıyla
//     (lost_state; incarnation_at ve started_at korunur).
//   - DEFAULT 0 zamanları V2CHTime ile bağlanır; INSERT'ten önce ValidateV2*
//     (epoch sentinel'i geçerlidir). FINAL okumada bu kolonlar (succeeded_at,
//     stuck_at, finished_at, pending_started_at) 1970 döner: DetectV2
//     onları kendisi Go sıfırına eşler (V2FromCHTime) — okuyucu eşlemez.
//   - BİLİNEN SINIRLAR (P2.2 / operatör kararı): lease'ten uzun bir dedektör
//     kesintisinde yapılan rollout, kesinti sonrası İLK örnekle damgalanır
//     (gerçek başlangıç KSM'de yok); boşluk sinyali (rollout_worker_runs)
//     girdiye eklenip bu olaylar işaretlenebilir — bu committe YOK. paused
//     geçen süre stuckAfter'a sayılır (resume zamanı kolonu yok).
//
// ── VARSAYIMLAR — §11 K/D DOĞRULAMADAN P2.2 CANLI KSM'YE BAĞLANMAZ ────────
//
//   V1 (K1.D/K1.S/K1.DS) kube_<kind>_metadata_generation VE
//      _status_observed_generation her hedef kümede her üç tür için var.
//      Kimlik ve kapı bunlara dayanır; observed yoksa her artış kapı süresini
//      bekler.
//   V2 (K2.1) Etiket adları: namespace + deployment | statefulset | daemonset;
//      kube_replicaset_owner'da owner_kind, owner_name (owner_is_controller
//      değeri K2.3 ile görülmeli; çağıran yalnız controller sahipliğini
//      almalı); STS revizyonları `revision` etiketinde.
//   V2b (K1) kube_statefulset_status_{current,update}_revision var (§4.2:
//      CMO'da denylist dışı). Yoksa STS START GENELDİR (new_revision boş) ve
//      yalnız observed yetişmiş + status_replicas > updated iken; ROLLBACK
//      görünmez; tek kapı tikinde biten rollout scale_only emilir.
//   V3 (K1.R, K3) kube_replicaset_owner{owner_kind="Deployment"} ve
//      kube_replicaset_spec_replicas var ve WorkerLimits içinde kesilmeden
//      gelir. Spec ailesi yoksa (minimal profil) yeniden-etkin RS rollback'i
//      yalnız kapı süresi sonunda genel START ile görülür — BİLİNEN SINIR:
//      aynı yol, bootstrap'ta rollout ortası + HPA artışını da genel
//      rollback gösterir (ek durum olmadan ayrılamaz).
//   V4 (K1) _created serileri CMO'da YOK (denylist) → incarnation ilk tam
//      okumadan; RS generation/observed_generation YOK ve BİLEREK
//      kullanılmaz (Presence.RSGeneration yalnız teşhis).
//   V5 (K0.5) CMO scrape 60 s, tik 30 s (karar 8): SampleAt =
//      max by (namespace, <kind>) (timestamp(kube_<kind>_metadata_generation{<L>="<V>"}));
//      timestamp() HAM seçiciye, toplamanın İÇİNDE uygulanır —
//      timestamp(max by (...) (...)) değerlendirme zamanını döndürür, KSM
//      scrape zamanını değil: SampleAt her tik ilerler, bayat koşul koruması
//      (SampleAt > started_at), stuckAfter, kapı süresi ve incarnation_at
//      tik saatine düşer. Aynı örneğin yeniden okunması SampleAt'i
//      ilerletmez. stuckAfter ve kapı süresi ÖRNEK zamanıyla ölçülür
//      (Coremetry saati değil). §11 doğrular: dedup=true querier'da
//      (Prometheus motoru ya da Thanos promql-engine) bir scrape aralığı
//      içinde ~30 s arayla iki okuma AYNI değeri döndürür (K0.6 biçimi, iki kez).
//   V6 (K0.4, H0.2) `max by (namespace, <kind>)` + dedup=true sonrası iş
//      yükü başına TEK seri (replika etiketi — ör. prometheus_replica —
//      querier'da düşer). Dedup replika değiştirip generation'ı geri
//      gösterirse ve _created yoksa yeni incarnation sanılır (audit kuralı
//      aynen; gerekirse iki ardışık okumada teyit — ayrı karar).
//   V7 (K2.3) Progressing status="false" yalnız ProgressDeadlineExceeded ile
//      görülür; `reason` etiketi gerekmez (KSM < v2.17'de yok).
//   V8 (K2.4) DaemonSet pod'larında label_controller_revision_hash var (CMO
//      pods=[*] allowlist); yoksa DS rollback görünmez, new_revision boş.
//      Çağıran hash'leri kube_pod_owner{owner_kind="DaemonSet"} ile bağlar.
//   V9 (K0.6, K0.2) KSM taze (< 90 s) ve tek KSM; up==0 ya da ikinci KSM
//      durumunda çağıran Complete=false verir.
//   V10 (K1.H, K5) HPA payı: kapı gürültüyü olaysız emer ama her ölçek artışı
//      en çok iki durum yazımıdır; K5 oranı yazım hacmini ölçer.
//   V11 status_replicas = sonlanmayan pod sayısı (sonlanan pod dahil değil):
//      saf ölçekte status == updated kalır, tutma sinyali yanlış tetiklenmez
//      (Deployment ve revizyon serisi olmayan StatefulSet; STS OrderedReady
//      ölçek artışında yeni pod'lar update revizyonuyla gelir → status == updated).
//   V12 (D) DeploymentConfig bu çekirdeğin DIŞINDA (kinds üç tür; karar 12
//      bekliyor): DC iş yükleri olay almaz.
//   V13 Namespace filtresi (nsMatcher) sabit: değişirse düşen iş yükleri K
//      okuma sonra yeniden görünüşte yeni incarnation + initial olur.
//   V14 (K2.2) RevisionImages: kube_pod_container_info{image} ⋈ kube_pod_owner
//      → revizyon; yalnız açık olaylı iş yükleri için okunur (§4.11).
//
// ── TEŞHİS KODLARI (Diag.Counts; P2.2 rollout_worker_runs'a özetler) ──────
//
//   invalid_now skipped_partial skipped_prev_partial ignored_kind invalid_observation
//   duplicate_observation lost_state
//   baseline initial_event new_incarnation generation_regressed pending start
//   start_reused late_evidence evidence_without_bump reactivation_without_bump
//   generic_start ride_along paused_pending waiting scale_only
//   scale_only_not_ignored gate_timeout adopt succeeded stuck absent gone
//   family_absent ambiguous_revision no_revision known_over_bound rejected

// V2ReplicaSet — Deployment'a ait bir RS (kube_replicaset_owner ⋈ spec).
type V2ReplicaSet struct {
	Name         string
	SpecReplicas uint32 // Presence.RSSpec false ise yok sayılır
}

// V2Observation — bir TAM okumada bir iş yükü.
//
// Replika alanlarının tür eşlemesi:
//
//	Deployment : spec_replicas, status_replicas, status_replicas_updated,
//	             status_replicas_ready, status_replicas_available
//	StatefulSet: replicas, status_replicas, status_replicas_updated,
//	             status_replicas_ready, status_replicas_available
//	DaemonSet  : Spec = status_desired_number_scheduled, Updated =
//	             status_updated_number_scheduled, Available =
//	             status_number_available (Status/Ready kullanılmaz)
type V2Observation struct {
	Kind      string
	Namespace string
	Name      string
	// SampleAt — max by (namespace,<kind>) (timestamp(kube_<kind>_metadata_generation)):
	// KSM scrape zamanı; timestamp() toplamanın İÇİNDE (V5).
	SampleAt time.Time
	// CreatedAt — kube_<kind>_created; sıfır = seri yok (CMO denylist'i).
	CreatedAt          time.Time
	Generation         uint64
	ObservedGeneration uint64
	SpecReplicas       uint32
	StatusReplicas     uint32
	UpdatedReplicas    uint32
	ReadyReplicas      uint32
	AvailableReplicas  uint32
	// Paused — kube_deployment_spec_paused == 1.
	Paused bool
	// ProgressDeadlineExceeded — kube_deployment_status_condition{condition="Progressing",status="false"} == 1.
	ProgressDeadlineExceeded bool
	// ReplicaSets — Deployment: owner_kind="Deployment", owner_name=Name.
	ReplicaSets []V2ReplicaSet
	// CurrentRevision / UpdateRevision — StatefulSet status_{current,update}_revision{revision}.
	CurrentRevision string
	UpdateRevision  string
	// RevisionHashes — DaemonSet pod'larındaki controller_revision_hash kümesi.
	RevisionHashes []string
	// RevisionImages — revizyon → imajlar (nil = bu tik okunmadı).
	RevisionImages map[string][]string
}

// V2Presence — küme düzeyinde seri ailesi varlığı (CMO denylist / profil).
type V2Presence struct {
	RSOwner              bool // kube_replicaset_owner
	RSSpec               bool // kube_replicaset_spec_replicas
	ProgressingCondition bool // kube_deployment_status_condition{condition="Progressing"}
	PodRevisionHash      bool // kube_pod_labels{label_controller_revision_hash}
	Created              bool // kube_<kind>_created (bilgi; kararı gözlemdeki CreatedAt verir)
	RSGeneration         bool // kube_replicaset_metadata_generation (bilgi; BİLEREK kullanılmaz)
}

// V2Snapshot — bir kümenin bir tikteki KSM okuması.
type V2Snapshot struct {
	Complete  bool // false: truncated / partial / uyarı / KSM down → fark alınmaz
	Presence  V2Presence
	Workloads []V2Observation
}

// V2Memory — tikler arası YALNIZ bellekte tutulan sayaçlar (DDL'de kolonu
// yok; failover'da boş başlar → yokluk kuralı yeniden sayar, nesil gerilemesi
// ve _created kuralları dayanıklı kalır).
type V2Memory struct {
	Absent map[V2Key]int // ardışık TAM okumada yokluk
}

// V2Input — DetectV2 girdisi.
type V2Input struct {
	ClusterID  string    // Remote Cluster EffectiveID()
	Now        time.Time // tik zamanı: yalnız updated_at + version
	Snapshot   V2Snapshot
	PrevStates []V2WorkloadState
	PrevEvents []V2Event
	// PrevIncomplete — v0.10.963 — P2.1: true = rollout_workload_state /
	// rollout_events okuması hatalı ya da yarım (keyset sayfası düştü) →
	// tik atlanır. Sıfır değeri güvenli: false = okuma tam.
	PrevIncomplete bool
	Memory         V2Memory
}

// V2DiagEntry — tek bir anormallik (sınırlı liste).
type V2DiagEntry struct {
	Key    V2Key
	Code   string
	Detail string
}

// V2Diag — koşu teşhisi.
type V2Diag struct {
	Skipped        string // "" | "partial" | "prev_partial" | "invalid_now"
	Counts         map[string]int
	Entries        []V2DiagEntry
	EntriesDropped int
}

// V2Output — DetectV2 çıktısı.
type V2Output struct {
	Events []V2Event
	States []V2WorkloadState
	Memory V2Memory
	Diag   V2Diag
}

const (
	v2DiagMaxEntries = 100
	v2TouchEvery     = 24 * time.Hour
)

// DetectV2 — v0.10.963 — SAF KSM dedektör çekirdeği (dosya başlığı).
func DetectV2(set V2Resolved, in V2Input) V2Output {
	d := &v2Detector{set: v2NormalizeSet(set), in: in, now: v2ms(in.Now), pres: in.Snapshot.Presence,
		diag: V2Diag{Counts: map[string]int{}}, rows: map[V2Key]*v2Rows{}}
	d.nowNano = uint64(d.now.UnixNano())
	d.enabled = map[string]bool{}
	for _, k := range d.set.Kinds {
		if c, ok := canonicalV2Kind(k); ok {
			d.enabled[c] = true
		}
	}
	return d.run()
}

type v2Detector struct {
	set     V2Resolved
	in      V2Input
	now     time.Time
	nowNano uint64
	readAt  time.Time
	pres    V2Presence
	enabled map[string]bool
	diag    V2Diag
	rows    map[V2Key]*v2Rows
}

type v2Rows struct {
	events []V2Event
	state  *V2WorkloadState
}

// v2NormalizeSet — sıfır değerli vidalar varsayılana (ResolvedV2 kelepçeli
// değer verir; bu yalnız elle kurulmuş V2Resolved için güvenlik ağı).
func v2NormalizeSet(set V2Resolved) V2Resolved {
	def := DefaultSettings().ResolvedV2()
	if set.DetectorInterval <= 0 {
		set.DetectorInterval = def.DetectorInterval
	}
	if set.StuckAfter <= 0 {
		set.StuckAfter = def.StuckAfter
	}
	if set.ObservedGenWaitTicks <= 0 {
		set.ObservedGenWaitTicks = def.ObservedGenWaitTicks
	}
	if set.IncarnationAbsentTicks <= 0 {
		set.IncarnationAbsentTicks = def.IncarnationAbsentTicks
	}
	if set.KnownRevisionsMax <= 0 {
		set.KnownRevisionsMax = def.KnownRevisionsMax
	}
	if len(set.Kinds) == 0 {
		set.Kinds = def.Kinds
	}
	return set
}

func (d *v2Detector) run() V2Output {
	if !d.now.After(v2Epoch) {
		// Now olmadan updated_at / version damgalanamaz (sıfır zamanın
		// UnixNano'su taşar): bütün tik atlanır, bellek aynen.
		d.diag.Skipped = "invalid_now"
		d.count("invalid_now")
		return V2Output{Memory: V2Memory{Absent: v2CopyAbsent(d.in.Memory.Absent)}, Diag: d.finishDiag()}
	}
	if !d.in.Snapshot.Complete {
		d.diag.Skipped = "partial"
		d.count("skipped_partial")
		return V2Output{Memory: V2Memory{Absent: v2CopyAbsent(d.in.Memory.Absent)}, Diag: d.finishDiag()}
	}
	if d.in.PrevIncomplete {
		// v0.10.963 — P2.1: durum/olay okuması hatalı ya da yarım. Çekirdek
		// "durum yok" ile "okunamadı"yı ayıramaz: boş PrevStates her türü
		// ilk-ever sanar (incarnation'lar yeniden basılır, açık olaylar
		// superseded olur, yarım okumada sahte 'initial' yazılır) → tik atlanır.
		d.diag.Skipped = "prev_partial"
		d.count("skipped_prev_partial")
		return V2Output{Memory: V2Memory{Absent: v2CopyAbsent(d.in.Memory.Absent)}, Diag: d.finishDiag()}
	}

	states := map[V2Key]V2WorkloadState{}
	kindHasState := map[string]bool{}
	for _, raw := range d.in.PrevStates {
		if raw.ClusterID != d.in.ClusterID {
			continue
		}
		s := v2NormState(raw)
		k := s.Key()
		if cur, ok := states[k]; ok && !v2StateNewer(s, cur) {
			continue
		}
		states[k] = s
		kindHasState[s.WorkloadKind] = true
	}

	evIdx := map[V2Key]map[int64]V2Event{}
	for _, raw := range d.in.PrevEvents {
		if raw.ClusterID != d.in.ClusterID {
			continue
		}
		e := v2NormEvent(raw)
		k := e.Key()
		m := evIdx[k]
		if m == nil {
			m = map[int64]V2Event{}
			evIdx[k] = m
		}
		inc := e.IncarnationAt.UnixMilli()
		if cur, ok := m[inc]; ok && (cur.Generation > e.Generation || (cur.Generation == e.Generation && cur.Version >= e.Version)) {
			continue
		}
		m[inc] = e
	}

	obs := map[V2Key]V2Observation{}
	seen := map[V2Key]bool{}
	kindSeen := map[string]bool{}
	for _, raw := range d.in.Snapshot.Workloads {
		kind, ok := canonicalV2Kind(raw.Kind)
		if !ok || !d.enabled[kind] {
			d.count("ignored_kind")
			continue
		}
		o := v2NormObs(raw, kind)
		k := V2Key{d.in.ClusterID, o.Namespace, kind, o.Name}
		if o.Namespace == "" || o.Name == "" || o.Generation == 0 || !o.SampleAt.After(v2Epoch) {
			d.count("invalid_observation")
			d.entry(k, "invalid_observation", fmt.Sprintf("generation=%d sample=%s", o.Generation, o.SampleAt.Format(time.RFC3339)))
			if o.Namespace != "" && o.Name != "" {
				seen[k] = true // geçersiz gözlem yokluk DEĞİLDİR
			}
			continue
		}
		seen[k] = true
		kindSeen[kind] = true
		if prev, dup := obs[k]; dup {
			d.count("duplicate_observation")
			if !v2ObsWins(o, prev) {
				continue
			}
		}
		obs[k] = o
		if o.SampleAt.After(d.readAt) {
			d.readAt = o.SampleAt
		}
	}
	if d.readAt.IsZero() {
		d.readAt = d.now
	}

	for _, k := range v2SortedKeys(obs) {
		o := obs[k]
		st, has := states[k]
		if lost := v2LostMint(evIdx[k], has, st.IncarnationAt.UnixMilli()); lost != nil {
			// v0.10.963 — P2.1: olay yazıldı, durum yazılamadı (mint yolu) →
			// durum olaydan yeniden kurulur; aynı incarnation anahtarı ve
			// started_at korunur (yoksa ikinci 'initial' + hayalet incarnation).
			d.count("lost_state")
			d.entry(k, "lost_state", fmt.Sprintf("incarnation %s g%d olaydan yeniden kuruldu", lost.IncarnationAt.Format(time.RFC3339), lost.Generation))
			st, has = v2StateFromEvent(*lost), true
		}
		if !has {
			d.mint(k, o, nil, !kindHasState[k.Kind])
			continue
		}
		d.observe(k, o, st, evIdx[k])
	}

	newMem := map[V2Key]int{}
	famNoted := map[string]bool{}
	for _, k := range v2SortedKeys(states) {
		if seen[k] || !d.enabled[k.Kind] {
			continue
		}
		prev := d.in.Memory.Absent[k]
		if !kindSeen[k.Kind] {
			if !famNoted[k.Kind] {
				famNoted[k.Kind] = true
				d.count("family_absent")
				d.entry(V2Key{ClusterID: d.in.ClusterID, Kind: k.Kind}, "family_absent", "türün hiçbir iş yükü okunmadı; yokluk sayılmaz")
			}
			if prev > 0 {
				newMem[k] = prev
			}
			continue
		}
		c := prev + 1
		newMem[k] = c
		d.count("absent")
		if c != d.set.IncarnationAbsentTicks {
			continue
		}
		d.count("gone")
		st := states[k]
		if last, ok := evIdx[k][st.IncarnationAt.UnixMilli()]; ok {
			if closed, ok := v2CloseOpen(last, d.readAt, fmt.Sprintf("iş yükü %d ardışık tam okumada yok", c)); ok {
				d.putEvent(k, &last, closed)
			}
		}
	}
	if len(newMem) == 0 {
		newMem = nil
	}
	return d.output(newMem)
}

// observe — durumu olan iş yükü: incarnation kararı, sonra kapı.
func (d *v2Detector) observe(k V2Key, o V2Observation, st V2WorkloadState, byInc map[int64]V2Event) {
	curInc := st.IncarnationAt.UnixMilli()
	incs := make([]int64, 0, len(byInc))
	for inc := range byInc {
		incs = append(incs, inc)
	}
	sort.Slice(incs, func(i, j int) bool { return incs[i] < incs[j] })
	for _, inc := range incs {
		if inc >= curInc {
			continue
		}
		e := byInc[inc]
		if closed, ok := v2CloseOpen(e, o.SampleAt, "eski incarnation'ın açık olayı kapandı"); ok {
			d.putEvent(k, &e, closed)
		}
	}
	var last *V2Event
	if e, ok := byInc[curInc]; ok {
		last = &e
	}
	reason, skip := d.incarnationChange(k, o, st)
	if skip {
		return
	}
	if reason != "" {
		if last != nil {
			if closed, ok := v2CloseOpen(*last, o.SampleAt, "yeni incarnation ("+reason+")"); ok {
				d.putEvent(k, last, closed)
			}
		}
		d.count("new_incarnation")
		d.mint(k, o, &st, false)
		return
	}
	d.advance(k, o, st, last)
}

func (d *v2Detector) incarnationChange(k V2Key, o V2Observation, st V2WorkloadState) (reason string, skip bool) {
	if !o.CreatedAt.IsZero() && !o.CreatedAt.Before(st.IncarnationAt.Add(time.Minute)) {
		return "created", false
	}
	if d.in.Memory.Absent[k] >= d.set.IncarnationAbsentTicks {
		return "absent", false
	}
	if hi := v2MaxU(st.Generation, st.PendingGeneration); o.Generation < hi {
		if !o.CreatedAt.IsZero() && o.CreatedAt.After(st.IncarnationAt) {
			// v0.10.963 — P2.1: yeni nesne 1 dk toleransı içinde yeniden
			// kuruldu, nesil sıfırlandı (kubectl replace --force, helm
			// uninstall/install) → yeni incarnation; atlamak tespiti dondururdu.
			return "created", false
		}
		if !o.CreatedAt.IsZero() { // aynı ya da daha eski _created: replika/dedup sapması
			d.count("generation_regressed")
			d.entry(k, "generation_regressed", fmt.Sprintf("%d < %d ama _created incarnation_at'ten sonra değil: replika/dedup sapması, tik atlandı", o.Generation, hi))
			return "", true
		}
		return "generation", false
	}
	return "", false
}

// mint — yeni iş yükü ya da yeni incarnation: baseline ya da initial olay.
func (d *v2Detector) mint(k V2Key, o V2Observation, prev *V2WorkloadState, bootstrap bool) {
	inc := o.CreatedAt
	if inc.IsZero() {
		inc = o.SampleAt.Truncate(time.Minute)
	}
	if prev != nil && !inc.After(prev.IncarnationAt) {
		inc = prev.IncarnationAt.Truncate(time.Minute).Add(time.Minute)
	}
	target := d.settledTarget(o)
	present := v2Present(o)
	ns := V2WorkloadState{ClusterID: k.ClusterID, Namespace: k.Namespace, WorkloadKind: k.Kind, Workload: k.Workload,
		IncarnationAt: inc, Generation: o.Generation, ObservedGeneration: o.ObservedGeneration, CurrentRevision: target,
		Images: v2ImagesOf(o, target), FirstSeenAt: o.SampleAt, LastSeenAt: o.SampleAt}
	ns.KnownRevisions = d.boundKnown(k, v2Union(nil, present, target), present, target)
	if bootstrap || !d.set.InitialEvents {
		d.count("baseline")
	} else {
		e := V2Event{ClusterID: k.ClusterID, Namespace: k.Namespace, WorkloadKind: k.Kind, Workload: k.Workload,
			IncarnationAt: inc, Generation: o.Generation, StartedAt: o.SampleAt, Status: V2StatusProgressing,
			ChangeType: V2ChangeInitial, NewRevision: target, Images: v2ImagesOf(o, target)}
		d.evaluate(&e, o, ns)
		if v2IsOpen(e.Status) {
			ns.OpenGeneration = e.Generation
		}
		d.count("initial_event")
		d.putEvent(k, nil, e)
	}
	d.putState(k, prev, ns, o.SampleAt)
}

// v2Work — advance'in iş yükü başına çalışma kopyası.
type v2Work struct {
	k       V2Key
	o       V2Observation
	st      V2WorkloadState // önceki (değişmez)
	ns      V2WorkloadState // yeni
	last    *V2Event        // incarnation'ın en son olayı (orijinal)
	lastUp  *V2Event        // last'ın güncel kopyası
	fresh   *V2Event        // bu tikte açılan olay
	started bool
	// holdUnknown — v0.10.982 — artışsız yeni revizyon (evidence_without_bump):
	// bilinmeyen mevcut revizyonlar known_revisions'a KATILMAZ.
	holdUnknown bool
}

// open — iş yükünün açık olayı (bu tikte açılan ya da süren), yoksa nil.
func (w *v2Work) open() *V2Event {
	if w.fresh != nil {
		return w.fresh
	}
	if w.lastUp != nil && v2IsOpen(w.lastUp.Status) {
		return w.lastUp
	}
	return nil
}

// advance — aynı incarnation: nesil kapısı, kanıt, olay durumu, bellek.
func (d *v2Detector) advance(k V2Key, o V2Observation, st V2WorkloadState, last *V2Event) {
	w := &v2Work{k: k, o: o, st: st, ns: v2NormState(st), last: last}
	if last != nil {
		c := v2NormEvent(*last)
		w.lastUp = &c
	}
	if o.Generation > v2MaxU(st.Generation, st.PendingGeneration) {
		w.ns.PendingGeneration, w.ns.PendingStartedAt = o.Generation, o.SampleAt
		d.count("pending")
	}
	w.ns.ObservedGeneration = o.ObservedGeneration

	ev := d.evidence(k, o, st, w.open())
	pending := w.ns.PendingGeneration != 0
	switch {
	case ev.kind == v2EvNone:
	case pending && ev.kind == v2EvKnown:
		d.start(w, w.ns.PendingGeneration, w.ns.PendingStartedAt, ev.target, V2ChangeRollback, "")
	case pending:
		d.start(w, w.ns.PendingGeneration, w.ns.PendingStartedAt, ev.target, V2ChangeRollout, "")
	case st.CurrentRevision == "":
		// hedef bilinmiyordu: aşağıda yerleşince sessizce benimsenir
	case ev.kind == v2EvNew && last != nil && last.Generation == w.ns.Generation && last.NewRevision == ev.target:
		// v0.10.963 — P2.1: geç START'ın olayı yazıldı, durumu yazılamadı →
		// aynı anahtar yeniden türer (start_reused; hedef ilerler). Yalnız ilk
		// yeniden deneme tikinde tutar: o tik revizyonu known'a katar.
		d.start(w, w.ns.Generation, last.StartedAt, ev.target, V2ChangeRollout, "")
	case ev.kind == v2EvNew && (last == nil || last.Generation < w.ns.Generation):
		d.count("late_evidence")
		d.start(w, w.ns.Generation, o.SampleAt, ev.target, V2ChangeRollout, "geç kanıt: revizyon nesil artışından sonra göründü")
	case ev.kind == v2EvNew:
		// v0.10.982 — P2.2 incelemesi: revizyon, nesil artışından ÖNCE
		// görünebilir (tikin sorguları farklı scrape'leri okur ya da KSM
		// informer kayması). Burada known'a katılsaydı artış geldiği tik yeni
		// revizyon "bilinen" sayılır → sahte ROLLBACK + önceki başarılı
		// rollout rolled_back. Bilinmeyen kalır; artış gelince rollout/config.
		d.count("evidence_without_bump")
		d.entry(k, "evidence_without_bump", ev.target)
		w.holdUnknown = true
	default:
		d.count("reactivation_without_bump")
		d.entry(k, "reactivation_without_bump", ev.target)
	}

	if !w.started && w.ns.CurrentRevision == "" {
		if t := d.settledTarget(o); t != "" {
			w.ns.CurrentRevision = t
			d.count("adopt")
		}
	}
	if cur := w.open(); cur != nil {
		d.evaluate(cur, o, w.ns)
	}
	if !w.started && w.ns.PendingGeneration != 0 {
		d.decidePending(w)
		if w.fresh != nil {
			d.evaluate(w.fresh, o, w.ns)
		}
	}

	present := v2Present(o)
	if w.holdUnknown {
		present = v2OnlyKnown(present, st.KnownRevisions)
	}
	w.ns.KnownRevisions = d.boundKnown(k, v2Union(st.KnownRevisions, present, w.ns.CurrentRevision), present, w.ns.CurrentRevision)
	if im := v2ImagesOf(o, w.ns.CurrentRevision); len(im) > 0 || w.ns.CurrentRevision != st.CurrentRevision {
		w.ns.Images = im
	}
	w.ns.OpenGeneration = 0
	if cur := w.open(); cur != nil {
		w.ns.OpenGeneration = cur.Generation
	}

	if w.lastUp != nil {
		d.putEvent(k, w.last, *w.lastUp)
	}
	if w.fresh != nil {
		d.putEvent(k, nil, *w.fresh)
	}
	d.putState(k, &w.st, w.ns, o.SampleAt)
}

// decidePending — kanıtsız bekleyen artış (dosya başlığı "NESİL KAPISI").
func (d *v2Detector) decidePending(w *v2Work) {
	o, ns := w.o, &w.ns
	ready := o.ObservedGeneration >= ns.PendingGeneration
	hold := v2Hold(o)
	timeout := o.SampleAt.Sub(ns.PendingStartedAt) >= time.Duration(d.set.ObservedGenWaitTicks)*d.set.DetectorInterval
	switch {
	case w.open() != nil:
		d.count("ride_along")
	case o.Paused:
		d.count("paused_pending")
	case ready && !hold:
		d.absorb(w, "scale_only")
	case ready && hold && !d.finerEvidencePossible(o):
		d.count("generic_start")
		d.start(w, ns.PendingGeneration, ns.PendingStartedAt, "", V2ChangeRollout, "")
	case timeout && ready && hold && !(o.Kind == V2KindDeployment && d.pres.RSSpec && w.st.CurrentRevision == ""):
		// v0.10.963 — P2.1: hedef bilinmiyorsa (bootstrap'ta rollout ortası)
		// RS spec ailesi varken bilinen RS kanıtı olay değildir (§4.4 adım
		// 3b/c): gerçek geri alma Known kanıtıyla zaten görülür; artış aşağıda
		// gate_timeout ile emilir, yerleşince hedef sessizce benimsenir. Spec
		// ailesi yoksa (V3 minimal profil) genel START korunur.
		ct := V2ChangeRollout
		if o.Kind == V2KindDeployment && d.pres.RSOwner {
			ct = V2ChangeRollback // yeni RS görünmedi → hedef bilinen bir RS
		}
		d.count("generic_start")
		d.start(w, ns.PendingGeneration, ns.PendingStartedAt, "", ct, "zaman aşımı: şablon değişti ama hedef revizyon görülemedi")
	case timeout:
		d.absorb(w, "gate_timeout")
	default:
		d.count("waiting")
	}
}

func (d *v2Detector) absorb(w *v2Work, code string) {
	w.ns.Generation = w.ns.PendingGeneration
	w.ns.PendingGeneration, w.ns.PendingStartedAt = 0, time.Time{}
	d.count(code)
	if code == "scale_only" && !d.set.IgnoreScale {
		d.count("scale_only_not_ignored")
		d.entry(w.k, "scale_only_not_ignored", "ignoreScale=false: §10.3.1 sözlüğünde ölçek change_type'ı yok, satır yazılmadı")
	}
}

// start — START: önceki olayı işaretler (rolled_back / superseded), yeni
// olayı açar, durumu hedefe taşır.
func (d *v2Detector) start(w *v2Work, gen uint64, at time.Time, target, ct, note string) {
	w.started = true
	d.count("start")
	oldRev := w.st.CurrentRevision
	if l := w.lastUp; l != nil && l.Generation == gen {
		d.count("start_reused") // olay yazıldı, durum yazılamadı: aynı anahtar yeniden türedi
	} else {
		if l != nil {
			fin := v2MaxTime(at, l.StartedAt)
			switch {
			case ct == V2ChangeRollback && l.ChangeType != V2ChangeInitial && l.NewRevision != "" && l.NewRevision == oldRev &&
				(v2IsOpen(l.Status) || l.Status == V2StatusSucceeded):
				l.Status = V2StatusRolledBack
				if l.FinishedAt.IsZero() {
					l.FinishedAt = fin
				}
				l.Note = v2AppendNote(l.Note, fmt.Sprintf("geri alındı: nesil %d", gen))
			case v2IsOpen(l.Status):
				l.Status, l.FinishedAt = V2StatusSuperseded, fin
				l.Note = v2AppendNote(l.Note, fmt.Sprintf("yerini aldı: nesil %d", gen))
			}
		}
		prev := v2ImagesOf(w.o, oldRev)
		if len(prev) == 0 {
			prev = v2CloneStrings(w.st.Images)
		}
		e := V2Event{ClusterID: w.k.ClusterID, Namespace: w.k.Namespace, WorkloadKind: w.k.Kind, Workload: w.k.Workload,
			IncarnationAt: w.st.IncarnationAt, Generation: gen, StartedAt: at, Status: V2StatusProgressing, ChangeType: ct,
			NewRevision: target, OldRevision: oldRev, Images: v2ImagesOf(w.o, target), PrevImages: prev, Note: note}
		if ct != V2ChangeRollback {
			e.ChangeType = v2ChangeByImages(e.Images, e.PrevImages)
		}
		w.fresh = &e
	}
	w.ns.Generation = v2MaxU(w.ns.Generation, gen)
	w.ns.PendingGeneration, w.ns.PendingStartedAt = 0, time.Time{}
	w.ns.CurrentRevision = target
}

// evaluate — açık olayın ilerlemesi: SUCCEEDED / STUCK (dosya başlığı).
func (d *v2Detector) evaluate(e *V2Event, o V2Observation, ns V2WorkloadState) {
	if !v2IsOpen(e.Status) {
		return
	}
	e.ObservedGeneration = o.ObservedGeneration
	e.SpecReplicas, e.UpdatedReplicas, e.AvailableReplicas = o.SpecReplicas, o.UpdatedReplicas, o.AvailableReplicas
	if e.NewRevision == "" && ns.CurrentRevision != "" {
		e.NewRevision = ns.CurrentRevision
	}
	if len(e.Images) == 0 {
		e.Images = v2ImagesOf(o, e.NewRevision)
	}
	if len(e.PrevImages) == 0 {
		e.PrevImages = v2ImagesOf(o, e.OldRevision)
	}
	if e.ChangeType == V2ChangeRollout || e.ChangeType == V2ChangeConfig {
		e.ChangeType = v2ChangeByImages(e.Images, e.PrevImages)
	}
	if (e.NewRevision == "" || e.NewRevision == ns.CurrentRevision) && v2Succeeded(o, d.pres) {
		at := v2MaxTime(o.SampleAt, e.StartedAt)
		e.Status, e.SucceededAt, e.FinishedAt = V2StatusSucceeded, at, at
		d.count("succeeded")
		return
	}
	if e.Status != V2StatusProgressing || o.Paused {
		return
	}
	switch {
	case o.Kind == V2KindDeployment && d.pres.ProgressingCondition && o.ProgressDeadlineExceeded &&
		o.ObservedGeneration >= o.Generation && o.SampleAt.After(e.StartedAt):
		e.Status, e.StuckReason, e.StuckAt = V2StatusStuck, V2StuckProgressDeadline, o.SampleAt
		d.count("stuck")
	case o.SampleAt.Sub(e.StartedAt) >= d.set.StuckAfter:
		e.Status, e.StuckReason, e.StuckAt = V2StatusStuck, V2StuckTimeout, o.SampleAt
		d.count("stuck")
	}
}

// v2Succeeded — §4.5 (kubectl rollout status eşdeğeri); hedef denetimi çağıranda.
func v2Succeeded(o V2Observation, pres V2Presence) bool {
	if o.ObservedGeneration < o.Generation {
		return false
	}
	switch o.Kind {
	case V2KindDeployment:
		return o.UpdatedReplicas >= o.SpecReplicas && o.StatusReplicas <= o.UpdatedReplicas &&
			o.AvailableReplicas >= o.UpdatedReplicas && !(pres.ProgressingCondition && o.ProgressDeadlineExceeded)
	case V2KindStatefulSet:
		if o.ReadyReplicas < o.SpecReplicas {
			return false
		}
		if o.UpdateRevision == "" && o.CurrentRevision == "" {
			return o.UpdatedReplicas >= o.SpecReplicas // revizyon serisi yok: güncellenen replika yedeği
		}
		return o.UpdateRevision != "" && o.UpdateRevision == o.CurrentRevision
	case V2KindDaemonSet:
		return o.UpdatedReplicas >= o.SpecReplicas && o.AvailableReplicas >= o.SpecReplicas
	}
	return false
}

// v2Hold — "eski şablonlu pod var" (kapıyı emmekten alıkoyar).
func v2Hold(o V2Observation) bool {
	switch o.Kind {
	case V2KindDeployment:
		return o.StatusReplicas > o.UpdatedReplicas
	case V2KindStatefulSet:
		// v0.10.963 — P2.1: revizyon serisi yoksa (V2b) status_replicas >
		// updated = eski revizyonlu pod var (V11 STS için de). spec
		// KULLANILMAZ: OrderedReady ölçek artışında updated < spec olur ve
		// sahte START doğururdu. Seri varsa kanıt revizyondan gelir.
		return o.UpdateRevision == "" && o.StatusReplicas > o.UpdatedReplicas
	case V2KindDaemonSet:
		return o.UpdatedReplicas < o.SpecReplicas
	}
	return false
}

// finerEvidencePossible — tutma sinyali varken revizyon kanıtı beklenebilir
// mi. StatefulSet gözlem başına (v0.10.963 — P2.1): Presence bayrağı yerine
// o tikteki update_revision — P2.2 bayrağı unutsa davranış sessizce değişmez.
func (d *v2Detector) finerEvidencePossible(o V2Observation) bool {
	switch o.Kind {
	case V2KindStatefulSet:
		return o.UpdateRevision != ""
	case V2KindDeployment:
		return d.pres.RSOwner
	case V2KindDaemonSet:
		return d.pres.PodRevisionHash
	}
	return true
}

// settledTarget — iş yükü yerleşikse (observed ≥ generation, tutma yok) tek
// hedef revizyon; belirsizse "". StatefulSet için update_revision her zaman hedeftir.
func (d *v2Detector) settledTarget(o V2Observation) string {
	switch o.Kind {
	case V2KindStatefulSet:
		return o.UpdateRevision
	case V2KindDeployment:
		if !d.pres.RSOwner || o.ObservedGeneration < o.Generation || v2Hold(o) {
			return ""
		}
		names := v2Present(o)
		if !d.pres.RSSpec {
			if len(names) == 1 {
				return names[0]
			}
			return ""
		}
		var active []string
		for _, rs := range o.ReplicaSets {
			if rs.SpecReplicas > 0 {
				active = append(active, rs.Name)
			}
		}
		if len(active) == 1 {
			return active[0]
		}
		if len(active) == 0 && len(names) == 1 {
			return names[0]
		}
	case V2KindDaemonSet:
		if d.pres.PodRevisionHash && o.ObservedGeneration >= o.Generation && !v2Hold(o) && len(o.RevisionHashes) == 1 {
			return o.RevisionHashes[0]
		}
	}
	return ""
}

type v2EvKind int

const (
	v2EvNone  v2EvKind = iota
	v2EvNew            // bilinmeyen revizyon
	v2EvKnown          // bilinen revizyon hedef oldu
)

type v2Evidence struct {
	kind   v2EvKind
	target string
}

// evidence — revizyon kümesi farkı (dosya başlığı "KANIT").
func (d *v2Detector) evidence(k V2Key, o V2Observation, st V2WorkloadState, open *V2Event) v2Evidence {
	switch o.Kind {
	case V2KindStatefulSet:
		u := o.UpdateRevision
		if u == "" {
			d.count("no_revision")
			return v2Evidence{}
		}
		if u == st.CurrentRevision {
			return v2Evidence{}
		}
		if st.CurrentRevision != "" && v2Has(st.KnownRevisions, u) {
			return v2Evidence{v2EvKnown, u}
		}
		return v2Evidence{v2EvNew, u}
	case V2KindDeployment:
		if !d.pres.RSOwner {
			return v2Evidence{}
		}
		weight := map[string]uint32{}
		var active []string
		for _, rs := range o.ReplicaSets {
			weight[rs.Name] = rs.SpecReplicas
			if rs.SpecReplicas > 0 {
				active = append(active, rs.Name)
			}
		}
		return d.setEvidence(k, v2Present(o), active, d.pres.RSSpec, st, open, weight)
	case V2KindDaemonSet:
		if !d.pres.PodRevisionHash || len(o.RevisionHashes) == 0 {
			return v2Evidence{}
		}
		return d.setEvidence(k, o.RevisionHashes, o.RevisionHashes, true, st, open, nil)
	}
	return v2Evidence{}
}

func (d *v2Detector) setEvidence(k V2Key, present, active []string, activeKnown bool, st V2WorkloadState, open *V2Event, weight map[string]uint32) v2Evidence {
	var unknown []string
	for _, r := range present {
		if !v2Has(st.KnownRevisions, r) {
			unknown = append(unknown, r)
		}
	}
	if len(unknown) > 0 {
		sort.Slice(unknown, func(i, j int) bool {
			if weight[unknown[i]] != weight[unknown[j]] {
				return weight[unknown[i]] > weight[unknown[j]]
			}
			return unknown[i] > unknown[j]
		})
		return v2Evidence{v2EvNew, unknown[0]}
	}
	if !activeKnown || st.CurrentRevision == "" {
		return v2Evidence{}
	}
	if open != nil {
		// v0.10.963 — P2.1: açık olay varken hedef dışı etkin RS'ler BOŞALIR
		// (superseded zincirinde eski-eski RS dahil: a→b→c'de a hâlâ spec>0);
		// §4.4(b) "spec 0 → >0" burada görülemez → yeniden etkinleşme kanıtı
		// yalnız yakınsamadır (etkin küme hedef dışı TEK bilinen RS).
		if len(active) == 1 && active[0] != st.CurrentRevision {
			return v2Evidence{v2EvKnown, active[0]} // rollout ortasında undo: eski RS'ye yakınsadı
		}
		return v2Evidence{}
	}
	var cands []string
	for _, r := range active {
		if r != st.CurrentRevision {
			cands = append(cands, r)
		}
	}
	switch {
	case len(cands) == 1:
		return v2Evidence{v2EvKnown, cands[0]}
	case len(cands) > 1:
		d.count("ambiguous_revision")
		d.entry(k, "ambiguous_revision", strings.Join(cands, ","))
	}
	return v2Evidence{}
}

// boundKnown — ilk görülme sırası; en eski, mevcut olmayan ve hedef olmayan atılır.
func (d *v2Detector) boundKnown(k V2Key, known, present []string, target string) []string {
	keep := map[string]bool{target: true}
	for _, p := range present {
		keep[p] = true
	}
	out := known
	for len(out) > d.set.KnownRevisionsMax {
		idx := -1
		for i, r := range out {
			if !keep[r] {
				idx = i
				break
			}
		}
		if idx < 0 {
			d.count("known_over_bound")
			d.entry(k, "known_over_bound", fmt.Sprintf("%d mevcut revizyon > knownRevisionsMax %d", len(out), d.set.KnownRevisionsMax))
			break
		}
		out = append(out[:idx:idx], out[idx+1:]...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ── Satır üretimi ────────────────────────────────────────────────────────

func (d *v2Detector) rowsFor(k V2Key) *v2Rows {
	r := d.rows[k]
	if r == nil {
		r = &v2Rows{}
		d.rows[k] = r
	}
	return r
}

// putEvent — değiştiyse damgalar (updated_at = Now, version tekdüze) ve ekler.
func (d *v2Detector) putEvent(k V2Key, orig *V2Event, cur V2Event) {
	cur = v2NormEvent(cur)
	if orig != nil && v2EventSame(*orig, cur) {
		return
	}
	cur.UpdatedAt, cur.Version = d.now, d.nowNano
	if orig != nil && orig.Version >= cur.Version {
		cur.Version = orig.Version + 1
	}
	r := d.rowsFor(k)
	for i := range r.events {
		if v2IDOf(r.events[i]) == v2IDOf(cur) {
			r.events[i] = cur
			return
		}
	}
	r.events = append(r.events, cur)
}

// putState — değiştiyse ya da son dokunuştan bir gün geçtiyse yazar.
func (d *v2Detector) putState(k V2Key, orig *V2WorkloadState, ns V2WorkloadState, sampleAt time.Time) {
	ns = v2NormState(ns)
	if orig != nil && v2StateSame(*orig, ns) && sampleAt.Sub(orig.LastSeenAt) < v2TouchEvery {
		return
	}
	ns.LastSeenAt = sampleAt
	ns.Version = d.nowNano
	if orig != nil {
		ns.LastSeenAt = v2MaxTime(orig.LastSeenAt, sampleAt)
		if orig.Version >= ns.Version {
			ns.Version = orig.Version + 1
		}
	}
	d.rowsFor(k).state = &ns
}

// output — iş yükü başına atomik doğrulama, deterministik sıra.
func (d *v2Detector) output(mem map[V2Key]int) V2Output {
	out := V2Output{Memory: V2Memory{Absent: mem}}
	for _, k := range v2SortedKeys(d.rows) {
		r := d.rows[k]
		var errs []string
		for _, e := range r.events {
			if err := ValidateV2Event(e); err != nil {
				errs = append(errs, fmt.Sprintf("olay g%d: %s", e.Generation, strings.ReplaceAll(err.Error(), "\n", "; ")))
			}
		}
		if r.state != nil {
			if err := ValidateV2State(*r.state); err != nil {
				errs = append(errs, "durum: "+strings.ReplaceAll(err.Error(), "\n", "; "))
			}
		}
		if len(errs) > 0 {
			d.count("rejected")
			d.entry(k, "rejected", strings.Join(errs, " | "))
			continue
		}
		out.Events = append(out.Events, r.events...)
		if r.state != nil {
			out.States = append(out.States, *r.state)
		}
	}
	v2SortEvents(out.Events)
	v2SortStates(out.States)
	out.Diag = d.finishDiag()
	return out
}

func (d *v2Detector) count(code string) { d.diag.Counts[code]++ }

func (d *v2Detector) entry(k V2Key, code, detail string) {
	d.diag.Entries = append(d.diag.Entries, V2DiagEntry{Key: k, Code: code, Detail: detail})
}

func (d *v2Detector) finishDiag() V2Diag {
	sort.SliceStable(d.diag.Entries, func(i, j int) bool {
		a, b := d.diag.Entries[i], d.diag.Entries[j]
		if a.Key != b.Key {
			return v2KeyLess(a.Key, b.Key)
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Detail < b.Detail
	})
	if n := len(d.diag.Entries); n > v2DiagMaxEntries {
		d.diag.EntriesDropped = n - v2DiagMaxEntries
		d.diag.Entries = d.diag.Entries[:v2DiagMaxEntries]
	}
	return d.diag
}

// ── Saf yardımcılar ───────────────────────────────────────────────────────

func v2IsOpen(status string) bool { return status == V2StatusProgressing || status == V2StatusStuck }

// v2CloseOpen — açık olayı superseded kapatır (kaybolan iş yükü, yeni incarnation).
func v2CloseOpen(e V2Event, at time.Time, note string) (V2Event, bool) {
	if !v2IsOpen(e.Status) {
		return e, false
	}
	e = v2NormEvent(e)
	e.Status, e.FinishedAt = V2StatusSuperseded, v2MaxTime(at, e.StartedAt)
	e.Note = v2AppendNote(e.Note, note)
	return e, true
}

// v2LostMint — v0.10.963 — P2.1: durum yazımı kaybolmuş mint'in olayı:
// incarnation'ı dayanıklı durumdakinden YENİ (durum yoksa herhangi) olan en
// büyük incarnation'lı olay; yoksa nil. Gerekçe: durum satırı olayla AYNI
// tikte yazılır ve daha uzun yaşar (TTL 400 gün, olay 180), incarnation'lar
// yalnız ileri gider → böyle bir olay ancak yazılamamış bir durumdan kalır.
func v2LostMint(byInc map[int64]V2Event, has bool, curInc int64) *V2Event {
	var best *V2Event
	for inc, e := range byInc {
		if has && inc <= curInc {
			continue
		}
		if best == nil || inc > best.IncarnationAt.UnixMilli() {
			c := e
			best = &c
		}
	}
	return best
}

// v2StateFromEvent — v0.10.963 — P2.1: kaybolan durumu olaydan kurar.
// known_revisions yalnız olayın kesin bildiği revizyonlar (eski + yeni):
// o tikte mevcut RS'leri katmak, boşlukta gelen gerçek bir rollout'u
// "bilinen RS" (sahte rollback) gösterirdi. Version 0 ve last_seen_at sıfır
// → putState bu tik mutlaka yazar.
func v2StateFromEvent(e V2Event) V2WorkloadState {
	e = v2NormEvent(e)
	s := V2WorkloadState{ClusterID: e.ClusterID, Namespace: e.Namespace, WorkloadKind: e.WorkloadKind, Workload: e.Workload,
		IncarnationAt: e.IncarnationAt, Generation: e.Generation, ObservedGeneration: e.ObservedGeneration,
		CurrentRevision: e.NewRevision, Images: v2CloneStrings(e.Images), FirstSeenAt: e.StartedAt}
	for _, r := range []string{e.OldRevision, e.NewRevision} {
		if r != "" && !v2Has(s.KnownRevisions, r) {
			s.KnownRevisions = append(s.KnownRevisions, r)
		}
	}
	if v2IsOpen(e.Status) {
		s.OpenGeneration = e.Generation
	}
	return s
}

func v2AppendNote(note, add string) string {
	if note == "" {
		return add
	}
	if strings.Contains(note, add) {
		return note
	}
	return note + "; " + add
}

func v2ChangeByImages(images, prev []string) string {
	if len(images) > 0 && len(prev) > 0 && reflect.DeepEqual(images, prev) {
		return V2ChangeConfig
	}
	return V2ChangeRollout
}

// v2Present — gözlemdeki revizyonlar (sıralı, tekil).
func v2Present(o V2Observation) []string {
	switch o.Kind {
	case V2KindDeployment:
		out := make([]string, 0, len(o.ReplicaSets))
		for _, rs := range o.ReplicaSets {
			out = append(out, rs.Name)
		}
		return out
	case V2KindStatefulSet:
		return v2SortedSet([]string{o.CurrentRevision, o.UpdateRevision})
	case V2KindDaemonSet:
		return v2CloneStrings(o.RevisionHashes)
	}
	return nil
}

// v2OnlyKnown — v0.10.982 — present'in known'da olan öğeleri (sıra korunur).
func v2OnlyKnown(present, known []string) []string {
	var out []string
	for _, p := range present {
		if v2Has(known, p) {
			out = append(out, p)
		}
	}
	return out
}

// v2Union — known (sırası korunur) + yeni mevcutlar (sıralı) + hedef.
func v2Union(known, present []string, target string) []string {
	out := v2CloneStrings(known)
	for _, p := range present {
		if !v2Has(out, p) {
			out = append(out, p)
		}
	}
	if target != "" && !v2Has(out, target) {
		out = append(out, target)
	}
	return out
}

func v2ImagesOf(o V2Observation, rev string) []string {
	if rev == "" || o.RevisionImages == nil {
		return nil
	}
	return v2CloneStrings(o.RevisionImages[rev])
}

func v2Has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func v2SortedSet(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			m[s] = true
		}
	}
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func v2CloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func v2ms(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return time.UnixMilli(t.UnixMilli()).UTC()
}

func v2MaxU(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func v2MaxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func v2CopyAbsent(m map[V2Key]int) map[V2Key]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[V2Key]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func v2SortedKeys[T any](m map[V2Key]T) []V2Key {
	keys := make([]V2Key, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return v2KeyLess(keys[i], keys[j]) })
	return keys
}

// v2NormObs — kopya: ms zamanlar, tekil/sıralı RS ve hash, normalize imajlar.
func v2NormObs(o V2Observation, kind string) V2Observation {
	o.Kind = kind
	o.Namespace, o.Name = strings.TrimSpace(o.Namespace), strings.TrimSpace(o.Name)
	o.SampleAt, o.CreatedAt = v2ms(o.SampleAt), v2ms(o.CreatedAt)
	o.CurrentRevision, o.UpdateRevision = strings.TrimSpace(o.CurrentRevision), strings.TrimSpace(o.UpdateRevision)
	spec := map[string]uint32{}
	for _, rs := range o.ReplicaSets {
		n := strings.TrimSpace(rs.Name)
		if n == "" {
			continue
		}
		if cur, ok := spec[n]; !ok || rs.SpecReplicas > cur {
			spec[n] = rs.SpecReplicas
		}
	}
	o.ReplicaSets = nil
	for _, n := range v2SortedSet(v2MapKeys(spec)) {
		o.ReplicaSets = append(o.ReplicaSets, V2ReplicaSet{Name: n, SpecReplicas: spec[n]})
	}
	o.RevisionHashes = v2SortedSet(o.RevisionHashes)
	if o.RevisionImages != nil {
		im := make(map[string][]string, len(o.RevisionImages))
		for rev, list := range o.RevisionImages {
			if rev = strings.TrimSpace(rev); rev != "" {
				im[rev] = v2SortedSet(list)
			}
		}
		o.RevisionImages = im
	}
	return o
}

func v2MapKeys(m map[string]uint32) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// v2ObsWins — yinelenen gözlemde (dedup kaçağı) hangisi kalır: büyük
// generation, sonra yeni örnek, sonra deterministik parmak izi.
func v2ObsWins(a, b V2Observation) bool {
	if a.Generation != b.Generation {
		return a.Generation > b.Generation
	}
	if !a.SampleAt.Equal(b.SampleAt) {
		return a.SampleAt.After(b.SampleAt)
	}
	return fmt.Sprintf("%#v", a) > fmt.Sprintf("%#v", b)
}

func v2StateNewer(a, b V2WorkloadState) bool {
	if a.Version != b.Version {
		return a.Version > b.Version
	}
	if !a.LastSeenAt.Equal(b.LastSeenAt) {
		return a.LastSeenAt.After(b.LastSeenAt)
	}
	return fmt.Sprintf("%#v", a) > fmt.Sprintf("%#v", b)
}

// v2NormEvent / v2NormState — ms kesim + (v0.10.963 — P2.1) DEFAULT 0
// kolonlarında CH'nin 1970 sentinel'i → Go sıfırı (V2FromCHTime). Eşleme
// v2ms'in İÇİNDE değil: v2ms TTL çapalarını da normalize eder, onlar epoch'ta
// reddedilmeye devam etmeli.
func v2NormEvent(e V2Event) V2Event {
	e.IncarnationAt, e.StartedAt = v2ms(e.IncarnationAt), v2ms(e.StartedAt)
	e.SucceededAt, e.StuckAt = v2ms(V2FromCHTime(e.SucceededAt)), v2ms(V2FromCHTime(e.StuckAt))
	e.FinishedAt, e.UpdatedAt = v2ms(V2FromCHTime(e.FinishedAt)), v2ms(e.UpdatedAt)
	e.Images, e.PrevImages = v2CloneStrings(e.Images), v2CloneStrings(e.PrevImages)
	return e
}

func v2NormState(s V2WorkloadState) V2WorkloadState {
	s.IncarnationAt, s.PendingStartedAt = v2ms(s.IncarnationAt), v2ms(V2FromCHTime(s.PendingStartedAt))
	s.FirstSeenAt, s.LastSeenAt = v2ms(s.FirstSeenAt), v2ms(s.LastSeenAt)
	s.KnownRevisions, s.Images = v2CloneStrings(s.KnownRevisions), v2CloneStrings(s.Images)
	return s
}

// v2EventSame — updated_at ve version dışındaki bütün kolonlar aynı mı.
func v2EventSame(a, b V2Event) bool {
	a, b = v2NormEvent(a), v2NormEvent(b)
	a.UpdatedAt, b.UpdatedAt, a.Version, b.Version = time.Time{}, time.Time{}, 0, 0
	return reflect.DeepEqual(a, b)
}

// v2StateSame — last_seen_at ve version dışındaki bütün kolonlar aynı mı.
func v2StateSame(a, b V2WorkloadState) bool {
	a, b = v2NormState(a), v2NormState(b)
	a.LastSeenAt, b.LastSeenAt, a.Version, b.Version = time.Time{}, time.Time{}, 0, 0
	return reflect.DeepEqual(a, b)
}
