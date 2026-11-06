package logstore

// exception_summary_test.go — v0.10.1880 (Log Search Faz 3 dilim 3.3). Java ve
// .NET fikstürleri: tür + mesaj + kök sebep, sınıf (kaynak tükenmesi vb.),
// uygulama çerçevesi satırları, kritik bayrağı; stack alanı / gövde / OTel
// öznitelikleri; exception olmayan satırda özet YOK (rozet uydurulmaz).

import (
	"reflect"
	"strings"
	"testing"
)

const javaOOM = `java.lang.OutOfMemoryError: Java heap space
	at com.acme.orders.cart.CartSerializer.write(CartSerializer.java:118)
	at com.acme.orders.api.OrderResource.create(OrderResource.java:64)
	at org.jboss.resteasy.core.MethodInjectorImpl.invoke(MethodInjectorImpl.java:170)
	at io.undertow.servlet.handlers.ServletHandler.handleRequest(ServletHandler.java:74)
Caused by: java.lang.OutOfMemoryError: Java heap space
	at com.acme.orders.cart.CartBuffer.grow(CartBuffer.java:41)`

const dotnetInvalid = `System.InvalidOperationException: Sequence contains no elements
   at Acme.Teller.Queue.TicketService.Next() in /src/Teller/Queue/TicketService.cs:line 87
   at Microsoft.AspNetCore.Mvc.Infrastructure.ActionMethodExecutor.Execute(ObjectMethodExecutor executor)
   at System.Threading.Tasks.Task.Wait()`

func TestSummarizeExceptionJavaBody(t *testing.T) {
	got := SummarizeException(nil, javaOOM, 17)
	if got == nil {
		t.Fatal("özet bekleniyordu")
	}
	if got.Type != "java.lang.OutOfMemoryError" || got.Message != "Java heap space" || got.Cause != "" {
		t.Fatalf("tür/mesaj/sebep: %+v", got)
	}
	if got.Class != "resource" || !got.Critical {
		t.Fatalf("kaynak tükenmesi + kritik: %+v", got)
	}
	if got.Frames != 5 || got.AppFrames != 3 || !reflect.DeepEqual(got.AppLines, []int{1, 2, 6}) {
		t.Fatalf("çerçeveler: frames=%d app=%d lines=%v", got.Frames, got.AppFrames, got.AppLines)
	}
	if len(got.Top) != 3 || !strings.Contains(got.Top[0], "CartSerializer") {
		t.Fatalf("üst uygulama çerçeveleri: %v", got.Top)
	}
	if got.StackField != "" {
		t.Fatalf("stack gövdeden: %q", got.StackField)
	}
}

func TestSummarizeExceptionDotnetBody(t *testing.T) {
	got := SummarizeException(nil, dotnetInvalid, 17)
	if got == nil || got.Type != "System.InvalidOperationException" || got.Message != "Sequence contains no elements" {
		t.Fatalf(".NET tür/mesaj: %+v", got)
	}
	if got.Class != "" || got.Critical {
		t.Fatalf("sınıfsız, kritik değil: %+v", got)
	}
	if got.Frames != 3 || got.AppFrames != 1 || !reflect.DeepEqual(got.AppLines, []int{1}) {
		t.Fatalf(".NET çerçeveler: frames=%d app=%d lines=%v", got.Frames, got.AppFrames, got.AppLines)
	}
}

func TestSummarizeExceptionStackFieldAndCause(t *testing.T) {
	stack := "org.springframework.dao.DataAccessResourceFailureException: could not execute\n" +
		"\tat com.acme.payments.Repo.save(Repo.java:12)\n" +
		"\tat org.springframework.orm.Template.run(Template.java:9)\n" +
		"Caused by: java.sql.SQLTransientConnectionException: HikariPool-1 - Connection is not available, request timed out after 30000ms\n" +
		"\tat com.zaxxer.hikari.pool.HikariPool.getConnection(HikariPool.java:155)"
	got := SummarizeException(map[string]string{"error.stack_trace": stack}, "payment failed", 17)
	if got == nil || got.StackField != "error.stack_trace" {
		t.Fatalf("ECS stack alanı: %+v", got)
	}
	if got.Type != "org.springframework.dao.DataAccessResourceFailureException" || got.Cause != "java.sql.SQLTransientConnectionException" {
		t.Fatalf("dış tür + kök sebep: %+v", got)
	}
	if got.Class != "resource" || !got.Critical {
		t.Fatalf("havuz tükenmesi kök sebepten: %+v", got)
	}
}

func TestSummarizeExceptionOTelAttrsAndClasses(t *testing.T) {
	got := SummarizeException(map[string]string{"exception.type": "java.net.SocketTimeoutException", "exception.message": "Read timed out"}, "upstream call failed", 13)
	if got == nil || got.Type != "java.net.SocketTimeoutException" || got.Class != "timeout" || got.Critical {
		t.Fatalf("OTel öznitelikleri: %+v", got)
	}
	cases := map[string]string{
		"java.lang.NullPointerException":  "null",
		"java.net.ConnectException":       "connectivity",
		"java.sql.SQLException ORA-00060": "database",
		"System.NullReferenceException":   "null",
		"java.lang.StackOverflowError":    "resource",
		"java.lang.IllegalStateException": "",
	}
	for typ, want := range cases {
		if got := classifyException(typ, ""); got != want {
			t.Errorf("%s → %q, want %q", typ, got, want)
		}
	}
	if fatal := SummarizeException(nil, "java.lang.IllegalStateException: boom", 21); fatal == nil || !fatal.Critical {
		t.Fatalf("FATAL kritik: %+v", fatal)
	}
}

func TestSummarizeExceptionNoneForPlainLines(t *testing.T) {
	for _, body := range []string{
		"order 42 accepted",
		"Retrying after com.acme.FooException: will retry", // satır başında değil
		"",
	} {
		if got := SummarizeException(map[string]string{"CHANNEL_CODE": "MOBILE"}, body, 9); got != nil {
			t.Errorf("%q: özet olmamalı, %+v", body, got)
		}
	}
}

func TestAttachExceptions(t *testing.T) {
	rows := []*LogRecord{{Body: javaOOM, Severity: 17}, {Body: "ok"}, nil}
	AttachExceptions(rows)
	if rows[0].Exception == nil || rows[1].Exception != nil {
		t.Fatalf("iliştirme: %+v %+v", rows[0].Exception, rows[1].Exception)
	}
}
