package api

// http_env_scope.go — v0.10.1376 (CoSRE küme kıyası; v0.10.1377 metrik kipi
// env süzgecinin ortak yarısı): span'in ortam adını ("eu-a") VictoriaMetrics'in
// HTTP histogramındaki ortam ETİKETİNE ve o etiketin CANLI değerlerine
// ("prod-eu-a") çevirir. Prod trace'leri %1 örnekliyor; span'den sayılan çağrı
// gerçek trafiğin ~%1'i. http_server_request_duration_seconds örneklenmemiş ve
// kümeyi deployment_environment_name etiketinde taşıyor.
//
// Etiket seçimi (operatör onaylı varsayılanlar):
//
//  1. configured — LabelMap.Env (Settings → Metrik backend'i → "Ortam") doluysa
//     KAZANIR, değeri olmasa bile (operatörün açık kararı);
//  2. default — deployment_environment_name (kod varsayılanı);
//  3. discovered — diğer yazımlar (vmetrics.RoleCandidates(RoleEnv)) YALNIZ
//     varsayılanın bu metrikte HİÇ değeri yoksa, sırayla, ilk değerlisi.
//
// Değer okuması sınırlı (≤1000 değer, 24 sa pencere, 2 sn) ve önbellekli (5 dk,
// anahtar metrik + etiket + limit + 10 dk'ya yuvarlanmış uç). Eşleme SAF:
// vmetrics.MatchEnvValues.
//
// v0.10.1377 — HTTP ucu (GET /api/metrics/http-env-scope) kendi dosyasında
// (http_env_scope_route.go); Service → HTTP Metrics, endpoint sayfası, liste ve
// Overview pencere karoları matcher'ı oradan alır. /metric-red aynı çözücüyü
// sunucuda çağırır (httpEnvScopeFilters) — iki yüzey aynı değerlere süzülür.
// v0.10.1437 — o uç KALDIRILDI: v0.10.1429'dan beri sayfalar eşleyiciyi
// /api/services/{name}/red-family cevabından alır (service_red_family_route.go);
// uçla paylaşılan üç SAF yardımcı (httpEnvScopeTermMax / httpEnvScopeTerms /
// httpEnvScopeFailed) buraya taşındı.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// v0.10.1376 — ortam etiketi sabitleri.
const (
	httpEnvLabelDefault   = "deployment_environment_name"
	httpEnvMetric         = chstore.ThroughputMetricDefault
	httpEnvValuesLimit    = 1000
	httpEnvValuesTimeout  = 2 * time.Second
	httpEnvValuesTTL      = 5 * time.Minute
	httpEnvValuesLookback = 24 * time.Hour
	httpEnvValuesSnap     = 10 * time.Minute
)

// httpEnvScopeTermMax — v0.10.1377: env/cluster adı uzunluk tavanı (anahtar
// şişmesin). /red-family aynı tavanı uygular.
const httpEnvScopeTermMax = 200

// httpEnvScopeTerms — SAF (v0.10.1377): sorgu parametreleri → çözücü terimleri.
func httpEnvScopeTerms(env, cluster string) map[string]string {
	t := map[string]string{}
	if env != "" {
		t[vmetrics.RoleEnv] = env
	}
	if cluster != "" {
		t[vmetrics.RoleCluster] = cluster
	}
	return t
}

// httpEnvScopeFailed — SAF (v0.10.1377): etiket okuma hatası → gövde. Ham hata
// metni (VM host/port) viewer'a gitmez; yalnız sınıfı.
func httpEnvScopeFailed(err error) httpEnvScope {
	return httpEnvScope{Failed: true, Reason: "VM etiket sorgusu başarısız: " + string(sourcestate.Classify(err))}
}

// Etiket kaynağı — v0.10.1376.
const (
	httpEnvSourceConfigured = "configured"
	httpEnvSourceDefault    = "default"
	httpEnvSourceDiscovered = "discovered"
)

// httpEnvLabelReader — v0.10.1376: çözümleyicinin VM'den okuduğu iki şey
// (vmMetricSource karşılar; testler sahte okuyucu verir).
type httpEnvLabelReader interface {
	MetricLabelValuesIn(ctx context.Context, metric, label string, from, to time.Time, limit int) ([]string, bool, error)
	MetricLabelMap() vmetrics.LabelMap
}

