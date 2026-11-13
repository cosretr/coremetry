package api

import (
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/healthverdict"
)

// chat_summary_card.go — v0.10.2101 (docs/cosre/experience-audit.md D4):
// CoSRE v2 kısa özet + link kartı. Özet cümlesi SUNUCUDA, bundle'ın ZATEN
// hesapladığı sayılardan kurulur (guidedSummaryFacts; yeni sorgu yok) —
// modelin "**Özet:**" satırı okunmaz. Kart data-links'in opsiyonel
// title/summary alanıdır (uistream translator, answer.linkCard); alan yoksa
// FE bugünkü çipleri çizer. Yalnız v2 (copilot_guided.go emit noktası
// cosreV2Marked kapılı); v1 cevabı değişmez. Rotalar: service_health,
// problems, my_services (operatör onayı 2026-11-13); link tavanı 3.

// summaryCardMaxLinks — kartın link tavanı (operatör onayı, audit §8 S4).
const summaryCardMaxLinks = 3

// guidedSummaryFacts — bundle'ların özet cümlesi için döndürdüğü sayılar.
// Sıfır değer = "bilinmiyor"; her bayrak okumanın gerçekten yapıldığını
// söyler (0 ile okunamadı ayrışır).
type guidedSummaryFacts struct {
	Scope string // servis ya da takım adı (kart başlığı)

	// service_health — hüküm etiketi (healthverdict.Label*) + RED.
	Verdict      string
	REDKnown     bool
	ErrorRatePct float64 // yüzde (aiRED.ErrorRate birimi)
	P99Ms        float64

	// service_health + problems — açık problem sayısı.
	ProblemsKnown bool
	Problems      uint64
	ProblemsExact bool // false → "en az N" (problemsTotal.known)

	// my_services — takım RED'i (hata oranına göre sıralı satırlardan).
	TeamREDKnown bool
	Services     int // takımın servis sayısı (tavan dışı kalanlar dahil)
	ErrServices  int // pencerede hatası olan servis
	Worst        string
	WorstErrPct  float64
}

// guidedSummaryTR — SAF: kartın tek düz cümlesi; sayı yoksa ("", false).
func guidedSummaryTR(route guidedRoute, f guidedSummaryFacts) (string, bool) {
	switch route.Intent {
	case guidedServiceHealth:
		if f.Verdict == "" || f.Verdict == healthverdict.LabelNoData || !f.REDKnown {
			return "", false
		}
		s := fmt.Sprintf("Sağlık durumu %s; hata oranı %s, p99 %s", f.Verdict, summaryPctTR(f.ErrorRatePct), endpointDurTR(f.P99Ms))
		if f.ProblemsKnown {
			s += ", " + summaryProblemsTR(f)
		}
		return s + ".", true
	case guidedProblems:
		if !f.ProblemsKnown {
			return "", false
		}
		if f.Problems == 0 && f.ProblemsExact {
			return "Açık problem yok.", true
		}
		if f.ProblemsExact {
			return fmt.Sprintf("%d açık problem var.", f.Problems), true
		}
		return fmt.Sprintf("En az %d açık problem var.", f.Problems), true
	case guidedMyServices:
		if !f.TeamREDKnown || f.Services <= 0 {
			return "", false
		}
		if f.ErrServices == 0 || f.Worst == "" {
			return fmt.Sprintf("%d servisin hiçbirinde hata yok.", f.Services), true
		}
		return fmt.Sprintf("%d servisten %d tanesinde hata var; en yüksek hata oranı %s (%s).",
			f.Services, f.ErrServices, f.Worst, summaryPctTR(f.WorstErrPct)), true
	}
	return "", false
}

// summaryProblemsTR — SAF: "açık problem yok" / "2 açık problem" / "en az 10 açık problem".
func summaryProblemsTR(f guidedSummaryFacts) string {
	switch {
	case f.Problems == 0 && f.ProblemsExact:
		return "açık problem yok"
	case f.ProblemsExact:
		return fmt.Sprintf("%d açık problem", f.Problems)
	}
	return fmt.Sprintf("en az %d açık problem", f.Problems)
}

// summaryPctTR — SAF: yüzde → "%1,2"; sıfırdan büyük ama gösterilen basamakta
// sıfır görünen oran "%<0,1" (fmtTRErrRate emsali).
func summaryPctTR(pct float64) string {
	if pct > 0 && pct < 0.05 {
		return "%<0,1"
	}
	return "%" + trNum(pct)
}

// guidedSummaryTitle — SAF: kart başlığı.
func guidedSummaryTitle(route guidedRoute, f guidedSummaryFacts) string {
	switch {
	case route.Intent == guidedMyServices && f.Scope != "":
		return f.Scope + " takımı"
	case route.Intent == guidedMyServices:
		return "Takımın servisleri"
	case f.Scope != "":
		return f.Scope
	case route.Intent == guidedProblems:
		return "Açık problemler"
	}
	return ""
}

// withSummaryCard — cevaba kart ekler (yerinde): özet cümlesi varsa ve en az
// bir link kalıyorsa linkler href ile tekilleşip 3'e kırpılır, linkCard
// {title, summary} yazılır. Aksi halde cevap DOKUNULMADAN kalır.
func withSummaryCard(answer map[string]any, route guidedRoute, f guidedSummaryFacts) {
	summary, ok := guidedSummaryTR(route, f)
	if !ok {
		return
	}
	links, _ := answer["links"].([]guidedAnswerLink)
	links = dedupLinksByHref(links)
	if len(links) == 0 {
		return
	}
	for _, l := range links {
		if l.ID != "" {
			// Kimlik taşıyan link (request_id) cevap METNİNDE satır içi
			// linke dönüşür; tavanla düşerse metindeki link kaybolur. Kart yok.
			return
		}
		if !strings.HasPrefix(l.Href, "/") || strings.HasPrefix(l.Href, "//") || strings.HasPrefix(l.Href, "/\\") {
			// Kart yalnız Coremetry iç sayfalarına götürür (FE parseDataLinksCard
			// aynı kuralı uygular); dış link (wiki ipucu) varsa bugünkü çipler.
			return
		}
	}
	if len(links) > summaryCardMaxLinks {
		links = links[:summaryCardMaxLinks]
	}
	answer["links"] = links
	answer["linkCard"] = map[string]string{"title": strings.TrimSpace(guidedSummaryTitle(route, f)), "summary": summary}
}

// summaryProblemCount — SAF: kartın problem sayısı; toplam bilinmiyorsa
// gösterilen satır sayısı "en az" olarak (problemsCountPhraseTR ile aynı kural).
func summaryProblemCount(total problemsTotal, shown int) (uint64, bool) {
	if total.known {
		return total.n, true
	}
	return uint64(shown), false
}

// teamSummaryFacts — SAF: takım RED satırlarından (hata oranına göre sıralı,
// mcptools.ReadTeamServicesRED) kartın sayıları. Satır yoksa RED bilinmiyor.
func teamSummaryFacts(team string, rows []chstore.ServiceSummary, services int) guidedSummaryFacts {
	f := guidedSummaryFacts{Scope: team, Services: services, TeamREDKnown: len(rows) > 0}
	for _, r := range rows {
		if r.ErrorCount == 0 {
			continue
		}
		f.ErrServices++
		if f.Worst == "" || r.ErrorRate > f.WorstErrPct {
			f.Worst, f.WorstErrPct = r.Name, r.ErrorRate
		}
	}
	return f
}
