package api

// chat_service_pattern_test.go — v0.10.2471 (operator-reported 2026-11-26):
// servis adı kalıbı ayrıştırıcısı, çözüm kararı (0 / 1 / küme / tavan), küme
// devam sorusu, genel bakış montajı (eksik kaynaklarla) ve küme kapsamı.
// Adlar sentetik (harvester-a, harvester-b, orders-api).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

func TestParseServicePattern(t *testing.T) {
	type want struct {
		kind, pattern string
		explicit      bool
	}
	pos := map[string]want{
		// operatörün cümleleri
		"harvester-* ile başlayan servisleri incele": {svcPatPrefix, "harvester-", true},
		"harvester ile başlayan servisler nasıl":     {svcPatPrefix, "harvester", true},
		"harvester-* servislerinde sorun var mı":     {svcPatPrefix, "harvester-", true},
		"adı payment içeren servisler":               {svcPatContains, "payment", true},
		"-batch ile biten servisler":                 {svcPatSuffix, "-batch", true},
		// Türkçe çeşitlemeler
		"HARVESTER İLE BAŞLAYAN SERVİSLER NASIL":         {svcPatPrefix, "harvester", true},
		"harvester'la başlayan servisler":                {svcPatPrefix, "harvester", true},
		"harvester ile baslayan servisleri listele":      {svcPatPrefix, "harvester", true},
		"adında order geçen servisleri listele":          {svcPatContains, "order", true},
		"servis adı pay ile başlayanlar nasıl":           {svcPatPrefix, "pay", true},
		"harvester önekli servisler nasıl":               {svcPatPrefix, "harvester", true},
		"batch sonekli servislerde hata var mı":          {svcPatSuffix, "batch", true},
		"son 1 saatte *-batch servisleri nasıl":          {svcPatSuffix, "-batch", true},
		"*payment* servislerini incele":                  {svcPatContains, "payment", true},
		"pay*-api servisleri nasıl":                      {svcPatGlob, "pay*-api", true},
		"`harvester-*` servisleri nasıl?":                {svcPatPrefix, "harvester-", true},
		"\"harvester-*\" adlı servisler":                 {svcPatPrefix, "harvester-", true},
		"hata veren harvester-* servisleri":              {svcPatPrefix, "harvester-", true},
		"harvester-* incele":                             {svcPatPrefix, "harvester-", false}, // yumuşak: servis sözcüğü yok
		"harvester-*'lerde hata var mı":                  {svcPatPrefix, "harvester-", false},
		"harvester- ile başlayanlar nasıl":               {svcPatPrefix, "harvester-", false},
		"harvester-* için logları getir":                 {svcPatPrefix, "harvester-", false},
		"harvester-* ile başlayan servislerin logları":   {svcPatPrefix, "harvester-", true},
		"adında payment geçenler nasıl":                  {svcPatContains, "payment", false},
		"prod ortamında harvester-* servisleri nasıl":    {svcPatPrefix, "harvester-", true},
		"servislerden harvester ile başlayanlar hangisi": {svcPatPrefix, "harvester", true},
		"adı pay ile başlayanlar nasıl":                  {svcPatPrefix, "pay", false}, // ad sözcüğü: yumuşak kabul
		// İngilizce
		"services starting with harv":                   {svcPatPrefix, "harv", true},
		"how are the services that start with harv-":    {svcPatPrefix, "harv-", true},
		"show services ending with -worker":             {svcPatSuffix, "-worker", true},
		"services whose name contains pay":              {svcPatContains, "pay", true},
		"services with pay in their name":               {svcPatContains, "pay", true},
		"any problem in harvester-* services?":          {svcPatPrefix, "harvester-", true},
		"services prefixed with harvester look healthy": {svcPatPrefix, "harvester", true},
	}
	for q, w := range pos {
		p, ok := parseServicePattern(q)
		if !ok {
			t.Errorf("%q: kalıp bekleniyordu", q)
			continue
		}
		if p.Kind != w.kind || p.Pattern != w.pattern || p.Explicit != w.explicit {
			t.Errorf("%q → {%s %q explicit=%v}, want {%s %q explicit=%v}", q, p.Kind, p.Pattern, p.Explicit, w.kind, w.pattern, w.explicit)
		}
		if p.Start < 0 || p.End > len(q) || p.Start >= p.End || !strings.Contains(strings.ToLower(strings.ReplaceAll(q[p.Start:p.End], "İ", "i")), strings.Trim(p.Pattern, "*-")[:2]) {
			t.Errorf("%q: deyiş aralığı [%d,%d) kalıbı kapsamıyor", q, p.Start, p.End)
		}
	}
	neg := []string{
		// sıradan tek servis soruları
		"orders-api servisi nasıl",
		"orders-api sağlığı nasıl?",
		"harvester-a neden yavaş",
		"servislerim nasıl",
		"en çok hata veren servisler hangileri",
		// yıldız URL / yol / sorgu içinde
		"/api/v1/* endpointlerinde hata var mı",
		"https://example.test/api/* yavaş mı",
		"GET /orders/* rotası neden yavaş",
		`rate(http_requests_total{service=~"harv.*"}[5m]) grafiğini çiz`,
		"sum(rate(http_server_duration_count[5m])) by (service) * 100",
		"select * from spans where service = 'orders-api'",
		"service.name:harv* logları",
		"5 * 3 kaç eder",
		"**önemli** servis hangisi",
		"*.example.test alan adına giden çağrılar",
		"a* servisleri",
		// başka nesneyi niteleyen deyiş
		"GET ile başlayan endpointler hangileri",
		"harvester-* podları nasıl",
		"pay* topiclerini kim tüketiyor",
		"hata ile başlayan loglar",
		"mesajı timeout ile biten loglar",
		"pods starting with harvester",
		"url'inde harv* içeren trace var mı",
		// servis kümesi olmayan gündelik deyiş
		"deploy ile başlayan yavaşlama neden",
		"dün gece ile başlayan sorun sürüyor mu",
		"why did the request end with timeout",
		"let's start with orders-api",
		"Son 1 saat içerisinde url'inde ocp5 içeren bir servis çağrımı veya trace'i var mı?",
		"orders içeren trace var mı",
		"",
	}
	for _, q := range neg {
		if p, ok := parseServicePattern(q); ok {
			t.Errorf("%q: kalıp OLMAMALI, got {%s %q}", q, p.Kind, p.Pattern)
		}
	}
}

