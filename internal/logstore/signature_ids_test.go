package logstore

// signature_ids_test.go — v0.10.1496 (operatör, prod ES): `{"request_id":
// "SPE…"}` gövdeli 256 satır 164 desene dağılıyordu. Kimlikler sentetik.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSignature_IDTokens(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"glued id + timestamp", "request SPE1015060201ZTod000000000000202610240340413867 00 accepted",
			"request <x> <x> accepted"},
		{"mixed alnum ≥10", "session K7QX2M9PLA4 opened", "session <x> opened"},
		{"ulid", "job 01HF3ZK8Q4N6V2W9X7Y5R3T1PB done", "job <x> done"},
		{"short hex id", "cache key 1a2b3c4d miss", "cache key <x> miss"},
		{"digit-dominant", "ref ab12345 rejected", "ref <x> rejected"},
		{"underscore digits", "order_123456 created", "order_<x> created"},
		{"prefixed compound collapses", "ord-8f3k2j1m9x-7q2w4e6r8t sent", "ord-<x> sent"},
		{"base64 no digits", "token dGhpcyBpcyBhIHRlc3Q= expired", "token <x>= expired"},
		{"quoted json value", `{"request_id":"SPE1015060201ZTod0000000000002026102403404138", "status":"OK"`,
			`{"request_id":"<x>", "status":"OK"`},
		{"natural language kept", "Payment authorization failed for customer account, retrying later",
			"Payment authorization failed for customer account, retrying later"},
		{"short tokens kept", "OP12 v2 sha256 log4j2 x86_64 utf8mb4 HTTP/1.1 deadbeef",
			"OP12 v2 sha256 log4j2 x86_64 utf8mb4 HTTP/1.1 deadbeef"},
		{"camelCase class kept", "java.lang.NullPointerException at OrderServiceImpl.handle",
			"java.lang.NullPointerException at OrderServiceImpl.handle"},
		{"turkish kept", "Müşteri işlemi başarısız: bağlantı zaman aşımı", "Müşteri işlemi başarısız: bağlantı zaman aşımı"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeSignature(c.in); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestNormalizeSignature_JSONBody(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"keys sorted, ids out, message masked",
			`{"request_id":"SPE1015060201ZTod000000000000202610240340413867","msg":"payment failed for K7QX2M9PLA4 after 1500ms","status":"FAILED"}`,
			`{"msg":"payment failed for <x> after <x>ms","request_id":<x>,"status":<x>}`},
		{"message key case-insensitive + priority", `{"Message":"boot ok","error":"x","b":1}`,
			`{"Message":"boot ok","b":<x>,"error":<x>}`},
		{"no message field → key set only", `{"request_id":"A1B2C3D4E5F6","amount":12.5}`,
			`{"amount":<x>,"request_id":<x>}`},
		{"non-string message skipped", `{"error":{"code":5},"event":"retry scheduled"}`,
			`{"error":<x>,"event":"retry scheduled"}`},
		{"truncated json falls back to masked text", `{"request_id":"SPE1015060201ZTod0000000000002026", "msg":"tim`,
			`{"request_id":"<x>", "msg":"tim`},
		// Dizi JSON nesnesi değil → düz metin yolu; v0.10.1829'dan beri
		// sayılar tek haneli de maskelenir ve virgüllü koşu tek yer tutucu.
		{"array is not an object", `[1,2,3]`, `[<x>]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeSignature(c.in); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
	// Seviye + arama dizesi aynı çözümden.
	info := signatureOf(`{"level":"DEBUG","msg":"payment failed for K7QX2M9PLA4","request_id":"A1B2C3D4E5F6"}`)
	if info.level != "DEBUG" || PatternSearchQuery(info.query) != `"payment failed for"` {
		t.Fatalf("level=%q query=%q", info.level, PatternSearchQuery(info.query))
	}
	if q := PatternSearchQuery(signatureOf(`{"request_id":"A1B2C3D4E5F6","amount":1}`).query); q != `"amount" AND "request_id"` {
		t.Fatalf("key-set query = %q", q)
	}
}

// Operatörün şekli: aynı servis, 256 satır, her satırın kendi request_id'si
// → TEK desen; satırlar Servis + Seviye taşır (patternRowIdentity).
func TestGroupBySignature_RequestIDBodiesCollapse(t *testing.T) {
	rows := make([]*LogRecord, 0, 256)
	for i := 0; i < 256; i++ {
		body := fmt.Sprintf(`{"request_id":"SPE%013dZTod%024d %02d","channel":"MOB","msg":"inquiry completed in %dms"}`,
			1015060201+i*7919, 202610240340413867+int64(i)*104729, i%100, 10+i)
		rows = append(rows, &LogRecord{Timestamp: int64(1000 + i), Body: body, SeverityText: "DEBUG"})
	}
	st := &pagedStore{rows: rows}
	res, err := GroupBySignature(t.Context(), st, Filter{Service: "orders-api"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res.Distinct != 1 || len(res.Groups) != 1 || res.Groups[0].Count != 256 {
		t.Fatalf("want 1 pattern × 256, got distinct=%d groups=%+v", res.Distinct, res.Groups)
	}
	g := res.Groups[0]
	if g.Severity != 5 || g.SeverityText != "DEBUG" || strings.Join(g.Services, ",") != "orders-api" {
		t.Fatalf("row identity lost: sev=%d/%q services=%v", g.Severity, g.SeverityText, g.Services)
	}
	if g.Query != `"inquiry completed in"` {
		t.Fatalf("query = %q", g.Query)
	}
}

// Bütçe: 2000 satırlık örnek imzası (JSON çözümü dahil) desen ucunun 2 sn
// bütçesinin çok altında kalmalı (CI gürültüsüne pay: 500 ms).
func TestNormalizeSignature_PerfBudget(t *testing.T) {
	bodies := make([]string, 2000)
	for i := range bodies {
		bodies[i] = fmt.Sprintf(`{"request_id":"SPE%013dZTod%024d","msg":"inquiry %d completed for K7QX%07d","f1":"a","f2":"b","f3":{"x":1}}`,
			i, int64(i)*31, i, i)
	}
	start := time.Now()
	for _, b := range bodies {
		_ = NormalizeSignature(b)
	}
	if d := time.Since(start); d.Milliseconds() > 500 {
		t.Fatalf("2000 signatures took %v", d)
	}
}
