package api

// cosre_actions.go — v0.10.2353 (docs/cosre/interaction-audit.md §4, §6 I5):
// onaylı aksiyon kartının (chat_action_card.go) YAZMA ucu.
//
//   POST /api/cosre/actions/confirm {token, kind, idemKey, conversationId?, fields}
//        → data-action-result parçası {type, id, data{kind, status, link?, noteId?, exchangeId}}
//   POST /api/cosre/actions/cancel  {token, kind} → {ok}  (yalnız action_cancel sinyali)
//
// Kapı: cosre_v2 bayrağı VE cosre_conversation.interaction=on; kapalıyken
// iki uç da 404 (rota etkisi yok). requireCopilot sarılı (AI rotaları emsali).
//
// Onay sırası (her ret yazımsız döner):
//  1. jeton: imza + süre + tür cosre-action + jetonun kullanıcısı = çağıran +
//     gövdedeki kind = jetondaki kind → aksi 403;
//  2. rol: kullanıcının KENDİ rolü — problem_note editor+, share_link her rol → 403;
//  3. kapsam: jetondaki ve formdaki servis kullanıcının kapsamında (takım →
//     sahip olunan servisler, chat_user_scope.go; kimlik okunamadıysa ret) → 403;
//  4. Redis yoksa 503 (onay yolu emsali: tekrar koruması olmadan yazma yok);
//  5. idemKey: aynı kullanıcının aynı anahtarlı ilk sonucu Redis'te → AYNEN döner,
//     ikinci yazım yok;
//  6. tek kullanım: SETNX(sha256(jeton)) — başka idemKey ile tekrar = 409;
//  7. yazım: share_link mevcut idempotent paylaşım yolu (applyAIChatShare;
//     konuşma kaydedilmemişse sunucu upsert ETMEZ → status needs_save, jeton
//     serbest kalır — açık soru 4); problem_note mevcut not yazma yolu
//     (saveProblemNote, audit problem.note.create; satır kimliği kullanıcı +
//     idemKey'den);
//  8. audit cosre.action.<kind> + action_confirm sistem sinyali (ağırlık 0).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func init() { registerRoutesExtra("cosre-actions", (*Server).registerCosreActionRoutes) }

func (s *Server) registerCosreActionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/cosre/actions/confirm", auth.RequireAnyRole(aiChatShareRoles, s.requireCopilot(s.confirmCosreAction)))
	mux.HandleFunc("POST /api/cosre/actions/cancel", auth.RequireAnyRole(aiChatShareRoles, s.requireCopilot(s.cancelCosreAction)))
}

const (
	cosreActionMaxBody   = 16 << 10
	cosreActionStatusOK  = "done"
	cosreActionNeedsSave = "needs_save"
)

// cosreActionNow — saat dikişi (test süresi geçmiş jetonu sınar).
var cosreActionNow = time.Now

var cosreActionIdemRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// cosreActionsEnabled — uç kapısı (v2 bayrağı + interaction).
func (s *Server) cosreActionsEnabled(ctx context.Context) bool {
	return s.cosreV2Enabled(ctx) && chstore.CurrentCosreConversation().Interaction == chstore.CosreInteractionOn
}

// verifyCosreAction — SAF (doğrulanmış talepler üstünde): jeton bu kullanıcı
// ve bu tür için mi; bağ çözülür.
func verifyCosreAction(c auth.ActionClaims, verifyErr error, kind, userID string) (cosreActionBind, bool) {
	var b cosreActionBind
	if verifyErr != nil || c.Kind != cosreActionTokenKind || userID == "" || c.Who != userID {
		return b, false
	}
	if json.Unmarshal([]byte(c.ID), &b) != nil || b.Kind != kind || !cosreActionKinds[b.Kind] || b.Exchange == "" || b.Nonce == "" {
		return cosreActionBind{}, false
	}
	if b.Kind == cosreActionProblemNote && b.Problem == "" {
		return cosreActionBind{}, false
	}
	return b, true
}

// cosreActionRoleOK — SAF: kullanıcının kendi rolü türü yazabilir mi.
func cosreActionRoleOK(kind, role string) bool {
	if kind == cosreActionProblemNote {
		return roleSatisfies(role, auth.RoleEditor)
	}
	return roleSatisfies(role, auth.RoleViewer)
}

// actionScopeAllows — SAF: servisler kullanıcının kapsamında mı. Kapsam
// globalde kapalı → serbest; kimlik yok / okuma hatası → RET; takım yok ya
// da takımın servisi yok → sohbetin kapsamsız duruşu (serbest); aksi hâlde
// her boş olmayan servis takımın servis kümesinde olmalı.
func actionScopeAllows(in userScopeInput, services ...string) bool {
	switch {
	case !in.Enabled:
		return true
	case in.UserID == "" || in.LookupErr != nil:
		return false
	case strings.TrimSpace(in.Team) == "" || len(in.TeamServices) == 0:
		return true
	}
	set := make(map[string]bool, len(in.TeamServices))
	for _, n := range in.TeamServices {
		set[strings.ToLower(n)] = true
	}
	for _, svc := range services {
		if svc = strings.ToLower(strings.TrimSpace(svc)); svc != "" && !set[svc] {
			return false
		}
	}
	return true
}