// httpEnvLabelCandidates — SAF (v0.10.1376): denenecek etiketler sırasıyla.
// LabelMap.Env doluysa yalnız o; değilse varsayılan + kalan rol adayları.
func httpEnvLabelCandidates(lm vmetrics.LabelMap) []string {
	if l := lm.Get(vmetrics.RoleEnv); l != "" {
		return []string{l}
	}
	out := []string{httpEnvLabelDefault}
	for _, c := range vmetrics.RoleCandidates(vmetrics.RoleEnv) {
		if c != httpEnvLabelDefault {
			out = append(out, c)
		}
	}
	return out
}

// httpEnvTerm — tek süzgeç teriminin çözümü (role "env" | "cluster").
type httpEnvTerm struct {
	Role   string   `json:"role"`
	Want   string   `json:"want"`
	Label  string   `json:"label,omitempty"`
	Values []string `json:"values,omitempty"`
	Tier   string   `json:"tier"`
}

// httpEnvScope — çözülmüş ortam kapsamı. Applied=false → süzgeç UYGULANMAZ ve
// Reason çağırana söylenir (sessizce düşürülen süzgeç yok).
type httpEnvScope struct {
	Label   string        `json:"label"`
	Source  string        `json:"source"`
	Terms   []httpEnvTerm `json:"terms"`
	Matcher string        `json:"matcher,omitempty"`
	Applied bool          `json:"applied"`
	Partial bool          `json:"partial,omitempty"`
	Reason  string        `json:"reason,omitempty"`
	// v0.10.1377 — etiket okuması BAŞARISIZ (VM erişilemedi / zaman aşımı):
	// "metrikte bu ortam yok"tan ayrı söylenir; süzgeç yine uygulanmaz.
	Failed bool `json:"failed,omitempty"`
}

// httpEnvValuesKey — SAF (v0.10.1376): değer önbelleği anahtarı; TÜM girdiler
// (metrik, etiket, limit, 10 dk'ya yuvarlanmış uç — pencere ondan türer).
func httpEnvValuesKey(metric, label string, to time.Time) string {
	return fmt.Sprintf("http-env-values:v1:m=%s:l=%s:n=%d:to=%d", metric, label, httpEnvValuesLimit, to.Truncate(httpEnvValuesSnap).Unix())
}

// httpEnvValuesWindow — SAF (v0.10.1376): değer penceresi = 24 sa, ucu 10 dk'ya
// yuvarlanmış `to`nun bir adım sonrası (anahtar ile okuma aynı pencereyi görür,
// `to` her zaman içinde).
func httpEnvValuesWindow(to time.Time) (time.Time, time.Time) {
	end := to.Truncate(httpEnvValuesSnap).Add(httpEnvValuesSnap)
	return end.Add(-httpEnvValuesLookback), end
}

// httpEnvValuesBody — önbellek gövdesi.
type httpEnvValuesBody struct {
	Values  []string `json:"values"`
	Partial bool     `json:"partial,omitempty"`
}

// httpEnvLabelValues — v0.10.1376: etiketin canlı değerleri (≤1000, 24 sa,
// 2 sn, 5 dk önbellek). Hata önbelleğe yazılmaz, çağırana döner.
func (s *Server) httpEnvLabelValues(ctx context.Context, vm httpEnvLabelReader, label string, to time.Time) ([]string, bool, error) {
	return s.metricEnvLabelValues(ctx, vm, httpEnvMetric, label, to)
}

// metricEnvLabelValues — v0.10.1416 (D8 genelleştirmesi): httpEnvLabelValues'un
// metrik parametreli çekirdeği (yapıştırılan PromQL ortamı SORGULANAN metriğin
// etiketine karşı çözer). Anahtar zaten metriği taşıyor (httpEnvValuesKey).
func (s *Server) metricEnvLabelValues(ctx context.Context, vm httpEnvLabelReader, metric, label string, to time.Time) ([]string, bool, error) {
	fn := func(c context.Context) (any, error) {
		cctx, cancel := context.WithTimeout(c, httpEnvValuesTimeout)
		defer cancel()
		from, end := httpEnvValuesWindow(to)
		vals, partial, err := vm.MetricLabelValuesIn(cctx, metric, label, from, end, httpEnvValuesLimit)
		if err != nil {
			return nil, err
		}
		return httpEnvValuesBody{Values: vals, Partial: partial}, nil
	}
	if s == nil || s.cache == nil || s.l1 == nil || s.stats == nil {
		v, err := fn(ctx)
		if err != nil {
			return nil, false, err
		}
		b := v.(httpEnvValuesBody)
		return b.Values, b.Partial, nil
	}
	raw, _, err := s.cachedJSON(ctx, httpEnvValuesKey(metric, label, to), httpEnvValuesTTL, false, fn)
	if err != nil {
		return nil, false, err
	}
	var b httpEnvValuesBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, false, err
	}
	return b.Values, b.Partial, nil
}