func TestServicePatternLabelAndMatch(t *testing.T) {
	names := []string{"Harvester-A", "harvester-b", "orders-api", "payment-batch", "billing-batch", "pay-core-api", "harvester-b"}
	cases := []struct {
		p     svcPattern
		label string
		want  []string
	}{
		{svcPattern{Kind: svcPatPrefix, Pattern: "harvester-"}, "harvester-*", []string{"Harvester-A", "harvester-b"}},
		{svcPattern{Kind: svcPatSuffix, Pattern: "-batch"}, "*-batch", []string{"payment-batch", "billing-batch"}},
		{svcPattern{Kind: svcPatContains, Pattern: "pay"}, "*pay*", []string{"payment-batch", "pay-core-api"}},
		{svcPattern{Kind: svcPatGlob, Pattern: "pay*-api"}, "pay*-api", []string{"pay-core-api"}},
		{svcPattern{Kind: svcPatGlob, Pattern: "*-b*ch"}, "*-b*ch", []string{"payment-batch", "billing-batch"}},
		{svcPattern{Kind: svcPatPrefix, Pattern: "zzqx"}, "zzqx*", nil},
	}
	for _, c := range cases {
		if got := c.p.Label(); got != c.label {
			t.Errorf("label %q, want %q", got, c.label)
		}
		if got := matchServicePattern(c.p, names); !slices.Equal(got, c.want) {
			t.Errorf("%s → %v, want %v (büyük/küçük harf duyarsız, katalog sırası, tekrar yok)", c.label, got, c.want)
		}
	}
	for pat, s := range map[string]string{"a*b": "ab", "a*b*c": "a-b-c", "*x": "x", "x*": "x"} {
		if !globMatch(pat, s) {
			t.Errorf("globMatch(%q, %q) true olmalı", pat, s)
		}
	}
	for pat, s := range map[string]string{"a*b": "ba", "a*b*c": "acb", "ab": "abc", "a*bc": "abcd"} {
		if globMatch(pat, s) {
			t.Errorf("globMatch(%q, %q) false olmalı", pat, s)
		}
	}
}

// Çözüm kararı: 0 → yok + yakın adlar; 1 → tek servis; 2..N → küme; N > tavan
// → hata, sonra trafik sırasıyla ilk tavan + kalan sayı.
func TestPlanServiceSetOutcomes(t *testing.T) {
	cat := []string{"harvester-a", "harvester-b", "orders-api", "billing-api"}
	p := svcPattern{Kind: svcPatPrefix, Pattern: "harvestr-"}
	if got := planServiceSet(p, nil, cat, nil, "", servicePatternCap); got.Outcome != setOutcomeNone || !slices.Contains(got.Near, "harvester-a") || got.Total != 0 {
		t.Errorf("0 eşleşme: %+v (yakın adlar katalogdan gelmeli)", got)
	}
	if got := planServiceSet(svcPattern{Kind: svcPatPrefix, Pattern: "zzqx"}, nil, cat, nil, "", servicePatternCap); got.Outcome != setOutcomeNone || len(got.Near) != 0 {
		t.Errorf("yakın ad yok: %+v (ad uydurulmaz)", got)
	}
	if got := planServiceSet(p, []string{"orders-api"}, cat, nil, "", servicePatternCap); got.Outcome != setOutcomeSingle || !slices.Equal(got.Services, []string{"orders-api"}) {
		t.Errorf("1 eşleşme: %+v", got)
	}
	if got := planServiceSet(p, []string{"harvester-b", "harvester-a"}, cat, nil, "", servicePatternCap); got.Outcome != setOutcomeSet ||
		!slices.Equal(got.Services, []string{"harvester-b", "harvester-a"}) || got.Total != 2 || got.LeftOut != 0 {
		t.Errorf("küme: %+v", got)
	}
	var many []string
	rank := map[string]svcRank{}
	for i := range 60 {
		n := fmt.Sprintf("harvester-%02d", i)
		many = append(many, n)
		rank[n] = svcRank{Requests: float64(i)}
	}
	rank["harvester-07"] = svcRank{Errors: 9, Requests: 1}
	rank["harvester-03"] = svcRank{Errors: 9, Requests: 5}
	got := planServiceSet(p, many, many, rank, "metric", servicePatternCap)
	if got.Outcome != setOutcomeCapped || len(got.Services) != servicePatternCap || got.Total != 60 || got.LeftOut != 10 || got.RankedBy != "metric" {
		t.Fatalf("tavan: %+v", got)
	}
	if got.Services[0] != "harvester-03" || got.Services[1] != "harvester-07" || got.Services[2] != "harvester-59" {
		t.Errorf("sıra hata azalan, sonra istek azalan olmalı: %v", got.Services[:4])
	}
	if slices.Contains(got.Services, "harvester-00") {
		t.Error("en az trafikli servis tavan dışında kalmalı")
	}
	blind := planServiceSet(p, many, many, nil, "", servicePatternCap)
	if blind.RankedBy != "name" || blind.Services[0] != "harvester-00" || blind.LeftOut != 10 {
		t.Errorf("sıra okunamadı → alfabetik: %+v", blind)
	}
	if len(many) != 60 {
		t.Fatal("girdi yerinde sıralanmamalı")
	}
}

