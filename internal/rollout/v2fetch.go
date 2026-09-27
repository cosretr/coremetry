package rollout

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// v2fetch.go — v0.10.982 — ROLLOUTS v2 P2.2: KSM OKUMA KATMANI (SAF)
// (docs/rollouts/v2-audit.md §4.8–§4.11, §3.4; kararlar 2, 8).
//
// Tik başına küme başına sorgu kümesi (V2TickQueries) → thanos.WorkerQuery
// (v2thanos.go) → vektör çözümü (ParseV2Vector) → V2Snapshot
// (BuildV2Snapshot) → açık olaylı / bekleyen / yeni iş yükleri için imajlar
// (V2ImageTargets → V2ImageQueries → ApplyV2Images). I/O YOK: işçi
// (v2worker.go) bu fonksiyonları sırayla çağırır, testler sahte vektörle.
//
// Sorgu kuralları:
//   - Her ifade TOPLAMALI (`max by (namespace, <tür>, …)`, `count`): HA
//     kopyaları tek seriye iner ve seri tavanını yemez (§4.8); dedup=true
//     WorkerQuery'de ayrıca gönderilir.
//   - Her seçici `namespace!=""` taban eşleştiricisi + kümenin namespace
//     kalkanını (thanos.NamespaceMatcher, baştaki virgüllü) taşır (§4.9).
//     Küme etiketi WorkerQuery'de (EffectiveQuery) eklenir.
//   - Aynı türün ölçü serileri TEK çağrıda: `max by (__name__, …)
//     ({__name__=~"kube_<tür>_(…)"})` (§11 K1 biçimi; çağrı sayısı tür
//     başına 3, Deployment 6).
//   - SampleAt = max by (…) (timestamp(ham seçici)) — timestamp()
//     toplamanın İÇİNDE (V5; dışında değerlendirme zamanını verir).
//   - Kısmi/kesik/eksik sonuç → Snapshot.Complete=false ve iş yükü YOK:
//     kısmi okumadan yokluk çıkarılmaz (§4.10).

// V2Sample — anlık vektörün bir öğesi.
type V2Sample struct {
	Labels map[string]string
	Value  float64
}

// V2QueryResult — bir işçi sorgusunun çözülmüş sonucu (thanos.ConsoleResult
// aynası; paket thanos'a bağımlı kalmasın diye).
type V2QueryResult struct {
	Samples     []V2Sample
	Series      int  // tutulan seri
	TotalSeries int  // Thanos'un döndürdüğü (≥ Series)
	Truncated   bool // WorkerLimits seri tavanı kesti
	Partial     bool // en az bir Thanos uyarısı (kısmi yanıt)
}

func (r V2QueryResult) usable() bool { return !r.Truncated && !r.Partial }

