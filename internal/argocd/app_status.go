package argocd

// app_status.go — v0.10.983 — ROLLOUTS v2 P3.1: argocd-metrics işçisinin
// SAF çekirdeği (docs/rollouts/v2-audit.md §5.1–5.4, §10.3.3, §10.4).
//
// Ağ yok, CH yok: argocd_app_status satır modeli + yazıcı sözleşmesi, hub
// sorgularının kurulumu, vektör çözücüler, senkron sayaç farkı ve durum
// diff'i. İşçi (metrics_worker.go) bunları I/O arayüzleriyle sürer.
//
// ── ANAHTAR (§5.2) ───────────────────────────────────────────────────────
//
// Uygulama anahtarı (instance, app_ns, name); `name` TEK BAŞINA ASLA anahtar
// değil. app_ns = exported_namespace doluysa o, yoksa namespace (ServiceMonitor
// varsayılan honorLabels'ında namespace = instance ns, exported_namespace =
// Application ns; honorLabels:true'da exported yok ve namespace = Application
// ns). Parça (shard) seçicisi instance namespace'i (+ tanımlıysa job): durum
// A/C'de doğrudan instance'ın serileri; durum B'de (exported yok) yalnız
// instance namespace'indeki Application'lar gelir — apps-in-any-namespace +
// honorLabels:true birleşimi KAPSANMAZ (eksik kapsama, yanlış satır değil;
// §11 H1.2 doğrulayacak).
//
// ── KOPYALAR (§5.4 kural 3) ──────────────────────────────────────────────
//
// Controller shard'ları ve HA replikaları seriyi çoğaltır: okuma `max by`
// (appInfoBy — pod/instance YOK) + dedup=true. Aynı anahtar yine de iki
// FARKLI durumla gelirse (durum geçişinde iki seri, shard yeniden dağılımı)
// uygulama o tikte BELİRSİZ sayılır: gözlem değildir, önceki durumu korunur,
// yokluğu sayılmaz (Ambiguous; teşhiste app_ambiguous). Keyfi seçim yanlış
// bir 'state' satırı yazabilirdi.
//
// ── SENKRON (§5.1) ───────────────────────────────────────────────────────
//
// argocd_app_sync_total yalnız TAMAMLANINCA artar; başlatan/revizyon yok;
// seri controller yeniden başlayınca ilk tamamlanan senkronla YENİDEN doğar.
// Artış bellekteki son değere göre: büyümüşse fark, küçülmüşse sıfırlanma
// (yeni değer kadar), bellekte yoksa ve bu parçanın TAM sayaç tabanı
// alınmışsa yeni doğan seri (değeri kadar). Taban yokken yeni seri sayılmaz
// (fail-safe: failover penceresindeki senkron kaybolur, uydurulmaz).
//
// ── YAZICI SÖZLEŞMESİ (§10.3.3, rollout_v2_schema.go) ────────────────────
//
// Anahtar (instance, app_ns, app, changed_at); change_kind anahtarda DEĞİL.
// Uygulama başına tik başına EN FAZLA BİR satır: aynı tikte senkron bitişi
// ile durum değişimi / appeared birlikteyse TEK 'sync' satırı yeni tuple'ı ve
// sync_phase'i taşır. changed_at = tik zamanı, tik aralığına kesik (iki lider
// aynı aralıkta aynı değişimi görürse aynı anahtar → RMT birleştirir).

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// change_kind sözlüğü (§10.3.3).
const (
	ChangeBaseline = "baseline" // ilk koşu / yeniden kurulumdan sonraki ilk tam envanterde bilinmeyen uygulama
	ChangeAppeared = "appeared" // tam envanter tabanından SONRA ilk kez görülen (ya da silinmişken dönen) uygulama
	ChangeState    = "state"    // durum tuple'ı değişti
	ChangeSync     = "sync"     // argocd_app_sync_total arttı (tamamlanan senkron)
	ChangeDeleted  = "deleted"  // iki ardışık TAM envanterde yok
)

