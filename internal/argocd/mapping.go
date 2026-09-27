package argocd

// mapping.go — v0.10.985 — ROLLOUTS v2 P3.2: Argo uygulaması ↔ iş yükü
// eşleyicisinin SAF çekirdeği (docs/rollouts/v2-audit.md §6, §10.3.5, §10.4,
// §10.6 "Service Argo card", §12.1 P3.2/P3.5).
//
// Ağ yok, CH yok: argocd_app_mapping satır modeli + yazıcı sözleşmesi, ad
// ayrıştırıcı (§6: envList + argoSuffix), kenar üretimi (BuildMapping),
// tablodaki canlı kenarlarla uzlaştırma (ReconcileMapping) ve servis
// kartının kenarlardan kurulması (ServiceAppsFromEdges). İşçi adımı
// metrics_worker.go'da (mapper), okuma internal/api/service_gitops_mapper.go.
//
// ── YÖNTEMLER (§10.3.5) ──────────────────────────────────────────────────
//
//   - manual (kesin, 100): Settings.Pins. İnsan pini otomatiği yener: pinli
//     (cluster, ns, workload) için ad tahmini ÜRETİLMEZ; servis kartında
//     manual kenarı olan uygulamanın o servisteki ad kenarları düşer.
//   - name (tahmini, mapping.nameConfidence): uygulama adı iş yükü adını tire
//     sınırlı parça olarak içerir VE dest_namespace iş yükünün namespace'i VE
//     dest_server çözülebiliyorsa iş yükünün kümesi.
//   - namespace (zayıf, mapping.namespaceConfidence): workload_kind/workload
//     boş kenar; dest_server çözülen her uygulama için (cluster, dest_ns)
//     altında, candidates = o (cluster, ns)'e deploy eden uygulama sayısı.
//     Servis kartında yalnız SAYILIR ("aynı namespace'te eşleşmeyen N").
//   - resource (kesin) yalnız Argo CD API'sinden gelir (P3.3, askıda); bu
//     eşleyici yazmaz. pod_label (tahmini) §11 K2.4'ü bekliyor; yazmaz.
//
// ── NEDEN manual/name v0.10.981 ile AYNI çekirdek ────────────────────────
//
// GitOps sekmesi (/api/services/{name}/gitops) bayrak açıkken bu tablodan
// okur; sekmenin ANLAMI değişmesin diye iki yol tek kural fonksiyonunu
// paylaşır (pinMatchesApp, nameEdgeMatches, pinnedWorkloads) ve
// TestMapperMatchesLiveMatcher iki yolun aynı sonucu verdiğini pinler.
// Bilinçli farklar (DECISIONS P3.2): eşleyici küme-içi adresi GENİŞ kuralla
// (IsInClusterServer: :443, sondaki "/", .cluster.local) hub'a çözer — canlı
// yol bayraksız olduğu için TAM yazımda kalır (v0.10.983 inceleme); dest_server'ı
// çözülemeyen eşleşmemiş uygulama zayıf kenar almaz, sayıya girmez.
//
// ── AD AYRIŞTIRMA (§6) ───────────────────────────────────────────────────
//
// `<prefix>-<team>-<component>-<env>-<suffix>`; env listesi ve suffix'ler
// ayardan (envList + Remote Cluster argoSuffix birleşimi), QuoteMeta ile
// kaçışlı, sona çapalı. Ayrıştırma KENARI DEĞİŞTİRMEZ (sekme anlamı): yalnız
// teşhis — ayrışan/ayrışmayan ad, suffix ↔ dest_server kümesinin argoSuffix
// tutarsızlığı (§6 adım 4: "işaretlenir, eşlemeyi değiştirmez"), ad kenarının
// component'e TAM mı yoksa yalnız parçaya mı dayandığı. base key (+ pairGroup)
// çift uyarısı P3.5'in işi.

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Eşleme yöntemleri (MatchManual / MatchName service_view.go'da) ve sınıfları.
const (
	MatchResource  = "resource"
	MatchPodLabel  = "pod_label"
	MatchNamespace = "namespace"

	ClassExact     = "exact"
	ClassEstimated = "estimated"
	ClassWeak      = "weak"
)

// MatchClassOf — yöntemin sınıfı (§10.3.5); bilinmeyen yöntem "".
func MatchClassOf(method string) string {
	switch method {
	case MatchManual, MatchResource:
		return ClassExact
	case MatchName, MatchPodLabel:
		return ClassEstimated
	case MatchNamespace:
		return ClassWeak
	}
	return ""
}

// MappingRow — argocd_app_mapping TAM satırı; alan sırası ve `ch` etiketleri
// migrations/0015 (≡ rollout_v2_schema.go) kolonlarıyla BİREBİR
// (TestMappingRowMirrorsDDL). Tam satır değiştirme (invariant #4).
type MappingRow struct {
	ClusterID      string    `ch:"cluster_id"`
	Namespace      string    `ch:"namespace"`
	WorkloadKind   string    `ch:"workload_kind"` // zayıf kenarda ''
	Workload       string    `ch:"workload"`      // zayıf kenarda ''
	InstanceID     string    `ch:"instance_id"`
	AppNamespace   string    `ch:"app_namespace"`
	AppName        string    `ch:"app_name"`
	MatchMethod    string    `ch:"match_method"`
	MatchClass     string    `ch:"match_class"`
	Confidence     uint8     `ch:"confidence"`
	Candidates     uint16    `ch:"candidates"`
	FirstMatchedAt time.Time `ch:"first_matched_at"` // taşınır
	LastVerifiedAt time.Time `ch:"last_verified_at"` // TTL çapası
	RemovedAt      time.Time `ch:"removed_at"`       // sıfır = canlı
	Version        uint64    `ch:"version"`
}

// MappingColumns — INSERT/SELECT kolon listesi, MappingRow sırasıyla.
func MappingColumns() []string {
	rt := reflect.TypeOf(MappingRow{})
	out := make([]string, rt.NumField())
	for i := range out {
		out[i] = rt.Field(i).Tag.Get("ch")
	}
	return out
}

// EdgeKey — kenar anahtarı = tablonun ORDER BY'ı.
type EdgeKey struct {
	ClusterID, Namespace, WorkloadKind, Workload string
	InstanceID, AppNamespace, AppName            string
}

// Key — satırın kenar anahtarı.
func (r MappingRow) Key() EdgeKey {
	return EdgeKey{r.ClusterID, r.Namespace, r.WorkloadKind, r.Workload, r.InstanceID, r.AppNamespace, r.AppName}
}

// App — kenarın uygulama anahtarı.
func (k EdgeKey) App() AppKey { return AppKey{k.InstanceID, k.AppNamespace, k.AppName} }

func edgeKeyLess(a, b EdgeKey) bool {
	x := [7]string{a.ClusterID, a.Namespace, a.WorkloadKind, a.Workload, a.InstanceID, a.AppNamespace, a.AppName}
	y := [7]string{b.ClusterID, b.Namespace, b.WorkloadKind, b.Workload, b.InstanceID, b.AppNamespace, b.AppName}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func epochish(t time.Time) bool { return t.IsZero() || t.Unix() <= 0 }

// ValidateMappingRow — yazıcı sözleşmesi (SAF); nil = yazılabilir. TTL çapası
// last_verified_at ve taşınan first_matched_at sıfır/epoch olamaz (1970'e
// yazılan satır ilk TTL birleşmesinde düşer — rollout_v2_schema.go), anahtar
// alanları boş olamaz, yöntem sözlükte ve sınıfı yöntemine uygun, zayıf kenar
// İŞ YÜKÜSÜZ (kind + workload boş) ve yalnız zayıf kenar öyle, güven 1..100,
// candidates ≥ 1, removed_at sıfır ya da gerçek zaman, açık version.
func ValidateMappingRow(r MappingRow) error {
	var errs []error
	if epochish(r.LastVerifiedAt) {
		errs = append(errs, errors.New("last_verified_at sıfır/epoch (TTL çapası)"))
	}
	if epochish(r.FirstMatchedAt) {
		errs = append(errs, errors.New("first_matched_at sıfır/epoch"))
	}
	if !r.RemovedAt.IsZero() && r.RemovedAt.Unix() <= 0 {
		errs = append(errs, errors.New("removed_at epoch (sıfır ya da gerçek zaman)"))
	}
	for _, f := range []struct{ name, v string }{
		{"cluster_id", r.ClusterID}, {"namespace", r.Namespace}, {"instance_id", r.InstanceID},
		{"app_namespace", r.AppNamespace}, {"app_name", r.AppName},
	} {
		if strings.TrimSpace(f.v) == "" {
			errs = append(errs, fmt.Errorf("%s boş", f.name))
		}
	}
	class := MatchClassOf(r.MatchMethod)
	switch {
	case class == "":
		errs = append(errs, fmt.Errorf("match_method %q sözlük dışı", r.MatchMethod))
	case r.MatchClass != class:
		errs = append(errs, fmt.Errorf("match_class %q, %s yönteminin sınıfı %s", r.MatchClass, r.MatchMethod, class))
	}
	weak := r.MatchMethod == MatchNamespace
	switch {
	case weak && (r.Workload != "" || r.WorkloadKind != ""):
		errs = append(errs, errors.New("namespace (zayıf) kenarı iş yükü taşımaz"))
	case !weak && (strings.TrimSpace(r.Workload) == "" || strings.TrimSpace(r.WorkloadKind) == ""):
		errs = append(errs, errors.New("iş yükü kenarı workload + workload_kind ister"))
	}
	if r.Confidence < 1 || r.Confidence > 100 {
		errs = append(errs, fmt.Errorf("confidence %d (1..100)", r.Confidence))
	}
	if r.Candidates < 1 {
		errs = append(errs, errors.New("candidates < 1"))
	}
	if r.Version == 0 {
		errs = append(errs, errors.New("version sıfır (açık istemci version'ı)"))
	}
	return errors.Join(errs...)
}

// ── Ortak kural fonksiyonları (canlı matcher + eşleyici) ─────────────────

// pinnedWorkloads — pini olan (cluster, ns, workload) kümesi; tür yok sayılır
// (canlı yol MV'den türsüz iş yükü görür). Uygulaması olmasa da pin iş yükünü
// ad tahmininden çıkarır (v0.10.981 davranışı).
func pinnedWorkloads(pins []Pin) map[ServiceWorkload]bool {
	out := make(map[ServiceWorkload]bool, len(pins))
	for _, p := range pins {
		out[ServiceWorkload{ClusterID: p.ClusterID, Namespace: p.Namespace, Workload: p.Workload}] = true
	}
	return out
}

// pinMatchesApp — manual kenar kuralı: pinin instance'ı ayarda var ve
// uygulamanın hub'ında, uygulama ad + namespace'i pininkiyle aynı; uygulamanın
// instance'ı çözülebildiyse pinin instance'ı o olmalı (appInstanceID "" =
// canlı yolda instance çözülemedi).
func pinMatchesApp(p Pin, pinInst Instance, pinInstOK bool, a AppStatus, appInstanceID string) bool {
	if !pinInstOK || pinInst.HubClusterID != a.HubClusterID || p.AppName != a.Name || p.AppNamespace != a.AppNamespace {
		return false
	}
	return appInstanceID == "" || appInstanceID == p.InstanceID
}

// nameEdgeMatches — name kenar kuralı (pinli iş yükü çağırandan elenir):
// dest_namespace = iş yükü ns'i, ad iş yükü adını tire sınırlı parça olarak
// içerir, dest_server çözüldüyse (destCluster ≠ "") iş yükünün kümesi.
func nameEdgeMatches(appName, destNamespace, destCluster string, w ServiceWorkload) bool {
	if destNamespace != w.Namespace || !nameHasPart(appName, w.Workload) {
		return false
	}
	return destCluster == "" || destCluster == w.ClusterID
}

// sortServiceApps — sekme sırası: manual önce, sonra hub, appNamespace, ad.
func sortServiceApps(apps []ServiceApp) {
	sort.SliceStable(apps, func(i, j int) bool {
		x, y := apps[i], apps[j]
		if (x.Match == MatchManual) != (y.Match == MatchManual) {
			return x.Match == MatchManual
		}
		if x.HubClusterID != y.HubClusterID {
			return x.HubClusterID < y.HubClusterID
		}
		if x.AppNamespace != y.AppNamespace {
			return x.AppNamespace < y.AppNamespace
		}
		return x.Name < y.Name
	})
}

// ── Ad ayrıştırıcı (§6) ───────────────────────────────────────────────────

// ParsedName — `<prefix>-<team>-<component>-<env>-<suffix>` parçaları.
// Tireli takım adı ayrışmaz: fazladan parça component'e kayar ("tahmini").
type ParsedName struct {
	Prefix, Team, Component, Env, Suffix string
}

// BaseKey — §6: `<prefix>-<team>-<component>-<env>`; çift anahtarı
// (BaseKey, pairGroup) P3.5'te.
func (p ParsedName) BaseKey() string {
	return p.Prefix + "-" + p.Team + "-" + p.Component + "-" + p.Env
}

// NameParser — envList + suffix'lerden kurulmuş çapalı düzenli ifade.
type NameParser struct{ re *regexp.Regexp }

// quotedAlt — küçük harf, tekil, boşsuz, QuoteMeta'lı; uzun olan önce (sıra
// yalnız belirlenimlilik için: ifade iki uçtan çapalı).
func quotedAlt(vals []string) string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vals {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	for i, v := range out {
		out[i] = regexp.QuoteMeta(v)
	}
	return strings.Join(out, "|")
}

// NewNameParser — env ya da suffix listesi boşsa nil (ayrıştırma yok; §6
// "hiçbir şey sabit kodlanmaz").
func NewNameParser(envs, suffixes []string) *NameParser {
	e, s := quotedAlt(envs), quotedAlt(suffixes)
	if e == "" || s == "" {
		return nil
	}
	re, err := regexp.Compile(`^([a-z0-9]+)-([a-z0-9]+)-([a-z0-9-]+?)-(` + e + `)-(` + s + `)$`)
	if err != nil {
		return nil
	}
	return &NameParser{re: re}
}

// Parse — ad ayrışırsa parçalar (küçük harf) + true. nil ayrıştırıcı false.
func (np *NameParser) Parse(name string) (ParsedName, bool) {
	if np == nil {
		return ParsedName{}, false
	}
	m := np.re.FindStringSubmatch(strings.ToLower(strings.TrimSpace(name)))
	if m == nil {
		return ParsedName{}, false
	}
	return ParsedName{Prefix: m[1], Team: m[2], Component: m[3], Env: m[4], Suffix: m[5]}, true
}

// ── Kenar üretimi ─────────────────────────────────────────────────────────

// MapperApp — hazır bir instance'ın bellekteki (silinmemiş) uygulaması.
type MapperApp struct {
	InstanceID    string
	HubClusterID  string
	AppNamespace  string
	Name          string
	DestServer    string
	DestNamespace string
}

// MapperWorkload — gözlenen iş yükü; ClusterID EffectiveID, Kind MV'nin
// workload_kind'ı (Deployment | StatefulSet | DaemonSet).
type MapperWorkload struct {
	ClusterID, Namespace, Kind, Workload string
}

// MapperInput — BuildMapping girdisi.
type MapperInput struct {
	Apps      []MapperApp
	Workloads []MapperWorkload
	Settings  Settings // Normalized
	// Ready — bu turda uzlaştırılan instance'lar. Hazır olmayan instance'ın
	// pini üretilmez (uygulaması görülmedi) ve tablodaki kenarlarına dokunulmaz.
	Ready     map[string]bool
	ByServer  map[string]string // normalleşmiş apiServerUrl → EffectiveID
	Normalize func(string) string
	// SuffixByCluster — EffectiveID → argoSuffix (§6 adım 4 tutarlılık denetimi).
	SuffixByCluster map[string]string
}

// MapperEdge — üretilen kenar (tabloya yazılmadan önce).
type MapperEdge struct {
	Key        EdgeKey
	Method     string
	Confidence int
	Candidates int
}

// MapperResult — kenarlar (anahtar sırasında) + iş yükü kenarı almayan
// uygulama sayısı + teşhis.
type MapperResult struct {
	Edges        []MapperEdge
	UnmappedApps int
	Diag         map[string]int
}

// BuildMapping — SAF: hazır instance'ların uygulamaları + gözlenen iş
// yükleri + pinler → kenarlar (dosya başı "YÖNTEMLER").
func BuildMapping(in MapperInput) MapperResult {
	res := MapperResult{Diag: map[string]int{}}
	set := in.Settings
	instByID := make(map[string]Instance, len(set.Instances))
	for _, i := range set.Instances {
		instByID[i.ID] = i
	}
	pinned := pinnedWorkloads(set.Pins)
	var suffixes []string
	for _, s := range in.SuffixByCluster {
		suffixes = append(suffixes, s)
	}
	parser := NewNameParser(set.EnvList, suffixes)
	if parser == nil {
		res.Diag["name_parse_unconfigured"]++
	}

	// İş yükleri namespace'e göre; türü boş olan (MV sözleşmesi dışı) atlanır.
	byNS := map[string][]MapperWorkload{}
	seenW := map[ServiceWorkload]bool{}
	for _, w := range in.Workloads {
		sw := ServiceWorkload{ClusterID: w.ClusterID, Namespace: w.Namespace, Workload: w.Workload}
		switch {
		case w.ClusterID == "" || w.Namespace == "" || w.Workload == "":
			res.Diag["workload_incomplete"]++
			continue
		case strings.TrimSpace(w.Kind) == "":
			res.Diag["workload_kind_empty"]++
			continue
		case seenW[sw]:
			continue
		}
		seenW[sw] = true
		byNS[w.Namespace] = append(byNS[w.Namespace], w)
	}

	// Pinler uygulama anahtarına göre; hazır olmayan instance'ınki sayılır.
	pinsByApp := map[AppKey][]Pin{}
	for _, p := range set.Pins {
		if !in.Ready[p.InstanceID] {
			res.Diag["pin_instance_not_ready"]++
			continue
		}
		k := AppKey{p.InstanceID, p.AppNamespace, p.AppName}
		pinsByApp[k] = append(pinsByApp[k], p)
	}
	pinUsed := map[AppKey]bool{}

	edges := map[EdgeKey]MapperEdge{}
	add := func(k EdgeKey, method string, conf int) {
		if _, ok := edges[k]; ok {
			return
		}
		edges[k] = MapperEdge{Key: k, Method: method, Confidence: conf, Candidates: 1}
	}
	seenApp := map[AppKey]bool{}
	for _, a := range in.Apps {
		ak := AppKey{a.InstanceID, a.AppNamespace, a.Name}
		if !in.Ready[a.InstanceID] || a.Name == "" || a.AppNamespace == "" || seenApp[ak] {
			continue
		}
		seenApp[ak] = true
		res.Diag["apps"]++
		dc := DestClusterID(a.DestServer, a.HubClusterID, in.ByServer, in.Normalize)
		switch {
		case strings.TrimSpace(a.DestServer) == "":
			res.Diag["dest_server_empty"]++
		case dc == "":
			res.Diag["dest_unresolved"]++
		}
		parsed, parsedOK := parser.Parse(a.Name)
		if parser != nil {
			if parsedOK {
				res.Diag["name_parsed"]++
				if want := in.SuffixByCluster[dc]; dc != "" && want != "" && !strings.EqualFold(want, parsed.Suffix) {
					res.Diag["suffix_mismatch"]++
				}
			} else {
				res.Diag["name_unparsed"]++
			}
		}
		st := AppStatus{HubClusterID: a.HubClusterID, AppNamespace: a.AppNamespace, Name: a.Name}
		mapped := false
		for _, p := range pinsByApp[ak] {
			pi, ok := instByID[p.InstanceID]
			if !pinMatchesApp(p, pi, ok, st, a.InstanceID) {
				continue
			}
			pinUsed[ak] = true
			mapped = true
			add(EdgeKey{p.ClusterID, p.Namespace, p.WorkloadKind, p.Workload, a.InstanceID, a.AppNamespace, a.Name}, MatchManual, 100)
		}
		for _, w := range byNS[a.DestNamespace] {
			sw := ServiceWorkload{ClusterID: w.ClusterID, Namespace: w.Namespace, Workload: w.Workload}
			if pinned[sw] || !nameEdgeMatches(a.Name, a.DestNamespace, dc, sw) {
				continue
			}
			mapped = true
			if parsedOK && strings.EqualFold(parsed.Component, w.Workload) {
				res.Diag["name_component_exact"]++
			} else {
				res.Diag["name_part_only"]++
			}
			add(EdgeKey{w.ClusterID, w.Namespace, w.Kind, w.Workload, a.InstanceID, a.AppNamespace, a.Name}, MatchName, set.Mapping.NameConfidence)
		}
		if dc != "" && a.DestNamespace != "" {
			add(EdgeKey{ClusterID: dc, Namespace: a.DestNamespace, InstanceID: a.InstanceID, AppNamespace: a.AppNamespace, AppName: a.Name},
				MatchNamespace, set.Mapping.NamespaceConfidence)
		}
		if !mapped {
			res.UnmappedApps++
		}
	}
	for k := range pinsByApp {
		if !pinUsed[k] {
			res.Diag["pin_app_missing"]++
		}
	}

	// candidates: aynı (cluster, ns) altındaki zayıf kenar (uygulama) sayısı.
	perNS := map[[2]string]int{}
	for k, e := range edges {
		if e.Method == MatchNamespace {
			perNS[[2]string{k.ClusterID, k.Namespace}]++
		}
	}
	for k, e := range edges {
		if e.Method == MatchNamespace {
			e.Candidates = perNS[[2]string{k.ClusterID, k.Namespace}]
			edges[k] = e
		}
		res.Diag["edges_"+e.Method]++
	}
	res.Edges = make([]MapperEdge, 0, len(edges))
	for _, e := range edges {
		res.Edges = append(res.Edges, e)
	}
	sort.Slice(res.Edges, func(i, j int) bool { return edgeKeyLess(res.Edges[i].Key, res.Edges[j].Key) })
	res.Diag["unmapped_apps"] = res.UnmappedApps
	return res
}

// ── Uzlaştırma ────────────────────────────────────────────────────────────

// MappingTouchEvery — değişmeyen canlı kenarın last_verified_at'i en geç bu
// kadar sonra yeniden yazılır (§10.3.5 "en az günde bir"; TTL 30 gün).
const MappingTouchEvery = 20 * time.Hour

// MappingFreshness — okuyucu yalnız last_verified_at'i bundan yeni kenarı
// canlı sayar: dokunma aralığı (20 sa) + en uzun eşleyici aralığı (4 sa) +
// pay. Hazır olmayan (hub'ı kopmuş, kaldırılmış) instance'ın kenarları
// tabloda 30 gün kalsa da sekmeye gelmez.
const MappingFreshness = 26 * time.Hour

// ReconcileInput — ReconcileMapping girdisi.
type ReconcileInput struct {
	Desired []MapperEdge
	// Existing — tablodaki CANLI (removed_at = 0) kenarlar; hazır olmayan
	// instance'ınkiler burada olsa da dokunulmaz.
	Existing    []MappingRow
	Ready       map[string]bool
	Now         time.Time
	TouchEvery  time.Duration
	NextVersion func() uint64
}

func clampConf(c int) uint8 {
	switch {
	case c < 1:
		return 1
	case c > 100:
		return 100
	}
	return uint8(c)
}

func clampCand(c int) uint16 {
	switch {
	case c < 1:
		return 1
	case c > 65535:
		return 65535
	}
	return uint16(c)
}

// ReconcileMapping — SAF: istenen kenarlar + tablodaki canlı kenarlar →
// yazılacak TAM satırlar (anahtar sırasında). Yeni kenar first_matched_at =
// şimdi; değişen (yöntem/sınıf/güven/candidates) ya da TouchEvery'den eski
// kenar first_matched_at'i TAŞIYARAK yeniden yazılır; istenmeyen canlı kenar
// (yalnız hazır instance'ta) removed_at = şimdi ile kapanır (bütün alanlar
// taşınır). Değişmeyen taze kenar yazılmaz.
func ReconcileMapping(in ReconcileInput) ([]MappingRow, map[string]int) {
	diag := map[string]int{}
	touch := in.TouchEvery
	if touch <= 0 {
		touch = MappingTouchEvery
	}
	ex := make(map[EdgeKey]MappingRow, len(in.Existing))
	for _, r := range in.Existing {
		if cur, ok := ex[r.Key()]; !ok || r.Version > cur.Version {
			ex[r.Key()] = r
		}
	}
	var out []MappingRow
	want := make(map[EdgeKey]bool, len(in.Desired))
	for _, d := range in.Desired {
		want[d.Key] = true
		row := MappingRow{
			ClusterID: d.Key.ClusterID, Namespace: d.Key.Namespace, WorkloadKind: d.Key.WorkloadKind, Workload: d.Key.Workload,
			InstanceID: d.Key.InstanceID, AppNamespace: d.Key.AppNamespace, AppName: d.Key.AppName,
			MatchMethod: d.Method, MatchClass: MatchClassOf(d.Method), Confidence: clampConf(d.Confidence),
			Candidates: clampCand(d.Candidates), FirstMatchedAt: in.Now, LastVerifiedAt: in.Now,
		}
		if cur, ok := ex[d.Key]; ok {
			same := cur.MatchMethod == row.MatchMethod && cur.MatchClass == row.MatchClass &&
				cur.Confidence == row.Confidence && cur.Candidates == row.Candidates
			if same && in.Now.Sub(cur.LastVerifiedAt) < touch {
				diag["edges_unchanged"]++
				continue
			}
			if !epochish(cur.FirstMatchedAt) {
				row.FirstMatchedAt = cur.FirstMatchedAt
			}
			if same {
				diag["edges_touched"]++
			} else {
				diag["edges_changed"]++
			}
		} else {
			diag["edges_new"]++
		}
		row.Version = in.NextVersion()
		out = append(out, row)
	}
	for k, cur := range ex {
		if want[k] || !in.Ready[k.InstanceID] {
			continue
		}
		cur.RemovedAt, cur.LastVerifiedAt = in.Now, in.Now
		if epochish(cur.FirstMatchedAt) {
			cur.FirstMatchedAt = in.Now
		}
		cur.Version = in.NextVersion()
		out = append(out, cur)
		diag["edges_removed"]++
	}
	sort.Slice(out, func(i, j int) bool { return edgeKeyLess(out[i].Key(), out[j].Key()) })
	return out, diag
}

// ── Servis kartı (okuma) ──────────────────────────────────────────────────

// ServiceEdgesInput — ServiceAppsFromEdges girdisi.
type ServiceEdgesInput struct {
	Workloads []ServiceWorkload
	// Edges — servisin (cluster, ns) çiftlerindeki canlı ve taze kenarlar.
	Edges []MappingRow
	// Statuses — uygulama başına argocd_app_status son satırı.
	Statuses map[AppKey]StatusRow
	// Syncs — son 24 saatteki 'sync' satırları, faz → adet. SyncsOK false →
	// okunmadı (Syncs24h null).
	Syncs     map[AppKey]map[string]int
	SyncsOK   bool
	Settings  Settings // Normalized
	ByServer  map[string]string
	Normalize func(string) string
}

// ServiceEdgesResult — sekme cevabı + teşhis (durum satırı olmayan / silinmiş
// uygulama gösterilmez).
type ServiceEdgesResult struct {
	ServiceMatchResult
	StatusMissing int
}

// ServiceAppsFromEdges — SAF: eşleme kenarları + son durum → canlı yolla
// (MatchServiceApps) aynı şekilde ServiceApp listesi. Servis içinde manual
// kenarı olan uygulamanın ad kenarları düşer; eşleşmeyen zayıf kenarlar
// otherInNamespace'e sayılır. Ayarda artık olmayan ya da devre dışı
// (v0.10.985 inceleme: işçi okumuyor, durumu donmuş) instance'ın kenarı atlanır.
func ServiceAppsFromEdges(in ServiceEdgesInput) ServiceEdgesResult {
	res := ServiceEdgesResult{ServiceMatchResult: ServiceMatchResult{Apps: []ServiceApp{}}}
	wset := map[ServiceWorkload]bool{}
	pairs := map[[2]string]bool{}
	for _, w := range in.Workloads {
		wset[w] = true
		pairs[[2]string{w.ClusterID, w.Namespace}] = true
	}
	instByID := map[string]Instance{}
	for _, i := range in.Settings.Instances {
		if i.Enabled {
			instByID[i.ID] = i
		}
	}
	type acc struct {
		manual, name []ServiceWorkload
		nameConf     int
	}
	byApp := map[AppKey]*acc{}
	weak := map[AppKey]bool{}
	for _, e := range in.Edges {
		if !e.RemovedAt.IsZero() {
			continue
		}
		if _, ok := instByID[e.InstanceID]; !ok {
			continue
		}
		ak := e.Key().App()
		if e.MatchMethod == MatchNamespace {
			if pairs[[2]string{e.ClusterID, e.Namespace}] {
				weak[ak] = true
			}
			continue
		}
		w := ServiceWorkload{ClusterID: e.ClusterID, Namespace: e.Namespace, Workload: e.Workload}
		if !wset[w] {
			continue
		}
		a := byApp[ak]
		if a == nil {
			a = &acc{}
			byApp[ak] = a
		}
		switch e.MatchMethod {
		case MatchManual:
			a.manual = append(a.manual, w)
		case MatchName:
			a.name = append(a.name, w)
			if a.nameConf == 0 || int(e.Confidence) < a.nameConf {
				a.nameConf = int(e.Confidence)
			}
		}
	}
	for _, ak := range sortedAppKeys(byApp) {
		a := byApp[ak]
		st, ok := in.Statuses[ak]
		if !ok || st.ChangeKind == ChangeDeleted {
			res.StatusMissing++
			continue
		}
		inst := instByID[ak.InstanceID]
		app := ServiceApp{
			AppStatus: AppStatus{
				HubClusterID: inst.HubClusterID, InstanceNamespace: inst.HubNamespace, AppNamespace: ak.AppNamespace, Name: ak.Name,
				Project: st.Project, Repo: st.Repo, DestServer: st.DestServer, DestNamespace: st.DestNamespace,
				SyncStatus: st.SyncStatus, HealthStatus: st.HealthStatus, Operation: st.Operation,
			},
			InstanceID: inst.ID, InstanceName: inst.Name,
			DestCluster: DestClusterID(st.DestServer, inst.HubClusterID, in.ByServer, in.Normalize),
		}
		switch st.AutoSync {
		case "true", "false":
			b := st.AutoSync == "true"
			app.AutoSync = &b
		}
		if len(a.manual) > 0 {
			app.Match, app.Confidence, app.Workloads = MatchManual, 100, dedupWorkloads(a.manual)
		} else {
			app.Match, app.Confidence, app.Workloads = MatchName, a.nameConf, dedupWorkloads(a.name)
		}
		if in.SyncsOK {
			m := map[string]int{}
			for ph, n := range in.Syncs[ak] {
				m[ph] = n
			}
			app.Syncs24h = m
		}
		res.Apps = append(res.Apps, app)
	}
	shown := map[AppKey]bool{}
	for _, a := range res.Apps {
		shown[AppKey{a.InstanceID, a.AppNamespace, a.Name}] = true
	}
	for ak := range weak {
		if _, matched := byApp[ak]; !matched && !shown[ak] {
			res.OtherInNamespace++
		}
	}
	sortServiceApps(res.Apps)
	return res
}

func dedupWorkloads(ws []ServiceWorkload) []ServiceWorkload {
	seen := map[ServiceWorkload]bool{}
	out := []ServiceWorkload{}
	for _, w := range ws {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	sortWorkloads(out)
	return out
}