// ParseV2Vector — v0.10.982 — Prometheus `data.result` (resultType=vector)
// → örnekler. NaN/±Inf değerli öğe atlanır (sayı değil; generation ya da
// replika olarak yorumlanamaz). Vektör dışı tür ve bozuk değer HATA: çağıran
// o kümenin tikini atlar (sessizce boş okumak yokluk sanılırdı).
func ParseV2Vector(resultType string, raw []byte) ([]V2Sample, error) {
	if resultType != "vector" {
		return nil, fmt.Errorf("beklenmeyen sonuç türü %q (vector beklenir)", resultType)
	}
	var items []struct {
		Metric map[string]string `json:"metric"`
		Value  []json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("vektör çözülemedi: %w", err)
	}
	out := make([]V2Sample, 0, len(items))
	for i, it := range items {
		if len(it.Value) != 2 {
			return nil, fmt.Errorf("öğe %d: değer çifti [ts, \"v\"] değil", i)
		}
		var s string
		if err := json.Unmarshal(it.Value[1], &s); err != nil {
			return nil, fmt.Errorf("öğe %d: değer dizgi değil: %w", i, err)
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("öğe %d: değer sayı değil: %q", i, s)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		l := it.Metric
		if l == nil {
			l = map[string]string{}
		}
		out = append(out, V2Sample{Labels: l, Value: v})
	}
	return out, nil
}

// V2Query — adlandırılmış işçi sorgusu.
type V2Query struct{ Name, Expr string }

// v0.10.982 — tik sorgu adları (BuildV2Snapshot sözlüğü).
const (
	V2QDeployGauges      = "deploy_gauges"
	V2QDeploySample      = "deploy_sample"
	V2QDeployProgressing = "deploy_progressing_false"
	V2QRSOwner           = "rs_owner"
	V2QRSActive          = "rs_active"
	V2QRSSpecCount       = "rs_spec_count"
	V2QStsGauges         = "sts_gauges"
	V2QStsSample         = "sts_sample"
	V2QStsRevisions      = "sts_revisions"
	V2QDsGauges          = "ds_gauges"
	V2QDsSample          = "ds_sample"
	V2QDsHashes          = "ds_hashes"
)

// v2KindLabel — KSM'nin tür etiketi (V2).
var v2KindLabel = map[string]string{V2KindDeployment: "deployment", V2KindStatefulSet: "statefulset", V2KindDaemonSet: "daemonset"}

// v2KindQueries — tür → (ölçü sorgusu, örnek zamanı sorgusu) adları.
var v2KindQueries = map[string][2]string{
	V2KindDeployment:  {V2QDeployGauges, V2QDeploySample},
	V2KindStatefulSet: {V2QStsGauges, V2QStsSample},
	V2KindDaemonSet:   {V2QDsGauges, V2QDsSample},
}

// Ölçü serisi adı → gözlem alanı; regex bu listeden kurulur (tek kaynak).
const (
	v2fGen = iota
	v2fObserved
	v2fSpec
	v2fStatus
	v2fUpdated
	v2fReady
	v2fAvailable
	v2fPaused
	v2fCreated
)

type v2Gauge struct {
	suffix string
	field  int
}

var v2Gauges = map[string][]v2Gauge{
	V2KindDeployment: {{"metadata_generation", v2fGen}, {"status_observed_generation", v2fObserved}, {"spec_replicas", v2fSpec},
		{"status_replicas", v2fStatus}, {"status_replicas_updated", v2fUpdated}, {"status_replicas_available", v2fAvailable},
		{"spec_paused", v2fPaused}, {"created", v2fCreated}},
	V2KindStatefulSet: {{"metadata_generation", v2fGen}, {"status_observed_generation", v2fObserved}, {"replicas", v2fSpec},
		{"status_replicas", v2fStatus}, {"status_replicas_updated", v2fUpdated}, {"status_replicas_ready", v2fReady},
		{"status_replicas_available", v2fAvailable}, {"created", v2fCreated}},
	V2KindDaemonSet: {{"metadata_generation", v2fGen}, {"status_observed_generation", v2fObserved},
		{"status_desired_number_scheduled", v2fSpec}, {"status_updated_number_scheduled", v2fUpdated},
		{"status_number_available", v2fAvailable}, {"created", v2fCreated}},
}

func v2GaugeExpr(kind, sel string) string {
	gs := v2Gauges[kind]
	parts := make([]string, len(gs))
	for i, g := range gs {
		parts[i] = g.suffix
	}
	lbl := v2KindLabel[kind]
	return fmt.Sprintf(`max by (__name__, namespace, %s) ({__name__=~"kube_%s_(%s)",%s})`, lbl, lbl, strings.Join(parts, "|"), sel)
}

func v2SampleExpr(kind, sel string) string {
	lbl := v2KindLabel[kind]
	return fmt.Sprintf(`max by (namespace, %s) (timestamp(kube_%s_metadata_generation{%s}))`, lbl, lbl, sel)
}

// V2TickQueries — v0.10.982 — küme başına tik sorguları, türler kanonik
// sırada (Deployment, StatefulSet, DaemonSet); kapalı türün sorgusu yok.
// nsMatcher = thanos.NamespaceMatcher(filter) ("" ya da `,namespace=~"…"`).
func V2TickQueries(kinds []string, nsMatcher string) []V2Query {
	on := v2KindSet(kinds)
	sel := `namespace!=""` + nsMatcher
	var out []V2Query
	if on[V2KindDeployment] {
		out = append(out,
			V2Query{V2QDeployGauges, v2GaugeExpr(V2KindDeployment, sel)},
			V2Query{V2QDeploySample, v2SampleExpr(V2KindDeployment, sel)},
			// KSM her koşul için true/false/unknown serisi yayar: status="false"
			// serisi (0/1) iş yükü başına tek seri; boş sonuç = aile yok (§4.5, V7).
			V2Query{V2QDeployProgressing, `max by (namespace, deployment) (kube_deployment_status_condition{condition="Progressing",status="false",` + sel + `})`},
			// owner_is_controller!="false": açık "false" dışlanır, etiket yoksa
			// ya da "true"ysa alınır (V2 — K2.3 değerleri görmeden daraltmaz).
			V2Query{V2QRSOwner, `max by (namespace, replicaset, owner_name) (kube_replicaset_owner{owner_kind="Deployment",owner_is_controller!="false",replicaset!="",` + sel + `})`},
			V2Query{V2QRSActive, `max by (namespace, replicaset) (kube_replicaset_spec_replicas{replicaset!="",` + sel + `}) > 0`},
			// Etkin RS listesi `> 0` ile yarıya iner; ailenin VARLIĞI ayrı sayımla
			// (her Deployment 0'a ölçeklenmişse etkin liste boştur ama aile vardır).
			V2Query{V2QRSSpecCount, `count(kube_replicaset_spec_replicas{replicaset!="",` + sel + `})`},
		)
	}
	if on[V2KindStatefulSet] {
		out = append(out,
			V2Query{V2QStsGauges, v2GaugeExpr(V2KindStatefulSet, sel)},
			V2Query{V2QStsSample, v2SampleExpr(V2KindStatefulSet, sel)},
			V2Query{V2QStsRevisions, `max by (__name__, namespace, statefulset, revision) ({__name__=~"kube_statefulset_status_(current|update)_revision",` + sel + `}) > 0`},
		)
	}
	if on[V2KindDaemonSet] {
		out = append(out,
			V2Query{V2QDsGauges, v2GaugeExpr(V2KindDaemonSet, sel)},
			V2Query{V2QDsSample, v2SampleExpr(V2KindDaemonSet, sel)},
			// V8: DS'nin tek revizyon kaynağı pod'lardaki controller_revision_hash
			// etiketi; pod → DS bağı kube_pod_owner. Sonuç (DS × hash) başına tek seri.
			V2Query{V2QDsHashes, `count by (namespace, owner_name, label_controller_revision_hash) (max by (namespace, pod, label_controller_revision_hash) (kube_pod_labels{label_controller_revision_hash!="",` + sel + `}) * on (namespace, pod) group_left (owner_name) max by (namespace, pod, owner_name) (kube_pod_owner{owner_kind="DaemonSet",` + sel + `}))`},
		)
	}
	return out
}

func v2KindSet(kinds []string) map[string]bool {
	on := map[string]bool{}
	for _, k := range kinds {
		if c, ok := canonicalV2Kind(k); ok {
			on[c] = true
		}
	}
	return on
}

// v2U64 / v2U32 / v2SecTime — KSM float değerleri → sayaç/zaman (negatif,
// NaN → 0 / zaman yok; kesir aşağı; tavan kelepçesi).
func v2U64(v float64) uint64 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if v >= math.MaxUint64 {
		return math.MaxUint64
	}
	return uint64(v)
}