// StatusRow — argocd_app_status TAM satırı; alan sırası ve `ch` etiketleri
// migrations/0015 (≡ rollout_v2_schema.go) kolonlarıyla BİREBİR
// (TestStatusRowMirrorsDDL). Tam satır değiştirme (invariant #4): her yazım
// bütün kolonları taşır.
type StatusRow struct {
	InstanceID    string    `ch:"instance_id"`
	AppNamespace  string    `ch:"app_namespace"`
	AppName       string    `ch:"app_name"`
	ChangedAt     time.Time `ch:"changed_at"`
	ChangeKind    string    `ch:"change_kind"`
	SyncStatus    string    `ch:"sync_status"`
	HealthStatus  string    `ch:"health_status"`
	Operation     string    `ch:"operation"`
	SyncPhase     string    `ch:"sync_phase"`
	AutoSync      string    `ch:"autosync_enabled"` // 'true' | 'false' | '' (etiket yok, Argo ≤ 2.8)
	Project       string    `ch:"project"`
	Repo          string    `ch:"repo"`
	DestServer    string    `ch:"dest_server"` // ham; eşleme anında normalize
	DestNamespace string    `ch:"dest_namespace"`
	ClusterID     string    `ch:"cluster_id"` // dest_server → EffectiveID; '' = eşlenmedi
	Version       uint64    `ch:"version"`
}

// StatusColumns — INSERT/SELECT kolon listesi, StatusRow sırasıyla (chstore
// bu listeyi kullanır; model ↔ SQL tek kaynak).
func StatusColumns() []string {
	rt := reflect.TypeOf(StatusRow{})
	out := make([]string, rt.NumField())
	for i := range out {
		out[i] = rt.Field(i).Tag.Get("ch")
	}
	return out
}

// AppKey — uygulama anahtarı (§5.2).
type AppKey struct {
	InstanceID   string
	AppNamespace string
	Name         string
}

// Key — satırın uygulama anahtarı.
func (r StatusRow) Key() AppKey { return AppKey{r.InstanceID, r.AppNamespace, r.AppName} }

// AppTuple — değişimi tanımlayan alanlar (§10.3.3: sync, health, operation,
// autosync, project, repo, dest_*). cluster_id türetilmiştir, tuple'da değil.
type AppTuple struct {
	SyncStatus    string
	HealthStatus  string
	Operation     string
	AutoSync      string
	Project       string
	Repo          string
	DestServer    string
	DestNamespace string
}

// Tuple — satırın durum tuple'ı.
func (r StatusRow) Tuple() AppTuple {
	return AppTuple{r.SyncStatus, r.HealthStatus, r.Operation, r.AutoSync, r.Project, r.Repo, r.DestServer, r.DestNamespace}
}

// WithTuple — tuple alanları değiştirilmiş kopya.
func (r StatusRow) WithTuple(t AppTuple) StatusRow {
	r.SyncStatus, r.HealthStatus, r.Operation, r.AutoSync = t.SyncStatus, t.HealthStatus, t.Operation, t.AutoSync
	r.Project, r.Repo, r.DestServer, r.DestNamespace = t.Project, t.Repo, t.DestServer, t.DestNamespace
	return r
}

// NonSteady — NonSteadyQuery'nin TANIMIYLA aynı: Synced + Healthy + işlem yok
// dışındaki her durum. İkisi ayrışırsa sabite dönen uygulama izlenmezdi.
func (t AppTuple) NonSteady() bool {
	return t.SyncStatus != "Synced" || t.HealthStatus != "Healthy" || t.Operation != ""
}

// ValidateStatusRow — yazıcı sözleşmesi (SAF); nil = yazılabilir. TTL
// çapası changed_at sıfır/epoch olamaz (1970'e yazılan satır ilk TTL
// birleşmesinde sessizce düşer — rollout_v2_schema.go), anahtar alanları
// boş olamaz, change_kind sözlükte, sync satırı fazlı ve yalnız sync satırı
// fazlı, autosync üç değerden biri, açık version.
func ValidateStatusRow(r StatusRow) error {
	var errs []error
	if r.ChangedAt.IsZero() || r.ChangedAt.Unix() <= 0 {
		errs = append(errs, errors.New("changed_at sıfır/epoch (TTL çapası)"))
	}
	if strings.TrimSpace(r.InstanceID) == "" || strings.TrimSpace(r.AppNamespace) == "" || strings.TrimSpace(r.AppName) == "" {
		errs = append(errs, errors.New("anahtar boş (instance_id, app_namespace, app_name)"))
	}
	switch r.ChangeKind {
	case ChangeBaseline, ChangeAppeared, ChangeState, ChangeSync, ChangeDeleted:
	default:
		errs = append(errs, fmt.Errorf("change_kind %q sözlük dışı", r.ChangeKind))
	}
	if (r.ChangeKind == ChangeSync) != (r.SyncPhase != "") {
		errs = append(errs, errors.New("sync_phase yalnız ve her zaman change_kind=sync satırında"))
	}
	if r.AutoSync != "" && r.AutoSync != "true" && r.AutoSync != "false" {
		errs = append(errs, fmt.Errorf("autosync_enabled %q", r.AutoSync))
	}
	if r.Version == 0 {
		errs = append(errs, errors.New("version sıfır (açık istemci version'ı)"))
	}
	return errors.Join(errs...)
}

