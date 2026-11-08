package api

// chat_clarify_entity.go — v0.10.1576 (operator-reported, prod: "Takımımın
// servisleri nasıl?" → CoSRE takım sordu → kullanıcı listedeki takım adını
// yazdı → cevap yüklenen tablolardan geldi). Operatör: "Bu durumda ragda
// aramasın, yüklü dokümanlarda arayayım mı diye sormasını beklerim."
//
// Kök neden: doküman kademeleri (kaynak keşfi + sıkı satır kademesi) guided'dan
// ÖNCE koşar; çıplak "Payments-Core" kimlik biçimli (tire / karışık harf) güçlü
// bir aday olduğu için tablo hücresiyle birebir eşleşti ve doc-rows cevapladı —
// guided'ın adı geçen takım dalı (isBareTeamAsk → team_services) hiç koşmadı.
//
// Kural: mesaj ÇIPLAK bilinen bir varlık adıysa (≤3 sözcük, adın dışında
// harf/rakam yok; canlı takım kataloğu önce, sonra servis adı) VE yüklü
// tablolarda sıkı eşleşme varsa doküman kademeleri kendiliğinden cevaplamaz:
// tek düz cümle sorulur (LLM yok, çip yok, exchangeId yok; sourceAskTextTR ile
// aynı biçim), bekleyen soru pendingKindDocOrEntity olarak saklanır.
// Cevap sınıfları (classifyDocOrEntityReply):
//
//	doküman / yüklü / tablo / excel / dosya / belge / wiki → ÖZGÜN ad sıkı
//	    satır kademesinde (docRowsAnswer) cevaplanır;
//	takım / ekip / servis / coremetry / telemetri / durum / sağlık → ÖZGÜN ad
//	    telemetri zorlamasıyla zincirde (doküman kademeleri atlanır → guided
//	    team_services / servis sağlığı);
//	"evet" / "tamam" yalnız başına → SINIF YOK (soru iki seçenekli; "evet"
//	    hangisini seçtiğini söylemez) → bekleyen soru düşer, tur yeni soru.
//
// Çıplak varlık değilse ya da tabloda eşleşme yoksa akış bayt bayt eski.
//
// v0.10.1942 (operator-reported, prod: "<takım> takımının servisleri nasıl?" →
// keşif kademesi benzer adlı BAŞKA takımın envanter satırlarını (OpenShift +
// sunucu) doğrudan cevapladı). Operatör: "Envanterde direkt cevap dönmesin önce
// sorsun; servislerim nasıl dediğinde yüklenen envanter dosyasına gitmemeli."
// Kök neden: doküman kademeleri (keşif, sıkı satır, RAG'ın gevşek satırı) yüklü
// tabloda güçlü eşleşme bulunca soru servis sağlığını sorsa da doğrudan
// envanterden cevaplıyordu; "önce sor" yalnız ÇIPLAK varlık adında vardı.
// Kural (iki parça):
//   1. isServiceHealthAsk → telemetri zorlanır, doküman kademeleri hiç koşmaz.
//   2. Tablo eşleşmesi kullanıcı seçmeden cevap olmaz: bu kademe her eşleşmede
//      sorar (çıplak varlıkta eski metin, değilse docsAskTR); bekleyen soru
//      yoksa (bellek kapalı) satır kademesi / keşif çiplerle sorar
//      (docRowsAsk: /excel · N satır, /telemetri). "envanter / tablo" cevabı ve
//      /excel komutu doğrudan cevaplar (withDocsConfirmed / sourceForcedAnswer).

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/copilot"
)

const (
	pendingKindDocOrEntity = "doc_or_entity"

	bareEntityTeam    = "team"
	bareEntityService = "service"

	entityReplyDocs   = "docs"
	entityReplyEntity = "entity"

	// bareEntityMaxTokens — çıplak varlık mesajının sözcük tavanı.
	bareEntityMaxTokens = 3
)

type docsConfirmedKey struct{}

// withDocsConfirmed — kullanıcı yüklü envanteri seçti: satır kademesi sormadan cevaplar.
func withDocsConfirmed(ctx context.Context) context.Context {
	return context.WithValue(ctx, docsConfirmedKey{}, true)
}

