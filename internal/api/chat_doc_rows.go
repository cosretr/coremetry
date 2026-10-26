package api

// chat_doc_rows.go — v0.10.1176 — TABLO SATIRI KADEMESİ: yüklenen Excel / CSV
// envanterinde soruda geçen kimliğin (hostname, uygulama kodu, IP) BİREBİR
// eşleştiği satırlar, tüm kolonlarıyla tablo olarak cevaplanır (operatör:
// "Excel'deki kolonlara göre ilişki kursun, tabloya çevirsin, search edince
// karşılıklarını bulsun").
//
// Neden embedding / LLM okuması değil: envanter binlerce satır; parça
// benzerliği hostname'i taşıyan satırı kaçırıyordu (v0.10.1175 kök nedeni) ve
// tüm belgeyi modele okutmak pencereye sığmaz. Bu kademe deterministik:
// wiki indeksindeki yerel kaynakta değeri içeren parçalar okunur, satırlar
// rowsToText biçiminden geri ayrıştırılır (rag.ParseTableRows), YALNIZ
// hücresinde kimlik geçen satırlar tabloya girer. LLM çağrısı yok.
//
// Sıra: wiki kademesinden sonra, guided telemetri yönlendiricisinden ÖNCE
// (servis adı gibi görünen uygulama kodu telemetriye kaçmasın) — ama soru
// telemetri sinyali taşıyorsa (wikiWeakCueVetoed: "hata oranı", "gecikme"…)
// kademe sessizce çekilir. Eşleşme yoksa hiçbir olay basılmaz.

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/rag"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	docRowsChunkLimit = 400
	// v0.10.1551 (operatör: "CRMHouse ait vserver adreslerini getir" → 16 satırdan
	// 1'i): kullanıcı "ilgili TÜM kayıtları" ister — çıktı tavanı 200.
	docRowsMax = 200
	// docRowsCommon — bu kadardan çok satırda geçen değer (PROD, Hayır) yaygın:
	// başka seçici değer varken satır seçmez (v0.10.1183 kuralı, eski 50).
	docRowsCommon  = 50
	docRowsMaxCols = 12
)

// docRowMatch — eşleşen satır + kaynağı.
type docRowMatch struct {
	Doc string // belge başlığı
	URL string
	rag.TableRow
}

// v0.10.1183 — Operator-reported: sunucu / fonksiyon envanteri Excel'lerinde
// bilgi hâlâ bulunamıyordu. Ölçüm: hostname'lerin çoğu rakam / tire taşımaz
// (CCFWKAPP, MCCREPORT) → 1176'nın "kimlik biçimli terim" kapısı onları hiç
// aramıyordu; ayrıca soru terimleri katlanmış (çağrı → cagri), hücre metni
// değil. Yeni kural: sorudaki kelime / ifade bir HÜCRE DEĞERİNE (katlanmış,
// kelime sınırlı) birebir eşitse satır eşleşir; kimlik biçimli terim hücrede
// alt-dize olarak da eşleşir (srvappl01 ↔ srvappl01.corp.local). 50'den çok
// satırda geçen değer (PROD, Hayır, Şube) yaygın sayılıp atlanır.
//
// strict (guided'dan ÖNCE çalışan erken kademe): yalnız güçlü işaret —
// kimlik biçimli ya da TAMAMI BÜYÜK HARF (≥3) yazılmış kelime veya ≥2
// kelimelik hücre ifadesi. Gevşek kip doküman adımında (guided eşleşmedikten
// sonra) her eşleşmeyi kabul eder; genel sohbet sorusu böylece erken
// kademeye kaçmaz.

// rowNeedle — sorudan aday kelime (ham yazım, katlanmış, güçlü mü).
type rowNeedle struct {
	raw, folded string
	strong      bool
}