func TestServicePatternNoneAnswer(t *testing.T) {
	p := svcPattern{Kind: svcPatPrefix, Pattern: "harvestr-"}
	text, chips := servicePatternNoneTR(p, 1200, []string{"harvester-a", "harvester-b"})
	for _, w := range []string{"«harvestr-*»", "önek", "bulamadım", "1,200", "harvester-a", "harvester-b"} {
		if !strings.Contains(text, w) {
			t.Errorf("cevap %q taşımalı:\n%s", w, text)
		}
	}
	if !slices.Equal(chips, []string{"harvester-a sağlığı nasıl?", "harvester-b sağlığı nasıl?"}) {
		t.Errorf("çipler: %v", chips)
	}
	if text, chips := servicePatternNoneTR(p, 0, nil); len(chips) != 0 || !strings.Contains(text, "Yakın bir ad da yok") {
		t.Errorf("yakın ad yok: %q %v", text, chips)
	}
}

func TestRewriteSinglePattern(t *testing.T) {
	for q, want := range map[string]string{
		"orders ile başlayan servisler nasıl":     "orders-api servisler nasıl",
		"orders-* servislerinde sorun var mı":     "orders-api servislerinde sorun var mı",
		"adı orders içeren servisin logları":      "orders-api servisin logları",
		"son 1 saatte orders-* servisleri nasıl?": "son 1 saatte orders-api servisleri nasıl?",
	} {
		p, ok := parseServicePattern(q)
		if !ok {
			t.Fatalf("%q: kalıp yok", q)
		}
		if got := rewriteSinglePattern(q, p, "orders-api"); got != want {
			t.Errorf("%q → %q, want %q", q, got, want)
		}
	}
}

func TestServiceSetFocusAndAsk(t *testing.T) {
	for q, want := range map[string]string{
		"hangisi en yavaş":           setFocusSlow,
		"p99'u en yüksek olan":       setFocusSlow,
		"hata verenleri göster":      setFocusErrors,
		"sadece hatalı olanlar":      setFocusErrors,
		"en yoğun hangisi":           setFocusTraffic,
		"açık problemi olanlar":      setFocusProblems,
		"servisleri incele":          "",
		"servislerinde sorun var mı": "",
		"servisler nasıl":            "",
	} {
		norm := normalizeGuidedMsg(q)
		if got := serviceSetFocus(norm, guidedTokens(norm)); got != want {
			t.Errorf("odak %q → %q, want %q", q, got, want)
		}
	}
	for q, other := range map[string]bool{
		"servislerinin loglarında hata var mı": true,
		"servisleri neden yavaş":               true,
		"servislerinin son deploy'u":           true,
		"servislerinin en yavaş trace'leri":    true,
		"servislerinin podları":                true,
		"servisleri incele":                    false,
		"servislerinde sorun var mı":           false,
		"hangisi en yavaş":                     false,
		"hata verenleri göster":                false,
	} {
		if got := serviceSetOtherAsk(guidedTokens(normalizeGuidedMsg(q))); got != other {
			t.Errorf("başka nesne sorusu %q → %v, want %v", q, got, other)
		}
	}
}

func TestServiceSetFollowUp(t *testing.T) {
	cases := []struct {
		q       string
		lastSet bool
		named   bool
		focus   string
		ok      bool
	}{
		{"hangisi en yavaş", true, false, setFocusSlow, true},
		{"hata verenleri göster", true, false, setFocusErrors, true},
		{"sadece hatalı olanlar", true, false, setFocusErrors, true},
		{"en yoğun hangisi", true, false, setFocusTraffic, true},
		{"bu servisler nasıl", false, false, "", true},
		{"bunlardan hangisi en yavaş", false, false, setFocusSlow, true},
		// çapa yok (araya tek servis turu girdi) ve küme işaret edilmiyor
		{"hangisi en yavaş", false, false, "", false},
		{"hata verenleri göster", false, false, "", false},
		// servis adı yazıldı → o servisin sorusu
		{"harvester-a en yavaş mı", true, true, "", false},
		// başka nesne → normal zincir (v2'de küme kapsamıyla döngü)
		{"en yavaş trace'leri göster", true, false, "", false},
		{"loglarında hata var mı", true, false, "", false},
		{"neden yavaş", true, false, "", false},
		// kümeyi bırakan deyiş
		{"tüm servisler nasıl", true, false, "", false},
		{"teşekkürler", true, false, "", false},
		{"", true, false, "", false},
	}
	for _, c := range cases {
		focus, ok := serviceSetFollowUp(c.q, c.lastSet, c.named)
		if ok != c.ok || focus != c.focus {
			t.Errorf("%q (lastSet=%v named=%v) → (%q, %v), want (%q, %v)", c.q, c.lastSet, c.named, focus, ok, c.focus, c.ok)
		}
	}
	if !clearsServiceSet("tüm servisler nasıl") || !clearsServiceSet("kapsamı kaldır") || clearsServiceSet("her birinin p99'u") {
		t.Error("küme bırakma deyişleri")
	}
}

