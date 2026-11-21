package api

// cosre_memory.go — CoSRE Faz E (hafıza): ekip notlarının yönetimi.
//
//	GET    /api/ai/memory[?service=]   → {items, limits}   her rol (viewer GÖRÜR)
//	POST   /api/ai/memory              editor+ · audit cosre_memory.create
//	       {statement, scope, source:{kind: correction|manual, conversationId?, exchangeId?}, expiresAt?}
//	PUT    /api/ai/memory/{id}         editor+ · audit cosre_memory.update
//	       {statement, scope, status: active|retired, expiresAt}
//	DELETE /api/ai/memory/{id}         editor+ · audit cosre_memory.delete
//
// Not yalnız AÇIK insan eyleminden doğar: cevabın "Düzelt" formu / sohbetin
// "Düzeltme: …" onay kartı (kind correction), yönetim listesi (manual) ve
// Problem "Kök neden" etiketi (rootcause — yalnız etiket PUT'u yazar,
// captureRootCauseMemory). Oy / triage kaynağı kapalı kümede YOK
// (cosrememory.ValidSourceKind; POST rootcause'u da reddeder).
//
// Depo saved_views page='cosre_memory' (chstore/cosre_memory.go). Neden
// requireCopilot'suz ve /api/copilot dışında: kurallar gibi notlar da AI
// kapalıyken yönetilebilir ve görülebilir; registerRoutesExtra ile kendi
// dosyasında (api.go büyümez). GET önbelleksiz: düşük trafik, yazımdan hemen
// sonra aynı liste görünmeli.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("cosre-memory", (*Server).registerCosreMemoryRoutes) }

func (s *Server) registerCosreMemoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/memory", s.listCosreMemory)
	mux.Handle("POST /api/ai/memory", auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.createCosreMemory)))
	mux.Handle("PUT /api/ai/memory/{id}", auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.updateCosreMemory)))
	mux.Handle("DELETE /api/ai/memory/{id}", auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.deleteCosreMemory)))
}

// cosreMemoryStore — depo dikişi (*chstore.Store karşılar; test sahte verir).
type cosreMemoryStore interface {
	ListCosreMemory(ctx context.Context) ([]cosrememory.Item, error)
	GetCosreMemoryItem(ctx context.Context, id string) (*cosrememory.Item, error)
	PutCosreMemoryItem(ctx context.Context, it cosrememory.Item) error
	DeleteCosreMemoryItem(ctx context.Context, id string) error
}

var cosreMemoryStoreOf = func(s *Server) cosreMemoryStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

type cosreMemoryLimits struct {
	MaxItems     int `json:"maxItems"`
	MinStatement int `json:"minStatement"`
	MaxStatement int `json:"maxStatement"`
	PromptItems  int `json:"promptItems"`
}

// cosreMemoryResponse — FE: CosreMemoryPayload (lib/types.ts).
type cosreMemoryResponse struct {
	Items  []cosrememory.Item `json:"items"`
	Limits cosreMemoryLimits  `json:"limits"`
}

// filterMemoryByService — SAF: servis verilmişse yalnız o servisin notları.
func filterMemoryByService(items []cosrememory.Item, service string) []cosrememory.Item {
	out := make([]cosrememory.Item, 0, len(items))
	service = strings.TrimSpace(service)
	for _, it := range items {
		if service == "" || (it.Scope.Kind == cosrememory.ScopeService && strings.EqualFold(it.Scope.Value, service)) {
			out = append(out, it)
		}
	}
	return out
}

