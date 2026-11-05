package logql

// lucene.go — v0.10.1819 (Log Search Faz 1 dilim 1.3; audit §3.3 seçenek B).
//
// Arayüz "KQL" diyor, ES Lucene `query_string` çalıştırıyor. Bu dosya ikisini
// tek gramerde buluşturur — ES DSL derleyicisi YAZILMAZ (mapping bilgisi
// gerekmez, analiz ES'te kalır):
//
//   - Sorgu logql'de ayrışıyorsa ES'e KANONİK Lucene gider. Kanonik = yapı
//     ve yapraklar KAYNAKTAKİ GİBİ (tırnak, kaçış, joker, parantez bayt bayt);
//     yalnız Lucene'in farklı okuyacağı KQL yazımları düzeltilir:
//       · küçük harf `and` / `or` / `not` / `to` → büyük harf (Lucene küçük
//         harfi TERİM sayar; operatör kararı: açık soru §9-7 = evet),
//       · `alan: değer` → `alan:değer`,
//       · `alan >= 5` → `alan:>=5`.
//     Bilinçli olarak AST'den yeniden YAZILMAZ: String() örtük AND'leri ve
//     öncelik parantezlerini açık yazar, Lucene'in klasik ayrıştırıcısı ise
//     karışık AND/OR'u öncelikle değil "zorunlu/isteğe bağlı" işaretleriyle
//     okur — AST'den yazmak, bugün çalışan bir sorgunun ES anlamını
//     sessizce değiştirirdi. Eski sorgu = aynı ES metni (altın test),
//     tek istisna küçük harf operatörler.
//   - Lucene'e özgü sözdizimi (fuzzy `~`, boost `^`, regex `/…/`, zorunlu
//     `+`) görülürse ya da sorgu logql'de ayrışmıyor ama yapısal olarak
//     sağlamsa → "Lucene" kipi: metin AYNEN gider (bugünkü davranış).
//   - Yapısal kırık (kapanmamış tırnak/parantez, asılı operatör, boş
//     parantez) ES'e GİTMEDEN yakalanır: konum + düz Türkçe cümle.
//
// CH backend'i Lucene çalıştıramaz; onun yolu Check'tir (logql ayrıştırması,
// kırıksa aynı dostça cümle).

import (
	"strings"
)

// Lang — sorgunun ES'te hangi dilde çalıştırıldığı (arayüz rozeti).
type Lang string

const (
	LangKQL    Lang = "kql"
	LangLucene Lang = "lucene"
)

// Translation — ES'e gidecek query_string metni + dil.
type Translation struct {
	Query string
	Lang  Lang
}

// ToES — sorgu metni → ES query_string metni. forceLucene (URL `lang=lucene`)
// metni olduğu gibi geçirir (yapısal kontrol de yok: operatör ham Lucene
// istedi, son söz ES'te). Hata yalnız yapısal kırıkta döner.
func ToES(q string, forceLucene bool) (Translation, *ParseError) {
	if strings.TrimSpace(q) == "" {
		return Translation{Query: q, Lang: LangKQL}, nil
	}
	if forceLucene {
		return Translation{Query: q, Lang: LangLucene}, nil
	}
	if _, err := Parse(q); err != nil {
		if pe := Lint(q); pe != nil {
			return Translation{}, pe
		}
		return Translation{Query: q, Lang: LangLucene}, nil
	}
	if LuceneOnly(q) {
		return Translation{Query: q, Lang: LangLucene}, nil
	}
	return Translation{Query: rewriteKQL(q), Lang: LangKQL}, nil
}

// Check — CH yolu: ayrışmıyorsa konumlu hata; yapısal kırıksa dostça cümle
// (Lint), değilse ayrıştırıcının kendi mesajı. Boş sorgu nil.
func Check(q string) *ParseError {
	if strings.TrimSpace(q) == "" {
		return nil
	}
	_, err := Parse(q)
	if err == nil {
		return nil
	}
	if pe := Lint(q); pe != nil {
		return pe
	}
	if pe, ok := err.(*ParseError); ok {
		return pe
	}
	return &ParseError{Pos: 0, Msg: err.Error()}
}

