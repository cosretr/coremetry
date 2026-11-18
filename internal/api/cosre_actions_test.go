package api

// cosre_actions_test.go — v0.10.2353 (interaction-audit §6 I5): onaylı
// aksiyon kartı — öneri kuralı ve onay ucu. Zorunlu: yetkisiz rol ve kapsam
// dışı → 403 + yazım yok; aynı idemKey → aynı sonuç, tek yazım; sahte /
// süresi geçmiş / yeniden kullanılan jeton ret; v1 ve interaction off → kart
// yok, uç 404.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

// actMemCache — Get/Set/SetNX/Del'li bellek içi önbellek.
type actMemCache struct {
	cache.Cache
	mu sync.Mutex
	m  map[string][]byte
}

func (c *actMemCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok, nil
}

func (c *actMemCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	return nil
}

func (c *actMemCache) SetNX(_ context.Context, k string, v []byte, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[k]; ok {
		return false, nil
	}
	c.m[k] = v
	return true, nil
}

func (c *actMemCache) Del(_ context.Context, k string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	return nil
}

type actEnv struct {
	s      *Server
	notes  []problemNote
	shares []string
	sigs   chan chstore.CosreSignal
}

func setInteraction(t *testing.T, mode string) {
	t.Helper()
	prev := chstore.CurrentCosreConversation()
	c := chstore.DefaultCosreConversation()
	c.Interaction = mode
	chstore.SetCosreConversation(c)
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
}

func newActEnv(t *testing.T) *actEnv {
	t.Helper()
	e := &actEnv{sigs: make(chan chstore.CosreSignal, 8)}
	e.s = &Server{auth: auth.NewService("cosre-action-test-secret-0123456789", time.Hour),
		cache: &actMemCache{m: map[string][]byte{}}, auditQ: make(chan chstore.AuditEntry, 16)}
	e.s.cosreV2Memo().set(true)
	t.Cleanup(func() { cosreV2Memos.Delete(e.s) })
	setInteraction(t, chstore.CosreInteractionOn)
	prevShare, prevNote, prevScope := cosreActionShareFn, cosreActionNoteFn, cosreActionScopeOf
	cosreActionShareFn = func(_ *Server, _ *http.Request, owner, conv string) (string, error) {
		if conv == "unsaved" {
			return "", errAIChatNotFound
		}
		e.shares = append(e.shares, owner+"/"+conv)
		return "sek", nil
	}
	cosreActionNoteFn = func(_ *Server, _ *http.Request, n problemNote) error {
		e.notes = append(e.notes, n)
		return nil
	}
	cosreActionScopeOf = func(_ *Server, _ context.Context, uid string) userScopeInput {
		return userScopeInput{Enabled: true, UserID: uid, Team: "orders-team", TeamServices: []string{"checkout", "orders"}}
	}
	setSystemSignalWriter(func(_ context.Context, g chstore.CosreSignal) error { e.sigs <- g; return nil })
	t.Cleanup(func() {
		cosreActionShareFn, cosreActionNoteFn, cosreActionScopeOf = prevShare, prevNote, prevScope
		setSystemSignalWriter(nil)
	})
	return e
}

