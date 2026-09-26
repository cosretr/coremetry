// Package argocd — v0.10.957 — Rollouts v2 P1.4: Argo CD ayar blobu
// (system_settings["argocd"]; docs/rollouts/v2-audit.md §10.7, §7.2–7.3,
// §7.5, §5.2–5.6; operatör onaylı karar 2 ve 5, 2026-09-26).
//
// SAF paket: ağ yok, CH yok. Blobun şekli, varsayılanları, okuma-yolu
// kelepçesi (Normalized), PUT doğrulaması (Validate, alan yolu taşıyan
// FieldError), düz-token reddi (ParseInput), apiUrl normalleştirmesi ve
// kalıcılık (LoadPersisted/SavePersisted; rollouts emsali — boot + 30 s
// yenileme + admin PUT + publishConfigReload). Keşif adaylarının saf
// türetimi discover.go'da; probe'un kendisi api katmanında (thanos konsol
// taşıması üzerinden label-values).
//
// P1 = davranış değişikliği YOK: bu blobu kendi GET/PUT/doğrulamasından
// başka hiçbir şey okumaz; işçi yok (P3.1/P3.3), bayrak varsayılan kapalı.
//
// ── NEDEN tokenRef VAR, token YOK ────────────────────────────────────────
//
// Instance başına Argo CD API kimliği yalnız `tokenRef` (env:NAME |
// file:/path, internal/secretref) olarak saklanır; düz token alanı HİÇ
// yok (thanos/oracle/devops'tan sıkı). Gerekçe: config export
// system_settings'i olduğu gibi döker (config_iox.go) — düz alan dışarı
// sızardı. Çözülemeyen ref FAIL-CLOSED (Token hata döner, işçi instance'ı
// atlar); thanos başlıksız istek atar, burada atılmaz (§7.2).
//
// ── NEDEN sayısal sıfır = varsayılan ─────────────────────────────────────
//
// Blob, rollouts emsali gibi operatörün GİRDİĞİNİ saklar; uygulanan
// değer Normalized'dır (sıfır/negatif → korunan varsayılan, alt sınır
// altı → alt sınır, tavan üstü → tavan). Böylece varsayılan sonradan
// değişirse eski bloblar da yeni varsayılanı alır ve elle yazılmış bozuk
// bir satır okuma yolunda sıfır timeout'lu ya da sınırsız bir okuyucu
// üretemez. PUT ise aralık dışını 400 ile REDDEDER (kelepçe operatörün
// yazım hatasını gizlemek için değil).
package argocd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/secretref"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// SettingsKey — system_settings anahtarı (tek JSON blob; CLAUDE.md
// invariant 6).
const SettingsKey = "argocd"

// Sınıflandırma kipinin metrics-only değerleri (§7.5, karar 15 — P1'de
// yalnız ayar; okuyan yok). estimate: API kimliği yokken metrik tabanlı
// "tahmini" argo_manual/argo_auto gösterilir; unknown: yalnız unknown.
const (
	MetricsOnlyEstimate = "estimate"
	MetricsOnlyUnknown  = "unknown"
)

// InClusterServer — Argo'nun "hub'ın kendisi" hedefi (§5.3).
const InClusterServer = "https://kubernetes.default.svc"

// Settings — system_settings["argocd"] (audit §10.7 + §5.6 "iki hub").
// JSON adları FE ve P3 işçileriyle sözleşmedir (TestSettingsJSONFieldNames).
//
// v0.10.957 — inceleme: Argo CD İKİ hub kümesinde koşar (operatör,
// 2026-09-26; §5.6). Tek `hubClusterId` + üst düzey `injectClusterLabel`
// yerine `hubs[{clusterId, injectClusterLabel}]` (enjeksiyon hub BAŞINA
// karar) ve her instance kendi `hubClusterId`'sini taşır. Karar 5 aynen:
// her hub sıradan etkin bir Remote Cluster + bu blobdaki işaret.
type Settings struct {
	Enabled bool `json:"enabled"`
	// Hubs — Argo metriklerini taşıyan hub Remote Cluster'ları (en çok
	// maxHubs; clusterId tekil). Keşif hub başına koşar.
	Hubs []Hub `json:"hubs,omitempty"`
	// EnvList — ad ayrıştırma env listesi (§6); küçük harf, tekil.
	EnvList        []string       `json:"envList,omitempty"`
	Instances      []Instance     `json:"instances,omitempty"`
	APIWorker      APIWorker      `json:"apiWorker"`
	Classification Classification `json:"classification"`
	// Pins — elle iş yükü ↔ Application kenarları (§10.3.5: mapper
	// match_method='manual' olarak kopyalar; "insan pini otomatiği yener").
	Pins      []Pin     `json:"pins,omitempty"`
	Reader    Reader    `json:"reader"`
	Intervals Intervals `json:"intervals"`
	Mapping   Mapping   `json:"mapping"`
	UpdatedAt int64     `json:"updatedAt,omitempty"`
}

// Hub — v0.10.957 — bir Argo hub'ı: Remote Cluster EffectiveID'si +
// o hub'ın Thanos küme etiketinin Argo sorgularına enjekte edilip
// edilmeyeceği (§5.6: H0.3 vs H0.5 her hub'da ayrı; nil = true = bugünkü
// her Thanos sorgusunun davranışı).
type Hub struct {
	ClusterID          string `json:"clusterId"`
	InjectClusterLabel *bool  `json:"injectClusterLabel,omitempty"`
}

