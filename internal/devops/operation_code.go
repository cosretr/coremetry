package devops

// operation_code.go — v0.10.1330 (operatör: trace explain "Kod incelemesi"
// stacktrace'siz trace'te "Kod okunamadı — kodsuz analiz. (bu kayıtta
// stacktrace yok)" diyordu). Stack yokken trace'in ANA operasyonunun
// (api pickMainOperation: kendi Oracle satırı → span attribute'u) kodu
// Code Search ile bulunur: giriş metodu (operasyon sabitini taşıyan
// anotasyonun metodu) + o metodun İLK çağırdığı yönetici metodu.
//
// Sözleşmeler:
//   - YALNIZ ana operasyon; diğerleri okunmaz (prompt "+N okunmadı" der).
//   - İstek bütçesi: TEK arama + en çok İKİ dosya çekimi (giriş + çağrılan).
//     Depo zinciri (branş, ağaç, sürüm) explain'inkiyle aynı ve aynı
//     cache'lerden (sourceChain).
//   - Süre tavanı min(fetchDeadline, 15 sn): stack yolu zaten koştu.
//   - Taksonomi DEĞİŞMEZ (code_stats.go pinli): yeni Outcome yok, sayaç
//     ARTMAZ (buildCodeContext bu isteği no-stack olarak zaten saydı —
//     ikinci kayıt "operatör kod istedi" anını çift sayardı). Kaynak
//     CodeContext.Source = CodeSourceOperation ile ayrışır.
//   - Hata metni Reason'a firstLine + sanitize ile gider; PAT/yapılandırma asla.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// CodeSourceOperation — CodeContext.Source: pencereler stack frame'inden
// değil, trace'in ana operasyonundan (v0.10.1330).
const CodeSourceOperation = "operation"

const (
	operationEntryMaxLines  = 60
	operationCalleeMaxLines = 60
	operationBudgetCap      = 6000
	operationDeadlineCap    = 15 * time.Second
	operationSearchTop      = 20
	operationCacheTTL       = 10 * time.Minute
	operationCacheMax       = 32
	// operationAnnotationLookback — operasyon sabitinden geriye, çevreleyen
	// anotasyonun "@Ad(" başını ararken bakılan karakter tavanı.
	operationAnnotationLookback = 600
)

// operationCodeRe — v0.10.1330: operasyon kodu biçim kapısı. Değer
// telemetriden (Oracle satırı / span attribute'u) gelir; arama sorgusuna ve
// prompt'a girmeden önce dar bir alfabeye bağlanır.
var operationCodeRe = regexp.MustCompile(`^[A-Z0-9_]{6,120}$`)

// ValidOperationCode — SAF: operasyon kodu aranabilir biçimde mi?
func ValidOperationCode(op string) bool { return operationCodeRe.MatchString(op) }

// OperationCodeRequest — FetchOperationCode girdisi. Repo/Hint çağıranın
// servis → depo çözümünden (ResolveRepo; pin fail-closed) gelir.
type OperationCodeRequest struct {
	Repo      string
	Hint      ProjectHint
	Service   string // ham servis adı (sürüm→ref deseni için)
	Version   string // kök servisin çalışan sürümü; boş = dal sırası
	Operation string
	// Others — trace'teki diğer operasyon sayısı; yalnız etiket (okunmaz).
	Others int
	// Source — v0.10.1349: operasyonun kaynağı (oracle | span | log |
	// function-code); CodeContext.OperationSource'a damgalanır.
	// OperationSourceFunctionCode iken PromptBlock "ADAY, kesin değil" der.
	Source string
}

// OperationSourceFunctionCode — v0.10.1349: operasyon adı trace'in kendisinden
// değil, FUNCTION_CODE → Oracle operasyon sözlüğünden (1:n) ADAY.
const OperationSourceFunctionCode = "function-code"

// OperationSearchQuery — SAF: Azure DevOps Code Search sorgusu. Tırnaklı tam
// belirteç + ext:java (SearchServiceCode'un düz searchText sözdizimi).
func OperationSearchQuery(op string) string {
	return `"` + strings.TrimSpace(op) + `" ext:java`
}

// operationNonSourceExt — v0.10.1330: operasyon adı bu dosyalarda da geçer
// (rapor CSV'si, swagger yaml'ı, mesaj tablosu) ama kod değildir.
var operationNonSourceExt = map[string]bool{
	"csv": true, "yaml": true, "yml": true, "json": true, "xml": true, "md": true,
	"txt": true, "properties": true, "log": true, "manifest": true, "mf": true,
}

// isTestPath — test dizini / test sınıfı mı? (küçük harfli yol)
func isTestPath(lower string) bool {
	segs := strings.Split(strings.TrimPrefix(lower, "/"), "/")
	for _, s := range segs[:len(segs)-1] {
		if s == "test" || s == "tests" || s == "it" || s == "testing" {
			return true
		}
	}
	base := segs[len(segs)-1]
	return strings.HasSuffix(base, "test.java") || strings.HasSuffix(base, "tests.java") || strings.HasSuffix(base, "it.java")
}

