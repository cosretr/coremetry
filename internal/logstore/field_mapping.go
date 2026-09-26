package logstore

// field_mapping.go — v0.10.944 (CoSRE araştırma asistanı, Faz A:
// çapraz-kaynak eşleme). Log backend'inde her ROLÜN (service, env,
// cluster, namespace, pod, version, trace_id, span_id, timestamp) hangi
// belge alanına oturduğunu ve bu kararın NEREDEN geldiğini söyler.
//
// Neden ayrı bir sözleşme: search_logs'un cevabı "cluster=a için log yok"
// dediğinde model iki şeyi ayırt edemiyordu — cluster alanı var ama
// eşleşme yok mu, yoksa bu indekste cluster alanı HİÇ yok mu? İkincisinde
// boş sonuç hiçbir şey kanıtlamaz. Karar üç kaynaktan biri:
//
//	configured — operatör alanı yazdı (Settings → Elasticsearch ya da
//	             COREMETRY_ES_FIELD_*); keşifsiz, AYNEN güvenilir
//	             (fields.Env sözleşmesi).
//	discovered — yapılandırma yok; aday listesinden mapping'de GERÇEKTEN
//	             var olan(lar) (tek field_caps, TTL'li — env deseni).
//	none       — prob başarılı, adayların hiçbiri mapping'de yok.
//
// İki yardımcı durum: unverified — prob DÜŞTÜ ya da istenmedi; yokluk
// İDDİA EDİLMEZ (es_trace_presence.go ilkesi: yanlış "çalışamaz" rozeti
// yanlış sessiz boştan daha az zararlı değil). schema — ClickHouse
// backend'inin sabit kolon/res-array ifadesi (operatör yapılandırması
// değil; "configured" demek yalan olurdu).
//
// Maliyet (ES-maliyet disiplini): rol keşfi TEK field_caps ile yapılır
// (tüm roller × adaylar × .keyword), karar olumlu ya da olumsuz
// fieldRoleTTL boyunca önbellekte. /logs arama yolu buna HİÇ dokunmaz;
// yalnız CoSRE tool'ları FieldMapping çağırır.

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rol adları — tool çıktısındaki `mapping` anahtarlarıyla birebir.
const (
	RoleService   = "service"
	RoleEnv       = "env"
	RoleCluster   = "cluster"
	RoleNamespace = "namespace"
	RolePod       = "pod"
	RoleVersion   = "version"
	RoleTraceID   = "trace_id"
	RoleSpanID    = "span_id"
	RoleTimestamp = "timestamp"
)

// FilterSeverity — Page.UnappliedFilters'ta seviye süzgecinin adı (rol
// değil: eşlemede alanı raporlanmaz, yalnız uygulanmadıysa itiraf edilir).
const FilterSeverity = "severity"

// FieldRoles — raporlanan rollerin kanonik sırası.
var FieldRoles = []string{
	RoleService, RoleEnv, RoleCluster, RoleNamespace, RolePod,
	RoleVersion, RoleTraceID, RoleSpanID, RoleTimestamp,
}

// Karar kaynakları.
const (
	FieldConfigured = "configured"
	FieldDiscovered = "discovered"
	FieldNone       = "none"
	FieldUnverified = "unverified"
	FieldSchema     = "schema"
)

// FieldResolution — bir rolün çözümü. Fields yalnız birden çok alan
// (ES keşfinde birden çok aday mevcut; CH res-array coalesce zinciri)
// rolü taşıdığında dolar; Field her zaman birincisidir.
type FieldResolution struct {
	Field  string   `json:"field,omitempty"`
	Fields []string `json:"fields,omitempty"`
	Source string   `json:"source"`
}

// Real — rol GERÇEK bir alana oturuyor mu (configured / discovered /
// schema). unverified ve none "gerçek alan" iddiası taşımaz — search_logs
// match=trace_id kararı buna bakar.
func (r FieldResolution) Real() bool {
	switch r.Source {
	case FieldConfigured, FieldDiscovered, FieldSchema:
		return r.Field != ""
	}
	return false
}

// Label — modele giden tek satır: "<alan> (<kaynak>)" ya da yalnız kaynak.
func (r FieldResolution) Label() string {
	switch {
	case len(r.Fields) > 1:
		return strings.Join(r.Fields, "|") + " (" + r.Source + ")"
	case r.Field != "":
		return r.Field + " (" + r.Source + ")"
	case r.Source == "":
		return FieldNone
	}
	return r.Source
}