func (e *actEnv) token(t *testing.T, b cosreActionBind, uid string) string {
	t.Helper()
	raw, _ := json.Marshal(b)
	tok, err := e.s.auth.SignAction(cosreActionTokenKind, string(raw), uid, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *actEnv) do(handler http.HandlerFunc, body any, role string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/cosre/actions/confirm", strings.NewReader(string(raw)))
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u-1", Email: "one@example.test", Role: role}))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

var noteBind = cosreActionBind{Kind: cosreActionProblemNote, Exchange: "x-1", Problem: "P-7", Service: "checkout", Nonce: "n1"}

func noteBody(tok, idem string) map[string]any {
	return map[string]any{"token": tok, "kind": cosreActionProblemNote, "idemKey": idem,
		"fields": map[string]any{"title": "checkout yavaş", "service": "checkout", "window": "son 1 saat",
			"summary": "p95 870 ms.", "evidence": []string{"s1 get_service_health"}}}
}

func TestCosreActionKindsClosedSet(t *testing.T) {
	var got []string
	for k := range cosreActionKinds {
		got = append(got, k)
	}
	if len(got) != len(uistream.ActionKinds) {
		t.Fatalf("küme %v ≠ %v", got, uistream.ActionKinds)
	}
	for _, k := range uistream.ActionKinds {
		if !cosreActionKinds[k] {
			t.Fatalf("%s eksik", k)
		}
	}
	for _, prod := range []string{"restart", "rollback", "scale", "jira", "problem_create"} {
		if cosreActionKinds[prod] {
			t.Fatalf("%s kapalı kümede olmamalı", prod)
		}
	}
}

func TestActionCardKindRule(t *testing.T) {
	cases := []struct {
		answered bool
		ev       int
		problem  string
		role     string
		want     string
	}{
		{false, 2, "P-1", auth.RoleEditor, ""},
		{true, 0, "P-1", auth.RoleEditor, ""},
		{true, 1, "P-1", auth.RoleEditor, cosreActionProblemNote},
		{true, 1, "P-1", auth.RoleViewer, cosreActionShareLink},
		{true, 1, "", auth.RoleAdmin, cosreActionShareLink},
	}
	for _, c := range cases {
		if got := actionCardKind(c.answered, c.ev, c.problem, c.role); got != c.want {
			t.Errorf("%+v → %q", c, got)
		}
	}
}

func cardRun(t *testing.T, ctx context.Context, s *Server, page *agentctx.PageContext) []string {
	t.Helper()
	var kinds []string
	var card map[string]any
	emit := s.beginChatActionTurn(ctx, "x-9", page).wrapEmit(ctx, func(k string, p any) {
		kinds = append(kinds, k)
		if k == chatActionEventKind {
			card = p.(map[string]any)
		}
	})
	emit("step-result", map[string]any{"i": 1, "tool": "get_service_health", "ok": true})
	emit("step-result", map[string]any{"i": 2, "tool": "find_traces", "ok": false})
	emit("answer", map[string]any{"text": "checkout yavaş. p95 870 ms. Üçüncü cümle."})
	emit("done", map[string]any{"ok": true})
	if card != nil {
		f := card["fields"].(cosreActionFields)
		if f.Service != "checkout" || f.Summary != "checkout yavaş. p95 870 ms." || !reflect.DeepEqual(f.Evidence, []string{"s1 get_service_health"}) {
			t.Fatalf("alanlar %+v", f)
		}
		claims, err := s.auth.VerifyAction(card["token"].(string), time.Now())
		if _, ok := verifyCosreAction(claims, err, card["kind"].(string), "u-1"); !ok {
			t.Fatal("kartın jetonu doğrulanmalı")
		}
		if card["cluster"] != "c-east" || card["namespace"] != "shop" {
			t.Fatalf("kimlik %v", card)
		}
	}
	return kinds
}

func TestChatActionCardGate(t *testing.T) {
	e := newActEnv(t)
	base := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleEditor})
	page := &agentctx.PageContext{Page: "problems", Service: "checkout", ProblemID: "P-7", Cluster: "c-east", Namespace: "shop"}
	// v1: işaretsiz → kart yok.
	if got := cardRun(t, base, e.s, page); !reflect.DeepEqual(got, []string{"step-result", "step-result", "answer", "done"}) {
		t.Fatalf("v1 akışı değişti: %v", got)
	}
	v2 := withCosreV2Loop(base)
	if got := cardRun(t, v2, e.s, page); !reflect.DeepEqual(got, []string{"step-result", "step-result", "answer", chatActionEventKind, "done"}) {
		t.Fatalf("v2 + on: %v", got)
	}
	setInteraction(t, chstore.CosreInteractionOff)
	if e.s.beginChatActionTurn(v2, "x-9", page) != nil {
		t.Fatal("interaction off → nil")
	}
	if got := cardRun(t, v2, e.s, page); len(got) != 4 {
		t.Fatalf("off: %v", got)
	}
}