// ── Sorgular (§5.4) ────────────────────────────────────────────────────────

// syncBy — senkron sayaçlarının katlama kümesi (pod/instance yok; §5.3
// join anahtarı + faz).
const syncBy = `sum by (namespace, exported_namespace, name, phase)`

// ShardSelector — instance parçasının seçici gövdesi: namespace (instance
// ns; §5.4 "shard by instance namespace") + tanımlıysa job. Değerler PromQL
// dize sabiti olarak kaçışlanır; küme etiketi enjeksiyonu okuyucunun işi.
func ShardSelector(inst Instance) string {
	sel := `namespace="` + promStringEscaper.Replace(inst.HubNamespace) + `"`
	if inst.MetricsJob != "" {
		sel += `,job="` + promStringEscaper.Replace(inst.MetricsJob) + `"`
	}
	return sel
}

// InventoryQuery — parçanın tam envanteri (anlık; kopyalar katlanmış).
func InventoryQuery(sel string) string {
	return appInfoBy + ` (` + AppInfoMetric + `{` + sel + `})`
}

// NonSteadyQuery — tik başına ucuz okuma: yalnız sabit OLMAYAN uygulamalar
// (OutOfSync, Healthy değil ya da işlem sürüyor; §11 H6 "non-steady set").
// `unless` aynı metriğin özdeş etiket kümelerini eler.
func NonSteadyQuery(sel string) string {
	return appInfoBy + ` (` + AppInfoMetric + `{` + sel + `} unless ` + AppInfoMetric + `{` + sel +
		`,sync_status="Synced",health_status="Healthy",operation=""})`
}

// TargetedQueries — ada göre hedefli okuma (sabite dönen ya da senkronu
// görülen ama bu tikte gözlenmeyen uygulamalar). Tekil, sıralı, boşsuz adlar
// ServiceSelectorCap'lik parçalara bölünür; en çok maxQueries sorgu, sığmayan
// adlar `rest` olarak döner (sonraki tik ya da envanter).
func TargetedQueries(sel string, names []string, maxQueries int) (qs []string, rest []string) {
	seen := map[string]bool{}
	var uniq []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		uniq = append(uniq, n)
	}
	sort.Strings(uniq)
	for len(uniq) > 0 {
		if len(qs) >= maxQueries {
			return qs, uniq
		}
		n := len(uniq)
		if n > ServiceSelectorCap {
			n = ServiceSelectorCap
		}
		qs = append(qs, appInfoBy+` (`+AppInfoMetric+`{`+sel+`,name=~"`+promRegexAlt(uniq[:n])+`"})`)
		uniq = uniq[n:]
	}
	return qs, nil
}

// promDur — PromQL süre dizgisi (tam saniye, en az 1s).
func promDur(d time.Duration) string {
	s := int(d / time.Second)
	if s < 1 {
		s = 1
	}
	return strconv.Itoa(s) + "s"
}

// syncNotDryRun — v0.10.983 inceleme: Argo v3.1+ `dry_run` etiketi (§5.1);
// `argocd app sync --dry-run` da Succeeded fazıyla sayacı artırır ama
// kümede hiçbir şey değişmez — 'sync' satırı P3.4'te sahte GitOps kanıtı
// olurdu. `!="true"` etiketi hiç olmayan seriyi (Argo ≤ 3.0) de eşler.
const syncNotDryRun = `,dry_run!="true"`

// syncSeries — parçanın dry-run DIŞI senkron sayacı seçicisi.
func syncSeries(sel string) string { return SyncTotalMetric + `{` + sel + syncNotDryRun + `}` }