func TestFreshServiceSetAndState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	set := &chatServiceSet{Kind: svcPatPrefix, Pattern: "harvester-", Label: "harvester-*", Services: []string{"harvester-a", "harvester-b"}, Total: 2, At: now.UnixNano()}
	c := ChatContext{Service: "orders-api", Operation: "GET /x", Trace: "4bf92f3577b34da6a3ce929d0e0e4736"}
	st := &chatContextState{ctx: c}
	st.noteServiceSet(set)
	if !st.dirty || st.ctx.ServiceSet == nil || st.ctx.Service != "" || st.ctx.Operation != "" || st.ctx.Trace != "" {
		t.Fatalf("küme kapsam olunca tek servis öznesi düşmeli: %+v", st.ctx)
	}
	if freshServiceSet(st.ctx, now.Add(serviceSetTTL-time.Second)) == nil || freshServiceSet(st.ctx, now.Add(serviceSetTTL+time.Second)) != nil {
		t.Error("ömür sınırı")
	}
	if !set.has("HARVESTER-A") || set.has("orders-api") || set.leftOut() != 0 {
		t.Error("üyelik / tavan dışı")
	}
	// Kalıp yazılmadıkça alan JSON'a girmez (v1 bağlamı bayt bayt eski).
	if b, _ := json.Marshal(ChatContext{Service: "orders-api"}); strings.Contains(string(b), "serviceSet") {
		t.Errorf("boş küme JSON'a sızdı: %s", b)
	}
	var nilSt *chatContextState
	nilSt.noteServiceSet(set) // nil durum no-op
}

// Küme kapsamı takım kapsamıyla AYNI mekanizmadan iner: data-scope olayı,
// döngü eki ve `services` argümanı enjeksiyonu.
func TestServicePatternScopeReusesUserScope(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	set := &chatServiceSet{Kind: svcPatPrefix, Pattern: "harvester-", Label: "harvester-*", Services: []string{"harvester-a", "harvester-b"}, Total: 60, At: now.UnixNano()}
	us := servicePatternScope(set)
	if us.Unscoped || us.Reason != scopeReasonPattern || us.Team != "" || us.Trimmed != 58 {
		t.Fatalf("kapsam: %+v", us)
	}
	if !slices.Contains(uistream.ScopeReasons, scopeReasonPattern) {
		t.Fatal("pattern nedeni uistream.ScopeReasons'ta yok")
	}
	ev := us.event()
	if ev["reason"] != "pattern" || ev["pattern"] != "harvester-*" || ev["count"] != 60 || ev["unscoped"] != false || ev["trimmed"] != 58 {
		t.Errorf("data-scope: %v", ev)
	}
	if _, has := ev["team"]; has {
		t.Errorf("takım alanı olmamalı: %v", ev)
	}
	add := userScopeAddendum(us, nil)
	for _, w := range []string{"SERVİS KÜMESİ KAPSAMI", `"harvester-*"`, "harvester-a, harvester-b", "58 tanesi tavan dışında"} {
		if !strings.Contains(add, w) {
			t.Errorf("ek %q taşımalı:\n%s", w, add)
		}
	}
	if strings.Contains(add, "takımı") || add != copilot.ChatServicePatternScopeAddendum("harvester-*", 60, set.Services) {
		t.Errorf("küme eki takım ekinden ayrı olmalı:\n%s", add)
	}
	// Enjeksiyon: şema `services` alıyorsa küme konur; `team` KONMAZ.
	schema := map[string]any{"properties": map[string]any{"services": map[string]any{"type": "array"}, "team": map[string]any{"type": "string"}}}
	args, applied := applyUserScope("open_problems", json.RawMessage(`{}`), schema, us)
	if !applied || !strings.Contains(string(args), `"services":["harvester-a","harvester-b"]`) || strings.Contains(string(args), `"team"`) {
		t.Errorf("enjeksiyon: applied=%v args=%s", applied, args)
	}
	// Etkin küme takım kapsamının yerine geçer; kaçışta ve ömrü dolunca geçmez.
	team := userScope{Reason: scopeReasonTeam, Team: "acme", Services: []string{"orders-api"}}
	st := &chatContextState{ctx: ChatContext{ServiceSet: set}}
	if got := activeServicePatternScope(team, st, false, now.Add(time.Minute)); got.Reason != scopeReasonPattern {
		t.Errorf("etkin küme kapsam olmalı: %+v", got)
	}
	if got := activeServicePatternScope(userScope{Unscoped: true, Reason: scopeReasonDisabled}, st, false, now); got.Reason != scopeReasonPattern {
		t.Errorf("kullanıcı kapsamı kapalıyken de açık seçim uygulanır: %+v", got)
	}
	for name, got := range map[string]userScope{
		"kaçış (@ / komut)": activeServicePatternScope(team, st, true, now),
		"ömrü doldu":        activeServicePatternScope(team, st, false, now.Add(serviceSetTTL+time.Minute)),
		"küme yok":          activeServicePatternScope(team, &chatContextState{}, false, now),
		"durum yok":         activeServicePatternScope(team, nil, false, now),
	} {
		if got.Reason != scopeReasonTeam {
			t.Errorf("%s: takım kapsamı kalmalı, got %+v", name, got)
		}
	}
}

