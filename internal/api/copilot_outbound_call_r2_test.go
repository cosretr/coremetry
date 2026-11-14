package api

// v0.10.1455 — outbound_call R2 (yolsuz hedef sözcüğü) regresyon testleri.
//
// Prod: "<servis> mainframe cevap sürelerimde artış var mı?" doküman/RAG
// kademesine düşüyordu; "esbprod'a giden çağrılar yavaşladı mı?" giden
// çağrıyı değil servisin gelen isteklerini ölçen yollara gidiyordu. Pinler:
// (1) hedef + ölçü → outbound_call, topolojiden / metrik grafiğinden önce;
// Oracle db_health'te, kuyruk/log/pod/kaynak/yol/çift kendi yollarında;
// (2) host tek adaya inmezse "hangi dış host?" çipleri, çip aynı rotaya
// döner; (3) yolsuz özet MV-first, ≥2 ortam ham + ortam başına, iş kodları;
// (4) R1 açık notu: sonda penceresi > boş varsayılan pencere → genişleme.
// Adlar sentetik.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	obHostESB  = "esbprod.example.internal"
	obHostESBT = "esbtest.example.internal"
	obHostMF   = "mainframe-gw.example.test"
	obHostKX   = "kxzone.example.test"
)

var (
	obSvcs  = []string{obTestSvc, "payments", obTestOther, "esb-adapter"}
	obEnvs  = []string{"prod-eu-a", "prod-eu-b"}
	obHosts = []outboundHostEntry{
		{Host: obHostESB, Callers: []string{obTestSvc}, Calls: 900},
		{Host: obHostMF, Callers: []string{obTestSvc, "payments"}, Calls: 700},
		{Host: obHostESBT, Callers: []string{obTestOther}, Calls: 300},
		{Host: obHostKX, Callers: []string{"payments"}, Calls: 100},
	}
)