// operationHitDropped — SAF: isabet operasyonun GİRİŞ dosyası olamaz mı?
// Kaynak-dışı uzantı, okuma yasak listesi, sabit sözlüğü (…Names.java,
// …Constants….java) ve test dizinleri elenir.
func operationHitDropped(p string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	if lower == "" {
		return true
	}
	segs := strings.Split(lower, "/")
	base := segs[len(segs)-1]
	if operationNonSourceExt[sourceExt(base)] {
		return true
	}
	if ok, _ := ClassifySourcePath(lower); !ok {
		return true
	}
	if strings.HasSuffix(base, "names.java") || strings.Contains(base, "constants") {
		return true
	}
	return isTestPath(lower)
}

// operationHitScore — SAF: giriş dosyası olma olasılığı.
func operationHitScore(h ServiceCodeHit) int {
	s := 0
	if strings.Contains(h.Snippet, "@Service(") || strings.Contains(h.Snippet, "SelfServiceNames.") {
		s += 100
	}
	segs := strings.Split(h.Path, "/")
	base := segs[len(segs)-1]
	if strings.Contains(base, "Controller") || strings.Contains(base, "Rest") {
		s += 10
	}
	if strings.Contains(strings.ToLower("/"+strings.TrimPrefix(h.Path, "/")), "/service/rest/") {
		s += 5
	}
	return s
}