func setFixtureData() serviceSetData {
	return serviceSetData{
		MetricStatus: setSecOK,
		Metric: map[string]serviceREDMetricRow{
			"harvester-a": {Source: "http", Requests: 36000, RatePerS: 20, ErrorRate: f64(2.5), P99Ms: f64(840)},
			"harvester-b": {Source: "spanmetrics", Requests: 1800, RatePerS: 1, ErrorRate: f64(0), P99Ms: f64(120)},
			"harvester-e": {Source: "http", Requests: 900, RatePerS: 0.5}, // durum etiketi / gecikme yok
		},
		SpanOK: true,
		Span: []chstore.ServiceSummary{
			{Name: "harvester-a", SpanCount: 5000, ErrorCount: 900, ErrorRate: 18, P99Ms: 700}, // metrik ezer
			{Name: "harvester-c", SpanCount: 1200, ErrorCount: 48, ErrorRate: 4, P99Ms: 2300},
		},
		Summary: teamSummary{ProblemsOK: true, P1: 1, P2: 1, OpenBySvc: map[string]int{"harvester-a": 2}, P1BySvc: map[string]int{"harvester-a": 1},
			ExceptionsOK: true, Exceptions: []teamSumException{{Service: "harvester-a", Type: "java.net.SocketTimeoutException", Message: "Read timed out", Occurrences: 1432}}},
		AnomalyOK: true, Anomalies: map[string]int{"harvester-c": 1},
	}
}

var setFixtureNames = []string{"harvester-a", "harvester-b", "harvester-c", "harvester-d", "harvester-e"}

func rowNames(v serviceSetView) []string {
	out := make([]string, 0, len(v.Rows))
	for _, r := range v.Rows {
		out = append(out, r.Name)
	}
	return out
}

// Tam montaj: metrik önce (örneklenmemiş), span yalnız metrikte olmayan
// serviste ve etiketli, sorunlu servis önde, sayılar okuyuculardan.
func TestAssembleServiceSetFull(t *testing.T) {
	v := assembleServiceSet(serviceSetInput{Label: "harvester-*", Kind: svcPatPrefix, Names: setFixtureNames, Total: 5, RangeS: 1800, Data: setFixtureData()})
	if got := rowNames(v); !slices.Equal(got, []string{"harvester-a", "harvester-c", "harvester-b", "harvester-e", "harvester-d"}) {
		t.Fatalf("sıra (sorunlu önce; metrik, span, verisiz): %v", got)
	}
	a := v.Rows[0]
	if a.Source != "metric" || a.ErrPct != 2.5 || a.P99Ms != 840 || a.RatePerS != 20 || a.Errors != 900 || a.Problems != 2 || a.P1 != 1 {
		t.Errorf("metrik satırı span'i ezmeli (örneklenmemiş): %+v", a)
	}
	if c := v.Rows[1]; c.Source != "span" || c.Spans != 1200 || c.ErrPct != 4 || c.Anomalies != 1 {
		t.Errorf("metrikte olmayan servis span'den: %+v", c)
	}
	if e := v.Rows[3]; e.HasErr || e.HasP99 {
		t.Errorf("ölçülmeyen hata oranı / p99 sıfır sayılmamalı: %+v", e)
	}
	if v.MetricN != 3 || v.SpanN != 1 || v.SilentN != 1 || v.LeftOut != 0 || !v.usable() {
		t.Errorf("sayımlar: %+v", v)
	}
	for sec, got := range map[string]string{"metric": v.MetricStatus, "span": v.SpanStatus, "problem": v.ProblemStatus, "anomaly": v.AnomalyStatus} {
		if got != setSecOK {
			t.Errorf("%s bölümü %q, want ok", sec, got)
		}
	}
	ev := renderServiceSetEvidenceTR(v)
	for _, w := range []string{"«harvester-*»", "5 servis eşleşti", "son " + fmtAgoTR(1800),
		"- harvester-a [metrik · http]: 20.00 istek/sn, hata %2.50, p99 840 ms · açık problem 2 (P1 1)",
		"- harvester-c [span · ÖRNEKLENMİŞ, gerçek oran değil]: 1,200 span, saklanan span'lerde hata payı %4.00 (48 hata), p99 2300 ms · aktif anomali 1",
		"- harvester-e [metrik · http]: 0.50 istek/sn, hata oranı ölçülmüyor (durum etiketi yok)",
		"- harvester-d: bu pencerede metrik ve span verisi yok",
		"P1 1 · P2 1 · P3 0", "java.net.SocketTimeoutException Read timed out (1,432 kez)", "UYDURMA"} {
		if !strings.Contains(ev, w) {
			t.Errorf("kanıt %q taşımalı:\n%s", w, ev)
		}
	}
	if strings.Contains(ev, "18.00") || strings.Contains(ev, "5,000 span") {
		t.Errorf("metrik olan servisin örneklenmiş span değeri kanıta girmemeli:\n%s", ev)
	}
	tail := renderServiceSetTailTR(v)
	for _, w := range []string{"**«harvester-*» servisleri** — 5 servis", "| harvester-a | 20.00 istek/sn | %2.50 | 840 ms | metrik | 2 (P1 1) | 0 |",
		"| harvester-c | 1,200 span | %4.00 | 2300 ms | span (örneklenmiş) | 0 | 1 |", "| harvester-d | — | — | — | veri yok | 0 | 0 |",
		teamOverviewClosingQ, "konuşmanın kapsamı", "1 servis bu pencerede metrik ya da span üretmedi"} {
		if !strings.Contains(tail, w) {
			t.Errorf("sunucu eki %q taşımalı:\n%s", w, tail)
		}
	}
	if !strings.HasPrefix(tail, "\n\n") {
		t.Error("ek anlatımdan boş satırla ayrılmalı")
	}
}

