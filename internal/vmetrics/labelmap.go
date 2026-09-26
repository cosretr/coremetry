package vmetrics

// v0.10.944 (CoSRE araştırma asistanı, çapraz kaynak eşlemesi — VM yarısı).
//
// SORUN. Bir metrik sorusundaki bağlam boyutları (servis, ortam, cluster,
// namespace, pod, sürüm) VictoriaMetrics'te HANGİ etiket adında yaşıyor?
// Kurulumdan kuruluma değişiyor: OTLP ile yazan bir kurulumda
// `k8s_namespace_name`, kube-state-metrics/cAdvisor kazıyan bir kurulumda
// `namespace`, ortam ise `deployment_environment_name` / `deployment_environment`
// ya da hiç. Bugüne dek tek kural promLabel'dı (service.name → service_name) ve
// geri kalan boyutlar metrik yolunda hiç ifade edilemiyordu; env süzgeci
// bilinçli olarak REDDEDİLİYORDU (metricsource.go EnvFilterExpr).
//
// ÇÖZÜM, ÜÇ KATMAN, bu sırayla:
//
//  1. configured — operatörün LabelMap'i (Settings → victoria_metrics blobu).
//     Doluysa KAZANIR, metrikte görülmese bile (operatörün açık kararı; tool
//     bunu notla söyler).
//  2. convention — ürünün bugünkü varsayılanı. Yalnız servis için var:
//     `service_name` (promLabel kuralı; QueryMetric'in servis süzgeci). Keşif
//     yapıldıysa etiketin metrikte GÖRÜLMESİ şart; keşif yoksa bugünkü davranış.
//  3. discovered — metriğin CANLI etiket kümesinde (/api/v1/labels) aday
//     listesinden ilk görülen. Aday listeleri OTel semconv → promLabel ve
//     yaygın Prometheus yazımlarıdır; hiçbiri UYDURULMUŞ bir kurulum adı değil.
//     v0.10.944 — görülen diğer adaylar Alternatives'e düşer (belirsizlik
//     raporda görünür; çağıran kısmi işaretler).
//
// Hiçbiri yoksa "none": süzgeç UYGULANAMAZ ve çağıran bunu kısmi (partial)
// olarak raporlamak ZORUNDA — sessizce düşürülen süzgeç, v0.9.566 sınıfının
// (yanlış ama makul sayı) ta kendisi.
//
// SAF: çözümleyici ağ görmez; keşif sonucunu argüman olarak alır. Tablo testli
// (labelmap_test.go).

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// LabelMap — rol → VM etiket adı. Boş alan = konvansiyon/keşif.
type LabelMap struct {
	Service   string `json:"service,omitempty"`
	Env       string `json:"env,omitempty"`
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Pod       string `json:"pod,omitempty"`
	Version   string `json:"version,omitempty"`
}

// Bağlam rolleri — tool argümanlarının ve eşleme raporunun anahtarları.
const (
	RoleService   = "service"
	RoleEnv       = "env"
	RoleCluster   = "cluster"
	RoleNamespace = "namespace"
	RolePod       = "pod"
	RoleVersion   = "version"
)

// LabelRoles — rapor sırası (sabit; model her çağrıda aynı sırayı görür).
var LabelRoles = []string{RoleService, RoleEnv, RoleCluster, RoleNamespace, RolePod, RoleVersion}

// Eşleme kaynakları.
const (
	SourceConfigured = "configured"
	SourceConvention = "convention"
	SourceDiscovered = "discovered"
	SourceNone       = "none"
)

// Get — rolün yapılandırılmış etiketi ("" = yok).
func (m LabelMap) Get(role string) string {
	switch role {
	case RoleService:
		return strings.TrimSpace(m.Service)
	case RoleEnv:
		return strings.TrimSpace(m.Env)
	case RoleCluster:
		return strings.TrimSpace(m.Cluster)
	case RoleNamespace:
		return strings.TrimSpace(m.Namespace)
	case RolePod:
		return strings.TrimSpace(m.Pod)
	case RoleVersion:
		return strings.TrimSpace(m.Version)
	}
	return ""
}

// Normalized — boşluklar kırpılmış kopya (elle düzenlenmiş blob ya da
// formdan gelen " service_name " aynı etiketi anlatır).
func (m LabelMap) Normalized() LabelMap {
	return LabelMap{
		Service: m.Get(RoleService), Env: m.Get(RoleEnv), Cluster: m.Get(RoleCluster),
		Namespace: m.Get(RoleNamespace), Pod: m.Get(RolePod), Version: m.Get(RoleVersion),
	}
}

var labelNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidLabelName — MetricsQL etiket adı dilbilgisi. Noktalı ad
// (`k8s.pod.name`) GEÇERSİZ: VM'de o boyut `k8s_pod_name` olarak yaşar ve
// eşlemeye noktalı adı kabul edip promLabel'la sessizce çevirmek, operatörün
// yazdığından farklı bir etiketi sorgulamak olurdu.
func ValidLabelName(s string) bool { return labelNameRe.MatchString(s) }

// LabelMapProblem — PUT doğrulaması: "" = geçerli, aksi hâlde 400 metni.
// Tek yazım: api (mergeVMSettings) bu fonksiyonu çağırır, kendi kopyasını
// tutmaz.
func LabelMapProblem(m LabelMap) string {
	n := m.Normalized()
	for _, role := range LabelRoles {
		v := n.Get(role)
		if v == "" || ValidLabelName(v) {
			continue
		}
		return fmt.Sprintf("labelMap.%s %q geçersiz — VictoriaMetrics etiket adı "+
			"[a-zA-Z_][a-zA-Z0-9_]* olmalı (ör. %s)", role, v, promLabel(v))
	}
	return ""
}

