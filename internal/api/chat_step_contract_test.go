package api

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/sre"
)

// stepTTLCache — Set'in anahtar + TTL'ini kaydeder.
type stepTTLCache struct {
	actMemCache
	mu  sync.Mutex
	ttl map[string]time.Duration
}

func (c *stepTTLCache) Set(ctx context.Context, k string, v []byte, ttl time.Duration) error {
	c.mu.Lock()
	c.ttl[k] = ttl
	c.mu.Unlock()
	return c.actMemCache.Set(ctx, k, v, ttl)
}

type stepEv struct {
	kind string
	card chatStepCard
}

// stepRun — withStepIDs altında sarıcıyı koşar: araçlı adım, paket adımı, S3 bloğu.
func stepRun(t *testing.T, ctx context.Context, s *Server) ([]stepEv, []string) {
	t.Helper()
	var evs []stepEv
	var kinds []string
	base := func(kind string, p any) {
		kinds = append(kinds, kind)
		if kind == chatStepStateKind {
			evs = append(evs, stepEv{kind, p.(chatStepCard)})
		}
	}
	turn := s.beginChatStepTurn(ctx, "x-1", "c-1", false)
	emit := turn.wrapEmit(ctx, withStepIDs(base))
	ctx = withChatStepTurn(ctx, turn)
	stepTurnOf(ctx).setRound(1)
	i := emitStepChipInput(emit, "find_traces", `{"service":"payments"}`, "", json.RawMessage(`{"service":"payments"}`))
	emit("step-result", map[string]any{"i": i, "tool": "find_traces", "ok": true, "preview": `{"traces":[1,2]}`, "durationMs": int64(40)})
	m := map[string]any{"label": "belirti: hata %5"}
	emit("step", m)
	stepTurnOf(ctx).notePack(ctx, m, sre.ChatPackStep{Step: sre.PackStepSymptom, OK: true, Label: "belirti: hata %5"})
	emit("block", map[string]any{"id": "b1", "type": "trace_list"})
	emit("answer", map[string]any{"text": "tamam"})
	return evs, kinds
}

func TestChatStepTurnOffIsIdentity(t *testing.T) {
	e := newActEnv(t)
	base := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer})
	want := []string{"step", "step-result", "step", "block", "answer"}
	if _, got := stepRun(t, base, e.s); !reflect.DeepEqual(got, want) { // v1: işaretsiz
		t.Fatalf("v1: %v", got)
	}
	setInteraction(t, chstore.CosreInteractionOff)
	v2 := withCosreV2Loop(base)
	if e.s.beginChatStepTurn(v2, "x-1", "c-1", false) != nil {
		t.Fatal("interaction off → nil")
	}
	if _, got := stepRun(t, v2, e.s); !reflect.DeepEqual(got, want) {
		t.Fatalf("off: %v", got)
	}
}

func TestChatStepTurnContract(t *testing.T) {
	e := newActEnv(t)
	mc := &stepTTLCache{actMemCache: actMemCache{m: map[string][]byte{}}, ttl: map[string]time.Duration{}}
	e.s.cache = mc
	ctx := withCosreV2Loop(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer}))
	evs, kinds := stepRun(t, ctx, e.s)
	if want := []string{"step", chatStepStateKind, "step-result", chatStepStateKind, "step", chatStepStateKind, "block", chatStepStateKind, "answer"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("sıra: %v", kinds)
	}
	run, done, pack, ref := evs[0].card, evs[1].card, evs[2].card, evs[3].card
	if run.ID != "s1" || run.State != stepStateRunning || run.Signal != stepSignalTrace || run.Token != "" ||
		run.Title != "Trace · find traces — payments" || run.Spent != (stepSpent{Calls: 1, Rounds: 1}) {
		t.Fatalf("running: %+v", run)
	}
	if done.State != stepStateDone || done.Result != "2 satır · 40 ms" || done.Token == "" {
		t.Fatalf("done: %+v", done)
	}
	if pack.ID != "s2" || pack.I != 2 || pack.Label != "belirti: hata %5" || pack.Title != sre.PackStepSymptom ||
		pack.Signal != stepSignalMetric || pack.Result != "hata %5" || pack.Order != 2 {
		t.Fatalf("paket: %+v", pack)
	}
	if ref.ID != "s1" || ref.EvidenceRef != "b1" || ref.Token != done.Token {
		t.Fatalf("evidenceRef: %+v", ref)
	}
	c, err := e.s.auth.VerifyAction(done.Token, time.Now())
	var bind cosreStepBind
	if err != nil || c.Kind != cosreStepTokenKind || c.Who != "u-1" || json.Unmarshal([]byte(c.ID), &bind) != nil ||
		bind != (cosreStepBind{Exchange: "x-1", Conversation: "c-1", Step: "s1"}) {
		t.Fatalf("jeton: %+v %v %+v", c, err, bind)
	}
	key := cosreStepsKey("u-1", "c-1", "x-1")
	if key != "copilot:steps:u-1:c-1:x-1" || mc.ttl[key] != 2*time.Hour {
		t.Fatalf("anahtar/TTL: %q %v", key, mc.ttl[key])
	}
	var recs []chatStepRecord
	if err := json.Unmarshal(mc.m[key], &recs); err != nil || len(recs) != 2 || recs[0].EvidenceRef != "b1" || recs[1].ID != "s2" {
		t.Fatalf("kayıt: %v %+v", err, recs)
	}
	for _, r := range recs {
		if b, _ := json.Marshal(r); len(b) > stepRecordMaxBytes {
			t.Fatalf("kayıt %d B", len(b))
		}
	}
}