// httpEnvLabel — çözülmüş ortam etiketi + canlı değerleri.
type httpEnvLabel struct {
	Label   string
	Source  string
	Values  []string
	Partial bool
}

// httpEnvLabelFor — v0.10.1376: etiket seçimi (configured > default >
// discovered; discovered yalnız varsayılan değersizken). Hiçbiri değerli
// değilse ilk aday değersiz döner (çağıran "eşleşmedi" der). Okuma hatası
// çağırana döner.
func (s *Server) httpEnvLabelFor(ctx context.Context, vm httpEnvLabelReader, to time.Time) (httpEnvLabel, error) {
	return s.metricEnvLabelFor(ctx, vm, httpEnvMetric, to)
}

// metricEnvLabelFor — v0.10.1416: httpEnvLabelFor'un metrik parametreli çekirdeği
// (aynı seçim kuralı; değerler `metric` üzerinden okunur).
func (s *Server) metricEnvLabelFor(ctx context.Context, vm httpEnvLabelReader, metric string, to time.Time) (httpEnvLabel, error) {
	lm := vm.MetricLabelMap().Normalized()
	cands := httpEnvLabelCandidates(lm)
	src := httpEnvSourceDefault
	if lm.Env != "" {
		src = httpEnvSourceConfigured
	}
	first := httpEnvLabel{Label: cands[0], Source: src}
	for i, l := range cands {
		vals, partial, err := s.metricEnvLabelValues(ctx, vm, metric, l, to)
		if err != nil {
			return httpEnvLabel{}, err
		}
		if i == 0 {
			first.Values, first.Partial = vals, partial
		}
		if len(vals) == 0 {
			continue
		}
		out := httpEnvLabel{Label: l, Source: src, Values: vals, Partial: partial}
		if i > 0 {
			out.Source = httpEnvSourceDiscovered
		}
		return out, nil
	}
	return first, nil
}

// httpEnvUnresolvedReason — SAF (v0.10.1376).
func httpEnvUnresolvedReason(label, want string) string {
	return fmt.Sprintf("metrikte bu ortam yok (%s değerleri arasında %q eşleşmedi)", label, want)
}

// httpEnvRoles — terimlerin sabit işlenme sırası.
var httpEnvRoles = []string{vmetrics.RoleEnv, vmetrics.RoleCluster}

// resolveHTTPEnvScope — v0.10.1376: terimleri (rol → istenen ad) canlı
// değerlere karşı çözer. cluster rolü LabelMap.Cluster doluysa o etikette,
// değilse ortam etiketinde aranır; iki rol aynı etikette → değer KESİŞİMİ.
// Bir terim bile çözülmezse Applied=false + Reason. Etiket okuma hatası → err.
func (s *Server) resolveHTTPEnvScope(ctx context.Context, vm httpEnvLabelReader, terms map[string]string, to time.Time) (httpEnvScope, error) {
	return s.resolveMetricEnvScope(ctx, vm, httpEnvMetric, terms, to)
}

// resolveMetricEnvScope — v0.10.1416 (D8 genelleştirmesi): resolveHTTPEnvScope'un
// metrik parametreli çekirdeği. HTTP yüzeyleri httpEnvMetric ile çağırır (bayt
// bayt eski davranış); yapıştırılan PromQL (copilot_metric_chart_promql.go)
// SORGULANAN metrikle — o metrik HTTP histogramı değilse ortam etiketi ve canlı
// değerleri kendi serilerinden gelir.
func (s *Server) resolveMetricEnvScope(ctx context.Context, vm httpEnvLabelReader, metric string, terms map[string]string, to time.Time) (httpEnvScope, error) {
	return s.resolveMetricEnvScopeMapped(ctx, vm, metric, terms, nil, to)
}

