package thanos

import (
	"strconv"
	"strings"
)

// cluster_matcher.go — CLUSTER MATCHER ENJEKSİYONU (v0.10.128, K8s entity
// katmanı adım 2; docs/plans/entity-layer-design-2026-08-28.md §1.1).
//
// ── NEDEN ────────────────────────────────────────────────────────────────
//
// Keşif raporu engel #1: kod hiçbir cluster etiketi okumuyordu — satırlara
// cluster adı Go'da konfigden damgalanıyordu (`row.Cluster = c.Name`).
// "Cluster başına ayrı Thanos URL'i" modelinde doğru; TEK Thanos Querier'ın
// önünde N cluster varken her sorgu BÜTÜN cluster'ların serilerini
// döndürür ve pod/node/deployment tabloları cluster'ları karıştırır.
//
// ── NASIL ────────────────────────────────────────────────────────────────
//
// Şablon başına elle matcher eklemek 45+ şablonda 45 fırsat demekti (ve
// yeni şablon eklenirken unutulurdu). Bunun yerine İFADE düzeyinde
// enjeksiyon, doQuery'de: her vektör seçicisine `<label>="<value>"`
// eklenir — süslü parantezli seçicide `{`'dan hemen sonra, çıplak metrik
// adından sonra `{…}` olarak. Fonksiyon/agregat adları, by/without/on/
// ignoring/group_* etiket listeleri, anahtar kelimeler, dize sabitleri,
// süreler ve sayılar metrik SANILMAZ. Her şablon tablo-testli
// (cluster_matcher_test.go: enjeksiyon sonrası çıplak metrik 0, her
// seçicide matcher).
//
// Bağımlılık kararı: Prometheus'un promql/parser paketi bunu "doğru"
// yapardı ama ağır bir modül grafiği taşır; yeni bağımlılık gerekçe +
// onay ister. Bu tokenizer PromQL'in seçici gramerini kapsar; subquery,
// @, offset, UTF-8 tırnaklı adlar ve yorumlar v0.10.950'den beri golden
// testli (TestWithClusterMatcherConsoleGolden).
//
// Etiket adı BOŞ = enjeksiyon YOK = eski davranış (cluster başına URL).
//
// ── v0.10.950 — KONSOL SERTLEŞTİRMESİ (PromQL konsolu, spec karar 6) ─────
//
// Artık ham KULLANICI PromQL'i de bu tokenizer'dan geçiyor (console.go);
// şablon-dostu varsayımlar yetmez. Phase 0 audit §2 (docs/promql-console/
// audit.md): `#` yorumu hiç tanınmıyordu — yorumdaki bir kesme işareti
// (`# don't`) dize başlangıcı sanılıyor, sorgunun GERİ KALANI enjeksiyonsuz
// kopyalanıyordu. Paylaşımlı querier'da bu, başka cluster'ın verisi demek.
// Dört düzeltme:
//
//  1. Yorumlar enjeksiyondan ÖNCE ayrı, dize-bilinçli bir lexer geçişiyle
//     SİLİNİR (stripComments) — asla reddedilmez. Kural Prometheus
//     lexer'ınınki: dize dışındaki `#` satır sonuna (\n YA DA \r) kadar
//     yorumdur. Kendi satırındaki yorum satırıyla gider; effectiveQuery
//     okunaklı kalır.
//  2. Backtick dizesi HAMDIR: `\` kaçış değildir (Prometheus lexRawString).
//     Eski skipString backtick'te de `\`'i kaçış sayıyordu → `\` ile biten
//     ham dize kapanmamış sanılıp sorgunun kalanı enjeksiyonsuz
//     kopyalanıyordu (ikinci bypass, aynı sınıf).
//  3. Operand/operatör KONUMU izlenir. Prometheus grameri anahtar
//     kelimeleri (offset, sum, and, by, start…) METRİK ADI olarak da kabul
//     eder (metric_identifier); eski kural "anahtar kelime asla metrik
//     değil"di → `offset` adlı bir metrik çıplak sorgulanınca matcher'sız
//     giderdi. Yeni kural BAŞARISIZ-KAPALI: operand konumundaki her çıplak
//     ad metriktir; istisnalar dar ve gramerden gelir — çağrı `f(`, önek
//     gruplamalı agregat `sum by (…)`, inf/nan, bool/group_left/group_right;
//     operatör konumunda and/or/unless/atan2 ve offset/anchored/smoothed.
//     Geçersiz PromQL'e fazladan matcher eklemek zararsızdır (Prometheus
//     yine reddeder); eksik matcher veri sızdırır.
//  4. Etiket adı eski Prometheus sözdizimine uymuyorsa tırnaklanır
//     (`{"ad"="değer"}`, Prometheus 3 UTF-8) ve değerdeki satır sonu
//     kaçışlanır — elle bozulmuş bir ayar blobu matcher metnine kod sokamaz
//     (PUT zaten ValidThanosLabelName ile doğrular; bu derinlemesine
//     savunma).
//
// Kaynak haritası (rewriter.pos): çıktı baytı → girdi ofseti. Yalnız hata
// konumu çevirisinde açılır (originalPosition): Thanos'un "1:25: parse
// error" konumu ETKİN sorguyu anlatır, editör ise kullanıcının yazdığının
// altını çizer. /clusters sıcak yolunda (withClusterMatcher) harita yok,
// ek maliyet yok.

