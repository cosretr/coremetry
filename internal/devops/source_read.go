package devops

// source_read.go — v0.10.1050 (operatör: "Sohbet kod okuyabilsin: takip
// soruları bugün kod okuyamıyor."): sohbetin read_source_code aracının
// DevOps yarısı.
//
// Bugüne dek tek kod okuyucusu FetchCodeAt'ti ve yalnız trace/exception
// açıklamasının stack frame'lerini izliyordu. Sohbet ("bu metodun devamında ne
// var?", "X sınıfına da bak") yalnız açıklamanın 3000 rune'luk bağlamında
// alıntılanmış kodu görebiliyordu.
//
// ── NEDEN AYRI BİR OKUYUCU, NEDEN BU KADAR DAR ─────────────────────────────
//
// Aracın argümanlarını MODEL yazar ve modelin önündeki metnin büyük kısmı
// telemetridir (log gövdesi, exception mesajı, span adı) — span gönderebilen
// herkes o metni yazar. Yani argümanlar YÖNLENDİRİLEBİLİR kabul edilir:
//
//  1. Depo YALNIZ mevcut servis → depo çözümünden gelir (katalog pini /
//     ad konvansiyonu; çağıran ResolveRepo'dan geçirip Repo'yu verir) ve
//     servis sohbetin trace ÖZNESİNDE geçmelidir (çağıran denetler). Model
//     proje, depo, koleksiyon, URL ya da ref ADLANDIRAMAZ; organizasyon
//     araması (CodeSearch) bu yolda HİÇ koşmaz — bir sınıf adıyla başka
//     depoya sıçramak, modele depo seçtirmek olurdu.
//  2. Dosya YALNIZ depo AĞACINDAKİ bir yolla eşleşerek seçilir (BestPathForFrame
//     ailesi: ad + sonek). Ağacın listelemediği bir yol hiçbir zaman istenmez;
//     `..`, mutlak yol, joker ve kontrol karakteri istekten ÖNCE reddedilir.
//  3. YALNIZ KAYNAK DOSYA: uzantı izin listesi + yapılandırma/kimlik gibi görünen
//     her şeyi reddeden yasak listesi; yasak liste izni EZER. Reddedilen dosya
//     için içerik isteği hiç atılmaz ("bu dosya türü okunmaz").
//  4. Çıktı sınırlı: pencere merkezin iki yanında en çok 60 satır, satır başına
//     400 rune; toplam bütçe çağıranda (araç sonucu tavanının altında).
//
// Sürüm: SUNUCU türetir (öznenin span'leri; model seçemez) ve v0.10.1044'ün
// biçim kapısından geçer; yer tutucu ya da biçimsiz sürüm "sürüm yok" sayılır
// ve HİÇBİR yerde yankılanmaz — dal sırası okunur.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SourceOutcome — bir kaynak okumasının sınıfı (araç sonucunun `outcome` alanı).
type SourceOutcome string

const (
	SourceOK             SourceOutcome = "ok"
	SourceAmbiguous      SourceOutcome = "ambiguous"
	SourceNotFound       SourceOutcome = "not_found"
	SourceDenied         SourceOutcome = "denied"
	SourceInvalid        SourceOutcome = "invalid"
	SourceUnconfigured   SourceOutcome = "not_configured"
	SourceRepoUnresolved SourceOutcome = "repo_unresolved"
	SourceProjectDeadEnd SourceOutcome = "project_unresolved"
	SourceCatalogError   SourceOutcome = "catalog_error"
	SourceBackendError   SourceOutcome = "backend_error"
	SourceDeadline       SourceOutcome = "timeout"
	SourceCancelled      SourceOutcome = "cancelled"
	SourceUnreadable     SourceOutcome = "unreadable"
	// v0.10.1050 (güvenlik incelemesi) — kapsam: araç yalnız sohbetin trace
	// öznesindeki servislerin deposunu okur. Özne yok → out_of_scope; öznenin
	// trace'i okunamadı → trace_unreadable (api/chat_source_code.go).
	SourceOutOfScope      SourceOutcome = "out_of_scope"
	SourceTraceUnreadable SourceOutcome = "trace_unreadable"
)

// sourceDeniedReasonTR — reddin modele ve operatöre giden cümlesi (içerik yok).
const sourceDeniedReasonTR = "bu dosya türü okunmaz"

