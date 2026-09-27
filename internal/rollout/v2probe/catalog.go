// Package v2probe — v0.10.979 — ROLLOUTS v2 §11 SORGU PAKETİ (admin probe'un
// SAF çekirdeği; docs/rollouts/v2-audit.md §11.0–11.9, spec 2026-09-27).
//
// ── NE ───────────────────────────────────────────────────────────────────
//
// Faz 2 (KSM dedektörü, P2.2) ve Faz 3 (Argo metrikleri) operatörün §11
// canlı sorgu paketini koşmasına bağlıydı: iki hedef küme + iki hub üzerinde
// ~40 PromQL sorgusunu Grafana'da elle koşup sonuçları jetonlamak. Bu paket o
// paketi TABLO olarak taşır (catalog.go), sonuçları sourcestate sözlüğüyle
// sınıflar (result.go), bütün DEĞERLERİ koşu başına bir eşlemeyle jetonlar
// (tokenize.go), v2detect.go başlığındaki V1–V14 varsayımlarını yargılar
// (assess.go) ve tek bir markdown rapor basar (report.go). I/O yok: thanos,
// chstore, http bu paketi bilmez; api katmanı (internal/api/rollouts_v2_probe.go)
// okuyucuları sürer ve ham sonuçları Finalize'a verir.
//
// ── NEDEN internal/rollout ALTINDA AYRI PAKET ────────────────────────────
//
// v2detect.go'nun (P2.1) SAF çekirdeği dokunulmadan kalır; rollout paketi
// ikinci bir kaygı (rapor/jeton) taşımaz. v2probe yalnız stdlib +
// internal/sourcestate (kendisi stdlib-only) içe aktarır: import döngüsü
// imkânsız, api katmanı ve testler serbestçe kullanır.
//
// ── NEDEN ŞABLONDA <L>="<V>" YOK ─────────────────────────────────────────
//
// Küme matcher'ını thanos enjekte eder (ClusterConfig.EffectiveQuery /
// EffectiveMatchers; cluster_matcher.go v0.10.950 golden'ı: offset, unless,
// on() operandları ve çıplak adlar dahil). Şablon matcher taşısaydı iki kez
// enjekte edilir ya da hub'ın "matcher'sız" geçişi (§11.0 "H0–H1 önce
// matcher'sız") mümkün olmazdı. Rapor da yalnız ŞABLONU basar: etkin ifade
// gerçek etiket değerini taşır.
//
// ── YER TUTUCULAR (kapalı küme; TestCatalogPlaceholdersClosed) ───────────
//
//	{{env}}    envList QuoteMeta'lı `|` alternasyonu          dev|test|prod
//	{{sfx}}    suffixList aynı                                 ocpa|ocpb
//	{{RE}}     `[^-]+-[^-]+-.+-({{env}})-({{sfx}})` (ters tırnak; §11.0 RE)
//	{{pair}}   pairGroup başına suffix alternasyonu (≥2)
//	{{ns}}     `,namespace=~"…"` bağlacı (thanos.NamespaceMatcher) ya da ""
//	{{nsSel}}  `{namespace=~"…"}` seçicisi ya da ""
//	{{inst}}   `{namespace="<hubNamespace>"}` instance başına ya da ""
//	{{k5w}}    K5 penceresi: 6h; K3.7 > 5000 ise 1h (§11.0 K5 kuralı)
//
// Ters tırnak PromQL ham dizesidir: QuoteMeta'nın ters bölüleri "…" içinde
// çift kaçış isterdi, ham dizede güvenlidir. envList/suffixList boşken bu
// yer tutucuları isteyen satırlar ErrPlaceholderEmpty verir; yürütücü onları
// "skipped: envList/suffixList empty" diye kaydeder.
package v2probe

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Pack — §11 paketi.
type Pack string

const (
	PackT Pack = "T" // §11.8 ClickHouse span öznitelikleri (ÖNCE koşar)
	PackK Pack = "K" // §11.1 KSM (hedef başına)
	PackD Pack = "D" // §11.2 DeploymentConfig (hedef başına)
	PackR Pack = "R" // §11.3 Argo Rollouts (hedef VE hub)
	PackH Pack = "H" // §11.4 hub Argo metrikleri
	PackN Pack = "N" // §11.5 ad kuralı oranları (hub)
)

// AllPacks — yürütme sırası.
var AllPacks = []Pack{PackT, PackK, PackD, PackR, PackH, PackN}

// Scope — sorgunun hangi birimde koştuğu.
type Scope uint8

const (
	ScopeTarget Scope = iota
	ScopeHub
	ScopeTargetAndHub
	ScopeClickHouse
)

// Kind — okuyucu türü.
type Kind uint8

const (
	KindInstant     Kind = iota // POST /api/v1/query (WorkerQuery; hub nomatch: ConsoleQuery)
	KindLabels                  // POST /api/v1/labels (ConsoleLabels)
	KindLabelValues             // GET /api/v1/label/<Label>/values (ConsoleLabelValues)
	KindSeries                  // /api/v1/series — KATALOGDA YOK (§11.0: ad/repo/dest_server gezilmez)
	KindSQL                     // ClickHouse (api katmanı chstore)
	KindAPI                     // chstore yardımcı (EntitySeenClusterValues)
)

// Repeat — bit maskesi: bir satır birden çok varyantla koşabilir (H0.2 hem
// nomatch hem dedup_off).
type Repeat uint16

const (
	RepeatNone         Repeat = 0
	RepeatDedupOff     Repeat = 1 << iota // K0.4, H0.2 → variant "dedup_off" (Dedup=&false)
	RepeatSecondSample                    // K0.6b → variant "second_sample", ≥30 s sonra
	RepeatNoMatcher                       // H0.*, H1.* → variant "nomatch" (hub etiketi varsa)
	RepeatNSFilter                        // K3.* → variant "ns" (NamespaceFilter doluysa)
	RepeatPerPairGroup                    // H3.5 → pairGroup başına "pair:<key>"
	RepeatPerInstance                     // H6.2b → instance başına "inst" (VariantRaw = ns)
)

// Shape — sonuç biçimi.
type Shape uint8

const (
	ShapeScalar Shape = iota // tek sayı (count(...))
	ShapeRows                // etiketli satırlar (count by (...))
	ShapeNames               // dize listesi (label adları / __name__ değerleri)
)

