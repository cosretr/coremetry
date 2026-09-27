package rollout

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// v2model.go — v0.10.963 — Rollouts v2 P2.1 veri modeli (docs/rollouts/v2-audit.md
// §10.3.1–10.3.2; yazıcı sözleşmesi §10.2, v0.10.959 incelemesi).
//
// V2Event ve V2WorkloadState, rollout_events / rollout_workload_state DDL'ini
// (internal/chstore/rollout_v2_schema.go ≡ migrations/0015) kolon kolon,
// SIRASIYLA aynalar; `ch` etiketi kolon adıdır. v2model_test.go 0015'i okuyup
// ad + sıra + tip eşliğini pinler: DDL'e kolon eklenirse bu dosya da değişir.
//
// YAZICI SÖZLEŞMESİ (E2, §10.2): CH'de DEFAULT'suz kolon ZORUNLU DEĞİLDİR —
// atlanan ya da sıfır TTL çapası 1970'e yazılır ve satır ilk TTL
// birleşmesinde SESSİZCE düşer. Bu yüzden her satır, batch'e girmeden ÖNCE
// ValidateV2Event / ValidateV2State'ten geçer: TTL çapası (started_at,
// last_seen_at) ve anahtar zamanı (incarnation_at) sıfır-olmayan ve
// epoch-SONRASI olmalı. DetectV2 bunu kendi çıktısına uygular (iş yükü başına
// atomik red); P2.2 yazıcısı INSERT'ten hemen önce yeniden çağırır.
//
// DEFAULT 0 kolonları (succeeded_at, stuck_at, finished_at,
// pending_started_at): Go sıfırı "ayarlanmadı" demektir; bağlarken V2CHTime
// ile epoch'a çevrilir (Go sıfırı '0001-01-01' bağlanır, CH DateTime64 onu
// 1900'e kırpar → okuyucunun `= 0` sentinel'i tutmaz). Okumada (FINAL)
// aynı kolonlar 1970 döner: V2FromCHTime tersidir ve DetectV2 önceki
// satırlara kendisi uygular (v0.10.963 — P2.1 inceleme R2 / P21-1).
//
// Saf: I/O yok, saat yok.

// v0.10.963 — rollout_events sözlükleri (§10.3.1). Ölçek / yalnız-annotation
// change_type'ı YOKTUR: öyle bir satır yazılmaz.
const (
	V2StatusProgressing = "progressing"
	V2StatusSucceeded   = "succeeded"
	V2StatusStuck       = "stuck"
	V2StatusRolledBack  = "rolled_back"
	V2StatusSuperseded  = "superseded"

	V2ChangeRollout  = "rollout"
	V2ChangeConfig   = "config"
	V2ChangeRollback = "rollback"
	V2ChangeInitial  = "initial"

	V2StuckProgressDeadline = "progress_deadline"
	V2StuckTimeout          = "timeout"

	V2KindDeployment  = "Deployment"
	V2KindStatefulSet = "StatefulSet"
	V2KindDaemonSet   = "DaemonSet"
)

// V2Key — iş yükü kimliği (rollout_workload_state ORDER BY'ı).
type V2Key struct{ ClusterID, Namespace, Kind, Workload string }

// V2Event — rollout_events satırı (§10.3.1). Anahtar: (cluster_id, namespace,
// workload_kind, workload, incarnation_at, generation). Tam satır yazılır.
type V2Event struct {
	ClusterID          string    `ch:"cluster_id"`
	Namespace          string    `ch:"namespace"`
	WorkloadKind       string    `ch:"workload_kind"`
	Workload           string    `ch:"workload"`
	IncarnationAt      time.Time `ch:"incarnation_at"`
	Generation         uint64    `ch:"generation"`
	StartedAt          time.Time `ch:"started_at"` // TTL çapası
	Status             string    `ch:"status"`
	ChangeType         string    `ch:"change_type"`
	ObservedGeneration uint64    `ch:"observed_generation"`
	SpecReplicas       uint32    `ch:"spec_replicas"`
	UpdatedReplicas    uint32    `ch:"updated_replicas"`
	AvailableReplicas  uint32    `ch:"available_replicas"`
	NewRevision        string    `ch:"new_revision"`
	OldRevision        string    `ch:"old_revision"`
	Images             []string  `ch:"images"`
	PrevImages         []string  `ch:"prev_images"`
	VersionTag         string    `ch:"version_tag"` // P5.1 doldurur; P2.1 taşır
	StuckReason        string    `ch:"stuck_reason"`
	SucceededAt        time.Time `ch:"succeeded_at"`
	StuckAt            time.Time `ch:"stuck_at"`
	FinishedAt         time.Time `ch:"finished_at"`
	Note               string    `ch:"note"`
	UpdatedAt          time.Time `ch:"updated_at"` // SSE tail kursörü = tik zamanı
	Version            uint64    `ch:"version"`
}

// Key — olayın iş yükü kimliği.
func (e V2Event) Key() V2Key { return V2Key{e.ClusterID, e.Namespace, e.WorkloadKind, e.Workload} }