// promqlAggregateOps — önek gruplama (`sum by (…) (…)`) alabilen agregatlar.
var promqlAggregateOps = map[string]bool{
	"sum": true, "min": true, "max": true, "avg": true, "group": true,
	"stddev": true, "stdvar": true, "count": true, "count_values": true,
	"bottomk": true, "topk": true, "quantile": true, "limitk": true, "limit_ratio": true,
}

// promqlBinaryWords — OPERATÖR konumunda ikili operatör olan kelimeler.
// Operand konumunda aynı kelime metrik adıdır (and/or/unless gramerde
// metric_identifier; atan2 değil ama başarısız-kapalı: Prometheus reddeder).
var promqlBinaryWords = map[string]bool{"and": true, "or": true, "unless": true, "atan2": true}

// promqlPostfixModifiers — OPERATÖR konumunda seçici/subquery değiştiricisi
// (anchored/smoothed: Prometheus 3.x deneysel aralık değiştiricileri).
var promqlPostfixModifiers = map[string]bool{"offset": true, "anchored": true, "smoothed": true}

// labelListKeywords — ardından gelen parantezli liste ETİKET listesidir.
var labelListKeywords = map[string]bool{
	"by": true, "without": true, "on": true, "ignoring": true,
	"group_left": true, "group_right": true,
}

