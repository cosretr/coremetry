package argocd

// service_view.go — v0.10.981 — servis sayfasının GitOps sekmesi (P3.5'in
// metrics-only iskeleti; operatör: "o sekmede rolloutları argocd app info
// metriklerinden ve rollout sayfasından anlasın", 2026-09-27).
//
// SAF: ağ yok. api katmanı (internal/api/service_gitops.go) servisin iş
// yüklerini (workload_revision_activity_1m → EffectiveID) ve hub Thanos'un
// argocd_app_info vektörünü getirir; bu dosya sorgu ifadesini kurar,
// vektörü çözer ve uygulamaları iş yüklerine eşler.
//
// Eşleme (audit §6, §10.3.5; istek anında — v0.10.985'ten beri argocd-metrics
// işçisi açıkken sekme eşleyicinin tablosundan okur, bu yol bayrak kapalıyken
// ya da tabloda kenar yokken; kural fonksiyonları mapping.go'da ORTAK):
//   - manual (güven 100): Settings.Pins'te iş yükü (cluster, ns, workload)
//     ↔ (instance, appNamespace, appName) kenarı. İnsan pini otomatiği
//     yener: pinli iş yükü için ad tahmini yapılmaz.
//   - name (tahmini, Mapping.NameConfidence): uygulama adı tire sınırlı
//     bir parça olarak iş yükü adını içerir (<prefix>-<team>-<component>-
//     <env>-<suffix>'te component) VE dest_namespace iş yükünün
//     namespace'i VE dest_server çözülebiliyorsa iş yükünün kümesi.
// Aynı namespace'e deploy eden ama eşleşmeyen uygulamalar yalnız SAYILIR
// (zayıf kenar, §5.3: bir namespace'te bileşen başına bir uygulama).
//
// Kind yok: MV iş yükü türü taşımaz; pin (cluster, ns, workload) üzerinden
// eşlenir. Aynı namespace'te aynı adlı Deployment + StatefulSet nadir;
// olursa pin ikisine birden uyar. v0.10.985: eşleyicinin kenarı türü taşır
// (manual → pinin türü, name → MV workload_kind) ama pinli iş yükü kümesi
// de orada türsüzdür — iki yol aynı sonucu verir.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SyncTotalMetric — tamamlanan senkron sayacı (§5.1; başlatan/revizyon yok).
const SyncTotalMetric = "argocd_app_sync_total"

// ServiceAppCap — hub başına okunan en çok uygulama (§5.4 kural 2: topk).
const ServiceAppCap = 50

// ServiceSelectorCap — sorguya giren en çok namespace / pin adı (ifade boyu).
const ServiceSelectorCap = 50

// ServiceWorkload — servisin bir iş yükü; ClusterID EffectiveID.
type ServiceWorkload struct {
	ClusterID string `json:"clusterId"`
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
}

// AppStatus — argocd_app_info'nun bir uygulaması (hub başına tekil:
// (instanceNamespace, appNamespace, name)).
type AppStatus struct {
	HubClusterID      string `json:"hubClusterId"`
	InstanceNamespace string `json:"instanceNamespace,omitempty"`
	Job               string `json:"job,omitempty"`
	AppNamespace      string `json:"appNamespace"`
	Name              string `json:"name"`
	Project           string `json:"project,omitempty"`
	Repo              string `json:"repo,omitempty"`
	DestServer        string `json:"destServer,omitempty"`
	DestNamespace     string `json:"destNamespace,omitempty"`
	SyncStatus        string `json:"syncStatus,omitempty"`
	HealthStatus      string `json:"healthStatus,omitempty"`
	Operation         string `json:"operation,omitempty"`
	// AutoSync — nil: etiket yok (Argo < 2.9).
	AutoSync *bool `json:"autoSync,omitempty"`
}

// Eşleme yöntemleri.
const (
	MatchManual = "manual"
	MatchName   = "name"
)