// V2WorkloadState — rollout_workload_state satırı (§10.3.2): dedektörün
// kalıcı belleği. current_revision = dedektörün bildiği GÜNCEL HEDEF revizyon
// (Deployment: en yeni RS; StatefulSet: update_revision; DaemonSet: en yeni
// controller_revision_hash; "" = henüz belirlenemedi).
type V2WorkloadState struct {
	ClusterID          string    `ch:"cluster_id"`
	Namespace          string    `ch:"namespace"`
	WorkloadKind       string    `ch:"workload_kind"`
	Workload           string    `ch:"workload"`
	IncarnationAt      time.Time `ch:"incarnation_at"`
	Generation         uint64    `ch:"generation"`
	ObservedGeneration uint64    `ch:"observed_generation"`
	PendingGeneration  uint64    `ch:"pending_generation"`
	PendingStartedAt   time.Time `ch:"pending_started_at"`
	CurrentRevision    string    `ch:"current_revision"`
	KnownRevisions     []string  `ch:"known_revisions"`
	Images             []string  `ch:"images"`
	OpenGeneration     uint64    `ch:"open_generation"`
	FirstSeenAt        time.Time `ch:"first_seen_at"`
	LastSeenAt         time.Time `ch:"last_seen_at"` // TTL çapası
	Version            uint64    `ch:"version"`
}

// Key — durumun iş yükü kimliği.
func (s V2WorkloadState) Key() V2Key {
	return V2Key{s.ClusterID, s.Namespace, s.WorkloadKind, s.Workload}
}

var v2Epoch = time.Unix(0, 0).UTC()

// V2CHTime — v0.10.963 — DEFAULT 0 DateTime64 kolonuna bağlanacak değer:
// sıfır → Unix epoch (sentinel), aksi hâlde aynen.
func V2CHTime(t time.Time) time.Time {
	if t.IsZero() {
		return v2Epoch
	}
	return t
}

// V2FromCHTime — v0.10.963 — P2.1: V2CHTime'ın tersi. CH DEFAULT 0
// DateTime64 okumada 1970 döner (FINAL okuma dahil; sürücü kolon/sunucu
// dilimiyle verebilir → anlık karşılaştırma); "ayarlanmadı" = Go sıfırı.
// Epoch ÖNCESİ (Go sıfırının 1900'e kırpılmış hâli) de ayarlanmadı sayılır.
// DetectV2 önceki satırları kendisi eşler (v2NormEvent / v2NormState);
// okuyucunun ayrıca zeroIfEpoch uygulaması gerekmez. YALNIZ DEFAULT 0
// kolonları için: TTL çapaları (started_at, incarnation_at, last_seen_at,
// first_seen_at) eşlenmez, epoch çapa reddedilmeye devam eder.
func V2FromCHTime(t time.Time) time.Time {
	if !t.IsZero() && !t.After(v2Epoch) {
		return time.Time{}
	}
	return t
}

// v2Unset — DEFAULT 0 zamanı ayarlanmamış mı (Go sıfırı ya da epoch sentinel'i).
func v2Unset(t time.Time) bool { return t.IsZero() || t.Equal(v2Epoch) }

// v2Anchor — zorunlu zaman: sıfır değil ve epoch'tan SONRA.
func v2Anchor(col string, t time.Time) error {
	if t.IsZero() {
		return fmt.Errorf("%s sıfır", col)
	}
	if !t.After(v2Epoch) {
		return fmt.Errorf("%s epoch ya da öncesi (%s)", col, t.UTC().Format(time.RFC3339))
	}
	return nil
}

// v2Optional — DEFAULT 0 zamanı: sıfır ve epoch sentinel'i (V2CHTime'ın
// kendi ürettiği değer; v0.10.963 — P2.1: bağlandıktan sonra doğrulama da
// geçer) serbest; doluysa epoch'tan sonra.
func v2Optional(col string, t time.Time) error {
	if v2Unset(t) || t.After(v2Epoch) {
		return nil
	}
	return fmt.Errorf("%s epoch öncesi (%s)", col, t.UTC().Format(time.RFC3339))
}

func v2InVocab(v string, vocab ...string) bool {
	for _, x := range vocab {
		if v == x {
			return true
		}
	}
	return false
}

func v2KeyErrs(cluster, ns, kind, workload string) []error {
	var errs []error
	if cluster == "" {
		errs = append(errs, errors.New("cluster_id boş"))
	}
	if ns == "" {
		errs = append(errs, errors.New("namespace boş"))
	}
	if workload == "" {
		errs = append(errs, errors.New("workload boş"))
	}
	if !v2InVocab(kind, V2KindDeployment, V2KindStatefulSet, V2KindDaemonSet) {
		errs = append(errs, fmt.Errorf("workload_kind %q sözlük dışı", kind))
	}
	return errs
}