// SyncWindowQuery — tik başına senkron okuması: son `window` içinde değişen
// YA DA bu pencerede doğan sayaçların GÜNCEL değeri (bellek farkı artışı
// verir; pencere örtüşmesi çift sayım üretmez). Yeni doğan kolu gerekli:
// ilk tamamlanan senkronla doğan serinin tek değeri vardır, changes() 0 verir.
func SyncWindowQuery(sel string, window time.Duration) string {
	m := syncSeries(sel)
	w := promDur(window)
	on := ` on (namespace, exported_namespace, name, phase) `
	return syncBy + ` (` + m + `) and` + on + `(` +
		`(` + syncBy + ` (changes(` + m + `[` + w + `])) > 0)` +
		` or ` +
		`(` + syncBy + ` (` + m + `) unless` + on + syncBy + ` (` + m + ` offset ` + w + `))` +
		`)`
}

// SyncFullQuery — envanter tikinde parçanın bütün sayaçları (taban).
func SyncFullQuery(sel string) string {
	return syncBy + ` (` + syncSeries(sel) + `)`
}

// LivenessQuery — v0.10.983 inceleme: envanter tikinde parçanın scrape
// hedefleri (`up`, aynı seçici; aynı Prometheus'un ürettiği seri, küme
// etiketi enjeksiyonu Argo serileriyle aynı karara bağlı). Sonuç
// {up="1"} ve {up="0"} hedef sayıları. Yokluk (deleted) yalnız hedefler
// sağlamken işlenir: controller (shard'ı) çöktüğünde ya da scrape
// koptuğunda seriler bayatlar ve Thanos uyarısız KÜÇÜK bir vektör döner —
// bu "uygulama silindi" değil "veri yok"tur.
//
// v0.10.983 ikinci inceleme: yalnız argocd_app_info üreten job'ların
// hedefleri sayılır (`and on (job)`). metricsJob boşken seçici bütün
// namespace'tir; kalıcı up=0 kalan ilgisiz bir hedef (dex, redis-exporter
// ServiceMonitor'u) her envanterde silmeyi sonsuza dek bekletiyordu. Job
// düzeyinde (instance değil) süzülür: çöken controller shard'ının kendi
// argocd_app_info serileri bayatlasa da job'u diğer shard'larla yaşar, o
// shard'ın up=0'ı sayılmaya devam eder; bütün shard'lar düşerse envanter
// boş döner (veri yok yolu).
func LivenessQuery(sel string) string {
	return `count_values("up", up{` + sel + `} and on (job) group by (job) (` + AppInfoMetric + `{` + sel + `}))`
}

// ParseLiveness — SAF: LivenessQuery sonucu → sağlam / düşmüş hedef sayısı.
// Tanınmayan değer atlanır.
func ParseLiveness(resultType string, raw json.RawMessage) (up, down int, err error) {
	rows, err := decodeVector(resultType, raw)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range rows {
		f, ok := sampleValue(r.Value)
		if !ok || f < 0 {
			continue
		}
		switch r.Metric["up"] {
		case "1":
			up += int(f)
		case "0":
			down += int(f)
		}
	}
	return up, down, nil
}

// ── Çözücüler ─────────────────────────────────────────────────────────────

// appNamespaceOf — §5.2: app_ns = exported_namespace, yoksa namespace.
// exported varken namespace instance ns'idir (ikinci dönüş).
func appNamespaceOf(m map[string]string) (appNS, instanceNS string) {
	if ex := m["exported_namespace"]; ex != "" {
		return ex, m["namespace"]
	}
	return m["namespace"], ""
}

// autosyncOf — 'true' | 'false' | ” (etiket yok ya da tanınmaz).
func autosyncOf(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true":
		return "true"
	case "false":
		return "false"
	}
	return ""
}

// tupleOf — argocd_app_info etiketlerinden durum tuple'ı.
func tupleOf(m map[string]string) AppTuple {
	return AppTuple{
		SyncStatus: m["sync_status"], HealthStatus: m["health_status"], Operation: m["operation"],
		AutoSync: autosyncOf(m["autosync_enabled"]), Project: m["project"], Repo: m["repo"],
		DestServer: m["dest_server"], DestNamespace: m["dest_namespace"],
	}
}