const (
	// SourceContextDefault / SourceContextMax — pencere yarıçapı (satır):
	// varsayılan explain penceresiyle aynı (codeWindowRadius), tavan iki katı.
	SourceContextDefault = codeWindowRadius
	SourceContextMax     = 60
	// SourceQueryMaxBytes — `file` argümanının tavanı. Gerçek bir paket
	// yolu + sınıf adı bunun çok altında kalır.
	SourceQueryMaxBytes = 200
	// SourceCandidateMax — birden çok eşleşmede modele giden aday yol sayısı.
	SourceCandidateMax = 10
	// sourceLineMaxRunes — tek satırın tavanı: küçültülmüş (minified) bir
	// dosyanın 100 KB'lık tek satırı pencere bütçesini tek başına yemesin.
	sourceLineMaxRunes = 400
	// sourceMaxLine — `line` argümanının mantıklı üst sınırı.
	sourceMaxLine = 10_000_000
)

// SourceRequest — ReadSource girdisi. Repo ve Hint ÇAĞIRANIN servis → depo
// çözümünden gelir (ResolveRepo); model bu iki alana dokunamaz.
type SourceRequest struct {
	Repo string
	Hint ProjectHint
	// Service — ham servis adı (depo onu ÇÖZEN çağıranda seçildi). Yalnız
	// sourceChain adaptörüne gider: sürüm→ref deseni servis bilgisi isterse
	// (entegrasyon notu, sourceChain) tek çağrı yeri orası.
	Service string
	File    string // NormalizeSourceQuery'den geçmiş sorgu
	Line    int    // pencere merkezi; 0 = dosyanın ilk penceresi
	Context int    // yarıçap; 0 = SourceContextDefault, tavan SourceContextMax
	Version string // çalışan sürüm (SUNUCU türetir: öznenin trace'i); boş = dal sırası
}

// SourceRead — okumanın ürünü. Content YALNIZ modele gider: tarayıcıya,
// ai_calls'a ve span'lere yalnız referans alanları (Repo, ref, Path, satır
// aralığı) çıkar — "kod tarayıcıya gitmez" sözleşmesi (copilot_code.go).
type SourceRead struct {
	Outcome SourceOutcome
	Repo    string
	Branch  string
	// RefKind "commit" (çalışan sürümün commit'i) | "branch" (dal ucu).
	RefKind string
	// Version — sürüm biliniyordu (biçim kapısından geçti); Commit doluysa o
	// sürümün commit'inden okundu, boşsa dal ucuna düşüldü (Reason söyler).
	Version string
	Commit  string
	Path    string // depo-göreli yol (baştaki "/" yok)
	// Line — istenen merkez (dosya dışındaysa son satıra çekilmiş hâli).
	Line int
	// TotalLines — okunan satır sayısı; FileTruncated ise dosyanın GERÇEK
	// boyu değil, yanıt tavanına (fileBodyCap) kadar okunan kısım.
	TotalLines    int
	FileTruncated bool
	FromLine      int
	ToLine        int
	// Content — "N| kod" biçiminde numaralı pencere (YALNIZ modele).
	Content string
	// Signature — pencerenin ÜSTÜNDE kalan çevreleyen metot imzası (YALNIZ modele).
	Signature     string
	SignatureLine int
	// Candidates — birden çok eşleşmede (Outcome=ambiguous) en çok
	// SourceCandidateMax depo-göreli yol; CandidatesTotal gerçek sayı.
	Candidates      []string
	CandidatesTotal int
	// DeniedMatches — eşleşip dosya türü yüzünden okunmayan yol sayısı.
	DeniedMatches int
	TreeCapped    bool
	// Reason — operatöre/modele düz cümle (düzeltme izi, geri-düşüş, ıska);
	// KOD İÇERMEZ.
	Reason string
}

// RefLabel — okunan ref'in gösterimi: "1.4.2 (çalışan sürüm, commit c0ffee00)"
// ya da "release (dal)". Okuma ağaca ulaşmadıysa "".
func (r SourceRead) RefLabel() string {
	switch {
	case r.RefKind == "commit" && r.Commit != "":
		return fmt.Sprintf("%s (çalışan sürüm, commit %s)", r.Version, shortSHA(r.Commit))
	case r.Branch != "":
		return r.Branch + " (dal)"
	}
	return ""
}

// ClampSourceContext — yarıçap: ≤0 → varsayılan, tavan SourceContextMax.
func ClampSourceContext(n int) int {
	switch {
	case n <= 0:
		return SourceContextDefault
	case n > SourceContextMax:
		return SourceContextMax
	}
	return n
}

// SourceVersionUsable — SAF: sürüm ref'e bağlanabilir mi (yer tutucu değil +
// v0.10.1044 biçim kapısı). Kapının TEK tanımı refSafeVersion; burası onu
// dışa açar ki araç biçimsiz sürümü istekten ÖNCE reddedebilsin.
func SourceVersionUsable(v string) bool {
	v = strings.TrimSpace(v)
	return !IsPlaceholderVersion(v) && refSafeVersion(v)
}

