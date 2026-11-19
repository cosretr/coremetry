package api

// chat_profile_split.go — v0.10.2365 (docs/cosre/investigation-design.md S6
// §4.5; kararlar 21–24): araştırma / anlatım profil ayrımı.
//
// Yeni yüzey `chat-research` mevcut yüzey haritasıyla çözülür (Ayarlar › AI
// profilleri, `research` grubu). Eşlenmemişse ya da etkinleşme koşulu
// tutmazsa alışveriş bugünkü tek profilli yoldur: plan nil, istek baytı aynı.
//
// Etkinken:
//   - normal araçlı turlar (stepNormal) araştırma profiliyle koşar; metinleri
//     istemciye AKMAZ (durum satırı ve çipler akar);
//   - araştırma modeli araçsız dönünce metni BULGU NOTU olur (≤600 B,
//     gösterilmez) ve sohbet profilinde araçsız, akan TEK anlatım çağrısı
//     yapılır; tavan turunda araştırma taslağı atlanır;
//   - anlatıcı araç mesajı görmez: son dört turun metni (≤1,5 KB) + soru +
//     ÖZETLER (bu alışverişin araç sonuçları, yeniden eskiye, sonuç başına
//     ≤900 B, toplam ≤6 KB) + BULGU NOTU;
//   - geri düşüşler: araştırma çağrısı (ağ geçidinin tek yeniden denemesinden
//     sonra) başarısız → kalan turlar sohbet profilinde, BİR kez
//     (research_fallback); ilk araştırma turu araç çağırmazsa tur sohbet
//     profilinde araçlı ve akan normal tura düşer (research_no_tools); anlatım
//     başarısız → bulgu notu cevap olur (narrate_fallback).
//   - kayıt: tek ai_calls satırı; model = anlatım profili; araştırma modeli ve
//     token'ı loop_research_model / loop_research_tokens'a.
// Model ailesine özgü hiçbir dal yoktur.

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// Bütçeler (§4.5 "Anlatıcının girdisi").
const (
	splitNoteMax      = 600
	splitDigestEach   = 900 // v0.10.2378 — özet ve geçmiş tavanları TABAN: kanıt ölçeğiyle çarpılır
	splitDigestTotal  = 6 * 1024
	splitHistoryMax   = 1536
	splitHistoryTurns = 4
)

// Sinyaller — etkileşim satırının ölçüm adımı (ekranda çizilmez, i=-1).
const (
	splitSignalTool       = "profile_split"
	splitResearchNoTools  = "research_no_tools"
	splitResearchFallback = "research_fallback"
	splitNarrateFallback  = "narrate_fallback"
)

// splitResearchFailLabel — araştırma çağrısı düştüğünde operatörün gördüğü çip.
const splitResearchFailLabel = "araştırma modeli yanıt vermedi — sohbet profiliyle sürüyor"

// profileSplit — alışverişin ayrım durumu (döngü tek goroutine).
type profileSplit struct {
	research      string // araştırma profil kimliği
	researchModel string
	on            bool // false = geri düşüldü; kalan turlar sohbet profilinde
	called        bool // araştırma modeli bu alışverişte araç çağırdı
	used          bool // en az bir araştırma çağrısı yapıldı
	tokens        uint32
	history       string // son dört turun metni (≤1,5 KB)
	question      string
	narrator      string // anlatıcının sistem istemi (üslup çekirdeği + durum bloğu)
	onSignal      func(string)
}

type profileSplitKey struct{}

func withProfileSplit(ctx context.Context, sp *profileSplit) context.Context {
	if sp == nil {
		return ctx
	}
	return context.WithValue(ctx, profileSplitKey{}, sp)
}

func profileSplitFrom(ctx context.Context) *profileSplit {
	sp, _ := ctx.Value(profileSplitKey{}).(*profileSplit)
	return sp
}

