package api

// ai_conversation_share.go — v0.10.1167 — CoSRE sohbet paylaşımı: kayıtlı bir
// konuşma ekip arkadaşına SALT-OKUNUR bağlantıyla açılır.
//
// Şema YOK: sır mevcut ai_conversations kaydının (saved_views page='ai-chat')
// blob'unda (aiChatBlob.ShareToken). Bağlantı kimlik + sır taşır
// (/cosre/shared/{id}?t=…): okuma tek GetSavedView, tablo taraması yok.
//   - POST   /api/ai/conversations/{id}/share → yalnız sahibi; sır yoksa basar
//     (varsa aynısı döner), denetim kaydı ai_chat.share.
//   - DELETE /api/ai/conversations/{id}/share → yalnız sahibi; sırrı siler,
//     eski bağlantılar 404 olur, denetim kaydı ai_chat.unshare.
//   - GET    /api/ai/shared/{id}?t=… → kimlikli her rol (viewer dahil) okur;
//     yazma ucu yok. Yanlış / boş sır = 404 (varlık doğrulanmaz).
// Paylaşım satırın "son etkinlik" damgasını (created_at) DEĞİŞTİRMEZ: geçmiş
// listesinin sırası sohbet etkinliğidir.

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("ai-conversation-share", (*Server).registerAIConversationShareRoutes)
}

var aiChatShareRoles = []string{auth.RoleAdmin, auth.RoleEditor, auth.RoleViewer}

func (s *Server) registerAIConversationShareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ai/conversations/{id}/share", auth.RequireAnyRole(aiChatShareRoles, s.shareAIConversation))
	mux.HandleFunc("DELETE /api/ai/conversations/{id}/share", auth.RequireAnyRole(aiChatShareRoles, s.unshareAIConversation))
	mux.HandleFunc("GET /api/ai/shared/{id}", auth.RequireAnyRole(aiChatShareRoles, s.getSharedAIConversation))
}

// aiSharedConversation — salt-okunur görünüm (sır ve sahip kimliği yok).
type aiSharedConversation struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	UpdatedAt int64           `json:"updatedAt"`
	Subject   string          `json:"subject,omitempty"`
	Messages  []aiChatMessage `json:"messages"`
	ReadOnly  bool            `json:"readOnly"`
}

// carriedShareToken — SAF: mevcut blob'daki sır (yoksa / okunamazsa "").
func carriedShareToken(existingBlob string) string {
	if existingBlob == "" {
		return ""
	}
	var b struct {
		ShareToken string `json:"shareToken"`
	}
	if json.Unmarshal([]byte(existingBlob), &b) != nil {
		return ""
	}
	return b.ShareToken
}

// shareTokenMatches — SAF: sabit zamanlı karşılaştırma; boş sır hiçbir şeyle eşleşmez.
func shareTokenMatches(stored, given string) bool {
	if stored == "" || given == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(given)) == 1
}

// errAIChatNotFound — konuşma yok / sahibi başkası / page ai-chat değil.
var errAIChatNotFound = errors.New("conversation not found")

// setAIChatShare — sahibin satırında sırrı yazar / siler; satır ve blob'un
// geri kalanı aynen (created_at dahil).
func (s *Server) setAIChatShare(w http.ResponseWriter, r *http.Request, enable bool) (*chstore.SavedView, string, bool) {
	ownerID, ok := s.aiChatOwner(w, r)
	if !ok {
		return nil, "", false
	}
	cur, token, err := s.applyAIChatShare(r, ownerID, strings.TrimSpace(r.PathValue("id")), enable)
	switch {
	case errors.Is(err, errAIChatNotFound):
		writeJSONError(w, http.StatusNotFound, "conversation not found")
		return nil, "", false
	case errors.Is(err, errAIChatUnreadable):
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return nil, "", false
	case err != nil:
		writeErr(w, err)
		return nil, "", false
	}
	return cur, token, true
}

var errAIChatUnreadable = errors.New("stored conversation is unreadable")

// applyAIChatShare — paylaşımın TEK yazma yolu (bu uç + v0.10.2353 onaylı
// aksiyon kartı, cosre_actions.go): var olan sır korunur (idempotent).
func (s *Server) applyAIChatShare(r *http.Request, ownerID, id string, enable bool) (*chstore.SavedView, string, error) {
	cur, err := s.aiChatRow(r, id, ownerID)
	if err != nil {
		return nil, "", err
	}
	if cur == nil {
		return nil, "", errAIChatNotFound
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(cur.QueryString), &blob); err != nil {
		return nil, "", fmt.Errorf("%w: %v", errAIChatUnreadable, err)
	}
	token, _ := blob["shareToken"].(string)
	switch {
	case enable && token == "":
		token = newRandID(16)
		blob["shareToken"] = token
	case !enable:
		token = ""
		delete(blob, "shareToken")
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		return nil, "", err
	}
	next := *cur
	next.QueryString = string(raw)
	if err := s.store.UpsertSavedView(r.Context(), next); err != nil {
		return nil, "", err
	}
	return cur, token, nil
}

func (s *Server) shareAIConversation(w http.ResponseWriter, r *http.Request) {
	cur, token, ok := s.setAIChatShare(w, r, true)
	if !ok {
		return
	}
	s.audit(r, "ai_chat.share", "ai_conversation", cur.ID, "{}")
	writeJSON(w, map[string]any{"id": cur.ID, "token": token, "path": sharedConversationPath(cur.ID, token)})
}

func (s *Server) unshareAIConversation(w http.ResponseWriter, r *http.Request) {
	cur, _, ok := s.setAIChatShare(w, r, false)
	if !ok {
		return
	}
	s.audit(r, "ai_chat.unshare", "ai_conversation", cur.ID, "{}")
	writeJSON(w, map[string]bool{"ok": true})
}

// sharedConversationPath — SAF: uygulama içi salt-okunur sayfa yolu.
func sharedConversationPath(id, token string) string {
	return "/cosre/shared/" + id + "?t=" + token
}

func (s *Server) getSharedAIConversation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	cur, err := s.store.GetSavedView(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	var blob aiChatBlob
	if cur == nil || cur.Page != aiChatPage || json.Unmarshal([]byte(cur.QueryString), &blob) != nil ||
		!shareTokenMatches(blob.ShareToken, r.URL.Query().Get("t")) {
		writeJSONError(w, http.StatusNotFound, "conversation not found")
		return
	}
	writeJSON(w, aiSharedConversation{
		ID: cur.ID, Title: cur.Name, Subject: blob.Subject, ReadOnly: true,
		UpdatedAt: conversationStamp(blob.UpdatedAt, cur.CreatedAt), Messages: blob.Messages,
	})
}