// TestRouteOutboundCall — plan §1.3 öncelik tablosunun yolsuz (R2) yarısı +
// sınır vakaları; routeGuidedIntentExt üzerinden (gerçek router sırası).
func TestRouteOutboundCall(t *testing.T) {
	type want struct {
		intent  guidedIntent
		peer    string
		svc     string
		options string // virgüllü; "-" = boş
		miss    bool
		envSet  string
	}
	ob := guidedOutboundCall
	cases := []struct {
		name, msg, ctxSvc string
		hosts             []outboundHostEntry
		w                 want
	}{
		{"prod: mainframe cevap süresi artışı (RAG'a düşüyordu)", obTestSvc + " mainframe cevap sürelerimde artış var mı?", "", obHosts, want{intent: ob, peer: obHostMF, svc: obTestSvc}},
		{"prod: esbprod'a giden çağrılar yavaşladı mı", "esbprod'a giden çağrılar yavaşladı mı?", "", obHosts, want{intent: ob, peer: obHostESB}},
		{"tam host adı + hata", obHostKX + " hataları arttı mı", "", obHosts, want{intent: ob, peer: obHostKX}},
		{"ESB gecikme grafiği: metric_chart DEĞİL, iki aday → çip", "ESB gecikme grafiği", "", obHosts, want{intent: ob, options: obHostESB + "," + obHostESBT}},
		{"çağıran adayları daraltır", obTestSvc + " esb gecikmesi arttı mı", "", obHosts, want{intent: ob, peer: obHostESB, svc: obTestSvc}},
		{"ekran servisi çağıran", "esb cevap süreleri arttı mı", obTestOther, obHosts, want{intent: ob, peer: obHostESBT, svc: obTestOther}},
		{"anamakine eşanlamı", "anamakine yanıt süreleri nasıl", "", obHosts, want{intent: ob, peer: obHostMF}},
		{"dış bağımlılıklarım yavaş mı (topoloji değil)", "dış bağımlılıklarım yavaş mı", obTestSvc, obHosts, want{intent: ob, svc: obTestSvc, options: obHostESB + "," + obHostMF}},
		{"tibco: canlı hostta yok → en yoğunlar + miss", "tibco cevap süreleri arttı mı", "", obHosts, want{intent: ob, miss: true, options: obHostESB + "," + obHostMF + "," + obHostESBT + "," + obHostKX}},
		{"katalog yok: sabit kök yine sahiplenir (dürüst cevap)", obTestSvc + " mainframe cevap sürelerinde artış var mı", "", nil, want{intent: ob, svc: obTestSvc, miss: true, options: "-"}},
		{"çift: servis → canlı dış host", obTestSvc + "'dan kxzone'a giden istekler yavaş mı", "", obHosts, want{intent: ob, peer: obHostKX, svc: obTestSvc}},
		{"≥2 ortam", "esbprod prod-eu-a ve prod-eu-b gecikmesi arttı mı", "", obHosts, want{intent: ob, peer: obHostESB, envSet: "prod-eu-a,prod-eu-b"}},
		// sınırlar — kendi yollarında kalır
		{"ölçü yok: kimler bağlı", "esb'ye kimler bağlı", "", obHosts, want{}},
		{"ölçü yok: mainframe nedir", "mainframe nedir", "", obHosts, want{}},
		{"bağımlılıkları göster → topoloji", "payments bağımlılıklarını göster", "", obHosts, want{intent: guidedServiceTopology}},
		{"oracle → db_health", "oracle cevap süreleri arttı mı", "", obHosts, want{intent: guidedDBHealth}},
		{"tibco ems kuyruk → mesajlaşma", "tibco kuyruk gecikmesi var mı", "", obHosts, want{intent: guidedMessagingHealth}},
		{"esb pod cpu → kaynak", "esb pod'larında cpu artışı var mı", "", obHosts, want{}},
		{"yol anması → endpoint/R1", "POST /esb/orders neden yavaş", "", obHosts, want{}},
		{"servis-servis çifti", obTestSvc + "'dan payments'a giden istekler yavaş mı", "", obHosts, want{intent: guidedPairRequests}},
		{"servis adı hedef değil", "esb-adapter gecikmesi arttı mı", "", obHosts, want{}},
		{"metrik grafiği aynen", obTestSvc + " heap grafiği", "", obHosts, want{intent: guidedMetricChart}},
		{"servis sağlığı aynen", obTestSvc + " sağlığı nasıl", "", obHosts, want{intent: guidedServiceHealth}},
		{"kök neden aynen", "payments neden yavaş", "", obHosts, want{intent: guidedRootCause}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := routeGuidedIntentExt(c.msg, obSvcs, obEnvs, nil, c.ctxSvc, false, c.hosts)
			if c.w.intent == "" {
				if r.Intent == guidedOutboundCall {
					t.Fatalf("outbound_call OLMAMALI: %+v", r)
				}
				return
			}
			if r.Intent != c.w.intent {
				t.Fatalf("intent %q, want %q (%+v)", r.Intent, c.w.intent, r)
			}
			if c.w.intent != guidedOutboundCall {
				return
			}
			opts := strings.Join(r.OutboundOptions, ",")
			if c.w.options == "-" {
				c.w.options = ""
			}
			if r.OutboundPeer != c.w.peer || r.Service != c.w.svc || opts != c.w.options || r.OutboundMiss != c.w.miss || strings.Join(r.EnvSet, ",") != c.w.envSet {
				t.Errorf("rota: peer=%q svc=%q opts=%q miss=%v envs=%v\nwant %+v", r.OutboundPeer, r.Service, opts, r.OutboundMiss, r.EnvSet, c.w)
			}
			if r.Service != "" && strings.Join(r.OutboundCallers, ",") != r.Service {
				t.Errorf("çağıran ölçüm listesine girmeli: %v", r.OutboundCallers)
			}
		})
	}
	// Katalogsuz router (routeGuidedIntent: disambig / wiki / takip çağıranları)
	// yalnız sabit köklerle çalışır, canlı host adını tanımaz.
	if r := routeGuidedIntent("esbprod'a giden çağrılar yavaşladı mı?", obSvcs, obEnvs, nil, ""); r.Intent != guidedOutboundCall || r.OutboundPeer != "" {
		t.Errorf("katalogsuz: %+v", r)
	}
}

func TestOutboundLiveHostMatches(t *testing.T) {
	hosts := []outboundHostEntry{{Host: "prod.example.test"}, {Host: "gateway.example.test"}, {Host: "orders-api.example.test"},
		{Host: "10.1.2.3"}, {Host: "abc.example.test"}, {Host: obHostESB}}
	live := lowerSet([]string{"orders-api"})
	for _, c := range []struct{ msg, want string }{
		{"prod gecikmesi", ""},           // ortam kademesi
		{"gateway gecikmesi", ""},        // genel sözcük
		{"orders-api gecikmesi", ""},     // canlı servis adı
		{"10 gecikmesi", ""},             // sayı
		{"abc gecikmesi", ""},            // <4 karakter
		{"esbprod gecikmesi", obHostESB}, // ilk DNS etiketi
		{"esbprodx gecikmesi", ""},       // birebir değil
		{"10.1.2.3 gecikmesi", "10.1.2.3"},
	} {
		norm := normalizeGuidedMsg(c.msg)
		var got []string
		for _, h := range outboundLiveHostMatches(norm, guidedTokens(norm), hosts, live) {
			got = append(got, h.Host)
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("%q → %v, want %q", c.msg, got, c.want)
		}
	}
}