func isIdentStart(b byte) bool {
	return b == '_' || b == ':' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isIdentByte(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func isQuote(b byte) bool { return b == '"' || b == '\'' || b == '`' }

// isPromSpace — Prometheus lexer'ının boşluk kümesi (isSpace); \v/\f gibi
// diğerleri Prometheus'ta hata, burada sıradan bayt.
func isPromSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// stringEnd — s[i] bir tırnak; kapanıştan SONRAKİ indeks ve kapandı mı.
// "…" ve '…' içinde `\` sonraki baytı kaçışlar; `…` HAM dizedir, kaçış
// yoktur (v0.10.950 — düzeltme 2). Kapanmayan dize girdinin sonuna uzanır;
// Prometheus onu zaten reddeder.
func stringEnd(s string, i int) (int, bool) {
	q := s[i]
	i++
	for i < len(s) {
		switch {
		case s[i] == '\\' && q != '`':
			i += 2
			continue
		case s[i] == q:
			return i + 1, true
		}
		i++
	}
	return len(s), false
}

// nextNonSpace — boşlukları atlayıp ilk karakterin indeksini verir.
func nextNonSpace(s string, i int) int {
	for i < len(s) && isPromSpace(s[i]) {
		i++
	}
	return i
}

// identAt — s[i:]'deki tanımlayıcı (yoksa "").
func identAt(s string, i int) string {
	if i >= len(s) || !isIdentStart(s[i]) {
		return ""
	}
	j := i
	for j < len(s) && isIdentByte(s[j]) {
		j++
	}
	return s[i:j]
}

// isGroupingWord — agregat gruplama kelimesi (by/without; büyük-küçük
// harf duyarsız, Prometheus lexer'ı gibi).
func isGroupingWord(s string) bool {
	s = strings.ToLower(s)
	return s == "by" || s == "without"
}

var matcherValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)

// escapeMatcherValue — etiket değeri PromQL dize sabiti olarak.
func escapeMatcherValue(v string) string { return matcherValueEscaper.Replace(v) }

// isLegacyLabelName — Prometheus eski etiket adı sözdizimi
// ([a-zA-Z_][a-zA-Z0-9_]*); promLabelNameRe'nin regex'siz eşi (sıcak yol).
func isLegacyLabelName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// matcherText — `label="value"`; eski sözdizimine uymayan ad tırnaklanır
// (v0.10.950 — düzeltme 4).
func matcherText(label, value string) string {
	name := label
	if !isLegacyLabelName(label) {
		name = `"` + escapeMatcherValue(label) + `"`
	}
	return name + `="` + escapeMatcherValue(value) + `"`
}

// rewriter — çıktı + (track ise) kaynak haritası: pos[k] = çıktının k.
// baytının GİRDİDEKİ ofseti; -1 = sentetik (enjekte edilen metin).
type rewriter struct {
	buf   []byte
	track bool
	pos   []int
}

// src — girdinin [from,to) aralığını kopyalar.
func (w *rewriter) src(s string, from, to int) {
	w.buf = append(w.buf, s[from:to]...)
	if w.track {
		for k := from; k < to; k++ {
			w.pos = append(w.pos, k)
		}
	}
}

// lit — sentetik metin ekler.
func (w *rewriter) lit(s string) {
	w.buf = append(w.buf, s...)
	if w.track {
		for range len(s) {
			w.pos = append(w.pos, -1)
		}
	}
}

// trimRight — sondaki cut baytlarını geri alır.
func (w *rewriter) trimRight(cut string) {
	for len(w.buf) > 0 && strings.IndexByte(cut, w.buf[len(w.buf)-1]) >= 0 {
		w.buf = w.buf[:len(w.buf)-1]
		if w.track {
			w.pos = w.pos[:len(w.pos)-1]
		}
	}
}

// stripComments — dize DIŞINDAKİ `#` yorumlarını siler (v0.10.950 —
// düzeltme 1). Yorumdan önceki satır içi boşluk da gider; kendi satırındaki
// yorum satır sonuyla birlikte gider; yorum silindiyse sondaki boşluk
// kırpılır. `#` yoksa (ya da yalnız dize içindeyse) girdi AYNEN ve harita
// nil (= özdeşlik) döner.
func stripComments(expr string, track bool) (string, []int) {
	if strings.IndexByte(expr, '#') < 0 {
		return expr, nil
	}
	w := rewriter{buf: make([]byte, 0, len(expr)), track: track}
	stripped, openString := false, false
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case isQuote(c):
			j, closed := stringEnd(expr, i)
			w.src(expr, i, j)
			openString = !closed
			i = j
		case c == '#':
			stripped = true
			// Dize içinde olamayız (dizeler bütün atlanır) → buf'ın
			// sonundaki boşluk ve satır sonu da dize dışıdır.
			w.trimRight(" \t")
			j := i
			for j < len(expr) && expr[j] != '\n' && expr[j] != '\r' {
				j++
			}
			if n := len(w.buf); n == 0 || w.buf[n-1] == '\n' || w.buf[n-1] == '\r' {
				if j < len(expr) && expr[j] == '\r' {
					j++
				}
				if j < len(expr) && expr[j] == '\n' {
					j++
				}
			}
			i = j
		default:
			j := i + 1
			for j < len(expr) && expr[j] != '#' && !isQuote(expr[j]) {
				j++
			}
			w.src(expr, i, j)
			i = j
		}
	}
	if !stripped {
		return expr, nil
	}
	if !openString {
		w.trimRight(" \t\r\n")
	}
	return string(w.buf), w.pos
}

// injectBraces — src[i]=='{'; kapanışa kadar (dizeler atlanarak) kopyalar,
// matcher'ı başa koyar: `{}` → `{m}`, `{a="b"}` → `{m,a="b"}`. Kapanış
// yoksa sentetik `}` EKLENMEZ: geçersiz girdi geçersiz kalır (Prometheus
// reddeder), enjeksiyonla "onarılmaz". Sonraki indeksi döndürür.
func injectBraces(w *rewriter, src string, i int, m string) int {
	k := i + 1
	for k < len(src) && src[k] != '}' {
		if isQuote(src[k]) {
			k, _ = stringEnd(src, k)
			continue
		}
		k++
	}
	a, b := i+1, k
	for a < b && isPromSpace(src[a]) {
		a++
	}
	for b > a && isPromSpace(src[b-1]) {
		b--
	}
	w.src(src, i, i+1)
	w.lit(m)
	if a < b {
		w.lit(",")
		w.src(src, a, b)
	}
	if k < len(src) {
		w.src(src, k, k+1)
		return k + 1
	}
	return k
}