func TestCosreActionConfirmNoteIdempotent(t *testing.T) {
	e := newActEnv(t)
	tok := e.token(t, noteBind, "u-1")
	w1 := e.do(e.s.confirmCosreAction, noteBody(tok, "idem-0001"), auth.RoleEditor)
	if w1.Code != http.StatusOK || len(e.notes) != 1 {
		t.Fatalf("ilk onay %d %s / %d not", w1.Code, w1.Body, len(e.notes))
	}
	var part map[string]any
	_ = json.Unmarshal(w1.Body.Bytes(), &part)
	data := part["data"].(map[string]any)
	if part["type"] != "data-action-result" || data["status"] != "done" || data["link"] != "/problems?problem=P-7" || data["noteId"] != e.notes[0].ID {
		t.Fatalf("sonuç %s", w1.Body)
	}
	if n := e.notes[0]; n.ProblemID != "P-7" || !strings.Contains(n.Text, "checkout yavaş") || !strings.Contains(n.Text, "s1 get_service_health") {
		t.Fatalf("not %+v", n)
	}
	if a := nextAudit(t, e.s); a.Action != "cosre.action.problem_note" || a.TargetID != "x-1" {
		t.Fatalf("audit %+v", a)
	}
	select {
	case g := <-e.sigs:
		if g.Kind != cosrelearn.SigActionConfirm || g.Detail != `{"kind":"problem_note","result":"done"}` || g.UserEmail != cosrelearn.SystemSignalActor {
			t.Fatalf("sinyal %+v", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("action_confirm sinyali yazılmadı")
	}
	// Aynı idemKey → aynı sonuç, ikinci yazım yok.
	w2 := e.do(e.s.confirmCosreAction, noteBody(tok, "idem-0001"), auth.RoleEditor)
	if w2.Code != http.StatusOK || w2.Body.String() != w1.Body.String() || len(e.notes) != 1 {
		t.Fatalf("yeniden gönderim %d %s / %d not", w2.Code, w2.Body, len(e.notes))
	}
	// Aynı jeton, başka idemKey → yeniden kullanım reddi.
	if w := e.do(e.s.confirmCosreAction, noteBody(tok, "idem-0002"), auth.RoleEditor); w.Code != http.StatusConflict || len(e.notes) != 1 {
		t.Fatalf("yeniden kullanılan jeton %d / %d not", w.Code, len(e.notes))
	}
}

func TestCosreActionConfirmRejects(t *testing.T) {
	e := newActEnv(t)
	good := e.token(t, noteBind, "u-1")
	outBind := noteBind
	outBind.Service = "payments"
	out := e.token(t, outBind, "u-1")
	other := e.token(t, noteBind, "u-2")
	forged := good[:len(good)-2] + "xx"
	outField := noteBody(good, "idem-0003")
	outField["fields"].(map[string]any)["service"] = "payments"
	cases := []struct {
		name string
		body map[string]any
		role string
		want int
	}{
		{"viewer editör eylemi", noteBody(good, "idem-0004"), auth.RoleViewer, http.StatusForbidden},
		{"kapsam dışı jeton servisi", noteBody(out, "idem-0005"), auth.RoleEditor, http.StatusForbidden},
		{"kapsam dışı form servisi", outField, auth.RoleEditor, http.StatusForbidden},
		{"başka kullanıcının jetonu", noteBody(other, "idem-0006"), auth.RoleEditor, http.StatusForbidden},
		{"sahte jeton", noteBody(forged, "idem-0007"), auth.RoleEditor, http.StatusForbidden},
		{"tür uyuşmazlığı", map[string]any{"token": good, "kind": cosreActionShareLink, "idemKey": "idem-0008", "fields": map[string]any{"title": "t"}}, auth.RoleEditor, http.StatusForbidden},
		{"kısa idemKey", noteBody(good, "x"), auth.RoleEditor, http.StatusBadRequest},
	}
	for _, c := range cases {
		if w := e.do(e.s.confirmCosreAction, c.body, c.role); w.Code != c.want {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	// Süresi geçmiş jeton.
	prevNow := cosreActionNow
	cosreActionNow = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if w := e.do(e.s.confirmCosreAction, noteBody(good, "idem-0009"), auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Errorf("süresi geçmiş: %d", w.Code)
	}
	cosreActionNow = prevNow
	if len(e.notes) != 0 || len(e.s.auditQ) != 0 {
		t.Fatalf("ret yazmamalı: %d not, %d audit", len(e.notes), len(e.s.auditQ))
	}
	// Redis yok → 503, yazım yok.
	noop, _ := cache.NewNoop()
	e.s.cache = noop
	if w := e.do(e.s.confirmCosreAction, noteBody(good, "idem-0010"), auth.RoleEditor); w.Code != http.StatusServiceUnavailable || len(e.notes) != 0 {
		t.Fatalf("Redis yok: %d", w.Code)
	}
}

func TestCosreActionGateOff(t *testing.T) {
	e := newActEnv(t)
	tok := e.token(t, noteBind, "u-1")
	setInteraction(t, chstore.CosreInteractionOff)
	if w := e.do(e.s.confirmCosreAction, noteBody(tok, "idem-0011"), auth.RoleEditor); w.Code != http.StatusNotFound {
		t.Fatalf("interaction off: %d", w.Code)
	}
	if w := e.do(e.s.cancelCosreAction, map[string]any{"token": tok, "kind": cosreActionProblemNote}, auth.RoleEditor); w.Code != http.StatusNotFound {
		t.Fatalf("cancel off: %d", w.Code)
	}
	setInteraction(t, chstore.CosreInteractionOn)
	e.s.cosreV2Memo().set(false)
	if w := e.do(e.s.confirmCosreAction, noteBody(tok, "idem-0012"), auth.RoleEditor); w.Code != http.StatusNotFound {
		t.Fatalf("v2 kapalı: %d", w.Code)
	}
	if len(e.notes) != 0 {
		t.Fatal("kapalı kapı yazmamalı")
	}
}

func TestCosreActionShareNeedsSave(t *testing.T) {
	e := newActEnv(t)
	b := cosreActionBind{Kind: cosreActionShareLink, Exchange: "x-2", Service: "orders", Nonce: "n2"}
	tok := e.token(t, b, "u-1")
	body := func(conv, idem string) map[string]any {
		return map[string]any{"token": tok, "kind": cosreActionShareLink, "idemKey": idem, "conversationId": conv,
			"fields": map[string]any{"title": "CoSRE konuşması — orders", "service": "orders"}}
	}
	w := e.do(e.s.confirmCosreAction, body("unsaved", "idem-0013"), auth.RoleViewer)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"needs_save"`) || len(e.shares) != 0 || len(e.s.auditQ) != 0 {
		t.Fatalf("kaydedilmemiş: %d %s", w.Code, w.Body)
	}
	// Kaydedildikten sonra aynı kart (jeton serbest kaldı) → bağlantı.
	w = e.do(e.s.confirmCosreAction, body("c-1", "idem-0014"), auth.RoleViewer)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"link":"/cosre/shared/c-1?t=sek"`) || !reflect.DeepEqual(e.shares, []string{"u-1/c-1"}) {
		t.Fatalf("paylaşım: %d %s", w.Code, w.Body)
	}
	if a := nextAudit(t, e.s); a.Action != "ai_chat.share" {
		t.Fatalf("audit %+v", a)
	}
	if a := nextAudit(t, e.s); a.Action != "cosre.action.share_link" {
		t.Fatalf("audit %+v", a)
	}
}

func TestCosreActionCancelSignal(t *testing.T) {
	e := newActEnv(t)
	tok := e.token(t, noteBind, "u-1")
	if w := e.do(e.s.cancelCosreAction, map[string]any{"token": tok, "kind": cosreActionProblemNote}, auth.RoleViewer); w.Code != http.StatusOK {
		t.Fatalf("cancel %d", w.Code)
	}
	select {
	case g := <-e.sigs:
		if g.Kind != cosrelearn.SigActionCancel || g.ExchangeID != "x-1" {
			t.Fatalf("sinyal %+v", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("action_cancel yazılmadı")
	}
	if w := e.do(e.s.cancelCosreAction, map[string]any{"token": "bozuk", "kind": cosreActionProblemNote}, auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("sahte jeton cancel %d", w.Code)
	}
	if len(e.notes) != 0 {
		t.Fatal("iptal yazmamalı")
	}
}

func TestActionScopeAllows(t *testing.T) {
	scoped := userScopeInput{Enabled: true, UserID: "u", Team: "t", TeamServices: []string{"checkout"}}
	cases := []struct {
		in   userScopeInput
		svcs []string
		want bool
	}{
		{scoped, []string{"Checkout", ""}, true},
		{scoped, []string{"checkout", "payments"}, false},
		{userScopeInput{Enabled: false}, []string{"payments"}, true},
		{userScopeInput{Enabled: true}, []string{"checkout"}, false},
		{userScopeInput{Enabled: true, UserID: "u"}, []string{"payments"}, true},
	}
	for i, c := range cases {
		if got := actionScopeAllows(c.in, c.svcs...); got != c.want {
			t.Errorf("%d: %v", i, got)
		}
	}
}
