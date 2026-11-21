package otlp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/cilcenk/coremetry/internal/chstore"
	tracecollpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// v0.10.2414 — mainframe (CTG/CICS) + IBM MQ-over-JMS golden test
// (/otel-conventions §2.6/§5). TAM-struct karşılaştırma.
//
// Sözleşme:
//   - OTel Java agent methods.include span'i (INTERNAL, code.namespace ya da
//     code.function.name "com.ibm.ctg." ile başlıyor) → kind=client,
//     peer_service=mainframe, rpc_system=cics_ctg;
//   - elle span (CLIENT, rpc.system=cics_ctg, peer.service YOK) →
//     peer_service=mainframe; açık rpc.system korunur;
//   - cics.* taşıyan SERVER span'i değişmez (çağrının alıcı tarafı);
//   - messaging.system=jms + IBM MQ MsgId'li messaging.message.id
//     ("ID:414d5120…", 48 hex) → msg_system=ibmmq; başka sağlayıcının id'si
//     → jms kalır;
//   - attr dizileri VERBATIM (messaging.system attr'ı "jms" kalır).
func TestConvertSpanMainframeGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "span_mainframe_ctg_mq.json"))
	if err != nil {
		t.Fatal(err)
	}
	var req tracecollpb.ExportTraceServiceRequest
	if err := protojson.Unmarshal(raw, &req); err != nil {
		t.Fatalf("fixture protojson: %v", err)
	}
	rows, links := ConvertTraces(&req)
	if len(links) != 0 {
		t.Fatalf("link beklenmiyordu: %d", len(links))
	}
	if len(rows) != 6 {
		t.Fatalf("6 span bekleniyordu, %d", len(rows))
	}
	res := []string{"service.name", "host.name", "deployment.environment"}
	resV := []string{"transfer-core", "transfer-core-1", "prod"}
	const tid = "00000000000000000000000000000001"
	ts := func(ns int64) time.Time { return time.Unix(0, 1_700_000_000_000_000_000+ns).UTC() }
	base := func(i int) chstore.Span {
		return chstore.Span{TraceID: tid, OpGroup: rows[i].OpGroup,
			ServiceName: "transfer-core", HostName: "transfer-core-1", DeployEnv: "prod",
			ResKeys: res, ResValues: resV}
	}
	want := make([]chstore.Span, 6)

	want[0] = base(0)
	want[0].SpanID, want[0].ParentID = "0000000000000001", "0000000000000009"
	want[0].Name, want[0].Kind, want[0].StatusCode = "JavaGateway.flow", "client", "unset"
	want[0].Time, want[0].Duration = ts(0), 120_000_000
	want[0].PeerService, want[0].RPCSystem = "mainframe", "cics_ctg"
	want[0].AttrKeys = []string{"code.namespace", "code.function", "thread.name"}
	want[0].AttrValues = []string{"com.ibm.ctg.client.JavaGateway", "flow", "default task-7"}
	want[0].ScopeName = "io.opentelemetry.methods"

	want[1] = base(1)
	want[1].SpanID, want[1].ParentID = "0000000000000002", "0000000000000009"
	want[1].Name, want[1].Kind = "JavaGateway.flow", "client"
	want[1].StatusCode, want[1].StatusMsg = "error", "java.io.IOException: CTG6651E Unable to connect to the Gateway"
	want[1].Time, want[1].Duration = ts(200_000_000), 10_000_000
	want[1].PeerService, want[1].RPCSystem = "mainframe", "cics_ctg"
	want[1].AttrKeys = []string{"code.function.name"}
	want[1].AttrValues = []string{"com.ibm.ctg.client.JavaGateway.flow"}
	want[1].ScopeName = "io.opentelemetry.methods"

	want[2] = base(2)
	want[2].SpanID, want[2].ParentID = "0000000000000003", "0000000000000009"
	want[2].Name, want[2].Kind, want[2].StatusCode = "CICS TR01 PAYPOST", "client", "unset"
	want[2].Time, want[2].Duration = ts(300_000_000), 80_000_000
	want[2].PeerService, want[2].RPCSystem, want[2].RPCMethod = "mainframe", "cics_ctg", "PAYPOST"
	want[2].AttrKeys = []string{"rpc.system", "rpc.method", "cics.transaction_id", "cics.program"}
	want[2].AttrValues = []string{"cics_ctg", "PAYPOST", "TR01", "PAYPOST"}
	want[2].ScopeName = "transfer-core.ctg"

	want[3] = base(3)
	want[3].SpanID = "0000000000000006"
	want[3].Name, want[3].Kind, want[3].StatusCode = "CICS TR01 PAYPOST", "server", "unset"
	want[3].Time, want[3].Duration = ts(300_000_000), 80_000_000
	want[3].AttrKeys = []string{"cics.program"}
	want[3].AttrValues = []string{"PAYPOST"}
	want[3].ScopeName = "transfer-core.ctg"

	want[4] = base(4)
	want[4].SpanID, want[4].ParentID = "0000000000000004", "0000000000000009"
	want[4].Name, want[4].Kind, want[4].StatusCode = "TRANSFER.CONFIRM publish", "producer", "unset"
	want[4].Time, want[4].Duration = ts(400_000_000), 5_000_000
	want[4].MsgSystem = "ibmmq"
	want[4].AttrKeys = []string{"messaging.system", "messaging.destination.name", "messaging.operation", "messaging.message.id"}
	want[4].AttrValues = []string{"jms", "TRANSFER.CONFIRM", "publish", "ID:414d5120514d4d41494e2020202020200123456789abcdef"}
	want[4].ScopeName = "io.opentelemetry.jms-1.1"

	want[5] = base(5)
	want[5].SpanID, want[5].ParentID = "0000000000000005", "0000000000000004"
	want[5].Name, want[5].Kind, want[5].StatusCode = "TRANSFER.AUDIT process", "consumer", "unset"
	want[5].Time, want[5].Duration = ts(410_000_000), 10_000_000
	want[5].MsgSystem = "jms"
	want[5].AttrKeys = []string{"messaging.system", "messaging.destination.name", "messaging.operation", "messaging.message.id"}
	want[5].AttrValues = []string{"jms", "TRANSFER.AUDIT", "process", "ID:broker-1-41231-1700000000000-1:1:1:1:1"}
	want[5].ScopeName = "io.opentelemetry.jms-1.1"

	for i := range want {
		got := *rows[i]
		got.Time = got.Time.UTC()
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("span %d (-want +got):\n want %+v\n  got %+v", i, want[i], got)
		}
	}
}