func (s *Server) listCosreMemory(w http.ResponseWriter, r *http.Request) {
	items := chstore.CurrentCosreMemory()
	if st := cosreMemoryStoreOf(s); st != nil {
		fresh, err := st.ListCosreMemory(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		items = fresh
	}
	writeJSON(w, cosreMemoryResponse{Items: filterMemoryByService(items, r.URL.Query().Get("service")),
		Limits: cosreMemoryLimits{MaxItems: cosrememory.MaxItems, MinStatement: cosrememory.MinStatementRunes,
			MaxStatement: cosrememory.MaxStatementRunes, PromptItems: cosrememory.PromptMaxItems}})
}

type cosreMemoryInput struct {
	Statement string            `json:"statement"`
	Scope     cosrememory.Scope `json:"scope"`
	Status    string            `json:"status"`
	ExpiresAt int64             `json:"expiresAt"`
	Source    struct {
		Kind           string `json:"kind"`
		ConversationID string `json:"conversationId"`
		ExchangeID     string `json:"exchangeId"`
	} `json:"source"`
}

func decodeCosreMemoryInput(w http.ResponseWriter, r *http.Request) (cosreMemoryInput, bool) {
	var in cosreMemoryInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return in, false
	}
	return in, true
}

// cosreMemoryStatus — SAF: saf çekirdeğin hatası → HTTP durumu (0 = 500 yolu).
func cosreMemoryStatus(err error) int {
	switch {
	case errors.Is(err, cosrememory.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, cosrememory.ErrStatement), errors.Is(err, cosrememory.ErrScope),
		errors.Is(err, cosrememory.ErrSource), errors.Is(err, cosrememory.ErrStatus),
		errors.Is(err, cosrememory.ErrExpiry), errors.Is(err, cosrememory.ErrCap):
		return http.StatusBadRequest
	}
	return 0
}

func writeCosreMemoryErr(w http.ResponseWriter, err error) {
	if code := cosreMemoryStatus(err); code != 0 {
		writeJSONError(w, code, err.Error())
		return
	}
	writeErr(w, err)
}

// applyLocalMemory — bu pod'un süreç-içi kopyasını günceller (del → çıkar).
func applyLocalMemory(it cosrememory.Item, del bool) {
	cur := chstore.CurrentCosreMemory()
	next := make([]cosrememory.Item, 0, len(cur)+1)
	if !del {
		next = append(next, it)
	}
	for _, x := range cur {
		if x.ID != it.ID {
			next = append(next, x)
		}
	}
	chstore.SetCosreMemory(next)
}

func (s *Server) auditCosreMemory(r *http.Request, action string, it cosrememory.Item) {
	details, _ := json.Marshal(map[string]any{"id": it.ID, "scope": it.Scope, "statement": it.Statement,
		"status": it.Status, "source": it.Source.Kind, "expiresAt": it.ExpiresAt})
	s.audit(r, action, "cosre_memory", it.ID, string(details))
}

func (s *Server) createCosreMemory(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCosreMemoryInput(w, r)
	if !ok {
		return
	}
	// rootcause yalnız etiket PUT'undan; oy / triage hiç (ValidSourceKind).
	if in.Source.Kind != cosrememory.SourceCorrection && in.Source.Kind != cosrememory.SourceManual {
		writeJSONError(w, http.StatusBadRequest, cosrememory.ErrSource.Error())
		return
	}
	st := cosreMemoryStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "not deposu yok")
		return
	}
	now := time.Now().UnixNano()
	if err := cosrememory.CheckExpiry(in.ExpiresAt, now); err != nil {
		writeCosreMemoryErr(w, err)
		return
	}
	name, email := s.cosreRuleActor(r.Context(), auth.FromContext(r.Context()))
	it, err := cosrememory.Normalize(cosrememory.Item{ID: newRandID(8), Statement: in.Statement, Scope: in.Scope,
		Status: cosrememory.StatusActive, ExpiresAt: in.ExpiresAt,
		Source: cosrememory.Source{Kind: in.Source.Kind, By: name, ByEmail: email, At: now,
			ConversationID: in.Source.ConversationID, ExchangeID: in.Source.ExchangeID}})
	if err != nil {
		writeCosreMemoryErr(w, err)
		return
	}
	if len(chstore.CurrentCosreMemory()) >= cosrememory.MaxItems {
		writeCosreMemoryErr(w, cosrememory.ErrCap)
		return
	}
	if err := st.PutCosreMemoryItem(r.Context(), it); err != nil {
		writeErr(w, err)
		return
	}
	applyLocalMemory(it, false)
	s.auditCosreMemory(r, "cosre_memory.create", it)
	writeJSON(w, map[string]any{"item": it})
}