// Eksik kaynaklar: okunamayan "OKUNAMADI"dır, ölçülmeyen "ölçülmüyor"dur;
// hiçbiri sıfır / "yok" diye yazılmaz ve diğer bölümleri düşürmez.
func TestAssembleServiceSetMissingSources(t *testing.T) {
	// 1) Metrik kaynağı VM değil → span'den, not düşülür.
	d := setFixtureData()
	d.MetricStatus, d.Metric = setSecMissing, nil
	v := assembleServiceSet(serviceSetInput{Label: "harvester-*", Names: setFixtureNames, Total: 5, RangeS: 3600, Data: d})
	if v.MetricN != 0 || v.SpanN != 2 || v.Rows[0].Name != "harvester-a" || v.Rows[0].Source != "span" || v.Rows[0].ErrPct != 18 || !v.usable() {
		t.Errorf("VM yok → span satırları: %+v", v.Rows)
	}
	if ev, tail := renderServiceSetEvidenceTR(v), renderServiceSetTailTR(v); !strings.Contains(ev, "metrik RED (örneklenmemiş) = ölçülmüyor (metrik kaynağı VictoriaMetrics değil)") ||
		!strings.Contains(tail, "örneklenmemiş metrik RED ölçülmüyor") {
		t.Errorf("ölçülmüyor notu eksik:\n%s\n%s", ev, tail)
	}
	// 2) Problem ve anomali okunamadı → sayı yazılmaz, "yok" denmez.
	d = setFixtureData()
	d.Summary, d.AnomalyOK, d.Anomalies = teamSummary{}, false, nil
	v = assembleServiceSet(serviceSetInput{Label: "harvester-*", Names: setFixtureNames, Total: 5, RangeS: 1800, Data: d})
	if v.ProblemStatus != setSecUnread || v.AnomalyStatus != setSecUnread || v.Rows[0].Problems != 0 || v.Rows[0].Name != "harvester-a" {
		t.Errorf("okunamayan bölüm: %+v", v)
	}
	ev, tail := renderServiceSetEvidenceTR(v), renderServiceSetTailTR(v)
	for _, w := range []string{"açık problemler = OKUNAMADI", "aktif anomaliler = OKUNAMADI", "Açık problemler OKUNAMADI", "Aktif anomaliler OKUNAMADI"} {
		if !strings.Contains(ev, w) {
			t.Errorf("kanıt %q taşımalı:\n%s", w, ev)
		}
	}
	if strings.Contains(ev, "Açık problem yok") || !strings.Contains(tail, "| harvester-a | 20.00 istek/sn | %2.50 | 840 ms | metrik | — | — |") ||
		!strings.Contains(tail, "açık problemler okunamadı (okunamadı ≠ yok)") {
		t.Errorf("okunamayan bölüm sıfır / yok diye yazılmamalı:\n%s\n%s", ev, tail)
	}
	// 3) Metrik okunamadı + span okundu → kullanılabilir, not düşülür.
	d = setFixtureData()
	d.MetricStatus, d.Metric = setSecUnread, nil
	v = assembleServiceSet(serviceSetInput{Label: "harvester-*", Names: setFixtureNames, Total: 5, RangeS: 1800, Data: d})
	if !v.usable() || !strings.Contains(renderServiceSetTailTR(v), "metrik RED okunamadı — değerler örneklenmiş span'den") {
		t.Errorf("metrik okunamadı: %+v", v)
	}
	// 4) İki RED kaynağı da yok → demet düşer (değer uydurulmaz).
	v = assembleServiceSet(serviceSetInput{Label: "harvester-*", Names: setFixtureNames, Total: 5, RangeS: 1800,
		Data: serviceSetData{MetricStatus: setSecUnread}})
	if v.usable() || v.SilentN != 5 {
		t.Errorf("RED yok: usable=%v %+v", v.usable(), v)
	}
	// 5) Hiç veri yok ama okumalar başarılı → dürüst boş.
	v = assembleServiceSet(serviceSetInput{Label: "harvester-*", Names: setFixtureNames, Total: 5, RangeS: 1800,
		Data: serviceSetData{MetricStatus: setSecNone, SpanOK: true, Summary: teamSummary{ProblemsOK: true}, AnomalyOK: true}})
	ev = renderServiceSetEvidenceTR(v)
	if !v.usable() || v.MetricStatus != setSecNone || v.SpanStatus != setSecNone || v.ProblemStatus != setSecNone || v.AnomalyStatus != setSecNone ||
		!strings.Contains(ev, "Açık problem yok (okundu)") || !strings.Contains(ev, "- harvester-a: bu pencerede metrik ve span verisi yok") {
		t.Errorf("boş ama okundu: %+v\n%s", v, ev)
	}
}