// LabelName — bir öznitelik anahtarının VM etiket adı (promLabel'ın dış
// kapısı): `resource.k8s.pod.name` → `k8s_pod_name`, `http.route` →
// `http_route`. Zaten geçerli bir etiket adı değişmeden döner.
func LabelName(key string) string { return promLabel(key) }

// roleConvention — rolün keşifsiz varsayılanı. Yalnız servis: QueryMetric'in
// servis süzgeci bugün de `service_name` (serviceLabel) üzerinden gider.
func roleConvention(role string) string {
	if role == RoleService {
		return serviceLabel()
	}
	return ""
}

// RoleCandidates — rolün keşif adayları, deneme sırasıyla. Kaynaklar
// UYDURULMUŞ değil, depodaki mevcut listelerden türetilir:
//
//	service   → chstore.ServiceIdentityLabels (throughput eşleyicisinin kimlik
//	            listesi) promLabel'dan geçmiş hâli
//	env       → chstore.EnvAttrKeys (metrik ortam anahtarları) promLabel'lı
//	cluster   → metricClusterExpr'in anahtarları (k8s / openshift / düz)
//	namespace → OTel k8s.namespace.name + kube-state-metrics `namespace`
//	pod       → OTel k8s.pod.name + kube-state-metrics `pod`
//	version   → OTel service.version
//
// Konvansiyon etiketi (service_name) listede YOK — önce o denenir.
func RoleCandidates(role string) []string {
	var keys []string
	switch role {
	case RoleService:
		keys = chstore.ServiceIdentityLabels
	case RoleEnv:
		keys = chstore.EnvAttrKeys
	case RoleCluster:
		keys = []string{"k8s.cluster.name", "openshift.cluster.name", "cluster"}
	case RoleNamespace:
		keys = []string{"k8s.namespace.name", "namespace"}
	case RolePod:
		keys = []string{"k8s.pod.name", "pod"}
	case RoleVersion:
		keys = []string{"service.version"}
	}
	conv := roleConvention(role)
	out := make([]string, 0, len(keys))
	seen := map[string]bool{conv: true}
	for _, k := range keys {
		l := promLabel(k)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// LabelResolution — bir rolün çözümü.
type LabelResolution struct {
	Role   string `json:"role"`
	Label  string `json:"label,omitempty"`
	Source string `json:"source"`
	// Present — etiket metriğin keşfedilen kümesinde görüldü mü. Keşif
	// yapılmadıysa false (bilinmiyor); configured bir etiketin görülmemesi
	// sonucu BOŞ bırakabilir ve çağıran bunu not düşer.
	Present bool `json:"present"`
	// Alternatives — v0.10.944 — keşifte Label'dan SONRA görülen diğer aday
	// yazımlar (yalnız discovered). MetricsQL tek eşleştiricide iki etiket
	// ADINI VEYA'layamaz (metricsource.go EnvFilterExpr'in bilinçli reddi);
	// ilk yazımı seçip susmak, diğerini taşıyan serileri gizler — çağıran
	// bunu kısmi (partial) olarak raporlar. CH ikizi tüm yazımları birleştirir
	// (metricEnvExpr / metricClusterExpr).
	Alternatives []string `json:"alternatives,omitempty"`
}

// Applied — süzgeç uygulanabilir mi (bir etiket çözüldü).
func (r LabelResolution) Applied() bool { return r.Label != "" && r.Source != SourceNone }

// String — rapor biçimi: "<etiket> (<kaynak>)" ya da "none". v0.10.944 —
// başka yazımlar da görüldüyse "<etiket> (discovered; ayrıca: a, b)": eşleme
// raporu her yazımı göstersin.
func (r LabelResolution) String() string {
	if !r.Applied() {
		return SourceNone
	}
	if len(r.Alternatives) > 0 {
		return r.Label + " (" + r.Source + "; ayrıca: " + strings.Join(r.Alternatives, ", ") + ")"
	}
	return r.Label + " (" + r.Source + ")"
}

// ResolveLabelRole — tek rolün çözümü. `present` metriğin keşfedilen etiket
// kümesi; `discovered` keşfin YAPILIP YAPILMADIĞI (boş küme ile "keşif yok"
// farklı şeyler: ilki "bu metrikte etiket yok", ikincisi "bilmiyoruz").
func ResolveLabelRole(role string, m LabelMap, present map[string]bool, discovered bool) LabelResolution {
	r := LabelResolution{Role: role, Source: SourceNone}
	if cfg := m.Get(role); cfg != "" {
		r.Label, r.Source, r.Present = cfg, SourceConfigured, present[cfg]
		return r
	}
	if conv := roleConvention(role); conv != "" && (!discovered || present[conv]) {
		r.Label, r.Source, r.Present = conv, SourceConvention, present[conv]
		return r
	}
	if !discovered {
		return r
	}
	// v0.10.944 — ilk görülende DURMA: tüm adaylar taranır, ilki Label,
	// kalanı Alternatives (belirsizlik görünür olsun; süzgeç yine tek etiket).
	for _, c := range RoleCandidates(role) {
		if !present[c] {
			continue
		}
		if r.Label == "" {
			r.Label, r.Source, r.Present = c, SourceDiscovered, true
			continue
		}
		r.Alternatives = append(r.Alternatives, c)
	}
	return r
}

// ResolveLabelRoles — LabelRoles sırasıyla tüm rollerin çözümü.
func ResolveLabelRoles(m LabelMap, present []string, discovered bool) []LabelResolution {
	set := make(map[string]bool, len(present))
	for _, p := range present {
		set[p] = true
	}
	out := make([]LabelResolution, 0, len(LabelRoles))
	for _, role := range LabelRoles {
		out = append(out, ResolveLabelRole(role, m, set, discovered))
	}
	return out
}
