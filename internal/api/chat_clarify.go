package api

// chat_clarify.go — v0.10.1571 (operatör, 2026-10-29: "çipler çok
// kullanılmıyor, soru cevap şeklinde frontier model gibi olsun"): CoSRE
// netleştirme sorusunu DÜZ METİN olarak sorar, kullanıcının serbest metin
// cevabını bir sonraki turda yorumlar. Çip yok.
//
// Durum: bekleyen soru konuşmanın sunucu bağlamında (ChatContext.Pending,
// chat_context.go — Redis, kullanıcı + konuşma). Sonraki serbest metin turu
// onu OKUR ve SİLER (takePending); 10 dk'dan eskisi yok sayılır. Araya bir
// açıklama (ExplainAsk) ya da kapsam/komut turu girerse soru o turda okunmaz,
// sonraki serbest metin turuna ya da TTL'e kadar bekler (bilinen sınır).
//
// Dilim 1 — kaynak belirsizliği: telemetri şekilli ama guided'ın almadığı bir
// soru, wiki gerçek bir alternatifken (wiki açık + oturumlu kullanıcı + yerel
// profil; armDisambigRescue ile aynı üçlü) kaynak sorusunu sorar.
//
// v0.10.2381 — soru metni, seçenek kümesi, cevap yorumlayıcısı ve hatırlama
// artık TEK yerde (chat_source_ask.go): bu dosyanın sorusu, başa baş keşif,
// tablo eşleşmesi ve "servis mi, genel bilgi mi" aynı cümleyi kurar. Cevap
// sunulan kaynaklardan biriyse ÖZGÜN soru o kaynaktan cevaplanır
// (routeSourceChoice: wiki = /wiki yolu; telemetri = /telemetri işareti, tablo /
// wiki / keşif atlanır; envanter = satır kademesi; genel = genel sohbet) ve
// seçim 30 dk hatırlanır. Cevap tanınmazsa bekleyen soru düşer, tur bayt bayt
// eski akışta sürer.
//
// Kayıt: soru turu cosre-turn:clarify/source_ask (LLM'siz); cevap turu
// clarify_reply adım çipi (args: kind + reply sınıfı), satırın kademesi
// gerçekten cevaplayan kademe. Şema değişikliği yok.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
)

const (
	pendingKindSource     = "source"
	pendingKindTraceSynth = "trace_synth"
	// chatPendingTTL — bekleyen sorunun ömrü; eskisi cevap sayılmaz.
	chatPendingTTL = 10 * time.Minute
	// chatPendingQuestionMax — saklanan özgün sorunun rune tavanı.
	chatPendingQuestionMax = 600
	// clarifyReplyTool — cevap turunun adım çipi (etkileşim satırının tools'u).
	clarifyReplyTool = "clarify_reply"

	clarifyReplyDropped = "dropped"
)

