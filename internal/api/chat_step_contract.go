package api

// chat_step_contract.go — v0.10.2363 (docs/cosre/interaction-audit.md §1,
// §6 I1; açık sorular 6, 10, 11): v2 sohbetinin ADIM SÖZLEŞMESİ.
//
// Kapı: v2 döngüsü (cosreV2Marked) VE cosre_conversation.interaction = on
// (cosreInteractionOn, I5 ile aynı tek alan). Kapalıyken beginChatStepTurn
// nil döner, wrapEmit emit'i AYNEN verir — akış bayt aynı.
//
// Açıkken araçlı her adımın tool parçasının YANINA eşli `step-state` olayı
// (uistream → data-step, id = s<i>; karar 10: tool parçası değişmez): adım
// başlarken running, step-result gelince done | failed | skipped. Aynı id'li
// data-step öncekinin yerine geçer (AI SDK veri parçası uzlaşması), istemci
// canlı adımı ve bitenlerin tek satırını aynı parçadan çizer. S4 paket
// adımları (chat_investigate.go) aynı sözleşmeyle, kendi etiket parçalarının
// id'siyle (label + i korunur, eski çip şeridi aynen).
//
// Biten her adımın özeti hemen Redis'e: copilot:steps:<uid>:<konuşma>:<exchange>
// (JSON dizi, adım başına ≤1 KB, ≤12 adım, TTL 2 sa). Redis yok (Noop) →
// yazım ve jeton YOK, sessizce (açık soru 6: yönlendirme Redis'siz kapalı).
// Jeton (auth.SignAction, tür cosre-step) exchange + konuşma + adım +
// kullanıcıyı imzalar; I2 yönlendirmesi adımı bununla adlandırır.
// Model girdisine sıfır token eklenir: olay yalnız akışa gider.

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sre"
)

const (
	chatStepStateKind  = "step-state"
	cosreStepTokenKind = "cosre-step"
	cosreStepTTL       = 2 * time.Hour

	stepTitleMaxRunes  = 60
	stepResultMaxRunes = 100
	stepRecordMaxBytes = 1024
	stepArgsMaxBytes   = 512
	stepViewMaxBytes   = 600
	stepRecordsMax     = 12

	stepStateRunning = "running"
	stepStateDone    = "done"
	stepStateFailed  = "failed"
	stepStateSkipped = "skipped"

	stepSignalMetric    = "metric"
	stepSignalTrace     = "trace"
	stepSignalLog       = "log"
	stepSignalKnowledge = "knowledge"
)

// stepSignalByPhase — evre (chat_step_phase.go aile haritası) → sinyal kapalı kümesi.
var stepSignalByPhase = map[string]string{
	"Metrik": stepSignalMetric, "Sağlık": stepSignalMetric, "Anomali": stepSignalMetric, "Altyapı": stepSignalMetric,
	"Trace": stepSignalTrace, "Exception": stepSignalTrace, "Topoloji": stepSignalTrace,
	"Log":   stepSignalLog,
	"Bilgi": stepSignalKnowledge, "Problem": stepSignalKnowledge, "Değişiklik": stepSignalKnowledge,
}

// packStepSignal — S4 paket adımı → sinyal.
var packStepSignal = map[string]string{
	sre.PackStepSymptom: stepSignalMetric, sre.PackStepErrors: stepSignalTrace, sre.PackStepChanges: stepSignalKnowledge,
	sre.PackStepDeps: stepSignalTrace, sre.PackStepAnomalies: stepSignalMetric, sre.PackStepTrace: stepSignalTrace,
	sre.PackStepLogs: stepSignalLog,
}

// packStepBlock — paket adımının S3 kanıt blok türü (evidenceRef eşlemesi).
var packStepBlock = map[string]string{sre.PackStepTrace: blocks.TypeTrace, sre.PackStepLogs: blocks.TypeLogs}

// stepSignal — SAF: aracın sinyali; bilinmeyen ad "".
func stepSignal(tool string) string { return stepSignalByPhase[stepPhaseTR(tool)] }

type stepSpent struct {
	Calls  int `json:"calls"`
	Rounds int `json:"rounds"`
}