// RankOperationHits — SAF, tablo-testli (v0.10.1330): elenmeyenleri skora
// göre sıralar; eşitlikte yol (deterministik — aynı trace iki kez aynı
// kanıtı üretsin).
func RankOperationHits(hits []ServiceCodeHit) []ServiceCodeHit {
	out := make([]ServiceCodeHit, 0, len(hits))
	for _, h := range hits {
		if operationHitDropped(h.Path) {
			continue
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := operationHitScore(out[i]), operationHitScore(out[j])
		if si != sj {
			return si > sj
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// maskJava — SAF: dize/karakter sabitlerinin İÇİ ve yorumlar boşlukla
// değiştirilir; uzunluk ve satır sonları korunur (ofsetler kaynakla bire bir).
// Böylece "}" taşıyan bir dize ya da yorumdaki bir çağrı yapı taramasını
// şaşırtmaz. Java metin blokları (""") da dize sayılır.
func maskJava(src string) string {
	b := []byte(src)
	n := len(b)
	blank := func(i int) {
		if b[i] != '\n' && b[i] != '\r' {
			b[i] = ' '
		}
	}
	for i := 0; i < n; {
		switch {
		case b[i] == '/' && i+1 < n && b[i+1] == '/':
			for i < n && b[i] != '\n' {
				blank(i)
				i++
			}
		case b[i] == '/' && i+1 < n && b[i+1] == '*':
			blank(i)
			blank(i + 1)
			i += 2
			for i < n && !(b[i] == '*' && i+1 < n && b[i+1] == '/') {
				blank(i)
				i++
			}
			if i < n {
				blank(i)
				i++
			}
			if i < n {
				blank(i)
				i++
			}
		case b[i] == '"' && i+2 < n && b[i+1] == '"' && b[i+2] == '"':
			i += 3
			for i < n && !(b[i] == '"' && i+2 < n && b[i+1] == '"' && b[i+2] == '"') {
				if b[i] == '\\' && i+1 < n {
					blank(i)
					i++
				}
				blank(i)
				i++
			}
			i += 3
		case b[i] == '"' || b[i] == '\'':
			q := b[i]
			i++
			for i < n && b[i] != q && b[i] != '\n' {
				if b[i] == '\\' && i+1 < n {
					blank(i)
					i++
				}
				blank(i)
				i++
			}
			i++
		default:
			i++
		}
	}
	return string(b)
}

// matchClose — SAF: masked[open] açılış ('(' ya da '{') → eşi; yoksa -1.
func matchClose(masked string, open int) int {
	if open < 0 || open >= len(masked) {
		return -1
	}
	o := masked[open]
	var c byte
	switch o {
	case '(':
		c = ')'
	case '{':
		c = '}'
	default:
		return -1
	}
	depth := 0
	for i := open; i < len(masked); i++ {
		switch masked[i] {
		case o:
			depth++
		case c:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// lineAt — ofsetin 1-tabanlı satır numarası.
func lineAt(s string, off int) int {
	if off > len(s) {
		off = len(s)
	}
	return strings.Count(s[:off], "\n") + 1
}

// lineStartOff — ofsetin bulunduğu satırın başı.
func lineStartOff(s string, off int) int {
	if i := strings.LastIndexByte(s[:off], '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

// annotationStartBefore — SAF: masked[pos]'u çevreleyen anotasyonun '@'
// ofseti; pos bir anotasyonun argümanı değilse -1.
func annotationStartBefore(masked string, pos int) int {
	floor := pos - operationAnnotationLookback
	if floor < 0 {
		floor = 0
	}
	depth := 0
	for k := pos - 1; k >= floor; k-- {
		switch masked[k] {
		case ')', '}':
			depth++
		case '{':
			if depth > 0 {
				depth--
			}
			// depth 0'da '{': anotasyon dizi ilklendiricisi olabilir
			// (@X(value = {A, B})) — '(' aramaya devam.
		case ';':
			if depth == 0 {
				return -1
			}
		case '(':
			if depth > 0 {
				depth--
				continue
			}
			j := k - 1
			for j >= 0 && (masked[j] == ' ' || masked[j] == '\t' || masked[j] == '\n' || masked[j] == '\r') {
				j--
			}
			end := j + 1
			for j >= 0 && (isIdentByte(masked[j]) || masked[j] == '.') {
				j--
			}
			if j >= 0 && masked[j] == '@' && end > j+1 {
				return j
			}
			return -1
		}
	}
	return -1
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// skipAnnotations — SAF: p'den itibaren boşluk + ardışık anotasyonları
// (argümanlarıyla) atlar; bildirimin başı döner.
func skipAnnotations(masked string, p int) int {
	n := len(masked)
	for {
		for p < n && (masked[p] == ' ' || masked[p] == '\t' || masked[p] == '\n' || masked[p] == '\r') {
			p++
		}
		if p >= n || masked[p] != '@' {
			return p
		}
		p++
		for p < n && (isIdentByte(masked[p]) || masked[p] == '.') {
			p++
		}
		q := p
		for q < n && (masked[q] == ' ' || masked[q] == '\t' || masked[q] == '\n' || masked[q] == '\r') {
			q++
		}
		if q < n && masked[q] == '(' {
			c := matchClose(masked, q)
			if c < 0 {
				return n
			}
			p = c + 1
		}
	}
}

// MethodWindow — kesilen metot penceresi + çağrı taraması için gövde.
type MethodWindow struct {
	Window CodeWindow
	// Body — metot gövdesi ('{' ile '}' arası, ham kaynak).
	Body string
	// Lines — metodun TAM satır sayısı (pencere kırpıldıysa > pencere).
	Lines int
	// Header / BodyLine (v0.10.1506, operasyon kartı R4) — bildirimin
	// anotasyonsuz başlığı (değiştiriciler + tip + ad + parametreler, '{'
	// öncesi; ham kaynak) ve gövdeyi açan '{' satırı. Kart imzayı ve çağrı
	// yerlerinin MUTLAK satırını bunlardan kurar; mevcut çağıranlar okumaz.
	Header   string
	BodyLine int
}

// Truncated — pencere metodun tamamını taşımıyor mu?
func (m MethodWindow) Truncated() bool {
	return m.Lines > m.Window.ToLine-m.Window.FromLine+1
}

// numberedLines — [from, to] satırlarını "N| kod" biçiminde (satır başına
// sourceLineMaxRunes tavanı).
func numberedLines(lines []string, from, to int) string {
	var b strings.Builder
	for i := from; i <= to && i <= len(lines); i++ {
		ln := lines[i-1]
		if r := []rune(ln); len(r) > sourceLineMaxRunes {
			ln = string(r[:sourceLineMaxRunes]) + " …[satır kırpıldı]"
		}
		fmt.Fprintf(&b, "%d| %s\n", i, ln)
	}
	return strings.TrimRight(b.String(), "\n")
}

func splitSourceLines(src string) []string {
	lines := strings.Split(src, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// methodWindowFrom — SAF: annoStart (ilk anotasyon ya da bildirim satırı
// ofseti) + declStart'tan metodu keser. ok=false: gövdeli bir bildirim yok.
func methodWindowFrom(src, masked string, annoStart, declStart, maxLines int) (MethodWindow, bool) {
	// Bildirimin '{' ya da ';' sonu (parantez derinliği 0'da).
	depth, open := 0, -1
	for i := declStart; i < len(masked); i++ {
		c := masked[i]
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
		} else if depth == 0 && (c == ';' || c == '}') {
			return MethodWindow{}, false
		} else if depth == 0 && c == '{' {
			open = i
			break
		}
	}
	if open < 0 {
		return MethodWindow{}, false
	}
	header := masked[declStart:open]
	if !strings.Contains(header, "(") {
		return MethodWindow{}, false // sınıf / alan bildirimi, metot değil
	}
	closeAt := matchClose(masked, open)
	if closeAt < 0 {
		return MethodWindow{}, false
	}
	// Anotasyon satırlarının üstünde yığılı başka anotasyonlar varsa dahil.
	lines := splitSourceLines(src)
	mlines := splitSourceLines(masked)
	from := lineAt(src, annoStart)
	for from > 1 && strings.HasPrefix(strings.TrimSpace(mlines[from-2]), "@") {
		from--
	}
	to := lineAt(src, closeAt)
	total := to - from + 1
	if maxLines > 0 && total > maxLines {
		to = from + maxLines - 1
	}
	w := CodeWindow{FromLine: from, ToLine: to, Content: numberedLines(lines, from, to)}
	return MethodWindow{Window: w, Body: src[open+1 : closeAt], Lines: total, Header: src[declStart:open], BodyLine: lineAt(src, open)}, true
}

// AnnotatedMethodWindow — SAF, tablo-testli (v0.10.1330): kaynakta
// `SelfServiceNames.<…>.<op>` sabitini ARGÜMANINDA taşıyan anotasyonun
// metodu. Dize/yorum maskeli tarama; gövde parantez-dengeli. maxLines'ı aşan
// metot ilk maxLines satırında kesilir (MethodWindow.Truncated).
func AnnotatedMethodWindow(src, op string, maxLines int) (MethodWindow, bool) {
	if !ValidOperationCode(op) || strings.TrimSpace(src) == "" {
		return MethodWindow{}, false
	}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	masked := maskJava(src)
	re := regexp.MustCompile(`SelfServiceNames(?:\s*\.\s*\w+)*\s*\.\s*` + regexp.QuoteMeta(op) + `\b`)
	for _, m := range re.FindAllStringIndex(masked, -1) {
		at := annotationStartBefore(masked, m[0])
		if at < 0 {
			continue
		}
		// Bu anotasyondan önce aynı bildirimin başka anotasyonları olabilir:
		// pencere satır başından, bildirim anotasyonların arkasından.
		decl := skipAnnotations(masked, at)
		if mw, ok := methodWindowFrom(src, masked, lineStartOff(masked, at), decl, maxLines); ok {
			return mw, true
		}
	}
	return MethodWindow{}, false
}

// calleeRe — gövdedeki ilk yönetici/servis çağrısı: `xxxManager.foo(` /
// `xxxService.foo(` (alan adı küçük harfle; `this.` öneki serbest).
var calleeRe = regexp.MustCompile(`(?:^|[^\w$.])(?:this\s*\.\s*)?([a-z][\w$]*(?:Manager|Service))\s*\.\s*([a-z][\w$]*)\s*\(`)

// ManagerCallee — SAF: gövdenin (dize/yorum maskeli) İLK yönetici/servis
// çağrısı → (alan adı, metot adı); yoksa ok=false.
func ManagerCallee(body string) (receiver, method string, ok bool) {
	m := calleeRe.FindStringSubmatch(maskJava(body))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// FieldType — SAF: aynı dosyadaki alan bildiriminden alanın tipi (paket
// niteleyicisi ve generic'ler soyulmuş). Bulunamazsa alan adının baş harfi
// büyütülmüş hâli (creditProductInformationManager →
// CreditProductInformationManager) — yaygın adlandırma; found=false.
func FieldType(src, field string) (typ string, found bool) {
	masked := maskJava(strings.ReplaceAll(src, "\r\n", "\n"))
	re := regexp.MustCompile(`(?m)(?:^|[;{}])\s*(?:@[\w.]+(?:\([^)]*\))?\s+)*(?:(?:private|protected|public|static|final|transient|volatile)\s+)*([A-Z][\w.$]*)(?:\s*<[^;=(){}]*>)?\s+` + regexp.QuoteMeta(field) + `\s*[;=]`)
	if m := re.FindStringSubmatch(masked); m != nil {
		t := m[1]
		if i := strings.LastIndexByte(t, '.'); i >= 0 {
			t = t[i+1:]
		}
		return t, true
	}
	if field == "" {
		return "", false
	}
	r := []rune(field)
	r[0] = unicode.ToUpper(r[0])
	return string(r), false
}

// CalleeMethodWindow — SAF, tablo-testli: kaynakta `method` adlı GÖVDELİ
// metot bildirimi (arayüz bildirimi `…;` atlanır, çağrılar atlanır). İlk
// gövdeli aşırı yükleme seçilir; üstündeki anotasyonlar pencereye girer.
func CalleeMethodWindow(src, method string, maxLines int) (MethodWindow, bool) {
	if strings.TrimSpace(method) == "" || strings.TrimSpace(src) == "" {
		return MethodWindow{}, false
	}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	masked := maskJava(src)
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(method) + `\s*\(`)
	for _, m := range re.FindAllStringIndex(masked, -1) {
		if m[0] > 0 && masked[m[0]-1] == '.' {
			continue // çağrı (x.method(), değil bildirim
		}
		ls := lineStartOff(masked, m[0])
		le := strings.IndexByte(masked[m[0]:], '\n')
		lineEnd := len(masked)
		if le >= 0 {
			lineEnd = m[0] + le
		}
		line := masked[ls:lineEnd]
		if sigSkipRe.MatchString(line) || !sigDeclRe.MatchString(line) {
			continue
		}
		// Bildirim satırın başından: değiştiriciler + tip + ad. Satırdaki
		// anotasyonlar (aynı satır) atlanır.
		decl := skipAnnotations(masked, ls)
		if decl > m[0] {
			continue
		}
		if mw, ok := methodWindowFrom(src, masked, ls, decl, maxLines); ok {
			return mw, true
		}
	}
	return MethodWindow{}, false
}

// commonDirPrefix — iki yolun ortak dizin parçası sayısı.
func commonDirPrefix(a, b string) int {
	as := strings.Split(strings.ToLower(strings.TrimPrefix(a, "/")), "/")
	bs := strings.Split(strings.ToLower(strings.TrimPrefix(b, "/")), "/")
	n := 0
	for i := 0; i < len(as)-1 && i < len(bs)-1; i++ {
		if as[i] != bs[i] {
			break
		}
		n++
	}
	return n
}

// CalleePathCandidates — SAF: çağrılan tipin ağaçtaki dosyası; önce
// <Tip>Impl.java (gövde orada), sonra <Tip>.java. Test yolları elenir;
// birden çok adayda giriş dosyasına dizince en yakın olan, sonra kısa yol.
func CalleePathCandidates(paths []string, typ, entryPath string) []string {
	var out []string
	for _, name := range []string{typ + "Impl.java", typ + ".java"} {
		allowed, _ := MatchSourcePaths(paths, name)
		var kept []string
		for _, p := range allowed {
			if !isTestPath(strings.ToLower(p)) {
				kept = append(kept, p)
			}
		}
		sort.SliceStable(kept, func(i, j int) bool {
			ci, cj := commonDirPrefix(kept[i], entryPath), commonDirPrefix(kept[j], entryPath)
			if ci != cj {
				return ci > cj
			}
			if len(kept[i]) != len(kept[j]) {
				return len(kept[i]) < len(kept[j])
			}
			return kept[i] < kept[j]
		})
		out = append(out, kept...)
	}
	return out
}

// treePathFor — SAF: arama isabetinin yolu → OKUNAN ağaçtaki yol. Ağaçta
// birebir (harf-duyarsız) yoksa aynı dosya adlı ve son üç parçası tutan tek
// yol; o da yoksa "" (ağaç kesikse çağıran isabet yolunu doğrudan dener).
func treePathFor(paths []string, hit string) string {
	want := "/" + strings.TrimPrefix(strings.TrimSpace(hit), "/")
	lw := strings.ToLower(want)
	for _, p := range paths {
		if strings.ToLower(p) == lw {
			return p
		}
	}
	segs := strings.Split(lw, "/")
	tail := segs
	if len(tail) > 3 {
		tail = tail[len(tail)-3:]
	}
	suffix := "/" + strings.Join(tail, "/")
	var match []string
	for _, p := range paths {
		if strings.HasSuffix(strings.ToLower(p), suffix) {
			match = append(match, p)
		}
	}
	if len(match) == 1 {
		return match[0]
	}
	return ""
}

// ── küçük sonuç cache'i ─────────────────────────────────────────────────

type opCacheEntry struct {
	windows []CodeWindow
	reason  string
	trimmed string
	outcome CodeOutcome
	at      time.Time
}

type opCodeCache struct {
	mu sync.Mutex
	m  map[string]opCacheEntry
}

var operationCodeCaches sync.Map // *Service → *opCodeCache

func (s *Service) opCache() *opCodeCache {
	v, _ := operationCodeCaches.LoadOrStore(s, &opCodeCache{})
	return v.(*opCodeCache)
}

func (c *opCodeCache) get(key string) (opCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Since(e.at) > operationCacheTTL {
		return opCacheEntry{}, false
	}
	e.windows = append([]CodeWindow(nil), e.windows...)
	return e, true
}

func (c *opCodeCache) put(key string, e opCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]opCacheEntry{}
	}
	if len(c.m) >= operationCacheMax {
		oldestKey, oldestAt := "", time.Now()
		for k, v := range c.m {
			if v.at.Before(oldestAt) {
				oldestKey, oldestAt = k, v.at
			}
		}
		if oldestKey != "" {
			delete(c.m, oldestKey)
		}
	}
	e.at = time.Now()
	e.windows = append([]CodeWindow(nil), e.windows...)
	c.m[key] = e
}

// operationCacheKey — proje/depo/ref/operasyon/bütçe (hepsi; v0.5.187 dersi).
func operationCacheKey(cfg Settings, repo string, src RefSpec, op string, budget int) string {
	return fmt.Sprintf("%s|%s|%s|%s@%s|op:%s|b:%d", cfg.BaseURL, cfg.Collection, cfg.Project, repo, refCacheName(src), op, budget)
}

// OperationSearchOffReason — Code Search kapalıyken operatöre giden tek
// yazım (api kapısı da bunu basar).
const OperationSearchOffReason = "bu kayıtta stacktrace yok; operasyon kodu araması için Ayarlar → Kod entegrasyonu → Code Search açık olmalı"

func (s *Service) operationDeadline() time.Duration {
	d := s.fetchDeadline()
	if d > operationDeadlineCap {
		d = operationDeadlineCap
	}
	return d
}

// FetchOperationCode — v0.10.1330: stack yokken trace'in ana operasyonunun
// giriş metodu + ilk çağırdığı yönetici metodu. FAIL-OPEN: hata dönmez,
// boş bağlam + Reason. Source her dalda CodeSourceOperation.
func (s *Service) FetchOperationCode(ctx context.Context, req OperationCodeRequest) CodeContext {
	cc, _ := s.fetchOperationCode(ctx, req)
	return cc
}

// fetchOperationCode — FetchOperationCode'un gövdesi; ikinci dönüş (v0.10.1349)
// = arama ISKALADI (Code Search kaynak giriş dosyası döndürmedi). Aday
// döngüsü (FetchOperationCodeCandidates) yalnız ıskada sonraki adaya geçer.
func (s *Service) fetchOperationCode(ctx context.Context, req OperationCodeRequest) (out CodeContext, searchMiss bool) {
	op := strings.TrimSpace(req.Operation)
	out = CodeContext{Repo: strings.TrimSpace(req.Repo), Source: CodeSourceOperation, OperationOthers: req.Others}
	defer func() {
		out.Source = CodeSourceOperation
		out.OperationOthers = req.Others
		out.Operation, out.OperationSource = op, strings.TrimSpace(req.Source)
	}()
	if s == nil || !s.Configured() {
		out.Outcome, out.Reason = CodeUnconfigured, "kod entegrasyonu yapılandırılmamış (Ayarlar → Kod entegrasyonu)"
		return out, false
	}
	if !s.CodeSearchEnabled() {
		out.Outcome, out.Reason = CodeNoStack, OperationSearchOffReason
		return out, false
	}
	if !ValidOperationCode(op) {
		out.Outcome, out.Reason = CodeNoStack, "bu kayıtta stacktrace yok; operasyon kodu aranabilir biçimde değil"
		return out, false
	}
	if out.Repo == "" {
		out.Outcome, out.Reason = CodeRepoUnresolved, "servis için depo çözülemedi"
		return out, false
	}
	cfg := s.CurrentSettings()
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, s.operationDeadline())
	defer cancel()
	project, _, dead := pickProject(cfg, req.Hint)
	if dead != "" {
		out.Outcome, out.Reason = CodeProjectDeadEnd, dead
		return out, false
	}
	cfg.Project = project
	version := strings.TrimSpace(req.Version)
	note := ""
	if version != "" && !SourceVersionUsable(version) {
		version = ""
	}
	sc := s.sourceChain(ctx, parent, cfg, out.Repo, req.Service, version)
	ch := sc.chain
	out.Repo, out.Branch = ch.repo, ch.branch
	if ch.class != "" {
		out.Outcome, out.Reason = ch.class, ch.reason
		if deadlineHit(parent, ctx) {
			out.Outcome, out.Reason = CodeDeadline, deadlineReason(s.operationDeadline())
		}
		return out, false
	}
	out.BrowseURL = BrowseURL(cfg, ch.repo, ch.branch)
	note = ch.note
	if rev := sc.rev; rev != nil {
		out.Revision = rev
		if !rev.Verified {
			note = withNote(note, revisionFallbackNote(rev, ch.branch))
		}
	}
	budget := cfg.codeBudget()
	if budget > operationBudgetCap {
		budget = operationBudgetCap
	}
	out.Budget = budget
	key := operationCacheKey(cfg, ch.repo, sc.src, op, budget)
	if e, ok := s.opCache().get(key); ok {
		out.Windows, out.Trimmed, out.Outcome = e.windows, e.trimmed, e.outcome
		out.Reason = withNote(note, e.reason)
		out.Stats = FetchStats{Fetched: 0, Resolved: len(e.windows)}
		return out, false
	}
	fail := func(class CodeOutcome, reason string) CodeContext {
		out.Outcome, out.Reason = class, withNote(note, reason)
		if deadlineHit(parent, ctx) {
			out.Outcome, out.Reason = CodeDeadline, withNote(note, deadlineReason(s.operationDeadline()))
		} else if parent.Err() != nil {
			out.Outcome, out.Reason = CodeCancelled, "istek iptal edildi — kod bağlamı toplanamadı"
		}
		return out
	}

	// 1) TEK arama.
	hits, err := s.SearchServiceCode(ctx, cfg.Project, ch.repo, OperationSearchQuery(op), operationSearchTop)
	if err != nil {
		return fail(CodeBackendError, "operasyon kodu araması başarısız: "+sanitize(firstLine(err.Error()), cfg)), false
	}
	ranked := RankOperationHits(hits)
	if len(ranked) == 0 {
		why := "Code Search'te bu operasyonu anan kaynak dosya yok"
		if len(hits) > 0 {
			why = fmt.Sprintf("Code Search %d dosya buldu ama hiçbiri kaynak giriş dosyası değil (sabit sözlüğü / yapılandırma / test)", len(hits))
		}
		// v0.10.1349 — ıska yalnız sınıf hâlâ tree-miss ise (süre/iptal değil).
		r := fail(CodeTreeMiss, why)
		return r, r.Outcome == CodeTreeMiss
	}
	entryPath := ""
	for _, h := range ranked {
		if p := treePathFor(sc.paths, h.Path); p != "" {
			entryPath = p
			break
		}
		if sc.capped {
			entryPath = "/" + strings.TrimPrefix(h.Path, "/")
			break
		}
	}
	if entryPath == "" {
		return fail(CodeTreeMiss, "Code Search isabeti okunan ağaçta yok: "+strings.TrimPrefix(ranked[0].Path, "/")), false
	}

	// 2) Giriş dosyası (çekim 1/2).
	fetched := 0
	body, ferr := sc.fetch(ctx, entryPath)
	fetched++
	if ferr != nil {
		return fail(CodeBackendError, "operasyon dosyası okunamadı: "+sanitize(firstLine(ferr.Error()), cfg)), false
	}
	body, _ = trimTruncatedBody(body, fileBodyCap)
	entry, ok := AnnotatedMethodWindow(body, op, operationEntryMaxLines)
	if !ok {
		return fail(CodeWindowFailed, "operasyon dosyası okundu ama sabiti anotasyonunda taşıyan metot bulunamadı: "+strings.TrimPrefix(entryPath, "/")), false
	}
	ew := entry.Window
	ew.Path, ew.Operation, ew.Frame = entryPath, true, op
	windows := []CodeWindow{ew}
	partial := false
	opNote := "" // operasyona özgü not (cache'e girer; zincir notu girmez)
	var trims []string
	if entry.Truncated() {
		trims = append(trims, fmt.Sprintf("giriş metodu %d satır — ilk %d satır verildi", entry.Lines, operationEntryMaxLines))
	}

	// 3) İlk yönetici çağrısı (çekim 2/2).
	if recv, method, ok := ManagerCallee(entry.Body); ok {
		typ, _ := FieldType(body, recv)
		cands := CalleePathCandidates(sc.paths, typ, entryPath)
		label := typ + "." + method
		switch {
		case len(cands) == 0:
			partial = true
			opNote = withNote(opNote, "çağrılan "+typ+" dosyası okunan ağaçta yok — yalnız giriş metodu verildi")
		default:
			cp := cands[0]
			cbody, cerr := sc.fetch(ctx, cp)
			fetched++
			if cerr != nil {
				partial = true
				opNote = withNote(opNote, "çağrılan "+label+" dosyası okunamadı: "+sanitize(firstLine(cerr.Error()), cfg))
				break
			}
			cbody, _ = trimTruncatedBody(cbody, fileBodyCap)
			callee, cok := CalleeMethodWindow(cbody, method, operationCalleeMaxLines)
			if !cok {
				partial = true
				opNote = withNote(opNote, "çağrılan "+label+" gövdesi "+strings.TrimPrefix(cp, "/")+" içinde bulunamadı")
				break
			}
			cw := callee.Window
			segs := strings.Split(cp, "/")
			cls := strings.TrimSuffix(segs[len(segs)-1], ".java")
			cw.Path, cw.Operation, cw.Callee, cw.Frame = cp, true, true, cls+"."+method
			windows = append(windows, cw)
			if callee.Truncated() {
				trims = append(trims, fmt.Sprintf("çağrılan metot %d satır — ilk %d satır verildi", callee.Lines, operationCalleeMaxLines))
			}
		}
	}

	clamped, cut := ClampCodeWindows(windows, budget)
	if cut {
		dropped := len(windows) - len(clamped)
		t := fmt.Sprintf("kod bütçesi (%d karakter) doldu — pencereler kısaltıldı", budget)
		if dropped > 0 {
			t = fmt.Sprintf("kod bütçesi (%d karakter) doldu — %d pencere düştü", budget, dropped)
		}
		trims = append(trims, t)
	}
	if len(clamped) == 0 {
		return fail(CodeWindowFailed, "operasyon penceresi bütçeye sığmadı"), false
	}
	stampWindowLinksAt(cfg, ch.repo, ch.branch, sc.src, clamped)
	for i := range clamped {
		// Link metodun başına: Line=0 (hata satırı yok) olduğu için damga
		// satırsız; operatör metodun kendisine insin.
		w := &clamped[i]
		w.WebURL = FileURLAt(cfg, w.Project, w.Repo, operationLinkRef(sc.src, ch.branch), w.Path, w.FromLine)
	}
	out.Windows = clamped
	out.Trimmed = strings.Join(trims, "; ")
	out.Outcome = CodeOK
	if partial || out.Trimmed != "" {
		out.Outcome = CodePartial
	}
	opNote = withNote(opNote, out.Trimmed)
	out.Stats = FetchStats{Fetched: fetched, Resolved: len(windows), Candidates: len(ranked)}
	s.opCache().put(key, opCacheEntry{windows: clamped, reason: opNote, trimmed: out.Trimmed, outcome: out.Outcome})
	out.Reason = withNote(note, opNote)
	return out, false
}

// ── v0.10.1349 (operatör-bildirimli): fonksiyon kodu ADAYLARI ─────────────
//
// HATASIZ trace'te ana operasyon kesin kaynaktan bulunamayınca api katmanı
// trace'in FUNCTION_CODE değerlerini sözlükten (1:n) aday operasyonlara
// çevirir; burada adaylar SIRAYLA denenir. Bütçe: dış tavan
// operationDeadline (≤15 sn) TÜM döngü için; en çok
// OperationCandidateMax aday; her aday FetchOperationCode'un kendi bütçesi
// (TEK arama + ≤2 dosya). İlk aramanın İSABET ettiği adayda durulur (isabet
// sonra okunamasa bile — sonraki aday başka bir controller, aynı trace'in
// kanıtı değil). En kötü hâl: 2 arama + 2 dosya.

// OperationCandidateMax — denenecek aday tavanı.
const OperationCandidateMax = 2

// OperationNoneReason — ne kesin operasyon ne aday var: operatöre giden tek
// yazım (api de basar).
const OperationNoneReason = "bu kayıtta stacktrace yok; trace'in operasyon adı bulunamadı"

// OperationCandidatesMissReason — SAF: bütün adayların araması ıskaladı.
func OperationCandidatesMissReason(cands []string) string {
	return "bu kayıtta stacktrace yok; fonksiyon kodu adayları (" + strings.Join(cands, ", ") + ") için depoda controller bulunamadı"
}

// FetchOperationCodeCandidates — adayları sırayla dener (bkz. bölüm başı).
// req.Operation yok sayılır; req.Source boşsa OperationSourceFunctionCode.
// Dönüşte OperationCandidates = denenmeye ALINAN adaylar (≤2, tekil, geçerli).
// FAIL-OPEN: hata dönmez, boş bağlam + Reason.
func (s *Service) FetchOperationCodeCandidates(ctx context.Context, req OperationCodeRequest, candidates []string) CodeContext {
	if strings.TrimSpace(req.Source) == "" {
		req.Source = OperationSourceFunctionCode
	}
	var cands []string
	seen := map[string]bool{}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if !ValidOperationCode(c) || seen[c] {
			continue
		}
		seen[c] = true
		cands = append(cands, c)
		if len(cands) >= OperationCandidateMax {
			break
		}
	}
	stamp := func(cc CodeContext) CodeContext {
		cc.Source, cc.OperationSource, cc.OperationOthers = CodeSourceOperation, req.Source, req.Others
		cc.OperationCandidates = append([]string(nil), cands...)
		return cc
	}
	if len(cands) == 0 {
		return stamp(CodeContext{Repo: strings.TrimSpace(req.Repo), Outcome: CodeNoStack, Reason: OperationNoneReason})
	}
	if s == nil || !s.Configured() || !s.CodeSearchEnabled() {
		// Kapı cevapları FetchOperationCode'unkiyle aynı (ağ isteği yok).
		r := req
		r.Operation = cands[0]
		cc := s.FetchOperationCode(ctx, r)
		cc.Operation = ""
		return stamp(cc)
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, s.operationDeadline())
	defer cancel()
	var last CodeContext
	for _, c := range cands {
		r := req
		r.Operation = c
		cc, miss := s.fetchOperationCode(ctx, r)
		if !miss {
			if deadlineHit(parent, ctx) && cc.Empty() {
				cc.Outcome, cc.Reason = CodeDeadline, deadlineReason(s.operationDeadline())
			}
			return stamp(cc)
		}
		last = cc
		if ctx.Err() != nil {
			break
		}
	}
	last.Operation = ""
	last.Reason = OperationCandidatesMissReason(cands)
	if deadlineHit(parent, ctx) {
		last.Outcome, last.Reason = CodeDeadline, deadlineReason(s.operationDeadline())
	}
	return stamp(last)
}

// operationLinkRef — pencere linkinin ref'i: commit okunduysa commit, değilse dal.
func operationLinkRef(src RefSpec, branch string) RefSpec {
	if src.Kind == "commit" {
		return src
	}
	return RefSpec{Kind: "branch", Name: branch}
}
