package api

// chat_action_card.go — v0.10.2353 (docs/cosre/interaction-audit.md §4, §6
// I5; açık sorular 1, 2, 4, 9): v2 sohbetinin ONAYLI AKSİYON KARTI
// (data-action) — öneri tarafı. Yazma tarafı cosre_actions.go.
//
// Kapı: v2 döngüsü (cosreV2Marked) VE cosre_conversation.interaction = on.
// Kapalıyken beginChatActionTurn nil döner, wrapEmit emit'i AYNEN verir —
// akış bayt aynı (api/testdata/chat_stream altınları).
//
// Karar 9: kartı YALNIZ deterministik sunucu kuralı önerir; model eylem aracı
// görmez, model metni kart doğurmaz. Kural (alışveriş başına en çok BİR kart):
//   - cevap var VE en az bir araç başarıyla yürüdü (kanıt) — aksi hâlde kart yok;
//   - sayfa bağlamında problem var VE kullanıcı editor+ → problem_note;
//   - değilse → share_link (konuşma bağlantısı; her rol — paylaşım ucu da öyle).
// Jira ve Problem oluşturma YOK (açık soru 1, 2). Prod'a dokunan tür yok.
//
// Kart alanları sunucuda doldurulur (başlık, servis, pencere, özet = cevabın
// ilk cümleleri, kanıt = başarılı adımlar s<i> araç); formda düzenlenebilir.
// İmzalı jeton (auth.SignAction, tür cosre-action) tür + alışveriş + problem
// + servis + kimlik (cluster, namespace; sayfa bağlamında varsa) + rastgele
// nonce + kullanıcıyı bağlar; onay ucu tek kullanım için Redis SETNX ister.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	chatActionEventKind  = "action"
	cosreActionTokenKind = "cosre-action" // notify ignore ucu bu türü reddeder (default dalı)
	cosreActionTTL       = 2 * time.Hour

	cosreActionShareLink   = "share_link"
	cosreActionProblemNote = "problem_note"

	actionTitleMaxRunes    = 120
	actionSummaryMaxRunes  = 600
	actionServiceMaxRunes  = 200
	actionWindowMaxRunes   = 80
	actionEvidenceMax      = 8
	actionEvidenceMaxRunes = 120
)

// cosreActionKinds — kapalı küme (uistream.ActionKinds ile eşit; test pinler).
var cosreActionKinds = map[string]bool{cosreActionShareLink: true, cosreActionProblemNote: true}

// cosreInteractionOn — dilim kapısı: v2 VE cosre_conversation.interaction=on.
func cosreInteractionOn(ctx context.Context) bool {
	return cosreV2Marked(ctx) && chstore.CurrentCosreConversation().Interaction == chstore.CosreInteractionOn
}

// cosreActionBind — jetonun imzaladığı bağ (ActionClaims.ID = JSON).
type cosreActionBind struct {
	Kind      string `json:"k"`
	Exchange  string `json:"x"`
	Problem   string `json:"p,omitempty"`
	Service   string `json:"s,omitempty"`
	Cluster   string `json:"c,omitempty"`
	Namespace string `json:"n,omitempty"`
	Nonce     string `json:"r"`
}

// cosreActionFields — kartın düzenlenebilir alanları (öneri ve onay gövdesi).
type cosreActionFields struct {
	Title    string   `json:"title"`
	Service  string   `json:"service,omitempty"`
	Window   string   `json:"window,omitempty"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence,omitempty"`
}

// clampActionFields — SAF: boşluk sıkıştırma + rune tavanları.
func clampActionFields(f cosreActionFields) cosreActionFields {
	out := cosreActionFields{
		Title:   clampChatRunes(collapseChatWS(f.Title), actionTitleMaxRunes),
		Service: clampChatRunes(strings.TrimSpace(f.Service), actionServiceMaxRunes),
		Window:  clampChatRunes(collapseChatWS(f.Window), actionWindowMaxRunes),
		Summary: clampChatRunes(strings.TrimSpace(f.Summary), problemNoteMaxRunes),
	}
	for _, e := range f.Evidence {
		if e = clampChatRunes(collapseChatWS(e), actionEvidenceMaxRunes); e != "" && len(out.Evidence) < actionEvidenceMax {
			out.Evidence = append(out.Evidence, e)
		}
	}
	return out
}

// actionCardKind — SAF kural: kanıtlı cevap yoksa ""; problem + editor+ →
// problem_note; değilse share_link.
func actionCardKind(answered bool, evidence int, problemID, role string) string {
	switch {
	case !answered || evidence == 0:
		return ""
	case strings.TrimSpace(problemID) != "" && roleSatisfies(role, auth.RoleEditor):
		return cosreActionProblemNote
	}
	return cosreActionShareLink
}

// actionSummary — SAF: cevabın ilk iki cümlesi (≤600 rune).
func actionSummary(answer string) string {
	text := collapseChatWS(answer)
	n, cut := 0, len(text)
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' {
			n++
			if n == 2 {
				cut = i + 1
				break
			}
		}
	}
	return clampChatRunes(strings.TrimSpace(text[:cut]), actionSummaryMaxRunes)
}

