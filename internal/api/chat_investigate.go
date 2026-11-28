package api

// chat_investigate.go — v0.10.2361 (docs/cosre/investigation-design.md S4,
// karar 13–17, 19, 20): hata incelemesinde sınırlı kanıt paketi.
//
// Tetik (investigateWanted, SAF): v2 + cosre_conversation.evidence = on +
// sınıf hata avı + tek çözülmüş servis + serbest döngü. Trace takibi, rapor
// sınıfı (rota / açıklama turu), Derin kip, kod turu, onay devamı, rota ve
// operasyon ön-getirmesi turunda koşmaz. Açık ipucu ("incele", "araştır",
// "kök neden", "sorunu bul", "ne oldu") aynı koşullarda guided orta
// kademeleri atlar (investigateSkipTiers); ipucu yoksa rota bugünkü gibi.
//
// Paket (internal/sre/chatpack.go) iki dalga, ≤8 sn, model turu ve araç
// çağrısı sayılmaz (döngü tavanları aynen). Sonuç:
//   - tek sentetik araç çifti `evidence_pack` (≤2.048 B özet) konuşmaya;
//   - trace / log gövdeleri S3 kanıt adayı (ekleme kuralı aynen);
//   - RED sayıları konuşma durumu defterine, gövdeler doğrulayıcıya;
//   - adım başına bir çip, `style` açıkken tek ara cümle;
//   - turun istemine ≤650 B araştırma bölümü (v2SubsetPlan; yalnız bu tur).
// evidence off / shadow: hiçbiri koşmaz, akış bayt aynı.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sre"
)

// evidencePackTool — sentetik araç çiftinin adı (değerlendirme sahtesi adı da).
const (
	evidencePackTool   = "evidence_pack"
	evidencePackToolID = "evidence_pack_0"
	investigateRangeS  = 3600 // pencere yoksa son 1 saat
)

// investigateCues — açık araştırma ipuçları (normalizeGuidedMsg küçük harf; alt dize).
var investigateCues = []string{"incele", "araştır", "arastir", "kök neden", "kok neden", "sorunu bul", "ne oldu"}

// investigateCue — SAF.
func investigateCue(question string) bool {
	n := normalizeGuidedMsg(question)
	for _, c := range investigateCues {
		if strings.Contains(n, c) {
			return true
		}
	}
	return false
}

// investigateIn — tetiğin girdileri.
type investigateIn struct {
	V2       bool
	Evidence string
	Class    answerClass
	Service  string
	// Blocked — trace takibi, Derin, rapor sınıfı, kod turu, onay devamı,
	// rota ya da operasyon ön-getirmesi turu.
	Blocked bool
}

// investigateWanted — SAF tetik tablosu (TestInvestigateTrigger).
func investigateWanted(in investigateIn) bool {
	return in.V2 && in.Evidence == chstore.CosreEvidenceOn && in.Class == classErrorHunt &&
		strings.TrimSpace(in.Service) != "" && !in.Blocked
}

// investigateService — turun tek çözülmüş servisi: durum çözücüsünün (bu
// turda anılan ya da devralınan), yoksa ekranın.
func investigateService(stt *chatStateTurn, screen string) string {
	if s := stt.toolService(); s != "" {
		return s
	}
	return screen
}

// investigateSkipTiers — açık ipucu + paket koşacaksa guided orta kademeler atlanır.
func investigateSkipTiers(ctx context.Context, question string, st ChatContext, service string, blocked bool) bool {
	if !investigateCue(question) {
		return false
	}
	return investigateWanted(investigateIn{V2: cosreV2Marked(ctx), Evidence: cosreEvidenceMode(ctx),
		Class: answerClassHint(question, st, false), Service: service, Blocked: blocked})
}

// investigateTurn — serbest döngü öncesi paketin girdileri.
type investigateTurn struct {
	Question string
	State    ChatContext
	Service  string
	Blocked  bool
	RangeS   int64 // ekran / istek penceresi
	To       time.Time
}

// investigateWindow — SAF: durum penceresi > sorunun açık penceresi > ekran > 1 sa.
func investigateWindow(in investigateTurn) (from, to time.Time) {
	to = in.To
	rangeS := in.State.RangeS
	if in.State.WinToMs > 0 && rangeS > 0 {
		to = time.UnixMilli(in.State.WinToMs)
	}
	if rangeS <= 0 {
		rangeS = v2OpPrefetchRangeS(in.Question, in.RangeS, true)
	}
	if rangeS <= 0 {
		rangeS = investigateRangeS
	}
	return to.Add(-time.Duration(rangeS) * time.Second), to
}