func v2U32(v float64) uint32 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if v >= math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

func v2SecTime(v float64) time.Time {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(math.Round(v * 1000))).UTC()
}

type v2wkey struct{ kind, ns, name string }

// BuildV2Snapshot — v0.10.982 — tik sonuçları → V2Snapshot + teşhis
// sayaçları (fetch_*). SAF, deterministik (iş yükleri tür, namespace, ad
// sırasında). Beklenen sorgulardan biri eksik, kesik ya da kısmiyse
// Complete=false ve iş yükü YOK.
func BuildV2Snapshot(kinds []string, res map[string]V2QueryResult) (V2Snapshot, map[string]int) {
	diag := map[string]int{}
	expected := V2TickQueries(kinds, "")
	complete := true
	for _, q := range expected {
		r, ok := res[q.Name]
		switch {
		case !ok:
			diag["fetch_missing"]++
			complete = false
		case r.Truncated:
			diag["fetch_truncated"]++
			complete = false
		case r.Partial:
			diag["fetch_partial"]++
			complete = false
		}
	}
	if !complete {
		return V2Snapshot{}, diag
	}
	on := v2KindSet(kinds)
	obs := map[v2wkey]*V2Observation{}
	get := func(kind string, l map[string]string) *V2Observation {
		ns, name := l["namespace"], l[v2KindLabel[kind]]
		if ns == "" || name == "" {
			diag["fetch_bad_sample"]++
			return nil
		}
		k := v2wkey{kind, ns, name}
		o := obs[k]
		if o == nil {
			o = &V2Observation{Kind: kind, Namespace: ns, Name: name}
			obs[k] = o
		}
		return o
	}
	var pres V2Presence
	for _, kind := range v2Kinds {
		if !on[kind] {
			continue
		}
		gq, sq := v2KindQueries[kind][0], v2KindQueries[kind][1]
		field := map[string]int{}
		lbl := v2KindLabel[kind]
		for _, g := range v2Gauges[kind] {
			field["kube_"+lbl+"_"+g.suffix] = g.field
		}
		for _, s := range res[gq].Samples {
			f, ok := field[s.Labels["__name__"]]
			if !ok {
				diag["fetch_bad_sample"]++
				continue
			}
			o := get(kind, s.Labels)
			if o == nil {
				continue
			}
			switch f {
			case v2fGen:
				o.Generation = v2U64(s.Value)
			case v2fObserved:
				o.ObservedGeneration = v2U64(s.Value)
			case v2fSpec:
				o.SpecReplicas = v2U32(s.Value)
			case v2fStatus:
				o.StatusReplicas = v2U32(s.Value)
			case v2fUpdated:
				o.UpdatedReplicas = v2U32(s.Value)
			case v2fReady:
				o.ReadyReplicas = v2U32(s.Value)
			case v2fAvailable:
				o.AvailableReplicas = v2U32(s.Value)
			case v2fPaused:
				o.Paused = s.Value >= 1
			case v2fCreated:
				o.CreatedAt = v2SecTime(s.Value)
				if !o.CreatedAt.IsZero() {
					pres.Created = true
				}
			}
		}
		for _, s := range res[sq].Samples {
			if o := get(kind, s.Labels); o != nil {
				o.SampleAt = v2SecTime(s.Value)
			}
		}
	}
	if on[V2KindDeployment] {
		pde := res[V2QDeployProgressing].Samples
		pres.ProgressingCondition = len(pde) > 0
		for _, s := range pde {
			if s.Value >= 1 {
				if o := get(V2KindDeployment, s.Labels); o != nil {
					o.ProgressDeadlineExceeded = true
				}
			}
		}
		active := map[[2]string]uint32{}
		for _, s := range res[V2QRSActive].Samples {
			if ns, rs := s.Labels["namespace"], s.Labels["replicaset"]; ns != "" && rs != "" {
				active[[2]string{ns, rs}] = v2U32(s.Value)
			}
		}
		for _, s := range res[V2QRSSpecCount].Samples {
			if s.Value > 0 {
				pres.RSSpec = true
			}
		}
		owners := res[V2QRSOwner].Samples
		pres.RSOwner = len(owners) > 0
		for _, s := range owners {
			ns, rs, d := s.Labels["namespace"], s.Labels["replicaset"], s.Labels["owner_name"]
			if ns == "" || rs == "" || d == "" {
				diag["fetch_bad_sample"]++
				continue
			}
			o := obs[v2wkey{V2KindDeployment, ns, d}]
			if o == nil {
				diag["rs_without_deployment"]++ // sahibi bu okumada yok (silinmekte / ns kalkanı dışı)
				continue
			}
			o.ReplicaSets = append(o.ReplicaSets, V2ReplicaSet{Name: rs, SpecReplicas: active[[2]string{ns, rs}]})
		}
	}
	if on[V2KindStatefulSet] {
		type rk struct {
			k      v2wkey
			metric string
		}
		revs := map[rk][]string{}
		for _, s := range res[V2QStsRevisions].Samples {
			ns, name, rev, m := s.Labels["namespace"], s.Labels["statefulset"], s.Labels["revision"], s.Labels["__name__"]
			if ns == "" || name == "" || rev == "" || (m != "kube_statefulset_status_current_revision" && m != "kube_statefulset_status_update_revision") {
				diag["fetch_bad_sample"]++
				continue
			}
			k := rk{v2wkey{V2KindStatefulSet, ns, name}, m}
			revs[k] = append(revs[k], rev)
		}
		for k, list := range revs {
			o := obs[k.k]
			if o == nil {
				continue
			}
			list = v2SortedSet(list)
			if len(list) != 1 {
				// İki revizyon aynı anda "güncel": bayat seri / dedup kaçağı —
				// tahmin etmek yerine boş (revizyonsuz STS yolu, V2b).
				diag["sts_revision_ambiguous"]++
				continue
			}
			if k.metric == "kube_statefulset_status_current_revision" {
				o.CurrentRevision = list[0]
			} else {
				o.UpdateRevision = list[0]
			}
		}
	}
	if on[V2KindDaemonSet] {
		hashes := res[V2QDsHashes].Samples
		pres.PodRevisionHash = len(hashes) > 0
		for _, s := range hashes {
			ns, ds, h := s.Labels["namespace"], s.Labels["owner_name"], s.Labels["label_controller_revision_hash"]
			if ns == "" || ds == "" || h == "" {
				diag["fetch_bad_sample"]++
				continue
			}
			if o := obs[v2wkey{V2KindDaemonSet, ns, ds}]; o != nil {
				o.RevisionHashes = append(o.RevisionHashes, h)
			}
		}
	}
	snap := V2Snapshot{Complete: true, Presence: pres}
	keys := make([]v2wkey, 0, len(obs))
	for k := range obs {
		keys = append(keys, k)
	}
	kindOrder := map[string]int{V2KindDeployment: 0, V2KindStatefulSet: 1, V2KindDaemonSet: 2}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.kind != b.kind {
			return kindOrder[a.kind] < kindOrder[b.kind]
		}
		if a.ns != b.ns {
			return a.ns < b.ns
		}
		return a.name < b.name
	})
	for _, k := range keys {
		o := *obs[k]
		sort.Slice(o.ReplicaSets, func(i, j int) bool { return o.ReplicaSets[i].Name < o.ReplicaSets[j].Name })
		o.RevisionHashes = v2SortedSet(o.RevisionHashes)
		snap.Workloads = append(snap.Workloads, o)
	}
	return snap, diag
}