func TestChatStepTurnNoRedisSilent(t *testing.T) {
	e := newActEnv(t)
	noop, _ := cache.NewNoop()
	e.s.cache = noop
	ctx := withCosreV2Loop(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer}))
	evs, _ := stepRun(t, ctx, e.s)
	if len(evs) != 4 {
		t.Fatalf("olay: %d", len(evs))
	}
	for _, ev := range evs {
		if ev.card.Token != "" {
			t.Fatalf("Redis yok → jeton yok: %+v", ev.card)
		}
	}
}

func TestClampStepRecordCaps(t *testing.T) {
	long := strings.Repeat("ğ", 900)
	r, b := clampStepRecord(chatStepRecord{ID: "s1", Title: long, Result: long, Args: long, View: long, State: stepStateDone})
	if b == nil || len(b) > stepRecordMaxBytes {
		t.Fatalf("bayt: %d", len(b))
	}
	if utf8.RuneCountInString(r.Title) > stepTitleMaxRunes || utf8.RuneCountInString(r.Result) > stepResultMaxRunes ||
		!utf8.ValidString(r.View) || !utf8.ValidString(r.Args) {
		t.Fatalf("tavan: %+v", r)
	}
	if got := stepToolTitle(strings.Repeat("x_", 80), nil); utf8.RuneCountInString(got) > stepTitleMaxRunes {
		t.Fatalf("başlık %d rune", utf8.RuneCountInString(got))
	}
}

func TestStepSignalClosedSet(t *testing.T) {
	for _, p := range stepPhaseByTool {
		if !slices.Contains(uistream.StepSignals, stepSignalByPhase[p]) {
			t.Errorf("evre %q sinyalsiz", p)
		}
	}
	for n, sig := range packStepSignal {
		if !slices.Contains(uistream.StepSignals, sig) {
			t.Errorf("paket %q: %q", n, sig)
		}
	}
	for _, st := range []string{stepStateRunning, stepStateDone, stepStateFailed, stepStateSkipped} {
		if !slices.Contains(uistream.StepStates, st) {
			t.Errorf("durum %q", st)
		}
	}
}

func TestStepResultLine(t *testing.T) {
	cases := []struct {
		ev          map[string]any
		state, line string
	}{
		{map[string]any{"ok": false, "skipped": true, "reason": "call_cap"}, stepStateSkipped, "atlandı: call_cap"},
		{map[string]any{"ok": false}, stepStateFailed, "okunamadı"},
		{map[string]any{"ok": true, "preview": "ilk satır\nikinci", "durationMs": 5}, stepStateDone, "ilk satır · 5 ms"},
		{map[string]any{"ok": true, "preview": `{"a":1}`}, stepStateDone, "veri alındı"},
	}
	for _, c := range cases {
		if s, l := stepResultLine(c.ev); s != c.state || l != c.line {
			t.Errorf("%v → %s %q", c.ev, s, l)
		}
	}
}