// Has — ad bu rolü taşıyan alanlardan biri mi (list_log_fields rol notu).
// `.keyword` alt-alanı çıplak alanın rolünü paylaşır.
func (r FieldResolution) Has(name string) bool {
	base := strings.TrimSuffix(name, ".keyword")
	if r.Field != "" && (r.Field == name || r.Field == base) {
		return true
	}
	for _, f := range r.Fields {
		if f == name || f == base {
			return true
		}
	}
	return false
}

// FieldMapping — bir backend'in rol → alan çözümleri.
type FieldMapping struct {
	Backend string                     `json:"backend"`
	Roles   map[string]FieldResolution `json:"roles"`
}

// Role — rolün çözümü; bilinmeyen rol "none".
func (m FieldMapping) Role(role string) FieldResolution {
	if r, ok := m.Roles[role]; ok {
		return r
	}
	return FieldResolution{Source: FieldNone}
}

// Labels — rol → "<alan> (<kaynak>)" (tool çıktısının `mapping` bloğu).
func (m FieldMapping) Labels() map[string]string {
	out := make(map[string]string, len(FieldRoles))
	for _, role := range FieldRoles {
		out[role] = m.Role(role).Label()
	}
	return out
}

// FieldMapper — isteğe bağlı backend yeteneği (Unwrap üzerinden type-assert;
// Switchable bilerek iletmez). probe=false ağa ÇIKMAZ: taze önbellek yoksa
// yapılandırılmamış roller "unverified" döner (hata yolu, iptal sonrası).
type FieldMapper interface {
	FieldMapping(ctx context.Context, probe bool) FieldMapping
}

// UnresolvedFilterRoles — SAF: istenen filtre rollerinden, eşlemede alanı
// OLMAYANLAR (Source none). ES bu filtreleri aday listesiyle yine gönderir
// (mevcut /logs davranışı) ama hiçbir aday mapping'de yok — sonuç
// kaçınılmaz boştur ve "log yok" demek değildir; çağıran bunu kısmi not
// olarak söyler. env burada YOK: Page.EnvUnapplied tek otorite.
func UnresolvedFilterRoles(f Filter, m FieldMapping) []string {
	var out []string
	for _, p := range []struct{ role, val string }{
		{RoleService, f.Service}, {RoleCluster, f.Cluster},
		{RoleNamespace, f.Namespace}, {RolePod, f.Pod},
	} {
		if p.val != "" && m.Role(p.role).Source == FieldNone {
			out = append(out, p.role)
		}
	}
	return out
}

// RoleValue — bir kayıtta rolün değeri: önce eşlemenin alan(lar)ı, sonra
// rolün bilinen okuma anahtarları (ES belge yolları + CH res anahtarları);
// her anahtar önce ResourceAttributes, sonra Attributes'ta aranır.
// service/trace_id/span_id kaydın kanonik alanından okunur.
func RoleValue(rec *LogRecord, role string, m FieldMapping) string {
	if rec == nil {
		return ""
	}
	switch role {
	case RoleService:
		return rec.ServiceName
	case RoleTraceID:
		return rec.TraceID
	case RoleSpanID:
		return rec.SpanID
	}
	r := m.Role(role)
	keys := make([]string, 0, 2+len(r.Fields)+12)
	keys = append(keys, r.Field)
	keys = append(keys, r.Fields...)
	keys = append(keys, roleReadKeys(role)...)
	for _, k := range keys {
		if k == "" {
			continue
		}
		if v := rec.ResourceAttributes[k]; v != "" {
			return v
		}
		if v := rec.Attributes[k]; v != "" {
			return v
		}
	}
	return ""
}

