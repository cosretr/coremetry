package logstore

// signature_enum_test.go — v0.10.1504 (operatör): JSON gövdede kısa
// kategorik alanlar (status OK/FAILED, level, bool) desenleri yeniden
// ayırır; kimlik ve yüksek kardinaliteli alanlar `<x>` kalır. Kimlikler
// sentetik.

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestSigCategorical(t *testing.T) {
	cases := []struct {
		raw  string
		want string // "" = kategorik değil
	}{
		{`"OK"`, `"OK"`},
		{`"FAILED"`, `"FAILED"`},
		{`"not found"`, `"not found"`},
		{`true`, `true`},
		{`false`, `false`},
		{`"three word value"`, ""},
		{`"a very long categorical value!"`, ""}, // > 24 rune
		{`"QXC1015060201WKue"`, ""},              // kimlik
		{`"ord-123456"`, ""},                     // kimlik parçası
		{`"12.5"`, ""},                           // sayısal dizge
		{`42`, ""},                               // sayı
		{`null`, ""},
		{`{"a":1}`, ""},
		{`["x"]`, ""},
		{`""`, ""},
		{`" OK"`, ""},        // kenar boşluğu
		{`"10.0.0.1"`, ""},   // IP
		{`"2026-10-24"`, ""}, // tarih
		{`"a<b"`, ""},        // kaçış gerektiren
		{`"başarısız"`, `"başarısız"`},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			got, ok := sigCategorical(json.RawMessage(c.raw))
			if (c.want != "") != ok || got != c.want {
				t.Fatalf("sigCategorical(%s) = %q,%v; want %q", c.raw, got, ok, c.want)
			}
		})
	}
}

// enumRows — her i için body(i), ts artan.
func enumRows(n int, body func(i int) string) []*LogRecord {
	rows := make([]*LogRecord, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, &LogRecord{Timestamp: int64(1000 + i), ServiceName: "orders-api", Body: body(i)})
	}
	return rows
}

func groupTemplates(res *PatternsResult) []string {
	out := make([]string, 0, len(res.Groups))
	for _, g := range res.Groups {
		out = append(out, g.Template)
	}
	slices.Sort(out)
	return out
}

func TestGroupBySignature_CategoricalSplit(t *testing.T) {
	statuses := []string{"OK", "FAILED"}
	cases := []struct {
		name      string
		rows      []*LogRecord
		wantTpls  []string
		wantSplit []string
	}{
		{"status OK vs FAILED → 2 patterns, request_id 256 distinct stays <x>",
			enumRows(256, func(i int) string {
				return fmt.Sprintf(`{"request_id":"QXC%013dWKue","msg":"inquiry completed in %dms","status":%q}`, 1015060201+i*7919, 10+i, statuses[i%2])
			}),
			[]string{
				`{"msg":"inquiry completed in <x>ms","request_id":<x>,"status":"FAILED"}`,
				`{"msg":"inquiry completed in <x>ms","request_id":<x>,"status":"OK"}`,
			},
			[]string{"status"}},
		{"short non-ID user with 256 distinct values stays <x>",
			enumRows(256, func(i int) string {
				return fmt.Sprintf(`{"msg":"login","user":"user%c%c"}`, 'a'+rune(i%26), 'a'+rune(i/26))
			}),
			[]string{`{"msg":"login","user":<x>}`}, nil},
		{"key with 12 distinct values stays <x>",
			enumRows(120, func(i int) string {
				return fmt.Sprintf(`{"msg":"step done","stage":"stage%c"}`, 'a'+rune(i%12))
			}),
			[]string{`{"msg":"step done","stage":<x>}`}, nil},
		{"key with 9 distinct values splits",
			enumRows(90, func(i int) string {
				return fmt.Sprintf(`{"msg":"step done","stage":"stage%c"}`, 'a'+rune(i%9))
			}),
			nil, []string{"stage"}},
		{"key seen in only one line stays <x>",
			append(enumRows(5, func(int) string { return `{"msg":"tick"}` }),
				&LogRecord{Timestamp: 1, Body: `{"msg":"tick","outcome":"DENIED"}`}),
			[]string{`{"msg":"tick","outcome":<x>}`, `{"msg":"tick"}`}, nil},
		{"bool values split",
			enumRows(40, func(i int) string {
				return fmt.Sprintf(`{"msg":"cache lookup","hit":%v}`, i%4 == 0)
			}),
			[]string{`{"hit":false,"msg":"cache lookup"}`, `{"hit":true,"msg":"cache lookup"}`}, []string{"hit"}},
		{"level field splits",
			enumRows(30, func(i int) string {
				return fmt.Sprintf(`{"level":%q,"msg":"payment processed","trace":"%032x"}`, []string{"INFO", "ERROR", "WARN"}[i%3], i+1)
			}),
			[]string{
				`{"level":"ERROR","msg":"payment processed","trace":<x>}`,
				`{"level":"INFO","msg":"payment processed","trace":<x>}`,
				`{"level":"WARN","msg":"payment processed","trace":<x>}`,
			},
			[]string{"level"}},
		{"plain text unchanged",
			enumRows(4, func(i int) string { return fmt.Sprintf("connection refused after %dms", 100+i) }),
			[]string{"connection refused after <x>ms"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := GroupBySignature(t.Context(), &pagedStore{rows: c.rows}, Filter{}, 500)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(res.SplitKeys, c.wantSplit) {
				t.Fatalf("splitKeys = %v, want %v", res.SplitKeys, c.wantSplit)
			}
			if c.wantTpls != nil {
				if got := groupTemplates(res); !slices.Equal(got, c.wantTpls) {
					t.Fatalf("templates\n got  %q\n want %q", got, c.wantTpls)
				}
			}
			var total int64
			for _, g := range res.Groups {
				total += g.Count
				if g.Hash != fmt.Sprintf("%016x", SignatureHash(g.Template)) {
					t.Fatalf("hash does not match template %q", g.Template)
				}
			}
			if total != int64(len(c.rows)) || res.Distinct != len(res.Groups) {
				t.Fatalf("counts: total=%d rows=%d distinct=%d groups=%d", total, len(c.rows), res.Distinct, len(res.Groups))
			}
		})
	}
}

