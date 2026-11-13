package api

// chat_question_flow_test.go — v0.10.2102 (experience-audit D2): kaynak
// sorusunun kartı GERÇEK handler'dan. v2 akışı düz metin soruyu AYNEN taşır
// ve yanına data-question yazar; v1 akışında question çerçevesi yok. Kartın
// tıkı context.clarifyOption ile gelir ve bekleyen soruya BİREBİR eşlenir
// (metin sınıflandırılmaz); serbest metin yolu chat_clarify_flow_test'te.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
)

// postV2Clarify — v2 yazıcısıyla (UI Message Stream) tek tur; ham gövde.
func (rg *clarifyRig) postV2(t *testing.T, option string, turns ...string) string {
	t.Helper()
	msgs := make([]map[string]any, 0, len(turns))
	for i, txt := range turns {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, map[string]any{"role": role, "text": txt})
	}
	cx := map[string]any{"conversation": clarifyConv}
	if option != "" {
		cx["clarifyOption"] = option
	}
	raw, _ := json.Marshal(map[string]any{"messages": msgs, "context": cx})
	r := httptest.NewRequest(http.MethodPost, "/api/cosre/v2/chat", bytes.NewReader(raw))
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: clarifyUID, Email: clarifyUID + "@example.test", Role: "viewer"})
	r = r.WithContext(withCosreV2Loop(ctx))
	w := httptest.NewRecorder()
	rg.s.copilotChatWith(w, r, v2ChatSink)
	return w.Body.String()
}

func TestClarifySourceQuestionCardV2(t *testing.T) {
	rg := newClarifyRig(t, nil)
	body := rg.postV2(t, "", clarifyQ)
	q, _ := clarifyQuestion(pendingKindSource, sourceAskTextTR)
	qb, _ := json.Marshal(map[string]any{"data": q, "id": "question", "type": "data-question"})
	var want map[string]any
	_ = json.Unmarshal(qb, &want)
	wantLine, _ := json.Marshal(want) // anahtarlar sıralı (çevirici map yazar)
	if !strings.Contains(body, "data: "+string(wantLine)+"\n") {
		t.Fatalf("data-question yok ya da farklı:\n%s", body)
	}
	textDelta, _ := json.Marshal(sourceAskTextTR)
	if !strings.Contains(body, `"delta":`+string(textDelta)) {
		t.Fatalf("düz metin soru akışta kalmalı:\n%s", body)
	}
	if p := rg.pending(t); p == nil || p.Kind != pendingKindSource {
		t.Fatalf("bekleyen soru yazılmalı: %+v", p)
	}
}

func TestClarifySourceQuestionNotInV1(t *testing.T) {
	rg := newClarifyRig(t, nil)
	fr := rg.post(t, clarifyQ)
	if q := framesOf(fr, "question"); len(q) != 0 {
		t.Fatalf("v1 akışına question sızdı: %v", q)
	}
	if got := lastAnswerText(fr); got != sourceAskTextTR {
		t.Fatalf("v1 cevabı değişti: %q", got)
	}
}

func TestClarifySourceOptionWiki(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.postV2(t, "", clarifyQ)
	rg.postV2(t, sourceReplyWiki, clarifyQ, sourceAskTextTR, "Wiki dokümanları")
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierWiki {
		t.Fatalf("seçenek wiki → tier %q", r.Tier)
	}
	if m := replyTool(r); m["reply"] != srcWiki {
		t.Fatalf("clarify_reply: %v", m)
	}
}

// Seçenek METİNDEN önce gelir: kart tıkı birebir eşlenir.
func TestClarifySourceOptionBeatsText(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("telemetri cevabı") })
	rg := newClarifyRig(t, llm)
	rg.postV2(t, "", clarifyQ)
	rg.postV2(t, sourceReplyTelemetry, clarifyQ, sourceAskTextTR, "zzz")
	r := lastRow(t, rg.rows())
	if r.Tier == aisurface.TierWiki || r.Tier == aisurface.TierClarify {
		t.Fatalf("seçenek telemetri → tier %q", r.Tier)
	}
	if m := replyTool(r); m["reply"] != srcTelemetry {
		t.Fatalf("clarify_reply: %v", m)
	}
}

// Geçersiz seçenek kimliği serbest metin yoluna düşer (bugünkü davranış).
func TestClarifySourceBadOptionFallsBackToText(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.postV2(t, "", clarifyQ)
	rg.postV2(t, "bilinmeyen", clarifyQ, sourceAskTextTR, "wikide ara")
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierWiki {
		t.Fatalf("metin yolu wiki olmalı: %q", r.Tier)
	}
}

// v1 ucu seçeneği OKUMAZ: aynı gövde metin yoluna düşer ("zzz" → dropped).
func TestClarifySourceOptionIgnoredOnV1(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.post(t, clarifyQ)
	postLoopChat(t, rg.s, context.Background(), clarifyUID, map[string]any{
		"messages": []map[string]any{{"role": "user", "text": clarifyQ}, {"role": "assistant", "text": sourceAskTextTR}, {"role": "user", "text": "zzz"}},
		"context":  map[string]any{"conversation": clarifyConv, "clarifyOption": sourceReplyWiki}})
	if m := replyTool(lastRow(t, rg.rows())); m["reply"] != clarifyReplyDropped {
		t.Fatalf("v1 seçeneği okumamalı: %v", m)
	}
}
