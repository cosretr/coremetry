package api

// chat_bare_entity_test.go — v0.10.2139 (CoSRE router boşlukları D6,
// docs/cosre/router-gaps-audit.md D + G). Prod: yazım hatalı tek ad ve
// "# svc-a-prod" sınıflandırıcıda none'a düşüyordu; tek satırda çitle başlayan
// hata gövdesi ve JSON parametre dökümü hiçbir şekle oturmuyordu. Yalnız v2.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

func TestBareEntityNearQuery(t *testing.T) {
	cases := []struct {
		q    string
		want string
		ok   bool
	}{
		{"ordrs-api", "ordrs-api", true},
		{"# ordrs-api", "ordrs-api", true},
		{"biling api", "biling api", true},
		{"73066", "", false}, // salt sayı eşleşme kurmaz (v0.10.2112)
		{"12 34", "", false},
		{"ab", "", false},
		{"svc-a trace detail sayfası", "", false}, // >2 jeton
	}
	for _, c := range cases {
		got, ok := bareEntityNearQuery(c.q)
		if ok != c.ok || got != c.want {
			t.Errorf("bareEntityNearQuery(%q) = (%q,%v), want (%q,%v)", c.q, got, ok, c.want, c.ok)
		}
	}
}

func TestV2BareServiceEnv(t *testing.T) {
	svcs := []string{"svc-a", "svc-b", "svc-c-prod"}
	envs := []string{"prod", "uat"}
	cases := []struct {
		q        string
		svc, env string
		ok       bool
	}{
		{"# svc-a-prod", "svc-a", "prod", true},
		{"svc-a-uat", "svc-a", "uat", true},
		{"#svc-b", "svc-b", "", true},
		{"svc-b", "svc-b", "", false},           // çıplak tam ad find_entity'de kalır
		{"svc-c-prod", "svc-c-prod", "", false}, // canlı ad ortam ekiyle bitiyor: ad aynen (find_entity)
		{"# svc-z-prod", "", "", false},         // servis yok
		{"# svc-a-test", "", "", false},         // canlı ortam değil
		{"# svc-a prod", "", "", false},         // iki jeton
	}
	for _, c := range cases {
		svc, env, ok := v2BareServiceEnv(c.q, svcs, envs)
		if ok != c.ok || (ok && (svc != c.svc || env != c.env)) {
			t.Errorf("v2BareServiceEnv(%q) = (%q,%q,%v), want (%q,%q,%v)", c.q, svc, env, ok, c.svc, c.env, c.ok)
		}
	}
}

func TestEntityPickQuestion(t *testing.T) {
	q, ok := entityPickQuestion("soru", []string{"svc-a", "svc-a", "svc-b", "svc-c", "svc-d"})
	if !ok || len(q.Options) != entityPickMax || q.Options[1].ID != "svc-b" || q.Kind != questionKindEntityPick || !q.AllowFree {
		t.Fatalf("kart: %+v", q)
	}
	if !slices.Contains(uistream.QuestionKinds, q.Kind) || len(q.Options) > uistream.QuestionMaxOptions {
		t.Fatalf("kapalı küme / tavan ihlali: %+v", q)
	}
	for _, o := range q.Options {
		if o.ID != o.Label {
			t.Errorf("etiket servis adının kendisi olmalı (tık = ad, find_entity tek servise çözer): %+v", o)
		}
	}
	if _, ok := entityPickQuestion("", []string{"svc-a"}); ok {
		t.Error("metinsiz kart olmamalı")
	}
	if _, ok := entityPickQuestion("soru", nil); ok {
		t.Error("seçeneksiz kart olmamalı")
	}
	if !strings.Contains(entityNearAskTR("ordrs-api", []string{"orders-api"}), "orders-api servisini mi") {
		t.Error("tek aday cümlesi")
	}
}