// ── İmajlar (V14; §4.11: yalnız açık olaylı iş yükleri, sınırlı) ──────────

const (
	v2ImageMaxTargets    = 100 // tik başına küme başına imajı okunan iş yükü
	v2ImageNamesPerQuery = 60  // sorgu başına owner_name regex'indeki ad
	v2ImageMaxQueries    = 20  // tik başına küme başına imaj sorgusu
	v2ImageFillMax       = 20  // tik başına artan bütçeyle imajı doldurulan durum
)

// V2ImageTargets — v0.10.982 — imajı okunacak iş yükleri (anahtar sırasında).
//
// primary (en çok limit; capped = tavanın kestiği sayı): durumu olan ve açık
// olayı (durum open_generation ya da bellekteki son olay) olan, nesil artışı
// bekleyen (pending) ya da bu okumada nesli değişmiş iş yükü — START tikinde
// imaj okunsun ki change_type (rollout/config) ve prev_images hızlı biten
// rollout'ta da dolsun. Ayrıca DURUMU OLMAYAN iş yükü, türünün durumu varsa
// (bootstrap değil, mint → 'initial'): ilk tam okumada zaten hazır olan iş
// yükünün initial olayı aynı tikte succeeded kapanır, sonraki tikte hedef
// olmaz — imajı mint tikinde okunmazsa hiç okunmazdı (inceleme düzeltmesi).
//
// fill (en çok fillLimit, primary dışı): durumu olan, hedef revizyonu belli,
// replikası > 0 ama imaj listesi BOŞ iş yükü (bootstrap baseline'ı imaj
// okumaz) — ilk START'ta eski revizyonun pod'ları gitmişse prev_images
// durumdan gelir; boşsa config değişimi (rollout restart) 'rollout' sanılırdı.
// Çağıran fill'i yalnız primary'den artan sorgu bütçesiyle okur.
func V2ImageTargets(snap V2Snapshot, clusterID string, states []V2WorkloadState, events []V2Event, limit, fillLimit int) (primary, fill []V2Key, capped int) {
	st := map[V2Key]V2WorkloadState{}
	kindHasState := map[string]bool{}
	for _, s := range states {
		if s.ClusterID == clusterID {
			st[s.Key()] = s
			kindHasState[s.WorkloadKind] = true
		}
	}
	type incKey struct {
		k   V2Key
		inc int64
	}
	openEv := map[incKey]bool{}
	latest := map[incKey]V2Event{}
	for _, e := range events {
		if e.ClusterID != clusterID {
			continue
		}
		ik := incKey{e.Key(), e.IncarnationAt.UnixMilli()}
		if cur, ok := latest[ik]; !ok || e.Generation > cur.Generation {
			latest[ik] = e
		}
	}
	for ik, e := range latest {
		if v2IsOpen(e.Status) {
			openEv[ik] = true
		}
	}
	for _, o := range snap.Workloads {
		kind, ok := canonicalV2Kind(o.Kind)
		if !ok {
			continue
		}
		k := V2Key{clusterID, o.Namespace, kind, o.Name}
		s, has := st[k]
		switch {
		case !has:
			if kindHasState[kind] {
				primary = append(primary, k)
			}
		case s.OpenGeneration != 0 || s.PendingGeneration != 0 || o.Generation != s.Generation ||
			openEv[incKey{k, v2ms(s.IncarnationAt).UnixMilli()}]:
			primary = append(primary, k)
		case len(s.Images) == 0 && s.CurrentRevision != "" && o.SpecReplicas > 0:
			fill = append(fill, k)
		}
	}
	sort.Slice(primary, func(i, j int) bool { return v2KeyLess(primary[i], primary[j]) })
	sort.Slice(fill, func(i, j int) bool { return v2KeyLess(fill[i], fill[j]) })
	if limit > 0 && len(primary) > limit {
		primary, capped = primary[:limit], len(primary)-limit
	}
	if len(fill) > fillLimit {
		fill = fill[:max(fillLimit, 0)]
	}
	return primary, fill, capped
}

