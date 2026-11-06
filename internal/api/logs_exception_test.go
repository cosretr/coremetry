package api

// logs_exception_test.go — v0.10.1882 (Log Search Faz 3 dilim 3.3): exception
// özeti yalnız ES log yolunda iliştirilir; CH log yolu değişmez (operatör
// 2026-11-06: prod logları yalnız ES'te).

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestAttachLogExceptionsOnlyES(t *testing.T) {
	body := "java.lang.OutOfMemoryError: Java heap space\n\tat com.acme.orders.Cart.write(Cart.java:1)\n\tat com.acme.orders.Api.create(Api.java:2)"
	es := &Server{logs: &esScopeLogStore{}}
	p := &logstore.Page{Logs: []*logstore.LogRecord{{Body: body, Severity: 17}}}
	es.attachLogExceptions(p)
	if p.Logs[0].Exception == nil || !p.Logs[0].Exception.Critical {
		t.Fatalf("ES: özet bekleniyordu: %+v", p.Logs[0].Exception)
	}
	ch := &Server{logs: &patternFilterLogStore{}}
	p2 := &logstore.Page{Logs: []*logstore.LogRecord{{Body: body, Severity: 17}}}
	ch.attachLogExceptions(p2)
	if p2.Logs[0].Exception != nil {
		t.Fatal("CH log yolu: özet iliştirilmemeli (bugünkü davranış)")
	}
	(&Server{}).attachLogExceptions(p2) // logs nil — panik yok
	es.attachLogExceptions(nil)
}