// httpEnvTierEntity — v0.10.2211: cluster terimi cluster varlığının span
// ortamlarıyla çözüldü (ham ad ortam etiketiyle kıyaslanmadı).
const httpEnvTierEntity = "entity"

// serviceClusterEnvsLookback — cluster → ortam eşlemesinin penceresi.
const serviceClusterEnvsLookback = 24 * time.Hour

// serviceClusterEnvs — v0.10.2211 (operatör, prod: endpoint detayında
// cluster=cl-a → "deployment_environment_name=cl-a, birebir", karolar boş;
// servisin serileri prod-cl-a taşıyor). Span tarafının cluster kapsamıyla
// aynı boyut: service_env_summary_5m (cluster × deploy_env), son 24 sa.
// Okunamazsa / eşleme yoksa nil (çağıran ham ada düşer).
func (s *Server) serviceClusterEnvs(ctx context.Context, service, cluster string, to time.Time) []string {
	if s == nil || s.store == nil || service == "" || strings.TrimSpace(cluster) == "" {
		return nil
	}
	envs, err := s.store.ServiceClusterEnvs(ctx, service, strings.TrimSpace(cluster), to.Add(-serviceClusterEnvsLookback), to)
	if err != nil {
		return nil
	}
	return envs
}

// matchClusterEnvs — SAF (v0.10.2211): cluster varlığının span ortamları →
// metrik etiketinin canlı değerleri (her ortam aynı toleranslı eşlemeyle;
// birleşim; yalnız birebir / harf duyarsız). Eşleşme yoksa Tier none (çağıran ham ada düşer).
func matchClusterEnvs(envs, live []string) vmetrics.EnvMatch {
	m := vmetrics.EnvMatch{Tier: vmetrics.EnvTierNone}
	var vals []string
	for _, e := range envs {
		// Span ortamı gerçek öznitelik değeri: yalnız birebir / harf duyarsız
		// (önek gevşetmesi "dr-cl-a"yı "cl-a"ya bağlardı).
		if em := vmetrics.MatchEnvValue(e, live); em.Tier == vmetrics.EnvTierExact || em.Tier == vmetrics.EnvTierCI {
			vals = append(vals, em.Values...)
		}
	}
	if len(vals) == 0 {
		return m
	}
	m.Tier, m.Values = httpEnvTierEntity, intersectStrings(sortedUniqueStrings(vals), live)
	if len(m.Values) == 0 {
		m.Tier, m.Values = vmetrics.EnvTierNone, nil
	}
	return m
}