// RoleKeys — v0.10.944: rolün değerini taşıyabilen TÜM anahtarlar (eşlemenin
// alan(lar)ı + RoleValue'nun okuma anahtarları), boşlar hariç. search_logs
// satırının `attrs` haritası rol olarak zaten yüzeye çıkan anahtarları
// tekrar etmesin diye (mcptools logAttrs). Liste RoleValue ile AYNI kaynaktan.
func RoleKeys(role string, m FieldMapping) []string {
	r := m.Role(role)
	cand := append(append([]string{r.Field}, r.Fields...), roleReadKeys(role)...)
	out := make([]string, 0, len(cand))
	for _, k := range cand {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// roleReadKeys — rolün okuma anahtarları: ES aday yolları + CH res-array
// anahtarları (aynı kavram, iki backend'in yazımı).
func roleReadKeys(role string) []string {
	switch role {
	case RoleEnv:
		return append(envFieldCandidates(), chEnvKeys...)
	case RoleCluster:
		return append(append([]string{}, esClusterFields...), chClusterKeys...)
	case RoleNamespace:
		return append(append([]string{}, esNamespaceFields...), chNamespaceKeys...)
	case RolePod:
		return append(append([]string{}, esPodFields...), chPodKeys...)
	case RoleVersion:
		return append(append([]string{}, esVersionFields...), chVersionKeys...)
	}
	return nil
}

// ─── Elasticsearch ────────────────────────────────────────────────────

// esVersionFields — service.version'ın ES belge yolu adayları (OTel ES
// exporter ECS/ham kip, düzleştirilmiş, OpenShift label normalizasyonu).
// Yalnız okuma/raporlama: search_logs version ile SÜZMEZ.
var esVersionFields = []string{
	"service.version",
	"resource.service.version",
	"resource.attributes.service.version",
	"resource_attributes.service.version",
	"kubernetes.labels.version",
	"kubernetes.labels.app_kubernetes_io_version",
}

// esServiceFallbackFields — buildQuery'deki svcFields'ın yapılandırılmış
// alandan SONRAKİ adayları (kaynak-pin testi iki listeyi eşit tutar).
var esServiceFallbackFields = []string{
	"kubernetes.container.name", "kubernetes.container_name",
	"kubernetes.labels.app", "kubernetes.labels_app",
}

// withESFieldEnv — v0.10.944: yeni rol alanlarının env tohumu. Yalnız BOŞ
// üyeyi doldurur ve yalnız BOOT yapılandırmasına uygulanır (NewESManager;
// blob'suz doğrudan çağıran için NewES — main.go boot kurulumu). Sonrasında
// blob/PUT değeri — açık "" (keşfe dön) DAHİL — kazanır: ESManager.build
// fieldsAuthoritative ile env'i yeniden uygulatmaz (es_config_persist.go
// "UI kazanır" sözleşmesi; COREMETRY_ES_FIELD_ENV ile aynı). internal/config
// bu dört anahtarı taşımadığı için okuma burada (esTimeoutFromEnv emsali).
func withESFieldEnv(m ESFieldMap) ESFieldMap {
	fill := func(dst *string, key string) {
		if *dst == "" {
			*dst = strings.TrimSpace(os.Getenv(key))
		}
	}
	fill(&m.Cluster, "COREMETRY_ES_FIELD_CLUSTER")
	fill(&m.Namespace, "COREMETRY_ES_FIELD_NAMESPACE")
	fill(&m.Pod, "COREMETRY_ES_FIELD_POD")
	fill(&m.Version, "COREMETRY_ES_FIELD_VERSION")
	return m
}

// fieldOrDiscover — boot log etiketi.
func fieldOrDiscover(f string) string {
	if f == "" {
		return "(self-discover)"
	}
	return strconv.Quote(f)
}

// roleFilterFields — filtrenin hedef alanları: yapılandırılmış alan varsa
// YALNIZ o (keşif/aday yok), yoksa aday listesi.
func roleFilterFields(configured string, candidates []string) []string {
	if configured != "" {
		return []string{configured}
	}
	return candidates
}

// roleNeedsKeyword — rolün "var" sayılması için keyword-yetenekli alan
// şartı. Süzgeç rolleri exact-term koşar (analyzed alan tireli değeri
// bulamaz — env kuralı); kimlik/sürüm/zaman yalnız varlık ister.
func roleNeedsKeyword(role string) bool {
	switch role {
	case RoleService, RoleEnv, RoleCluster, RoleNamespace, RolePod:
		return true
	}
	return false
}

// esRoleConfigured — defaults() ÖNCESİ haritadan rolün yapılandırılmış alanı.
func esRoleConfigured(role string, raw ESFieldMap) string {
	switch role {
	case RoleService:
		return raw.Service
	case RoleEnv:
		return raw.Env
	case RoleCluster:
		return raw.Cluster
	case RoleNamespace:
		return raw.Namespace
	case RolePod:
		return raw.Pod
	case RoleVersion:
		return raw.Version
	case RoleTraceID:
		return raw.TraceID
	case RoleSpanID:
		return raw.SpanID
	case RoleTimestamp:
		return raw.Timestamp
	}
	return ""
}

// esRoleCandidates — rolün keşif adayları (öncelik sırasıyla). eff =
// defaults() SONRASI harita (service.name / @timestamp varsayılanları).
func esRoleCandidates(role string, eff ESFieldMap) []string {
	switch role {
	case RoleService:
		svc := eff.Service
		if svc == "" {
			svc = "service.name"
		}
		return append([]string{svc}, esServiceFallbackFields...)
	case RoleEnv:
		return envFieldCandidates()
	case RoleCluster:
		return esClusterFields
	case RoleNamespace:
		return esNamespaceFields
	case RolePod:
		return esPodFields
	case RoleVersion:
		return esVersionFields
	case RoleTraceID:
		return traceFieldCandidates("")
	case RoleSpanID:
		return []string{"span.id", "span_id", "spanId", "SpanId"}
	case RoleTimestamp:
		ts := eff.Timestamp
		if ts == "" {
			ts = "@timestamp"
		}
		return []string{ts}
	}
	return nil
}

// esRoleProbeFields — tek field_caps'in alan listesi: tüm rollerin
// adayları + .keyword varyantları, tekilleştirilmiş.
func esRoleProbeFields(eff ESFieldMap) []string {
	seen := map[string]bool{}
	var out []string
	for _, role := range FieldRoles {
		for _, c := range esRoleCandidates(role, eff) {
			for _, f := range []string{c, c + ".keyword"} {
				if c == "" || seen[f] {
					continue
				}
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// capKeywordCapable — alan keyword tipli VE aggregatable mı (env kuralı).
func capKeywordCapable(c traceFieldCap) bool {
	for _, t := range c.Types {
		if t == "keyword" {
			return c.Aggregatable
		}
	}
	return false
}

// capPresent — aday mapping'de rolü taşıyabilecek biçimde var mı.
func capPresent(caps map[string]traceFieldCap, name string, needKeyword bool) bool {
	if needKeyword {
		return capKeywordCapable(caps[name]) || capKeywordCapable(caps[name+".keyword"])
	}
	return len(caps[name].Types) > 0 || len(caps[name+".keyword"].Types) > 0
}

// resolveFieldRole — KARAR kuralı (SAF, tablo testli): configured >
// discovered > none. Prob yapılmadı/düştüyse (probed=false) yapılandırılmamış
// rol "unverified" — yokluk iddia edilmez.
func resolveFieldRole(configured string, candidates []string, caps map[string]traceFieldCap, probed, needKeyword bool) FieldResolution {
	if configured = strings.TrimSpace(configured); configured != "" {
		return FieldResolution{Field: configured, Source: FieldConfigured}
	}
	if !probed {
		return FieldResolution{Source: FieldUnverified}
	}
	var found []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if capPresent(caps, c, needKeyword) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return FieldResolution{Source: FieldNone}
	case 1:
		return FieldResolution{Field: found[0], Source: FieldDiscovered}
	}
	return FieldResolution{Field: found[0], Fields: found, Source: FieldDiscovered}
}

// buildESFieldMapping — SAF: ham + etkin harita ve field_caps kararından
// tüm rollerin çözümü.
func buildESFieldMapping(raw, eff ESFieldMap, caps map[string]traceFieldCap, probed bool) FieldMapping {
	m := FieldMapping{Backend: "elasticsearch", Roles: make(map[string]FieldResolution, len(FieldRoles))}
	for _, role := range FieldRoles {
		m.Roles[role] = resolveFieldRole(esRoleConfigured(role, raw), esRoleCandidates(role, eff),
			caps, probed, roleNeedsKeyword(role))
	}
	return m
}

// fieldRoleTTL — rol kararının ömrü (envFieldTTL gerekçesi: mapping
// rollover/pipeline deploy'la değişir, istekle değil).
const fieldRoleTTL = 10 * time.Minute

// esRoleFieldCache — rol keşfi önbelleği; olumlu VE olumsuz karar aynı TTL.
type esRoleFieldCache struct {
	mu      sync.Mutex
	caps    map[string]traceFieldCap
	probed  bool
	expires time.Time
}

// FieldMapping — FieldMapper. probe=true en fazla bir field_caps
// (envFieldCapsTimeout) koşar; sonuç fieldRoleTTL önbellekte.
func (s *ESStore) FieldMapping(ctx context.Context, probe bool) FieldMapping {
	caps, probed := s.roleCaps(ctx, probe)
	return buildESFieldMapping(s.rawFields, s.fields, caps, probed)
}

func (s *ESStore) roleCaps(ctx context.Context, probe bool) (map[string]traceFieldCap, bool) {
	now := time.Now()
	s.roleFields.mu.Lock()
	if now.Before(s.roleFields.expires) {
		caps, probed := s.roleFields.caps, s.roleFields.probed
		s.roleFields.mu.Unlock()
		return caps, probed
	}
	s.roleFields.mu.Unlock()
	if !probe {
		return nil, false
	}
	fcCtx, cancel := context.WithTimeout(ctx, envFieldCapsTimeout)
	defer cancel()
	// Pencere-daraltılmış somut indeksler — karar, sorguların GERÇEKTEN
	// vurduğu indeksleri tarif etmeli (env keşfiyle aynı).
	idx := s.queryIndices(fcCtx, Filter{From: now.Add(-24 * time.Hour), To: now})
	caps, err := s.fieldCaps(fcCtx, idx, esRoleProbeFields(s.fields))
	probed := err == nil
	if err != nil {
		caps = nil
		log.Printf("[logstore-es] rol alanı keşfi düştü (yapılandırılmamış roller %s boyunca 'unverified'): %v", fieldRoleTTL, err)
	}
	// Çağıranın kendi bütçesi/iptali probu düşürdüyse karar önbelleğe
	// YAZILMAZ: sağlıklı küme bir TTL boyunca 'unverified' kalmasın.
	if ctx.Err() != nil {
		return caps, probed
	}
	s.roleFields.mu.Lock()
	s.roleFields.caps, s.roleFields.probed, s.roleFields.expires = caps, probed, now.Add(fieldRoleTTL)
	s.roleFields.mu.Unlock()
	return caps, probed
}

// ─── ClickHouse ───────────────────────────────────────────────────────

// CH logs tablosunun res-array anahtar zincirleri — clickhouse.go'daki
// chLogs*Expr ifadelerinin AYNASI (field_mapping_test.go her anahtarın
// ifadede geçtiğini pinler). service/trace_id/span_id/time sabit kolon.
var (
	chEnvKeys       = []string{"deployment.environment.name", "deployment.environment"}
	chClusterKeys   = []string{"k8s.cluster.name", "openshift.cluster.name", "cluster"}
	chNamespaceKeys = []string{"k8s.namespace.name", "kubernetes.namespace_name", "kubernetes.namespace", "namespace"}
	chPodKeys       = []string{"k8s.pod.name", "kubernetes.pod_name", "kubernetes.pod.name", "pod_name"}
	chVersionKeys   = []string{"service.version"}
)

// chFieldMapping — SAF: CH şeması sabit, keşif yok; her rol "schema".
// service için /logs arama dilindeki kanonik ad (service.name → service_name
// kolonu) raporlanır — modelin query'ye yazabileceği ad.
func chFieldMapping() FieldMapping {
	multi := func(keys []string) FieldResolution {
		r := FieldResolution{Field: keys[0], Source: FieldSchema}
		if len(keys) > 1 {
			r.Fields = append([]string{}, keys...)
		}
		return r
	}
	return FieldMapping{Backend: "clickhouse", Roles: map[string]FieldResolution{
		RoleService:   {Field: "service.name", Source: FieldSchema},
		RoleEnv:       multi(chEnvKeys),
		RoleCluster:   multi(chClusterKeys),
		RoleNamespace: multi(chNamespaceKeys),
		RolePod:       multi(chPodKeys),
		RoleVersion:   multi(chVersionKeys),
		RoleTraceID:   {Field: "trace_id", Source: FieldSchema},
		RoleSpanID:    {Field: "span_id", Source: FieldSchema},
		RoleTimestamp: {Field: "time", Source: FieldSchema},
	}}
}

// FieldMapping — FieldMapper (CH: ağ yok, probe yok sayılır).
func (s *CHStore) FieldMapping(context.Context, bool) FieldMapping { return chFieldMapping() }
