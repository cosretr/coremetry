// Coremetry demo — mainframe hop: CICS via CICS Transaction Gateway (CTG)
// plus an IBM MQ hand-off (v0.10.2414).
//
// Flow (operator-specified): mobile app → api-gateway → mobile-bff →
// eft-bsa (JBoss EAP, Java 8) → mainframe. eft-bsa runs the CICS posting
// transaction through the CTG (ECI, COMMAREA) and puts a confirmation
// message on an IBM MQ queue; eft-settlement-listener consumes it (MQ
// carries the trace context in message properties, so the consumer span
// sits in the same trace).
//
// The CTG hop has the shape the OTel Java agent gives it with
// otel.instrumentation.methods.include (see ctgCall). The IBM MQ hop has
// the shape the agent's JMS instrumentation gives it: messaging.system=
// "jms" and messaging.message.id = the IBM MQ JMSMessageID ("ID:" + 48
// hex digits starting with "AMQ "). Coremetry's converter turns the CTG
// span into a call to "mainframe" and the JMS spans into "ibmmq"
// (internal/otlp/mainframe.go).
// All names are fictional.
package main

import (
	"fmt"
	mrand "math/rand/v2"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const (
	mqManager = "QMEFT01"
	mqQueue   = "EFT.CONFIRM.OUT"
)

var (
	rtAndroid = [4]string{"java", "Android Runtime", "14", "Android 14 (ART)"}
	rtJava8   = [4]string{"java", "OpenJDK Runtime Environment", "1.8.0_402", "OpenJDK 64-Bit Server VM 25.402-b06 (JBoss EAP 7.4)"}
)

func init() {
	for _, s := range []Service{
		svc("mobile-app", rtAndroid, "mobile-app-android", "mobile-app-android-beta"),
		svc("eft-bsa", rtJava8, "eft-bsa-prod-1", "eft-bsa-prod-2"),
		svc("eft-settlement-listener", rtJava8, "eftl-prod-1", "eftl-prod-2"),
	} {
		services[s.Name] = s
	}
	scenarios = append(scenarios, struct {
		name   string
		weight int
		fn     scenario
	}{"MainframeEft", 2, scenarioMainframeEft})
}

// ctgCall appends the span the OTel Java agent produces for a CTG call when
// the gateway method is listed in otel.instrumentation.methods.include
// (e.g. com.ibm.ctg.client.JavaGateway[flow]). The agent has no CTG
// instrumentation of its own, so this is an INTERNAL span named
// Class.method with code.* attributes only: no peer.service, no server
// address, no transaction or program id. CICS-level failures (ECI return
// codes such as ECI_ERR_NO_CICS) are return values, not exceptions, so
// the span stays OK; only gateway connection errors (IOException) mark
// it as an error. The caller's span carries the business failure.
func ctgCall(t *Trace, service string, parent []byte, off, d time.Duration, connErr bool) []byte {
	return t.Add(service, "JavaGateway.flow", tracepb.Span_SPAN_KIND_INTERNAL, parent, off, d,
		kv("code.namespace", "com.ibm.ctg.client.JavaGateway", "code.function", "flow",
			"code.function.name", "com.ibm.ctg.client.JavaGateway.flow",
			"thread.name", "default task-"+fmt.Sprint(mrand.IntN(64)+1)),
		!connErr, ifErr(connErr, "java.io.IOException: CTG6651E Unable to connect to the Gateway"))
}

// mqMessageID is an IBM MQ JMSMessageID: "ID:" + hex of the 24-byte MQMD
// MsgId the queue manager generates — "AMQ " + the queue manager name
// padded to 12 bytes + 8 unique bytes.
func mqMessageID() string {
	return fmt.Sprintf("ID:414d5120%x%016x", fmt.Sprintf("%-12s", mqManager), mrand.Uint64())
}

func scenarioMainframeEft() *Trace {
	t := NewTrace()
	total := dur(300, 800)
	ref := txnRef()
	amt := amount(100, 50000)
	cicsDown := rollFail(3) // CICS region unavailable (AEY9)
	httpStatus := iff(cicsDown, 503, 201)
	M.RecordHTTP("api-gateway", "POST", "/api/v1/eft", httpStatus, ms(total))
	M.RecordDB("eft-bsa", "oracle", "INSERT", ms(total)*0.15)
	M.RecordBiz("eft.submitted")
	errIf := func(msg string) string { return ifErr(cicsDown, msg) }

	// Mobile app → api-gateway.
	app := t.Add("mobile-app", "POST /api/v1/eft", tracepb.Span_SPAN_KIND_CLIENT, nil, 0, total,
		kv("http.method", "POST", "http.url", "https://api.example.internal/api/v1/eft", "http.status_code", httpStatus,
			"banking.txn_ref", ref, "banking.amount", amt, "peer.service", "api-gateway",
			"os.name", "Android", "app.version", "8.14.2"),
		!cicsDown, errIf("503 Service Unavailable"))
	api := t.Add("api-gateway", "POST /api/v1/eft", tracepb.Span_SPAN_KIND_SERVER, app, 6*ms1, total-12*ms1,
		kv("http.method", "POST", "http.route", "/api/v1/eft", "http.status_code", httpStatus,
			"banking.txn_ref", ref), !cicsDown, errIf("503: mainframe unavailable"))

	// api-gateway → mobile-bff.
	bffDur := total - 24*ms1
	t.Add("api-gateway", "POST mobile-bff /eft", tracepb.Span_SPAN_KIND_CLIENT, api, 10*ms1, bffDur,
		kv("http.method", "POST", "http.url", "http://mobile-bff/eft", "http.status_code", httpStatus,
			"peer.service", "mobile-bff"), !cicsDown, errIf("503"))
	bff := t.Add("mobile-bff", "POST /eft", tracepb.Span_SPAN_KIND_SERVER, api, 12*ms1, bffDur-4*ms1,
		kv("http.method", "POST", "http.route", "/eft", "http.status_code", httpStatus, "banking.txn_ref", ref),
		!cicsDown, errIf("eft-bsa returned 503"))

	// mobile-bff → eft-bsa (JBoss EAP, Java 8, REST).
	bsaStart := 20 * ms1
	bsaDur := bffDur - 20*ms1
	t.Add("mobile-bff", "POST eft-bsa /eft-bsa/rest/eft/submit", tracepb.Span_SPAN_KIND_CLIENT, bff, bsaStart, bsaDur,
		kv("http.method", "POST", "http.url", "http://eft-bsa:8080/eft-bsa/rest/eft/submit", "http.status_code", httpStatus,
			"peer.service", "eft-bsa"), !cicsDown, errIf("503"))
	bsa := t.Add("eft-bsa", "POST /eft-bsa/rest/eft/submit", tracepb.Span_SPAN_KIND_SERVER, bff, bsaStart+2*ms1, bsaDur-4*ms1,
		kv("http.method", "POST", "http.route", "/eft-bsa/rest/eft/submit", "http.status_code", httpStatus,
			"webengine.name", "JBoss EAP", "webengine.version", "7.4.14",
			"banking.txn_ref", ref, "banking.amount", amt),
		!cicsDown, errIf("CICS ECI rc=-3 ECI_ERR_NO_CICS (CICSPRD1)"))
	t.Add("eft-bsa", "INSERT EFT_ORDERS", tracepb.Span_SPAN_KIND_CLIENT, bsa, bsaStart+6*ms1, dur(6, 25),
		oraDB("COREBANK", "INSERT", "EFT_ORDERS", "INSERT INTO EFT_ORDERS (REF, AMOUNT, STATUS) VALUES (:1, :2, 'NEW')", oracleCore), true, "")

	// eft-bsa → mainframe: CICS posting transaction via CTG (dominant latency).
	cicsOff := bsaStart + 36*ms1
	cicsDur := dur(60, 260)
	// CICS down → the ECI call returns ECI_ERR_NO_CICS (-3); the agent span
	// stays OK, eft-bsa marks its own span as failed.
	ctgCall(t, "eft-bsa", bsa, cicsOff, iff(cicsDown, dur(2, 8), cicsDur), false)
	if cicsDown {
		M.RecordBiz("eft.mainframe_failed")
		return t
	}

	// IBM MQ put of the confirmation message.
	putOff := cicsOff + cicsDur + 4*ms1
	msgID := mqMessageID()
	put := t.Add("eft-bsa", mqQueue+" publish", tracepb.Span_SPAN_KIND_PRODUCER, bsa, putOff, dur(2, 9),
		kv("messaging.system", "jms", "messaging.operation", "publish",
			"messaging.destination.name", mqQueue, "messaging.message.id", msgID,
			"banking.txn_ref", ref), true, "")
	M.RecordBiz("eft.mq_put")

	// Consumer picks it up a little later in the same trace.
	recvOff := putOff + dur(5, 40)
	lst := t.Add("eft-settlement-listener", mqQueue+" process", tracepb.Span_SPAN_KIND_CONSUMER, put, recvOff, dur(15, 60),
		kv("messaging.system", "jms", "messaging.operation", "process",
			"messaging.destination.name", mqQueue, "messaging.message.id", msgID,
			"banking.txn_ref", ref), true, "")
	t.Add("eft-settlement-listener", "UPDATE EFT_ORDERS", tracepb.Span_SPAN_KIND_CLIENT, lst, recvOff+3*ms1, dur(4, 18),
		oraDB("COREBANK", "UPDATE", "EFT_ORDERS", "UPDATE EFT_ORDERS SET STATUS = 'POSTED' WHERE REF = :1", oracleCore), true, "")
	if mrand.IntN(100) < 30 {
		kafkaPub(t, "eft-settlement-listener", lst, "eft.posted", recvOff+25*ms1, ref)
	}
	M.RecordBiz("eft.posted")
	return t
}
