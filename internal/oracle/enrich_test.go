package oracle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeEnrichStore struct {
	rows      []chstore.OracleErrorRow
	rowsErr   error
	prev      *chstore.RootCauseHypothesis
	upserted  []chstore.RootCauseHypothesis
	sumCalled []string
}

func (f *fakeEnrichStore) OracleErrorsByKey(_ context.Context, _, _, _, _ string, _, _ time.Time, _ int) ([]chstore.OracleErrorRow, error) {
	return f.rows, f.rowsErr
}
func (f *fakeEnrichStore) SpanSummariesForTraces(_ context.Context, ids []string, _, _ time.Time) ([]chstore.TraceSpanSummary, error) {
	f.sumCalled = ids
	out := []chstore.TraceSpanSummary{}
	for _, id := range ids {
		out = append(out, chstore.TraceSpanSummary{TraceID: id, ErrorService: "loan-svc"})
	}
	return out, nil
}
func (f *fakeEnrichStore) GetHypothesis(context.Context, string, string) (*chstore.RootCauseHypothesis, error) {
	return f.prev, nil
}
func (f *fakeEnrichStore) UpsertHypothesis(_ context.Context, h chstore.RootCauseHypothesis) error {
	f.upserted = append(f.upserted, h)
	return nil
}

// v0.10.898 (Aşama 3 dilim E) — kanıt: dağılımlar top-N, trace listesi en yeni
// önce ≤50, özne notu, mevcut hipotezin aday/skor alanları korunur, satır
// hatası hipotez yazmaz (soft-fail).
func TestEnricherOnEvidence(t *testing.T) {
	now := time.Date(2026, 9, 23, 14, 0, 0, 0, time.UTC)
	rows := []chstore.OracleErrorRow{}
	for i := 0; i < 60; i++ {
		rows = append(rows, chstore.OracleErrorRow{Time: now.Add(-time.Duration(i) * time.Second), TraceID: "t" + itoa(i), ExternalCode: "X" + itoa(i%3), ErrorType: "T", InstanceID: "inst-1", ChannelCode: "MOB"})
	}
	f := &fakeEnrichStore{rows: rows, prev: &chstore.RootCauseHypothesis{AnchorKind: "problem", AnchorID: "p1", TopSuspect: "keep", Confidence: 0.4}}
	subj := NewSubjectResolver(&fakeState{kv: map[string][]byte{}}, nil, nil)
	subj.lastRes["o-1\x00OP_A"] = anomaly.ExternalSubjectResolution{Service: "loan-svc", Source: "trace", Note: "trace'ten (3/3)"}
	e := NewEnricher(f, subj)
	e.now = func() time.Time { return now }
	ev := anomaly.ExternalEvent{
		Target:  anomaly.ExternalTarget{SourceID: "o-1", SourceName: "oracle-prod", Query: CounterQuery, GroupBy: CounterGroupBy},
		Problem: chstore.Problem{ID: "p1", RuleID: "anomaly:ext:oracle-prod/OP_A/E1/MOB/-:ext:error_count", Service: "loan-svc"},
		Values:  []string{"OP_A", "E1", "MOB", "-"}, Current: 40, Median: 5, MAD: 1, Z: 8, From: now.Add(-10 * time.Minute), To: now,
	}
	e.OnEvidence(context.Background(), ev)
	if len(f.upserted) != 1 {
		t.Fatalf("hipotez yazımı: %d", len(f.upserted))
	}
	h := f.upserted[0]
	if h.TopSuspect != "keep" || h.Confidence != 0.4 || h.AnchorID != "p1" || h.Deep == nil || h.Deep.External == nil {
		t.Fatalf("mevcut alanlar korunmalı, Deep dolmalı: %+v", h)
	}
	x := h.Deep.External
	if x.Rows != 60 || len(h.Deep.TraceIDs) != enrichTraceLimit || h.Deep.TraceIDs[0] != "t0" || len(f.sumCalled) != enrichTraceLimit {
		t.Fatalf("trace listesi: rows=%d ids=%d first=%s", x.Rows, len(h.Deep.TraceIDs), h.Deep.TraceIDs[0])
	}
	if x.SubjectSource != "trace" || x.SubjectNote != "trace'ten (3/3)" || x.Labels["operation.code"] != "OP_A" || x.Source != "oracle-prod" {
		t.Fatalf("özne/etiket: %+v", x)
	}
	if d := x.Distributions["external_code"]; len(d) != 3 || d[0].Count != 20 {
		t.Fatalf("dağılım: %+v", x.Distributions)
	}
	if _, ok := x.Distributions["host"]; ok {
		t.Fatal("boş alan dağılıma girmez")
	}
	if len(x.SpanSummary) != enrichTraceLimit {
		t.Fatalf("span özeti: %d", len(x.SpanSummary))
	}
	// Satır hatası → yazım yok, istatistikte hata.
	f2 := &fakeEnrichStore{rowsErr: errors.New("ch down")}
	e2 := NewEnricher(f2, nil)
	e2.OnEvidence(context.Background(), ev)
	if len(f2.upserted) != 0 {
		t.Fatal("hata yolunda hipotez yazılmamalı")
	}
	if st, ok := e2.Stats("p1"); !ok || st.Error == "" {
		t.Fatalf("istatistik: %+v", st)
	}
}
