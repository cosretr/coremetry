package v2probe

// tokenize.go — v0.10.979 — JETONLAYICI: sızıntı kapısı. SAF, tablo testli.
//
// Koşu başına BİR kez, Finalize'da, bütün ham sonuçlardan deterministik
// sırayla (birimler → katalog sırası → satırlar ham etiket demetine göre)
// kurulur: aynı ham küme iki koşuda aynı jetonları verir. Ham → jeton
// eşlemesi goroutine'i terk etmez; Count() yalnız boyutunu söyler.
//
// ── SINIFLAR (etiket adı → kural) ────────────────────────────────────────
//
//   küme uzayı  cluster, cluster_id, cluster_name, k8s_cluster,
//               openshift_cluster, tenant, tenant_id, CH cluster, T3 value →
//               tohum takma adı (cluster-a / hub-1) ya da sıradaki
//               cluster-c, cluster-d … (z'den sonra aa)
//   prometheus / prometheus_replica → <prometheus-N> / <replica-N>
//   dest_server → https://kubernetes.default.svc ve "" harfi harfine; host
//               APIHosts'ta → https://api.<clusterToken>.<domain>[:port];
//               bilinmeyen host → https://api.server-N.<domain>[:port];
//               şema ve port harfi harfine
//   namespace, exported_namespace, dest_namespace → <team-N>-<env> (değer
//               envList'ten bir env ile bitiyorsa; env operatörün kendi
//               sözlüğü), aksi hâlde <ns-N>. Aynı takım env'ler arasında
//               aynı N alır (N7 / H1.2 biçimi görünür kalır).
//   job         <team-N>-<env>-metrics (ns kuralı + "-metrics"), aksi <job-N>
//   name        app-N · project <project-N> · repo <repo-N> · base <base-N>
//               · pod <pod-N> · sfx <suffix-a>… (suffixList sırası) / <sfx-N>
//   deploy_env  <env>-<clusterToken> (değer <env>-<tohum ham küme> ise),
//               aksi <deploy-env-N>
//   LiteralLabels → değer aynen (enum / sayı / sürüm / metrik adı)
//   VARSAYILAN RED → <label-name-N>; rapor altbilgisi bu etiket adlarını
//               listeler (değer asla) ki allowlist genişletilebilsin.
//
// v0.10.979 — Value() SIRASI: satıra özel literalFor > sınıflar >
// LiteralLabels > varsayılan red. Sınıflar küresel allowlist'ten ÖNCE
// bakılır ki bir allowlist girdisi jetonlanan bir sınıfı asla gölgelemesin
// (olay: T1 sayaç adı `k8s_cluster` LiteralLabels'a girince hub H0.3/H0.5
// satırındaki `k8s_cluster` küme ADI ham basıldı). T paketi sayaçları
// (sampled, depl, k8s_cluster …) bu yüzden küresel değil, T1 satırının
// LiteralFor'unda yaşar; TestLiteralLabelsDisjointFromClasses çivisi.
//
// ScrubText — serbest metin (Detail, Warnings, Infos): tohum takma adları ve
// öğrenilmiş ham değerler (en uzun önce) + her https?://host[:port] →
// https://<host>.

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Alias — tohum: bir birimin jetonu ve o birime ait bütün ham yazımlar
// (EffectiveID, Name, ThanosLabelValue, SpanClusterValues…).
type Alias struct {
	Token string
	Raw   []string
}

// Seeds — api katmanının verdiği tohumlar.
type Seeds struct {
	Clusters []Alias
	// APIHosts — küçük harf host[:port] ve host → küme jetonu.
	APIHosts   map[string]string
	EnvList    []string
	SuffixList []string
}

// LiteralLabels — değeri aynen basılan etiketler.
var LiteralLabels = map[string]bool{
	"__name__": true, "version": true, "condition": true, "status": true, "reason": true,
	"owner_kind": true, "owner_is_controller": true, "scaletargetref_kind": true, "phase": true,
	"strategy": true, "traffic_router": true, "sync_status": true, "health_status": true,
	"operation": true, "autosync_enabled": true, "dry_run": true, "port": true, "env": true,
	"apps_per_target_ns": true, "same_name_same_server": true, "clusters_per_base": true,
	// T paketi sayaçları burada DEĞİL: T1 satırının LiteralFor'unda
	// (catalog.go); `k8s_cluster` hub'da küme adıdır (v0.10.979).
}