func docsConfirmed(ctx context.Context) bool {
	v, _ := ctx.Value(docsConfirmedKey{}).(bool)
	return v
}

var (
	// healthSubjectStems / healthStateStems — servis sağlığı sorusunun iki yarısı
	// (katlanmış önekler): "servisleri" + "nasıl", "takımın" + "durumu".
	healthSubjectStems = []string{"servis", "service", "uygulama", "takim", "ekip", "ekib", "team"} // ekib: "ekibinde" (p→b)
	healthStateStems   = []string{"nasil", "durum", "saglik", "health", "sorun", "iyi"}
	// healthDocCueStems — envanteri / dokümanı açıkça anan soru sağlık sorusu sayılmaz.
	healthDocCueStems = []string{"envanter", "tablo", "excel", "dokuman", "wiki", "belge", "runbook", "dosya", "kayit"}
)

// isServiceHealthAsk — SAF (v0.10.1942): soru servis sağlığını mı soruyor.
// "servislerim / takımımın servisleri" iyelik biçimi ya da (servis|takım) +
// (nasıl|durum|sağlık|sorun|iyi) birlikte; nasıl-yapılır sorusu ve envanteri /
// dokümanı açıkça anan soru değil.
func isServiceHealthAsk(q string) bool {
	norm := normalizeGuidedMsg(q)
	toks := guidedTokens(norm)
	if len(toks) == 0 || hasHowToShape(toks) {
		return false
	}
	folded := strings.Fields(foldWords(norm))
	has := func(stems []string) bool {
		for _, t := range folded {
			for _, st := range stems {
				if strings.HasPrefix(t, st) {
					return true
				}
			}
		}
		return false
	}
	if has(healthDocCueStems) {
		return false
	}
	return hasTeamSelfSignal(toks) || (has(healthSubjectStems) && has(healthStateStems))
}

// docsAskTR — SAF (v0.10.1942): çıplak olmayan soruda envanter eşleşmesi sorusu.
func docsAskTR(n int, vals []string) string {
	what := ""
	if len(vals) > 0 {
		v := vals
		if len(v) > 2 {
			v = v[:2]
		}
		what = " (" + strings.Join(v, ", ") + ")"
	}
	return fmt.Sprintf("Bu soru yüklü envanter tablosunda %d satırla eşleşiyor%s. Envanterden mi göstereyim, yoksa canlı telemetriden mi bakayım?", n, what)
}