// Query — katalog satırı.
type Query struct {
	ID     string
	Pack   Pack
	Scope  Scope
	Kind   Kind
	Expr   string        // PromQL şablonu (anlık) ya da match[] seçicisi (label API'leri); ASLA <L>="<V>" içermez
	Label  string        // KindLabelValues: "__name__"
	Window time.Duration // label API'leri: start = now - Window
	Repeat Repeat
	Shape  Shape
	// MaxRows — raporda tutulan satır (§11.0 "≤ 50"); skaler için 0.
	MaxRows int
	// Tokenise — değeri jetonlanan etiket ADLARI (belgeleme; asıl sınıf
	// kararı tokenize.go'da etiket adına göre). Listede olmayan ve
	// LiteralLabels'ta da olmayan etiket varsayılan-reddedilir.
	Tokenise []string
	// LiteralFor — bu satıra ÖZEL harfi harfine tutulan etiketler (K0.2 job:
	// exporter adları enum gibidir).
	LiteralFor []string
	Informs    []string // "V1", "dec 26", "§4.2"
	Expect     string   // raporda basılan tek satırlık beklenti
	// FallbackFor — yalnız adı geçen satır bad_data ile düşerse koşar (H5.2).
	FallbackFor string
}

// Params — Expand girdisi (api katmanı doldurur).
type Params struct {
	EnvList, SuffixList []string
	// PairGroups — sentetik anahtar ("pair-1") → suffix listesi (≥2). Anahtar
	// varyant adına girer; ham pairGroup adı buraya GİRMEZ.
	PairGroups map[string][]string
	// NSMatcher — thanos.NamespaceMatcher çıktısı (`,namespace=~"…"`) ya da "".
	NSMatcher string
	// InstanceNS — hub'ın yapılandırılmış instance'larının hubNamespace'leri (ham).
	InstanceNS []string
	// K5Window — "6h" (varsayılan) ya da "1h".
	K5Window string
	// WithoutMatcher — hub'ın Thanos etiketi varsa nomatch varyantları üret.
	WithoutMatcher bool
}

// Instance — somut bir çağrı.
type Instance struct {
	Query
	Variant    string // "" | nomatch | dedup_off | second_sample | ns | pair:<key> | inst
	Expr       string
	VariantRaw string // inst: ham hubNamespace (Finalize jetonlar)
}

// ErrPlaceholderEmpty — {{env}}/{{sfx}}/{{RE}} isteyen satır, liste boş.
var ErrPlaceholderEmpty = errors.New("v2probe: envList/suffixList empty")

// Placeholders — kapalı küme.
var Placeholders = []string{"{{env}}", "{{sfx}}", "{{RE}}", "{{pair}}", "{{ns}}", "{{nsSel}}", "{{inst}}", "{{k5w}}"}

const bq = "`"

// alternation — QuoteMeta'lı `|` alternasyonu; boş öğeler atlanır.
func alternation(vals []string) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		parts = append(parts, regexp.QuoteMeta(v))
	}
	return strings.Join(parts, "|")
}

var promStringEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// render — yer tutucuları doldurur. ns/inst/pair varyanta göre.
func render(expr string, p Params, ns, inst, pair string, needsNS bool) (string, error) {
	env, sfx := alternation(p.EnvList), alternation(p.SuffixList)
	k5 := p.K5Window
	if k5 == "" {
		k5 = "6h"
	}
	nsSel := ""
	if needsNS && ns != "" {
		nsSel = "{" + strings.TrimPrefix(ns, ",") + "}"
	}
	if !needsNS {
		ns = ""
	}
	instSel := ""
	if inst != "" {
		instSel = `{namespace="` + promStringEscaper.Replace(inst) + `"}`
	}
	if strings.Contains(expr, "{{env}}") || strings.Contains(expr, "{{RE}}") {
		if env == "" {
			return "", ErrPlaceholderEmpty
		}
	}
	if strings.Contains(expr, "{{sfx}}") || strings.Contains(expr, "{{RE}}") {
		if sfx == "" {
			return "", ErrPlaceholderEmpty
		}
	}
	re := bq + `[^-]+-[^-]+-.+-(` + env + `)-(` + sfx + `)` + bq
	r := strings.NewReplacer(
		"{{RE}}", re,
		"{{env}}", env,
		"{{sfx}}", sfx,
		"{{pair}}", pair,
		"{{nsSel}}", nsSel,
		"{{ns}}", ns,
		"{{inst}}", instSel,
		"{{k5w}}", k5,
	)
	return r.Replace(expr), nil
}