// injectMatcher — withClusterMatcher'ın çekirdeği: m HAZIR matcher metni
// (`label="value"`). Önce yorumlar silinir, sonra her vektör seçicisine m
// eklenir. track=true ise dönen harita çıktı baytlarını ORİJİNAL expr
// ofsetlerine bağlar (yorum silme + enjeksiyon birleşik).
func injectMatcher(expr, m string, track bool) (string, []int) {
	src, smap := stripComments(expr, track)
	w := rewriter{buf: make([]byte, 0, len(src)+32), track: track}
	operand := true          // sıradaki token OPERAND konumunda mı (seçici/sayı/çağrı)
	listDepth := 0           // by/without/on/ignoring/group_* etiket listesi derinliği
	listThenOperand := false // liste kapanınca operand mı (on/ignoring/group_*) operatör mü (by/without)
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case isQuote(c):
			j, _ := stringEnd(src, i)
			w.src(src, i, j)
			i = j
			if listDepth == 0 {
				operand = false
			}
		case c == '{':
			i = injectBraces(&w, src, i, m)
			operand = false
		case c == '[':
			// süre penceresi / subquery: [5m], [1h:5m], [30m:]
			j := strings.IndexByte(src[i:], ']')
			if j < 0 {
				w.src(src, i, len(src))
				i = len(src)
				break
			}
			w.src(src, i, i+j+1)
			i += j + 1
			operand = false
		case c == '(':
			if listDepth > 0 {
				listDepth++
			}
			operand = true
			w.src(src, i, i+1)
			i++
		case c == ')':
			if listDepth > 0 {
				listDepth--
				if listDepth == 0 {
					operand = listThenOperand
				}
			} else {
				operand = false
			}
			w.src(src, i, i+1)
			i++
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentByte(src[j]) {
				j++
			}
			lower := strings.ToLower(src[i:j])
			n := nextNonSpace(src, j)
			var next byte
			if n < len(src) {
				next = src[n]
			}
			w.src(src, i, j)
			i = j
			switch {
			case listDepth > 0:
				// etiket listesindeki ad: metrik değil
			case labelListKeywords[lower] && next == '(':
				listDepth = 1
				listThenOperand = lower != "by" && lower != "without"
				w.src(src, j, n+1)
				i = n + 1
			case next == '(' || next == '{':
				// fonksiyon/agregat çağrısı ya da süslü seçicinin adı —
				// '(' / '{' dalları konumu kurar, '{' dalı enjekte eder
			case lower == "inf" || lower == "nan":
				operand = false
			case lower == "bool" || lower == "group_left" || lower == "group_right":
				// değiştirici; ardından operand gelir
				operand = true
			case operand && promqlAggregateOps[lower] && isGroupingWord(identAt(src, n)):
				// önek gruplama: `sum by (…) (…)` / `topk without (…) (…)`
			case !operand && promqlBinaryWords[lower]:
				operand = true
			case !operand && promqlPostfixModifiers[lower]:
				// offset/anchored/smoothed — operatör konumu sürer
			default:
				// çıplak metrik adı (operand konumundaki anahtar kelime dahil)
				w.lit("{" + m + "}")
				operand = false
			}
		case c >= '0' && c <= '9' || (c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9'):
			// sayı / süre sabiti: 5m, 1e3, 0x1f, 0.5, .5
			j := i + 1
			for j < len(src) && (isIdentByte(src[j]) || src[j] == '.') {
				j++
			}
			w.src(src, i, j)
			i = j
			operand = false
		default:
			switch c {
			case '+', '-', '*', '/', '%', '^', '=', '!', '<', '>', ',', '@':
				if listDepth == 0 {
					operand = true
				}
			}
			w.src(src, i, i+1)
			i++
		}
	}
	out := string(w.buf)
	if !track {
		return out, nil
	}
	if smap != nil {
		for k, p := range w.pos {
			if p >= 0 {
				w.pos[k] = smap[p]
			}
		}
	}
	return out, w.pos
}

// withClusterMatcher — expr'deki her vektör seçicisine label="value" ekler.
// label boşsa expr AYNEN döner (yorumlar dahil). Saf; golden + her-şablon
// testli.
func withClusterMatcher(expr, label, value string) string {
	if label == "" {
		return expr
	}
	out, _ := injectMatcher(expr, matcherText(label, value), false)
	return out
}