// chatStepCard — data-step sözleşmesi (tel). Label + I yalnız paket adımında
// (etiket parçasının yerine geçer; eski çip şeridi etiketi okur).
type chatStepCard struct {
	ID          string    `json:"id"`
	I           int       `json:"i,omitempty"`
	Order       int       `json:"order"`
	Title       string    `json:"title"`
	Label       string    `json:"label,omitempty"`
	Tool        string    `json:"tool,omitempty"`
	Signal      string    `json:"signal,omitempty"`
	State       string    `json:"state"`
	Result      string    `json:"result,omitempty"`
	EvidenceRef string    `json:"evidenceRef,omitempty"`
	Spent       stepSpent `json:"spent"`
	Token       string    `json:"token,omitempty"`
}

// chatStepRecord — Redis özeti (adım başına ≤1 KB).
type chatStepRecord struct {
	ID          string    `json:"id"`
	Order       int       `json:"order"`
	Tool        string    `json:"tool,omitempty"`
	Title       string    `json:"title"`
	Signal      string    `json:"signal,omitempty"`
	State       string    `json:"state"`
	OK          bool      `json:"ok"`
	Ms          int64     `json:"ms,omitempty"`
	Result      string    `json:"result,omitempty"`
	Args        string    `json:"args,omitempty"`
	View        string    `json:"view,omitempty"`
	EvidenceRef string    `json:"evidenceRef,omitempty"`
	Spent       stepSpent `json:"spent"`
}

// cosreStepBind — jetonun imzaladığı bağ (ActionClaims.ID = JSON).
type cosreStepBind struct {
	Exchange     string `json:"x"`
	Conversation string `json:"c,omitempty"`
	Step         string `json:"s"`
}

// clampStepRecord — SAF: alan tavanları, ardından toplam ≤1 KB (önce view,
// sonra args, sonra sonuç kısalır).
func clampStepRecord(r chatStepRecord) (chatStepRecord, []byte) {
	r.Title = clampChatRunes(collapseChatWS(r.Title), stepTitleMaxRunes)
	r.Result = clampChatRunes(collapseChatWS(r.Result), stepResultMaxRunes)
	r.Args = clipBytes(r.Args, stepArgsMaxBytes)
	r.View = clipBytes(r.View, stepViewMaxBytes)
	for {
		b, err := json.Marshal(r)
		if err != nil {
			return r, nil
		}
		over := len(b) - stepRecordMaxBytes
		switch {
		case over <= 0:
			return r, b
		case r.View != "":
			r.View = clipBytes(r.View, max(0, len(r.View)-over-8))
		case r.Args != "":
			r.Args = clipBytes(r.Args, max(0, len(r.Args)-over-8))
		case r.Result != "":
			r.Result = ""
		default:
			return r, nil
		}
	}
}

// stepToolTitle — SAF: "Trace · find traces — payments" (≤60 rune).
func stepToolTitle(tool string, argsJSON any) string {
	t := strings.ReplaceAll(tool, "_", " ")
	if p := stepPhaseTR(tool); p != "" {
		t = p + " · " + t
	}
	if svc := stepArgService(argsJSON); svc != "" {
		t += " — " + svc
	}
	return clampChatRunes(t, stepTitleMaxRunes)
}

func stepArgService(v any) string {
	var raw []byte
	switch a := v.(type) {
	case json.RawMessage:
		raw = a
	case []byte:
		raw = a
	case string:
		raw = []byte(a)
	default:
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	s, _ := m["service"].(string)
	return strings.TrimSpace(s)
}

// stepResultLine — SAF: step-result → (durum, tek satır sonuç).
func stepResultLine(ev map[string]any) (string, string) {
	ok, _ := ev["ok"].(bool)
	skipped, _ := ev["skipped"].(bool)
	reason, _ := ev["reason"].(string)
	preview, _ := ev["preview"].(string)
	ms, hasMs := asMs(ev["durationMs"])
	switch {
	case skipped:
		if reason != "" {
			return stepStateSkipped, "atlandı: " + reason
		}
		return stepStateSkipped, "atlandı"
	case !ok:
		return stepStateFailed, "okunamadı"
	}
	line := stepPreviewSummary(preview)
	if hasMs {
		line += " · " + strconv.FormatInt(ms, 10) + " ms"
	}
	return stepStateDone, line
}

func asMs(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// stepPreviewSummary — SAF: JSON gövdede ilk dizinin boyu ("3 satır"); düz
// metinde ilk dolu satır; boşsa "veri yok".
func stepPreviewSummary(preview string) string {
	p := strings.TrimSpace(preview)
	if p == "" {
		return "veri yok"
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(p), &obj) == nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			var arr []json.RawMessage
			if json.Unmarshal(obj[k], &arr) == nil {
				return strconv.Itoa(len(arr)) + " satır"
			}
		}
		return "veri alındı"
	}
	var arr []json.RawMessage
	if json.Unmarshal([]byte(p), &arr) == nil {
		return strconv.Itoa(len(arr)) + " satır"
	}
	if i := strings.IndexByte(p, '\n'); i > 0 {
		p = p[:i]
	}
	return clampChatRunes(collapseChatWS(p), stepResultMaxRunes)
}