// Expand — bir katalog satırının bu birimdeki somut çağrıları, yürütme
// sırasında. Sıfır instance (pairGroup yok) geçerlidir: yürütücü "skipped"
// yazar.
func Expand(q Query, p Params) ([]Instance, error) {
	if q.Kind == KindSQL || q.Kind == KindAPI {
		return []Instance{{Query: q, Expr: q.Expr}}, nil
	}
	switch {
	case q.Repeat&RepeatPerPairGroup != 0:
		keys := make([]string, 0, len(p.PairGroups))
		for k, v := range p.PairGroups {
			if len(v) >= 2 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		out := make([]Instance, 0, len(keys))
		for _, k := range keys {
			e, err := render(q.Expr, p, "", "", alternation(p.PairGroups[k]), false)
			if err != nil {
				return nil, err
			}
			out = append(out, Instance{Query: q, Variant: "pair:" + k, Expr: e})
		}
		return out, nil
	case q.Repeat&RepeatPerInstance != 0:
		if len(p.InstanceNS) == 0 {
			e, err := render(q.Expr, p, "", "", "", false)
			if err != nil {
				return nil, err
			}
			return []Instance{{Query: q, Expr: e}}, nil
		}
		nss := append([]string(nil), p.InstanceNS...)
		sort.Strings(nss)
		out := make([]Instance, 0, len(nss))
		for _, ns := range nss {
			e, err := render(q.Expr, p, "", ns, "", false)
			if err != nil {
				return nil, err
			}
			out = append(out, Instance{Query: q, Variant: "inst", Expr: e, VariantRaw: ns})
		}
		return out, nil
	}
	base, err := render(q.Expr, p, "", "", "", false)
	if err != nil {
		return nil, err
	}
	out := []Instance{{Query: q, Expr: base}}
	if q.Repeat&RepeatNoMatcher != 0 && p.WithoutMatcher {
		out = append(out, Instance{Query: q, Variant: "nomatch", Expr: base})
	}
	if q.Repeat&RepeatDedupOff != 0 {
		out = append(out, Instance{Query: q, Variant: "dedup_off", Expr: base})
	}
	if q.Repeat&RepeatSecondSample != 0 {
		out = append(out, Instance{Query: q, Variant: "second_sample", Expr: base})
	}
	if q.Repeat&RepeatNSFilter != 0 && p.NSMatcher != "" {
		e, err := render(q.Expr, p, p.NSMatcher, "", "", true)
		if err != nil {
			return nil, err
		}
		out = append(out, Instance{Query: q, Variant: "ns", Expr: e})
	}
	return out, nil
}

// ByPack — katalogun bir paketinin satırları, yürütme sırasında.
func ByPack(p Pack) []Query {
	out := make([]Query, 0, 64)
	for _, q := range Catalogue {
		if q.Pack == p {
			out = append(out, q)
		}
	}
	return out
}

// Find — id ile satır.
func Find(id string) (Query, bool) {
	for _, q := range Catalogue {
		if q.ID == id {
			return q, true
		}
	}
	return Query{}, false
}

// Runs — satır bu roldeki birimde koşar mı.
func (q Query) Runs(role string) bool {
	switch q.Scope {
	case ScopeTarget:
		return role == "target"
	case ScopeHub:
		return role == "hub"
	case ScopeTargetAndHub:
		return role == "target" || role == "hub"
	}
	return false
}

// Kısa yardımcılar: katalog tablosu okunur kalsın.
func inst(id string, pack Pack, scope Scope, expr string, shape Shape, maxRows int, informs ...string) Query {
	return Query{ID: id, Pack: pack, Scope: scope, Kind: KindInstant, Expr: expr, Shape: shape, MaxRows: maxRows, Informs: informs}
}

func (q Query) rep(r Repeat) Query           { q.Repeat = r; return q }
func (q Query) tok(labels ...string) Query   { q.Tokenise = labels; return q }
func (q Query) lit(labels ...string) Query   { q.LiteralFor = labels; return q }
func (q Query) expect(s string) Query        { q.Expect = s; return q }
func (q Query) fallback(id string) Query     { q.FallbackFor = id; return q }
func (q Query) window(d time.Duration) Query { q.Window = d; return q }

func labels(id string, pack Pack, scope Scope, match string, informs ...string) Query {
	return Query{ID: id, Pack: pack, Scope: scope, Kind: KindLabels, Expr: match, Shape: ShapeNames, MaxRows: 100, Window: 300 * time.Second, Informs: informs}
}

func names(id string, pack Pack, scope Scope, match string, informs ...string) Query {
	return Query{ID: id, Pack: pack, Scope: scope, Kind: KindLabelValues, Label: "__name__", Expr: match, Shape: ShapeNames, MaxRows: 100, Window: 600 * time.Second, Informs: informs}
}

const (
	// Argo uygulama başına tek satıra indiren grup (shard/HA kopyaları düşer).
	appGroup = `group by (namespace, exported_namespace, name) (argocd_app_info)`
	// H6.5/H6.6 geçiş sayımı; offset yerine {{off}} değil — iki ayrı satır.
	transitionGroup = `group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info)`
	clusterLabels   = `cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id`
)

// Catalogue — yürütme sırası (paket başına). Sayımlar
// TestCatalogPackCounts'ta çivili.
var Catalogue = []Query{
	// ── T (ClickHouse; önce) ────────────────────────────────────────────────
	// v0.10.979 — T1 sayaçları (sayı) yalnız BU satırda harfi harfine:
	// küresel LiteralLabels'a girseler hub H0.3/H0.5'teki `k8s_cluster`
	// küme adını gölgelerlerdi (tokenize.go Value sırası).
	{ID: "T1", Pack: PackT, Scope: ScopeClickHouse, Kind: KindSQL, Expr: "rolloutProbeCoverageSQL(900, cluster)", Shape: ShapeRows, MaxRows: 50,
		Tokenise: []string{"cluster"}, Informs: []string{"§9.1", "§9.2", "§9.3", "P5.1", "P5.3", "dec 29"},
		LiteralFor: []string{"sampled", "svc_version", "img_tag", "container_id", "depl", "rs", "sts", "ds", "env_name", "k8s_cluster", "ocp_cluster"},
		Expect:     "span cluster değeri başına öznitelik kapsaması (15 dk örneklem, ≤50 satır)"},
	{ID: "T2", Pack: PackT, Scope: ScopeClickHouse, Kind: KindSQL, Expr: "rolloutProbeEnvSQL(900, cluster)", Shape: ShapeRows, MaxRows: 50,
		Tokenise: []string{"deploy_env", "cluster"}, Informs: []string{"§9.3.3", "§9.3.5", "P5.2", "dec 29"},
		Expect: "deploy_env × cluster biçimi (türetilen cluster formu görünür)"},
	{ID: "T3", Pack: PackT, Scope: ScopeClickHouse, Kind: KindAPI, Expr: "EntitySeenClusterValues(now-7d) + SpanClusterOwner", Shape: ShapeRows, MaxRows: 50,
		Tokenise: []string{"span_cluster", "owner"}, Informs: []string{"§9.3.2"},
		Expect: "eşleşmemiş span cluster değeri sayısı (+ ≤50 jetonlu değer)"},

	// ── K (hedef başına) ───────────────────────────────────────────────────
	inst("K0.1", PackK, ScopeTarget, `count(kube_node_info)`, ShapeScalar, 0, "plumbing").expect("skaler > 0 — matcher çalışıyor"),
	inst("K0.2", PackK, ScopeTarget, `count by (job) (up{job=~".*state-metrics.*"} == 1)`, ShapeRows, 20, "V9", "dec 26").lit("job").
		expect("kube-state-metrics (+ openshift-state-metrics); ek job = ikinci KSM"),
	inst("K0.3", PackK, ScopeTarget, `count by (version) (kube_state_metrics_build_info)`, ShapeRows, 5, "V7", "dec 26").
		expect("tek sürüm satırı (boş = telemetri portu scrape edilmiyor)"),
	inst("K0.4", PackK, ScopeTarget, `count(kube_deployment_spec_replicas) / count(count by (namespace, deployment) (kube_deployment_spec_replicas))`, ShapeScalar, 0, "V6", "§4.8").
		rep(RepeatDedupOff).expect("tam 1; > 1 = kopya (dedup)"),
	inst("K0.5", PackK, ScopeTarget, `quantile(0.5, count_over_time(kube_deployment_metadata_generation[10m]))`, ShapeScalar, 0, "V5", "dec 8").
		expect("10 ⇒ 60 s scrape; 20 ⇒ 30 s"),
	inst("K0.6", PackK, ScopeTarget, `time() - max(timestamp(kube_deployment_metadata_generation))`, ShapeScalar, 0, "V9", "V5").expect("< 90 s (tazelik)"),
	inst("K0.6b", PackK, ScopeTarget, `max(timestamp(kube_deployment_metadata_generation))`, ShapeScalar, 0, "V5", "dec 8").
		rep(RepeatSecondSample).expect("iki örnek ≥30 s arayla: dedup=true querier aynı scrape zamanını döndürür mü"),
	inst("K1.D", PackK, ScopeTarget, `count by (__name__) ({__name__=~"kube_deployment_(metadata_generation|status_observed_generation|spec_replicas|status_replicas|status_replicas_updated|status_replicas_available|status_replicas_ready|status_replicas_unavailable|status_condition|spec_paused|created|labels|annotations)"})`, ShapeRows, 13, "V1", "V4", "V11").
		expect("CMO'da created, annotations yok"),
	inst("K1.R", PackK, ScopeTarget, `count by (__name__) ({__name__=~"kube_replicaset_(owner|created|spec_replicas|status_replicas|status_ready_replicas|metadata_generation|status_observed_generation|labels)"})`, ShapeRows, 8, "V3", "V4", "dec 3").
		expect("CMO'da created, metadata_generation, status_observed_generation yok"),
	inst("K1.P", PackK, ScopeTarget, `count by (__name__) ({__name__=~"kube_pod_(container_info|owner|labels|info|created|start_time)"})`, ShapeRows, 6, "V14", "V8").expect("container_info + owner şart"),
	inst("K1.S", PackK, ScopeTarget, `count by (__name__) ({__name__=~"kube_statefulset_(metadata_generation|status_observed_generation|replicas|status_replicas|status_replicas_updated|status_replicas_ready|status_replicas_available|status_replicas_current|status_current_revision|status_update_revision|created)"})`, ShapeRows, 11, "V1", "V2b", "V4").
		expect("current/update_revision varsa STS rollback görünür"),
	inst("K1.DS", PackK, ScopeTarget, `count by (__name__) ({__name__=~"kube_daemonset_(metadata_generation|status_observed_generation|status_desired_number_scheduled|status_updated_number_scheduled|status_number_available|status_number_unavailable|status_number_ready|status_current_number_scheduled|created)"})`, ShapeRows, 9, "V1", "V4").
		expect("generation + observed + desired/updated"),
	inst("K1.H", PackK, ScopeTarget, `count by (scaletargetref_kind) (kube_horizontalpodautoscaler_info)`, ShapeRows, 10, "V10").expect("HPA payı (generation gürültüsü)"),
	names("K1.X", PackK, ScopeTarget, `{__name__=~"kube_(deployment|replicaset|statefulset|daemonset|replicationcontroller)_.*"}`, "V1", "V2b", "V4", "V12").expect("PromQL'siz çapraz kontrol: metrik adları"),
	labels("K2.1a", PackK, ScopeTarget, `kube_pod_container_info`, "V14").expect("etiket adları: image, image_spec, image_id …"),
	labels("K2.1b", PackK, ScopeTarget, `kube_replicaset_owner`, "V2").expect("namespace, replicaset, owner_kind, owner_name, owner_is_controller"),
	labels("K2.1c", PackK, ScopeTarget, `kube_deployment_status_condition`, "V7").expect("condition, status (+ reason ≥ v2.17)"),
	labels("K2.1d", PackK, ScopeTarget, `kube_statefulset_status_update_revision`, "V2").expect("namespace, statefulset, revision"),
	labels("K2.1e", PackK, ScopeTarget, `kube_deployment_labels`, "§4.2").expect("label_* adları"),
	inst("K2.2a", PackK, ScopeTarget, `count(kube_pod_container_info)`, ShapeScalar, 0, "V14").expect("container sayısı"),
	inst("K2.2b", PackK, ScopeTarget, `count(kube_pod_container_info{image_spec!=""})`, ShapeScalar, 0, "V14").expect("image_spec dolu"),
	inst("K2.2c", PackK, ScopeTarget, `count(kube_pod_container_info{image_id!=""})`, ShapeScalar, 0, "V14").expect("image_id dolu"),
	inst("K2.2d", PackK, ScopeTarget, `count(kube_pod_container_info{image=~".+@sha256:.+"})`, ShapeScalar, 0, "V14", "P5.1").expect("image digest biçimli"),
	inst("K2.2e", PackK, ScopeTarget, `count(kube_pod_container_info{image_spec=~".+@sha256:.+"})`, ShapeScalar, 0, "V14").expect("image_spec digest biçimli"),
	inst("K2.3a", PackK, ScopeTarget, `count by (owner_kind, owner_is_controller) (kube_replicaset_owner)`, ShapeRows, 10, "V2", "V3").expect("enum tablosu"),
	inst("K2.3b", PackK, ScopeTarget, `count by (owner_kind) (kube_pod_owner)`, ShapeRows, 10, "V8", "V12", "V14").expect("ReplicaSet, DaemonSet, StatefulSet, ReplicationController …"),
	inst("K2.3c", PackK, ScopeTarget, `count by (condition, status, reason) (kube_deployment_status_condition == 1)`, ShapeRows, 20, "V7").expect("boş reason ⇒ KSM < v2.17"),
	inst("K2.4a", PackK, ScopeTarget, `count(kube_pod_labels{label_pod_template_hash!=""})`, ShapeScalar, 0, "§4.6").expect("pod-template-hash"),
	inst("K2.4b", PackK, ScopeTarget, `count(kube_pod_labels{label_controller_revision_hash!=""})`, ShapeScalar, 0, "V8").expect("DS/STS revizyon kaynağı"),
	inst("K2.4c", PackK, ScopeTarget, `count(kube_pod_labels{label_rollouts_pod_template_hash!=""})`, ShapeScalar, 0, "dec 11").expect("Argo Rollouts pod'ları"),
	inst("K2.4d", PackK, ScopeTarget, `count(kube_pod_labels{label_deploymentconfig!=""})`, ShapeScalar, 0, "V12", "dec 12").expect("DC pod'ları"),
	inst("K2.4e", PackK, ScopeTarget, `count(kube_pod_labels{label_app_kubernetes_io_instance!=""})`, ShapeScalar, 0, "P3.2").expect("eşleme ipucu"),
	inst("K2.4f", PackK, ScopeTarget, `count(kube_pod_labels{label_argocd_argoproj_io_instance!=""})`, ShapeScalar, 0, "P3.2").expect("eşleme ipucu"),
	inst("K2.4g", PackK, ScopeTarget, `count(kube_deployment_labels{label_app_kubernetes_io_instance!=""})`, ShapeScalar, 0, "P3.2").expect("CMO'da 0 beklenir"),
	inst("K3.1", PackK, ScopeTarget, `count(kube_replicaset_owner{owner_kind="Deployment"{{ns}}})`, ShapeScalar, 0, "V3", "V13", "dec 2", "dec 3").rep(RepeatNSFilter).expect("> 1000 ⇒ v1 yolu bugün kesik"),
	inst("K3.2", PackK, ScopeTarget, `count(kube_replicaset_spec_replicas{{nsSel}})`, ShapeScalar, 0, "V3", "V13").rep(RepeatNSFilter).expect("RS spec serisi"),
	inst("K3.3", PackK, ScopeTarget, `count(kube_replicaset_spec_replicas{{nsSel}} > 0)`, ShapeScalar, 0, "V3").rep(RepeatNSFilter).expect("etkin RS"),
	inst("K3.4", PackK, ScopeTarget, `count(kube_deployment_metadata_generation{{nsSel}})`, ShapeScalar, 0, "V1", "V13").rep(RepeatNSFilter).expect("Deployment sayısı"),
	inst("K3.5", PackK, ScopeTarget, `count(kube_statefulset_replicas{{nsSel}})`, ShapeScalar, 0, "V1").rep(RepeatNSFilter).expect("STS sayısı"),
	inst("K3.6", PackK, ScopeTarget, `count(kube_daemonset_status_desired_number_scheduled{{nsSel}})`, ShapeScalar, 0, "V1").rep(RepeatNSFilter).expect("DS sayısı"),
	inst("K3.7", PackK, ScopeTarget, `count(kube_pod_container_info{{nsSel}})`, ShapeScalar, 0, "V14").rep(RepeatNSFilter).expect("> 5000 ⇒ K5 penceresi 1h"),
	inst("K4.1", PackK, ScopeTarget, `count(kube_deployment_status_condition{condition="Progressing",status="false"} == 1)`, ShapeScalar, 0, "V7", "dec 10").expect("şu an stuck"),
	inst("K4.2", PackK, ScopeTarget, `count(max by (namespace, deployment) (kube_deployment_status_observed_generation) < on (namespace, deployment) max by (namespace, deployment) (kube_deployment_metadata_generation))`, ShapeScalar, 0, "V1", "V6").expect("controller gecikmesi"),
	inst("K4.3", PackK, ScopeTarget, `count(kube_deployment_spec_paused == 1)`, ShapeScalar, 0, "paused").expect("paused"),
	inst("K4.4", PackK, ScopeTarget, `count(kube_statefulset_status_update_revision unless on (namespace, statefulset, revision) kube_statefulset_status_current_revision)`, ShapeScalar, 0, "V2b", "V2").expect("STS rollout ortasında"),
	inst("K5.1", PackK, ScopeTarget, `sum(changes(kube_deployment_metadata_generation[{{k5w}}]))`, ShapeScalar, 0, "V10", "dec 8").expect("generation artışları"),
	inst("K5.2", PackK, ScopeTarget, `count(changes(kube_deployment_spec_replicas[{{k5w}}]) > 0)`, ShapeScalar, 0, "V10", "V11").expect("ölçeklenen Deployment"),
	inst("K5.3", PackK, ScopeTarget, `count(count by (namespace, replicaset) (kube_replicaset_owner{owner_kind="Deployment"}) unless count by (namespace, replicaset) (kube_replicaset_owner{owner_kind="Deployment"} offset {{k5w}}))`, ShapeScalar, 0, "V3", "V10").expect("yeni RS"),
	inst("K5.4", PackK, ScopeTarget, `count((kube_replicaset_spec_replicas > 0) and on (namespace, replicaset) (kube_replicaset_spec_replicas offset {{k5w}} == 0))`, ShapeScalar, 0, "V3").expect("yeniden etkin RS (rollback adayı)"),
	inst("K5.5", PackK, ScopeTarget, `sum(changes(kube_statefulset_metadata_generation[{{k5w}}]))`, ShapeScalar, 0, "V10").expect("STS generation artışları"),
	inst("K5.6", PackK, ScopeTarget, `sum(changes(kube_daemonset_metadata_generation[{{k5w}}]))`, ShapeScalar, 0, "V10").expect("DS generation artışları"),
	inst("K6.1", PackK, ScopeTarget, `count(kube_replicaset_owner{replicaset!="",owner_kind="Deployment"})`, ShapeScalar, 0, "V3", "dec 3").expect("> 1000 ⇒ v1 yolu kesik"),
	inst("K6.2", PackK, ScopeTarget, `count(kube_replicaset_spec_replicas{replicaset!=""})`, ShapeScalar, 0, "V3", "dec 3").expect("v1 yolu"),
	inst("K6.3", PackK, ScopeTarget, `count(kube_replicaset_status_ready_replicas{replicaset!=""})`, ShapeScalar, 0, "dec 3").expect("v1 yolu"),
	inst("K6.4", PackK, ScopeTarget, `count(kube_replicaset_created{replicaset!=""})`, ShapeScalar, 0, "V4", "dec 3", "dec 9").expect("_created var mı"),

	// ── D (hedef başına) ───────────────────────────────────────────────────
	inst("D1", PackD, ScopeTarget, `count(openshift_deploymentconfig_spec_replicas)`, ShapeScalar, 0, "V12", "dec 12").expect("DC sayısı"),
	inst("D2", PackD, ScopeTarget, `count(openshift_deploymentconfig_spec_replicas > 0)`, ShapeScalar, 0, "V12").expect("etkin DC"),
	inst("D3", PackD, ScopeTarget, `count(count by (namespace) (openshift_deploymentconfig_spec_replicas))`, ShapeScalar, 0, "V12").expect("DC'li namespace"),
	inst("D4", PackD, ScopeTarget, `count(kube_pod_owner{owner_kind="ReplicationController"})`, ShapeScalar, 0, "V12").expect("RC pod'ları"),
	inst("D5", PackD, ScopeTarget, `count by (owner_kind) (kube_replicationcontroller_owner)`, ShapeRows, 10, "V12").expect("DENEYSEL; minimal profilde boş"),
	inst("D6", PackD, ScopeTarget, `count by (__name__) ({__name__=~"kube_replicationcontroller_.*|openshift_deploymentconfig_.*"})`, ShapeRows, 40, "V12").expect("RC/DC metrik adları"),
	inst("D7", PackD, ScopeTarget, `sum(changes(openshift_deploymentconfig_metadata_generation[6h]))`, ShapeScalar, 0, "V12").expect("DC generation artışları"),
	inst("D8", PackD, ScopeTarget, `count(kube_deployment_spec_replicas > 0)`, ShapeScalar, 0, "V12").expect("pay = D2 ÷ (D2 + D8)"),

	// ── R (hedef VE hub) ──────────────────────────────────────────────────
	names("R0", PackR, ScopeTargetAndHub, `{__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"}`, "dec 11").expect("boş = Argo Rollouts yok"),
	inst("R1", PackR, ScopeTargetAndHub, `count by (__name__) ({__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"})`, ShapeRows, 40, "dec 11").expect("metrik adı → sayı"),
	inst("R2", PackR, ScopeTargetAndHub, `count(kube_replicaset_owner{owner_kind="Rollout"})`, ShapeScalar, 0, "V3", "dec 11").expect("Rollout sahipli RS"),
	inst("R3", PackR, ScopeTargetAndHub, `count by (strategy, traffic_router, phase) (rollout_info)`, ShapeRows, 20, "dec 11").expect("strateji dağılımı"),

	// ── H (hub başına) ────────────────────────────────────────────────────
	inst("H0.1", PackH, ScopeHub, `count(argocd_app_info)`, ShapeScalar, 0, "dec 2", "§5.4").rep(RepeatNoMatcher).expect("toplam seri (≈ 40000?)"),
	inst("H0.2", PackH, ScopeHub, `count by (prometheus, prometheus_replica) (argocd_app_info)`, ShapeRows, 10, "V6").rep(RepeatNoMatcher|RepeatDedupOff).tok("prometheus", "prometheus_replica").
		expect("dedup: prometheus_replica'sız 1 satır"),
	inst("H0.3", PackH, ScopeHub, `count by (`+clusterLabels+`) (argocd_app_info)`, ShapeRows, 20, "dec 5").rep(RepeatNoMatcher).
		tok("cluster", "cluster_id", "cluster_name", "k8s_cluster", "openshift_cluster", "prometheus", "tenant", "tenant_id").expect("Argo serisindeki küme etiketleri (H0.5 ile kıyasla)"),
	inst("H0.4", PackH, ScopeHub, `count(`+appGroup+`)`, ShapeScalar, 0, "V6").rep(RepeatNoMatcher).expect("tekil uygulama (= H0.1 değilse shard/replika kopyası)"),
	inst("H0.5", PackH, ScopeHub, `count by (`+clusterLabels+`) (kube_node_info)`, ShapeRows, 20, "dec 5").rep(RepeatNoMatcher).
		tok("cluster", "cluster_id", "cluster_name", "k8s_cluster", "openshift_cluster", "prometheus", "tenant", "tenant_id").expect("kube_node_info'daki küme etiketleri"),
	inst("H1.1", PackH, ScopeHub, `count by (namespace, job) (argocd_app_info)`, ShapeRows, 50, "§5.2").rep(RepeatNoMatcher).tok("namespace", "job").expect("instance başına bir satır"),
	inst("H1.2", PackH, ScopeHub, `count by (namespace, exported_namespace, job) (argocd_app_info)`, ShapeRows, 50, "§5.2", "dec 28").rep(RepeatNoMatcher).tok("namespace", "exported_namespace", "job").
		expect("(A) exported_namespace == namespace; (B) yok; (C) farklı"),
	inst("H1.3a", PackH, ScopeHub, `count by (exported_namespace) (group by (namespace, exported_namespace) (argocd_app_info)) > 1`, ShapeRows, 50, "§5.2").rep(RepeatNoMatcher).tok("exported_namespace").expect("çakışma (boş = yok)"),
	inst("H1.3b", PackH, ScopeHub, `count by (job) (group by (namespace, job) (argocd_app_info)) > 1`, ShapeRows, 50, "§5.2").rep(RepeatNoMatcher).tok("job").expect("job çakışması"),
	inst("H1.4", PackH, ScopeHub, `count by (namespace) (group by (namespace, pod) (argocd_app_info))`, ShapeRows, 50, "§5.4").rep(RepeatNoMatcher).tok("namespace").expect("instance başına controller shard"),
	inst("H1.5", PackH, ScopeHub, `count by (namespace, job) (argocd_cluster_info)`, ShapeRows, 50, "§5.1").rep(RepeatNoMatcher).tok("namespace", "job").expect("sıfır uygulamalı instance'lar dahil"),
	inst("H1.6a", PackH, ScopeHub, `max(count by (namespace) (argocd_app_info))`, ShapeScalar, 0, "dec 2").rep(RepeatNoMatcher).expect("en büyük instance"),
	inst("H1.6b", PackH, ScopeHub, `max(count by (namespace, dest_server) (argocd_app_info))`, ShapeScalar, 0, "dec 2").rep(RepeatNoMatcher).expect("en büyük shard"),
	inst("H1.6c", PackH, ScopeHub, `count(count by (namespace, dest_server) (argocd_app_info) > 1000)`, ShapeScalar, 0, "dec 2").rep(RepeatNoMatcher).expect("1000'i aşan shard"),
	inst("H2.1", PackH, ScopeHub, `count by (dest_server) (argocd_app_info)`, ShapeRows, 50, "dec 7").tok("dest_server").expect("onlarca satır; jetonlu"),
	inst("H2.2", PackH, ScopeHub, `count(group by (dest_server) (argocd_app_info))`, ShapeScalar, 0, "dec 7").expect("hedef sayısı"),
	inst("H2.3", PackH, ScopeHub, `count(argocd_app_info{dest_server=""})`, ShapeScalar, 0, "dec 28").expect("destination.name ya da başarısız lookup"),
	inst("H2.4", PackH, ScopeHub, `count(argocd_app_info{dest_server="https://kubernetes.default.svc"})`, ShapeScalar, 0, "§5.6").expect("hub'ın kendisindeki uygulamalar"),
	inst("H2.5", PackH, ScopeHub, `count by (port) (label_replace(group by (dest_server) (argocd_app_info), "port", "$1", "dest_server", `+bq+`https?://[^/]+:([0-9]+)/?`+bq+`))`, ShapeRows, 10, "dec 7").expect("port dağılımı (port literal)"),
	inst("H2.6", PackH, ScopeHub, `count(group by (dest_server, dest_namespace) (argocd_app_info))`, ShapeScalar, 0, "§5.3").expect("hedef ns sayısı"),
	inst("H2.7", PackH, ScopeHub, `count_values("apps_per_target_ns", count by (dest_server, dest_namespace) (group by (namespace, exported_namespace, name, dest_server, dest_namespace) (argocd_app_info)))`, ShapeRows, 50, "P3.2").expect("hedef ns başına uygulama histogramı"),
	inst("H2.8", PackH, ScopeHub, `count(argocd_app_info{dest_namespace=""})`, ShapeScalar, 0, "§5.3").expect("dest_namespace boş"),
	inst("H3.1", PackH, ScopeHub, `count(count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)) > 1)`, ShapeScalar, 0, "§5.2").expect("gerçek kopyalar"),
	inst("H3.2", PackH, ScopeHub, `count_values("same_name_same_server", count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)))`, ShapeRows, 50, "§5.2").expect("histogram"),
	inst("H3.3", PackH, ScopeHub, `count(count by (name) (group by (namespace, name) (argocd_app_info)) > 1)`, ShapeScalar, 0, "§5.2").expect("aynı ad iki instance'ta"),
	inst("H3.4", PackH, ScopeHub, `count_values("clusters_per_base", count by (base) (group by (base, dest_server) (label_replace(argocd_app_info{name=~`+bq+`.+-({{env}})-({{sfx}})`+bq+`}, "base", "$1", "name", `+bq+`(.+)-({{sfx}})`+bq+`))))`, ShapeRows, 50, "dec 7").expect("taban başına küme sayısı histogramı"),
	inst("H3.5", PackH, ScopeHub, `count(count by (base) (group by (base, sync_status, health_status) (label_replace(argocd_app_info{name=~`+bq+`.+-({{pair}})`+bq+`}, "base", "$1", "name", `+bq+`(.+)-({{pair}})`+bq+`))) > 1)`, ShapeScalar, 0, "dec 29", "P3.5").rep(RepeatPerPairGroup).expect("çift durum uyuşmazlığı (şimdi)"),
	inst("H4.1", PackH, ScopeHub, `count by (sync_status, health_status) (group by (namespace, exported_namespace, name, sync_status, health_status) (argocd_app_info))`, ShapeRows, 18, "dec 15").expect("≤ 18 satır"),
	inst("H4.2", PackH, ScopeHub, `count by (operation) (argocd_app_info)`, ShapeRows, 10, "§7.5").expect("operasyon dağılımı"),
	inst("H4.3", PackH, ScopeHub, `count by (autosync_enabled) (group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))`, ShapeRows, 5, "dec 15").expect("etiketsiz tek satır ⇒ Argo ≤ 2.8"),
	inst("H4.4", PackH, ScopeHub, `count by (namespace, autosync_enabled) (argocd_app_info)`, ShapeRows, 50, "dec 15").tok("namespace").expect("instance başına autosync"),
	labels("H5.1a", PackH, ScopeHub, `argocd_app_info`, "§5.1").window(600 * time.Second).expect("etiket adları harfi harfine"),
	labels("H5.1b", PackH, ScopeHub, `argocd_app_sync_total`, "§5.1").window(600 * time.Second).expect("etiket adları"),
	labels("H5.1c", PackH, ScopeHub, `argocd_cluster_info`, "§5.1").window(600 * time.Second).expect("etiket adları"),
	labels("H5.1d", PackH, ScopeHub, `argocd_app_labels`, "§10").window(600 * time.Second).expect("etiket adları"),
	inst("H5.2a", PackH, ScopeHub, `topk(1, argocd_app_info)`, ShapeNames, 1, "§11.4").fallback("H5.1a").expect("H5.1a bad_data ise: yalnız etiket ADLARI"),
	inst("H5.2b", PackH, ScopeHub, `topk(1, argocd_app_sync_total)`, ShapeNames, 1, "§11.4").fallback("H5.1b").expect("H5.1b bad_data ise: yalnız etiket ADLARI"),
	inst("H5.3a", PackH, ScopeHub, `count(argocd_app_info{autosync_enabled!=""})`, ShapeScalar, 0, "dec 15").expect("autosync_enabled var mı"),
	inst("H5.3b", PackH, ScopeHub, `count(argocd_app_info{exported_namespace!=""})`, ShapeScalar, 0, "§5.2").expect("exported_namespace var mı"),
	inst("H5.3c", PackH, ScopeHub, `count(argocd_app_sync_total{dry_run!=""})`, ShapeScalar, 0, "dec 28").expect("dry_run (v3.1+)"),
	inst("H5.3d", PackH, ScopeHub, `count(argocd_app_sync_total{exported_namespace!=""})`, ShapeScalar, 0, "§5.3").expect("sync serisinde exported_namespace"),
	inst("H5.3e", PackH, ScopeHub, `count by (namespace, version) (argocd_info)`, ShapeRows, 50, "dec 28").tok("namespace").expect("instance başına sürüm (v2.13+)"),
	inst("H5.4a", PackH, ScopeHub, `count(group by (name) (argocd_app_info))`, ShapeScalar, 0, "§10.2").expect("distinct name"),
	inst("H5.4b", PackH, ScopeHub, `count(group by (project) (argocd_app_info))`, ShapeScalar, 0, "§10.2").expect("distinct project"),
	inst("H5.4c", PackH, ScopeHub, `count(group by (repo) (argocd_app_info))`, ShapeScalar, 0, "§10.2").expect("distinct repo"),
	inst("H5.4d", PackH, ScopeHub, `count(group by (dest_server) (argocd_app_info))`, ShapeScalar, 0, "§10.2").expect("distinct dest_server"),
	inst("H5.4e", PackH, ScopeHub, `count(group by (dest_namespace) (argocd_app_info))`, ShapeScalar, 0, "§10.2").expect("distinct dest_namespace"),
	inst("H5.4f", PackH, ScopeHub, `count(group by (namespace) (argocd_app_info))`, ShapeScalar, 0, "§10.2").expect("distinct namespace"),
	inst("H5.4g", PackH, ScopeHub, `count(group by (job) (argocd_app_info))`, ShapeScalar, 0, "§10.2").expect("distinct job"),
	inst("H6.1", PackH, ScopeHub, `count(argocd_app_sync_total)`, ShapeScalar, 0, "P3.1").expect("sync serisi sayısı"),
	inst("H6.2a", PackH, ScopeHub, `sum by (phase) (increase(argocd_app_sync_total[1h]))`, ShapeRows, 10, "intervals.metricsS").expect("saatlik sync/faz"),
	inst("H6.2b", PackH, ScopeHub, `sum by (phase) (increase(argocd_app_sync_total{{inst}}[24h]))`, ShapeRows, 10, "dec 27").rep(RepeatPerInstance).expect("24 saatlik sync/faz (instance başına)"),
	inst("H6.3", PackH, ScopeHub, `sum by (autosync_enabled) (sum by (namespace, exported_namespace, name) (increase(argocd_app_sync_total[24h])) * on (namespace, exported_namespace, name) group_left (autosync_enabled) group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))`, ShapeRows, 5, "dec 15").expect("autosync payı"),
	inst("H6.4", PackH, ScopeHub, `count(resets(argocd_app_sync_total[24h]) > 0)`, ShapeScalar, 0, "§5.1").expect("sayaç sıfırlamaları"),
	inst("H6.5", PackH, ScopeHub, `count(`+transitionGroup+` unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 1h))`, ShapeScalar, 0, "P3.1").expect("saatlik geçiş"),
	inst("H6.6", PackH, ScopeHub, `count(`+transitionGroup+` unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 2m))`, ShapeScalar, 0, "P3.1").expect("2 dakikalık geçiş"),
	inst("H6.7", PackH, ScopeHub, `count(group by (namespace, exported_namespace, name) (argocd_app_info unless argocd_app_info{sync_status="Synced",health_status="Healthy",operation=""}))`, ShapeScalar, 0, "P3.1", "dec 2").expect("kararsız küme boyu (tik başına çekim)"),

	// ── N (hub başına; envList/suffixList boşsa hepsi atlanır) ────────────
	inst("N1", PackN, ScopeHub, `count(group by (namespace, exported_namespace, name) (argocd_app_info{name=~{{RE}}})) / count(`+appGroup+`)`, ShapeScalar, 0, "§6", "dec 16").expect("genel eşleşme oranı"),
	inst("N2", PackN, ScopeHub, `(count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info{name=~{{RE}}})) or count by (namespace) (`+appGroup+`) * 0) / count by (namespace) (`+appGroup+`)`, ShapeRows, 50, "§6").tok("namespace").expect("instance başına oran"),
	inst("N3a", PackN, ScopeHub, `count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`+bq+`.+-({{env}})-[^-]+`+bq+`}))`, ShapeScalar, 0, "dec 7").expect("bilinen env, bilinmeyen suffix"),
	inst("N3b", PackN, ScopeHub, `count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`+bq+`.+-({{sfx}})`+bq+`}))`, ShapeScalar, 0, "dec 16").expect("bilinmeyen env, bilinen suffix"),
	inst("N3c", PackN, ScopeHub, `count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`+bq+`[^-]+(-[^-]+){0,3}`+bq+`}))`, ShapeScalar, 0, "§6").expect("dört parça ya da az"),
	inst("N4", PackN, ScopeHub, `topk(20, count by (sfx) (label_replace(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}}), "sfx", "$1", "name", `+bq+`.*-([^-]+)`+bq+`)))`, ShapeRows, 20, "dec 7").tok("sfx").expect("bilinmeyen son parçalar (jetonlu)"),
	inst("N5a", PackN, ScopeHub, `topk by (dest_server) (1, count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `+bq+`.*-([^-]+)`+bq+`)))`, ShapeRows, 50, "dec 7").tok("dest_server", "sfx").expect("dest_server başına baskın son parça"),
	inst("N5b", PackN, ScopeHub, `max by (dest_server) (count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `+bq+`.*-([^-]+)`+bq+`))) / count by (dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info))`, ShapeRows, 50, "§6").tok("dest_server").expect("baskın parçanın payı"),
	inst("N6", PackN, ScopeHub, `count by (sfx) (group by (sfx, dest_server) (label_replace(argocd_app_info{name=~`+bq+`.+-({{sfx}})`+bq+`}, "sfx", "$1", "name", `+bq+`.+-({{sfx}})`+bq+`))) > 1`, ShapeRows, 50, "§6").tok("sfx").expect("bir suffix birden çok sunucuda (suffix→cluster kırılır)"),
	inst("N7", PackN, ScopeHub, `count by (namespace) (group by (namespace, env) (label_replace(argocd_app_info{name=~{{RE}}}, "env", "$1", "name", `+bq+`.+-({{env}})-[^-]+`+bq+`))) > 1`, ShapeRows, 50, "dec 16").tok("namespace").expect("instance birden çok env servis ediyor"),
	inst("L1", PackN, ScopeHub, `count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}}))`, ShapeScalar, 0, "§6").expect("eşleşmeyen ad sayısı"),
	// v0.10.979 — iç `group by` `project`i TAŞIMALI: PromQL toplama
	// listelenmeyen etiketi düşürür, dış `count by (namespace, project)`
	// aksi hâlde projesiz ns sayımına çökerdi (audit §11.5 listeleme adım 2).
	inst("L2", PackN, ScopeHub, `count by (namespace, project) (group by (namespace, exported_namespace, name, project) (argocd_app_info{name!~{{RE}}}))`, ShapeRows, 50, "§6").tok("namespace", "project").expect("eşleşmeyenler instance × proje"),
}