// splitGateIn — etkinleşme tablosunun girdisi (§4.5 "Etkinleşme").
type splitGateIn struct {
	Research, Chat string // eşli araştırma profili; sohbetin çözüldüğü profil
	Explicit       bool   // açık profil seçimi (sohbet seçicisi / değerlendirme sabitlemesi)
	Deep           bool
	TraceFollowUp  bool
	CodeTurn       bool // kod turu alışverişi yerel profile sabitler
	Class          answerClass
}

// splitActive — SAF: ayrım bu alışverişte açılır mı.
func splitActive(in splitGateIn) bool {
	if in.Research == "" || in.Research == in.Chat || in.Explicit || in.Deep || in.TraceFollowUp || in.CodeTurn {
		return false
	}
	switch in.Class {
	case classStatus, classErrorHunt, classCompare, classExplain:
		return true
	}
	return false
}

// planProfileSplit — alışveriş başına bir kez, araç seti çözülmeden ÖNCE.
// Değerlendirmede eşleme canlı haritadan değil yalnız koşunun
// researchProfile alanından okunur (üretimde evm nil → alan okunmaz).
func (s *Server) planProfileSplit(ctx context.Context, req *chatRequest, evm *chatEvalMode, st ChatContext, deep, traceFU, codeTurn, report bool) *profileSplit {
	if s == nil || s.copilot == nil {
		return nil
	}
	in := splitGateIn{
		Research: s.copilot.MappedSurfaceProfile(copilot.SurfaceChatResearch),
		Chat:     s.copilot.ProfileIDFor(ctx),
		Explicit: strings.TrimSpace(req.Context.Profile) != "",
		Deep:     deep, TraceFollowUp: traceFU, CodeTurn: codeTurn,
		Class: answerClassHint(lastUserText(req.Messages), st, report),
	}
	if evm != nil {
		in.Research, in.Explicit = "", false
		if _, _, _, _, ok := s.copilot.ProfileView(evm.ResearchProfile); ok {
			in.Research = evm.ResearchProfile
		}
	}
	if !splitActive(in) {
		return nil
	}
	sp := &profileSplit{research: in.Research, on: true, question: lastUserText(req.Messages),
		history: splitHistoryScaled(req.Messages, s.evidenceScale(ctx))} // v0.10.2378 — anlatım profilinin kanıt ölçeği
	sp.researchModel = s.copilot.ModelFor(copilot.WithProfile(ctx, in.Research))
	if evm != nil {
		sp.onSignal = evm.OnSplit
	}
	return sp
}

// live — ayrım hâlâ açık mı (nil güvenli).
func (sp *profileSplit) live() bool { return sp != nil && sp.on }

// toolsetFor — araç seti araştırma profiline göre seçilir (kataloğu o görür);
// ayrım kapalıyken cur aynen.
func (sp *profileSplit) toolsetFor(ctx context.Context, s *Server, cur string) string {
	if !sp.live() {
		return cur
	}
	return s.copilot.ToolsetFor(copilot.WithProfile(ctx, sp.research))
}

// dropForLocal — alışveriş yerel profile sabitlendiyse ayrım kapanır; ayrım
// açıktıysa true (çağıran kataloğu sohbet profilinin araç setiyle kurar).
func (sp *profileSplit) dropForLocal(pinned bool) bool {
	if !pinned || !sp.live() {
		return false
	}
	sp.on = false
	return true
}

// setNarrator — anlatıcının sistem istemi (döngü istemi kurulduktan sonra).
func (sp *profileSplit) setNarrator(sys string) {
	if sp != nil {
		sp.narrator = sys
	}
}

// mark — döngü satırının araştırma alanları.
func (sp *profileSplit) mark(lu copilot.LoopUsage) copilot.LoopUsage {
	if sp != nil && sp.used {
		lu.ResearchModel, lu.ResearchTokens = sp.researchModel, sp.tokens
	}
	return lu
}