// V2ImageQuery — bir imaj sorgusu: namespace + aile (Deployment RS'leri ya
// da STS/DS pod revizyon hash'leri) + kapsadığı iş yükleri.
type V2ImageQuery struct {
	Expr       string
	Namespace  string
	Deployment bool
	Workloads  []V2Key
}

// v2PromRegexValue — PromQL dizgisi içinde tam-eşleşen regex alternatifi:
// ad regex-kaçışlanır (RS adında "." olabilir), sonra dizgi çerçevesi.
func v2PromRegexValue(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = v2PromString(regexp.QuoteMeta(n))
	}
	return strings.Join(parts, "|")
}

func v2PromString(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

// V2ImageQueries — v0.10.982 — hedefler → sorgular (namespace ve aile
// başına; bir iş yükünün adları tek sorguda kalır; sorgu başına ≤
// v2ImageNamesPerQuery ad, toplam ≤ v2ImageMaxQueries sorgu). İkinci dönüş:
// tavan yüzünden imajı okunmayan iş yükü sayısı.
//
//	Deployment: kube_pod_container_info ⋈ kube_pod_owner{owner_kind="ReplicaSet"}
//	            → (RS adı = revizyon, imaj).
//	STS / DS:   kube_pod_container_info ⋈ kube_pod_labels{controller_revision_hash}
//	            ⋈ kube_pod_owner{owner_kind=~"StatefulSet|DaemonSet"} → (tür, ad,
//	            revizyon hash'i, imaj). STS pod hash'i = ControllerRevision adı
//	            = update_revision; DS hash'i RevisionHashes ile aynı etiketten.
func V2ImageQueries(snap V2Snapshot, clusterID string, targets []V2Key, nsMatcher string) ([]V2ImageQuery, int) {
	byKey := map[V2Key]V2Observation{}
	for _, o := range snap.Workloads {
		if kind, ok := canonicalV2Kind(o.Kind); ok {
			byKey[V2Key{clusterID, o.Namespace, kind, o.Name}] = o
		}
	}
	type group struct {
		ns  string
		dep bool
	}
	var order []group
	members := map[group][]V2Key{}
	for _, k := range targets {
		if _, ok := byKey[k]; !ok {
			continue
		}
		g := group{k.Namespace, k.Kind == V2KindDeployment}
		if _, seen := members[g]; !seen {
			order = append(order, g)
		}
		members[g] = append(members[g], k)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].ns != order[j].ns {
			return order[i].ns < order[j].ns
		}
		return order[i].dep && !order[j].dep
	})
	var out []V2ImageQuery
	dropped := 0
	for _, g := range order {
		var cur []V2Key
		var names []string
		flush := func() {
			if len(cur) == 0 {
				return
			}
			if len(out) >= v2ImageMaxQueries {
				dropped += len(cur)
			} else {
				out = append(out, V2ImageQuery{Expr: v2ImageExpr(g.ns, g.dep, names, nsMatcher), Namespace: g.ns, Deployment: g.dep, Workloads: cur})
			}
			cur, names = nil, nil
		}
		for _, k := range members[g] {
			own := v2ImageNames(byKey[k])
			if len(own) == 0 {
				continue
			}
			if len(names)+len(own) > v2ImageNamesPerQuery {
				flush()
			}
			if len(own) > v2ImageNamesPerQuery {
				dropped++ // tek iş yükü tavandan büyük: okunmaz
				continue
			}
			cur = append(cur, k)
			names = append(names, own...)
		}
		flush()
	}
	return out, dropped
}

