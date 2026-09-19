package api

import (
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// v0.10.809 — CoSRE yol tarifi / onboarding (operatör isteği 2026-09-19).
// "Nasıl bakarım / nereden görürüm" soruları veri sorusu değildir: LLM
// çağrılmaz, cevap sunucunun ürün haritasından (mcptools/product_guide.go)
// deterministik kurulur. TEK TIK sözleşmesi (operatör kararı): cevap `links`
// çipleri taşır, `open` anahtarı YAZILMAZ — sayfa kendiliğinden değişmez
// (open_page niyeti "aç/götür" denince zaten açar, o yol aynen kalır).
//
// Konu bulunamazsa konu dizini + Ask çipleri; çipe tıklamak yeni soru
// olarak gider ve yine buraya düşer (sunucuda durum yok).
func (s *Server) guidedHowToAnswer(emit func(string, any), route guidedRoute, question, ctxService string) (bool, bool) {
	svc := route.Service
	if svc == "" {
		svc = ctxService // ekran/sohbet bağlamındaki servis adımlara girer
	}
	args, _ := json.Marshal(map[string]string{"question": question, "service": svc})
	n := emitGuidedStep(emit, "product_guide", string(args))
	ans, ok := mcptools.GuideFor("", question, svc, route.TraceID)
	if !ok {
		text := mcptools.GuideIndexTR()
		emitGuidedStepResult(emit, n, "product_guide", text, nil)
		emit("answer", map[string]any{
			"text":        text,
			"suggestions": howToTopicChips(nil),
			"links":       []guidedAnswerLink{{Label: "Servisler", Href: "/services"}, {Label: "Trace'ler", Href: "/traces"}},
		})
		return true, true
	}
	text := mcptools.RenderGuideTR(ans)
	emitGuidedStepResult(emit, n, "product_guide", text, nil)
	links := make([]guidedAnswerLink, 0, len(ans.Links))
	for _, l := range ans.Links {
		links = append(links, guidedAnswerLink{Label: l.Label, Href: l.Href})
	}
	emit("answer", map[string]any{
		"text":        text,
		"suggestions": howToTopicChips(ans.Related),
		"links":       dedupLinksByHref(links),
	})
	return true, true
}

// howToTopicChips — SAF: ilgili konuların Ask metinleri (boş liste = tüm
// konular, en çok 6).
func howToTopicChips(related []string) []string {
	topics := mcptools.GuideTopics()
	want := map[string]bool{}
	for _, id := range related {
		want[id] = true
	}
	var out []string
	for _, t := range topics {
		if len(related) > 0 && !want[t.ID] {
			continue
		}
		out = append(out, t.Ask)
		if len(out) >= 6 {
			break
		}
	}
	return out
}
