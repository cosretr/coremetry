package chstore

import (
	"os"
	"strings"
	"testing"
	"time"
)

// v0.10.1005 — Operator-reported (prod): /traces'te `function_code = …` çipiyle
// gelen satırların Name kolonu kök span yerine bir MQ "publish" span'inin adını
// gösteriyordu (süre 21 ms / 4 span; trace detayında 25 ms / 12 span). Çipler
// WHERE'de span düzeyinde olduğu için satır yalnız eşleşen span'lerden
// kuruluyordu. Kural: çip trace'i SEÇER, satırı şekillendirmez — sayfa çipsiz
// WHERE ile yeniden kurulur (trace_row_repair.go).

func TestSpanScopedChips(t *testing.T) {
	chip := []FilterExpr{{Key: "function_code", Op: "=", Values: []string{"SPE0017"}}}
	group := &FilterGroup{Join: "OR", Filters: chip}
	cases := []struct {
		name string
		f    TraceFilter
		want bool
	}{
		{"çip yok", TraceFilter{Service: "svc"}, false},
		{"düz çip, arama yok → WHERE'de span düzeyi", TraceFilter{Filters: chip}, true},
		{"gruplu çip, arama yok", TraceFilter{FilterRoot: group}, true},
		{"boş grup çip sayılmaz", TraceFilter{FilterRoot: &FilterGroup{Join: "AND"}}, false},
		{"arama varken çipler HAVING'de (satır zaten tüm span'lerden)", TraceFilter{Filters: chip, Search: "POST /login"}, false},
		{"span-düzeyi prob eski şekli ister", TraceFilter{Filters: chip, forceFiltersInWhere: true}, false},
		{"yalnız Errors: çip değil (v0.10.258 ayrı yol)", TraceFilter{HasError: true}, false},
	}
	for _, c := range cases {
		if got := spanScopedChips(c.f); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestWithoutChips(t *testing.T) {
	f := TraceFilter{Service: "svc", Env: "prod", HasError: true, MinMs: 5,
		Filters:    []FilterExpr{{Key: "function_code", Op: "=", Values: []string{"X"}}},
		FilterRoot: &FilterGroup{Join: "OR"}}
	got := withoutChips(f)
	if got.Filters != nil || got.FilterRoot != nil || got.HasError {
		t.Fatalf("çipler ve hata WHERE'i düşmeli: %+v", got)
	}
	if got.Service != "svc" || got.Env != "prod" || got.MinMs != 5 {
		t.Fatalf("diğer yüklemler aynen kalmalı: %+v", got)
	}
	if len(f.Filters) != 1 || !f.HasError {
		t.Fatal("girdi değişmemeli (değer kopyası)")
	}
}

func TestTraceRowRepairBounds(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 11, 54, 36, 0, time.UTC)
	rows := []TraceRow{
		{TraceID: "a", StartTime: t0.UnixNano(), DurationMs: 21.2},
		{TraceID: "b", StartTime: t0.Add(-8 * time.Second).UnixNano(), DurationMs: 345},
	}
	from, to := traceRowRepairBounds(rows)
	// Kök, eşleşen ilk span'den ÖNCE başlar → alt sınır geri açılır.
	if want := t0.Add(-8*time.Second - traceRowRepairLead); !from.Equal(want) {
		t.Errorf("alt sınır: %s, istenen %s", from, want)
	}
	if want := t0.Add(21200 * time.Microsecond).Add(traceExtrasToSlack); !to.Equal(want) {
		t.Errorf("üst sınır: %s, istenen %s", to, want)
	}
}

func TestMergeRepairedRows(t *testing.T) {
	page := []TraceRow{
		{TraceID: "a", RootName: "log.service.masterlog publish", ServiceName: "chatbot-svc", StartTime: 104, DurationMs: 21.2, SpanCount: 4, Extras: map[string]string{"function_code": "SPE0017"}},
		{TraceID: "b", RootName: "log.service.servicelog publish", ServiceName: "chatbot-svc", StartTime: 90, DurationMs: 345, SpanCount: 36},
		{TraceID: "c", RootName: "orphan publish", ServiceName: "chatbot-svc", StartTime: 80, DurationMs: 5, SpanCount: 2},
	}
	repaired := []TraceRow{ // sıra farklı; "c" yok (CH'de başka span'i bulunamadı)
		{TraceID: "b", RootName: "POST /gateway/execute", ServiceName: "gateway-svc", RootRoute: "/gateway/execute", StartTime: 85, DurationMs: 350, SpanCount: 40, HasError: true, ErrorSpans: 2},
		{TraceID: "a", RootName: "/BSAWEB/GatewayService/execute", ServiceName: "chatbot-svc", StartTime: 100, DurationMs: 25.11, SpanCount: 12},
	}
	mergeRepairedRows(page, repaired)
	if page[0].TraceID != "a" || page[1].TraceID != "b" || page[2].TraceID != "c" {
		t.Fatalf("sıra korunmalı: %+v", page)
	}
	if a := page[0]; a.RootName != "/BSAWEB/GatewayService/execute" || a.SpanCount != 12 || a.DurationMs != 25.11 || a.StartTime != 100 || a.Extras["function_code"] != "SPE0017" {
		t.Errorf("a: kök adı + tüm trace ölçüleri gelmeli, extras korunmalı: %+v", a)
	}
	if b := page[1]; b.RootName != "POST /gateway/execute" || b.ServiceName != "gateway-svc" || b.RootRoute != "/gateway/execute" || !b.HasError || b.ErrorSpans != 2 {
		t.Errorf("b: %+v", b)
	}
	if c := page[2]; c.RootName != "orphan publish" || c.SpanCount != 2 {
		t.Errorf("onarım satırı olmayan trace olduğu gibi kalmalı: %+v", c)
	}
}

// Kablolama: onarım, extras'tan ÖNCE ve yumuşak; id listesi PREWHERE'de.
func TestRowRepairWiring(t *testing.T) {
	repo, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(repo)
	i, j := strings.Index(src, "s.repairSpanScopedRows(ctx, out, f)"), strings.Index(src, "if err := s.fillTraceExtras(ctx, out, f.ExtraAttrs)")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("onarım GetTraces'te extras'tan önce çağrılmalı (i=%d j=%d)", i, j)
	}
	rep, err := os.ReadFile("trace_row_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"len(rows) > traceRowRepairMaxIDs",
		`buildGetTracesListSQLWith(stage2PrewhereSQL(len(rows))+lwc.sql(), "", "trace_start", "DESC", stage2Settings)`,
		"lf := withoutChips(f)",
	} {
		if !strings.Contains(string(rep), w) {
			t.Errorf("trace_row_repair.go %q taşımalı", w)
		}
	}
	// Çipsiz WHERE: çip yüklemi SQL'e girmez, zaman sınırı ve servis girer.
	f := withoutChips(TraceFilter{Service: "chatbot-svc", From: time.Unix(100, 0), To: time.Unix(200, 0),
		Filters: []FilterExpr{{Key: "function_code", Op: "=", Values: []string{"SPE0017"}}}})
	wc := buildGetTracesWhere(f, "")
	sql := wc.sql()
	if strings.Contains(sql, "function_code") || !strings.Contains(sql, "service_name = ?") || !strings.Contains(sql, "time") {
		t.Errorf("onarım WHERE'i: %s", sql)
	}
}