// docRowsAsk — v0.10.1942: bekleyen soru saklanamayan yolda (bellek kapalı,
// keşif, RAG'ın gevşek satırı) çiplerle sor: /excel aynı soruyu envanterden,
// /telemetri canlı veriden cevaplar (sourceCommandQuestion önceki soruyu alır).
func docRowsAsk(emit func(string, any), n int, vals []string, alts []string) {
	chips := []string{sourceChip(srcCand{Kind: srcExcel, Label: fmt.Sprintf("%d satır", n)}), "/" + srcTelemetry}
	for _, a := range alts {
		if len(chips) >= discoverMaxChips {
			break
		}
		if !strings.HasPrefix(a, "/"+srcTelemetry) && !strings.HasPrefix(a, "/"+srcExcel) {
			chips = append(chips, a)
		}
	}
	emit("answer", map[string]any{"text": docsAskTR(n, vals), "suggestions": chips,
		"sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
}

// docOrEntityAskTR — SAF: sorunun deterministik metni (LLM yok).
func docOrEntityAskTR(kind, name string) string {
	if kind == bareEntityService {
		return "“" + name + "” bir servis adı. Servisin durumunu mu getireyim, yoksa yüklü dokümanlarda mı arayayım?"
	}
	return "“" + name + "” bir takım adı. Takımın servislerini mi getireyim, yoksa yüklü dokümanlarda mı arayayım?"
}

// bareRemainderEmpty — SAF: katlanmış mesajdan ad (sınırlı) çıkarılınca harf /
// rakam kalmıyor mu.
func bareRemainderEmpty(folded, name string) bool {
	if name == "" {
		return false
	}
	i := indexBounded(folded, name)
	if i < 0 {
		return false
	}
	for _, r := range folded[:i] + folded[i+len(name):] {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// bareEntityOf — SAF: mesaj yalnız bilinen bir takım ya da servis adı mı.
// Takım önce (guided router'ın sırası: adı geçen takım servis dallarından önce).
func bareEntityOf(msg string, teams, services []string) (kind, name string) {
	// Sondaki noktalama ad karakteri sayılmasın ("order-api." → sınırlı eşleşme).
	msg = strings.TrimRight(strings.TrimSpace(msg), ".,;:!?…")
	norm := normalizeGuidedMsg(msg)
	toks := guidedTokens(norm)
	if len(toks) == 0 || len(toks) > bareEntityMaxTokens {
		return "", ""
	}
	if team := extractTeamEntity(msg, teams); team != "" && isBareTeamAsk(msg, team) {
		return bareEntityTeam, team
	}
	if svc := extractServiceFullName(norm, services); svc != "" && bareRemainderEmpty(norm, strings.ToLower(svc)) {
		return bareEntityService, svc
	}
	return "", ""
}

var (
	entityReplyDocStems = []string{"dokuman", "doc", "yuklu", "yuklen", "tablo", "excel", "dosya", "belge", "wiki", "rag", "envanter"}
	entityReplyEntStems = []string{"takim", "ekip", "team", "servis", "service", "coremetry", "telemetri", "durum",
		"saglik", "canli", "veri"}
	entityReplyFillers = func() map[string]bool {
		m := map[string]bool{"getir": true, "getirin": true, "goster": true, "gosterin": true, "olsun": true}
		for k := range sourceReplyFillers {
			m[k] = true
		}
		return m
	}()
)

// classifyDocOrEntityReply — SAF: "docs" | "entity" | "" (cevap değil).
func classifyDocOrEntityReply(text string) string {
	switch classifyStemReply(text, entityReplyDocStems, entityReplyEntStems, entityReplyFillers) {
	case 1:
		return entityReplyDocs
	case 2:
		return entityReplyEntity
	}
	return ""
}

// chatDocOrEntityAskTier — çıplak varlık + tabloda eşleşme → soru. Kapılardan
// biri kapalıysa false ve akış bayt bayt eski.
func (s *Server) chatDocOrEntityAskTier(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, tc wikiTierContext, cst *chatContextState) bool {
	if !s.canRemember(cst) || !wikiTierAllowed(tc) || forcedChatSource(ctx) != "" {
		return false
	}
	q := strings.TrimSpace(lastUserText(msgs))
	if q == "" {
		return false
	}
	rows, vals := s.docRowsFind(ctx, q, true)
	if len(rows) == 0 {
		return false // doküman kademeleri zaten cevaplamazdı
	}
	// Çıplak varlık adı (≤3 sözcük; katalog yalnız o zaman okunur) eski metinle;
	// v0.10.1942'den beri diğer her tablo eşleşmesi de önce sorulur.
	kind, name := "", ""
	if len(guidedTokens(normalizeGuidedMsg(q))) <= bareEntityMaxTokens {
		kind, name = bareEntityOf(q, s.guidedTeamNames(ctx), s.guidedServiceNames(ctx))
	}
	text := docsAskTR(len(rows), vals)
	if kind != "" {
		text = docOrEntityAskTR(kind, name)
	}
	cst.setPending(&chatPendingAsk{Kind: pendingKindDocOrEntity, Question: clipRunes(q, chatPendingQuestionMax),
		Entity: kind, AskedAt: time.Now().UnixNano()})
	chatExchangeFrom(ctx).noteRoute("doc_or_entity_ask")
	emit("answer", map[string]any{"text": text, "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
	return true
}

// docOrEntityNoRowsTR — SAF: doküman seçildi ama satır artık yok.
func docOrEntityNoRowsTR(q string) string {
	return "Yüklü dokümanlarda “" + clipRunes(q, 80) + "” için eşleşen satır bulamadım."
}