// Ayrılan desenler: sayım, örnek, sorgu ve servis doğru dağılır; "Ara"
// mesaj metniyle kalır (alan:değer gövde JSON anahtarlarında iki backend'de
// de güvenilir değil).
func TestGroupBySignature_CategoricalSplitGroupFields(t *testing.T) {
	rows := enumRows(10, func(i int) string {
		st := "OK"
		if i >= 7 {
			st = "FAILED"
		}
		return fmt.Sprintf(`{"msg":"transfer finished for K7QX%07d","status":%q}`, i, st)
	})
	res, err := GroupBySignature(t.Context(), &pagedStore{rows: rows}, Filter{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("groups = %+v", res.Groups)
	}
	ok, failed := res.Groups[0], res.Groups[1]
	if ok.Count != 7 || failed.Count != 3 {
		t.Fatalf("counts ok=%d failed=%d", ok.Count, failed.Count)
	}
	if ok.Sample != rows[0].Body || failed.Sample != rows[7].Body {
		t.Fatalf("samples: %q / %q", ok.Sample, failed.Sample)
	}
	if ok.FirstSeen != 1000 || ok.LastSeen != 1006 || failed.FirstSeen != 1007 || failed.LastSeen != 1009 {
		t.Fatalf("seen ranges: %+v / %+v", ok, failed)
	}
	if ok.Query != `"transfer finished for"` || failed.Query != ok.Query {
		t.Fatalf("queries %q / %q", ok.Query, failed.Query)
	}
	if !slices.Equal(failed.Services, []string{"orders-api"}) || failed.ServiceCount != 1 {
		t.Fatalf("services %v", failed.Services)
	}
}

// Taban örneklemesi kategorik anahtarı başka karara bağlarsa (burada taban
// tek satırda gördü → `<x>`) birleşim yine aynı imza uzayında olur.
func TestAlignPatternBaseline_SplitKeysDiffer(t *testing.T) {
	body := func(st string) string { return fmt.Sprintf(`{"msg":"transfer finished","status":%q}`, st) }
	cur, err := GroupBySignature(t.Context(), &pagedStore{rows: enumRows(6, func(i int) string {
		return body([]string{"OK", "FAILED"}[i%2])
	})}, Filter{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	base, err := GroupBySignatureN(t.Context(), &pagedStore{rows: []*LogRecord{
		{Timestamp: 1, Body: body("OK")}, {Timestamp: 2, Body: `{"msg":"transfer finished"}`},
	}}, Filter{}, PatternsMaxGroups, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.SplitKeys) != 0 || !slices.Equal(cur.SplitKeys, []string{"status"}) {
		t.Fatalf("precondition: cur=%v base=%v", cur.SplitKeys, base.SplitKeys)
	}
	aligned := AlignPatternBaseline(cur, base)
	if aligned == base || !slices.Equal(aligned.SplitKeys, cur.SplitKeys) || len(base.SplitKeys) != 0 {
		t.Fatalf("aligned must be a new result with cur keys; base untouched")
	}
	JoinPatternBaseline(cur, base)
	for _, g := range cur.Groups {
		switch g.Template {
		case `{"msg":"transfer finished","status":"OK"}`:
			if g.PrevCount != 1 || g.New {
				t.Fatalf("OK should match baseline: %+v", g)
			}
		case `{"msg":"transfer finished","status":"FAILED"}`:
			if g.PrevCount != 0 || !g.New {
				t.Fatalf("FAILED is new in baseline: %+v", g)
			}
		default:
			t.Fatalf("unexpected template %q", g.Template)
		}
	}
	if same := AlignPatternBaseline(cur, aligned); same != aligned {
		t.Fatal("equal SplitKeys must return base as-is")
	}
}

// Bütçe: 2000 satırlık örneklemin iki geçişli gruplaması (çözüm + ince
// gruplar + birleşim) desen ucunun 2 sn bütçesinin çok altında kalmalı.
func TestGroupBySignature_EnumPerfBudget(t *testing.T) {
	rows := enumRows(PatternsSampleCap, func(i int) string {
		return fmt.Sprintf(`{"request_id":"QXC%013dWKue%024d","msg":"inquiry completed for K7QX%07d","status":%q,"user":"u%c%c","f3":{"x":1},"ok":%v}`,
			i, int64(i)*31, i, []string{"OK", "FAILED", "TIMEOUT"}[i%3], 'a'+rune(i%26), 'a'+rune(i/26%26), i%2 == 0)
	})
	start := time.Now()
	res, err := GroupBySignature(t.Context(), &pagedStore{rows: rows}, Filter{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d.Milliseconds() > 500 {
		t.Fatalf("2000-row grouping took %v", d)
	}
	if !slices.Equal(res.SplitKeys, []string{"ok", "status"}) || res.Distinct != 6 {
		t.Fatalf("splitKeys=%v distinct=%d", res.SplitKeys, res.Distinct)
	}
}