// rewriteKQL — ayrışan sorgunun kaynak metnini token token kopyalar, yalnız
// Lucene'in farklı okuyacağı KQL yazımlarını düzeltir (dosya başı).
func rewriteKQL(q string) string {
	toks, err := lex(q)
	if err != nil {
		return q
	}
	var b strings.Builder
	last := 0
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == tEOF {
			break
		}
		switch {
		case (t.kind == tAnd || t.kind == tOr || t.kind == tNot || t.kind == tTo) && isWordOp(t.text) && t.text != strings.ToUpper(t.text):
			b.WriteString(q[last:t.pos])
			b.WriteString(strings.ToUpper(t.text))
			last = t.end
		case t.kind == tField && t.spaced:
			b.WriteString(q[last:t.pos])
			b.WriteString(t.text + ":")
			last = t.end
		case t.kind == tTerm && isIdentToken(t) && i+2 < len(toks) && toks[i+1].kind == tCmp &&
			(toks[i+2].kind == tTerm || toks[i+2].kind == tPhrase) && !precededByField(toks, i):
			cmp, v := toks[i+1], toks[i+2]
			b.WriteString(q[last:t.pos])
			b.WriteString(q[t.pos:t.end] + ":" + cmp.text + q[v.pos:v.end])
			last = v.end
			i += 2
		}
	}
	b.WriteString(q[last:])
	return b.String()
}

// precededByField — terim bir `alan:` önekinin değeri mi (o zaman KQL
// karşılaştırması değil; `a:b >= 5` gibi bir şey ayrışmazdı zaten).
func precededByField(toks []token, i int) bool {
	return i > 0 && toks[i-1].kind == tField
}

func isWordOp(s string) bool {
	switch strings.ToUpper(s) {
	case "AND", "OR", "NOT", "TO":
		return true
	}
	return false
}

// LuceneOnly — tırnak dışında Lucene'e özgü sözdizimi var mı: fuzzy /
// yakınlık `~`, boost `^`, terim başında regex `/…/`, terim başında zorunlu
// `+`. Kaçışlı karakterler sayılmaz. Saf.
func LuceneOnly(q string) bool {
	inQ := false
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == '\\' {
			i++
			continue
		}
		if c == '"' {
			inQ = !inQ
			continue
		}
		if inQ {
			continue
		}
		atStart := i == 0 || isSpace(q[i-1]) || q[i-1] == '(' || q[i-1] == ':'
		switch {
		case c == '~' || c == '^':
			return true
		case c == '+' && atStart:
			return true
		case c == '/' && atStart && i+1 < len(q) && strings.IndexByte(q[i+1:], '/') >= 0 && !(i > 0 && q[i-1] == ':' && i+1 < len(q) && q[i+1] == '/'):
			// `alan:/regex/` ya da `/regex/`; `http://` (iki nokta + //) şema.
			return true
		}
	}
	return false
}