// packStepCard — SAF: S4 paket adımı → (durum, sonuç).
func packStepState(st sre.ChatPackStep) (string, string) {
	if st.OK {
		return stepStateDone, strings.TrimPrefix(st.Label, st.Step+": ")
	}
	state := stepStateSkipped
	if st.Reason == "okuma hatası" {
		state = stepStateFailed
	}
	return state, "atlandı: " + st.Reason
}

// chatStepTurn — alışverişin adım sözleşmesi durumu (yalnız kapı açıkken).
type chatStepTurn struct {
	s       *Server
	uid     string
	conv    string
	xid     string
	persist bool // Redis var + kullanıcı + imza anahtarı
	emit    func(string, any)

	mu      sync.Mutex
	order   int
	calls   int
	rounds  int
	cards   map[string]*chatStepCard
	blockOf map[string]string // adım id → S3 blok türü
	records []chatStepRecord
}

type chatStepTurnKey struct{}

// beginChatStepTurn — kapı kapalıysa nil. eval: değerlendirme koşusu
// (Redis'e yazmaz, jeton yok).
func (s *Server) beginChatStepTurn(ctx context.Context, xid, conv string, eval bool) *chatStepTurn {
	if !cosreInteractionOn(ctx) || xid == "" {
		return nil
	}
	t := &chatStepTurn{s: s, xid: xid, conv: strings.TrimSpace(conv), cards: map[string]*chatStepCard{}, blockOf: map[string]string{}}
	if c := auth.FromContext(ctx); c != nil {
		t.uid = c.UserID
	}
	t.persist = !eval && t.uid != "" && s.auth != nil && s.cache != nil && !cache.IsNoop(s.cache)
	return t
}

func withChatStepTurn(ctx context.Context, t *chatStepTurn) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, chatStepTurnKey{}, t)
}

func stepTurnOf(ctx context.Context) *chatStepTurn {
	t, _ := ctx.Value(chatStepTurnKey{}).(*chatStepTurn)
	return t
}

// setRound — döngünün tur sayacı (harcanan bütçe); nil alıcı no-op.
func (t *chatStepTurn) setRound(n int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.rounds = n
	t.mu.Unlock()
}

// wrapEmit — step / step-result / block çerçevelerini okur, eşli step-state
// yayar. nil alıcı emit'i aynen döner (bayt aynı).
func (t *chatStepTurn) wrapEmit(ctx context.Context, emit func(string, any)) func(string, any) {
	if t == nil {
		return emit
	}
	t.emit = emit
	return func(kind string, payload any) {
		emit(kind, payload) // önce: withStepIDs `i`yi yerinde damgalar
		m, _ := payload.(map[string]any)
		if m == nil {
			return
		}
		switch kind {
		case "step":
			t.onStep(m)
		case "step-result":
			t.onResult(ctx, m)
		case "block":
			t.onBlock(ctx, m)
		}
	}
}

func (t *chatStepTurn) onStep(m map[string]any) {
	tool, _ := m["tool"].(string)
	i, _ := m["i"].(int)
	if tool == "" || i <= 0 {
		return
	}
	t.mu.Lock()
	t.order++
	t.calls++
	id := "s" + strconv.Itoa(i)
	c := &chatStepCard{ID: id, Order: t.order, Title: stepToolTitle(tool, m["argsJson"]), Tool: tool,
		Signal: stepSignal(tool), State: stepStateRunning, Spent: stepSpent{Calls: t.calls, Rounds: t.rounds}}
	t.cards[id] = c
	if bt := mcptools.EvidencePartTools[tool]; bt != "" {
		t.blockOf[id] = bt
	}
	out := *c
	t.mu.Unlock()
	t.emit(chatStepStateKind, out)
}