// chatInvestigate — tetik tutarsa paketi koşar, konuşmaya tohum çiftini ekler
// ve künyeye paketi yazar; tutmazsa conv ve tools aynen.
func (s *Server) chatInvestigate(ctx context.Context, emit func(string, any), in investigateTurn, conv []copilot.ChatMessage, tools []string) ([]copilot.ChatMessage, []string) {
	plan := v2LoopPlanFrom(ctx)
	if plan == nil || !investigateWanted(investigateIn{V2: cosreV2Marked(ctx), Evidence: cosreEvidenceMode(ctx),
		Class: answerClassHint(in.Question, in.State, in.Blocked), Service: in.Service, Blocked: in.Blocked}) {
		return conv, tools
	}
	from, to := investigateWindow(in)
	// v0.10.2491 — inline akışta kalıcı anlatım (chat_narration.go); değilse eski geçici cümle.
	if !chatNarratorOf(ctx).narrate(ctx, emit, investigateStatusLine(in.Service)) && answerStyleOn(ctx) {
		emit(chatStatusEventKind, map[string]string{"text": investigateStatusLine(in.Service)})
	}
	var rd sre.ChatPackReaders
	if t := evalConvFrom(ctx); t != nil {
		rd = evalPackReaders(t, in.Service, from)
	} else {
		rd = sre.ChatPackReadersFrom(s.mcpDeps())
	}
	pack := sre.RunChatPack(ctx, rd, sre.ChatPackRequest{Service: in.Service, From: from, To: to}, func(st sre.ChatPackStep) {
		m := map[string]any{"label": st.Label} // any: withStepIDs `i`yi yerinde damgalar (JSON aynı)
		emit("step", m)
		stepTurnOf(ctx).notePack(ctx, m, st) // v0.10.2363 — I1: paket adımı aynı sözleşmeyle
	})
	// v0.10.2378 — özet eşiği kanıt ölçeğiyle (ölçek 1 = 2 KB, aynen).
	digest := pack.DigestJSONWithin(scaleCap(sre.ChatPackDigestMax, s.evidenceScale(ctx)))
	suggestTurnOf(ctx).notePack(pack.Digest) // v0.10.2364 — I3 çip olguları (chat_suggestions.go)
	args, _ := json.Marshal(map[string]any{"service": in.Service, "range_s": int64(to.Sub(from).Seconds())})
	conv = append(append([]copilot.ChatMessage{}, conv...),
		copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: evidencePackToolID, Name: evidencePackTool, Input: args}}},
		copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: evidencePackToolID, Name: evidencePackTool, Content: digest}}},
	)
	plan.Investigate = true
	ev := plan.evidenceOf()
	x := chatExchangeFrom(ctx)
	for _, b := range []struct{ tool, body string }{{mcptools.ExplainTraceName, pack.TraceJSON}, {mcptools.SearchLogsV2Name, pack.LogsJSON}} {
		if b.body == "" {
			continue
		}
		ev.collect(b.tool, b.body, false) // S3 adayı; modele giden özet zaten pakette
		if x != nil {
			x.verify.addTool(b.tool, nil, b.body)
		}
	}
	if x != nil {
		x.verify.addTool(evidencePackTool, args, digest)
		pa, _ := json.Marshal(struct {
			Service string             `json:"service"`
			WallMs  int64              `json:"wallMs"`
			Bytes   int                `json:"bytes"`
			Steps   []sre.ChatPackStep `json:"steps"`
		}{in.Service, pack.WallMs, len(digest), pack.Steps})
		x.noteStep(map[string]any{"tool": evidencePackTool, "args": string(pa), "i": -1}, time.Now())
	}
	stateTurnFrom(ctx).noteFacts(investigateFacts(in.Service, stateWindowLabel(in.State), pack.Digest.RED))
	evalConvFrom(ctx).notePack(pack.Steps, digest)
	return conv, append(tools, evidencePackTool)
}

// investigateFacts — SAF: paketin RED sayıları deftere.
func investigateFacts(svc, window string, r *sre.PackRED) []chatFact {
	if r == nil || r.Spans == 0 {
		return nil
	}
	return []chatFact{
		{Tool: evidencePackTool, Scope: svc, Name: "err_pct", Value: r.ErrPct, Unit: "%", Window: window},
		{Tool: evidencePackTool, Scope: svc, Name: "p95_ms", Value: r.P95Ms, Unit: "ms", Window: window},
	}
}

