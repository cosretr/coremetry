package chstore

import (
	"os"
	"strings"
	"testing"
	"time"
)

// v0.10.1010 — Operator-reported (prod): `function_code = …` çipiyle liste dolu
// ve satırların çoğu ERROR; "Errors" işaretlenince "Trace bulunamadı". Errors
// HAVING'i çipin daralttığı span'lerde hata arıyordu; fonksiyon kodunu taşıyan
// log-yayın span'leri hata vermiyor, hata aynı trace'in başka span'inde.
// Çipe uyan hatalı span yoksa hata TRACE düzeyinde aranır. Hata şekli ve
// kesişim sorgusu yerel ClickHouse'ta yeniden üretildi (eski sayım 0, kesişim
// doğru iki trace; hatasız ve çipsiz trace'ler dışarıda).

func chipFilter() []FilterExpr {
	return []FilterExpr{{Key: "function_code", Op: "=", Values: []string{"CAL1051"}}}
}

func TestTraceLevelErrorEligible(t *testing.T) {
	cases := []struct {
		name string
		f    TraceFilter
		want bool
	}{
		{"Errors + span-düzeyi çip", TraceFilter{HasError: true, Filters: chipFilter()}, true},
		{"servisli de (çip yine WHERE'de)", TraceFilter{HasError: true, Filters: chipFilter(), Service: "svc"}, true},
		{"Errors yok", TraceFilter{Filters: chipFilter()}, false},
		{"çip yok: Errors span-yerel, WHERE'de (idx_status)", TraceFilter{HasError: true}, false},
		{"arama + çip: çipler HAVING'de, hata zaten trace düzeyi", TraceFilter{HasError: true, Filters: chipFilter(), Search: "x"}, false},
		{"aday listesi varken ikinci kez koşmaz", TraceFilter{HasError: true, Filters: chipFilter(), CandidateIDs: []string{"a"}}, false},
		{"trace id araması", TraceFilter{HasError: true, Filters: chipFilter(), TraceID: "abc"}, false},
	}
	for _, c := range cases {
		if got := traceLevelErrorEligible(c.f); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestTraceErrScopeAndWheres(t *testing.T) {
	f := TraceFilter{HasError: true, Filters: chipFilter(), Service: "callcenter-svc", Env: "prod", Search: "",
		RootOnly: true, RequireServices: nil, MinMs: 5, MaxMs: 900,
		From: time.Unix(1000, 0), To: time.Unix(5000, 0)}
	sc := traceErrScope(f)
	if sc.Filters != nil || sc.HasError || sc.RootOnly || sc.MinMs != 0 || sc.MaxMs != 0 || sc.Service != "callcenter-svc" || sc.Env != "prod" {
		t.Fatalf("kapsam: %+v", sc)
	}
	chip, errw, both := traceErrWheres(f, "")
	cs, es, bs := chip.sql(), errw.sql(), both.sql()
	// Çip anahtarı ve değeri bind-arg'dır (SQL metninde değil) — yüklemin
	// varlığı argümanlardan okunur.
	hasChip := func(wc whereClause) bool {
		k, v := false, false
		for _, a := range wc.args {
			if a == "function_code" {
				k = true
			}
			if a == "CAL1051" {
				v = true
			}
		}
		return k && v
	}
	// Çip tarafı: çip var, hata yok. Hata tarafı: hata var, çip yok. İkisi: ikisi de.
	if !hasChip(chip) || strings.Contains(cs, "status_code = 'error'") {
		t.Errorf("çip tarafı: %s %v", cs, chip.args)
	}
	if hasChip(errw) || !strings.HasSuffix(es, "status_code = 'error'") {
		t.Errorf("hata tarafı: %s %v", es, errw.args)
	}
	if !hasChip(both) || !strings.HasSuffix(bs, "status_code = 'error'") {
		t.Errorf("aynı-span probu: %s %v", bs, both.args)
	}
	// Üçü de aynı kapsamı taşır: pencere + servis + ortam; süre / kök taşımaz.
	for name, sql := range map[string]string{"çip": cs, "hata": es, "ikisi": bs} {
		if !strings.Contains(sql, "service_name = ?") || !strings.Contains(sql, "deploy_env = ?") || !strings.Contains(sql, "time") {
			t.Errorf("%s: kapsam eksik: %s", name, sql)
		}
		if strings.Contains(sql, "duration >=") || strings.Contains(sql, "duration <=") {
			t.Errorf("%s: süre yüklemi kapsamda olmamalı: %s", name, sql)
		}
	}
}

func TestTraceErrJoinSides(t *testing.T) {
	var chip, errw whereClause
	chip.add("attr_function_code = ?", "CAL1051")
	errw.add("status_code = 'error'")
	cases := []struct {
		name          string
		nChip, nErr   uint64
		wantInnerChip bool
		wantCapped    bool
	}{
		{"hatalar nadir → hata tarafı küme", 1_700_000, 4_000, false, false},
		{"çip seçici → çip tarafı küme", 900, 250_000, true, false},
		{"eşitlikte hata tarafı küme", 500, 500, false, false},
		{"küçük taraf da tavanı aşıyor → tavanlı", traceErrJoinSetMax + 1, traceErrJoinSetMax + 1, false, true},
		{"çip küçük ama tavan üstü", traceErrJoinSetMax + 1, traceErrJoinSetMax + 5, true, true},
	}
	for _, c := range cases {
		outer, inner, capped := traceErrJoinSides(chip, errw, c.nChip, c.nErr)
		innerChip := strings.Contains(inner.sql(), "attr_function_code")
		outerChip := strings.Contains(outer.sql(), "attr_function_code")
		if innerChip != c.wantInnerChip || outerChip == innerChip || capped != c.wantCapped {
			t.Errorf("%s: iç=çip? %v, dış=çip? %v, tavanlı %v", c.name, innerChip, outerChip, capped)
		}
	}
}

func TestTraceErrSQLBounds(t *testing.T) {
	join := traceErrJoinSQL("WHERE time >= ? AND a = ?", "WHERE time >= ? AND status_code = 'error'")
	for _, w := range []string{
		"trace_id GLOBAL IN (", "ORDER BY time DESC", "LIMIT ?)", "SETTINGS max_execution_time = 15", "distributed_product_mode = 'global'",
	} {
		if !strings.Contains(join, w) {
			t.Errorf("kesişim SQL'i %q içermeli:\n%s", w, join)
		}
	}
	if strings.Count(join, "LIMIT ?") != 2 || strings.Contains(join, "GROUP BY") {
		t.Errorf("iki tavan (küme + bütçe), GROUP BY yok:\n%s", join)
	}
	cnt := traceErrCountSQL("WHERE time >= ?")
	if !strings.Contains(cnt, "LIMIT ?") || !strings.Contains(cnt, "max_execution_time = 5") {
		t.Errorf("tavanlı sayım: %s", cnt)
	}
}

// Kablolama: kanca hata-önce bloğundan ÖNCE; trace kipinde adaylar yazılır ve
// HasError kapanır (liste aşamaları çipli span'lerde hata aramaz).
func TestTraceLevelErrorWiring(t *testing.T) {
	b, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i, j := strings.Index(src, "if traceLevelErrorEligible(f) {"), strings.Index(src, "if errorFirstEligible(f) {")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("trace-düzeyi hata kancası hata-önce bloğundan önce olmalı (i=%d j=%d)", i, j)
	}
	block := src[i:j]
	for _, w := range []string{
		"if mode == traceErrModeTrace {",
		"f.CandidateIDs, f.HasError = ids, false",
		"return []TraceRow{}, 0, false, nil",
		"*f.RankedWithin = len(ids)",
	} {
		if !strings.Contains(block, w) {
			t.Errorf("kanca %q taşımalı", w)
		}
	}
	// Adaylar yazıldıktan sonra süzgeç artık hata-önce / ikinci tura uygun değil.
	f := TraceFilter{HasError: false, Filters: chipFilter(), CandidateIDs: []string{"a"}, Service: "svc"}
	if errorFirstEligible(f) || traceLevelErrorEligible(f) {
		t.Error("adaylı, hatası kapatılmış süzgeç yeniden aday üretmemeli")
	}
}