// NormalizeSourceQuery — `file` argümanının giriş kapısı (SAF, tablo-testli).
// Değer hiçbir zaman URL'e girmez (yalnız ağaçla eşleştirilir), ama yine de
// her şüpheli biçim İSTEKTEN ÖNCE reddedilir: kontrol karakteri, joker,
// ters bölü, `..`, mutlak yol (/, ~), boş ya da "." yol parçası, 200 bayt üstü.
// İzinli karakterler: harf, rakam ve `. _ - / $ + @`.
func NormalizeSourceQuery(q string) (string, error) {
	// Mesajlar "zorunlu"/"geçersiz" taşır: mcp.ClassifyToolError onları
	// bad_args sınıfına koyar (model aynı çağrıyı tekrarlamaz, argümanı düzeltir).
	q = strings.TrimSpace(q)
	if q == "" {
		return "", errors.New("file zorunlu — dosya ya da sınıf adı ver (ör. ChargeHandler.java)")
	}
	if len(q) > SourceQueryMaxBytes {
		return "", fmt.Errorf("file geçersiz: çok uzun (%d bayt; en çok %d)", len(q), SourceQueryMaxBytes)
	}
	if !utf8.ValidString(q) {
		return "", errors.New("file geçersiz: UTF-8 değil")
	}
	for _, r := range q {
		switch {
		case unicode.IsControl(r):
			return "", errors.New("file geçersiz: kontrol karakteri içeremez")
		case strings.ContainsRune("*?[]{}", r):
			return "", errors.New("file geçersiz: joker karakter içeremez — tam dosya ya da sınıf adı ver")
		case unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-/$+@", r):
		default:
			return "", errors.New("file geçersiz: yalnız harf, rakam ve . _ - / $ + @ içerebilir")
		}
	}
	if strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~") {
		return "", errors.New("file geçersiz: mutlak yol olamaz — depo-göreli yol ya da dosya adı ver")
	}
	if strings.Contains(q, "..") {
		return "", errors.New("file geçersiz: üst dizin (..) içeremez")
	}
	for _, seg := range strings.Split(q, "/") {
		if seg == "" || seg == "." {
			return "", errors.New("file geçersiz: boş ya da '.' yol parçası içeremez")
		}
	}
	return q, nil
}

// sourceAllowExt — okunabilen kaynak uzantıları. JVM dilleri (stack ayrıştırıcı
// ve explain'in pencere çiti bunları tanır), C# (explain'in hata-kodu araması
// .cs fırlatıcıyı okur) ve servis dillerinin yaygınları. İki dar kural
// (sourceNarrowAllowed): .sql yalnız mapper/mappers dizininde (dökümler,
// seed'ler okunmaz), .xml yalnız adı "…Mapper.xml" olan MyBatis mapper.
var sourceAllowExt = map[string]bool{
	"java": true, "kt": true, "kts": true, "scala": true, "groovy": true,
	"cs": true, "go": true, "py": true, "js": true, "jsx": true, "ts": true, "tsx": true,
	"rb": true, "php": true, "sql": true,
}

// sourceDenyExt — yapılandırma / anahtar / sertifika / durum dosyaları. Yasak
// liste izin listesini EZER; .xml'in mapper dışı hâli ayrıca reddedilir.
var sourceDenyExt = map[string]bool{
	"properties": true, "yml": true, "yaml": true, "json": true, "toml": true, "ini": true,
	"cfg": true, "conf": true, "config": true, "env": true, "pem": true, "key": true,
	"crt": true, "cer": true, "der": true, "p12": true, "pfx": true, "jks": true,
	"keystore": true, "truststore": true, "kdbx": true, "tfvars": true, "tfstate": true,
	"gradle": true, "npmrc": true, "netrc": true, "pgpass": true, "htpasswd": true,
}

// sourceDenyTokens — yolun HERHANGİ bir yerinde geçen kimlik kokulu parçalar.
var sourceDenyTokens = []string{"secret", "credential", "password", "passwd", "privatekey", "private_key", "keystore", "truststore"}

// sourceDenyNameTokens — v0.10.1050 (güvenlik incelemesi): DOSYA ADINDA geçen
// anahtar/bağlantı kokulu parçalar (ApiKeys.java, DataSourceConfig.java).
var sourceDenyNameTokens = []string{"apikey", "api-key", "api_key", "datasource"}

// sourceDenyStems — adın ilk parçası (ilk noktaya kadar) bu kümedeyse dosya
// yapılandırma gibi görünür: settings.py, config.go, Config.java, conf.py …
// v0.10.1050 — wp-config.php, LocalSettings.php, environment.prod.ts,
// ormconfig.ts, knexfile.js.
var sourceDenyStems = map[string]bool{
	"settings": true, "local_settings": true, "config": true, "configuration": true,
	"conf": true, "secrets": true, "credentials": true, "appsettings": true, "env": true,
	"wp-config": true, "localsettings": true, "environment": true, "ormconfig": true, "knexfile": true,
}