// v2ImageNames — sorgunun owner_name adayları: Deployment'ta iş yükünün RS
// adları, STS/DS'de iş yükünün kendi adı.
func v2ImageNames(o V2Observation) []string {
	if o.Kind == V2KindDeployment {
		var out []string
		for _, rs := range o.ReplicaSets {
			out = append(out, rs.Name)
		}
		return v2SortedSet(out)
	}
	return []string{o.Name}
}

func v2ImageExpr(ns string, dep bool, names []string, nsMatcher string) string {
	nsSel := `namespace="` + v2PromString(ns) + `"`
	re := v2PromRegexValue(names)
	containers := `max by (namespace, pod, image) (kube_pod_container_info{` + nsSel + `,image!=""` + nsMatcher + `})`
	if dep {
		return `count by (namespace, owner_name, image) (` + containers +
			` * on (namespace, pod) group_left (owner_name) max by (namespace, pod, owner_name) (kube_pod_owner{owner_kind="ReplicaSet",` +
			nsSel + `,owner_name=~"` + re + `"` + nsMatcher + `}))`
	}
	return `count by (namespace, owner_kind, owner_name, label_controller_revision_hash, image) ((` + containers +
		` * on (namespace, pod) group_left (label_controller_revision_hash) max by (namespace, pod, label_controller_revision_hash) (kube_pod_labels{` +
		nsSel + `,label_controller_revision_hash!=""` + nsMatcher + `})) * on (namespace, pod) group_left (owner_kind, owner_name) max by (namespace, pod, owner_kind, owner_name) (kube_pod_owner{owner_kind=~"StatefulSet|DaemonSet",` +
		nsSel + `,owner_name=~"` + re + `"` + nsMatcher + `}))`
}