// cosreActionScopeOf — kapsam okuması (TEST DİKİŞİ). Tavansız tam servis
// listesi (MaxTeamServices kırpması uygulanmaz: kırpılan servis yanlış ret olurdu).
var cosreActionScopeOf = func(s *Server, ctx context.Context, uid string) userScopeInput {
	in := userScopeInput{Enabled: s.aiUserScopeEnabled(ctx)}
	if !in.Enabled {
		return in
	}
	s.readUserScopeTeam(ctx, &in, uid)
	s.readUserScopeServices(ctx, &in)
	return in
}

// Yazma dikişleri (TEST DİKİŞİ; üretimde mevcut yollar).
var (
	cosreActionShareFn = func(s *Server, r *http.Request, ownerID, convID string) (string, error) {
		_, tok, err := s.applyAIChatShare(r, ownerID, convID, true)
		return tok, err
	}
	cosreActionNoteFn = func(s *Server, r *http.Request, n problemNote) error {
		return s.saveProblemNote(r, n, "cosre-action")
	}
)

func cosreActionTokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "cosre:action:tok:" + hex.EncodeToString(sum[:])
}

func cosreActionResultKey(uid, idem string) string {
	sum := sha256.Sum256([]byte(uid + "\x00" + idem))
	return "cosre:action:res:" + hex.EncodeToString(sum[:])
}

// cosreActionNoteID — SAF: kullanıcı + idemKey'den kararlı not kimliği.
func cosreActionNoteID(uid, idem string) string {
	sum := sha256.Sum256([]byte("cosre-action\x00" + uid + "\x00" + idem))
	return "pn-" + hex.EncodeToString(sum[:8])
}

// cosreActionNoteText — SAF: formdan not metni.
func cosreActionNoteText(f cosreActionFields) string {
	var b strings.Builder
	b.WriteString("CoSRE bulgusu: " + f.Title)
	var meta []string
	if f.Service != "" {
		meta = append(meta, "servis "+f.Service)
	}
	if f.Window != "" {
		meta = append(meta, "pencere "+f.Window)
	}
	if len(meta) > 0 {
		b.WriteString(" (" + strings.Join(meta, ", ") + ")")
	}
	b.WriteString("\n" + f.Summary)
	if len(f.Evidence) > 0 {
		b.WriteString("\nKanıt: " + strings.Join(f.Evidence, "; "))
	}
	return b.String()
}

type cosreActionIn struct {
	Token          string            `json:"token"`
	Kind           string            `json:"kind"`
	IdemKey        string            `json:"idemKey"`
	ConversationID string            `json:"conversationId"`
	Fields         cosreActionFields `json:"fields"`
}

