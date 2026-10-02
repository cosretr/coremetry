package templater

import "strings"

// v0.10.1030 — "aynı şablon ailesi" yüklemi.
//
// Operatör (prod, ekran görüntüsüyle): "Çok fazla problem geliyor." Problems
// sekmesi `log_template_new` satırlarıyla doluyordu — TEK servis için on ya da
// daha fazla satır, hepsi aynı saniyede doğmuş, hepsi "peak 0.0x · now 0.0x ·
// no signal", desenleri aynı uzun önekle başlıyor (yapısal JSON satırları:
// `{"Timestamp":"<*>","Level":"Warning","MessageTemplate":"U…`).
//
// Kök neden: puller her tikte ~1000 satırlık ÖRNEKTEN ağacı soğuk kurar
// (puller.go → Reset); bir kümenin kimliği şablon belirteçlerinin sha1'i ve
// her inceltmede YENİDEN hesaplanır (drain.go Add → clusterID). Aynı satır
// ailesi her tikte hangi satırlar örneklendiyse ona göre farklı bir inceltme
// düzeyinde (farklı `<*>` konumları) biter → farklı kimlik → defterde
// first_seen = şimdi olan yeni bir satır → "yeni şablon" anomalisi. Yani
// kimlik eşitliği "yeni" için kararsız bir ölçüt.
//
// Bu yüklem soruyu ağacın KENDİ kuralıyla sorar: "Drain bu iki şablonu aynı
// kümeye koyar mıydı?" Yeni bir sezgi değil — Add'in üç kapısı:
//
//  1. Katman 1: belirteç sayısı aynı (tokenCountKey 50 üstünü tek kovaya
//     toplar ama similarity farklı boyda 0 döner; etkin kural EŞİT sayı).
//  2. Katman 2..Depth-1: yönlendirme öneki (ilk Depth-1 belirteç) uyumlu.
//  3. Yaprak: similarity(...) >= SimThreshold (varsayılan 0.4).
//
// Yaprakta (kapı 3) iki girdi de şablon olduğu için `<*>` İKİ TARAFTA da
// eşleşir (Drain yalnız şablon tarafındaki `<*>`'ı sayar, çünkü öbür taraf
// ham satır).
//
// Yönlendirmede (kapı 2) ise Drain değişmez bir belirteci KENDİ çocuğuna
// gönderir; "*" çocuğuna yalnız maskelenmiş belirteç (`<*>`) ya da
// MaxChildren taşması düşer. Yani bir yönlendirme konumunda `<*>` ↔ değişmez
// karşılaşması Drain'de yalnız taşmayla olur ve taşma ortak bir ebeveyn
// yolunun altında yaşanır. Kural (v0.10.1030 gözden geçirme düzeltmesi):
// yönlendirme konumlarında ya HER konum birebir aynıdır (`<*>` == `<*>` dahil
// — ikisi de "*" çocuğu), ya da `<*>` ↔ değişmez karşılaşmasına yalnız iki
// şablon en az bir yönlendirme konumunda AYNI DEĞİŞMEZ belirteci paylaşıyorsa
// izin verilir (taşma örneği: `User <*> logged …` ↔ `User alice logged …`).
// Böylece yönlendirme öneki tamamen `<*>` olan bir şablon aynı boydaki her
// şablonla eşleşemez (`<*> <*>` ↔ `Shutting down` aile DEĞİL).

// templateWildcard — Drain'in değişken konum işareti (drain.go'da düz
// literal olarak geçer; burada adlandırıldı).
const templateWildcard = "<*>"

// ParsedTemplate — v0.10.1030: bir kez belirteçlerine ayrılmış saklanmış
// şablon. Dedektör tik başına her şablonu BİR kez ayırır (aday × bilinen
// çiftlerinde yeniden ayırma yok); belirteç sayısı SQL ön süzgecine de girer.
type ParsedTemplate struct {
	tokens []string
}

// ParseTemplate — saklanmış şablon dizgesini (chstore.LogTemplate.Template =
// Cluster.TemplateString()) belirteçlerine ayırır. Yeniden MASKELEMEZ.
func ParseTemplate(s string) ParsedTemplate {
	return ParsedTemplate{tokens: splitTemplate(s)}
}

// TokenCount — Drain katman 1'in anahtarı olan belirteç sayısı.
func (p ParsedTemplate) TokenCount() int { return len(p.tokens) }