// sourceDenyDirs — v0.10.1050: uzantı → yapılandırma DİZİNİ. Bu dillerde
// ayarlar kod dosyası olarak yazılır (Django settings/production.py, Node
// config/database.js, Angular src/environments/, Rails config/initializers/);
// yol böyle bir dizinden geçiyorsa dosya yapılandırmadır.
var sourceDenyDirs = map[string][]string{
	"py":  {"settings"},
	"js":  {"config", "environments", "initializers"},
	"jsx": {"config", "environments", "initializers"},
	"ts":  {"config", "environments", "initializers"},
	"tsx": {"config", "environments", "initializers"},
	"rb":  {"config", "environments", "initializers"},
	"php": {"config", "environments", "initializers"},
}

// sourceSQLDirs — v0.10.1050: .sql yalnız bu dizinlerden birinin altında
// okunur (MyBatis mapper SQL'i); dökümler, seed'ler, migration'lar okunmaz.
var sourceSQLDirs = map[string]bool{"mapper": true, "mappers": true}

// sourceExt — son yol parçasının uzantısı (küçük harf, noktasız); yoksa "".
func sourceExt(base string) string {
	i := strings.LastIndex(base, ".")
	if i <= 0 || i == len(base)-1 {
		return ""
	}
	return strings.ToLower(base[i+1:])
}

// knownSourceExt — uzantı iki listeden birinde (ya da xml) mi: sorgu biçimini
// (dosya adı mı, sınıf adı mı) seçerken kullanılır.
func knownSourceExt(ext string) bool {
	return ext == "xml" || sourceAllowExt[ext] || sourceDenyExt[ext]
}

// sourceDenyReason — yasak listesinin kararı (yol ya da sorgu için ortak);
// "" = yasak değil. lower: küçük harfli tam metin.
func sourceDenyReason(lower string) string {
	segs := strings.Split(lower, "/")
	for _, seg := range segs {
		if strings.HasPrefix(seg, ".env") {
			return sourceDeniedReasonTR + " (ortam/kimlik dosyası)"
		}
	}
	for _, tok := range sourceDenyTokens {
		if strings.Contains(lower, tok) {
			return sourceDeniedReasonTR + " (adı kimlik bilgisi çağrıştırıyor)"
		}
	}
	base := segs[len(segs)-1]
	ext := sourceExt(base)
	if sourceDenyExt[ext] {
		return sourceDeniedReasonTR + " (yapılandırma/anahtar dosyası)"
	}
	for _, tok := range sourceDenyNameTokens {
		if strings.Contains(base, tok) {
			return sourceDeniedReasonTR + " (adı anahtar/bağlantı bilgisi çağrıştırıyor)"
		}
	}
	stem := base
	if i := strings.Index(stem, "."); i >= 0 {
		stem = stem[:i]
	}
	if sourceDenyStems[stem] || strings.Contains(base, ".config.") || strings.Contains(base, ".conf.") ||
		strings.Contains(base, ".settings.") || strings.Contains(base, ".gradle") {
		return sourceDeniedReasonTR + " (yapılandırma dosyası gibi görünüyor)"
	}
	for _, dir := range sourceDenyDirs[ext] {
		for _, seg := range segs[:len(segs)-1] {
			if seg == dir {
				return sourceDeniedReasonTR + " (yapılandırma dizininde: " + dir + "/)"
			}
		}
	}
	return ""
}

// sourceNarrowAllowed — v0.10.1050: .xml yalnız adı "…mapper.xml" (MyBatis
// mapper; mybatis-config.xml DEĞİL), .sql yalnız sourceSQLDirs altında.
func sourceNarrowAllowed(lower, ext string) bool {
	segs := strings.Split(lower, "/")
	switch ext {
	case "xml":
		return strings.HasSuffix(segs[len(segs)-1], "mapper.xml")
	case "sql":
		for _, seg := range segs[:len(segs)-1] {
			if sourceSQLDirs[seg] {
				return true
			}
		}
		return false
	}
	return true
}

// ClassifySourcePath — SAF, tablo-testli: depo ağacındaki bir yol okunabilir
// kaynak dosya mı? Yasak liste ÖNCE (izni ezer), sonra uzantı izin listesi.
// Dönen why yalnız reddedildiğinde dolu.
func ClassifySourcePath(p string) (ok bool, why string) {
	lower := strings.ToLower(strings.TrimSpace(p))
	if lower == "" {
		return false, sourceDeniedReasonTR
	}
	if r := sourceDenyReason(lower); r != "" {
		return false, r
	}
	segs := strings.Split(lower, "/")
	ext := sourceExt(segs[len(segs)-1])
	switch {
	case ext == "xml" || ext == "sql":
		if sourceNarrowAllowed(lower, ext) {
			return true, ""
		}
		return false, sourceDeniedReasonTR + " (XML yalnız …Mapper.xml, SQL yalnız mapper dizininde okunur)"
	case sourceAllowExt[ext]:
		return true, ""
	}
	return false, sourceDeniedReasonTR + " (kaynak dosya uzantısı değil)"
}