// ValidateV2Event — v0.10.963 — rollout_events yazıcı sözleşmesi (SAF).
// nil = satır yazılabilir.
func ValidateV2Event(e V2Event) error {
	errs := v2KeyErrs(e.ClusterID, e.Namespace, e.WorkloadKind, e.Workload)
	for _, err := range []error{
		v2Anchor("started_at", e.StartedAt),
		v2Anchor("incarnation_at", e.IncarnationAt),
		v2Anchor("updated_at", e.UpdatedAt),
		v2Optional("succeeded_at", e.SucceededAt),
		v2Optional("stuck_at", e.StuckAt),
		v2Optional("finished_at", e.FinishedAt),
	} {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if !v2InVocab(e.Status, V2StatusProgressing, V2StatusSucceeded, V2StatusStuck, V2StatusRolledBack, V2StatusSuperseded) {
		errs = append(errs, fmt.Errorf("status %q sözlük dışı", e.Status))
	}
	if !v2InVocab(e.ChangeType, V2ChangeRollout, V2ChangeConfig, V2ChangeRollback, V2ChangeInitial) {
		errs = append(errs, fmt.Errorf("change_type %q sözlük dışı", e.ChangeType))
	}
	if !v2InVocab(e.StuckReason, "", V2StuckProgressDeadline, V2StuckTimeout) {
		errs = append(errs, fmt.Errorf("stuck_reason %q sözlük dışı", e.StuckReason))
	}
	if e.Generation == 0 {
		errs = append(errs, errors.New("generation 0"))
	}
	if e.Version == 0 {
		errs = append(errs, errors.New("version 0 (açık istemci version'ı şart)"))
	}
	return errors.Join(errs...)
}

// ValidateV2State — v0.10.963 — rollout_workload_state yazıcı sözleşmesi (SAF).
func ValidateV2State(s V2WorkloadState) error {
	errs := v2KeyErrs(s.ClusterID, s.Namespace, s.WorkloadKind, s.Workload)
	for _, err := range []error{
		v2Anchor("last_seen_at", s.LastSeenAt),
		v2Anchor("first_seen_at", s.FirstSeenAt),
		v2Anchor("incarnation_at", s.IncarnationAt),
		v2Optional("pending_started_at", s.PendingStartedAt),
	} {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if s.PendingGeneration != 0 && v2Unset(s.PendingStartedAt) {
		errs = append(errs, errors.New("pending_generation var ama pending_started_at sıfır (failover'da START zamanı kaybolur)"))
	}
	if s.Generation == 0 {
		errs = append(errs, errors.New("generation 0"))
	}
	if s.Version == 0 {
		errs = append(errs, errors.New("version 0 (açık istemci version'ı şart)"))
	}
	return errors.Join(errs...)
}

// v2EvID — bir iş yükü içinde olay kimliği (incarnation ms, generation).
type v2EvID struct {
	inc int64
	gen uint64
}

func v2IDOf(e V2Event) v2EvID { return v2EvID{e.IncarnationAt.UnixMilli(), e.Generation} }

// V2Carry — v0.10.963 — SAF: önceki durum + DetectV2 çıktısı → bir sonraki
// tikin PrevStates / PrevEvents'i (RMT anlamı: aynı anahtarda büyük version
// kazanır; eşitlikte çıktı). P2.2 lideri tikler arasında bellekte tutabilir;
// failover'da aynı kümeyi CH'den FINAL okur. Çıktı deterministik sıralı.
func V2Carry(states []V2WorkloadState, events []V2Event, out V2Output) ([]V2WorkloadState, []V2Event) {
	sm := map[V2Key]V2WorkloadState{}
	for _, s := range states {
		if cur, ok := sm[s.Key()]; !ok || s.Version >= cur.Version {
			sm[s.Key()] = s
		}
	}
	for _, s := range out.States {
		if cur, ok := sm[s.Key()]; !ok || s.Version >= cur.Version {
			sm[s.Key()] = s
		}
	}
	type eid struct {
		k  V2Key
		id v2EvID
	}
	em := map[eid]V2Event{}
	for _, list := range [][]V2Event{events, out.Events} {
		for _, e := range list {
			k := eid{e.Key(), v2IDOf(e)}
			if cur, ok := em[k]; !ok || e.Version >= cur.Version {
				em[k] = e
			}
		}
	}
	rs := make([]V2WorkloadState, 0, len(sm))
	for _, s := range sm {
		rs = append(rs, s)
	}
	re := make([]V2Event, 0, len(em))
	for _, e := range em {
		re = append(re, e)
	}
	v2SortStates(rs)
	v2SortEvents(re)
	return rs, re
}

func v2KeyLess(a, b V2Key) bool {
	if a.ClusterID != b.ClusterID {
		return a.ClusterID < b.ClusterID
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Workload < b.Workload
}

func v2SortStates(s []V2WorkloadState) {
	sort.Slice(s, func(i, j int) bool { return v2KeyLess(s[i].Key(), s[j].Key()) })
}

func v2SortEvents(e []V2Event) {
	sort.Slice(e, func(i, j int) bool {
		a, b := e[i], e[j]
		if a.Key() != b.Key() {
			return v2KeyLess(a.Key(), b.Key())
		}
		if !a.IncarnationAt.Equal(b.IncarnationAt) {
			return a.IncarnationAt.Before(b.IncarnationAt)
		}
		return a.Generation < b.Generation
	})
}