// Inject — v0.10.957 — uygulanan enjeksiyon (nil = true).
func (h Hub) Inject() bool { return h.InjectClusterLabel == nil || *h.InjectClusterLabel }

// Instance — bir Argo CD örneği (bir hub üzerinde bir namespace).
type Instance struct {
	ID string `json:"id"` // TÜM hub'larda tekil; CH instance_id (§10.3.3, §5.6)
	// HubClusterID — v0.10.957 — örneğin koştuğu hub (hubs[].clusterId;
	// §5.6). https://kubernetes.default.svc bu hub'a çözülür.
	HubClusterID       string `json:"hubClusterId"`
	Name               string `json:"name,omitempty"`       // görünen ad
	HubNamespace       string `json:"hubNamespace"`         // örneğin çalıştığı ns (§5.2); hub başına tekil
	MetricsJob         string `json:"metricsJob,omitempty"` // `job` etiketi (<team>-<env>-metrics)
	APIURL             string `json:"apiUrl,omitempty"`     // ASLA türetilmez (§7.1); normalleşmiş
	TokenRef           string `json:"tokenRef,omitempty"`   // env:NAME | file:/path — tek kimlik alanı
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
	Enabled            bool   `json:"enabled"`
	Discovered         bool   `json:"discovered,omitempty"`       // keşif önerisinden geldi
	AppsAnyNamespace   bool   `json:"appsAnyNamespace,omitempty"` // exported_namespace ≠ namespace (§5.2 durum C)
}

// APIWorker — Argo API işçisinin istemci-taraflı kısıtı (§7.3: sunucuda
// genel hız sınırı yok, throttling bizde).
type APIWorker struct {
	RPS           float64 `json:"rps,omitempty"`           // instance başına token-bucket hızı (1)
	Burst         int     `json:"burst,omitempty"`         // (5)
	MaxConcurrent int     `json:"maxConcurrent,omitempty"` // instance başına eşzamanlı istek (2)
}

// Classification — pencereler dakika (§7.5; KSM çapası ±5 dk, out_of_band
// [çapa−30 dk, çapa+5 dk]).
type Classification struct {
	WindowMin            int    `json:"windowMin,omitempty"`
	OutOfBandLookbackMin int    `json:"outOfBandLookbackMin,omitempty"`
	MetricsOnlyMode      string `json:"metricsOnlyMode,omitempty"` // estimate | unknown
}

// Pin — elle eşleme; WorkloadKind rollout_events.workload_kind yazımıyla.
type Pin struct {
	ClusterID    string `json:"clusterId"`
	Namespace    string `json:"namespace"`
	WorkloadKind string `json:"workloadKind"`
	Workload     string `json:"workload"`
	InstanceID   string `json:"instanceId"`
	AppNamespace string `json:"appNamespace"`
	AppName      string `json:"appName"`
}

// Reader — hub okuyucusunun sınırları; karar 2 tavanları (≤ 50 000 seri,
// ≤ 64 MiB gövde, timeout 30 s varsayılan / 45 s tavan) — sayılar
// thanos.Worker* sabitlerinden (thanos.WorkerLimits normalleştirmesiyle aynı).
type Reader struct {
	MaxSeries  int `json:"maxSeries,omitempty"`
	MaxBodyMiB int `json:"maxBodyMiB,omitempty"`
	TimeoutS   int `json:"timeoutS,omitempty"`
}

// Intervals — işçi kadansları (§10.4, §5.4).
type Intervals struct {
	MetricsS          int `json:"metricsS,omitempty"`
	InventoryMin      int `json:"inventoryMin,omitempty"`
	MapperMin         int `json:"mapperMin,omitempty"`
	ClassifierReevalH int `json:"classifierReevalH,omitempty"`
}

// Mapping — tahmini (ad) ve zayıf (namespace) kenar güvenleri, 0..100
// (annex §7.4: 0.7 / 0.3; manual ve resource sabit 100 — ayar değil).
type Mapping struct {
	NameConfidence      int `json:"nameConfidence,omitempty"`
	NamespaceConfidence int `json:"namespaceConfidence,omitempty"`
}