// SameFamily — SameTemplateFamily'nin önceden ayrılmış hâli (aynı kural).
func (p ParsedTemplate) SameFamily(q ParsedTemplate) bool {
	return sameFamilyTokens(p.tokens, q.tokens, defaultDepth, defaultSimThreshold)
}

// SameTemplateFamily — v0.10.1030: iki SAKLANMIŞ şablon dizgesini
// (belirteçler tek boşlukla birleşik) Drain'in varsayılan ayarlarıyla
// (NewDrain) aynı kümeye düşer mi diye yargılar. SAF; simetrik; boş/yalnız-
// boşluk girdi → false (Add sıfır belirteçli satırı hiç kümelemez).
func SameTemplateFamily(a, b string) bool {
	// Ucuz ön eleme, ayırmasız: farklı belirteç sayısı Drain'de asla aynı
	// küme değildir.
	n := countTemplateTokens(a)
	if n == 0 || n != countTemplateTokens(b) {
		return false
	}
	return sameFamilyTokens(splitTemplate(a), splitTemplate(b), defaultDepth, defaultSimThreshold)
}

func sameFamilyTokens(ta, tb []string, depth int, simThreshold float64) bool {
	n := len(ta)
	if n == 0 || n != len(tb) {
		return false
	}
	if !routingCompatible(ta, tb, routePrefixLen(depth, n)) {
		return false
	}
	// Mevcut similarity'yi AYNEN kullan (çatallama yok): iki taraftan
	// birinde `<*>` olan konumu birleşik "şablon"da `<*>` yap; similarity
	// şablon tarafındaki `<*>`'ı eşleşme sayar, kalan konumlar düz eşitlik.
	merged := make([]string, n)
	for i := range ta {
		if ta[i] == templateWildcard || tb[i] == templateWildcard {
			merged[i] = templateWildcard
		} else {
			merged[i] = ta[i]
		}
	}
	return similarity(merged, tb) >= simThreshold
}

// routingCompatible — ilk r yönlendirme konumu için başlık yorumundaki
// kural: iki farklı değişmez → asla; `<*>` ↔ değişmez → yalnız ortak bir
// değişmez yönlendirme belirteci varsa (taşma); aksi hâlde birebir eşitlik.
func routingCompatible(ta, tb []string, r int) bool {
	mixed, sharedLiteral := false, false
	for i := 0; i < r; i++ {
		a, b := ta[i], tb[i]
		switch {
		case a == b:
			if a != templateWildcard {
				sharedLiteral = true
			}
		case a == templateWildcard || b == templateWildcard:
			mixed = true
		default:
			return false
		}
	}
	return !mixed || sharedLiteral
}

// routePrefixLen — Add'in yönlendirme katmanlarının boyu: ilk Depth-1
// belirteç (satır daha kısaysa satır boyu). Add ve sameFamilyTokens ikisi
// de BUNU okur.
func routePrefixLen(depth, n int) int {
	r := depth - 1
	if r > n {
		r = n
	}
	if r < 0 {
		r = 0
	}
	return r
}

// isTemplateSep — Tokenize'ın ayırıcıları: YALNIZ boşluk ve sekme. "\n"
// ayırıcı DEĞİL (Tokenize onu belirtecin içinde bırakır); strings.Fields
// kullanmak çok satırlı belirteçleri bölüp sayıyı kaydırırdı.
func isTemplateSep(r rune) bool { return r == ' ' || r == '\t' }

// splitTemplate — saklanmış şablonu belirteçlerine geri ayırır: puller
// `strings.Join(c.Template, " ")` yazar ve belirteçlerde boşluk/sekme
// bulunamaz (Tokenize onlarda böler), yani bu TemplateString'in tam tersi.
// Yeniden MASKELEMEZ: belirteçler zaten maskelenmiş.
func splitTemplate(s string) []string {
	return strings.FieldsFunc(s, isTemplateSep)
}

// countTemplateTokens — splitTemplate(s) boyunu ayırma yapmadan sayar.
// Bayt bazlı yürüyüş güvenli: ' ' ve '\t' ASCII, UTF-8 çok baytlı
// dizilerde bu baytlar geçmez.
func countTemplateTokens(s string) int {
	n, in := 0, false
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			in = false
			continue
		}
		if !in {
			n++
			in = true
		}
	}
	return n
}