func TestHasOutboundSignal(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"orders-api mainframe cevap sürelerimde artış var mı?", true},
		{"esb ortalama süreleri", true},
		{"dış servis çağrılarında artış var mı", true},
		{"kxzone.example.test ortalaması arttı mı", true},
		{"mainframe nedir", false},
		{"orders-api ortalaması", false},
	} {
		norm := normalizeGuidedMsg(c.msg)
		if got := hasOutboundSignal(norm, guidedTokens(norm)); got != c.want {
			t.Errorf("hasOutboundSignal(%q) = %v", c.msg, got)
		}
	}
	// RAG tier'ına düşen prod sorusu artık guided kapısını geçer.
	if !hasGuidedSignal(normalizeGuidedMsg("orders-api mainframe cevap sürelerimde artış var mı?")) {
		t.Error("guided sinyal kapısı (RAG'a düşüyordu)")
	}
}

func TestOutboundVetoesOpPrefetch(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"orders-api dış host " + obHostESB + " çağrı süreleri", true}, // "hangi dış host?" çipi
		{"giden çağrılar yavaşladı mı", true},
		{"mainframe cevap süreleri", false}, // sabit kök ön-getirmede kalır (plan §1.2)
		{"dış servis /esb/x gecikmesi", false},
		{"dış servis listesi", false}, // ölçü yok
		{"PAYMENT_CREATE neden yavaş", false},
	} {
		if got := outboundVetoesOpPrefetch(c.msg); got != c.want {
			t.Errorf("veto(%q) = %v", c.msg, got)
		}
	}
}

// TestOutboundAskHostChips — "hangi dış host?" cevabı: tahmin yok, adaylar
// çip; çip metni yeniden router'dan TEK hosta çözülür (servis + ortam taşınır).
func TestOutboundAskHostChips(t *testing.T) {
	r := routeGuidedIntentExt("ESB gecikme grafiği", obSvcs, obEnvs, nil, "", false, obHosts)
	if r.OutboundPeer != "" || len(r.OutboundOptions) != 2 {
		t.Fatalf("iki aday bekleniyordu: %+v", r)
	}
	text := outboundAskHostTR(r)
	if !strings.Contains(text, "'esb' birden çok dış hostla eşleşti") || !strings.Contains(text, "tahmin etmiyorum") {
		t.Errorf("metin: %s", text)
	}
	fu := guidedFollowups(r, nil)
	if len(fu) != 2 || fu[0].Kind != fuKindOutboundHost || fu[0].Label != obHostESB {
		t.Fatalf("çipler: %+v", fu)
	}
	for _, f := range fu {
		if !outboundVetoesOpPrefetch(f.Text) {
			t.Errorf("çip ön-getirmeye kaptırılmamalı: %q", f.Text)
		}
		back := routeGuidedIntentExt(f.Text, obSvcs, obEnvs, nil, "", false, obHosts)
		if back.Intent != guidedOutboundCall || back.OutboundPeer != f.Label {
			t.Errorf("çip %q → %+v", f.Text, back)
		}
	}
	r.Service, r.Env = obTestSvc, "prod-eu-a"
	chip := outboundHostChipText(r, obHostMF)
	back := routeGuidedIntentExt(chip, obSvcs, obEnvs, nil, "", false, obHosts)
	if back.OutboundPeer != obHostMF || back.Service != obTestSvc || back.Env != "prod-eu-a" {
		t.Errorf("servis/ortam çipte taşınmalı: %q → %+v", chip, back)
	}
	miss := outboundAskHostTR(guidedRoute{OutboundTerm: "tibco", OutboundMiss: true, OutboundOptions: []string{obHostESB}, Service: obTestSvc})
	if !strings.Contains(miss, "'tibco' hiçbir canlı dış host adında geçmiyor") || !strings.Contains(miss, "("+obTestSvc+" çağırıyor)") {
		t.Errorf("miss metni: %s", miss)
	}
	if s := guidedSuggestions(r); len(s) != 2 || !strings.Contains(s[0], "dış host") {
		t.Errorf("öneriler: %v", s)
	}
	if l := guidedAnswerLinks(guidedRoute{Intent: guidedOutboundCall}, linkWindow{}); len(l) != 1 || l[0].Href != "/external" {
		t.Errorf("hostsuz link: %+v", l)
	}
}

