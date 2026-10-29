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
// profil; armDisambigRescue ile aynı üçlü) yalnız sourceAskTextTR'yi sorar.
// Cevap "wiki" → ÖZGÜN soru wikide (wikiForcedAnswer, /wiki ile aynı yol);
// "Coremetry / veri / servis…" → özgün soru telemetri zorlamasıyla zincirde
// (/telemetri ile aynı işaret: tablo / wiki / keşif atlanır). Cevap bu iki
// sınıftan değilse bekleyen soru düşer, tur bayt bayt eski akışta sürer.
//
// Kayıt: soru turu cosre-turn:clarify/source_ask (LLM'siz); cevap turu
// clarify_reply adım çipi (args: kind + reply sınıfı), satırın kademesi
// gerçekten cevaplayan kademe. Şema değişikliği yok.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
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

	sourceReplyWiki      = srcWiki
	sourceReplyTelemetry = srcTelemetry
	clarifyReplyDropped  = "dropped"
)

// sourceAskTextTR — kaynak sorusunun deterministik metni (LLM yok).
const sourceAskTextTR = "Bunu Coremetry verisinde mi arayayım (servis sağlığı, hatalar, trace'ler), yoksa wiki dokümanlarında mı?"

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

var (
	sourceReplyWikiStems = []string{"wiki", "dokuman", "dokumantasyon", "runbook", "belge", "docs", "documentation"}
	sourceReplyTelStems  = []string{"coremetry", "telemetri", "veri", "servis", "canli", "trace", "metrik", "log", "hata"}
	sourceReplyFillers   = map[string]bool{"de": true, "da": true, "ara": true, "arayin": true, "bak": true, "bakin": true,
		"once": true, "ilk": true, "lutfen": true, "evet": true, "tamam": true, "ok": true, "olur": true, "ise": true,
		"icin": true, "mi": true, "mu": true, "misin": true, "orada": true, "oradan": true, "kaynak": true,
		"tarafinda": true, "uzerinden": true, "icinde": true, "in": true, "the": true, "please": true, "search": true}
)

func foldedHasPrefix(f string, stems []string) bool {
	for _, s := range stems {
		if strings.HasPrefix(f, s) {
			return true
		}
	}
	return false
}

// classifySourceReply — SAF: kaynak sorusuna verilen serbest metin cevabın
// sınıfı: "wiki" | "telemetri" | "" (cevap değil). 1–6 sözcük; her sözcük
// tek bir listeye önekle uyar ya da dolgu sözcüğüdür; iki liste birden → "".
func classifySourceReply(text string) string {
	toks := memTokens(text)
	if len(toks) == 0 || len(toks) > 6 {
		return ""
	}
	var wikiHit, telHit bool
	for _, t := range toks {
		w, tm := foldedHasPrefix(t.f, sourceReplyWikiStems), foldedHasPrefix(t.f, sourceReplyTelStems)
		switch {
		case w && tm:
			return ""
		case w:
			wikiHit = true
		case tm:
			telHit = true
		case sourceReplyFillers[t.f]:
		default:
			return ""
		}
	}
	switch {
	case wikiHit && telHit:
		return ""
	case wikiHit:
		return sourceReplyWiki
	case telHit:
		return sourceReplyTelemetry
	}
	return ""
}

// chatSourceAskTier — kaynak sorusu. Kapılardan biri kapalıysa false ve akış
// bayt bayt eski.
func (s *Server) chatSourceAskTier(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, tc wikiTierContext, cst *chatContextState) bool {
	if !s.canRemember(cst) || !wikiTierAllowed(tc) || forcedChatSource(ctx) != "" {
		return false
	}
	q := strings.TrimSpace(lastUserText(msgs))
	if !sourceAmbiguous(q) {
		return false
	}
	w := wikiKB()
	if w == nil || !w.Enabled() || !sourceCodeCallerAllowed(auth.FromContext(ctx)) || !s.localOnlyAvailable(ctx) {
		return false
	}
	cst.setPending(&chatPendingAsk{Kind: pendingKindSource, Question: clipRunes(q, chatPendingQuestionMax), AskedAt: time.Now().UnixNano()})
	chatExchangeFrom(ctx).noteRoute("source_ask")
	emit("answer", map[string]any{"text": sourceAskTextTR, "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
	return true
}

// clarifyReply — cevap turunun sonucu.
type clarifyReply struct {
	ctx     context.Context
	msgs    []copilot.ChatMessage
	tier    string // "" = işlenmedi (zincir sürer); aisurface.Tier* adı
	handled bool
	ok      bool
}

// withForcedChatSource — /telemetri komutunun işareti (chat_source_discovery.go).
func withForcedChatSource(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, forcedSourceKey{}, kind)
}

// chatClarifyReplyTier — bekleyen soru varsa bu turun metnini cevap olarak
// yorumlar. Bekleyen soru yoksa ctx / msgs aynen döner.
func (s *Server) chatClarifyReplyTier(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, cst *chatContextState) clarifyReply {
	out := clarifyReply{ctx: ctx, msgs: msgs}
	p, ok := cst.takePending(time.Now())
	if !ok {
		return out
	}
	reply := lastUserText(msgs)
	class := clarifyReplyClass(p, reply)
	args, _ := json.Marshal(map[string]string{"kind": p.Kind, "reply": class})
	n := emitGuidedStep(emit, clarifyReplyTool, string(args))
	emitGuidedStepResult(emit, n, clarifyReplyTool, clarifyReplyEvidenceTR(p, class), nil)
	switch {
	case p.Kind == pendingKindSource && class == sourceReplyWiki:
		out.msgs = replaceLastUserText(msgs, p.Question)
		out.ok = s.wikiForcedAnswer(ctx, emit, out.msgs, p.Question)
		out.tier, out.handled = "wiki", true
	case p.Kind == pendingKindSource && class == sourceReplyTelemetry:
		out.msgs = replaceLastUserText(msgs, p.Question)
		out.ctx = withRecordsQuestion(withForcedChatSource(ctx, srcTelemetry), p.Question)
	}
	return out
}

// clarifyReplyClass — SAF: bekleyen sorunun türüne göre cevabın sınıfı;
// tanınmayan cevap clarifyReplyDropped.
func clarifyReplyClass(p *chatPendingAsk, reply string) string {
	if p != nil && p.Kind == pendingKindSource {
		if c := classifySourceReply(reply); c != "" {
			return c
		}
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
	switch class {
	case sourceReplyWiki:
		what = "özgün soru wiki dokümanlarında aranıyor"
	case sourceReplyTelemetry:
		what = "özgün soru Coremetry verisinde cevaplanıyor"
	}
	return "bekleyen soru (" + p.Kind + "): " + clipRunes(q, 200) + "\ncevap: " + class + " — " + what
}