// ShardObs — bir parçanın bu tikteki gözlemi.
type ShardObs struct {
	Apps      map[AppKey]AppTuple
	Ambiguous map[AppKey]bool // aynı anahtarda farklı durumlar (gözlem değil)
	Skipped   int             // adsız ya da başka instance namespace'inden seri
}

func (o *ShardObs) add(k AppKey, t AppTuple) {
	if o.Apps == nil {
		o.Apps = map[AppKey]AppTuple{}
	}
	if o.Ambiguous[k] {
		return
	}
	if cur, ok := o.Apps[k]; ok && cur != t {
		delete(o.Apps, k)
		if o.Ambiguous == nil {
			o.Ambiguous = map[AppKey]bool{}
		}
		o.Ambiguous[k] = true
		return
	}
	o.Apps[k] = t
}

// Merge — başka bir okumanın gözlemini katar (hedefli okuma); çelişen durum
// belirsizdir.
func (o *ShardObs) Merge(other ShardObs) {
	for k := range other.Ambiguous {
		delete(o.Apps, k)
		if o.Ambiguous == nil {
			o.Ambiguous = map[AppKey]bool{}
		}
		o.Ambiguous[k] = true
	}
	for k, t := range other.Apps {
		o.add(k, t)
	}
	o.Skipped += other.Skipped
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

func decodeVector(resultType string, raw json.RawMessage) ([]promSample, error) {
	if resultType != "vector" {
		return nil, fmt.Errorf("beklenmeyen sonuç türü %q", resultType)
	}
	var rows []promSample
	if len(raw) == 0 {
		return rows, nil
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("vector çözülemedi: %w", err)
	}
	return rows, nil
}

// sampleValue — anlık örneğin sonlu değeri (NaN/Inf/bozuk → false).
func sampleValue(v []json.RawMessage) (float64, bool) {
	if len(v) != 2 {
		return 0, false
	}
	var s string
	if json.Unmarshal(v[1], &s) != nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != f || f > 1e15 || f < -1e15 {
		return 0, false
	}
	return f, true
}

// ParseShardApps — SAF: envanter / sabit olmayan / hedefli okuma → gözlem.
// exported_namespace varken namespace parçanın instance ns'i olmalı (değilse
// seri başka instance'ındır; Skipped). Adsız seri atlanır.
func ParseShardApps(instanceID, hubNamespace, resultType string, raw json.RawMessage) (ShardObs, error) {
	rows, err := decodeVector(resultType, raw)
	if err != nil {
		return ShardObs{}, err
	}
	obs := ShardObs{Apps: map[AppKey]AppTuple{}}
	for _, r := range rows {
		m := r.Metric
		appNS, instNS := appNamespaceOf(m)
		if m["name"] == "" || appNS == "" || (instNS != "" && instNS != hubNamespace) {
			obs.Skipped++
			continue
		}
		obs.add(AppKey{instanceID, appNS, m["name"]}, tupleOf(m))
	}
	return obs, nil
}

// SyncKey — bir parçada senkron sayacı anahtarı (instance parçadan bilinir).
type SyncKey struct {
	AppNamespace string
	Name         string
	Phase        string
}

// ParseSyncCounters — SAF: SyncWindowQuery / SyncFullQuery sonucu → sayaç
// değerleri. Negatif, sonlu olmayan ya da adsız örnek atlanır.
func ParseSyncCounters(hubNamespace, resultType string, raw json.RawMessage) (map[SyncKey]float64, error) {
	rows, err := decodeVector(resultType, raw)
	if err != nil {
		return nil, err
	}
	out := map[SyncKey]float64{}
	for _, r := range rows {
		appNS, instNS := appNamespaceOf(r.Metric)
		f, ok := sampleValue(r.Value)
		if !ok || f < 0 || r.Metric["name"] == "" || appNS == "" || (instNS != "" && instNS != hubNamespace) {
			continue
		}
		out[SyncKey{appNS, r.Metric["name"], r.Metric["phase"]}] += f
	}
	return out, nil
}

// syncEps — kayan nokta gürültüsü (sayaçlar tamsayı; sum by toplamı).
const syncEps = 0.5

// SyncIncreases — SAF: sayaç artışları (dosya başı "SENKRON"). cur yalnız
// okunan serileri taşır (pencere okuması); prev'de olup cur'da olmayan seri
// değişmemiştir. baselineOK: parçanın tam sayaç tabanı alındı mı.
func SyncIncreases(prev, cur map[SyncKey]float64, baselineOK bool) (map[SyncKey]float64, map[string]int) {
	inc := map[SyncKey]float64{}
	diag := map[string]int{}
	for k, v := range cur {
		p, ok := prev[k]
		switch {
		case ok && v-p >= syncEps:
			inc[k] = v - p
		case ok && p-v >= syncEps:
			// Sıfırlanma (controller yeniden başladı; seri ilk senkronla doğdu).
			diag["sync_counter_reset"]++
			if v >= syncEps {
				inc[k] = v
			}
		case ok:
		case baselineOK && v >= syncEps:
			diag["sync_newborn"]++
			inc[k] = v
		case v >= syncEps:
			diag["sync_unbaselined"]++
		}
	}
	return inc, diag
}

// syncPhaseRank — aynı tikte birden çok faz artarsa satırın fazı: Succeeded >
// Failed > Error > diğerleri (alfabetik). Sınıflandırma için tamamlanan
// senkronun VARLIĞI esas; faz ikincil.
func syncPhaseRank(p string) int {
	switch p {
	case "Succeeded":
		return 0
	case "Failed":
		return 1
	case "Error":
		return 2
	}
	return 3
}

// SyncPhases — SAF: artışlar → uygulama başına tek faz.
func SyncPhases(instanceID string, inc map[SyncKey]float64) map[AppKey]string {
	out := map[AppKey]string{}
	for k := range inc {
		if k.Phase == "" {
			continue
		}
		ak := AppKey{instanceID, k.AppNamespace, k.Name}
		cur, ok := out[ak]
		if !ok || syncPhaseRank(k.Phase) < syncPhaseRank(cur) || (syncPhaseRank(k.Phase) == syncPhaseRank(cur) && k.Phase < cur) {
			out[ak] = k.Phase
		}
	}
	return out
}

// DestClusterID — SAF: dest_server → Remote Cluster EffectiveID (§5.3).
// Küme-içi adres instance'ın KENDİ hub'ına çözülür (§5.6); boş ya da
// eşleşmeyen → "" (eşlenmedi, sayılır). normalize nil ise ham değer.
func DestClusterID(destServer, hubClusterID string, byServer map[string]string, normalize func(string) string) string {
	ds := strings.TrimSpace(destServer)
	if ds == "" {
		return ""
	}
	if IsInClusterServer(ds) {
		return hubClusterID
	}
	if normalize != nil {
		ds = normalize(ds)
	}
	return byServer[ds]
}

// ── Diff ──────────────────────────────────────────────────────────────────

// Yokluk kuralları: bir uygulama iki ARDIŞIK tam envanterde yoksa silinmiş
// sayılır (tek envanterlik controller shard kesintisi sahte deleted +
// appeared çifti üretmesin). Tam envanterde bilinen canlı uygulamaların
// yarıdan fazlası (en az massMinApps varken) birden yoksa YOKLUK işlemesi
// dondurulur (Mass); işçi süre dolunca MassAccept ile kabul eder.
// v0.10.983 inceleme: bilinen canlı uygulamaların HEPSİ birden yoksa (uygulama
// sayısından bağımsız) bu da toplu yokluktur ve MassAccept ile de KABUL
// EDİLMEZ (TotalAbsence) — boş/yer değiştirmiş okuma (scrape kopması, yanlış
// küme etiketi, başka veri kaynağı) "hepsi silindi" değildir. HoldAbsence
// (işçi: parça hedefleri sağlam değil) yokluk işlemesini tümden atlar.
const (
	absentInventories = 2
	massMinApps       = 20
)

// StatusRefreshAge — v0.10.983 inceleme: TTL (180 gün, changed_at'e bağlı)
// değişmeyen uygulamanın TEK satırını düşürür; bellekte bilinen uygulama için
// bir daha satır yazılmazdı (§10.3.3 "sonraki tam envanterde yeniden taban"
// sözü tutulmazdı). Son satırı bundan eski, değişmemiş uygulama tam envanterde
// aynı tuple'la 'baseline' olarak tazelenir (değişim zamanı iddia edilmez).
// 30 günlük pay: TTL birleşmesi satırı yaşından önce düşürmez ama tik
// kaçırmaları / kapalı bayrak dönemleri için boşluk.
const StatusRefreshAge = 150 * 24 * time.Hour

// AppMem — işçi belleğinde bir uygulama: son yazılan/geri kurulan satır.
type AppMem struct {
	Row     StatusRow
	Deleted bool // son satır 'deleted'
	Absent  int  // ardışık tam envanterde yokluk
}

// DiffInput — bir parçanın tik girdisi.
type DiffInput struct {
	InstanceID   string
	HubClusterID string
	ChangedAt    time.Time
	// Inventory — Obs TAM envanterdir (yokluk anlamlıdır). Değilse Obs sabit
	// olmayan + hedefli okumadır; yokluktan hiçbir şey çıkarılmaz.
	Inventory bool
	Obs       ShardObs
	// Syncs — bu tikte tamamlanan senkronu görülen uygulamalar → faz. İşçi
	// yalnız GÖZLENEN uygulamaları geçirir (tuple'ı bilinmeyen satır yazılmaz).
	Syncs map[AppKey]string
	Prev  map[AppKey]AppMem
	// Baseline — instance için ilk koşu ya da yeniden kurulumdan sonra henüz
	// tam envanter yok: bilinmeyen uygulama envanterde 'baseline' (yeni mi,
	// TTL'i dolmuş eski mi ayırt edilemez; değişim zamanı iddia edilmez).
	Baseline bool
	// BaselineKeys — v0.10.983 inceleme: taban envanterinde BELİRSİZ görülen
	// (HA çiftinin iki farklı durumu) uygulamalar: taban vardı ama anahtar
	// belleğe girmedi. İlk gözlendiklerinde (envanter dışı okuma dahil)
	// 'appeared' değil 'baseline' yazılır.
	BaselineKeys map[AppKey]bool
	// Rebaseline — v0.10.983 inceleme: parçanın veri kaynağı değişti (hub,
	// Thanos URL'si, küme etiketi ya da seçici) ve henüz dolu bir taban
	// envanteri yok: bilinen uygulamanın farklı tuple'ı kaynak değişiminin
	// eseri olabilir → 'state' yerine 'baseline' (Baseline ile birlikte).
	Rebaseline bool
	// HoldAbsence — v0.10.983 inceleme: yokluk işlenmez (parça hedefleri
	// sağlam değil / doğrulanamadı); Absent sayaçları ilerlemez.
	HoldAbsence bool
	// RefreshBefore — sıfır değilse: tam envanterde değişmemiş ve son satırı
	// bundan eski uygulama 'baseline' ile tazelenir (StatusRefreshAge).
	RefreshBefore time.Time
	MassAccept    bool
	ClusterOf     func(destServer string) string
	// NextVersion — tekdüze açık istemci version'ı.
	NextVersion func() uint64
}

// DiffOutput — yazılacak satırlar (anahtar sırasında) ve yeni bellek.
type DiffOutput struct {
	Rows []StatusRow
	Next map[AppKey]AppMem
	Mass bool // toplu yokluk görüldü (MassAccept yoksa işlenmedi)
	// TotalAbsence — bilinen canlı uygulamaların HEPSİ yoktu: MassAccept'le de
	// işlenmedi (v0.10.983 inceleme).
	TotalAbsence bool
	Diag         map[string]int
}

func sortedAppKeys[V any](m map[AppKey]V) []AppKey {
	ks := make([]AppKey, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		a, b := ks[i], ks[j]
		if a.InstanceID != b.InstanceID {
			return a.InstanceID < b.InstanceID
		}
		if a.AppNamespace != b.AppNamespace {
			return a.AppNamespace < b.AppNamespace
		}
		return a.Name < b.Name
	})
	return ks
}