func TestOutboundEnvLinesAndTable(t *testing.T) {
	rows := []chstore.OutboundAgg{
		{Env: "prod-eu-a", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "cur", Calls: 500, Errors: 40, AvgMs: 900},
		{Env: "prod-eu-a", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "prev", Calls: 500, Errors: 5, AvgMs: 400},
		{Env: "prod-eu-b", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "cur", Calls: 300, Errors: 3, AvgMs: 410},
		{Env: "prod-eu-b", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "prev", Calls: 300, Errors: 3, AvgMs: 400},
	}
	lines := outboundEnvLinesTR(rows, nil)
	for _, w := range []string{"KÜME KIYASI", "- prod-eu-a: 500 çağrı, ort 900 ms (önceki 400 ms), hata %8 — ARTIŞ VAR (gecikme + hata)", "- prod-eu-b: 300 çağrı, ort 410 ms (önceki 400 ms), hata %1 — belirgin fark yok"} {
		if !strings.Contains(lines, w) {
			t.Errorf("%q yok:\n%s", w, lines)
		}
	}
	got, _ := outboundTableRowsX(rows, nil, true, false)
	cols := outboundTableColumnsFor(true, "p99", false)
	if len(got) != 2 || len(got[0]) != len(cols) || got[0][0] != "prod-eu-a" || got[0][8] != 125.0 || got[1][8] != 2.5 {
		t.Errorf("ortam sütunu + ortam başına Δ: %v", got)
	}
	if cols[0].Label != "Ortam" || cols[7].Label != "p99 ms" {
		t.Errorf("sütunlar: %+v", cols)
	}
	for _, c := range cols {
		if c.Kind == "trace" {
			t.Error("MV tablosu trace sütunu taşımaz")
		}
	}
	codes := outboundCodeRows([]chstore.OutboundCode{{Kind: "channel_code", Code: "C7", Calls: 10}, {Kind: "function_code", Code: "F2", Calls: 5, Errors: 1, AvgMs: 12.34},
		{Kind: "function_code", Code: "F1", Calls: 50}, {Kind: "function_code", Code: " "}})
	if len(codes) != 3 || codes[0][1] != "F1" || codes[1][1] != "F2" || codes[1][3] != 20.0 || codes[2][0] != "channel_code" {
		t.Errorf("iş kodu satırları: %v", codes)
	}
	sel, notes := outboundHostCallerScope([]chstore.ExternalCaller{{Service: obTestOther}, {Service: obTestSvc}}, obTestSvc, obHostESB)
	if len(sel) != 1 || sel[0].Service != obTestSvc || notes != nil {
		t.Errorf("daraltma: %v %v", sel, notes)
	}
	if sel, notes = outboundHostCallerScope([]chstore.ExternalCaller{{Service: obTestOther}}, obTestSvc, obHostESB); len(sel) != 1 || len(notes) != 1 || !strings.Contains(notes[0], "çağırmıyor") {
		t.Errorf("çağırmayan servis beyanı: %v %v", sel, notes)
	}
}

// obHostServer — store'suz sunucu + R2 kataloğu (servis/ortam/host önbellekte).
func obHostServer(t *testing.T) (*Server, *loopLLM) {
	t.Helper()
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return map[string]any{"content": "MODEL"} })
	s, mc := newLoopTestServer(t, llm)
	svcs, _ := json.Marshal(obSvcs)
	envs, _ := json.Marshal(obEnvs)
	hosts, _ := json.Marshal(obHosts)
	mc.m["copilot:guided:svcnames"], mc.m["copilot:guided:envnames"], mc.m[outboundHostsCacheKey] = svcs, envs, hosts
	return s, llm
}

type obRun struct {
	tools      map[string]int
	tables     []mcptools.EvTablePayload
	charts     []mcptools.EvChartPayload
	answer     map[string]any
	evidence   map[string]mcptools.ChatEvidence
	stepResult map[string]string
}

func runOutboundGuided(t *testing.T, s *Server, ctx context.Context, msg, ctxSvc string) obRun {
	t.Helper()
	return runOutboundGuidedEnv(t, s, ctx, msg, ctxSvc, "")
}

