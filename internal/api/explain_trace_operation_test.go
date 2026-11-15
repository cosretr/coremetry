package api

// explain_trace_operation_test.go — v0.10.1329 (operatör-bildirimli): trace
// 6008dded… kökü POST /ACMWEB/application/expressOrderPreValidate, kendi
// Oracle satırı …APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST; Trace çipi
// sözlüğün en çok satırlı adayını (…APPLICATIONINQ_…, "+15") gösteriyordu.
// pickMainOperation FE pickTraceOperation'ın backend ikizi: Oracle satırları →
// span attribute'u → "" (sözlük YOK).

import (
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPickMainOperation(t *testing.T) {
	const (
		trx  = "LOAN_CONSUMER_APP_APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST"
		inq  = "LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST"
		inq2 = "LOAN_CONSUMER_APP_APPLICATIONINQ_CUSTOMER_LIMIT_REST"
	)
	base := time.Unix(1_760_000_000, 0)
	row := func(op, spanID string, sec int) chstore.OracleErrorRow {
		return chstore.OracleErrorRow{OperationCode: op, SpanID: spanID, Time: base.Add(time.Duration(sec) * time.Second)}
	}
	span := func(id, parent, kind, svc string, start int64, attrs map[string]string) chstore.SpanRow {
		return chstore.SpanRow{SpanID: id, ParentSpanID: parent, Kind: kind, ServiceName: svc, StartTime: start, Attributes: attrs}
	}
	spans := []chstore.SpanRow{
		span("r", "", "server", "acmweb", 1, map[string]string{"FUNCTION_CODE": "LCA01"}),
		span("c1", "r", "server", "loan-inq", 2, nil),
		span("c2", "r", "server", "loan-inq", 3, nil),
	}
	cases := []struct {
		name       string
		rows       []chstore.OracleErrorRow
		spans      []chstore.SpanRow
		wantMain   string
		wantOthers []string
		wantSource string
	}{
		{"tek Oracle satırı", []chstore.OracleErrorRow{row(trx, "", 10)}, spans, trx, nil, "oracle"},
		{"TRX kök + INQ çocuklar → TRX ana (INQ daha çok satırlı olsa da)",
			[]chstore.OracleErrorRow{row(inq, "c1", 5), row(inq, "c2", 6), row(inq2, "c2", 7), row(trx, "r", 9)},
			spans, trx, []string{inq, inq2}, "oracle"},
		{"span bağı yok → en çok satırlı, eşitlikte en erken",
			[]chstore.OracleErrorRow{row(inq2, "", 8), row(trx, "", 3), row(inq, "", 5), row(inq, "", 6), row(trx, "", 4)},
			spans, trx, []string{inq, inq2}, "oracle"},
		{"operasyonsuz satır sayılmaz", []chstore.OracleErrorRow{row(" ", "r", 1), row(inq, "c1", 2)}, spans, inq, nil, "oracle"},
		{"satır yok + kök span attribute'u", nil, []chstore.SpanRow{
			span("r", "", "server", "acmweb", 1, map[string]string{"OPERATION_CODE": trx}),
			span("c", "r", "server", "loan-inq", 2, map[string]string{"operation.code": inq}),
		}, trx, []string{inq}, "span"},
		{"kökte yok → giriş span'i, sonra herhangi bir span", nil, []chstore.SpanRow{
			span("r", "", "server", "acmweb", 1, nil),
			span("i", "r", "internal", "acmweb", 2, map[string]string{"operation_code": inq2}),
			span("s", "r", "consumer", "loan-inq", 3, map[string]string{"operation.code": inq}),
		}, inq, []string{inq2}, "span"},
		{"yalnız internal span taşıyor", nil, []chstore.SpanRow{
			span("r", "", "server", "acmweb", 1, nil),
			span("i", "r", "internal", "acmweb", 2, map[string]string{"operation_code": inq2}),
		}, inq2, nil, "span"},
		// Sözlük bu fonksiyonda yok: fonksiyon kodu tek başına ana operasyon üretmez.
		{"yalnız FUNCTION_CODE → bilinmiyor", nil, spans, "", nil, ""},
		{"boş girdi", nil, nil, "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			main, others, source := pickMainOperation(c.rows, c.spans, nil)
			if main != c.wantMain || source != c.wantSource || !reflect.DeepEqual(others, c.wantOthers) {
				t.Fatalf("pickMainOperation = (%q, %q, %q), want (%q, %q, %q)", main, others, source, c.wantMain, c.wantOthers, c.wantSource)
			}
		})
	}
}
