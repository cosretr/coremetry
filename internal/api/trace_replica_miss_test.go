package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// fakeTraceDetailDeps — çözümün dört okuması, sayaçlı.
type fakeTraceDetailDeps struct {
	primary     []chstore.SpanRow
	primaryErr  error
	stubOK      bool
	agedOut     bool
	replicas    []chstore.SpanRow
	replicasErr error
	hints       []time.Time
	allCalls    int
}

func (f *fakeTraceDetailDeps) deps() traceDetailDeps {
	return traceDetailDeps{
		Primary: func(_ context.Context, _ string, hint time.Time) ([]chstore.SpanRow, string, error) {
			f.hints = append(f.hints, hint)
			return f.primary, traceSourceCH, f.primaryErr
		},
		Stub: func(context.Context, string) (chstore.TraceAggregateStub, bool) {
			return chstore.TraceAggregateStub{StartTimeNs: 1, EndTimeNs: 2, SpanCount: 3}, f.stubOK
		},
		AgedOut: func(context.Context, int64) bool { return f.agedOut },
		AllReplicas: func(context.Context, string, int64, int64) ([]chstore.SpanRow, error) {
			f.allCalls++
			return f.replicas, f.replicasErr
		},
	}
}

// v0.10.810 + v0.10.2201 — stub → TTL kararı → tüm replika → replica_miss / aged_out.
func TestResolveTraceDetailReplicaFallback(t *testing.T) {
	ctx := context.Background()
	span := []chstore.SpanRow{{TraceID: "a", SpanID: "s1", StartTime: 1, EndTime: 2}}
	cases := []struct {
		name       string
		f          fakeTraceDetailDeps
		wantSpans  int
		wantSource string
		wantReason string
		wantAll    int
	}{
		{"birincil isabet", fakeTraceDetailDeps{primary: span}, 1, traceSourceCH, "", 0},
		{"replika ıraksaması", fakeTraceDetailDeps{stubOK: true, replicas: span}, 1, traceSourceAllReplicas, "", 1},
		{"replika boş", fakeTraceDetailDeps{stubOK: true}, 0, "", traceStubReplicaMiss, 1},
		{"replika hata", fakeTraceDetailDeps{stubOK: true, replicasErr: errors.New("x")}, 0, "", traceStubReplicaMiss, 1},
		{"yaşlı", fakeTraceDetailDeps{stubOK: true, agedOut: true, replicas: span}, 0, "", traceStubAgedOut, 0},
		{"hiç yok", fakeTraceDetailDeps{}, 0, traceSourceCH, "", 0},
	}
	for _, c := range cases {
		f := c.f
		got, err := resolveTraceDetailWith(ctx, f.deps(), "a", time.Time{})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got.Spans) != c.wantSpans || got.Source != c.wantSource || got.StubReason != c.wantReason || f.allCalls != c.wantAll {
			t.Errorf("%s: spans=%d src=%q reason=%q all=%d", c.name, len(got.Spans), got.Source, got.StubReason, f.allCalls)
		}
		if (c.wantReason != "") != (got.Stub != nil) {
			t.Errorf("%s: stub künyesi neden ile birlikte olmalı", c.name)
		}
	}
	if traceSourceAllReplicas != "clickhouse_all_replicas" {
		t.Error("FE union ile aynı dize")
	}
}

// v0.10.2201 — Operator-reported: trace sayfada AÇIK (replika yedeğiyle
// bulundu), "Bu trace'i açıkla" aynı trace'e "bulunamadı" diyordu. İki yüzey
// aynı çözümden okur: sayfanın bulduğunu açıklama da bulur.
func TestExplainFindsTraceThePageFinds(t *testing.T) {
	f := &fakeTraceDetailDeps{stubOK: true, replicas: []chstore.SpanRow{
		{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "s1", ServiceName: "pay", Name: "GET /x", StartTime: 1e18, EndTime: 1e18 + 5e6},
	}}
	old := traceDetailDepsSeam
	traceDetailDepsSeam = f.deps
	defer func() { traceDetailDepsSeam = old }()
	s := &Server{}
	page, _, err := s.traceDetailPayload(context.Background(), "0123456789abcdef0123456789abcdef", time.Time{})
	if err != nil || page["source"] != traceSourceAllReplicas {
		t.Fatalf("sayfa: %v %v", page["source"], err)
	}
	hint := time.Unix(0, 1e18)
	in, err := s.buildTraceExplainInputNear(context.Background(), "0123456789abcdef0123456789abcdef", hint)
	if err != nil {
		t.Fatalf("açıklama sayfanın bulduğu trace'i bulamadı: %v", err)
	}
	if in.RootService != "pay" {
		t.Errorf("kök servis = %q", in.RootService)
	}
	if len(f.hints) != 2 || !f.hints[1].Equal(hint) {
		t.Errorf("açıklama istemcinin pencere ipucunu ilk probe olarak geçirmeli: %v", f.hints)
	}
}

// Gerçek yokluk (MV de bilmiyor) açıklamada errExplainTraceNotFound kalır.
func TestExplainTraceGenuinelyMissing(t *testing.T) {
	f := &fakeTraceDetailDeps{}
	old := traceDetailDepsSeam
	traceDetailDepsSeam = f.deps
	defer func() { traceDetailDepsSeam = old }()
	_, err := (&Server{}).buildTraceExplainInputNear(context.Background(), "0123456789abcdef0123456789abcdef", time.Time{})
	if !errors.Is(err, errExplainTraceNotFound) {
		t.Fatalf("err = %v", err)
	}
}