// actionTitle — SAF: varsayılan başlık.
func actionTitle(kind, service string) string {
	base := "CoSRE bulgusu"
	if kind == cosreActionShareLink {
		base = "CoSRE konuşması"
	}
	if service != "" {
		return base + " — " + service
	}
	return base
}

// actionLabel — SAF: kartın onay düğmesi metni.
func actionLabel(kind string) string {
	if kind == cosreActionProblemNote {
		return "Probleme not olarak ekle"
	}
	return "Paylaşım bağlantısı oluştur"
}

// chatActionTurn — alışverişin kart durumu (yalnız kapı açıkken).
type chatActionTurn struct {
	s        *Server
	uid      string
	role     string
	xid      string
	page     *agentctx.PageContext
	evidence []string
	answer   string
	answered bool
	offered  bool
}

// beginChatActionTurn — kapı kapalıysa / kullanıcı ya da imza anahtarı yoksa nil.
func (s *Server) beginChatActionTurn(ctx context.Context, xid string, page *agentctx.PageContext) *chatActionTurn {
	c := auth.FromContext(ctx)
	if !cosreInteractionOn(ctx) || c == nil || c.UserID == "" || s.auth == nil || xid == "" {
		return nil
	}
	return &chatActionTurn{s: s, uid: c.UserID, role: c.Role, xid: xid, page: page}
}

// wrapEmit — step-result ve answer çerçevelerini okur; "done"dan önce en çok
// bir kart. nil alıcı emit'i aynen döner (bayt aynı).
func (t *chatActionTurn) wrapEmit(ctx context.Context, emit func(string, any)) func(string, any) {
	if t == nil {
		return emit
	}
	return func(kind string, payload any) {
		switch kind {
		case "step-result":
			t.noteStep(payload)
		case "answer":
			if text := stateAnswerText(payload); strings.TrimSpace(text) != "" {
				t.answered, t.answer = true, text
			}
		case "done":
			if card := t.card(ctx); card != nil {
				emit(chatActionEventKind, card)
			}
		}
		emit(kind, payload)
	}
}

// noteStep — başarılı adımın kanıt referansı ("s<i> araç").
func (t *chatActionTurn) noteStep(payload any) {
	var ev struct {
		I       int    `json:"i"`
		Tool    string `json:"tool"`
		OK      bool   `json:"ok"`
		Skipped bool   `json:"skipped"`
	}
	b, err := json.Marshal(payload)
	if err != nil || json.Unmarshal(b, &ev) != nil || !ev.OK || ev.Skipped || ev.Tool == "" {
		return
	}
	if len(t.evidence) < actionEvidenceMax {
		t.evidence = append(t.evidence, "s"+strconv.Itoa(ev.I)+" "+ev.Tool)
	}
}

// card — kuralın kartı (nil = yok). Servis / pencere önce konuşma durumundan,
// yoksa sayfa bağlamından; kimlik (cluster, namespace) sayfa bağlamından.
func (t *chatActionTurn) card(ctx context.Context) map[string]any {
	if t.offered {
		return nil
	}
	problem := ""
	if t.page != nil {
		problem = t.page.ProblemID
	}
	kind := actionCardKind(t.answered, len(t.evidence), problem, t.role)
	if kind == "" {
		return nil
	}
	var service, window, cluster, namespace string
	if st := stateTurnFrom(ctx); st != nil {
		w := chatStateWireOf(st.st.ctx, chatStateNow())
		service, cluster = w.Service, w.Cluster
		if w.Window != nil {
			window = w.Window.Label
		}
	}
	if p := t.page; p != nil {
		service = firstNonEmpty(service, p.Service)
		cluster = firstNonEmpty(cluster, p.Cluster)
		namespace = p.Namespace
		if window == "" && p.TimeRange != nil {
			window = p.TimeRange.Preset
		}
	}
	if kind != cosreActionProblemNote {
		problem = ""
	}
	bind := cosreActionBind{Kind: kind, Exchange: t.xid, Problem: strings.TrimSpace(problem), Service: service,
		Cluster: cluster, Namespace: namespace, Nonce: newRandID(8)}
	raw, err := json.Marshal(bind)
	if err != nil {
		return nil
	}
	tok, err := t.s.auth.SignAction(cosreActionTokenKind, string(raw), t.uid, cosreActionTTL)
	if err != nil {
		return nil
	}
	t.offered = true
	f := clampActionFields(cosreActionFields{Title: actionTitle(kind, service), Service: service, Window: window,
		Summary: actionSummary(t.answer), Evidence: t.evidence})
	out := map[string]any{
		"kind": kind, "exchangeId": t.xid, "token": tok, "label": actionLabel(kind),
		"expiresAt": time.Now().Add(cosreActionTTL).UnixMilli(), "fields": f,
	}
	if bind.Problem != "" {
		out["problemId"] = bind.Problem
	}
	if cluster != "" {
		out["cluster"] = cluster
	}
	if namespace != "" {
		out["namespace"] = namespace
	}
	return out
}
