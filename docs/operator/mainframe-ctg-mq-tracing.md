# Seeing mainframe (CICS via CTG) and IBM MQ calls in traces

A Java application on JBoss EAP calls CICS through the CICS Transaction Gateway (CTG) and puts messages on IBM MQ. This page explains how those calls show up in Coremetry traces. The local demo has an example: `cmd/demo/mainframe.go`, scenario `MainframeEft`.

## 1. Without code changes: `methods.include`

The OpenTelemetry Java agent has no built-in CTG instrumentation, but it can turn any method call into a span:

```
-javaagent:/opt/otel/opentelemetry-javaagent.jar
-Dotel.instrumentation.methods.include=com.ibm.ctg.client.JavaGateway[flow]
```

On JBoss EAP, add these to `JAVA_OPTS` (for example in `standalone.conf`). The agent supports Java 8.

What you get:

- A span named `JavaGateway.flow`, kind **INTERNAL**.
- Attributes: the method only — `code.namespace` = `com.ibm.ctg.client.JavaGateway` and `code.function` = `flow` on older agents, `code.function.name` = `com.ibm.ctg.client.JavaGateway.flow` on agents that emit the stable code semantic conventions (plus thread attributes if you enabled them).
- **No** CICS transaction id, program name, gateway address or `peer.service`. The duration is visible, but Coremetry can't tell from these attributes alone which system the call went to. Coremetry recognises `com.ibm.ctg.*` spans as mainframe calls; see section 4.
- **No error for CICS failures.** CICS-level failures such as `ECI_ERR_NO_CICS` (-3) or a transaction abend (`ECI_ERR_TRANSACTION_ABEND`, -7) are return codes, not exceptions, so the span stays OK. Only exceptions thrown by `flow` (for example an `IOException` when the gateway can't be reached) make it an error. The application's own span has to carry the failure.

## 2. Full detail: a manual span

Wrapping the CTG call in a few lines of the OTel API puts the transaction, program and return code in the trace. The agent provides the API, so the dependency only needs to be `provided`:

```xml
<dependency>
  <groupId>io.opentelemetry</groupId>
  <artifactId>opentelemetry-api</artifactId>
  <scope>provided</scope>
</dependency>
```

```java
import io.opentelemetry.api.GlobalOpenTelemetry;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.SpanKind;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.api.trace.Tracer;
import io.opentelemetry.context.Scope;

private static final Tracer TRACER = GlobalOpenTelemetry.getTracer("eft-bsa.ctg");

int callCics(JavaGateway gw, ECIRequest req, String tranId, String program) throws IOException {
    Span span = TRACER.spanBuilder("CICS " + tranId + " " + program)
        .setSpanKind(SpanKind.CLIENT)
        .setAttribute("peer.service", "mainframe")
        .setAttribute("server.address", gatewayHost)   // CTG host
        .setAttribute("server.port", gatewayPort)
        .setAttribute("rpc.system", "cics_ctg")
        .setAttribute("rpc.method", program)
        .setAttribute("cics.transaction_id", tranId)
        .setAttribute("cics.program", program)
        .setAttribute("cics.applid", cicsApplid)
        .setAttribute("ctg.request_type", "ECI")
        .startSpan();
    try (Scope ignored = span.makeCurrent()) {
        int rc = gw.flow(req);
        int eciRc = req.Cics_Rc;                       // ECI return code
        span.setAttribute("ctg.return_code", rc);
        span.setAttribute("cics.eci_return_code", eciRc);
        if (req.Abend_Code != null && !req.Abend_Code.trim().isEmpty()) {
            span.setAttribute("cics.abend_code", req.Abend_Code.trim());
        }
        if (rc != 0 || eciRc != 0) {
            span.setStatus(StatusCode.ERROR, "ECI rc=" + eciRc);
        }
        return rc;
    } catch (IOException e) {
        span.recordException(e);
        span.setStatus(StatusCode.ERROR, e.getMessage());
        throw e;
    } finally {
        span.end();
    }
}
```

With this span:

- **Topology:** CICS shows up as a `mainframe` node and in External dependencies.
- **Errors:** they show up in the trace and in the failure rate.
- **Filtering:** you can filter by transaction id or program (`cics.transaction_id`, `cics.program`).
- **Turn `methods.include` off for this method.** If you keep it, the `JavaGateway.flow` span becomes a child of this span, and Coremetry counts both as calls to `mainframe`, so every call is counted twice.

API names checked against the IBM CTG Java base-classes Javadoc: `ECIRequest.Abend_Code` (abend code, `String`) and `ECIRequest.Cics_Rc` (ECI return code) are public fields, and requests run through `JavaGateway.flow(GatewayRequest)`. Not checked: the exact return value of `flow` and whether your CTG level also offers `getRc()`, which some IBM samples use instead of `Cics_Rc`. Compile against your CTG client jar before you roll it out.

Do not put the COMMAREA content in an attribute; it can hold customer data.

## 3. IBM MQ

- **IBM MQ through JMS** (`javax.jms` / `jakarta.jms`, the usual setup on JBoss): the agent's JMS instrumentation creates the producer and consumer spans automatically and carries the trace context in the message properties, so the consumer joins the same trace. The agent always sets `messaging.system` to `jms`, whatever the provider. Its instrumentation scope (`io.opentelemetry.jms-1.1` / `io.opentelemetry.jakarta-jms-3.0`), destination name and resource attributes don't name the provider either. What does identify IBM MQ is `messaging.message.id`: the agent copies the `JMSMessageID` into it when the span ends, and an IBM MQ message id has a fixed shape (section 4).
- **IBM MQ classes for Java (`com.ibm.mq.MQQueue.put`)** without JMS: the agent creates no span. Use a manual `PRODUCER` span like the one above with `messaging.system=ibmmq`, `messaging.destination.name=<queue>` and `messaging.operation.type=send`. You then also have to carry the trace context in the message properties yourself.

## 4. What Coremetry does with these spans

Coremetry fills its own columns for these spans when they arrive (in the OTLP converter, `internal/otlp/mainframe.go`). The span attributes are stored exactly as sent. Only the columns that topology, External dependencies and the messaging views read are filled.

**CTG / CICS → `mainframe`.** A span counts as a call to the mainframe when its kind is INTERNAL or CLIENT and any of these is true:

- `code.namespace` or `code.function.name` starts with `com.ibm.ctg.` (the `methods.include` span from section 1);
- `rpc.system` is `cics_ctg`;
- it carries a non-empty `cics.transaction_id` or `cics.program`.

For such a span Coremetry stores kind `client`, peer `mainframe` and RPC system `cics_ctg`. An explicit `peer.service` or `rpc.system` on the span is kept as sent. Server and consumer spans are left alone: a span with `cics.*` attributes on the receiving side is not a call to the mainframe.

Result: a `mainframe` node in the topology and a `mainframe` row in External dependencies, with call count, latency and errors. In the trace view, the `JavaGateway.flow` span shows as CLIENT. Filters `peer.service = mainframe` and `rpc.system = cics_ctg` match it.

**IBM MQ through JMS → `ibmmq`.** A span with `messaging.system = jms` counts as IBM MQ when `messaging.message.id` is an IBM MQ message id: `ID:` followed by exactly 48 hex digits that start with the queue manager's product identifier (`414d5120` = `AMQ ` on Linux, Unix and Windows; `43535120` = `CSQ ` on z/OS; or the EBCDIC forms `c1d4d840` / `c3e2d840`). IBM MQ queue managers generate every message id this way. Spans from other JMS providers (ActiveMQ, Artemis and so on) keep `jms`.

Result: the queue shows as an `ibmmq` queue node (`queue:ibmmq:<queue>`) in the topology and under IBM MQ in the messaging views, with producer and consumer split.

Limits:

- If a send fails before IBM MQ assigns a message id, that producer span has no id and stays `jms`.
- If an application sets its own MQ message id, the rule doesn't apply. IBM tells applications not to start their own ids with these bytes.
- The filter `messaging.system = jms` no longer matches recognised IBM MQ spans. Use `messaging.system = ibmmq`.
- Spans stored before this release keep their old values.
