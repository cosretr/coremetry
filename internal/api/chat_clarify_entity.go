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

import (
	"context"
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
	if q == "" || len(guidedTokens(normalizeGuidedMsg(q))) > bareEntityMaxTokens {
		return false // katalog okumasından önce ucuz kapı
	}
	kind, name := bareEntityOf(q, s.guidedTeamNames(ctx), s.guidedServiceNames(ctx))
	if kind == "" {
		return false
	}
	if rows, _ := s.docRowsFind(ctx, q, true); len(rows) == 0 {
		return false // doküman kademeleri zaten cevaplamazdı
	}
	cst.setPending(&chatPendingAsk{Kind: pendingKindDocOrEntity, Question: clipRunes(q, chatPendingQuestionMax),
		Entity: kind, AskedAt: time.Now().UnixNano()})
	chatExchangeFrom(ctx).noteRoute("doc_or_entity_ask")
	emit("answer", map[string]any{"text": docOrEntityAskTR(kind, name), "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
	return true
}

// docOrEntityNoRowsTR — SAF: doküman seçildi ama satır artık yok.
func docOrEntityNoRowsTR(q string) string {
	return "Yüklü dokümanlarda “" + clipRunes(q, 80) + "” için eşleşen satır bulamadım."
}