// chatPendingAsk — bekleyen netleştirme sorusu.
type chatPendingAsk struct {
	Kind string `json:"kind"`
	// Question — özgün kullanıcı sorusu (kaynak seçilince yeniden koşar).
	Question string `json:"question,omitempty"`
	// AskedAt — sorulma anı (unix ns).
	AskedAt int64 `json:"askedAt"`
	// TraceID / Fingerprint / Operation — trace sentezi sorusunun öznesi.
	TraceID     string `json:"traceId,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Operation   string `json:"operation,omitempty"`
	// Entity — v0.10.1576 çıplak varlık adının türü (team | service); v0.10.2381'dan
	// beri kaynak sorusunun bağlamı (kayıt için).
	Entity string `json:"entity,omitempty"`
	// Options — v0.10.2332 slot sorusunun seçenekleri (≤4; cevap bunlara doğrulanır).
	// v0.10.2381 — kaynak sorusunda SUNULAN kaynak türleri (pendingSourceOptions).
	Options []string `json:"options,omitempty"`
}

// pendingAskValid — SAF: soru tanınan türde, öznesi dolu ve TTL içinde mi.
// Saat kayması için 1 dk'lık ileri tolerans.
func pendingAskValid(p *chatPendingAsk, now time.Time) bool {
	if p == nil || p.AskedAt <= 0 {
		return false
	}
	switch p.Kind {
	case pendingKindSource:
		if strings.TrimSpace(p.Question) == "" {
			return false
		}
	case pendingKindTraceSynth:
		if strings.TrimSpace(p.TraceID) == "" || strings.TrimSpace(p.Fingerprint) == "" {
			return false
		}
	case pendingKindSlot: // v0.10.2332 (chat_answer_slot.go)
		if strings.TrimSpace(p.Question) == "" || len(p.Options) < 2 || len(p.Options) > 4 {
			return false
		}
	default:
		return false
	}
	age := now.Sub(time.Unix(0, p.AskedAt))
	return age >= -time.Minute && age <= chatPendingTTL
}

// setPending — bekleyen soruyu yazar (flush Redis'e taşır). nil-güvenli.
func (st *chatContextState) setPending(p *chatPendingAsk) {
	if st == nil || p == nil {
		return
	}
	st.ctx.Pending, st.dirty = p, true
}

// takePending — bekleyen soruyu okur ve SİLER; geçersiz / süresi dolmuşsa
// yine siler ve false döner. nil-güvenli.
func (st *chatContextState) takePending(now time.Time) (*chatPendingAsk, bool) {
	if st == nil || st.ctx.Pending == nil {
		return nil, false
	}
	p := st.ctx.Pending
	st.ctx.Pending, st.dirty = nil, true
	if !pendingAskValid(p, now) {
		return nil, false
	}
	return p, true
}

// canRemember — bu konuşmanın bekleyen sorusu bir sonraki tura taşınabilir
// mi (kullanıcı + önbellek). Taşınamıyorsa soru sorulmaz: cevabı anlaşılmaz.
func (s *Server) canRemember(st *chatContextState) bool {
	return st != nil && st.user != "" && s != nil && s.cache != nil
}

// sourceTelemetryNouns — kaynak sorusunu tetikleyen telemetri adları (önek).
var sourceTelemetryNouns = []string{"servis", "service", "takım", "takim", "ekip", "team", "hata", "error",
	"problem", "trace", "log", "operasyon", "operation", "endpoint", "exception", "istisna", "latency",
	"gecikme", "yavaş", "yavas", "deploy", "pod", "alarm", "alert", "incident", "metrik", "metric",
	"sağlık", "saglik", "sorun", "arıza", "ariza"}

// sourceAmbiguous — SAF: soru telemetri şekilli ama açık bir wiki işareti ya
// da trace kimliği taşımıyor mu (iki kaynak da makul cevap verebilir).
// Yalnız telemetri ADLARI sayılır; guidedTelemetrySignal BİLEREK yok — onun
// "nasıl / neden / iyi" kökleri "merhaba nasılsın" gibi genel soruları da
// kaynak sorusuna sokardı (genel cevap niyet kademesinde kalmalı).
func sourceAmbiguous(question string) bool {
	q := strings.TrimSpace(question)
	if q == "" {
		return false
	}
	if strong, weak := wikiQuestionCue(q); strong || weak {
		return false
	}
	if extractTraceID(q) != "" {
		return false
	}
	toks := guidedTokens(normalizeGuidedMsg(q))
	if len(toks) < 2 {
		return false
	}
	return tokenHasPrefix(toks, sourceTelemetryNouns...)
}

// sourceReplyFillers — kaynak cevabında kaynak adının yanında geçebilen dolgu
// sözcükleri (katlanmış; chat_source_ask.go sourceChoiceFillers bunu genişletir).
var sourceReplyFillers = map[string]bool{"de": true, "da": true, "ara": true, "arayin": true, "bak": true, "bakin": true,
	"once": true, "ilk": true, "lutfen": true, "evet": true, "tamam": true, "ok": true, "olur": true, "ise": true,
	"icin": true, "mi": true, "mu": true, "misin": true, "orada": true, "oradan": true, "kaynak": true,
	"tarafinda": true, "uzerinden": true, "icinde": true, "in": true, "the": true, "please": true, "search": true}

// chatSourceAskTier — kaynak sorusu (telemetri adlı soru guided'a oturmadı,
// wiki gerçek bir alternatif). Kapılardan biri kapalıysa akış bayt bayt eski.
// v0.10.2381 — soru tek kurucudan; hatırlanan seçim varsa sormadan o kaynağa.
func (s *Server) chatSourceAskTier(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, tc wikiTierContext, cst *chatContextState) sourceRoute {
	out := sourceRoute{ctx: ctx, msgs: msgs, emit: emit}
	if !s.canRemember(cst) || !wikiTierAllowed(tc) || forcedChatSource(ctx) != "" {
		return out
	}
	q := strings.TrimSpace(lastUserText(msgs))
	if !sourceAmbiguous(q) {
		return out
	}
	avail := s.sourceAvail(ctx, q, false)
	if !avail.Wiki { // tetik bugünkü: wiki açık + oturumlu kullanıcı + yerel profil
		return out
	}
	res := s.sourceAskOrReuse(ctx, emit, cst, sourceAskSite{Route: "source_ask", Question: q, Avail: avail, NeedMemory: true})
	return s.finishSourceAsk(ctx, emit, msgs, q, nil, res)
}

// clarifyReply — cevap turunun sonucu.
type clarifyReply struct {
	ctx     context.Context
	msgs    []copilot.ChatMessage
	emit    func(string, any) // v0.10.2381 — zincir sürerse (telemetri) diğer kaynak çipleriyle
	tier    string            // "" = işlenmedi (zincir sürer); aisurface.Tier* adı
	handled bool
	ok      bool
}

// withForcedChatSource — /telemetri komutunun işareti (chat_source_discovery.go).
func withForcedChatSource(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, forcedSourceKey{}, kind)
}

// chatClarifyReplyTier — bekleyen soru varsa bu turun metnini cevap olarak
// yorumlar. Bekleyen soru yoksa ctx / msgs aynen döner.
//
// v0.10.2102 (D2) — option: v2 soru kartının tıkı (context.clarifyOption);
// bekleyen sorunun kümesindeyse BİREBİR o sınıf, değilse metin yolu aynen.
func (s *Server) chatClarifyReplyTier(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, cst *chatContextState, loc *time.Location, option string) clarifyReply {
	out := clarifyReply{ctx: ctx, msgs: msgs, emit: emit}
	if cst.takeGeneral(time.Now()) { // v0.10.2381 — önceki cevap genel bilgiydi (chat_source_ask.go)
		ctx = withPrevGeneral(ctx)
		out.ctx = ctx
	}
	p, ok := cst.takePending(time.Now())
	if !ok {
		return out
	}
	reply := lastUserText(msgs)
	class, picked := "", false
	if cosreV2Marked(ctx) { // v1'e yeni davranış yok: seçenek yalnız v2 ucunda okunur
		class, picked = clarifyOptionClass(p, option)
	}
	if !picked {
		class = clarifyReplyClass(p, reply)
	}
	if p.Kind == pendingKindSlot { // v0.10.2332 — seçenek kümesine doğrulanır (kart tıkı ya da serbest metin)
		class = slotReplyClass(p, option, reply)
	}
	args, _ := json.Marshal(map[string]string{"kind": p.Kind, "reply": class})
	if class == clarifyReplyDropped {
		// v0.10.1575 — düşen soru EKRANDA adım çizmez ("Nasıl cevapladım"da
		// gürültüydü: kullanıcı yeni bir soru sordu); ölçüm için yalnız
		// etkileşim satırına yazılır (aynı tool adı + args, kimlik -1).
		if x := chatExchangeFrom(ctx); x != nil {
			x.noteStep(map[string]any{"tool": clarifyReplyTool, "args": string(args), "i": -1}, time.Now())
		}
	} else {
		n := emitGuidedStep(emit, clarifyReplyTool, string(args))
		emitGuidedStepResult(emit, n, clarifyReplyTool, clarifyReplyEvidenceTR(p, class), nil)
	}
	switch {
	case p.Kind == pendingKindSource && class != clarifyReplyDropped: // v0.10.2381 — tek kaynak sorusunun cevabı
		cst.rememberSource(class, time.Now())
		r := s.routeSourceChoice(ctx, emit, msgs, class, p.Question, sourceAltChips(pendingSourceOptions(p), class, nil))
		out.ctx, out.msgs, out.emit = r.ctx, r.msgs, r.emit
		out.handled, out.ok, out.tier = r.handled, r.ok, r.tier
	case p.Kind == pendingKindTraceSynth && class == synthReplyYes: // v0.10.1572 dilim 2 (chat_clarify_synth.go)
		out.handled, out.ok = s.chatTraceSynthesisAnswer(ctx, emit, p, loc)
		out.tier = "operation"
	case p.Kind == pendingKindSlot && class != clarifyReplyDropped: // v0.10.2332 — özgün soru seçilen servisle
		out.msgs = replaceLastUserText(msgs, slotRewriteQuestion(p.Question, class))
	case p.Kind == pendingKindTraceSynth && class == synthReplyNo:
		chatExchangeFrom(ctx).noteRoute("trace_declined")
		emit("answer", map[string]any{"text": synthDeclinedTR, "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
		out.tier, out.handled, out.ok = "clarify", true, true
	}
	return out
}

// clarifyReplyClass — SAF: bekleyen sorunun türüne göre cevabın sınıfı;
// tanınmayan cevap clarifyReplyDropped.
func clarifyReplyClass(p *chatPendingAsk, reply string) string {
	if p != nil && p.Kind == pendingKindSource {
		if c := classifySourceChoice(reply, pendingSourceOptions(p)); c != "" {
			return c
		}
	}
	if p != nil && p.Kind == pendingKindTraceSynth {
		return synthReplyClass(reply)
	}
	return clarifyReplyDropped
}

// clarifyReplyEvidenceTR — SAF: adım çipinin kanıt metni.
func clarifyReplyEvidenceTR(p *chatPendingAsk, class string) string {
	q := p.Question
	if q == "" {
		q = p.Operation
	}
	what := "bekleyen soru düşürüldü; bu tur yeni bir soru olarak cevaplanıyor"
	switch {
	case p.Kind == pendingKindSource && class == srcWiki:
		what = "özgün soru wiki dokümanlarında aranıyor"
	case p.Kind == pendingKindSource && class == srcTelemetry:
		what = "özgün soru Coremetry verisinde cevaplanıyor"
	case p.Kind == pendingKindSource && class == srcExcel:
		what = "özgün soru yüklü envanter tablolarında aranıyor"
	case p.Kind == pendingKindSource && class == srcExceptions:
		what = "özgün soru Exceptions gruplarında aranıyor"
	case p.Kind == pendingKindSource && class == srcGeneral:
		what = "özgün soru genel bilgiyle cevaplanıyor"
	case class == synthReplyYes:
		what = "Oracle açıklaması trace " + p.TraceID + " ile sentezleniyor"
	case class == synthReplyNo:
		what = "trace incelemesi atlandı"
	}
	if p.Kind == pendingKindSlot && class != clarifyReplyDropped { // v0.10.2332
		what = "seçilen servisle (" + class + ") özgün soru yeniden cevaplanıyor"
	}
	return "bekleyen soru (" + p.Kind + "): " + clipRunes(q, 200) + "\ncevap: " + class + " — " + what
}