// ServiceApp — servise eşlenen bir uygulama.
type ServiceApp struct {
	AppStatus
	InstanceID   string            `json:"instanceId,omitempty"`
	InstanceName string            `json:"instanceName,omitempty"`
	DestCluster  string            `json:"destClusterId,omitempty"` // dest_server → EffectiveID ("" = çözülemedi)
	Match        string            `json:"match"`                   // manual | name
	Confidence   int               `json:"confidence"`
	Workloads    []ServiceWorkload `json:"workloads"`
	// Syncs24h — son 24 saatte tamamlanan senkronlar, faz → adet
	// (argocd_app_sync_total increase; yuvarlanmış). nil → JSON null =
	// okunmadı (hata / kesik sonuç); {} = okundu, senkron yok. omitempty
	// YOK: boş harita düşerdi ve "yok" ile "okunmadı" ayrılamazdı.
	Syncs24h map[string]int `json:"syncs24h"`
}

// appInfoBy — kopyaları (shard/HA pod, instance) katlayan etiket kümesi.
const appInfoBy = `max by (job, namespace, exported_namespace, name, project, repo, dest_server, dest_namespace, sync_status, health_status, operation, autosync_enabled)`

// ServiceAppQuery — SAF: pinli adlar + servisin namespace'lerine deploy
// eden uygulamalar, kopyalar katlanmış (§5.4 kural 1–3). Namespace kolu
// topk ile sınırlı; pin kolu topk'SUZ (zaten ≤ ServiceSelectorCap ad) —
// değerlerin hepsi 1 olduğundan ortak bir topk pinli uygulamayı keyfi
// düşürebilirdi (v0.10.981 inceleme: "insan pini otomatiği yener").
// İkisi de boşsa "" (sorgu yok).
func ServiceAppQuery(destNamespaces, pinnedNames []string) string {
	var parts []string
	if re := promRegexAlt(pinnedNames); re != "" {
		parts = append(parts, appInfoBy+` (`+AppInfoMetric+`{name=~"`+re+`"})`)
	}
	if re := promRegexAlt(destNamespaces); re != "" {
		parts = append(parts, fmt.Sprintf(`topk(%d, %s (%s{dest_namespace=~"%s"}))`, ServiceAppCap, appInfoBy, AppInfoMetric, re))
	}
	return strings.Join(parts, " or ")
}

// ServiceSyncQuery — SAF: eşlenen uygulamaların son 24 saatlik tamamlanan
// senkronları, faz başına. names boşsa "".
func ServiceSyncQuery(names []string) string {
	re := promRegexAlt(names)
	if re == "" {
		return ""
	}
	return `sum by (namespace, exported_namespace, name, phase) (increase(` + SyncTotalMetric + `{name=~"` + re + `"}[24h]))`
}

// promRegexAlt — tekil, sıralı, boşsuz değerlerden tam eşleşen PromQL
// alternasyonu (QuoteMeta + dize kaçışı); ServiceSelectorCap'te kesilir.
func promRegexAlt(vals []string) string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) > ServiceSelectorCap {
		out = out[:ServiceSelectorCap]
	}
	for i, v := range out {
		out[i] = promStringEscaper.Replace(regexp.QuoteMeta(v))
	}
	return strings.Join(out, "|")
}

