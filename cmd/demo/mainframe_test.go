package main

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/otlp"
	tracecollpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// v0.10.2414 — the MainframeEft scenario emits the agent-shaped CTG span
// (INTERNAL, code.* only) and agent-shaped JMS spans (messaging.system=jms
// + IBM MQ JMSMessageID). Pushed through the real OTLP converter, the CTG
// span must come out as an outbound call to "mainframe" and the JMS spans
// as "ibmmq" — the columns topology and External dependencies read.
func TestMainframeEftClassifiedByConverter(t *testing.T) {
	var tr *Trace
	for i := 0; i < 200; i++ { // skip the CICS-down roll (no MQ hop)
		tr = scenarioMainframeEft()
		if len(tr.spans) > 10 {
			break
		}
	}
	var rs []*tracepb.ResourceSpans
	for _, si := range tr.spans {
		rs = append(rs, &tracepb.ResourceSpans{
			Resource:   &resourcepb.Resource{Attributes: []*commonpb.KeyValue{kvStr("service.name", si.service)}},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{si.span}}},
		})
	}
	rows, _ := otlp.ConvertTraces(&tracecollpb.ExportTraceServiceRequest{ResourceSpans: rs})
	var ctg, mq int
	for _, r := range rows {
		switch {
		case r.Name == "JavaGateway.flow":
			ctg++
			if r.Kind != "client" || r.PeerService != "mainframe" || r.RPCSystem != "cics_ctg" {
				t.Errorf("CTG span: kind=%q peer=%q rpc=%q, want client/mainframe/cics_ctg", r.Kind, r.PeerService, r.RPCSystem)
			}
		case r.Kind == "producer" || r.Kind == "consumer":
			if r.ServiceName == "eft-bsa" || r.ServiceName == "eft-settlement-listener" {
				if r.MsgSystem == "kafka" {
					continue // optional eft.posted publish
				}
				mq++
				if r.MsgSystem != "ibmmq" {
					t.Errorf("%s %s: msg_system=%q, want ibmmq", r.ServiceName, r.Name, r.MsgSystem)
				}
			}
		}
	}
	if ctg != 1 || mq != 2 {
		t.Fatalf("got %d CTG and %d MQ spans, want 1 and 2", ctg, mq)
	}
}