// ClassifySourceQuery — SAF: sorgunun KENDİSİ istekten önce reddedilebilir mi?
// Uzantı taşıyan sorgu yol gibi sınıflanır; uzantısız sorguda (sınıf adı)
// yalnız yasak liste uygulanır — son karar ağaçtaki gerçek yolda.
func ClassifySourceQuery(q string) (ok bool, why string) {
	lower := strings.ToLower(strings.TrimSpace(q))
	if r := sourceDenyReason(lower); r != "" {
		return false, r
	}
	// Noktalı sınıf adı (com.example.Config, com.example.Config.java) yol
	// biçimine çevrilir: son parçanın yapılandırma kokusu ancak öyle görünür.
	asPath := lower
	if !strings.Contains(lower, "/") {
		if ext := sourceExt(lower); knownSourceExt(ext) {
			asPath = strings.ReplaceAll(lower[:len(lower)-len(ext)-1], ".", "/") + "." + ext
		} else {
			asPath = strings.ReplaceAll(lower, ".", "/")
		}
	}
	if r := sourceDenyReason(asPath); r != "" {
		return false, r
	}
	segs := strings.Split(asPath, "/")
	if ext := sourceExt(segs[len(segs)-1]); ext != "" && knownSourceExt(ext) {
		return ClassifySourcePath(asPath)
	}
	return true, ""
}

// sourceQueryForms — sorgudan eşleşme biçimleri: suffixes (yol "/"+s ile
// biter) ya da stems (uzantısız yol "/"+s ile biter, uzantı ne olursa olsun).
func sourceQueryForms(q string) (suffixes, stems []string) {
	if strings.Contains(q, "/") {
		segs := strings.Split(q, "/")
		if knownSourceExt(sourceExt(segs[len(segs)-1])) {
			return []string{q}, nil
		}
		return nil, []string{q}
	}
	if ext := sourceExt(q); knownSourceExt(ext) {
		stem := q[:len(q)-len(ext)-1]
		suffixes = []string{q}
		if strings.Contains(stem, ".") { // com.example.cards.ChargeHandler.java
			suffixes = append(suffixes, strings.ReplaceAll(stem, ".", "/")+q[len(stem):])
		}
		return suffixes, nil
	}
	base := q
	if i := strings.Index(base, "$"); i > 0 { // iç sınıf: ChargeHandler$1 → ChargeHandler
		base = base[:i]
	}
	if !strings.Contains(base, ".") {
		return nil, []string{base}
	}
	parts := strings.Split(base, ".")
	stems = []string{strings.Join(parts, "/")}
	// com.example.cards.ChargeHandler.charge — son parça küçük harfle
	// başlıyorsa metot olabilir: bir de onsuz dene.
	if last := parts[len(parts)-1]; len(parts) > 2 && last != "" && unicode.IsLower([]rune(last)[0]) {
		stems = append(stems, strings.Join(parts[:len(parts)-1], "/"))
	}
	return nil, stems
}

// pathStem — yolun uzantısız hâli (son parçada nokta yoksa yolun kendisi).
func pathStem(p string) string {
	i, j := strings.LastIndex(p, "."), strings.LastIndex(p, "/")
	if i > j+1 {
		return p[:i]
	}
	return p
}

// MatchSourcePaths — SAF, tablo-testli: sorguyu depo AĞACININ yollarıyla
// eşleştirir (BestPathForFrame ailesi: tam parça soneki; ChargeHandler.java
// MyChargeHandler.java'yı yakalamaz). Karşılaştırma harf-duyarsız; birden çok
// izinli eşleşmede TEK bir harf-duyarlı eşleşme varsa o seçilir. allowed
// okunabilir yollar (kısa önce, sonra sözlük), denied eşleşip yasak listeye
// takılan yol sayısı. Ağaçta olmayan bir yol bu fonksiyondan ÇIKAMAZ.
func MatchSourcePaths(paths []string, q string) (allowed []string, denied int) {
	suffixes, stems := sourceQueryForms(q)
	match := func(p string, fold bool) bool {
		cmp, stem := p, pathStem(p)
		norm := func(s string) string { return s }
		if fold {
			cmp, stem = strings.ToLower(cmp), strings.ToLower(stem)
			norm = strings.ToLower
		}
		for _, s := range suffixes {
			w := "/" + norm(s)
			if strings.HasSuffix(cmp, w) || cmp == norm(s) {
				return true
			}
		}
		for _, s := range stems {
			w := "/" + norm(s)
			if stem != cmp && (strings.HasSuffix(stem, w) || stem == norm(s)) {
				return true
			}
		}
		return false
	}
	var exact []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] || !match(p, true) {
			continue
		}
		seen[p] = true
		if ok, _ := ClassifySourcePath(p); !ok {
			denied++
			continue
		}
		allowed = append(allowed, p)
		if match(p, false) {
			exact = append(exact, p)
		}
	}
	if len(allowed) > 1 && len(exact) == 1 {
		return exact, denied
	}
	sort.Slice(allowed, func(i, j int) bool {
		if len(allowed[i]) != len(allowed[j]) {
			return len(allowed[i]) < len(allowed[j])
		}
		return allowed[i] < allowed[j]
	})
	return allowed, denied
}