// rowNeedles — SAF: arama adayları (en çok 8). Durak kelimeler QueryTerms'le elenir.
func rowNeedles(question string) []rowNeedle {
	keep := map[string]bool{}
	for _, t := range wiki.QueryTerms(question) {
		keep[t] = true
	}
	var out []rowNeedle
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(question, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '.' || r == '_' || r == '\'')
	}) {
		if i := strings.IndexRune(w, '\''); i >= 0 {
			w = w[:i] // CCFWKAPP'in → CCFWKAPP
		}
		w = strings.Trim(w, ".-_")
		f := wiki.Fold(w)
		if len([]rune(w)) < 3 || seen[f] {
			continue
		}
		ident := strings.ContainsAny(f, "0123456789-._")
		if !keep[f] && !ident {
			continue
		}
		seen[f] = true
		out = append(out, rowNeedle{raw: w, folded: f, strong: ident || isAllCaps(w) || isMixedCaseIdent(w)})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

// isMixedCaseIdent — SAF (v0.10.1551): kelime içinde (ilk harften sonra)
// büyük harf + küçük harf — PascalCase / camelCase kimlik (CRMHouse,
// OrderService, orderId). Yalnız baş harfi büyük olağan kelime (Opentelemetry,
// Kampanya) değildir.
func isMixedCaseIdent(w string) bool {
	rs := []rune(w)
	lower, innerUpper, letters := false, false, 0
	for i, r := range rs {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.IsLower(r) {
			lower = true
		} else if unicode.IsUpper(r) && i > 0 {
			innerUpper = true
		}
	}
	return letters >= 3 && lower && innerUpper
}

func isAllCaps(w string) bool {
	letters := 0
	for _, r := range w {
		if unicode.IsLetter(r) {
			if !unicode.IsUpper(r) {
				return false
			}
			letters++
		}
	}
	return letters >= 3
}

// foldWords — SAF: katlanmış, kelime ayraçları tek boşluk, iki uçta boşluk.
func foldWords(s string) string {
	parts := strings.FieldsFunc(wiki.Fold(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '.' || r == '_')
	})
	return " " + strings.Join(parts, " ") + " "
}

type docRowChunk struct{ Title, URL, Text string }

// matchDocRows — SAF: eşleşen satırlar (belge + sayfa + satır tekil, ≤docRowsMax)
// ve eşleşmeyi taşıyan değerler.
func matchDocRows(chunks []docRowChunk, question string, strict bool) ([]docRowMatch, []string) {
	needles := rowNeedles(question)
	if len(needles) == 0 {
		return nil, nil
	}
	fq := foldWords(question)
	type cand struct {
		m    docRowMatch
		vals []string
	}
	var cands []cand
	count := map[string]int{}
	seen := map[string]bool{}
	for _, c := range chunks {
		for _, r := range rag.ParseTableRows(c.Text) {
			k := fmt.Sprintf("%s\x00%s\x00%d", c.Title, r.Sheet, r.Row)
			if seen[k] {
				continue
			}
			seen[k] = true
			var vals []string
			for _, cell := range r.Cells {
				fv := foldWords(cell.Val)
				v := strings.TrimSpace(fv)
				if len([]rune(v)) < 3 {
					continue
				}
				matched := ""
				if strings.Contains(fq, fv) && (!strict || strings.Contains(v, " ") || needleStrongIn(needles, v)) {
					matched = v
				}
				for _, n := range needles {
					if matched == "" && strings.ContainsAny(n.folded, "0123456789-._") && strings.Contains(v, n.folded) {
						matched = n.folded
					}
				}
				if matched != "" && !containsStr(vals, matched) {
					vals = append(vals, matched)
				}
			}
			if len(vals) == 0 {
				continue
			}
			for _, v := range vals {
				count[v]++
			}
			cands = append(cands, cand{m: docRowMatch{Doc: c.Title, URL: c.URL, TableRow: r}, vals: vals})
		}
	}
	var out []docRowMatch
	var used []string
	// v0.10.1551 — yaygın değer (PROD, Hayır) başka seçici değer varken satır
	// seçmez; soruda YALNIZ yaygın değerler eşleştiyse ve değer GÜÇLÜ yazılmışsa
	// ("CRMHouse'un 80 kaydı") onlar seçer — aksi hâlde istenen kayıtlar hiç
	// gelmezdi. Küçük harfli genel kelime ("prod ortamındaki") yine seçmez.
	selective := false
	for _, n := range count {
		if n <= docRowsCommon {
			selective = true
			break
		}
	}
	for _, cd := range cands {
		ok := false
		for _, v := range cd.vals {
			if count[v] <= docRowsCommon || (!selective && needleStrongIn(needles, v)) {
				ok = true
				if !containsStr(used, v) {
					used = append(used, v)
				}
			}
		}
		if ok {
			out = append(out, cd.m)
			if len(out) >= docRowsMax {
				break
			}
		}
	}
	return out, used
}

// needleStrongIn — SAF: hücre değeri güçlü bir aday kelimeyi kelime olarak içeriyor mu.
func needleStrongIn(needles []rowNeedle, v string) bool {
	pv := " " + v + " "
	for _, n := range needles {
		if n.strong && strings.Contains(pv, " "+n.folded+" ") {
			return true
		}
	}
	return false
}