// runOutboundGuidedEnv — v0.10.1468: ekran ortamıyla (ctxEnv).
func runOutboundGuidedEnv(t *testing.T, s *Server, ctx context.Context, msg, ctxSvc, ctxEnv string) obRun {
	t.Helper()
	evs, emit := captureEmit()
	handled, ok := s.copilotChatGuided(ctx, emit, []copilot.ChatMessage{{Role: "user", Text: msg}}, ctxSvc, "", "", 0, "", ctxEnv, time.Time{}, 0, "")
	if !handled || !ok {
		t.Fatalf("guided sahiplenmedi (RAG'a düşerdi): %q", msg)
	}
	out := obRun{tools: map[string]int{}, evidence: map[string]mcptools.ChatEvidence{}, stepResult: map[string]string{}}
	for _, e := range *evs {
		switch e.kind {
		case "step":
			if m, _ := e.payload.(map[string]any); m != nil {
				out.tools[m["tool"].(string)]++
			}
		case "step-result":
			if m, _ := e.payload.(map[string]any); m != nil {
				if ev, ok := m["evidence"].(mcptools.ChatEvidence); ok {
					out.evidence[ev.Tool] = ev
				}
				if tool, _ := m["tool"].(string); tool != "" {
					txt, _ := m["text"].(string)
					out.stepResult[tool] = txt
				}
			}
		case "ev_block":
			switch p := e.payload.(mcptools.EvidenceBlock).Payload.(type) {
			case mcptools.EvTablePayload:
				out.tables = append(out.tables, p)
			case mcptools.EvChartPayload:
				out.charts = append(out.charts, p)
			}
		case "answer":
			out.answer, _ = e.payload.(map[string]any)
		}
	}
	if out.answer == nil {
		t.Fatal("answer yok")
	}
	return out
}