// Bound — GET `bounds` girdisi (UI input min/max/varsayılan).
type Bound struct {
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"default"`
}

// intField — tamsayı alanlarının TEK tablosu: Normalized, Validate ve
// Bounds buradan okur; sınır kodda ve UI'da ayrışamaz (promql_console
// emsali).
type intField struct {
	path          string
	min, max, def int
	ptr           func(*Settings) *int
}

var intFields = []intField{
	{"apiWorker.burst", 1, 50, 5, func(s *Settings) *int { return &s.APIWorker.Burst }},
	{"apiWorker.maxConcurrent", 1, 10, 2, func(s *Settings) *int { return &s.APIWorker.MaxConcurrent }},
	{"classification.windowMin", 1, 60, 5, func(s *Settings) *int { return &s.Classification.WindowMin }},
	{"classification.outOfBandLookbackMin", 5, 240, 30, func(s *Settings) *int { return &s.Classification.OutOfBandLookbackMin }},
	// karar 2: seri tavanı 50 000; alt sınır 1000 — daha küçüğü her okumayı
	// kesik (truncated) yapar ve işçi o parçanın diff'ini hep atlardı.
	// Tavan/varsayılanlar lane R1'in dışa açtığı thanos.Worker* sabitleri (tek
	// doğruluk kaynağı; worker_query.go "argocd reader{} aynı tavanlara kırpar").
	{"reader.maxSeries", 1000, thanos.WorkerMaxSeries, thanos.WorkerMaxSeries, func(s *Settings) *int { return &s.Reader.MaxSeries }},
	{"reader.maxBodyMiB", 1, thanos.WorkerMaxBodyMiB, thanos.WorkerMaxBodyMiB, func(s *Settings) *int { return &s.Reader.MaxBodyMiB }},
	{"reader.timeoutS", 5, thanos.WorkerMaxTimeoutS, thanos.WorkerDefaultTimeoutS, func(s *Settings) *int { return &s.Reader.TimeoutS }},
	{"intervals.metricsS", 30, 600, 60, func(s *Settings) *int { return &s.Intervals.MetricsS }},
	{"intervals.inventoryMin", 5, 120, 15, func(s *Settings) *int { return &s.Intervals.InventoryMin }},
	{"intervals.mapperMin", 5, 240, 10, func(s *Settings) *int { return &s.Intervals.MapperMin }},
	{"intervals.classifierReevalH", 1, 168, 24, func(s *Settings) *int { return &s.Intervals.ClassifierReevalH }},
	{"mapping.nameConfidence", 1, 100, 70, func(s *Settings) *int { return &s.Mapping.NameConfidence }},
	{"mapping.namespaceConfidence", 1, 100, 30, func(s *Settings) *int { return &s.Mapping.NamespaceConfidence }},
}

// rps sınırları (float; tablo dışı): 0.1 – 20 istek/s, varsayılan 1 (§7.3).
const (
	rpsMin, rpsMax, rpsDef = 0.1, 20.0, 1.0
)

// Liste tavanları — blob küçük kalsın (config export + audit details
// bütün blobu taşır); gerçekçi ölçek onlarca instance, yüzlerce pin.
const (
	maxHubs      = 8 // v0.10.957 — bugün iki hub (§5.6); pay bırakır
	maxEnvs      = 50
	maxEnvLen    = 32
	maxInstances = 100
	maxPins      = 500
	maxNameLen   = 128
	maxJobLen    = 256
	maxFieldLen  = 253
)

var (
	// instanceIDRe — CH instance_id + URL-güvenli: DNS etiketi biçimi.
	instanceIDRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// dnsLabelRe — Kubernetes namespace (RFC 1123 etiketi).
	dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	// envRe — ad ayrıştırmada env TEK tire-sınırlı jeton (§6 regex'i);
	// tireli env ayrıştırmayı bozar.
	envRe = regexp.MustCompile(`^[a-z0-9]+$`)
)

// workloadKinds — pin türlerinin kanonik yazımı (rollout_events /
// argocd_sync_events 'Kind/namespace/name' sözlüğü, §10.3.1/§10.3.4).
var workloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "DeploymentConfig", "Rollout"}

// secretKeys — PUT gövdesinde görülürse 400: düz gizli değer saklanmaz.
var secretKeys = map[string]bool{"token": true, "apitoken": true, "authtoken": true, "bearertoken": true, "password": true}

func boolPtr(b bool) *bool { return &b }

// DefaultSettings — korunan varsayılanlar (§7.3, §7.5, §10.7, karar 2).
// Bayrak KAPALI, hub/instance/pin yok.
func DefaultSettings() Settings {
	var s Settings
	for _, f := range intFields {
		*f.ptr(&s) = f.def
	}
	s.APIWorker.RPS = rpsDef
	s.Classification.MetricsOnlyMode = MetricsOnlyEstimate
	return s
}

// Bounds — GET `bounds` (UI min/max/varsayılan) — tablodan.
func Bounds() map[string]Bound {
	out := make(map[string]Bound, len(intFields)+1)
	for _, f := range intFields {
		out[f.path] = Bound{Min: float64(f.min), Max: float64(f.max), Default: float64(f.def)}
	}
	out["apiWorker.rps"] = Bound{Min: rpsMin, Max: rpsMax, Default: rpsDef}
	return out
}

// copyHubs — v0.10.957 — derin kopya (InjectClusterLabel işaretçisi
// paylaşılmasın: okuyucu kopyayı değiştirse canlı ayar bozulmaz).
func copyHubs(in []Hub) []Hub {
	if in == nil {
		return nil
	}
	out := make([]Hub, len(in))
	for i, h := range in {
		out[i] = Hub{ClusterID: h.ClusterID}
		if h.InjectClusterLabel != nil {
			out[i].InjectClusterLabel = boolPtr(*h.InjectClusterLabel)
		}
	}
	return out
}

// HubByID — v0.10.957 — hubs[] içinde clusterId ile.
func (s Settings) HubByID(clusterID string) (Hub, bool) {
	for _, h := range s.Hubs {
		if h.ClusterID == clusterID {
			return h, true
		}
	}
	return Hub{}, false
}

// Normalized — SAF okuma-yolu kelepçesi: UYGULANAN değerler. Sıfır/negatif
// → varsayılan, alt sınır altı → alt sınır, tavan üstü → tavan (karar 2
// tavanları dahil); hub başına nil injectClusterLabel → true; bilinmeyen
// kip → estimate; out_of_band penceresi eşleşme penceresinden kısa olamaz.
// Dilimler KOPYALANIR (okuyucu kopyayı değiştirse canlı ayar bozulmaz).
func (s Settings) Normalized() Settings {
	out := s
	out.EnvList = append([]string(nil), s.EnvList...)
	out.Instances = append([]Instance(nil), s.Instances...)
	out.Pins = append([]Pin(nil), s.Pins...)
	out.Hubs = copyHubs(s.Hubs)
	for i := range out.Hubs {
		out.Hubs[i].InjectClusterLabel = boolPtr(out.Hubs[i].Inject())
	}
	for _, f := range intFields {
		v := f.ptr(&out)
		switch {
		case *v <= 0:
			*v = f.def
		case *v < f.min:
			*v = f.min
		case *v > f.max:
			*v = f.max
		}
	}
	switch r := out.APIWorker.RPS; {
	case math.IsNaN(r) || math.IsInf(r, 0) || r <= 0:
		out.APIWorker.RPS = rpsDef
	case r < rpsMin:
		out.APIWorker.RPS = rpsMin
	case r > rpsMax:
		out.APIWorker.RPS = rpsMax
	}
	switch m := strings.ToLower(strings.TrimSpace(out.Classification.MetricsOnlyMode)); m {
	case MetricsOnlyEstimate, MetricsOnlyUnknown:
		out.Classification.MetricsOnlyMode = m
	default:
		out.Classification.MetricsOnlyMode = MetricsOnlyEstimate
	}
	if out.Classification.OutOfBandLookbackMin < out.Classification.WindowMin {
		out.Classification.OutOfBandLookbackMin = out.Classification.WindowMin
	}
	return out
}

// FieldError — PUT doğrulama hatası; Path JSON alan yolu
// ("instances[2].apiUrl"). API 400 gövdesine {error, field} olarak gider.
type FieldError struct {
	Path string
	Msg  string
}

func (e *FieldError) Error() string {
	if e.Path == "" {
		return e.Msg
	}
	return e.Path + ": " + e.Msg
}

func fieldErr(path, format string, args ...any) *FieldError {
	return &FieldError{Path: path, Msg: fmt.Sprintf(format, args...)}
}

// ClusterRef — Remote Cluster kaydının doğrulamanın gördüğü dar yüzü
// (api katmanı thanos anlık görüntüsünden doldurur; token taşımaz).
type ClusterRef struct {
	ID            string
	Name          string
	Enabled       bool
	APIServerURLs []string // lane R2 (P1.3) alanı; boşsa in-cluster kuralı sessiz
}

// ParseInput — PUT gövdesi → Settings. Düz gizli alan (token, password …;
// büyük/küçük harf duyarsız) üst düzeyde ya da bir instance'ta görülürse
// FieldError: decoder bilinmeyen alanı sessizce atardı ve operatör
// token'ını kaydettiğini sanardı. Bilinmeyen DİĞER alanlar yok sayılır
// (ileri uyum; GET cevabının ek alanları).
func ParseInput(raw []byte) (Settings, error) {
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, fieldErr("", "geçersiz JSON: %v", err)
	}
	var probe struct {
		Instances []map[string]json.RawMessage `json:"instances"`
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(raw, &top)
	for _, k := range sortedKeys(top) {
		if secretKeys[strings.ToLower(k)] {
			return Settings{}, fieldErr(k, "düz gizli değer saklanmaz; instance başına tokenRef kullanın (env:NAME | file:/path)")
		}
		// v0.10.957 — inceleme: tek-hub şekli (§10.7 ilk taslağı) iki hub
		// kararıyla (§5.6) kalktı; decoder bu anahtarları SESSİZCE atardı ve
		// operatör hub'ı kaydettiğini sanardı (token reddiyle aynı gerekçe).
		if k == "hubClusterId" || k == "injectClusterLabel" {
			return Settings{}, fieldErr(k, "üst düzey alan yok; hubs[{clusterId, injectClusterLabel}] ve instance başına hubClusterId kullanın (iki hub, audit §5.6)")
		}
	}
	_ = json.Unmarshal(raw, &probe)
	for i, inst := range probe.Instances {
		for _, k := range sortedKeys(inst) {
			if secretKeys[strings.ToLower(k)] {
				return Settings{}, fieldErr(fmt.Sprintf("instances[%d].%s", i, k),
					"düz token saklanmaz; tokenRef kullanın (env:NAME | file:/path)")
			}
		}
	}
	return s, nil
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hasControl — kontrol karakteri (satır sonu dahil) var mı.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// Validate — SAF PUT doğrulaması + kanonikleştirme. Dönen blob kalıcı
// yazılandır: dizeler kırpılmış, envList küçük harf/tekil/boşsuz, apiUrl
// normalleşmiş, pin türleri kanonik, kip küçük harf; sayısal sıfırlar
// SIFIR kalır (= varsayılan; uygulanan Normalized). UpdatedAt sunucu
// sahiplidir (girdi atılır). İlk hata döner (alan yolu ile).
//
// clusters: Remote Cluster kayıtları (etkin + devre dışı). Her
// hubs[].clusterId bir kayda EŞİT olmalı (ad değil id) ve tekil; bayrak
// açıkken en az bir hub zorunlu ve her hub etkin olmalı (karar 5, §5.6).
// Her instance hubs'taki bir hub'a bağlıdır (tek hub varsa boş alan o
// hub'a tamamlanır). https://kubernetes.default.svc yalnız bir HUB kaydının
// apiServerUrls'ünde bulunabilir (§3.2, §5.3: instance'ın kendi hub'ı).
// v0.10.957 — thanos PUT'u artık bu adresi hiçbir kayda yazdırmıyor (iki hub,
// §5.6); buradaki denetim config import'la gelen eski/el yapımı bloblara karşı
// savunma olarak kalır.
func Validate(in Settings, clusters []ClusterRef) (Settings, error) {
	out := in
	out.UpdatedAt = 0

	byID := make(map[string]ClusterRef, len(clusters))
	for _, c := range clusters {
		byID[c.ID] = c
	}
	hubs, err := canonicalHubs(out.Enabled, in.Hubs, byID, clusters)
	if err != nil {
		return in, err
	}
	out.Hubs = hubs

	envs, err := canonicalEnvs(in.EnvList)
	if err != nil {
		return in, err
	}
	out.EnvList = envs

	insts, ids, err := canonicalInstances(in.Instances, hubs)
	if err != nil {
		return in, err
	}
	out.Instances = insts

	pins, err := canonicalPins(in.Pins, ids, byID)
	if err != nil {
		return in, err
	}
	out.Pins = pins

	for _, f := range intFields {
		v := *f.ptr(&out)
		if v < 0 {
			return in, fieldErr(f.path, "negatif olamaz (%d); 0 = varsayılan %d", v, f.def)
		}
		if v != 0 && (v < f.min || v > f.max) {
			return in, fieldErr(f.path, "%d–%d olmalı (0 = varsayılan %d), girilen %d", f.min, f.max, f.def, v)
		}
	}
	if r := out.APIWorker.RPS; math.IsNaN(r) || math.IsInf(r, 0) || r < 0 || (r != 0 && (r < rpsMin || r > rpsMax)) {
		return in, fieldErr("apiWorker.rps", "%g–%g olmalı (0 = varsayılan %g)", rpsMin, rpsMax, rpsDef)
	}
	mode := strings.ToLower(strings.TrimSpace(in.Classification.MetricsOnlyMode))
	if mode != "" && mode != MetricsOnlyEstimate && mode != MetricsOnlyUnknown {
		return in, fieldErr("classification.metricsOnlyMode", "%q geçersiz; %q ya da %q", in.Classification.MetricsOnlyMode, MetricsOnlyEstimate, MetricsOnlyUnknown)
	}
	out.Classification.MetricsOnlyMode = mode

	// Çapraz alanlar ETKİN değerlerle; PUT'ta REDDEDİLİR (okuma yolu
	// kelepçeler): açıkça yazılmış bir değerin sessizce değişmesi operatöre
	// yalan söylerdi.
	// out_of_band için Normalized'ın yükseltmesinden ÖNCEKİ etkin değer
	// (0 = varsayılan 30): windowMin=45 + boş lookback da reddedilir.
	eff := out.Normalized()
	oob := out.Classification.OutOfBandLookbackMin
	if oob == 0 {
		oob = DefaultSettings().Classification.OutOfBandLookbackMin
	}
	if oob < eff.Classification.WindowMin {
		return in, fieldErr("classification.outOfBandLookbackMin", "%d dk, eşleşme penceresinden (windowMin=%d) kısa olamaz", oob, eff.Classification.WindowMin)
	}
	if eff.Mapping.NamespaceConfidence >= eff.Mapping.NameConfidence {
		return in, fieldErr("mapping.namespaceConfidence", "zayıf (namespace) güveni %d, tahmini (ad) güveninden %d küçük olmalı",
			eff.Mapping.NamespaceConfidence, eff.Mapping.NameConfidence)
	}
	return out, nil
}

// canonicalHubs — v0.10.957 — inceleme (§5.6 iki hub): kırp, var olan ve
// tekil Remote Cluster id'si; bayrak açıkken ≥1 hub ve her hub etkin.
// Küme-içi adres (https://kubernetes.default.svc) yalnız bir hub kaydında
// (hub'ı olmayan blobda kural sessiz: henüz Argo yapılandırılmıyor).
func canonicalHubs(enabled bool, in []Hub, byID map[string]ClusterRef, clusters []ClusterRef) ([]Hub, error) {
	if len(in) > maxHubs {
		return nil, fieldErr("hubs", "en çok %d hub", maxHubs)
	}
	if enabled && len(in) == 0 {
		return nil, fieldErr("hubs", "Argo CD açıkken en az bir hub Remote Cluster zorunlu (karar 5)")
	}
	out := copyHubs(in)
	seen := map[string]int{}
	for i := range out {
		path := fmt.Sprintf("hubs[%d].clusterId", i)
		id := strings.TrimSpace(out[i].ClusterID)
		out[i].ClusterID = id
		c, ok := byID[id]
		if !ok {
			hint := ""
			for _, cand := range clusters {
				if id != "" && cand.Name == id {
					hint = fmt.Sprintf(" (%q bir ad; id'si %q)", id, cand.ID)
					break
				}
			}
			return nil, fieldErr(path, "bilinmeyen Remote Cluster id %q%s", id, hint)
		}
		if j, dup := seen[id]; dup {
			return nil, fieldErr(path, "%q zaten hubs[%d]", id, j)
		}
		seen[id] = i
		if enabled && !c.Enabled {
			return nil, fieldErr(path, "hub Remote Cluster %q devre dışı; Argo CD açıkken her hub etkin olmalı", c.Name)
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	for _, other := range clusters {
		if _, isHub := seen[other.ID]; isHub {
			continue
		}
		for _, u := range other.APIServerURLs {
			if IsInClusterServer(u) {
				return nil, fieldErr("hubs", "Remote Cluster %q %s taşıyor ama Argo hub'ı değil; bu adres yalnız bir hub kaydında geçerli", other.Name, InClusterServer)
			}
		}
	}
	return out, nil
}

func canonicalEnvs(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for i, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue // UI'da sondaki virgül; sessizce atlanır
		}
		if len(e) > maxEnvLen || !envRe.MatchString(e) {
			return nil, fieldErr(fmt.Sprintf("envList[%d]", i), "%q geçersiz: env tek jeton olmalı ([a-z0-9], ≤%d; tire ad ayrıştırmayı bozar)", in[i], maxEnvLen)
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) > maxEnvs {
		return nil, fieldErr("envList", "en çok %d env", maxEnvs)
	}
	return out, nil
}

// canonicalInstances — kırp + doğrula. v0.10.957 — inceleme (§5.6): id
// TÜM hub'larda tekil; hubClusterId hubs'taki bir hub olmalı (tek hub
// varken boş alan o hub'a tamamlanır, birden çokken zorunlu); hubNamespace
// HUB BAŞINA tekil (iki hub'da aynı "openshift-gitops" ayrı instance'tır).
func canonicalInstances(in []Instance, hubs []Hub) ([]Instance, map[string]bool, error) {
	if len(in) > maxInstances {
		return nil, nil, fieldErr("instances", "en çok %d instance", maxInstances)
	}
	hubSet := make(map[string]bool, len(hubs))
	for _, h := range hubs {
		hubSet[h.ClusterID] = true
	}
	out := make([]Instance, 0, len(in))
	ids := map[string]bool{}
	nss := map[[2]string]string{} // (hub, ns) → instance id
	for i, inst := range in {
		p := func(f string) string { return fmt.Sprintf("instances[%d].%s", i, f) }
		inst.ID = strings.TrimSpace(inst.ID)
		inst.HubClusterID = strings.TrimSpace(inst.HubClusterID)
		inst.Name = strings.TrimSpace(inst.Name)
		inst.HubNamespace = strings.TrimSpace(inst.HubNamespace)
		inst.MetricsJob = strings.TrimSpace(inst.MetricsJob)
		inst.TokenRef = strings.TrimSpace(inst.TokenRef)
		if !instanceIDRe.MatchString(inst.ID) {
			return nil, nil, fieldErr(p("id"), "%q geçersiz: küçük harf, rakam, tire; ≤63 (DNS etiketi)", inst.ID)
		}
		if ids[inst.ID] {
			return nil, nil, fieldErr(p("id"), "%q tekrar ediyor (id tüm hub'larda tekil)", inst.ID)
		}
		ids[inst.ID] = true
		switch {
		case inst.HubClusterID == "" && len(hubs) == 1:
			inst.HubClusterID = hubs[0].ClusterID
		case inst.HubClusterID == "" && len(hubs) == 0:
			return nil, nil, fieldErr(p("hubClusterId"), "önce hubs'a bir hub Remote Cluster ekleyin")
		case inst.HubClusterID == "":
			return nil, nil, fieldErr(p("hubClusterId"), "%d hub var; instance'ın hub'ı zorunlu", len(hubs))
		case !hubSet[inst.HubClusterID]:
			return nil, nil, fieldErr(p("hubClusterId"), "%q hubs listesinde değil", inst.HubClusterID)
		}
		if len(inst.Name) > maxNameLen || hasControl(inst.Name) {
			return nil, nil, fieldErr(p("name"), "≤%d karakter, kontrol karakteri yok", maxNameLen)
		}
		if !dnsLabelRe.MatchString(inst.HubNamespace) {
			return nil, nil, fieldErr(p("hubNamespace"), "%q geçersiz Kubernetes namespace'i (zorunlu)", inst.HubNamespace)
		}
		nsKey := [2]string{inst.HubClusterID, inst.HubNamespace}
		if o, dup := nss[nsKey]; dup {
			return nil, nil, fieldErr(p("hubNamespace"), "%q bu hub'da zaten %q instance'ına ait (hub'da namespace başına tek Argo CD)", inst.HubNamespace, o)
		}
		nss[nsKey] = inst.ID
		if len(inst.MetricsJob) > maxJobLen || hasControl(inst.MetricsJob) {
			return nil, nil, fieldErr(p("metricsJob"), "≤%d karakter, kontrol karakteri yok", maxJobLen)
		}
		if inst.APIURL != "" {
			u, err := NormalizeAPIURL(inst.APIURL)
			if err != nil {
				return nil, nil, fieldErr(p("apiUrl"), "%v", err)
			}
			inst.APIURL = u
		}
		if inst.TokenRef != "" && !secretref.Valid(inst.TokenRef) {
			return nil, nil, fieldErr(p("tokenRef"), "%s", secretref.InvalidMessage)
		}
		out = append(out, inst)
	}
	return out, ids, nil
}

func canonicalKind(k string) (string, bool) {
	k = strings.TrimSpace(k)
	for _, c := range workloadKinds {
		if strings.EqualFold(k, c) {
			return c, true
		}
	}
	return "", false
}

func canonicalPins(in []Pin, instanceIDs map[string]bool, clusters map[string]ClusterRef) ([]Pin, error) {
	if len(in) > maxPins {
		return nil, fieldErr("pins", "en çok %d pin", maxPins)
	}
	out := make([]Pin, 0, len(in))
	seen := map[Pin]int{}
	for i, p := range in {
		path := func(f string) string { return fmt.Sprintf("pins[%d].%s", i, f) }
		p.ClusterID = strings.TrimSpace(p.ClusterID)
		p.Namespace = strings.TrimSpace(p.Namespace)
		p.Workload = strings.TrimSpace(p.Workload)
		p.InstanceID = strings.TrimSpace(p.InstanceID)
		p.AppNamespace = strings.TrimSpace(p.AppNamespace)
		p.AppName = strings.TrimSpace(p.AppName)
		if _, ok := clusters[p.ClusterID]; !ok {
			return nil, fieldErr(path("clusterId"), "bilinmeyen Remote Cluster id %q", p.ClusterID)
		}
		if !dnsLabelRe.MatchString(p.Namespace) {
			return nil, fieldErr(path("namespace"), "%q geçersiz Kubernetes namespace'i", p.Namespace)
		}
		kind, ok := canonicalKind(p.WorkloadKind)
		if !ok {
			return nil, fieldErr(path("workloadKind"), "%q geçersiz; %s", p.WorkloadKind, strings.Join(workloadKinds, " | "))
		}
		p.WorkloadKind = kind
		if p.Workload == "" || len(p.Workload) > maxFieldLen || hasControl(p.Workload) {
			return nil, fieldErr(path("workload"), "zorunlu, ≤%d karakter", maxFieldLen)
		}
		if !instanceIDs[p.InstanceID] {
			return nil, fieldErr(path("instanceId"), "bu blobda %q instance'ı yok", p.InstanceID)
		}
		if !dnsLabelRe.MatchString(p.AppNamespace) {
			return nil, fieldErr(path("appNamespace"), "%q geçersiz Kubernetes namespace'i", p.AppNamespace)
		}
		if p.AppName == "" || len(p.AppName) > maxFieldLen || hasControl(p.AppName) {
			return nil, fieldErr(path("appName"), "zorunlu, ≤%d karakter", maxFieldLen)
		}
		if j, dup := seen[p]; dup {
			return nil, fieldErr(fmt.Sprintf("pins[%d]", i), "pins[%d] ile aynı", j)
		}
		seen[p] = i
		out = append(out, p)
	}
	return out, nil
}

// NormalizeAPIURL — Argo CD API adresi (instance başına; §7.1 "asla
// türetilmez"). Kırp, http/https şart, host şart, userinfo/sorgu/parça
// yasak; şema ve host küçük harf, sondaki "/" atılır. Yol KORUNUR
// (argocd-server --rootpath) ve büyük/küçük harfi değişmez. Varsayılan
// port EKLENMEZ: lane R2'nin NormalizeAPIServerURL'ü Kubernetes API için
// :6443 ekler ve yol reddeder — Argo route'u için ikisi de yanlış olurdu.
func NormalizeAPIURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("boş")
	}
	if hasControl(s) || strings.ContainsAny(s, " \t") {
		return "", errors.New("boşluk / kontrol karakteri içeremez")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("URL ayrıştırılamadı")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("http:// ya da https:// ile başlamalı")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("host zorunlu")
	}
	if u.User != nil {
		return "", errors.New("kullanıcı bilgisi (user:pass@) içeremez; kimlik tokenRef ile")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("sorgu (?) ya da parça (#) içeremez")
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	return scheme + "://" + strings.ToLower(u.Host) + path, nil
}

// IsInClusterServer — Argo'nun in-cluster hedefi mi (şema https, host
// kubernetes.default.svc[.cluster.local], port/sondaki "/" fark etmez).
// thanos.IsInClusterAPIServerURL'ün (lane R2) ÜST kümesi: o yalnız
// portsuz/:6443 yazımı tanır; burada :443 ve .cluster.local da yakalanır —
// "yalnız hub'da" kuralı için geniş tespit daha güvenli taraftır.
func IsInClusterServer(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "kubernetes.default.svc" || host == "kubernetes.default.svc.cluster.local"
}

// ── Kalıcılık (rollouts emsali) ────────────────────────────────────────────

// Store — system_settings'in dar yüzü (*chstore.Store karşılar).
type Store interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
}

// ErrNoTokenRef — instance'ın tokenRef'i yok (API işçisi onu atlar;
// metrics-only kip).
var ErrNoTokenRef = errors.New("argocd: instance tokenRef tanımlı değil")

// SettingsService — canlı blob + çözülmüş token'lar (bellek; ASLA JSON'a
// girmez). Her rolde yaşar: api PUT/GET, P3 işçileri okur.
type SettingsService struct {
	mu        sync.RWMutex
	cfg       Settings
	tokens    map[string]string // instance id → çözülmüş token
	tokenErrs map[string]string // instance id → çözüm hatası
	getenv    func(string) string
	readFile  func(string) ([]byte, error)
}

func NewSettingsService() *SettingsService { return newSettingsServiceWith(os.Getenv, os.ReadFile) }

func newSettingsServiceWith(getenv func(string) string, readFile func(string) ([]byte, error)) *SettingsService {
	s := &SettingsService{getenv: getenv, readFile: readFile}
	s.Configure(DefaultSettings())
	return s
}

// Current — saklanan (operatörün girdiği) blob; dilimler kopya.
func (s *SettingsService) Current() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.EnvList = append([]string(nil), s.cfg.EnvList...)
	c.Instances = append([]Instance(nil), s.cfg.Instances...)
	c.Pins = append([]Pin(nil), s.cfg.Pins...)
	c.Hubs = copyHubs(s.cfg.Hubs)
	return c
}

// Resolved — uygulanan değerler (Normalized).
func (s *SettingsService) Resolved() Settings { return s.Current().Normalized() }

// Configure — canlı swap + tokenRef çözümü (thanos Configure deseni: sıcak
// yolda IO yok; file rotasyonu 30 s yenilemede alınır).
func (s *SettingsService) Configure(cfg Settings) { s.apply(cfg, false) }

// apply — v0.10.957 — inceleme: token çözümü (dosya IO'su) kilit DIŞINDA,
// "daha yeni mi" karşılaştırması ve swap kilit İÇİNDE (rollouts
// applyLoaded emsali). Eskiden LoadPersisted Current()'ı okuyup kilidi
// bırakıyor, token'ları çözüyor, sonra koşulsuz swap ediyordu: arada bu
// pod'da biten bir admin PUT'u (SavePersisted) bayat blobla ≤30 s ezilirdi.
func (s *SettingsService) apply(cfg Settings, onlyIfNewer bool) bool {
	tokens := map[string]string{}
	errs := map[string]string{}
	for _, inst := range cfg.Instances {
		if inst.TokenRef == "" {
			continue
		}
		v, err := secretref.ResolveWith(inst.TokenRef, s.getenv, s.readFile)
		if err != nil {
			errs[inst.ID] = err.Error()
			continue
		}
		tokens[inst.ID] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if onlyIfNewer && isStale(s.cfg, cfg) {
		return false // kalıcı blob canlı ayardan ESKİ: canlı ayar korunur
	}
	s.cfg, s.tokens, s.tokenErrs = cfg, tokens, errs
	return true
}

// Token — FAIL-CLOSED: çözülemeyen ref ya da bilinmeyen instance hata.
func (s *SettingsService) Token(instanceID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inst := range s.cfg.Instances {
		if inst.ID != instanceID {
			continue
		}
		if inst.TokenRef == "" {
			return "", ErrNoTokenRef
		}
		if v, ok := s.tokens[instanceID]; ok {
			return v, nil
		}
		return "", fmt.Errorf("argocd: instance %q tokenRef çözülemedi: %s", instanceID, s.tokenErrs[instanceID])
	}
	return "", fmt.Errorf("argocd: bilinmeyen instance %q", instanceID)
}

// TokenStatus — rozet: çözüldü mü, değilse neden (hata metni ref'i ve
// dosya yolunu taşır — gizli DEĞİL; çözülen değeri asla taşımaz).
func (s *SettingsService) TokenStatus(instanceID string) (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.tokens[instanceID]; ok {
		return true, ""
	}
	return false, s.tokenErrs[instanceID]
}

// isStale — SAF: kalıcı blob canlı ayardan ESKİ mi (eski pod'un / gecikmeli
// replikanın bayat blobu daha yeni admin PUT'unu ezmesin; rollouts
// applyLoaded emsali: eşit ya da yeni alınır).
func isStale(cur, loaded Settings) bool { return loaded.UpdatedAt < cur.UpdatedAt }

// LoadPersisted — boot + 30 s yenileme + peer sinyali. Anahtar yok →
// varsayılan korunur; bozuk JSON / okuma hatası → hata, canlı ayar
// EZİLMEZ. Her yüklemede tokenRef'ler yeniden çözülür (dosya rotasyonu).
func (s *SettingsService) LoadPersisted(ctx context.Context, store Store) error {
	if s == nil || store == nil {
		return nil
	}
	raw, err := store.GetSetting(ctx, SettingsKey)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var cfg Settings
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("argocd settings decode: %w", err)
	}
	if !s.apply(cfg, true) {
		// Canlı ayar daha yeni: yine de KENDİ token'ları tazelensin (file:
		// rotasyonu). Arada daha yeni bir PUT biterse bu da atlanır.
		s.apply(s.Current(), true)
	}
	return nil
}

// SavePersisted — damgala, yaz, bu pod'da canlı değiştir. Yazım başarısızsa
// canlı ayar değişmez. Store yoksa hata (panik değil).
func (s *SettingsService) SavePersisted(ctx context.Context, store Store, cfg Settings) error {
	if store == nil {
		return errors.New("argocd: ayar deposu bağlı değil")
	}
	cfg.UpdatedAt = time.Now().UnixNano()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := store.PutSetting(ctx, SettingsKey, raw); err != nil {
		return err
	}
	s.Configure(cfg)
	return nil
}
