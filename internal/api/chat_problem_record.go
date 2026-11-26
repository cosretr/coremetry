package api

// chat_problem_record.go — v0.10.2477: Problem kapsamlı sohbetin İLK turu
// kayıttan açılır.
//
// Sohbet Problem sayfasından (sayfa bağlamı problemId) açıldığında, ilk turda
// kayıtlı araştırma (bulgular, Problem grafı kökeni, etki olayları, hipotezler
// + durumları, onaylı benzer vakalar) çitli VERİ bloğu olarak deterministik
// verilir (sre.RenderChatRecordTR, ≤ sre.ChatRecordMaxBytes):
//
//   - serbest araç döngüsünde sentetik `saved_investigation` çağrısı + sonucu
//     (guided_prefetch tohumlama deseni, chat_guided_escalate.go);
//   - guided anlatımında kanıtın sonuna ek bölüm (kök-neden rotası kendi
//     "KAYITLI ARAŞTIRMA" bloğunu zaten taşıyorsa eklenmez — çift kayıt yok).
//
// Kayıt yoksa / bitmemişse görünür adım çipi söyler ve tur bugünkü gibi sürer
// (blok yok). Yalnız okuma; önbellek yok (tek satır, ilk tur).

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/sre"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

const (
	problemRecordTool   = "saved_investigation"
	problemRecordToolID = "cosre-saved-investigation"
	problemRecordRead   = 2 * time.Second
)

// chatProblemRecord — ilk turun kayıt bloğu (nil = yok).
type chatProblemRecord struct {
	ProblemID string
	Block     string
	// v0.10.2480 (B) — kayıttan karşılama: sayfadaki kimlik, kaydın bittiği an
	// ve Problem başlangıcı (unix ns).
	Ref        string
	FinishedAt int64
	ProblemAt  int64
}

// v0.10.2480 (B) — kayıttan karşılanan çağrılar. Aynı Problem için
// get_problem / get_problem_root_cause / get_correlation_evidence, kayıt
// tazeyse YENİDEN KOŞMAZ: sonuç "kayıttan" (bütçeden düşmez). Tazelik sınırı:
//   - kök neden + korelasyon (başlangıç analizi): kayıt Problem başlangıcından
//     SONRA bitmiş ve ≤ recordFreshOnset (24 sa) eski — başlangıç penceresinin
//     analizi pencere geçtikten sonra değişmez;
//   - get_problem (canlı durum / değer): ≤ recordFreshLive (15 dk).
const (
	recordFreshOnset = 24 * time.Hour
	recordFreshLive  = 15 * time.Minute
)

// recordServedTools — kayıttan karşılanabilen araçlar → canlı durum mu.
var recordServedTools = map[string]bool{
	"get_problem":              true,
	"get_problem_root_cause":   false,
	"get_correlation_evidence": false,
}

// recordFresh — SAF: kayıt bu araç için taze mi.
func recordFresh(tool string, finishedAt, problemAt int64, now time.Time) bool {
	live, ok := recordServedTools[tool]
	if !ok || finishedAt <= 0 {
		return false
	}
	age := now.Sub(time.Unix(0, finishedAt))
	if age < 0 {
		age = 0
	}
	if live {
		return age <= recordFreshLive
	}
	return finishedAt >= problemAt && age <= recordFreshOnset
}

// serve — SAF: aynı Problem için taze kayıtla karşılanan çağrının sonucu
// (ok=false → araç bugünkü gibi koşar).
func (r *chatProblemRecord) serve(tool string, args []byte, now time.Time) (string, bool) {
	if r == nil {
		return "", false
	}
	var a struct {
		ProblemID string `json:"problem_id"`
	}
	if json.Unmarshal(args, &a) != nil {
		return "", false
	}
	id := strings.TrimSpace(a.ProblemID)
	if id == "" || !(strings.EqualFold(id, r.ProblemID) || (r.Ref != "" && strings.EqualFold(id, r.Ref))) {
		return "", false
	}
	if !recordFresh(tool, r.FinishedAt, r.ProblemAt, now) {
		return "", false
	}
	ageMin := int(now.Sub(time.Unix(0, r.FinishedAt)) / time.Minute)
	b, _ := json.Marshal(map[string]any{
		"served_from": problemRecordTool, "problem_id": id, "record_age_min": max(ageMin, 0),
		"note": "Bu Problem için istenen bilgi yukarıdaki saved_investigation sonucunda; yeniden okunmadı. Kayıtta olmayan bir şey gerekiyorsa başka bir araç çağır.",
	})
	return string(b), true
}