var clusterLabelSet = map[string]bool{
	"cluster": true, "cluster_id": true, "cluster_name": true, "k8s_cluster": true,
	"openshift_cluster": true, "tenant": true, "tenant_id": true, "unit": true, "span_cluster": true, "owner": true,
}

var nsLabelSet = map[string]bool{"namespace": true, "exported_namespace": true, "dest_namespace": true}

// Tokenizer — koşu başına eşleme.
type Tokenizer struct {
	seeds    Seeds
	envs     []string          // küçük harf, uzun önce
	suffixes map[string]int    // küçük harf → indeks
	cluster  map[string]string // ham → jeton (tohumlar dahil)
	clusterN int               // sıradaki tohumsuz küme harfi
	classes  map[string]map[string]string
	counters map[string]int
	teams    map[string]int
	denied   map[string]bool
	learned  []string // ScrubText için öğrenilmiş ham değerler
}

// NewTokenizer — tohumlardan.
func NewTokenizer(seeds Seeds) *Tokenizer {
	t := &Tokenizer{seeds: seeds, cluster: map[string]string{}, classes: map[string]map[string]string{},
		counters: map[string]int{}, teams: map[string]int{}, denied: map[string]bool{}, suffixes: map[string]int{}}
	for _, a := range seeds.Clusters {
		for _, r := range a.Raw {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if _, dup := t.cluster[r]; !dup {
				t.cluster[r] = a.Token
			}
		}
		if strings.HasPrefix(a.Token, "cluster-") {
			t.clusterN++
		}
	}
	for _, e := range seeds.EnvList {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			t.envs = append(t.envs, e)
		}
	}
	sort.Slice(t.envs, func(i, j int) bool { return len(t.envs[i]) > len(t.envs[j]) })
	for i, s := range seeds.SuffixList {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			if _, dup := t.suffixes[s]; !dup {
				t.suffixes[s] = i
			}
		}
	}
	return t
}

// Count — eşleme boyutu (tohumlar dahil ham değer sayısı).
func (t *Tokenizer) Count() int {
	n := len(t.cluster)
	for _, m := range t.classes {
		n += len(m)
	}
	return n
}

