package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// v0.10.1034 (operatör: "Kod inceleme çalışma mantığı ile direkt Ask CoSRE
// farklı.") — get_logs_for_trace'in ham-kayıt kancası. "Kodu da incele" kod
// çekicisini incelemenin AYNI log okumasından besler; aracın çıktısı öznitelik
// değerini 200 runede kestiği için stack kancadan KESİLMEDEN gelmeli. Pinlenen:
// (1) kanca tam stack'i ve servisi görür, (2) araç çıktısı kancayla da kancasız
// da AYNI (MCP/sohbet sözleşmesi), (3) kanca dilimi değiştirmez, (4) kancasız
// ctx'te hiçbir şey çağrılmaz, (5) başarısız okumada kanca çağrılmaz.

func sinkTestStack() string {
	var b strings.Builder
	b.WriteString("com.example.cards.LimitExceededException: limit aşıldı\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "\tat com.example.cards.CardLimitService.check%d(CardLimitService.java:%d)\n", i, 10*i+4)
	}
	return b.String()
}

func callLogsTool(t *testing.T, ctx context.Context, d Deps) (any, error) {
	t.Helper()
	for _, tool := range ToolList(d) {
		if tool.Name == "get_logs_for_trace" {
			return tool.Handler(ctx, json.RawMessage(`{"trace_id":"`+validTID+`"}`))
		}
	}
	t.Fatal("get_logs_for_trace ToolList'te yok")
	return nil, nil
}

func TestGetLogsForTraceSinkGetsUncutRecords(t *testing.T) {
	stack := sinkTestStack()
	if len([]rune(stack)) <= logAttrValueMaxRunes {
		t.Fatalf("fikstür stack'i (%d rune) araç tavanını (%d) aşmalı", len([]rune(stack)), logAttrValueMaxRunes)
	}
	recs := []*logstore.LogRecord{
		{Timestamp: 1, Severity: 9, SeverityText: "INFO", ServiceName: "payments-api", Body: "istek alındı"},
		{Timestamp: 2, Severity: 17, SeverityText: "ERROR", ServiceName: "card-limits", Body: "limit kontrolü başarısız",
			Attributes: map[string]string{"exception.stacktrace": stack}},
	}
	d := Deps{LogStore: &stubLogStore{page: &logstore.Page{Logs: recs, Total: 2}}}

	plain, err := callLogsTool(t, context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	var got []*logstore.LogRecord
	calls := 0
	ctx := WithTraceLogsSink(context.Background(), func(logs []*logstore.LogRecord) {
		calls++
		got = logs
	})
	hooked, err := callLogsTool(t, ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(got) != 2 {
		t.Fatalf("kanca çağrısı=%d kayıt=%d; 1 / 2", calls, len(got))
	}
	if got[1].Attributes["exception.stacktrace"] != stack || got[1].ServiceName != "card-limits" {
		t.Error("kanca kesilmemiş stack'i ve onu basan servisi görmedi")
	}
	if got[0] != recs[0] || got[1] != recs[1] {
		t.Error("kanca dilimi kopyalanmış/yeniden sıralanmış — aynı okumanın kayıtları beklenir")
	}
	pb, _ := json.Marshal(plain)
	hb, _ := json.Marshal(hooked)
	if string(pb) != string(hb) {
		t.Errorf("kanca araç çıktısını değiştirdi\nkancasız: %s\nkancalı:  %s", pb, hb)
	}
	if strings.Contains(string(hb), "CardLimitService.java:94") {
		t.Error("araç çıktısı stack'i kesmedi — fikstür tavanı sınamıyor")
	}

	// Başarısız okuma: kanca çağrılmaz (degraded zarf).
	calls = 0
	bad := Deps{LogStore: &stubLogStore{searchErr: logstore.ErrBackendSlow}}
	if _, err := callLogsTool(t, ctx, bad); err != nil {
		t.Fatalf("yavaş backend araç hatası olmamalı (degraded): %v", err)
	}
	if calls != 0 {
		t.Errorf("başarısız okumada kanca %d kez çağrıldı", calls)
	}
}

func TestWithTraceLogsSinkNil(t *testing.T) {
	ctx := context.Background()
	if WithTraceLogsSink(ctx, nil) != ctx {
		t.Error("nil kanca ctx'i değiştirdi")
	}
	if traceLogsSinkOf(ctx) != nil {
		t.Error("kancasız ctx'te kanca bulundu")
	}
}