// recordCoverageTR — SAF: tohum bloğunun neyi kapsadığı + kayıttan karşılanan
// araçlar (talimat satırı; çitin dışında, kısa).
func recordCoverageTR(inv sre.Investigation) string {
	var parts []string
	if len(inv.Findings) > 0 {
		parts = append(parts, "bulgular")
	}
	if len(inv.Hypotheses) > 0 {
		parts = append(parts, "kök neden hipotezleri")
	}
	if f := inv.Facts; f != nil {
		if f.GraphOrigin != nil {
			parts = append(parts, "Problem grafı kökeni")
		}
		if f.Impact != nil && len(f.Impact.Rows) > 0 {
			parts = append(parts, "etki olayları")
		}
		if f.Similar != nil && len(f.Similar.Cases) > 0 {
			parts = append(parts, "benzer vakalar")
		}
	}
	if inv.Summary != nil && !inv.Summary.Gated && strings.TrimSpace(inv.Summary.Text) != "" {
		parts = append(parts, "ajan özeti")
	}
	if len(parts) == 0 {
		return ""
	}
	return "KAYIT KAPSAMI: " + strings.Join(parts, ", ") + ". get_problem_root_cause ve get_correlation_evidence bu Problem için kayıttan karşılanır; araçları yalnız kayıtta OLMAYAN için çağır.\n"
}

// firstChatTurn — SAF: konuşmada tek kullanıcı mesajı var, asistan cevabı yok.
func firstChatTurn(msgs []copilot.ChatMessage) bool {
	users := 0
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if strings.TrimSpace(m.Text) != "" {
				users++
			}
		case "assistant":
			return false
		}
	}
	return users == 1
}

// problemRecordStepTR — SAF: görünür adım çipi.
func problemRecordStepTR(found bool, state string) string {
	switch {
	case found:
		return "kayıtlı araştırmadan başlandı"
	case state == sre.StateQueued || state == sre.StateRunning:
		return "araştırma sürüyor — kayıt henüz yok, araçlarla bakılıyor"
	}
	return "bu Problem için kayıtlı araştırma yok — araçlarla bakılıyor"
}

// chatProblemRecordFor — G/Ç: sayfa Problem'i + ilk tur → blok. Okuma hatası
// sessizce "kayıt yok" (tur bugünkü gibi).
func (s *Server) chatProblemRecordFor(ctx context.Context, emit func(string, any), page *agentctx.PageContext, msgs []copilot.ChatMessage) *chatProblemRecord {
	if page == nil || strings.TrimSpace(page.ProblemID) == "" || !firstChatTurn(msgs) {
		return nil
	}
	st := s.sreReadStore()
	if st == nil {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, problemRecordRead)
	defer cancel()
	id, ref := page.ProblemID, page.ProblemID
	if chstore.IsProblemDisplayID(id) && s.store != nil {
		p, err := s.store.GetProblemByDisplayID(c, id)
		if err != nil || p == nil || p.ID == "" {
			emitGuidedContextStep(emit, problemRecordStepTR(false, ""))
			return nil
		}
		id = p.ID
	}
	inv, ok, err := sre.Load(c, st, id)
	if err == nil && ok && inv.State == sre.StateShared && inv.SharedFrom != "" {
		if src, sok, serr := sre.Load(c, st, inv.SharedFrom); serr == nil && sok {
			src.State = sre.StateShared
			inv = src
		}
	}
	if err != nil || !ok {
		emitGuidedContextStep(emit, problemRecordStepTR(false, ""))
		return nil
	}
	block := sre.RenderChatRecordTR(inv, ref, tzdefault.Location(), sre.ChatRecordMaxBytes)
	if block == "" {
		emitGuidedContextStep(emit, problemRecordStepTR(false, inv.State))
		return nil
	}
	emitGuidedContextStep(emit, problemRecordStepTR(true, inv.State))
	return &chatProblemRecord{ProblemID: id, Block: recordCoverageTR(inv) + block, Ref: ref,
		FinishedAt: inv.FinishedAt, ProblemAt: inv.ProblemAt}
}

// seedScaled — serbest döngünün konuşmasına sentetik çağrı + sonuç (nil → aynen).
func (r *chatProblemRecord) seedScaled(msgs []copilot.ChatMessage, scale int) []copilot.ChatMessage {
	if r == nil {
		return msgs
	}
	args, _ := json.Marshal(map[string]string{"problemId": r.ProblemID})
	ev, _ := clampToolResultScaled(r.Block, scale)
	out := append([]copilot.ChatMessage{}, msgs...)
	return append(out,
		copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: problemRecordToolID, Name: problemRecordTool, Input: json.RawMessage(args)}}},
		copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: problemRecordToolID, Name: problemRecordTool, Content: ev}}},
	)
}

// seededTools — künyenin kaynağı (nil → yok).
func (r *chatProblemRecord) seededTools() []string {
	if r == nil {
		return nil
	}
	return []string{problemRecordTool}
}

// guidedSuffix — SAF: guided anlatım kanıtının eki. Kanıt kayıtlı araştırma
// bloğunu zaten taşıyorsa (kök-neden rotası) "".
func (r *chatProblemRecord) guidedSuffix(evidence string) string {
	if r == nil || strings.Contains(evidence, "KAYITLI ARAŞTIRMA (") {
		return ""
	}
	return "\n\n" + r.Block
}

type chatProblemRecordKey struct{}

func withChatProblemRecord(ctx context.Context, r *chatProblemRecord) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, chatProblemRecordKey{}, r)
}

func chatProblemRecordFrom(ctx context.Context) *chatProblemRecord {
	r, _ := ctx.Value(chatProblemRecordKey{}).(*chatProblemRecord)
	return r
}