// DiffStatus — SAF: gözlem + önceki bellek → değişim satırları (dosya başı).
func DiffStatus(in DiffInput) DiffOutput {
	out := DiffOutput{Next: make(map[AppKey]AppMem, len(in.Prev)), Diag: map[string]int{}}
	for k, m := range in.Prev {
		if m.Deleted && m.Row.ChangedAt.Before(in.ChangedAt) {
			// v0.10.983 ikinci inceleme: önceki tikte silinmiş uygulama
			// bellekten düşer (CH yeniden kurulumu da almaz — argocdRebuildKeep);
			// ApplicationSet önizlemeleriyle günde binlerce ölü kayıt uzun
			// yaşayan liderde birikip her tik kopyalanırdı. Geri dönen uygulama
			// yine 'appeared' (bilinmeyen) olur; bu tikte Prev'de durduğu için
			// changed_at çakışma kaydırması da korunur.
			continue
		}
		out.Next[k] = m
	}
	clusterOf := in.ClusterOf
	if clusterOf == nil {
		clusterOf = func(string) string { return "" }
	}
	emit := func(k AppKey, t AppTuple, kind, phase string) {
		at := in.ChangedAt
		if pm, ok := in.Prev[k]; ok && !pm.Row.ChangedAt.Before(at) {
			// Aynı aralıkta ikinci satır (yeniden edinim tiki, failover): aynı
			// anahtar önceki satırı — belki bir 'sync'i — RMT'de SESSİZCE ezerdi.
			// Önceki satırın 1 ms sonrasına kaydırılır; sıra korunur.
			at = pm.Row.ChangedAt.Add(time.Millisecond)
			out.Diag["changed_at_bumped"]++
		}
		r := StatusRow{InstanceID: k.InstanceID, AppNamespace: k.AppNamespace, AppName: k.Name, ChangedAt: at,
			ChangeKind: kind, SyncPhase: phase, ClusterID: clusterOf(t.DestServer)}
		r = r.WithTuple(t)
		r.Version = in.NextVersion()
		out.Rows = append(out.Rows, r)
		out.Next[k] = AppMem{Row: r, Deleted: kind == ChangeDeleted}
		out.Diag["rows_"+kind]++
	}
	if n := len(in.Obs.Ambiguous); n > 0 {
		// Belirsiz uygulama: satır yok, bellek aynen, yokluğu sayılmaz.
		out.Diag["app_ambiguous"] += n
	}
	for _, k := range sortedAppKeys(in.Obs.Apps) {
		t := in.Obs.Apps[k]
		phase := in.Syncs[k]
		pm, known := in.Prev[k]
		switch {
		case phase != "":
			emit(k, t, ChangeSync, phase)
		case !known && in.Baseline && !in.Inventory:
			out.Diag["unknown_before_inventory"]++
		case !known && (in.Baseline || in.BaselineKeys[k]):
			emit(k, t, ChangeBaseline, "")
		case !known || pm.Deleted:
			emit(k, t, ChangeAppeared, "")
		case pm.Row.Tuple() != t && in.Rebaseline && in.Baseline:
			out.Diag["rebaseline_changed"]++
			emit(k, t, ChangeBaseline, "")
		case pm.Row.Tuple() != t:
			emit(k, t, ChangeState, "")
		case in.Inventory && !in.RefreshBefore.IsZero() && pm.Row.ChangedAt.Before(in.RefreshBefore):
			out.Diag["ttl_refresh"]++
			emit(k, t, ChangeBaseline, "")
		default:
			pm.Absent = 0
			out.Next[k] = pm
		}
	}
	if !in.Inventory {
		return out
	}
	if in.HoldAbsence {
		out.Diag["absence_held"]++
		return out
	}
	var live, missing []AppKey
	for _, k := range sortedAppKeys(in.Prev) {
		m := in.Prev[k]
		if m.Deleted {
			continue
		}
		live = append(live, k)
		if _, seen := in.Obs.Apps[k]; !seen && !in.Obs.Ambiguous[k] {
			missing = append(missing, k)
		}
	}
	if len(live) > 0 && len(missing) == len(live) {
		out.Mass, out.TotalAbsence = true, true
		out.Diag["total_absence"] += len(missing)
		return out
	}
	if len(live) >= massMinApps && len(missing)*2 > len(live) {
		out.Mass = true
		out.Diag["mass_absence"] += len(missing)
		if !in.MassAccept {
			return out
		}
	}
	for _, k := range missing {
		m := out.Next[k]
		m.Absent++
		if m.Absent < absentInventories {
			out.Next[k] = m
			out.Diag["absent_pending"]++
			continue
		}
		emit(k, m.Row.Tuple(), ChangeDeleted, "")
	}
	return out
}