// TestOutboundHostGuidedMV — prod sorusu uçtan uca (guided kademesi):
// LLM'siz, MV-first (çağıranlar cur/önceki/geçen hafta + tek çağıranlı trend),
// iş kodu tablosu, kaynak "trace (örneklenmiş)", hüküm ARTIŞ VAR.
func TestOutboundHostGuidedMV(t *testing.T) {
	s, llm := obHostServer(t)
	now := time.Now()
	f := &fakeOutboundReader{
		hostCallers: func(from, to time.Time) []chstore.ExternalCaller {
			if to.Before(now.Add(-24 * time.Hour)) { // geçen hafta
				return []chstore.ExternalCaller{{Service: obTestSvc, Calls: 1000, Errors: 3, AvgMs: 395, P99Ms: 900}}
			}
			if to.Before(now.Add(-30 * time.Minute)) { // önceki pencere
				return []chstore.ExternalCaller{{Service: obTestSvc, Calls: 1100, Errors: 4, AvgMs: 420, P99Ms: 950}, {Service: "payments", Calls: 50, AvgMs: 10}}
			}
			return []chstore.ExternalCaller{{Service: obTestSvc, Calls: 1240, Errors: 38, AvgMs: 812, P99Ms: 2310}, {Service: "payments", Calls: 60, AvgMs: 12}}
		},
		trend: []chstore.ExternalTrendPoint{{Bucket: 1_000_000, Calls: 100, Errors: 3, AvgMs: 800, P99Ms: 2000}, {Bucket: 1_000_300, Calls: 110, AvgMs: 820, P99Ms: 2310}},
		codes: []chstore.OutboundCode{{Kind: "function_code", Code: "F100", Calls: 900, Errors: 30, AvgMs: 850}},
	}
	installOutboundReader(t, f)
	ctx := armOutboundSlot(context.Background(), "", "", 0, time.Time{})
	run := runOutboundGuided(t, s, ctx, obTestSvc+" mainframe cevap sürelerimde artış var mı?", "")
	if n := len(llm.calls()); n != 0 {
		t.Errorf("LLM'siz olmalı, %d çağrı", n)
	}
	for _, tool := range []string{outboundProbeTool, outboundCallsTool, outboundSeriesTool, outboundCodesTool, outboundVerdictTool} {
		if run.tools[tool] != 1 {
			t.Errorf("%s çipi: %v", tool, run.tools)
		}
	}
	calls := strings.Join(f.calls, " ")
	for _, w := range []string{"hostcallers:" + obHostMF, "trend:" + obHostMF + ":" + obTestSvc, "codes"} {
		if !strings.Contains(calls, w) {
			t.Errorf("okuma %q yok: %s", w, calls)
		}
	}
	if strings.Contains(calls, "aggs") {
		t.Error("ortamsız host özeti ham ölçüme gitmemeli (MV-first)")
	}
	if len(f.hostWins) != 3 || f.hostWins[0] != time.Hour {
		t.Errorf("MV okumaları (cur 1 sa varsayılan, önceki, geçen hafta): %v", f.hostWins)
	}
	if f.codeQuery.PathLike != chstore.OutboundAllPaths || f.codeQuery.PeerHost != obHostMF || strings.Join(f.codeQuery.Services, ",") != obTestSvc {
		t.Errorf("iş kodu okuması: %+v", f.codeQuery)
	}
	if len(run.tables) != 2 || len(run.charts) != 1 {
		t.Fatalf("bloklar: %d tablo %d grafik", len(run.tables), len(run.charts))
	}
	if !strings.Contains(run.charts[0].Title, "trace (örneklenmiş)") || run.charts[0].Series[1].Name != "p99 ms" {
		t.Errorf("grafik: %q %+v", run.charts[0].Title, run.charts[0].Series)
	}
	if run.tables[1].Columns[1].Label != "Kod" || run.tables[1].Rows[0][1] != "F100" {
		t.Errorf("iş kodu tablosu: %+v", run.tables[1])
	}
	text, _ := run.answer["text"].(string)
	for _, w := range []string{obTestSvc + " → " + obHostMF + " · (tüm yollar)", "kaynak: topology_edges_5m (MV) · trace (örneklenmiş)",
		"1.240 çağrı", "önceki pencere 420 ms", "**ARTIŞ VAR** (gecikme ve hata oranı)", "p99 (5 dk kova maksimumu, MV) 2.310 ms", "iş kodu kırılımı"} {
		if !strings.Contains(text, w) {
			t.Errorf("cevapta %q yok:\n%s", w, text)
		}
	}
	if strings.Contains(text, "p95") {
		t.Error("MV kova maksimumu p95 diye yazılmamalı")
	}
	if _, has := run.answer["exchangeId"]; has {
		t.Error("deterministik cevap oylanmaz")
	}
	raw, _ := json.Marshal(run.answer["links"])
	if !strings.Contains(string(raw), "/external?host="+obHostMF) || !strings.Contains(string(raw), "search="+obHostMF) {
		t.Errorf("linkler: %s", raw)
	}
	if ev := run.evidence[outboundCallsTool]; !strings.Contains(ev.Source, "topology_edges_5m (MV)") || ev.QueryKind != "sql" {
		t.Errorf("kanıt: %+v", ev)
	}
}

// TestOutboundHostGuidedEnvs — ≥2 ortam: MV ortam taşımaz → ham client span
// (deploy_env IN + GROUP BY deploy_env), ortam başına satır + KÜME KIYASI.
func TestOutboundHostGuidedEnvs(t *testing.T) {
	s, llm := obHostServer(t)
	f := &fakeOutboundReader{
		hostCallers: func(time.Time, time.Time) []chstore.ExternalCaller {
			return []chstore.ExternalCaller{{Service: obTestSvc, Calls: 800, AvgMs: 600}}
		},
		aggs: []chstore.OutboundAgg{
			{Env: "prod-eu-a", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "cur", Calls: 500, Errors: 40, AvgMs: 900, P95Ms: 2000},
			{Env: "prod-eu-a", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "prev", Calls: 500, Errors: 5, AvgMs: 400},
			{Env: "prod-eu-b", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "cur", Calls: 300, Errors: 3, AvgMs: 410},
			{Env: "prod-eu-b", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "prev", Calls: 300, Errors: 3, AvgMs: 400},
		},
		points: []chstore.OutboundPoint{{Bucket: 1_000_000, Service: "prod-eu-a", Calls: 10, AvgMs: 900, P95Ms: 2000}},
	}
	installOutboundReader(t, f)
	ctx := armOutboundSlot(context.Background(), "", "", 0, time.Time{})
	run := runOutboundGuided(t, s, ctx, "esbprod prod-eu-a ve prod-eu-b gecikmesi arttı mı", "")
	if len(llm.calls()) != 0 {
		t.Error("LLM'siz")
	}
	q := f.lastQuery
	if !q.ByEnv || strings.Join(q.Envs, ",") != "prod-eu-a,prod-eu-b" || q.PathLike != chstore.OutboundAllPaths || q.PeerHost != obHostESB || strings.Join(q.Services, ",") != obTestSvc {
		t.Errorf("ham ortam sorgusu: %+v", q)
	}
	text, _ := run.answer["text"].(string)
	for _, w := range []string{"ortamlar prod-eu-a, prod-eu-b", "KÜME KIYASI", "- prod-eu-a:", "ARTIŞ VAR", "MV ortam taşımaz"} {
		if !strings.Contains(text, w) {
			t.Errorf("%q yok:\n%s", w, text)
		}
	}
	if len(run.tables) != 1 || run.tables[0].Columns[0].Label != "Ortam" || run.tables[0].Rows[0][0] != "prod-eu-a" {
		t.Errorf("ortam tablosu: %+v", run.tables)
	}
	if run.tools[outboundProbeTool] != 1 || run.tools[outboundCallsTool] != 1 {
		t.Errorf("çipler: %v", run.tools)
	}
}