// signal — ölçüm adımı + değerlendirme bildirimi.
func (sp *profileSplit) signal(ctx context.Context, sig string) {
	if x := chatExchangeFrom(ctx); x != nil {
		args, _ := json.Marshal(map[string]string{"signal": sig})
		x.noteStep(map[string]any{"tool": splitSignalTool, "args": string(args), "i": -1}, time.Now())
	}
	if sp.onSignal != nil {
		sp.onSignal(sig)
	}
}

// chipModelRankingAllowed — interaction-audit karar 3 kancası: çiplerin
// modelce sıralanması yalnız profil ayrımı eşliyken açılabilir. v0.10.2365'te
// sıralama YOK; çipler deterministik şablonla kalır.
func chipModelRankingAllowed(sp *profileSplit) bool { return sp != nil && sp.research != "" }

// splitLoopTurn — loopTurn'ün ayrım kapısı; done=false → bugünkü yol.
func (s *Server) splitLoopTurn(ctx context.Context, prompt string, in stepInput, emit func(string, any)) (copilot.ChatTurn, string, error, bool) {
	sp := profileSplitFrom(ctx)
	if !sp.live() {
		return copilot.ChatTurn{}, "", nil, false
	}
	switch in.Kind {
	case stepCap: // tavan turu: araştırma taslağı atlanır, doğrudan anlatım
		t, err := s.splitNarrate(ctx, in.Conv, emit, sp, "")
		return t, prompt, err, true
	case stepNormal:
	default:
		return copilot.ChatTurn{}, "", nil, false
	}
	sp.used = true
	rt, sys, err := s.loopTurnOne(copilot.WithProfile(ctx, sp.research), prompt, in, splitMuteDeltas(emit))
	sp.tokens += rt.InputTokens + rt.OutputTokens
	switch {
	case err != nil && ctx.Err() != nil:
		return rt, sys, err, true
	case err != nil: // ağ geçidinin tek yeniden denemesinden sonra: kalan turlar sohbet profilinde
		sp.on = false
		sp.signal(ctx, splitResearchFallback)
		emit("step", map[string]string{"label": splitResearchFailLabel})
		splitDropReplay(in.Conv)
		t, sys2, err2 := s.loopTurnOne(ctx, prompt, in, emit)
		return t, sys2, err2, true
	case len(rt.ToolCalls) > 0:
		sp.called = true
		return rt, sys, nil, true
	case !sp.called: // ilk araştırma turu araç çağırmadı → bugünkü tek profilli tur
		sp.on = false
		sp.signal(ctx, splitResearchNoTools)
		splitDropReplay(in.Conv)
		t, sys2, err2 := s.loopTurnOne(ctx, prompt, in, emit)
		return splitAddTokens(t, rt), sys2, err2, true
	}
	t, err := s.splitNarrate(ctx, in.Conv, emit, sp, splitClip(strings.TrimSpace(rt.Text), splitNoteMax))
	return splitAddTokens(t, rt), sys, err, true
}

// splitNarrate — sohbet profilinde araçsız, akan tek anlatım çağrısı.
func (s *Server) splitNarrate(ctx context.Context, conv []copilot.ChatMessage, emit func(string, any), sp *profileSplit, note string) (copilot.ChatTurn, error) {
	plan := answerClassLoopPlan(ctx, stepPlan{}) // örnekler + sınıf satırı EN SONA, tavan ctx'e
	sys := sp.narrator + copilot.ChatNarrateAddendum() + plan.SystemAddendum
	nctx := copilot.WithToolless(plan.planCtx(ctx))
	msgs := []copilot.ChatMessage{{Role: "user", Text: copilot.ChatNarrateUser(sp.history, sp.question, splitDigestsScaled(conv, s.evidenceScale(ctx)), note)}} // v0.10.2378 — anlatım profilinin kanıt ölçeği
	turn, err := s.chatLoopTurn(nctx, sys, msgs, nil, emit)
	turn.ToolCalls = nil // araçsız çağrı; metinden ayrışan çağrı da yürümez
	if err == nil {
		seamNoteLoopCall(ctx, s, nctx, sys, msgs, nil, turn.Text)
		return turn, nil
	}
	if ctx.Err() != nil || note == "" {
		return turn, err
	}
	sp.signal(ctx, splitNarrateFallback)
	turn.Text = note // cevap dikişinden geçip yayınlanır
	return turn, nil
}