// Denied — varsayılan-reddedilen etiket adları (sıralı).
func (t *Tokenizer) Denied() []string {
	out := make([]string, 0, len(t.denied))
	for k := range t.denied {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func letters(i int) string {
	s := ""
	for {
		s = string(rune('a'+i%26)) + s
		i = i/26 - 1
		if i < 0 {
			return s
		}
	}
}

func (t *Tokenizer) learn(raw string) {
	if len(raw) >= 3 {
		t.learned = append(t.learned, raw)
	}
}

// numbered — sınıf başına ilk görülme numaralı jeton.
func (t *Tokenizer) numbered(class, raw, format string) string {
	m := t.classes[class]
	if m == nil {
		m = map[string]string{}
		t.classes[class] = m
	}
	if tok, ok := m[raw]; ok {
		return tok
	}
	t.counters[class]++
	tok := strings.ReplaceAll(format, "N", strconv.Itoa(t.counters[class]))
	m[raw] = tok
	t.learn(raw)
	return tok
}

// ClusterToken — küme uzayı: tohum ya da sıradaki harf.
func (t *Tokenizer) ClusterToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if tok, ok := t.cluster[raw]; ok {
		return tok
	}
	tok := "cluster-" + letters(t.clusterN)
	t.clusterN++
	t.cluster[raw] = tok
	t.learn(raw)
	return tok
}

// splitEnv — değer envList'ten bir env ile bitiyorsa (takım, env, true).
func (t *Tokenizer) splitEnv(v string) (string, string, bool) {
	lv := strings.ToLower(v)
	for _, e := range t.envs {
		if strings.HasSuffix(lv, "-"+e) && len(lv) > len(e)+1 {
			return v[:len(v)-len(e)-1], e, true
		}
	}
	return "", "", false
}

func (t *Tokenizer) nsToken(raw string) string {
	if raw == "" {
		return ""
	}
	m := t.classes["namespace"]
	if m == nil {
		m = map[string]string{}
		t.classes["namespace"] = m
	}
	if tok, ok := m[raw]; ok {
		return tok
	}
	var tok string
	if team, env, ok := t.splitEnv(raw); ok {
		key := strings.ToLower(team)
		n, seen := t.teams[key]
		if !seen {
			t.counters["team"]++
			n = t.counters["team"]
			t.teams[key] = n
		}
		tok = "<team-" + strconv.Itoa(n) + ">-" + env
	} else {
		t.counters["ns"]++
		tok = "<ns-" + strconv.Itoa(t.counters["ns"]) + ">"
	}
	m[raw] = tok
	t.learn(raw)
	return tok
}

func (t *Tokenizer) jobToken(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasSuffix(raw, "-metrics") {
		if _, _, ok := t.splitEnv(strings.TrimSuffix(raw, "-metrics")); ok {
			m := t.classes["job"]
			if m == nil {
				m = map[string]string{}
				t.classes["job"] = m
			}
			if tok, ok := m[raw]; ok {
				return tok
			}
			tok := t.nsToken(strings.TrimSuffix(raw, "-metrics")) + "-metrics"
			m[raw] = tok
			t.learn(raw)
			return tok
		}
	}
	return t.numbered("job", raw, "<job-N>")
}

// destServerToken — URL biçimi korunur, host jetona iner.
func (t *Tokenizer) destServerToken(raw string) string {
	switch raw {
	case "", "https://kubernetes.default.svc":
		return raw
	}
	m := t.classes["dest_server"]
	if m == nil {
		m = map[string]string{}
		t.classes["dest_server"] = m
	}
	if tok, ok := m[raw]; ok {
		return tok
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	var tok string
	if err != nil || u.Host == "" {
		t.counters["dest_server"]++
		tok = "<dest-server-" + strconv.Itoa(t.counters["dest_server"]) + ">"
	} else {
		host := strings.ToLower(u.Host)
		port := u.Port()
		ct, ok := t.seeds.APIHosts[host]
		if !ok {
			ct, ok = t.seeds.APIHosts[strings.ToLower(u.Hostname())]
		}
		var name string
		if ok {
			name = ct
		} else {
			name = t.numbered("api_server", strings.ToLower(u.Hostname()), "server-N")
		}
		scheme := u.Scheme
		if scheme == "" {
			scheme = "https"
		}
		tok = scheme + "://api." + name + ".<domain>"
		if port != "" {
			tok += ":" + port
		}
	}
	m[raw] = tok
	t.learn(raw)
	if u != nil && u.Hostname() != "" {
		t.learn(u.Hostname())
	}
	return tok
}

func (t *Tokenizer) deployEnvToken(raw string) string {
	if raw == "" {
		return ""
	}
	m := t.classes["deploy_env"]
	if m == nil {
		m = map[string]string{}
		t.classes["deploy_env"] = m
	}
	if tok, ok := m[raw]; ok {
		return tok
	}
	var tok string
	lv := strings.ToLower(raw)
	for _, e := range t.envs {
		if strings.HasPrefix(lv, e+"-") {
			rest := raw[len(e)+1:]
			if ct, ok := t.cluster[rest]; ok {
				tok = e + "-" + ct
			}
			break
		}
	}
	if tok == "" {
		t.counters["deploy_env"]++
		tok = "<deploy-env-" + strconv.Itoa(t.counters["deploy_env"]) + ">"
	}
	m[raw] = tok
	t.learn(raw)
	return tok
}

func (t *Tokenizer) sfxToken(raw string) string {
	if raw == "" {
		return ""
	}
	if i, ok := t.suffixes[strings.ToLower(raw)]; ok {
		m := t.classes["sfx"]
		if m == nil {
			m = map[string]string{}
			t.classes["sfx"] = m
		}
		tok := "<suffix-" + letters(i) + ">"
		m[raw] = tok
		t.learn(raw)
		return tok
	}
	return t.numbered("sfx", raw, "<sfx-N>")
}

// Value — etiket adına göre sınıf gönderimi. literalFor: bu satıra özel
// harfi harfine etiketler. Sıra: literalFor > sınıflar > LiteralLabels >
// varsayılan red (v0.10.979 — küresel allowlist bir sınıfı gölgeleyemez).
func (t *Tokenizer) Value(label, raw string, literalFor ...string) string {
	for _, l := range literalFor {
		if l == label {
			return raw
		}
	}
	switch {
	case clusterLabelSet[label]:
		return t.ClusterToken(raw)
	case label == "prometheus":
		if raw == "" {
			return ""
		}
		return t.numbered("prometheus", raw, "<prometheus-N>")
	case label == "prometheus_replica":
		if raw == "" {
			return ""
		}
		return t.numbered("prometheus_replica", raw, "<replica-N>")
	case label == "dest_server":
		return t.destServerToken(raw)
	case nsLabelSet[label]:
		return t.nsToken(raw)
	case label == "job":
		return t.jobToken(raw)
	case label == "name":
		if raw == "" {
			return ""
		}
		return t.numbered("name", raw, "app-N")
	case label == "project":
		if raw == "" {
			return ""
		}
		return t.numbered("project", raw, "<project-N>")
	case label == "repo":
		if raw == "" {
			return ""
		}
		return t.numbered("repo", raw, "<repo-N>")
	case label == "sfx":
		return t.sfxToken(raw)
	case label == "base":
		if raw == "" {
			return ""
		}
		return t.numbered("base", raw, "<base-N>")
	case label == "pod":
		if raw == "" {
			return ""
		}
		return t.numbered("pod", raw, "<pod-N>")
	case label == "deploy_env":
		return t.deployEnvToken(raw)
	case LiteralLabels[label]:
		return raw
	}
	// Varsayılan red.
	t.denied[label] = true
	if raw == "" {
		return ""
	}
	return t.numbered("deny:"+label, raw, "<"+label+"-N>")
}

// Row — bir satırın bütün etiketlerini jetonlar (anahtar sırası bağımsız:
// çağıran satırları önce sıralar).
func (t *Tokenizer) Row(labels map[string]string, literalFor []string) map[string]string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(labels))
	for _, k := range keys {
		out[k] = t.Value(k, labels[k], literalFor...)
	}
	return out
}