// ApplyV2Images — v0.10.982 — bir imaj sorgusunun sonucunu kapsadığı iş
// yüklerinin RevisionImages'ine yazar (okundu = boş da olsa {}, nil =
// okunmadı). Kesik/kısmi sonuç UYGULANMAZ (eksik imaj listesi sahte
// "imaj değişti" üretirdi).
func ApplyV2Images(snap *V2Snapshot, clusterID string, q V2ImageQuery, res V2QueryResult) {
	if snap == nil || !res.usable() {
		return
	}
	want := map[V2Key]bool{}
	for _, k := range q.Workloads {
		want[k] = true
	}
	type ownerRev struct{ kind, owner, rev string }
	imgs := map[ownerRev][]string{}
	for _, s := range res.Samples {
		l := s.Labels
		if l["namespace"] != q.Namespace || l["image"] == "" || l["owner_name"] == "" {
			continue
		}
		var k ownerRev
		if q.Deployment {
			k = ownerRev{V2KindDeployment, l["owner_name"], l["owner_name"]}
		} else {
			kind, ok := canonicalV2Kind(l["owner_kind"])
			if !ok || l["label_controller_revision_hash"] == "" {
				continue
			}
			k = ownerRev{kind, l["owner_name"], l["label_controller_revision_hash"]}
		}
		imgs[k] = append(imgs[k], l["image"])
	}
	for i := range snap.Workloads {
		o := &snap.Workloads[i]
		kind, ok := canonicalV2Kind(o.Kind)
		if !ok || !want[V2Key{clusterID, o.Namespace, kind, o.Name}] {
			continue
		}
		m := map[string][]string{}
		if kind == V2KindDeployment {
			for _, rs := range o.ReplicaSets {
				if list := imgs[ownerRev{V2KindDeployment, rs.Name, rs.Name}]; len(list) > 0 {
					m[rs.Name] = v2SortedSet(list)
				}
			}
		} else {
			for k, list := range imgs {
				if k.kind == kind && k.owner == o.Name {
					m[k.rev] = v2SortedSet(list)
				}
			}
		}
		o.RevisionImages = m
	}
}