// bareSelectorCount — ifadede matcher'sız (süslü parantezsiz) metrik adı
// sayısı. Testin "enjeksiyon tam mı" kapısı; withClusterMatcher ile aynı
// tokenizer kurallarını kullanır (işaretçi ham matcher metni olarak verilir
// — geçersiz etiket adı sayılıp tırnaklanmasın).
func bareSelectorCount(expr string) int {
	const marker = "\x00BARE\x00"
	rewritten, _ := injectMatcher(expr, marker, false)
	return strings.Count(rewritten, "{"+marker+"}")
}

// EffectiveQuery — v0.10.950: Thanos'a GERÇEKTEN giden ifade (konsol
// meta.effectiveQuery; UI tam olarak koşanı gösterir). Paylaşımlı querier
// (ThanosLabelName dolu): yorumlar silinmiş + her seçicide cluster
// matcher'ı. Cluster başına URL modelinde q AYNEN döner.
func (c ClusterConfig) EffectiveQuery(q string) string {
	label, value := c.EffectiveThanosLabel()
	return withClusterMatcher(q, label, value)
}

// EffectiveMatchers — v0.10.950: labels / label values / series API'lerinin
// match[] listesi. Boş girdiler atılır. Paylaşımlı querier'da her seçiciye
// AYNI matcher enjekte edilir; çağıran hiç seçici göndermediyse
// `{<label>="<value>"}` sentezlenir — aksi hâlde otomatik tamamlama BÜTÜN
// cluster'ların etiketlerini döndürürdü (audit §2). Cluster başına URL
// modelinde liste aynen (boşlar hariç) döner; dönüş asla nil değildir.
func (c ClusterConfig) EffectiveMatchers(match []string) []string {
	label, value := c.EffectiveThanosLabel()
	out := make([]string, 0, len(match)+1)
	for _, m := range match {
		if strings.TrimSpace(m) == "" {
			continue
		}
		out = append(out, withClusterMatcher(m, label, value))
	}
	if len(out) == 0 && label != "" {
		out = append(out, "{"+matcherText(label, value)+"}")
	}
	return out
}

// originalPosition — ETKİN sorgudaki (Thanos'a giden) 1-tabanlı
// satır:sütun konumunu kullanıcının yazdığı expr'deki konuma çevirir.
// Prometheus kuralı (ParseErr.Error): satır '\n' sayılarak, sütun satır
// başından BAYT olarak, ikisi de 1-tabanlı. Sentetik bayta (enjekte
// matcher) düşen konum sonraki kaynak baytına kayar; etkin sorgunun
// sonundaki konum son kaynak baytının hemen ardına. ok=false → konum etkin
// sorgunun dışında, çeviri yok.
func originalPosition(expr, label, value string, line, col int) (int, int, bool) {
	if label == "" {
		return line, col, true
	}
	eff, pos := injectMatcher(expr, matcherText(label, value), true)
	off, ok := offsetAt(eff, line, col)
	if !ok {
		return 0, 0, false
	}
	src := -1
	for k := off; k < len(pos); k++ {
		if pos[k] >= 0 {
			src = pos[k]
			break
		}
	}
	if src < 0 {
		src = 0
		for k := len(pos) - 1; k >= 0; k-- {
			if pos[k] >= 0 {
				src = pos[k] + 1
				break
			}
		}
	}
	l, c := lineColAt(expr, src)
	return l, c, true
}

// offsetAt — 1-tabanlı satır:sütun → bayt ofseti (q uzunluğuna kadar).
func offsetAt(q string, line, col int) (int, bool) {
	if line < 1 || col < 1 {
		return 0, false
	}
	start := 0
	for l := 1; l < line; l++ {
		k := strings.IndexByte(q[start:], '\n')
		if k < 0 {
			return 0, false
		}
		start += k + 1
	}
	off := start + col - 1
	if off > len(q) {
		return 0, false
	}
	return off, true
}

// lineColAt — bayt ofseti → 1-tabanlı satır:sütun (Prometheus ParseErr).
func lineColAt(q string, off int) (int, int) {
	if off > len(q) {
		off = len(q)
	}
	line := 1 + strings.Count(q[:off], "\n")
	return line, off - strings.LastIndexByte(q[:off], '\n')
}

// parseLineCol — "satır:sütun" → (satır, sütun).
func parseLineCol(p string) (int, int, bool) {
	a, b, ok := strings.Cut(p, ":")
	if !ok {
		return 0, 0, false
	}
	l, err1 := strconv.Atoi(a)
	c, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || l < 1 || c < 1 {
		return 0, 0, false
	}
	return l, c, true
}