func TestAssembleServiceSetFocusAndCap(t *testing.T) {
	in := serviceSetInput{Label: "harvester-*", Names: setFixtureNames, Total: 5, RangeS: 1800, Data: setFixtureData()}
	in.Focus = setFocusSlow
	if got := rowNames(assembleServiceSet(in)); !slices.Equal(got[:3], []string{"harvester-c", "harvester-a", "harvester-b"}) {
		t.Errorf("en yavaş: p99 azalan, p99'u olmayan sona: %v", got)
	}
	in.Focus = setFocusErrors
	v := assembleServiceSet(in)
	if got := rowNames(v); !slices.Equal(got, []string{"harvester-a", "harvester-c"}) || v.All != 5 {
		t.Errorf("hata verenler: yalnız sorunlular: %v", got)
	}
	in.Focus = setFocusTraffic
	if got := rowNames(assembleServiceSet(in)); !slices.Equal(got[:3], []string{"harvester-a", "harvester-b", "harvester-e"}) {
		t.Errorf("en yoğun: istek/sn azalan (metrik önce): %v", got)
	}
	in.Focus = setFocusProblems
	if got := rowNames(assembleServiceSet(in)); got[0] != "harvester-a" {
		t.Errorf("açık problemi olan önce: %v", got)
	}
	// "hata verenler" boşsa bunu söyler.
	clean := serviceSetInput{Label: "harvester-*", Names: []string{"harvester-b", "harvester-d"}, Total: 2, RangeS: 1800, Focus: setFocusErrors, Data: setFixtureData()}
	cv := assembleServiceSet(clean)
	if len(cv.Rows) != 0 || !strings.Contains(renderServiceSetEvidenceTR(cv), "hata veren ya da açık problemi olan servis yok") ||
		!strings.Contains(renderServiceSetTailTR(cv), "Bu kümede hata veren ya da açık problemi olan servis yok") {
		t.Errorf("boş hata odağı: %+v", cv)
	}
	// Tavan: kaç servisin dışarıda kaldığı ve sıranın kaynağı söylenir.
	capIn := serviceSetInput{Label: "harvester-*", Names: setFixtureNames, Total: 132, RankedBy: "metric", RangeS: 1800, Data: setFixtureData()}
	capV := assembleServiceSet(capIn)
	ev, tail := renderServiceSetEvidenceTR(capV), renderServiceSetTailTR(capV)
	if capV.LeftOut != 127 || !strings.Contains(ev, "132 servis eşleşti") || !strings.Contains(ev, "ilk 5 servis incelendi, 127 servis DIŞARIDA") ||
		!strings.Contains(ev, "örneklenmemiş metrik") || !strings.Contains(tail, "Kalıba 132 servis uyuyor") || !strings.Contains(tail, "127 servis dışarıda kaldı") ||
		!strings.Contains(tail, "daraltırsan") {
		t.Errorf("tavan notu:\n%s\n%s", ev, tail)
	}
	// Uzun küme: tabloda en çok serviceSetListMax satır + kalan sayı.
	var names []string
	for i := range serviceSetListMax + 7 {
		names = append(names, fmt.Sprintf("harvester-%02d", i))
	}
	long := assembleServiceSet(serviceSetInput{Label: "harvester-*", Names: names, Total: len(names), RangeS: 1800,
		Data: serviceSetData{MetricStatus: setSecNone, SpanOK: true, Summary: teamSummary{ProblemsOK: true}, AnomalyOK: true}})
	if tail := renderServiceSetTailTR(long); strings.Count(tail, "| harvester-") != serviceSetListMax || !strings.Contains(tail, "+7 servis daha") {
		t.Errorf("satır tavanı:\n%s", tail)
	}
}