// ScrubText — serbest metindeki ham adlar ve URL host'ları.
func (t *Tokenizer) ScrubText(s string) string {
	if s == "" {
		return s
	}
	type pair struct{ raw, tok string }
	var pairs []pair
	for raw, tok := range t.cluster {
		pairs = append(pairs, pair{raw, tok})
	}
	for _, raw := range t.learned {
		pairs = append(pairs, pair{raw, "<value>"})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if len(pairs[i].raw) != len(pairs[j].raw) {
			return len(pairs[i].raw) > len(pairs[j].raw)
		}
		return pairs[i].raw < pairs[j].raw
	})
	for _, p := range pairs {
		if p.raw != "" {
			s = strings.ReplaceAll(s, p.raw, p.tok)
		}
	}
	return scrubURLs(s)
}

// scrubURLs — https?://host[:port] → https://<host> (yol korunmaz).
func scrubURLs(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "://")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		start := i
		for start > 0 && isSchemeByte(s[start-1]) {
			start--
		}
		scheme := strings.ToLower(s[start:i])
		if scheme != "http" && scheme != "https" {
			b.WriteString(s[:i+3])
			s = s[i+3:]
			continue
		}
		end := i + 3
		for end < len(s) && !strings.ContainsRune(" \t\r\n\"'<>)],;/", rune(s[end])) {
			end++
		}
		b.WriteString(s[:start])
		b.WriteString("https://<host>")
		s = s[end:]
	}
}

func isSchemeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
