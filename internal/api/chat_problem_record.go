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
	return &chatProblemRecord{ProblemID: id, Block: block}
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