// sourceWindow — dosya içeriğinden numaralı pencere ("N| kod", WindowAround
// biçimi). line ≤ 0 → dosyanın ilk penceresi (1 … 2r+1); dosyanın dışındaki
// satır son satıra çekilir (outside=true). Satırlar sourceLineMaxRunes'ta
// kırpılır. İmza yalnız merkezli pencerede ve pencerenin üstündeyse.
func sourceWindow(content string, line, radius int) (w CodeWindow, total int, outside bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total = len(lines)
	if total == 0 || strings.TrimSpace(content) == "" {
		return CodeWindow{}, 0, false
	}
	from, to := 1, 2*radius+1
	if line > 0 {
		if line > total {
			line, outside = total, true
		}
		from, to = line-radius, line+radius
		if from < 1 {
			from = 1
		}
	}
	if to > total {
		to = total
	}
	var b strings.Builder
	for i := from; i <= to; i++ {
		ln := lines[i-1]
		if r := []rune(ln); len(r) > sourceLineMaxRunes {
			ln = string(r[:sourceLineMaxRunes]) + " …[satır kırpıldı]"
		}
		fmt.Fprintf(&b, "%d| %s\n", i, ln)
	}
	w = CodeWindow{Line: line, FromLine: from, ToLine: to, Content: strings.TrimRight(b.String(), "\n")}
	if line > 0 && from > 1 {
		if sig, at := EnclosingSignature(lines, from); sig != "" {
			if r := []rune(sig); len(r) > sourceLineMaxRunes {
				sig = string(r[:sourceLineMaxRunes]) + " …"
			}
			w.Signature, w.SignatureLine = sig, at
		}
	}
	return w, total, outside
}

// trimTruncatedBody — v0.10.1050 (SAF): dosya gövdesi okuma tavanına
// (fileBodyCap; fetchItemContentAt'in metin yolu LimitReader'la keser)
// dayandıysa yarım kalan SON satır atılır ve kesildiği söylenir. JSON yolu
// tavanı aşan gövdede çözülemez ve metin yoluna düşer, yani tavana dayanan
// içerik daima kesik sayılır (tam tavan boyunda bir dosya da — dürüst yön).
func trimTruncatedBody(body string, capBytes int) (string, bool) {
	if len(body) < capBytes {
		return body, false
	}
	if i := strings.LastIndexByte(body, '\n'); i >= 0 {
		return body[:i+1], true
	}
	return body, true
}

// ClipSourceWindow — numaralı pencereyi maxRunes'a indirir (SAF): merkez
// satır biliniyorsa o MERKEZDE kalır (centerToBudget), bilinmiyorsa ya da tek
// başına sığmıyorsa baştan satır sınırında kesilir. Dönen from/to korunan
// aralık; içerik "" ise hiçbir satır sığmadı.
func ClipSourceWindow(content string, center, maxRunes int) (string, int, int) {
	if utf8.RuneCountInString(content) <= maxRunes {
		f, t := numberedRange(content)
		return content, f, t
	}
	if center > 0 {
		if cut, f, t := centerToBudget(content, center, maxRunes); cut != "" {
			return cut, f, t
		}
	}
	cut, last := cutToLineBoundary(content, maxRunes)
	if cut == "" {
		return "", 0, 0
	}
	f, _ := numberedRange(cut)
	return cut, f, last
}

// numberedRange — numaralı içeriğin ilk ve son satır numarası.
func numberedRange(content string) (int, int) {
	lines := strings.Split(content, "\n")
	f, _ := lineNumberOf(lines[0])
	t, _ := lineNumberOf(lines[len(lines)-1])
	return f, t
}