func (t *chatStepTurn) onResult(ctx context.Context, m map[string]any) {
	i, _ := asMs(m["i"])
	if i <= 0 {
		return
	}
	if r, _ := m["reason"].(string); r == uistream.ReasonApprovalRequired {
		return // onay bekliyor: running kalır
	}
	id := "s" + strconv.FormatInt(i, 10)
	t.mu.Lock()
	c := t.cards[id]
	if c == nil {
		t.mu.Unlock()
		return
	}
	c.State, c.Result = stepResultLine(m)
	c.Result = clampChatRunes(c.Result, stepResultMaxRunes)
	ok, _ := m["ok"].(bool)
	ms, _ := asMs(m["durationMs"])
	preview, _ := m["preview"].(string)
	rec := chatStepRecord{ID: id, Order: c.Order, Tool: c.Tool, Title: c.Title, Signal: c.Signal, State: c.State,
		OK: ok, Ms: ms, Result: c.Result, View: preview, Spent: c.Spent}
	t.mu.Unlock()
	t.finish(ctx, c, rec)
}

// notePack — S4 paket adımı (chat_investigate.go): m etiket olayı (withStepIDs damgalı).
func (t *chatStepTurn) notePack(ctx context.Context, m map[string]any, st sre.ChatPackStep) {
	if t == nil {
		return
	}
	i, _ := m["i"].(int)
	if i <= 0 {
		return
	}
	label, _ := m["label"].(string)
	state, result := packStepState(st)
	id := "s" + strconv.Itoa(i)
	t.mu.Lock()
	t.order++
	c := &chatStepCard{ID: id, I: i, Order: t.order, Title: clampChatRunes(st.Step, stepTitleMaxRunes), Label: label,
		Signal: packStepSignal[st.Step], State: state, Result: clampChatRunes(result, stepResultMaxRunes),
		Spent: stepSpent{Calls: t.calls, Rounds: t.rounds}}
	t.cards[id] = c
	if bt := packStepBlock[st.Step]; bt != "" && st.OK {
		t.blockOf[id] = bt
	}
	rec := chatStepRecord{ID: id, Order: c.Order, Title: c.Title, Signal: c.Signal, State: state, OK: st.OK, Ms: st.Ms,
		Result: c.Result, Spent: c.Spent}
	t.mu.Unlock()
	t.finish(ctx, c, rec)
}

// onBlock — S3 kanıt bloğu: aynı türü üreten son biten adıma evidenceRef.
func (t *chatStepTurn) onBlock(ctx context.Context, m map[string]any) {
	bid, _ := m["id"].(string)
	typ, _ := m["type"].(string)
	if bid == "" || typ == "" {
		return
	}
	t.mu.Lock()
	var best *chatStepCard
	for id, bt := range t.blockOf {
		c := t.cards[id]
		if bt == typ && c != nil && c.State == stepStateDone && c.EvidenceRef == "" && (best == nil || c.Order > best.Order) {
			best = c
		}
	}
	if best == nil {
		t.mu.Unlock()
		return
	}
	best.EvidenceRef = bid
	for k := range t.records {
		if t.records[k].ID == best.ID {
			t.records[k].EvidenceRef = bid
		}
	}
	out := *best
	t.mu.Unlock()
	t.emit(chatStepStateKind, out)
	t.save(ctx)
}

// finish — biten adım: jeton (Redis varken), yayın, Redis özeti.
func (t *chatStepTurn) finish(ctx context.Context, c *chatStepCard, rec chatStepRecord) {
	if t.persist {
		if raw, err := json.Marshal(cosreStepBind{Exchange: t.xid, Conversation: t.conv, Step: c.ID}); err == nil {
			if tok, err := t.s.auth.SignAction(cosreStepTokenKind, string(raw), t.uid, cosreStepTTL); err == nil {
				t.mu.Lock()
				c.Token = tok
				t.mu.Unlock()
			}
		}
	}
	t.mu.Lock()
	out := *c
	if r, b := clampStepRecord(rec); b != nil && len(t.records) < stepRecordsMax {
		t.records = append(t.records, r)
	}
	t.mu.Unlock()
	t.emit(chatStepStateKind, out)
	t.save(ctx)
}

// cosreStepsKey — copilot:steps:<uid>:<konuşma>:<exchange> (konuşmasız "-").
func cosreStepsKey(uid, conv, xid string) string {
	if conv == "" {
		conv = "-"
	}
	return "copilot:steps:" + uid + ":" + conv + ":" + xid
}

// save — özet dizisini yazar; Redis yok / hata → sessiz.
func (t *chatStepTurn) save(ctx context.Context) {
	if !t.persist {
		return
	}
	t.mu.Lock()
	b, err := json.Marshal(t.records)
	t.mu.Unlock()
	if err != nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	_ = t.s.cache.Set(wctx, cosreStepsKey(t.uid, t.conv, t.xid), b, cosreStepTTL)
}
