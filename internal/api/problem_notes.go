package api

// problem_notes.go — v0.10.1169 — probleme not: CoSRE'nin "Bulguyu probleme
// not olarak kaydet" aksiyonunun yazma ucu + problem sayfasının not listesi.
//
// Şema YOK (invariant #5): notlar saved_views'ta page='problem-note',
// owner_id=” (ekipçe görünür), name=problem kimliği, query_string=JSON.
//   - POST /api/problems/{id}/notes {token,text,tool} — editör+ (yazma);
//     metin istemciden gelir ama jeton (chat_actions.go problemNoteAction)
//     problem + metin özetini + çağıranı imzalar: değiştirilmiş metin,
//     başka problem, başkasının jetonu ya da süresi geçmiş jeton = 403.
//     Aynı jetonla tekrar = aynı satır (kimlik jetondan türer). Denetim
//     kaydı problem.note.create.
//   - GET  /api/problems/{id}/notes — kimlikli her rol okur.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problem-notes", (*Server).registerProblemNoteRoutes) }

const problemNotePage = "problem-note"

func (s *Server) registerProblemNoteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/problems/{id}/notes", auth.RequireAnyRole(editorRoles, s.createProblemNote))
	mux.HandleFunc("GET /api/problems/{id}/notes", auth.RequireAnyRole(aiChatShareRoles, s.listProblemNotes))
}

// problemNote — saklanan / dönen not.
type problemNote struct {
	ID        string `json:"id"`
	ProblemID string `json:"problemId"`
	Text      string `json:"text"`
	Tool      string `json:"tool,omitempty"`
	By        string `json:"by,omitempty"`
	At        int64  `json:"at"` // unix ns
}

// verifyProblemNote — SAF (doğrulanmış talepler üstünde): jeton bu problem,
// bu metin ve bu çağıran için mi.
func verifyProblemNote(c auth.ActionClaims, problemID, text, userID string) bool {
	return c.Kind == problemNoteTokenKind && userID != "" && c.Who == userID &&
		c.ID == problemNoteDigest(problemID, text)
}

// problemNoteRowID — SAF: jetondan kararlı satır kimliği (çift tık = tek not).
func problemNoteRowID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "pn-" + hex.EncodeToString(sum[:8])
}

func (s *Server) createProblemNote(w http.ResponseWriter, r *http.Request) {
	c := auth.FromContext(r.Context())
	if c == nil || c.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	pid := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Token string `json:"token"`
		Text  string `json:"text"`
		Tool  string `json:"tool"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || pid == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid body")
		return
	}
	claims, err := s.auth.VerifyAction(body.Token, time.Now())
	if err != nil || !verifyProblemNote(claims, pid, body.Text, c.UserID) {
		writeJSONError(w, http.StatusForbidden, "not jetonu geçersiz ya da süresi dolmuş — soruyu yeniden sorun")
		return
	}
	n := problemNote{
		ID: problemNoteRowID(body.Token), ProblemID: pid, Text: body.Text,
		Tool: clampChatRunes(body.Tool, 80), By: firstNonEmpty(c.Email, c.UserID), At: time.Now().UnixNano(),
	}
	if err := s.saveProblemNote(r, n, "cosre"); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, n)
}

// saveProblemNote — notun TEK yazma yolu (tıkla-onayla ucu + agent-core S5
// onay-kapılı add_problem_note aracı): saved_views satırı + audit
// problem.note.create (source: cosre | cosre-approval).
func (s *Server) saveProblemNote(r *http.Request, n problemNote, source string) error {
	if s.store == nil {
		return errors.New("not deposu yapılandırılmamış")
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	if err := s.store.UpsertSavedView(r.Context(), chstore.SavedView{
		ID: n.ID, OwnerID: "", Name: n.ProblemID, Page: problemNotePage, QueryString: string(raw), CreatedAt: n.At,
	}); err != nil {
		return err
	}
	det, _ := json.Marshal(map[string]any{"note": n.ID, "tool": n.Tool, "source": source})
	s.audit(r, "problem.note.create", "problem", n.ProblemID, string(det))
	return nil
}

// problemNotesFor — SAF: satırlardan bu problemin notları, yeniden eskiye.
func problemNotesFor(rows []chstore.SavedView, problemID string) []problemNote {
	out := []problemNote{}
	for _, v := range rows {
		if v.Page != problemNotePage || v.Name != problemID {
			continue
		}
		var n problemNote
		if json.Unmarshal([]byte(v.QueryString), &n) != nil || n.ProblemID != problemID {
			continue
		}
		n.ID = v.ID
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

func (s *Server) listProblemNotes(w http.ResponseWriter, r *http.Request) {
	pid := strings.TrimSpace(r.PathValue("id"))
	rows, err := s.store.ListSavedViews(r.Context(), "", problemNotePage)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, problemNotesFor(rows, pid))
}