// ReadSource — servisin deposundan TEK bir kaynak dosyanın penceresi.
//
// Zincir explain'inkiyle AYNI parçalardan (çift yazım yok): pickProject →
// resolveChain (branş + depo adı düzeltmesi + 10 dk cache'li ağaç) →
// resolveRevision (sürüm → ref → commit ağacı; yoksa dal) → AĞAÇTA eşleşme →
// fetchItemContentAt — üç ağ çağrısı TEK adaptörde (sourceChain). Bilinçli
// farklar: organizasyon araması YOK (madde 1),
// kapsamlı alt-ağaç geri-denemesi YOK (kesik ağaçta ıska dürüstçe söylenir),
// sayaçlar (RecordCodeOutcome) "Kodu da incele" isabet oranını ölçtüğü için
// burada ARTMAZ. Süre tavanı FetchCode'unki (25 sn; sohbet aracının 20 sn
// bütçesi önce ısırır).
func (s *Service) ReadSource(ctx context.Context, req SourceRequest) SourceRead {
	out := SourceRead{Repo: strings.TrimSpace(req.Repo)}
	q, err := NormalizeSourceQuery(req.File)
	if err != nil {
		out.Outcome, out.Reason = SourceInvalid, err.Error()
		return out
	}
	if ok, why := ClassifySourceQuery(q); !ok {
		out.Outcome, out.Reason = SourceDenied, why
		return out
	}
	if s == nil || !s.Configured() {
		out.Outcome, out.Reason = SourceUnconfigured, "kod entegrasyonu yapılandırılmamış (Ayarlar → Kod entegrasyonu)"
		return out
	}
	if out.Repo == "" {
		out.Outcome, out.Reason = SourceRepoUnresolved, "servis için depo çözülemedi"
		return out
	}
	cfg := s.CurrentSettings()
	radius := ClampSourceContext(req.Context)
	line := req.Line
	if line < 0 {
		line = 0
	}
	if line > sourceMaxLine {
		line = sourceMaxLine
	}
	note, version := "", strings.TrimSpace(req.Version)
	if version != "" && !SourceVersionUsable(version) {
		// v0.10.1044 kapısı: biçimsiz/yer tutucu sürüm "sürüm yok" — değeri yankılanmaz.
		note, version = "verilen sürüm kullanılamadı (yer tutucu ya da biçimsiz) — dal sırasıyla okundu", ""
	}

	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, s.fetchDeadline())
	defer cancel()
	project, _, dead := pickProject(cfg, req.Hint)
	if dead != "" {
		out.Outcome, out.Reason = SourceProjectDeadEnd, dead
		return out
	}
	cfg.Project = project
	sc := s.sourceChain(ctx, parent, cfg, out.Repo, req.Service, version)
	ch := sc.chain
	out.Repo, out.Branch = ch.repo, ch.branch
	if ch.class != "" {
		out.Outcome, out.Reason = SourceBackendError, ch.reason
		if ch.class == CodeDeadline {
			out.Outcome = SourceDeadline
		}
		if o, r, ok := sourceParentOutcome(parent); ok {
			out.Outcome, out.Reason = o, r
		}
		return out
	}
	note = withNote(ch.note, note)
	paths, capped, why := sc.paths, sc.capped, sc.why
	if rev := sc.rev; rev != nil {
		out.Version = rev.Version
		if rev.Verified {
			out.Commit = rev.SHA
		} else {
			note = withNote(note, revisionFallbackNote(rev, ch.branch))
		}
	}
	out.RefKind, out.TreeCapped = sc.src.Kind, capped

	allowed, denied := MatchSourcePaths(paths, q)
	out.DeniedMatches = denied
	switch {
	case len(allowed) == 0 && denied > 0:
		out.Outcome = SourceDenied
		out.Reason = withNote(note, fmt.Sprintf("%s — %d eşleşen dosya yapılandırma/kimlik dosyası ya da kaynak dışı", sourceDeniedReasonTR, denied))
		return out
	case len(allowed) == 0:
		out.Outcome = SourceNotFound
		miss := "depo ağacında eşleşen kaynak dosya yok: " + q
		if capped {
			// Kesilme yalnız ıskada söylenir (v0.10.1040 kuralı): dosya kesik bölgede olabilir.
			miss = withNote(miss, cappedTreeNote(true, why, len(paths), s.treePathCap(), nil, 0))
		}
		out.Reason = withNote(note, miss)
		return out
	case len(allowed) > 1:
		out.Outcome, out.CandidatesTotal = SourceAmbiguous, len(allowed)
		for _, p := range allowed {
			if len(out.Candidates) >= SourceCandidateMax {
				break
			}
			out.Candidates = append(out.Candidates, strings.TrimPrefix(p, "/"))
		}
		out.Reason = note
		return out
	}

	path := allowed[0] // AĞACIN listelediği yol — başka bir yol istenmez
	out.Path = strings.TrimPrefix(path, "/")
	body, ferr := sc.fetch(ctx, path)
	if ferr != nil {
		o, r, byParent := sourceParentOutcome(parent)
		switch {
		case byParent:
			out.Outcome, out.Reason = o, r
		case deadlineHit(parent, ctx):
			out.Outcome, out.Reason = SourceDeadline, deadlineReason(s.fetchDeadline())
		default:
			out.Outcome, out.Reason = SourceUnreadable, "dosya okunamadı: "+sanitize(firstLine(ferr.Error()), cfg)
		}
		out.Reason = withNote(note, out.Reason)
		return out
	}
	body, truncated := trimTruncatedBody(body, fileBodyCap)
	w, total, outside := sourceWindow(body, line, radius)
	if total == 0 {
		out.Outcome, out.Reason = SourceUnreadable, withNote(note, "dosya boş")
		return out
	}
	switch {
	case truncated && outside:
		// Gerçek boy bilinmiyor: "dosyanın dışında" demek YANLIŞ olurdu.
		note = withNote(note, fmt.Sprintf("dosya büyük (yanıt tavanı %d MB), ilk %d satır okundu — istenen satır (%d) okunan kısmın dışında, son okunan pencere verildi", fileBodyCap>>20, total, line))
	case truncated:
		note = withNote(note, fmt.Sprintf("dosya büyük (yanıt tavanı %d MB), ilk %d satır okundu — toplam satır sayısı bilinmiyor", fileBodyCap>>20, total))
	case outside:
		note = withNote(note, fmt.Sprintf("istenen satır (%d) dosyanın dışında — dosya %d satır, son pencere verildi", line, total))
	}
	out.FileTruncated = truncated
	out.Outcome, out.Reason = SourceOK, note
	out.Line, out.TotalLines, out.FromLine, out.ToLine = w.Line, total, w.FromLine, w.ToLine
	out.Content, out.Signature, out.SignatureLine = w.Content, w.Signature, w.SignatureLine
	return out
}