func (s *Server) updateCosreMemory(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCosreMemoryInput(w, r)
	if !ok {
		return
	}
	st := cosreMemoryStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "not deposu yok")
		return
	}
	cur, err := st.GetCosreMemoryItem(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if cur == nil {
		writeCosreMemoryErr(w, cosrememory.ErrNotFound)
		return
	}
	now := time.Now().UnixNano()
	if in.ExpiresAt != cur.ExpiresAt {
		if err := cosrememory.CheckExpiry(in.ExpiresAt, now); err != nil {
			writeCosreMemoryErr(w, err)
			return
		}
	}
	name, _ := s.cosreRuleActor(r.Context(), auth.FromContext(r.Context()))
	next := *cur
	next.Statement, next.Scope, next.ExpiresAt, next.UpdatedAt, next.UpdatedBy = in.Statement, in.Scope, in.ExpiresAt, now, name
	if in.Status != "" {
		next.Status = in.Status
	}
	next, err = cosrememory.Normalize(next)
	if err != nil {
		writeCosreMemoryErr(w, err)
		return
	}
	if err := st.PutCosreMemoryItem(r.Context(), next); err != nil {
		writeErr(w, err)
		return
	}
	applyLocalMemory(next, false)
	s.auditCosreMemory(r, "cosre_memory.update", next)
	writeJSON(w, map[string]any{"item": next})
}

func (s *Server) deleteCosreMemory(w http.ResponseWriter, r *http.Request) {
	st := cosreMemoryStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "not deposu yok")
		return
	}
	cur, err := st.GetCosreMemoryItem(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if cur == nil {
		writeCosreMemoryErr(w, cosrememory.ErrNotFound)
		return
	}
	if err := st.DeleteCosreMemoryItem(r.Context(), cur.ID); err != nil {
		writeErr(w, err)
		return
	}
	applyLocalMemory(*cur, true)
	s.auditCosreMemory(r, "cosre_memory.delete", *cur)
	writeJSON(w, map[string]any{"ok": true, "id": cur.ID})
}

// captureRootCauseMemory — Problem "Kök neden" etiketi (doğrulanmış olgu) →
// servis kapsamlı not. Problem başına tek not (yeniden etiketleme günceller;
// ekip notu emekliye ayırdıysa durum korunur). Yalnız servis öznelidir
// (kind=db Problem'in öznesi servis değil). Hata etiketi düşürmez: not
// ikincildir, yalnız günlüğe değil denetime de yazılır.
func (s *Server) captureRootCauseMemory(r *http.Request, p chstore.Problem, cause, category, who string) {
	st := cosreMemoryStoreOf(s)
	if st == nil || strings.TrimSpace(p.Service) == "" || (p.Kind != "" && p.Kind != "service") {
		return
	}
	ctx := r.Context()
	id := cosrememory.RootCauseID(p.ID)
	now := time.Now().UnixNano()
	disp := chstore.ProblemDisplayID(p.ID)
	it := cosrememory.Item{ID: id, Scope: cosrememory.Scope{Kind: cosrememory.ScopeService, Value: p.Service},
		Statement: cosrememory.RootCauseStatement(disp, cause, category), Status: cosrememory.StatusActive,
		Source: cosrememory.Source{Kind: cosrememory.SourceRootCause, By: who, At: now, ProblemID: p.ID, DisplayID: disp}}
	action := "cosre_memory.capture"
	if cur, err := st.GetCosreMemoryItem(ctx, id); err == nil && cur != nil {
		it.Status, it.ExpiresAt, it.Source.At, it.UpdatedAt, it.UpdatedBy = cur.Status, cur.ExpiresAt, cur.Source.At, now, who
	}
	it, err := cosrememory.Normalize(it)
	if err != nil {
		return
	}
	if err := st.PutCosreMemoryItem(ctx, it); err != nil {
		return
	}
	applyLocalMemory(it, false)
	s.auditCosreMemory(r, action, it)
}