// TestOutboundHostGuidedAskAndErrors — çözülmeyen host → çipler (okuma yok);
// MV okunamadı → dürüst OKUNAMADI; host bu pencerede çağrılmıyor → "bu hosta".
func TestOutboundHostGuidedAskAndErrors(t *testing.T) {
	s, _ := obHostServer(t)
	f := &fakeOutboundReader{}
	installOutboundReader(t, f)
	ctx := armOutboundSlot(context.Background(), "", "", 0, time.Time{})
	run := runOutboundGuided(t, s, ctx, "ESB gecikme grafiği", "")
	if len(f.calls) != 0 {
		t.Errorf("host sorusu okuma yapmamalı: %v", f.calls)
	}
	fu, _ := json.Marshal(run.answer["followups"])
	if !strings.Contains(string(fu), fuKindOutboundHost) || !strings.Contains(string(fu), obHostESBT) {
		t.Errorf("çipler: %s", fu)
	}

	f2 := &fakeOutboundReader{hostErr: errors.New("max_execution_time")}
	installOutboundReader(t, f2)
	run = runOutboundGuided(t, s, ctx, "esbprod'a giden çağrılar yavaşladı mı?", "")
	if text, _ := run.answer["text"].(string); !strings.Contains(text, "OKUNAMADI (okunamadı ≠ çağrı yok)") {
		t.Errorf("okuma hatası: %s", text)
	}

	f3 := &fakeOutboundReader{hostCallers: func(time.Time, time.Time) []chstore.ExternalCaller { return nil }}
	installOutboundReader(t, f3)
	run = runOutboundGuided(t, s, ctx, "esbprod'a giden çağrılar yavaşladı mı?", "")
	if text, _ := run.answer["text"].(string); !strings.Contains(text, "bu hosta giden client span YOK") {
		t.Errorf("boş pencere: %s", text)
	}
}