// splitMuteDeltas — araştırma turunun metni istemciye akmaz.
func splitMuteDeltas(emit func(string, any)) func(string, any) {
	return func(ev string, v any) {
		if ev == "delta" {
			return
		}
		emit(ev, v)
	}
}

// splitDropReplay — profil değişince sağlayıcıya özgü yeniden oynatma
// yükleri (ham asistan içeriği, çağrı imzası) yalnız onları üreten profile
// gider: konuşmanın ortak dizisinde yerinde temizlenir.
func splitDropReplay(conv []copilot.ChatMessage) {
	for i := range conv {
		conv[i].RawContent = nil
		for j := range conv[i].ToolCalls {
			conv[i].ToolCalls[j].Raw = nil
		}
	}
}

// splitAddTokens — tek ai_calls satırı toplamı taşır: önceki çağrının token'ı eklenir.
func splitAddTokens(t, prev copilot.ChatTurn) copilot.ChatTurn {
	t.InputTokens += prev.InputTokens
	t.OutputTokens += prev.OutputTokens
	t.CachedTokens += prev.CachedTokens
	return t
}

// splitHistory — SAF: soru hariç son dört turun metni, yeniden eskiye
// sığdırılır, eskiden yeniye yazılır (≤1,5 KB).
func splitHistory(msgs []copilot.ChatMessage) string {
	return splitHistoryScaled(msgs, 1)
}

// splitHistoryScaled — v0.10.2378: bayt bütçesi anlatım (sohbet) profilinin
// kanıt ölçeğiyle (evidence_scale.go); tur tavanı aynı. Ölçek 1 bayt bayt eski.
func splitHistoryScaled(msgs []copilot.ChatMessage, scale int) string {
	if len(msgs) > 0 {
		msgs = msgs[:len(msgs)-1]
	}
	var lines []string
	left := scaleCap(splitHistoryMax, scale)
	for i := len(msgs) - 1; i >= 0 && len(lines) < splitHistoryTurns && left > 0; i-- {
		t := strings.TrimSpace(msgs[i].Text)
		if t == "" {
			continue
		}
		who := "Kullanıcı: "
		if msgs[i].Role == "assistant" {
			who = "Asistan: "
		}
		line := splitClip(who+t, left)
		left -= len(line) + 1
		lines = append(lines, line)
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}

// splitDigests — SAF: konuşmadaki araç sonuçlarının modelin gördüğü hâli,
// yeniden eskiye, sonuç başına ≤900 B, toplam ≤6 KB.
func splitDigests(conv []copilot.ChatMessage) string {
	return splitDigestsScaled(conv, 1)
}

// splitDigestsScaled — v0.10.2378: sonuç başı ve toplam özet tavanı anlatım
// profilinin kanıt ölçeğiyle (evidence_scale.go). Ölçek 1 bayt bayt eski.
func splitDigestsScaled(conv []copilot.ChatMessage, scale int) string {
	each, total := scaleCap(splitDigestEach, scale), scaleCap(splitDigestTotal, scale)
	var b strings.Builder
	for i := len(conv) - 1; i >= 0; i-- {
		rs := conv[i].ToolResults
		for j := len(rs) - 1; j >= 0; j-- {
			d := splitClip(rs[j].Name+": "+strings.TrimSpace(rs[j].Content), each)
			if b.Len()+len(d)+1 > total {
				return b.String()
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(d)
		}
	}
	return b.String()
}

// splitClip — SAF: en çok n bayt, rune sınırında.
func splitClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