// sourceChainResult — sourceChain adaptörünün ürünü.
type sourceChainResult struct {
	chain  chainResult
	rev    *Revision // nil = sürüm yok / kullanılamaz (istek atılmadı)
	src    RefSpec   // okunan ref: çalışan sürümün commit'i ya da dal ucu
	paths  []string  // OKUNAN ref'in ağacı
	capped bool
	why    string
	// fetch — dosya çekici; yol ve içerik AYNI ref'ten (satır kayması sınıfı).
	fetch func(ctx context.Context, path string) (string, error)
}

// sourceChain — read_source_code'un DevOps ağ zinciri, TEK ADAPTÖRDE:
// resolveChain (branş + depo adı düzeltmesi + 10 dk cache'li ağaç) →
// resolveRevision (sürüm → ref → commit ağacı; yoksa dal) → okunan ref'e bağlı
// fetchItemContentAt. code.go / version_ref.go'daki bu üç çağrının imzası
// değişirse YALNIZ bu fonksiyon değişir. service HAM servis adıdır
// (SourceRequest.Service): resolveRevision onu sürüm→ref deseninin {service}
// yer tutucusu için kendisi normalleştirir (conventionName, v0.10.1047) —
// buraya depo adı ya da soyulmuş ad VERİLMEZ.
func (s *Service) sourceChain(ctx, parent context.Context, cfg Settings, repo, service, version string) sourceChainResult {
	cli := s.clientFor(cfg.InsecureSkipVerify)
	ch := s.resolveChain(ctx, parent, cli, cfg, repo)
	out := sourceChainResult{chain: ch, src: RefSpec{Kind: "branch", Name: ch.branch}, paths: ch.paths, capped: ch.capped, why: ch.cappedWhy}
	if ch.class != "" {
		return out
	}
	if rev, tree := s.resolveRevision(ctx, cli, cfg, ch.ver, ch.repo, service, version); rev != nil {
		out.rev = rev
		if rev.Verified {
			out.src, out.paths, out.capped, out.why = RefSpec{Kind: "commit", Name: rev.SHA}, tree.paths, tree.capped, tree.why
		}
	}
	ver, rp, src := ch.ver, ch.repo, out.src
	out.fetch = func(c context.Context, path string) (string, error) {
		return fetchItemContentAt(c, cli, cfg, ver, rp, src, path)
	}
	return out
}

// sourceParentOutcome — ÇAĞIRANIN bağlamı bittiyse sınıf: istemci iptali
// (Canceled) ile çağıranın süre bütçesinin dolması (sohbet aracının 20 sn'si,
// DeadlineExceeded) ayrı söylenir; ikisi de DevOps'un 25 sn tavanı değildir
// (deadlineHit yalnız onu yakalar). ok=false → çağıran bağlamı canlı.
func sourceParentOutcome(parent context.Context) (SourceOutcome, string, bool) {
	switch err := parent.Err(); {
	case errors.Is(err, context.Canceled):
		return SourceCancelled, "istek iptal edildi — dosya okunmadı", true
	case err != nil:
		return SourceDeadline, "araç süre bütçesi doldu — dosya okunmadı", true
	}
	return "", "", false
}