// investigateStatusLine — SAF: paketin ara cümlesi (araştırma ailesi).
func investigateStatusLine(svc string) string {
	return fmt.Sprintf("%s için hata izlerini topluyorum…", svc)
}

// investigateAddendum — plan araştırma turundaysa istem bölümü.
func (p *v2LoopPlan) investigateAddendum() string {
	if p == nil || !p.Investigate {
		return ""
	}
	return copilot.ChatInvestigateAddendum()
}

// ── değerlendirme sahteleri ─────────────────────────────────────────────

// evalPackStub — vakanın `evidence_pack` sahtesi (okuyucu başına alan; eksik
// alan = okuma hatası → unchecked).
type evalPackStub struct {
	RED        *evalPackRED           `json:"red"`
	PrevRED    *evalPackRED           `json:"prev_red"`
	ErrorOps   []chstore.ErrorRank    `json:"error_ops"`
	Exceptions []chstore.ExceptionRow `json:"exceptions"`
	Deploys    []mcptools.ChangeRow   `json:"deploys"`
	Edges      []chstore.ServiceEdge  `json:"edges"`
	Anomalies  []chstore.AnomalyEvent `json:"anomalies"`
	raw        map[string]json.RawMessage
}

type evalPackRED struct {
	Spans  uint64  `json:"spans"`
	Errors uint64  `json:"errors"`
	P95Ms  float64 `json:"p95_ms"`
}

var errEvalPackNoStub = fmt.Errorf("değerlendirme sahtesi: paket alanı yok")

// evalPackReaders — konuşma kipinde paketin okuyucuları vakanın sahtesinden.
func evalPackReaders(t *evalConvTurn, svc string, from time.Time) sre.ChatPackReaders {
	args, _ := json.Marshal(map[string]any{"service": svc})
	raw, ok := evalStubFor(t.Stubs[evidencePackTool], args)
	t.note(evalConvCall{Name: evidencePackTool, Origin: "pack", Args: args, Stub: ok})
	var st evalPackStub
	_ = json.Unmarshal(raw, &st)
	_ = json.Unmarshal(raw, &st.raw)
	has := func(k string) error {
		if _, ok := st.raw[k]; !ok {
			return errEvalPackNoStub
		}
		return nil
	}
	rd := sre.ChatPackReaders{
		RED: func(_ context.Context, _ string, f, _ time.Time) (chstore.ServiceWindowRED, error) {
			r := st.RED
			if !f.Equal(from) {
				r = st.PrevRED
			}
			if r == nil {
				return chstore.ServiceWindowRED{}, errEvalPackNoStub
			}
			return chstore.ServiceWindowRED{Spans: r.Spans, Errors: r.Errors, P95Ms: r.P95Ms}, nil
		},
		ErrorOps: func(context.Context, string, time.Time, time.Time, int) ([]chstore.ErrorRank, error) {
			return st.ErrorOps, has("error_ops")
		},
		Exceptions: func(context.Context, chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) {
			return st.Exceptions, has("exceptions")
		},
		Deploys: func(context.Context, string, time.Time, time.Time) (mcptools.DeployCheck, error) {
			return mcptools.DeployCheck{Rows: st.Deploys}, has("deploys")
		},
		Graph: func(context.Context, string, time.Time, time.Time) ([]chstore.ServiceEdge, error) {
			return st.Edges, has("edges")
		},
		Anomalies: func(context.Context, chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error) {
			return st.Anomalies, has("anomalies")
		},
	}
	stubBody := func(name, id string) (string, error) {
		a, _ := json.Marshal(map[string]string{"trace_id": id})
		res, ok := evalStubFor(t.Stubs[name], a)
		t.note(evalConvCall{Name: name, Origin: "pack", Args: a, Stub: ok})
		if !ok {
			return "", errEvalPackNoStub
		}
		return evalStubText(res), nil
	}
	rd.Trace = func(_ context.Context, id string) (string, error) { return stubBody(mcptools.ExplainTraceName, id) }
	rd.Logs = func(_ context.Context, id string, _ int) (string, error) {
		return stubBody(mcptools.SearchLogsV2Name, id)
	}
	return rd
}