func (s *Server) confirmCosreAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.cosreActionsEnabled(ctx) {
		http.NotFound(w, r)
		return
	}
	c := auth.FromContext(ctx)
	if c == nil || c.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var in cosreActionIn
	r.Body = http.MaxBytesReader(w, r.Body, cosreActionMaxBody)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !cosreActionIdemRe.MatchString(in.IdemKey) || !cosreActionKinds[in.Kind] {
		writeJSONError(w, http.StatusBadRequest, "geçersiz gövde (token, kind, idemKey)")
		return
	}
	bind, ok := s.verifyCosreActionToken(in.Token, in.Kind, c.UserID)
	if !ok {
		writeJSONError(w, http.StatusForbidden, "aksiyon jetonu geçersiz ya da süresi dolmuş — soruyu yeniden sorun")
		return
	}
	if !cosreActionRoleOK(bind.Kind, c.Role) {
		writeJSONError(w, http.StatusForbidden, "bu aksiyon için rolün yetmiyor")
		return
	}
	f := clampActionFields(in.Fields)
	if f.Title == "" || (bind.Kind == cosreActionProblemNote && f.Summary == "") {
		writeJSONError(w, http.StatusBadRequest, "başlık ve özet zorunlu")
		return
	}
	if !actionScopeAllows(cosreActionScopeOf(s, ctx, c.UserID), bind.Service, f.Service) {
		writeJSONError(w, http.StatusForbidden, "out_of_scope: servis kapsamında değil")
		return
	}
	rc := s.cache
	if rc == nil || cache.IsNoop(rc) {
		writeJSONError(w, http.StatusServiceUnavailable, "Redis yok — onaylı aksiyon kapalı")
		return
	}
	resKey := cosreActionResultKey(c.UserID, in.IdemKey)
	if raw, hit, err := rc.Get(ctx, resKey); err == nil && hit && len(raw) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw) // aynı idemKey → ilk sonuç, ikinci yazım yok
		return
	}
	tokKey := cosreActionTokenKey(in.Token)
	won, err := rc.SetNX(ctx, tokKey, []byte(in.IdemKey), cosreActionTTL)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Redis hatası — yeniden deneyin")
		return
	}
	if !won {
		if prev, hit, _ := rc.Get(ctx, tokKey); hit && string(prev) == in.IdemKey {
			writeJSONError(w, http.StatusConflict, "aksiyon sürüyor")
			return
		}
		writeJSONError(w, http.StatusConflict, "aksiyon jetonu zaten kullanıldı")
		return
	}
	data, err := s.runCosreAction(r, c, bind, in, f)
	if err != nil {
		_ = rc.Del(context.WithoutCancel(ctx), tokKey)
		writeErr(w, err)
		return
	}
	part := uistream.ActionResultPart(data)
	raw, _ := json.Marshal(part)
	if data["status"] == cosreActionNeedsSave {
		_ = rc.Del(ctx, tokKey) // yazım olmadı: kaydedip aynı kartla yeniden onaylanabilir
	} else {
		_ = rc.Set(ctx, resKey, raw, cosreActionTTL)
		det, _ := json.Marshal(map[string]any{"exchange": bind.Exchange, "status": data["status"], "problem": bind.Problem,
			"service": f.Service, "cluster": bind.Cluster, "namespace": bind.Namespace, "idemKey": in.IdemKey})
		s.audit(r, "cosre.action."+bind.Kind, "cosre_action", bind.Exchange, string(det))
		s.writeCosreActionSignal(ctx, bind, cosrelearn.SigActionConfirm, fmt.Sprint(data["status"]))
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// runCosreAction — türün yazımı; data-action-result gövdesi.
func (s *Server) runCosreAction(r *http.Request, c *auth.Claims, bind cosreActionBind, in cosreActionIn, f cosreActionFields) (map[string]any, error) {
	out := map[string]any{"kind": bind.Kind, "exchangeId": bind.Exchange}
	switch bind.Kind {
	case cosreActionShareLink:
		conv := strings.TrimSpace(in.ConversationID)
		if conv == "" {
			out["status"] = cosreActionNeedsSave
			return out, nil
		}
		tok, err := cosreActionShareFn(s, r, c.UserID, conv)
		if errors.Is(err, errAIChatNotFound) {
			out["status"] = cosreActionNeedsSave
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		s.audit(r, "ai_chat.share", "ai_conversation", conv, `{"source":"cosre-action"}`)
		out["status"], out["link"] = cosreActionStatusOK, sharedConversationPath(conv, tok)
	case cosreActionProblemNote:
		n := problemNote{
			ID: cosreActionNoteID(c.UserID, in.IdemKey), ProblemID: bind.Problem, Text: cosreActionNoteText(f),
			Tool: "cosre-action", By: firstNonEmpty(c.Email, c.UserID), At: time.Now().UnixNano(),
		}
		if err := cosreActionNoteFn(s, r, n); err != nil {
			return nil, err
		}
		out["status"], out["noteId"] = cosreActionStatusOK, n.ID
		out["link"] = "/problems?problem=" + bind.Problem
	}
	return out, nil
}

func (s *Server) verifyCosreActionToken(token, kind, uid string) (cosreActionBind, bool) {
	if s.auth == nil {
		return cosreActionBind{}, false
	}
	claims, err := s.auth.VerifyAction(token, cosreActionNow())
	return verifyCosreAction(claims, err, kind, uid)
}

// writeCosreActionSignal — action_confirm / action_cancel (sistem türü,
// ağırlık 0, değerlendirme adayına girmez). Ayrıntı {kind, result} ≤512 B.
func (s *Server) writeCosreActionSignal(ctx context.Context, bind cosreActionBind, kind, result string) {
	det, _ := json.Marshal(map[string]string{"kind": bind.Kind, "result": result})
	s.writeSystemSignals(ctx, bind.Exchange, []chstore.CosreSignal{{ExchangeID: clipBytes(bind.Exchange, aiFeedbackMaxIDLen),
		Kind: kind, UserEmail: cosrelearn.SystemSignalActor, Detail: string(det)}})
}

func (s *Server) cancelCosreAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.cosreActionsEnabled(ctx) {
		http.NotFound(w, r)
		return
	}
	c := auth.FromContext(ctx)
	if c == nil || c.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var in struct {
		Token string `json:"token"`
		Kind  string `json:"kind"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, cosreActionMaxBody)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !cosreActionKinds[in.Kind] {
		writeJSONError(w, http.StatusBadRequest, "geçersiz gövde (token, kind)")
		return
	}
	bind, ok := s.verifyCosreActionToken(in.Token, in.Kind, c.UserID)
	if !ok {
		writeJSONError(w, http.StatusForbidden, "aksiyon jetonu geçersiz ya da süresi dolmuş")
		return
	}
	s.writeCosreActionSignal(ctx, bind, cosrelearn.SigActionCancel, "cancelled")
	writeJSON(w, map[string]bool{"ok": true})
}