// Lint — YAPISAL kırıkları bulur; Lucene'in de mutlaka reddedeceği sınıflar:
// kapanmamış tırnak, eşleşmeyen ( ) [ ] { }, boş parantez, sorgunun başında
// ya da sonunda asılı AND/OR, sonda NOT. Mesajlar düz Türkçe, eylem söyler.
// Kaçışlı karakterler, tırnak içi ve `/regex/` içi atlanır. Sorun yoksa nil.
func Lint(q string) *ParseError {
	type open struct {
		c   byte
		pos int
	}
	var stack []open
	inQ, qPos := false, -1
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == '\\' {
			i++
			continue
		}
		if inQ {
			if c == '"' {
				inQ = false
			}
			continue
		}
		atStart := i == 0 || isSpace(q[i-1]) || q[i-1] == '(' || q[i-1] == ':'
		switch c {
		case '"':
			inQ, qPos = true, i
		case '/':
			if atStart && !(i > 0 && q[i-1] == ':' && i+1 < len(q) && q[i+1] == '/') {
				if j := strings.IndexByte(q[i+1:], '/'); j >= 0 {
					i += j + 1 // regex gövdesini atla
				}
			}
		case '(', '[', '{':
			if c == '(' {
				k := i + 1
				for k < len(q) && isSpace(q[k]) {
					k++
				}
				if k < len(q) && q[k] == ')' {
					return &ParseError{Pos: i, Msg: "Boş parantez. İçine bir ifade yazın ya da () silin."}
				}
			}
			stack = append(stack, open{c, i})
		case ')', ']', '}':
			if len(stack) == 0 {
				if c == ')' {
					return &ParseError{Pos: i, Msg: "Fazladan ). Bu ) karakterini silin ya da öncesine ( ekleyin."}
				}
				return &ParseError{Pos: i, Msg: "Fazladan " + string(c) + ". Aralığı [ ile açın ya da bu karakteri silin."}
			}
			top := stack[len(stack)-1]
			// Aralık [a TO b} / {a TO b] karışık ayraçla kapanabilir; yalnız
			// parantez ile aralık ayracının karışması kırıktır.
			if (c == ')') != (top.c == '(') {
				return &ParseError{Pos: top.pos, Msg: string(top.c) + " kapatılmamış. " + closeHint(top.c)}
			}
			stack = stack[:len(stack)-1]
		}
	}
	if inQ {
		return &ParseError{Pos: qPos, Msg: "\" kapatılmamış. Sonuna \" ekleyin."}
	}
	if len(stack) > 0 {
		top := stack[len(stack)-1]
		return &ParseError{Pos: top.pos, Msg: string(top.c) + " kapatılmamış. " + closeHint(top.c)}
	}
	// Asılı operatörler: tokenize ederek baş/son kelimeye bak (lex hatası
	// burada olamaz: tırnak ve parantez yukarıda sağlam çıktı).
	toks, err := lex(q)
	if err != nil || len(toks) < 2 {
		return nil
	}
	first, lastTok := toks[0], toks[len(toks)-2]
	if first.kind == tAnd || first.kind == tOr {
		op := strings.ToUpper(first.text)
		return &ParseError{Pos: first.pos, Msg: "Sorgu " + op + " ile başlıyor. Önüne bir terim ekleyin ya da " + op + " kelimesini silin."}
	}
	if lastTok.kind == tAnd || lastTok.kind == tOr || lastTok.kind == tNot {
		op := strings.ToUpper(lastTok.text)
		return &ParseError{Pos: lastTok.pos, Msg: "Sorgu " + op + " ile bitiyor. Sonuna bir terim ekleyin ya da " + op + " kelimesini silin."}
	}
	for i := 0; i+1 < len(toks); i++ {
		a, b := toks[i], toks[i+1]
		if (a.kind == tAnd || a.kind == tOr) && (b.kind == tAnd || b.kind == tOr) {
			return &ParseError{Pos: b.pos, Msg: "Art arda iki operatör (" + strings.ToUpper(a.text) + " " + strings.ToUpper(b.text) + "). Birini silin."}
		}
		if (a.kind == tAnd || a.kind == tOr || a.kind == tNot) && b.kind == tRParen {
			op := strings.ToUpper(a.text)
			return &ParseError{Pos: a.pos, Msg: op + " sonrasında ifade yok. ) önüne bir terim ekleyin ya da " + op + " kelimesini silin."}
		}
	}
	return nil
}

func closeHint(c byte) string {
	switch c {
	case '(':
		return "Sonuna ) ekleyin."
	case '[':
		return "Aralığı ] ya da } ile kapatın."
	}
	return "Aralığı } ya da ] ile kapatın."
}