func TestServiceSetLinksAndSuggestions(t *testing.T) {
	route := guidedRoute{Intent: guidedServiceSet, SetLabel: "harvester-*", TeamServices: []string{"harvester-a", "harvester-b"}}
	links := guidedAnswerLinks(route, noLinkWindow())
	if len(links) != 2 || links[0].Href != "/services?q=harvester-" || links[1].Href != "/service?name=harvester-a" {
		t.Errorf("linkler: %+v", links)
	}
	if got := serviceSetLinks(guidedRoute{SetLabel: "pay*-api"}); got[0].Href != "/services?q=pay" {
		t.Errorf("glob linki ilk parçayla süzer: %+v", got)
	}
	sugg := guidedSuggestions(route)
	if !slices.Equal(sugg, []string{"harvester-a sağlığı nasıl?", "harvester-b sağlığı nasıl?"}) {
		t.Errorf("öneriler: %v", sugg)
	}
	for _, q := range sugg { // v0.9.411 kuralı: her öneri guided router'da yönlenir
		if r := routeGuidedIntent(q, []string{"harvester-a", "harvester-b"}, nil, nil, ""); r.Intent == guidedNone {
			t.Errorf("öneri %q yönlenemiyor", q)
		}
	}
	chips := teamServicePickChips(route.TeamServices, "")
	if got := guidedFollowups(guidedRoute{Intent: guidedServiceSet, TeamFollowups: chips}, nil); len(got) != len(chips) || len(got) == 0 {
		t.Errorf("servis seçici çipleri küme bakışında da çıkmalı: %v", got)
	}
	if answerClassOfIntent(guidedServiceSet) != classStatus {
		t.Error("küme bakışı durum sınıfı")
	}
}

// Kademe sırası kaynak-pinli (chat_tier_order_test.go emsali): servis adı
// kalıbı kaynak sorusundan, keşiften, endpoint kartından ve guided'dan ÖNCE.
func TestServicePatternTierRunsBeforeAskTiers(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	at := strings.Index(src, "s.chatServicePatternTier(ctx, emit,")
	if at < 0 {
		t.Fatal("servis adı kalıbı kademesi copilot_chat.go'da yok")
	}
	if clar := strings.Index(src, "s.chatClarifyReplyTier(ctx, emit,"); clar < 0 || clar > at {
		t.Error("kademe netleştirme cevabından SONRA koşmalı (bekleyen soru cevabı kalıp sanılmasın)")
	}
	for _, later := range []string{"s.chatDocOrEntityAskTier(ctx, emit,", "s.chatSourceDiscoveryTier(ctx, emit,", "s.endpointPickPending(ctx,",
		"s.chatEndpointPickTier(ctx, emit,", "s.copilotChatGuided(ctx, emit,", "s.chatSourceAskTier(ctx, emit,"} {
		if i := strings.Index(src, later); i < 0 || i < at {
			t.Errorf("%q kalıp kademesinden SONRA gelmeli (konum %d, kademe %d)", later, i, at)
		}
	}
	if !strings.Contains(src, "activeServicePatternScope(us, cst,") {
		t.Error("v2 döngüsü etkin kümeyi kullanıcı kapsamı olarak almıyor")
	}
}

// Depo ve VM yokken demet değer uydurmaz: iki RED kaynağı da okunamadı →
// hata (çağıran v2'de küme kapsamıyla serbest döngüye bırakır), panik yok,
// çözüm adımı yine görünür.
func TestServiceSetBundleWithoutSources(t *testing.T) {
	s := &Server{}
	var steps []string
	emit := func(kind string, v any) {
		if m, ok := v.(map[string]any); ok && kind == "step" {
			if tool, _ := m["tool"].(string); tool != "" {
				steps = append(steps, tool)
			}
		}
	}
	route := guidedRoute{Intent: guidedServiceSet, Family: []string{"harvester-a", "harvester-b"}, SetLabel: "harvester-*", SetTotal: 2}
	now := time.Unix(1_800_000_000, 0)
	ev, src, err := s.guidedServiceSetBundle(context.Background(), emit, &route, now.Add(-30*time.Minute), now, 1800)
	if err == nil || ev != "" || src != "" || route.AnswerTail != "" {
		t.Fatalf("kaynak yokken demet düşmeli: ev=%q src=%q err=%v", ev, src, err)
	}
	if _, _, err := s.guidedServiceSetBundle(context.Background(), emit, &guidedRoute{Intent: guidedServiceSet}, now, now, 1800); err == nil {
		t.Error("boş küme hata olmalı")
	}
}

// Önbellek anahtarı TÜM girdileri özetler (CLAUDE.md: küme boyu değil, adlar).
func TestServiceSetREDKeyHashesAllInputs(t *testing.T) {
	from, to := time.Unix(1_800_000_000, 0), time.Unix(1_800_001_800, 0)
	base := serviceSetREDKey([]string{"harvester-a", "harvester-b"}, "", from, to, "service_name", "env", "cluster")
	for name, other := range map[string]string{
		"aynı boyda başka küme": serviceSetREDKey([]string{"harvester-a", "harvester-c"}, "", from, to, "service_name", "env", "cluster"),
		"ad sınırı":             serviceSetREDKey([]string{"harvester-a,harvester-b"}, "", from, to, "service_name", "env", "cluster"),
		"ortam":                 serviceSetREDKey([]string{"harvester-a", "harvester-b"}, "prod", from, to, "service_name", "env", "cluster"),
		"pencere":               serviceSetREDKey([]string{"harvester-a", "harvester-b"}, "", from.Add(-6*time.Hour), to, "service_name", "env", "cluster"),
		"etiket haritası":       serviceSetREDKey([]string{"harvester-a", "harvester-b"}, "", from, to, "service", "env", "cluster"),
	} {
		if other == base {
			t.Errorf("%s anahtarı değiştirmeli", name)
		}
	}
	if !strings.HasPrefix(base, "copilot:svcset:red:v1:") {
		t.Errorf("anahtar öneki: %s", base)
	}
}
