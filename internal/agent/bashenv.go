package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// v0.10.966 — runbook bash adımının ortamı. exec.Cmd.Env nil iken alt süreç
// os.Environ()'ı, yani pod'un TÜM sırlarını (COREMETRY_JWT_SECRET, CH
// parolası, ES/AI anahtarları, extraEnv/envFrom tokenRef hedefleri) miras
// alırdı; adım çıktısı (16 KiB) saklanır ve izleyici okuyabilir. Artık yalnız
// sabit bir TABAN küme + operatörün COREMETRY_AGENT_ENV_PASSTHROUGH ile açıkça
// verdiği adlar geçer. COREMETRY_* hiçbir yoldan geçmez. Emsal:
// internal/mcpclient/transport_stdio.go stdioEnv (v0.10.803).

// v0.10.966 — bash adımına geçen TABAN adlar (değer taşımaz / sır değil).
var bashBaseEnv = []string{"HOME", "HOSTNAME", "KUBECONFIG", "KUBERNETES_SERVICE_HOST",
	"KUBERNETES_SERVICE_PORT", "LANG", "LC_ALL", "NO_PROXY", "PATH", "SSL_CERT_DIR",
	"SSL_CERT_FILE", "TMPDIR", "TZ", "USER", "no_proxy"}

// Proxy adları: değer '@' İÇERMİYORSA geçer (user:pass@ taşıyan proxy düşer).
var bashProxyEnv = []string{"ALL_PROXY", "HTTPS_PROXY", "HTTP_PROXY", "all_proxy", "http_proxy", "https_proxy"}

// bashDefaultPath — ebeveynde PATH yok ya da boşsa adımın göreceği PATH.
const bashDefaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// bashCredWords — ÖNEK eşleşmesinde adı kimlik-benzeri sayan parçalar.
var bashCredWords = []string{"PASS", "SECRET", "TOKEN", "KEY", "CRED", "AUTH", "PRIVATE", "SESSION", "COOKIE"}

var bashEnvNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`) // secretref.envRefRe gövdesiyle aynı

// bashEnvDenied — SERT red: büyük harfe çevrilmiş ad COREMETRY_ ile
// başlıyorsa taban listesi, proxy, tam ad ya da önek ne derse desin geçmez
// (JWT secret, CH parolası, chart'ın önerdiği COREMETRY_SECRET_* tokenRef
// öneki, karışık harfli yazımlar ve knob'un kendisi).
func bashEnvDenied(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), "COREMETRY_")
}

// looksLikeCredential — YALNIZ önek eşleşmelerinde kullanılır: ad kimlik
// parçası taşıyorsa ya da değer '@' içeriyorsa (user:pass@host) önek onu
// süpürmez. Kimlik ancak tam adıyla listelenerek geçer.
func looksLikeCredential(name, value string) bool {
	u := strings.ToUpper(name)
	for _, w := range bashCredWords {
		if strings.Contains(u, w) {
			return true
		}
	}
	return strings.Contains(value, "@")
}

// bashEnvPolicy — operatör passthrough'u: tam adlar (harfe duyarlı) +
// sondaki '*' ile verilen önekler.
type bashEnvPolicy struct {
	exact  map[string]bool
	prefix []string
}

// parseBashPassthrough — COREMETRY_AGENT_ENV_PASSTHROUGH girdilerini
// doğrular. Reddedilen girdi boot loguna yazılır; geçersiz girdinin İÇERİĞİ
// asla yankılanmaz (`FOO=bar` bir değeri loglardı), yalnız sırası. accepted
// tekrarsız ve sıralıdır; kimlik-benzeri tam ad açıklamayla loglanır.
func parseBashPassthrough(entries []string) (p bashEnvPolicy, accepted, rejected []string) {
	p = bashEnvPolicy{exact: map[string]bool{}}
	prefixSeen := map[string]bool{}
	for i, raw := range entries {
		e := strings.TrimSpace(raw)
		if e == "" {
			continue
		}
		name, isPrefix := strings.CutSuffix(e, "*")
		switch {
		case isPrefix && name == "":
			// Çıplak '*' değer taşımaz; kısa önek gerekçesiyle gösterilir.
			rejected = append(rejected, fmt.Sprintf("#%d %q (prefix must be >=3 chars)", i+1, e))
		case !bashEnvNameRe.MatchString(name):
			rejected = append(rejected, fmt.Sprintf("#%d (invalid entry, not shown)", i+1))
		case bashEnvDenied(name):
			rejected = append(rejected, fmt.Sprintf("#%d %q (COREMETRY_ prefix is never passed)", i+1, e))
		case isPrefix && len(name) < 3:
			rejected = append(rejected, fmt.Sprintf("#%d %q (prefix must be >=3 chars)", i+1, e))
		case isPrefix:
			if !prefixSeen[name] {
				prefixSeen[name] = true
				p.prefix = append(p.prefix, name)
			}
		default:
			p.exact[name] = true
		}
	}
	sort.Strings(p.prefix)
	keys := make([]string, 0, len(p.exact)+len(p.prefix))
	for k := range p.exact {
		keys = append(keys, k)
	}
	for _, k := range p.prefix {
		keys = append(keys, k+"*")
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasSuffix(k, "*") && looksLikeCredential(k, "") {
			accepted = append(accepted, fmt.Sprintf("%q (credential-like name, passed because listed exactly)", k))
			continue
		}
		accepted = append(accepted, k)
	}
	return p, accepted, rejected
}

// bashPolicy — boot'ta ConfigureBashEnv ile bir kez yazılır, her adımda
// okunur. nil = passthrough yok (yalnız taban küme).
var bashPolicy atomic.Pointer[bashEnvPolicy]

func currentBashPolicy() bashEnvPolicy {
	if p := bashPolicy.Load(); p != nil {
		return *p
	}
	return bashEnvPolicy{}
}

// ConfigureBashEnv — COREMETRY_AGENT_ENV_PASSTHROUGH girdilerini ayrıştırır
// ve paket politikası olarak saklar. Dönen listeler YALNIZ ad taşır (boot
// logu için). Testler t.Cleanup'ta ConfigureBashEnv(nil) ile sıfırlar.
func ConfigureBashEnv(entries []string) (accepted, rejected []string) {
	p, accepted, rejected := parseBashPassthrough(entries)
	bashPolicy.Store(&p)
	return accepted, rejected
}

// BashBaseEnvNames — boot logu için taban + proxy adlarının KOPYASI.
func BashBaseEnvNames() []string {
	out := make([]string, 0, len(bashBaseEnv)+len(bashProxyEnv))
	out = append(out, bashBaseEnv...)
	return append(out, bashProxyEnv...)
}

// bashEnv — SAF: ebeveyn ortamından bash adımına geçecek "K=V" listesi,
// anahtar adına göre sıralı (mcpclient.stdioEnv ile aynı determinizm). ASLA
// nil dönmez: nil cmd.Env "her şeyi devral" demektir; boş dilim boş ortamdır.
// Sıra: sert red → operatörün tam adı (değer olduğu gibi) → taban → '@'
// taşımayan proxy → kimlik-benzeri olmayan önek eşleşmesi → düşür.
func bashEnv(parent []string, p bashEnvPolicy) []string {
	vals := map[string]string{}
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || bashEnvDenied(k) {
			continue
		}
		if bashEnvAllowed(k, v, p) {
			vals[k] = v
		}
	}
	if strings.TrimSpace(vals["PATH"]) == "" {
		vals["PATH"] = bashDefaultPath
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vals[k])
	}
	return out
}

// bashEnvAllowed — sert red geçmiş TEK bir girdinin kural zinciri (bashEnv).
func bashEnvAllowed(k, v string, p bashEnvPolicy) bool {
	if p.exact[k] {
		return true
	}
	for _, b := range bashBaseEnv {
		if k == b {
			return true
		}
	}
	for _, px := range bashProxyEnv {
		if k == px {
			return !strings.Contains(v, "@")
		}
	}
	for _, pre := range p.prefix {
		if strings.HasPrefix(k, pre) {
			return !looksLikeCredential(k, v)
		}
	}
	return false
}