func TestV2EntityNearAsk(t *testing.T) {
	s, mc := newLoopTestServer(t, noLLM(t), "u-near")
	mc.m["copilot:guided:svcnames"] = []byte(`["orders-api","billing-api","shop-payment-api"]`)
	var events []string
	var answer map[string]any
	var question questionPayload
	emit := func(kind string, v any) {
		events = append(events, kind)
		switch kind {
		case "answer":
			answer, _ = v.(map[string]any)
		case chatQuestionEventKind:
			question, _ = v.(questionPayload)
		}
	}
	if s.v2EntityNearAsk(context.Background(), emit, "ordrs-api") {
		t.Fatal("v1: kademe çalışmamalı")
	}
	ctx := withCosreV2Loop(context.Background())
	if !s.v2EntityNearAsk(ctx, emit, "ordrs-api") {
		t.Fatal("v2: yazım hatalı ad en yakın servisi sormalı")
	}
	if len(question.Options) == 0 || question.Options[0].ID != "orders-api" {
		t.Fatalf("kart: %+v (olaylar %v)", question, events)
	}
	if txt, _ := answer["text"].(string); !strings.Contains(txt, "bulamadım") {
		t.Fatalf("düz metin soru: %v", answer)
	}
	events = nil
	if s.v2EntityNearAsk(ctx, emit, "73066") || len(events) != 0 {
		t.Fatalf("salt sayı eşleşme kurmamalı: %v", events)
	}
	if s.v2EntityNearAsk(ctx, emit, "zzzqqq") || len(events) != 0 {
		t.Fatalf("aday yoksa eski yol: %v", events)
	}
}

func TestStripPasteFences(t *testing.T) {
	cases := map[string]string{
		"``` Caused by: DisconnectException: connection reset":        "Caused by: DisconnectException: connection reset",
		"```textNativeShellDispatch Params: {\"id\": 12}":             "NativeShellDispatch Params: {\"id\": 12}",
		"```java\nat com.acme.X.run(X.java:12)\n```":                  "at com.acme.X.run(X.java:12)",
		"```\nCaused by: com.acme.db.DisconnectException: reset\n```": "Caused by: com.acme.db.DisconnectException: reset",
		"svc-a hataları neler":                                        "svc-a hataları neler",
		"```textual analysis```":                                      "textual analysis", // "text" + küçük harf = etiket değil
	}
	for in, want := range cases {
		if got := stripPasteFences(in); got != want {
			t.Errorf("stripPasteFences(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestV2PastedQuestionAndParams(t *testing.T) {
	fenced := "``` Caused by: DisconnectException: connection reset by peer"
	if looksLikePastedError(fenced) {
		t.Fatal("ön koşul: çitli tek satır bugün şekle oturmuyor (audit G)")
	}
	if q, ok := v2PastedQuestion(fenced, true); !ok || !strings.HasPrefix(q, "Caused by:") {
		t.Fatalf("v2 çit soyma: (%q,%v)", q, ok)
	}
	if q, ok := v2PastedQuestion(fenced, false); ok || q != fenced {
		t.Fatal("v1 metni aynen")
	}
	r, ok := routePastedError(stripPasteFences(fenced), "", nil, nil)
	if !ok || r.LogValue != "DisconnectException" {
		t.Fatalf("soyulmuş metin log_field terimi: %+v %v", r, ok)
	}
	if _, ok := v2PastedQuestion("svc-a ```kod``` nedir", true); ok {
		t.Error("çit var ama hata şekli yok: yapıştırma sayılmamalı")
	}

	dump := stripPasteFences("```textNativeShellDispatch Params: {\"id\": 12, \"name\": \"x\"}")
	pr, ok := v2PastedParamsRoute(dump, nil)
	if !ok || pr.Intent != guidedLogField || pr.LogValue != "NativeShellDispatch" || pr.LogField != "message" || !pr.LogContains {
		t.Fatalf("parametre dökümü: %+v %v", pr, ok)
	}
	for _, q := range []string{"Params: {", "svc-a params nedir?", "NativeShellDispatch Params: { nedir?"} {
		if _, ok := v2PastedParamsRoute(q, nil); ok {
			t.Errorf("%q döküm sayılmamalı", q)
		}
	}
}

// Bağlar: v2 yapıştırma metni router'a, döküm rotası yalnız none iken, "#"
// servis biçimi none/find_entity iken; entity_pick yalnız tarama demetinde.
func TestV2BareEntityWiring(t *testing.T) {
	g := stripGoCommentsAPI(readRepoFile(t, "copilot_guided.go"))
	for _, want := range []string{
		"pastedQ, fenced := v2PastedQuestion(question, v2)",
		"pasted = pasted || fenced || paramsDump",
		"if v2 && route.Intent == guidedNone && paramsDump {",
		"if v2 && (route.Intent == guidedNone || route.Intent == guidedFindEntity) {",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("copilot_guided.go bağı eksik: %q", want)
		}
	}
	ns := funcBody(stripGoCommentsAPI(readRepoFile(t, "namespace_guided.go")), "guidedEntityScanAnswer")
	if !strings.Contains(ns, "err == nil && len(cands) == 0 && s.v2EntityNearAsk(ctx, emit, q)") || !strings.Contains(ns, "v2EntityCandidatesCard(ctx, emit, text, chips)") {
		t.Errorf("tarama demeti v2 kartını bağlamalı:\n%s", ns)
	}
}