// sortedUniqueStrings — SAF.
func sortedUniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// resolveMetricEnvScopeMapped — resolveMetricEnvScope + cluster varlığının
// span ortamları (clusterEnvs; nil = eşleme yok, bugünkü davranış). Cluster
// metrikte ayrı etikette (LabelMap.Cluster) değilse, yani ortam etiketinde
// aranıyorsa, önce eşlenen ortamlar denenir; eşleşmezse ham ad.
func (s *Server) resolveMetricEnvScopeMapped(ctx context.Context, vm httpEnvLabelReader, metric string, terms map[string]string, clusterEnvs []string, to time.Time) (httpEnvScope, error) {
	el, err := s.metricEnvLabelFor(ctx, vm, metric, to)
	if err != nil {
		return httpEnvScope{}, err
	}
	sc := httpEnvScope{Label: el.Label, Source: el.Source, Partial: el.Partial}
	clusterLabel := vm.MetricLabelMap().Get(vmetrics.RoleCluster)
	byLabel := map[string][]string{}
	var order []string
	for _, role := range httpEnvRoles {
		want := strings.TrimSpace(terms[role])
		if want == "" {
			continue
		}
		label, live := el.Label, el.Values
		if role == vmetrics.RoleCluster && clusterLabel != "" && clusterLabel != el.Label {
			label = clusterLabel
			vals, partial, err := s.metricEnvLabelValues(ctx, vm, metric, label, to)
			if err != nil {
				return httpEnvScope{}, err
			}
			live, sc.Partial = vals, sc.Partial || partial
		}
		m := vmetrics.MatchEnvValue(want, live)
		if role == vmetrics.RoleEnv {
			// 2026-11-21 — ortam etiketi `<env>-<cluster>` birleşik olabilir
			// ("prod-clusterA"): ortam "prod" tüm prod kümelerini kapsar. Bölme
			// küme etiketinin canlı değerleriyle doğrulanır (yapılandırılmış küme
			// etiketi, yoksa openshift_cluster_name); okunamazsa ≥3 karakter kuralı.
			cl := clusterLabel
			if cl == "" {
				cl = "openshift_cluster_name"
			}
			var clusters []string
			if cl != el.Label {
				if vals, _, cerr := s.metricEnvLabelValues(ctx, vm, metric, cl, to); cerr == nil {
					clusters = vals
				}
			}
			// Follow-up 2026-11-21 — yalnız-küme değerleri (deployment_environment_name
			// = "<küme>") küme varlığının ortamıyla eşlenir (cluster_env_map.go).
			m = vmetrics.MatchEnvRole(want, live, clusters, s.clusterEnvMap(ctx, to))
		}
		if role == vmetrics.RoleCluster && label == el.Label && len(clusterEnvs) > 0 {
			if cm := matchClusterEnvs(clusterEnvs, live); cm.Tier != vmetrics.EnvTierNone {
				m = cm
			}
		}
		sc.Terms = append(sc.Terms, httpEnvTerm{Role: role, Want: want, Label: label, Values: m.Values, Tier: m.Tier})
		if m.Tier == vmetrics.EnvTierNone {
			if sc.Reason == "" {
				sc.Reason = httpEnvUnresolvedReason(label, want)
			}
			continue
		}
		if prev, ok := byLabel[label]; ok {
			byLabel[label] = intersectStrings(prev, m.Values)
			if len(byLabel[label]) == 0 && sc.Reason == "" {
				sc.Reason = "env ve cluster aynı etikette kesişmiyor"
			}
			continue
		}
		byLabel[label] = m.Values
		order = append(order, label)
	}
	if len(sc.Terms) == 0 {
		sc.Reason = "süzgeç terimi yok"
		return sc, nil
	}
	if sc.Reason != "" {
		return sc, nil
	}
	parts := make([]string, 0, len(order))
	for _, l := range order {
		parts = append(parts, vmetrics.EnvMatcher(l, byLabel[l]))
	}
	sc.Matcher, sc.Applied = strings.Join(parts, ", "), true
	return sc, nil
}

// intersectStrings — SAF: a ∩ b, a'nın sırasıyla.
func intersectStrings(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, v := range b {
		in[v] = true
	}
	var out []string
	for _, v := range a {
		if in[v] {
			out = append(out, v)
		}
	}
	return out
}

// httpEnvScopeFilters — SAF (v0.10.1377): uygulanmış kapsamı metrik süzgeç
// ifadelerine çevirir (etiket başına bir ifade, terimlerin etiket sırasıyla;
// aynı etikette iki terim → değer kesişimi, resolveHTTPEnvScope ile aynı).
// Op "=" — VM derleyicisi (vmetrics promMatcher) tek değeri `label="v"`,
// çoklu değeri regexp.QuoteMeta'lı `label=~"a|b"` yazar. Uygulanmamış kapsam
// → nil (süzgeç YOK; çağıran EnvAmbiguous der).
func httpEnvScopeFilters(sc httpEnvScope) []chstore.FilterExpr {
	if !sc.Applied {
		return nil
	}
	byLabel := map[string][]string{}
	var order []string
	for _, t := range sc.Terms {
		if t.Label == "" || len(t.Values) == 0 {
			continue
		}
		if prev, ok := byLabel[t.Label]; ok {
			byLabel[t.Label] = intersectStrings(prev, t.Values)
			continue
		}
		byLabel[t.Label] = append([]string(nil), t.Values...)
		order = append(order, t.Label)
	}
	out := make([]chstore.FilterExpr, 0, len(order))
	for _, l := range order {
		if len(byLabel[l]) == 0 {
			return nil
		}
		out = append(out, chstore.FilterExpr{Key: l, Op: "=", Values: byLabel[l]})
	}
	return out
}