// docRowsMarkdown — SAF: belge + sayfa başına bir Markdown tablosu; kolonlar
// ilk görülme sırasıyla (en çok docRowsMaxCols), hücrelerde | kaçışlı.
func docRowsMarkdown(ids []string, rows []docRowMatch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Yüklenen belgelerde **%s** için %d satır bulundu", strings.Join(ids, ", "), len(rows))
	if len(rows) >= docRowsMax {
		fmt.Fprintf(&b, " (ilk %d)", docRowsMax)
	}
	b.WriteString(":\n")
	type group struct {
		title string
		cols  []string
		rows  []docRowMatch
	}
	var groups []*group
	at := map[string]*group{}
	for _, r := range rows {
		k := r.Doc + " › " + r.Sheet
		g := at[k]
		if g == nil {
			g = &group{title: k}
			at[k] = g
			groups = append(groups, g)
		}
		for _, c := range r.Cells {
			if !containsStr(g.cols, c.Key) && len(g.cols) < docRowsMaxCols {
				g.cols = append(g.cols, c.Key)
			}
		}
		g.rows = append(g.rows, r)
	}
	esc := func(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
	for _, g := range groups {
		fmt.Fprintf(&b, "\n**%s**\n\n| Satır | %s |\n|---|%s\n", esc(g.title),
			strings.Join(mapStr(g.cols, esc), " | "), strings.Repeat("---|", len(g.cols)))
		for _, r := range g.rows {
			vals := make([]string, len(g.cols))
			for _, c := range r.Cells {
				for i, col := range g.cols {
					if col == c.Key {
						vals[i] = esc(c.Val)
					}
				}
			}
			fmt.Fprintf(&b, "| %d | %s |\n", r.Row, strings.Join(vals, " | "))
		}
	}
	return b.String()
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func mapStr(xs []string, f func(string) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

// docRowsAnswer — kademe (strict: erken kademe; değil: doküman adımı).
// handled=false → olay yok, akış aynen sürer.
func (s *Server) docRowsAnswer(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, strict bool) (handled, ok bool) {
	q := lastUserText(msgs)
	rows, vals := s.docRowsFind(ctx, q, strict)
	if len(rows) == 0 {
		return false, false
	}
	s.docRowsEmit(withRecordsQuestion(ctx, q), emit, rows, vals, nil)
	return true, true
}

// docRowsFind — v0.10.1553: kademenin OKUMA yarısı (kaynak keşfi de çağırır;
// olay basmaz). Eşleşme yoksa nil.
func (s *Server) docRowsFind(ctx context.Context, question string, strict bool) ([]docRowMatch, []string) {
	if !wikiDocsActive(ctx) || s.store == nil {
		return nil, nil
	}
	needles := rowNeedles(question)
	if len(needles) == 0 || (strict && s.wikiWeakCueVetoed(ctx, question)) {
		return nil, nil
	}
	if strict {
		strong := false
		for _, n := range needles {
			strong = strong || n.strong
		}
		if !strong && len(needles) < 2 {
			return nil, nil
		}
	}
	// Aday başına ayrı okuma: OR'lu tek sorgu yaygın kelimeyle tavanı doldurup
	// nadir değeri kaçırabilirdi. Ham yazım (katlanmamış) — saklı metin katlanmaz.
	var chunks []docRowChunk
	seenChunk := map[string]bool{}
	for _, n := range needles {
		found, err := s.store.LocalDocChunksMatching(ctx, wiki.LocalWikiID, []string{n.raw}, docRowsChunkLimit)
		if err != nil {
			continue
		}
		for _, c := range found {
			if k := c.Path + "\x00" + c.Text; !seenChunk[k] {
				seenChunk[k] = true
				chunks = append(chunks, docRowChunk{Title: c.Title, URL: c.URL, Text: c.Text})
			}
		}
	}
	return matchDocRows(chunks, question, strict)
}

// docRowsEmit — kademenin YAZMA yarısı: tablo cevabı (+ kaynak keşfinin
// alternatif çipleri; nil = yok).
func (s *Server) docRowsEmit(ctx context.Context, emit func(string, any), rows []docRowMatch, vals []string, alts []string) {
	emitGuidedContextStep(emit, "yüklenen belgeler · satır araması")
	var sources []chatSource
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Doc] {
			continue
		}
		seen[r.Doc] = true
		kind := chatSourceUpload
		if r.URL != "" {
			kind = chatSourceURL
		}
		sources = append(sources, chatSource{Doc: "Belge · " + r.Doc, Ref: r.URL, Chunk: 1, Score: 1, Kind: kind})
	}
	// v0.10.1553 — LLM anlatımı (soru eşleşen satırlardan cevaplanır, ilişkiler
	// özetlenir); tam tablo altta. Model yoksa / boşsa yalnız tablo (eski).
	table := docRowsMarkdown(vals, rows)
	text := withRecordsNarrative(s.narrateRecords(ctx, emit, lastQuestionOr(ctx, ""), "yüklenen tablo satırları", table), table)
	ans := map[string]any{
		"text": text, "exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
		"sources": dedupeChatSources(sources), "links": []guidedAnswerLink{}, "allowedLinks": []string{},
	}
	if len(alts) > 0 {
		ans["suggestions"] = alts
	}
	emit("answer", ans)
}