// TestOutboundProbeWindowWiden — R1 açık notu: sonda yolu 24 sa MV
// penceresinde buldu, varsayılan 1 sa boş → ölçüm ≤6 sa'e bir kez genişler
// (beyanlı); açık pencere / sondasız rota genişlemez.
func TestOutboundProbeWindowWiden(t *testing.T) {
	s, _ := obHostServer(t)
	f := &fakeOutboundReader{aggsFn: func(q chstore.OutboundQuery) []chstore.OutboundAgg {
		if q.To.Sub(q.From) <= time.Hour {
			return nil
		}
		return []chstore.OutboundAgg{{Service: obTestSvc, Peer: obHostESB, Path: "/esb/external/rest/customer-kvkk", Win: "cur", Calls: 50, AvgMs: 300}}
	}}
	installOutboundReader(t, f)
	to := time.Now()
	route := outboundHit{Path: obTestPath, Callers: []string{obTestSvc}, Peer: obHostESB, Src: "mv", ProbeS: 86400}.route()
	evs, emit := captureEmit()
	if h, _ := s.guidedOutboundAnswer(context.Background(), emit, route, nil, to.Add(-time.Hour), to); !h {
		t.Fatal("handled")
	}
	if got := f.lastQuery.To.Sub(f.lastQuery.From); got != 6*time.Hour {
		t.Errorf("genişleyen pencere %v, want 6 sa", got)
	}
	text := (*evs)[len(*evs)-1].payload.(map[string]any)["text"].(string)
	for _, w := range []string{"son 1 saat içinde bu yola giden çağrı yok", "son 1 gün içinde bulmuştu", "6 saat'e genişletildi", "50 çağrı", "son 6 saat"} {
		if !strings.Contains(text, w) {
			t.Errorf("%q yok:\n%s", w, text)
		}
	}
	s, _ = obHostServer(t) // 60 sn önbellek temiz
	f.calls = nil
	route.OutboundProbeS = 0
	_, emit = captureEmit()
	s.guidedOutboundAnswer(context.Background(), emit, route, nil, to.Add(-time.Hour), to)
	if n := strings.Count(strings.Join(f.calls, " "), "aggs"); n != 1 {
		t.Errorf("sondasız rota tek okuma: %v", f.calls)
	}
	// açık pencere: guidedOutboundRoute ProbeS'i sıfırlar.
	s, _ = obHostServer(t)
	ctx := armOutboundSlot(context.Background(), obTestSvc, "", 0, time.Time{})
	f.calls = nil
	_, emit = captureEmit()
	s.guidedOutboundRoute(ctx, emit, outboundHit{Path: obTestPath, Callers: []string{obTestSvc}, Src: "mv", ProbeS: 86400}, obTestAsk+" son 1 saat", normalizeGuidedMsg(obTestAsk+" son 1 saat"),
		[]copilot.ChatMessage{{Role: "user", Text: obTestAsk + " son 1 saat"}}, "", obTestSvc, "", time.Time{}, time.UTC)
	if n := strings.Count(strings.Join(f.calls, " "), "aggs"); n != 1 {
		t.Errorf("açık pencere genişlemez: %v", f.calls)
	}
	// sonda MV penceresi hit'e yazılır; ≥2 ortam hit'e girer.
	f4 := &fakeOutboundReader{mv: func(string, string) ([]chstore.OutboundCaller, error) {
		return []chstore.OutboundCaller{{Service: obTestSvc, Host: obHostESB, Calls: 5}}, nil
	}}
	installOutboundReader(t, f4)
	sctx := armOutboundSlot(context.Background(), obTestSvc, "", 86400, time.Now())
	h, ok := (&Server{}).outboundProbe(sctx, func(string, any) {}, outboundSlotFrom(sctx), obTestPath, "", obEnvs)
	if !ok || h.ProbeS != 86400 || strings.Join(h.Envs, ",") != "prod-eu-a,prod-eu-b" || h.Env != "" {
		t.Errorf("hit: %+v", h)
	}
	if r := h.route(); strings.Join(r.EnvSet, ",") != "prod-eu-a,prod-eu-b" || r.OutboundProbeS != 86400 {
		t.Errorf("rota: %+v", r)
	}
}

// TestOutboundR2SourcePins — router sırası ve kablolama kaynak pinleri.
func TestOutboundR2SourcePins(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	g := read("copilot_guided.go")
	rb := g[strings.Index(g, "func routeGuidedIntentExt("):]
	io, it, im := strings.Index(rb, "routeOutboundCall("), strings.Index(rb, "topoStrong :="), strings.Index(rb, "routeMetricChart(")
	if io < 0 || io > it || io > im {
		t.Errorf("routeOutboundCall topoloji (%d) ve metrik grafiğinden (%d) ÖNCE olmalı: %d", it, im, io)
	}
	if strings.Index(rb, "extractTraceID(msg)") > io || strings.Index(rb, "routePastedPromQL(") > io {
		t.Error("trace ID / yapıştırılan PromQL giden çağrıdan önce kalmalı")
	}
	gb := g[strings.Index(g, "func (s *Server) copilotChatGuided("):]
	if !strings.Contains(gb, "routeGuidedIntentExt(routerQ, svcNames, envNames, teamNames, routerSvc, s.oracleOpsEnabled(), s.guidedOutboundHosts(ctx, norm))") {
		t.Error("guided kademesi canlı dış host kataloğunu geçirmeli")
	}
	pre := read("chat_operation_prefetch.go")
	body := pre[strings.Index(pre, "func (s *Server) chatOperationPrefetch("):]
	if iv, ir := strings.Index(body, "outboundVetoesOpPrefetch(raw)"), strings.Index(body, "s.resolveEndpointMention("); iv < 0 || iv > ir {
		t.Error("ön-getirme vetosu endpoint çözümünden önce")
	}
	if strings.Contains(read("copilot_intent.go"), "guidedOutboundCall") {
		t.Error("R2: outbound_call sınıflandırıcı listesine girmez")
	}
}