// v0.10.1008 — Operator-reported (prod): `function_code = …` çipi + "Root" →
// boş liste. Kök-varlığı WHERE'in daralttığı span kümesinde aranıyordu
// (countIf(kök)); fonksiyon kodunu kök span taşımadığı için her trace
// düşüyordu. v0.10.107'nin servis daraltması için koyduğu kural çiplere
// genellendi: daraltılmış şekilde kök, aday id'ler üstünde MV'den sorulur.
func TestRootScopeNarrowed(t *testing.T) {
	chip := []FilterExpr{{Key: "function_code", Op: "=", Values: []string{"SPE0017"}}}
	cases := []struct {
		name string
		f    TraceFilter
		want bool
	}{
		{"daraltma yok", TraceFilter{RootOnly: true}, false},
		{"servis", TraceFilter{Service: "svc"}, true},
		{"RequireServices", TraceFilter{RequireServices: []string{"a", "b"}}, true},
		{"span-düzeyi çip", TraceFilter{Filters: chip}, true},
		{"gruplu çip", TraceFilter{FilterRoot: &FilterGroup{Join: "OR", Filters: chip}}, true},
		{"arama + çip: çip HAVING'de, WHERE daralmıyor", TraceFilter{Filters: chip, Search: "x"}, false},
	}
	for _, c := range cases {
		if got := rootScopeNarrowed(c.f); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestRootCheckUsesNarrowedRule(t *testing.T) {
	repo, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(repo)
	for _, w := range []string{
		"rootPostFilter := f.RootOnly && rootScopeNarrowed(f)",
		"if rootScopeNarrowed(f) { // v0.10.1008",
	} {
		if !strings.Contains(src, w) {
			t.Errorf("repo.go %q taşımalı (kök kontrolü daraltılmamış kaynaktan)", w)
		}
	}
	if strings.Contains(src, `rootPostFilter := f.RootOnly && (f.Service != ""`) {
		t.Error("eski servis-yalnız kural geri gelmiş")
	}
	// Çipli + Root: WHERE'de çip var, HAVING'de ham countIf(kök) OLMAMALI.
	f := TraceFilter{RootOnly: true, From: time.Unix(100, 0), To: time.Unix(4000, 0),
		Filters: []FilterExpr{{Key: "function_code", Op: "=", Values: []string{"SPE0017"}}}}
	if !rootScopeNarrowed(f) || !spanScopedChips(f) {
		t.Fatal("çipli süzgeç daraltılmış sayılmalı")
	}
}