// ParseAppInfoVector — SAF: ServiceAppQuery sonucu (vector) → uygulamalar,
// (instanceNamespace, appNamespace, name) başına ilk satır (durum geçişinde
// iki seri görünebilir). app_ns = exported_namespace, yoksa namespace;
// instance_ns yalnız exported_namespace varken namespace (§5.2).
func ParseAppInfoVector(hubID string, raw json.RawMessage) ([]AppStatus, error) {
	var rows []struct {
		Metric map[string]string `json:"metric"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("vector çözülemedi: %w", err)
	}
	// Durum geçişinde bir uygulama iki seriyle görünebilir; Prometheus sonuç
	// sırası sabit değil — etiket dizgisine göre sırala ki seçilen seri
	// yenilemeler arasında oynamasın.
	keyOf := func(m map[string]string) string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var b strings.Builder
		for _, k := range ks {
			b.WriteString(k + "=" + m[k] + "\x00")
		}
		return b.String()
	}
	sort.SliceStable(rows, func(i, j int) bool { return keyOf(rows[i].Metric) < keyOf(rows[j].Metric) })
	seen := map[[3]string]bool{}
	out := []AppStatus{}
	for _, r := range rows {
		m := r.Metric
		a := AppStatus{
			HubClusterID: hubID, Job: m["job"], Name: m["name"], Project: m["project"], Repo: m["repo"],
			DestServer: m["dest_server"], DestNamespace: m["dest_namespace"],
			SyncStatus: m["sync_status"], HealthStatus: m["health_status"], Operation: m["operation"],
		}
		a.AppNamespace, a.InstanceNamespace = appNamespaceOf(m) // §5.2 (v0.10.983: işçiyle tek kural)
		if v, ok := m["autosync_enabled"]; ok && v != "" {
			b := v == "true"
			a.AutoSync = &b
		}
		if a.Name == "" {
			continue
		}
		k := [3]string{a.InstanceNamespace, a.AppNamespace, a.Name}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out, nil
}

// ParseSyncVector — SAF: ServiceSyncQuery sonucu → (appNamespace, name) →
// faz → yuvarlanmış adet (sıfırlar atılır).
func ParseSyncVector(raw json.RawMessage) (map[[2]string]map[string]int, error) {
	var rows []struct {
		Metric map[string]string `json:"metric"`
		Value  []json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("vector çözülemedi: %w", err)
	}
	out := map[[2]string]map[string]int{}
	for _, r := range rows {
		if len(r.Value) != 2 {
			continue
		}
		var s string
		if json.Unmarshal(r.Value[1], &s) != nil {
			continue
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f != f || f < 0.5 || f > 1e9 {
			continue
		}
		ns := r.Metric["exported_namespace"]
		if ns == "" {
			ns = r.Metric["namespace"]
		}
		k := [2]string{ns, r.Metric["name"]}
		if out[k] == nil {
			out[k] = map[string]int{}
		}
		out[k][r.Metric["phase"]] += int(f + 0.5)
	}
	return out, nil
}

// ServiceMatchInput — MatchServiceApps girdisi.
type ServiceMatchInput struct {
	Apps      []AppStatus // tüm hub'lardan
	Workloads []ServiceWorkload
	Settings  Settings // Normalized
	// ClusterByServer — normalleşmiş dest_server → EffectiveID (Remote
	// Cluster apiServerUrls). InClusterServer burada DEĞİL: hub'a çözülür.
	ClusterByServer map[string]string
	// NormalizeServer — dest_server normalleştiricisi (thanos.
	// NormalizeAPIServerURL; saf paket thanos'a bağımlı olmasın diye).
	NormalizeServer func(string) string
}

// ServiceMatchResult — eşleşenler + aynı namespace'te eşleşmeyen sayısı.
type ServiceMatchResult struct {
	Apps             []ServiceApp `json:"apps"`
	OtherInNamespace int          `json:"otherInNamespace"`
}

// MatchServiceApps — SAF: uygulamaları servisin iş yüklerine eşler (dosya
// başlığı). Sıra: manual önce, sonra ad; her grupta hub, appNamespace, ad.
func MatchServiceApps(in ServiceMatchInput) ServiceMatchResult {
	type wk = ServiceWorkload
	wset := map[wk]bool{}
	nsSet := map[string]bool{}
	for _, w := range in.Workloads {
		wset[w] = true
		nsSet[w.Namespace] = true
	}
	instByID := map[string]Instance{}
	for _, i := range in.Settings.Instances {
		instByID[i.ID] = i
	}
	// pinli iş yükü → pin listesi; pinli iş yüklerinde ad tahmini yok
	// (pinnedWorkloads / pinMatchesApp / nameEdgeMatches — mapping.go, eşleyiciyle
	// ORTAK kural; TestMapperMatchesLiveMatcher iki yolu karşılaştırır).
	allPinned := pinnedWorkloads(in.Settings.Pins)
	pinned := map[wk][]Pin{}
	for _, p := range in.Settings.Pins {
		w := wk{ClusterID: p.ClusterID, Namespace: p.Namespace, Workload: p.Workload}
		if wset[w] {
			pinned[w] = append(pinned[w], p)
		}
	}
	instanceOf := func(a AppStatus) (Instance, bool) {
		for _, i := range in.Settings.Instances {
			if i.HubClusterID != a.HubClusterID {
				continue
			}
			if a.InstanceNamespace != "" && i.HubNamespace == a.InstanceNamespace {
				return i, true
			}
			if a.InstanceNamespace == "" && ((i.MetricsJob != "" && i.MetricsJob == a.Job) || i.HubNamespace == a.AppNamespace) {
				return i, true
			}
		}
		return Instance{}, false
	}
	// v0.10.983 inceleme: bu yol (/api/services/{name}/gitops'un canlı dalı)
	// argocd bayrağına bağlı DEĞİL — işçinin geniş küme-içi kuralı
	// (DestClusterID: IsInClusterServer, :443 / sondaki "/" / .cluster.local)
	// buraya taşınsaydı bayrak kapalıyken GitOps sekmesinin eşleşmesi
	// değişirdi. Yalnız TAM InClusterServer yazımı hub'a çözülür (v0.10.981
	// davranışı). v0.10.985: geniş kural eşleyicide (BuildMapping) — o yol
	// metricsWorker bayrağının arkasında. Test: TestMatchServiceAppsInClusterExactOnly.
	destCluster := func(a AppStatus) string {
		if a.DestServer == "" {
			return ""
		}
		if a.DestServer == InClusterServer {
			return a.HubClusterID
		}
		ds := a.DestServer
		if in.NormalizeServer != nil {
			ds = in.NormalizeServer(ds)
		}
		return in.ClusterByServer[ds]
	}
	res := ServiceMatchResult{Apps: []ServiceApp{}}
	for _, a := range in.Apps {
		inst, instOK := instanceOf(a)
		dc := destCluster(a)
		app := ServiceApp{AppStatus: a, DestCluster: dc, Workloads: []ServiceWorkload{}}
		appInst := ""
		if instOK {
			app.InstanceID, app.InstanceName = inst.ID, inst.Name
			appInst = inst.ID
		}
		// manual
		for w, ps := range pinned {
			for _, p := range ps {
				pi, ok := instByID[p.InstanceID]
				if !pinMatchesApp(p, pi, ok, a, appInst) {
					continue
				}
				app.Workloads = append(app.Workloads, w)
				app.Match, app.Confidence = MatchManual, 100
				if !instOK {
					app.InstanceID, app.InstanceName = pi.ID, pi.Name
				}
			}
		}
		// name (yalnız pinsiz iş yükleri; manual eşleşen uygulamaya ek iş
		// yükü de ad ile eklenmez — güven karışmasın)
		if app.Match == "" {
			for _, w := range in.Workloads {
				if allPinned[w] || !nameEdgeMatches(a.Name, a.DestNamespace, dc, w) {
					continue
				}
				app.Workloads = append(app.Workloads, w)
				app.Match, app.Confidence = MatchName, in.Settings.Mapping.NameConfidence
			}
		}
		if app.Match == "" {
			if nsSet[a.DestNamespace] {
				res.OtherInNamespace++
			}
			continue
		}
		sortWorkloads(app.Workloads)
		res.Apps = append(res.Apps, app)
	}
	sortServiceApps(res.Apps)
	return res
}

// PinnedAppNames — SAF: servisin iş yüklerine bağlı pinlerin uygulama adları
// (pinli uygulama başka namespace'e deploy etse de sorguya girsin).
func PinnedAppNames(pins []Pin, workloads []ServiceWorkload) []string {
	wset := map[ServiceWorkload]bool{}
	for _, w := range workloads {
		wset[w] = true
	}
	var out []string
	for _, p := range pins {
		if wset[ServiceWorkload{ClusterID: p.ClusterID, Namespace: p.Namespace, Workload: p.Workload}] {
			out = append(out, p.AppName)
		}
	}
	return out
}

// nameHasPart — ad, part'ı tire sınırlı bir parça dizisi olarak içerir
// ("pay-team-checkout-api-prod-a" ⊇ "checkout-api"; "checkout" ⊉ "check").
func nameHasPart(name, part string) bool {
	name, part = strings.ToLower(name), strings.ToLower(part)
	if part == "" {
		return false
	}
	return strings.Contains("-"+name+"-", "-"+part+"-")
}

func sortWorkloads(ws []ServiceWorkload) {
	sort.Slice(ws, func(i, j int) bool {
		if ws[i].ClusterID != ws[j].ClusterID {
			return ws[i].ClusterID < ws[j].ClusterID
		}
		if ws[i].Namespace != ws[j].Namespace {
			return ws[i].Namespace < ws[j].Namespace
		}
		return ws[i].Workload < ws[j].Workload
	})
}