func TestIBMMQMessageID(t *testing.T) {
	for id, want := range map[string]bool{
		"ID:414d5120514d4d41494e2020202020200123456789abcdef": true,  // AMQ, distributed
		"ID:43535120514d5a3120202020202020200123456789abcdef": true,  // CSQ, z/OS
		"ID:C3E2D840D8D4E9F140404040404040400123456789ABCDEF": true,  // CSQ EBCDIC, upper case
		"ID:414d5120514d4d41494e202020202020":                 false, // too short
		"414d5120514d4d41494e2020202020200123456789abcdef":    false, // no ID: prefix
		"ID:414d5120514d4d41494e202020202020012345678zabcdef": false, // non-hex
		"ID:000000000000000000000000000000000000000000000001": false, // app-supplied
		"ID:broker-1-41231-1700000000000-1:1:1:1:1":           false, // other provider
		"": false,
	} {
		if got := ibmMQMessageID(id); got != want {
			t.Errorf("ibmMQMessageID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestApplyMainframeKeepsOtherKinds(t *testing.T) {
	attrs := []*commonpb.KeyValue{kvStr("code.namespace", "com.ibm.ctg.client.JavaGateway")}
	for _, kind := range []string{"server", "producer", "consumer"} {
		k, p, r := applyMainframe(attrs, kind, "", "")
		if k != kind || p != "" || r != "" {
			t.Errorf("%s: got (%q,%q,%q), want unchanged", kind, k, p, r)
		}
	}
	// Explicit peer.service wins; non-CTG internal span untouched.
	if k, p, _ := applyMainframe(attrs, "internal", "cics-prod", ""); k != "client" || p != "cics-prod" {
		t.Errorf("explicit peer: got (%q,%q)", k, p)
	}
	other := []*commonpb.KeyValue{kvStr("code.namespace", "com.example.ctgish.Client")}
	if k, p, r := applyMainframe(other, "internal", "", ""); k != "internal" || p != "" || r != "" {
		t.Errorf("non-CTG: got (%q,%q,%q)", k, p, r)
	}
}
