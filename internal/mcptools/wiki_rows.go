package mcptools

// wiki_rows.go — v0.10.1554 — Operator-reported ("Hepsini getirsin istiyorum"):
// Derin kipte model 1,9M karakterlik yüklenen Excel'i read_wiki_page'in 4800
// karakterlik ilk penceresinden okudu ve "CRMHouse" için 2 satırla durdu;
// sayfa tavanı (WikiPageMaxRunes) yüzünden sayfalama da tamamına ulaşamaz.
// Yeni `find` argümanı: yüklenen tablo belgesinin TAMAMI (pencere / tavan
// yok) satır satır taranır, hücresi değeri içeren satırların hepsi
// (≤ WikiFindRowsMax) rowsToText satırıyla aynen döner — model sayfayı
// okumaz. find'sız çağrıda büyük tablo belgesi için ipucu verilir.

import (
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/rag"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	// WikiFindRowsMax — api.docRowsMax ile aynı tavan.
	WikiFindRowsMax = 200
	// wikiTableHintMinRows — bu kadardan çok satırlı tablo belgesi pencereyle
	// okunmaz; find ipucu verilir.
	wikiTableHintMinRows = 40
)

// wikiTableLines — SAF: içeriğin tablo satırı (rowsToText) olan çizgileri,
// ham hâliyle, ayrıştırılmış hücreleriyle.
func wikiTableLines(content string) (lines []string, rows []rag.TableRow) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, " · satır ") {
			continue
		}
		if r := rag.ParseTableRows(line); len(r) == 1 {
			lines = append(lines, line)
			rows = append(rows, r[0])
		}
	}
	return lines, rows
}

// WikiPageFindRows — SAF: belgenin TAMAMINDA hücresi `find`i (katlanmış,
// büyük/küçük harfsiz, alt dize) içeren satırlar. Tablo satırı yoksa ilk
// pencere + not.
func WikiPageFindRows(rec wiki.PageRecord, find string) map[string]any {
	needle := strings.TrimSpace(wiki.Fold(find))
	lines, rows := wikiTableLines(rec.Content)
	if len(rows) == 0 || needle == "" {
		out := WikiPageWindow(rec, 0)
		out["note"] = "Bu sayfa tablo belgesi değil; `find` uygulanmadı, ilk pencere verildi."
		return out
	}
	var hits []string
	matched := 0
	for i, r := range rows {
		hit := false
		for _, c := range r.Cells {
			if strings.Contains(wiki.Fold(c.Val), needle) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		matched++
		if len(hits) < WikiFindRowsMax {
			hits = append(hits, lines[i])
		}
	}
	capped := matched > WikiFindRowsMax
	out := map[string]any{
		"source":       sourcestate.Result("wiki", "local", sourcestate.Outcome{Returned: len(hits), Limit: WikiFindRowsMax, Truncated: capped}),
		"found":        true,
		"project":      rec.Project,
		"wiki":         rec.WikiName,
		"path":         rec.Path,
		"title":        rec.Title,
		"url":          rec.URL,
		"find":         find,
		"total_rows":   len(rows),
		"matched_rows": matched,
		"rows":         hits,
		"data_note":    wikiDataNote,
	}
	switch {
	case matched == 0:
		out["note"] = fmt.Sprintf("Belgenin tamamı (%d satır) tarandı; hiçbir hücre %q içermiyor. Değeri aynen (sahip adı, sunucu adı, IP) yaz ya da search_wiki ile doğru belgeyi bul.", len(rows), find)
	case capped:
		out["note"] = fmt.Sprintf("Belgenin tamamı tarandı: %q geçen %d satırdan ilk %d verildi — daha seçici bir değerle tekrar çağır.", find, matched, WikiFindRowsMax)
	default:
		out["note"] = fmt.Sprintf("Belgenin tamamı (%d satır) tarandı; %q geçen %d satırın HEPSİ bu listede — cevapta hepsini listele, sayfayı ayrıca okuma.", len(rows), find, matched)
	}
	return out
}

// wikiPageWindowWithTableHint — SAF: pencere + büyük tablo belgesinde find ipucu.
func wikiPageWindowWithTableHint(rec wiki.PageRecord, offset int) map[string]any {
	out := WikiPageWindow(rec, offset)
	if len([]rune(rec.Content)) <= WikiWindowRunes {
		return out
	}
	if _, rows := wikiTableLines(rec.Content); len(rows) >= wikiTableHintMinRows {
		out["table_hint"] = fmt.Sprintf("Bu belge %d satırlık bir tablo; pencereyle tamamı okunamaz. Belirli kayıtlar için `find=<hücre değeri>` (sahip, sunucu adı, IP) ile yeniden çağır: eşleşen satırların TAMAMI döner.", len(rows))
	}
	return out
}
