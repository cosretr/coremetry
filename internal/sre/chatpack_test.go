package sre

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const packTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

// fakePackReaders — sabit veri; logs çağrı sayısı ES sorgu sayısıdır.
func fakePackReaders(logCalls *int32, delay time.Duration) ChatPackReaders {
	wait := func(ctx context.Context) error {
		select {
		case <-time.After(delay):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ChatPackReaders{
		RED: func(ctx context.Context, _ string, _, _ time.Time) (chstore.ServiceWindowRED, error) {
			if err := wait(ctx); err != nil {
				return chstore.ServiceWindowRED{}, err
			}
			return chstore.ServiceWindowRED{Spans: 3600, Errors: 180, P95Ms: 420}, nil
		},
		ErrorOps: func(ctx context.Context, _ string, _, _ time.Time, _ int) ([]chstore.ErrorRank, error) {
			return []chstore.ErrorRank{{Name: "POST /api/checkout", Spans: 1000, Errors: 150}}, wait(ctx)
		},
		Exceptions: func(ctx context.Context, _ chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) {
			return []chstore.ExceptionRow{{Type: "java.net.SocketTimeoutException", Message: "Read timed out", Count: 412, SampleTraceID: packTraceID}}, wait(ctx)
		},
		Deploys: func(ctx context.Context, _ string, _, _ time.Time) (mcptools.DeployCheck, error) {
			return mcptools.DeployCheck{Rows: []mcptools.ChangeRow{{Source: "rollout", Version: "1.4.2", TimeUnixNs: 1794992400000000000}}}, wait(ctx)
		},
		Graph: func(ctx context.Context, svc string, _, _ time.Time) ([]chstore.ServiceEdge, error) {
			return []chstore.ServiceEdge{{Source: svc, Target: "payments", CallCount: 900, ErrorRate: 12.5, AvgMs: 800}, {Source: "web", Target: svc}}, wait(ctx)
		},
		Anomalies: func(ctx context.Context, _ chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error) {
			return nil, wait(ctx)
		},
		Trace: func(ctx context.Context, id string) (string, error) {
			return `{"trace_id":"` + id + `","error_spans":[{"service":"payments","name":"POST /api/payments","exception_type":"java.net.SocketTimeoutException"}],"critical_path":[{"service":"checkout","name":"POST /api/checkout"}]}`, wait(ctx)
		},
		Logs: func(ctx context.Context, _ string, _ int) (string, error) {
			atomic.AddInt32(logCalls, 1)
			return `{"total":2,"rows":[{"sev":"ERROR","svc":"payments","body":"Read timed out"}],"page_url":"/logs"}`, wait(ctx)
		},
	}
}

func packReq() ChatPackRequest {
	to := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	return ChatPackRequest{Service: "checkout", From: to.Add(-time.Hour), To: to}
}

func TestChatPackRunsParallelWithinBudget(t *testing.T) {
	var logs int32
	delay := 60 * time.Millisecond
	var chips []string
	t0 := time.Now()
	p := runChatPack(context.Background(), fakePackReaders(&logs, delay), packReq(), chatPackBudget{wave: 2 * time.Second, step: time.Second},
		func(s ChatPackStep) { chips = append(chips, s.Label) })
	// 7 adım sıralı olsaydı ≥ 9 × delay (belirti iki okuma); paralel iki dalga ≈ 3 × delay.
	if el := time.Since(t0); el >= 7*delay {
		t.Fatalf("paralel değil: %v", el)
	}
	if len(p.Steps) != 7 || len(p.Digest.Unchecked) != 0 {
		t.Fatalf("adımlar %+v unchecked %+v", p.Steps, p.Digest.Unchecked)
	}
	if logs != 1 {
		t.Fatalf("ES sorgusu %d, en çok 1", logs)
	}
	if p.Digest.RED == nil || p.Digest.RED.ErrPct != 5 || p.Digest.Trace == nil || !strings.Contains(p.Digest.Trace.ErrorSpan, "payments") {
		t.Fatalf("özet %+v", p.Digest)
	}
	if len(p.Digest.Deps) != 1 || p.Digest.Deps[0].Target != "payments" {
		t.Fatalf("yalnız aşağı akış: %+v", p.Digest.Deps)
	}
	if chips[1] != "hata özeti: 1 operasyon, 1 istisna grubu" || p.TraceJSON == "" || p.LogsJSON == "" {
		t.Fatalf("çipler %q", chips)
	}
}

func TestChatPackTimeoutAndErrorsAreUnchecked(t *testing.T) {
	var logs int32
	rd := fakePackReaders(&logs, 0)
	rd.Graph = func(ctx context.Context, _ string, _, _ time.Time) ([]chstore.ServiceEdge, error) {
		<-ctx.Done() // adım süresi dolana dek bekler
		return nil, ctx.Err()
	}
	rd.Deploys = func(context.Context, string, time.Time, time.Time) (mcptools.DeployCheck, error) {
		return mcptools.DeployCheck{}, errors.New("boom")
	}
	rd.Anomalies = nil
	p := runChatPack(context.Background(), rd, packReq(), chatPackBudget{wave: 200 * time.Millisecond, step: 50 * time.Millisecond}, nil)
	got := map[string]string{}
	for _, u := range p.Digest.Unchecked {
		got[u.Step] = u.Reason
	}
	want := map[string]string{PackStepDeps: packReasonTimeout, PackStepChanges: packReasonError, PackStepAnomalies: packReasonNoReader}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, beklenen %q (%v)", k, got[k], v, got)
		}
	}
	if p.Digest.Trace == nil {
		t.Fatal("paket kısmi de olsa teslim edilmeli")
	}
}

func TestChatPackWaveDeadlineDoesNotWaitForSlowReader(t *testing.T) {
	var logs int32
	rd := fakePackReaders(&logs, 0)
	block := make(chan struct{})
	defer close(block)
	rd.Graph = func(context.Context, string, time.Time, time.Time) ([]chstore.ServiceEdge, error) {
		<-block // bağlamı dinlemeyen okuyucu
		return nil, nil
	}
	t0 := time.Now()
	p := runChatPack(context.Background(), rd, packReq(), chatPackBudget{wave: 80 * time.Millisecond, step: time.Second}, nil)
	if time.Since(t0) > time.Second {
		t.Fatal("dalga süresi uygulanmadı")
	}
	if p.Digest.Unchecked[0].Step != PackStepDeps || p.Digest.Unchecked[0].Reason != packReasonTimeout {
		t.Fatalf("%+v", p.Digest.Unchecked)
	}
}

func TestChatPackNoSampleTraceSkipsWave2(t *testing.T) {
	var logs int32
	rd := fakePackReaders(&logs, 0)
	rd.Exceptions = func(context.Context, chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) { return nil, nil }
	p := runChatPack(context.Background(), rd, packReq(), chatPackBudget{wave: time.Second, step: time.Second}, nil)
	if logs != 0 || len(p.Digest.Unchecked) != 2 || p.Digest.Unchecked[0].Reason != packReasonNoTrace {
		t.Fatalf("logs %d unchecked %+v", logs, p.Digest.Unchecked)
	}
}

func TestChatPackDigestCap(t *testing.T) {
	long := strings.Repeat("ş", 400)
	d := ChatPackDigest{Service: "checkout", RED: &PackRED{ErrPct: 1}}
	for i := 0; i < 3; i++ {
		d.ErrorOps = append(d.ErrorOps, PackErrorOp{Name: truncRunes(long, 100)})
		d.Exceptions = append(d.Exceptions, PackException{Type: truncRunes(long, 120), Message: truncRunes(long, 120), SampleTrace: packTraceID})
		d.Deploys = append(d.Deploys, PackDeploy{Version: truncRunes(long, 60)})
		d.Deps = append(d.Deps, PackDep{Target: truncRunes(long, 80)})
		d.Anomalies = append(d.Anomalies, PackAnomaly{Pattern: truncRunes(long, 80)})
	}
	d.Trace = &PackTrace{ID: packTraceID, ErrorSpan: truncRunes(long, 100), TopPath: []string{truncRunes(long, 80), truncRunes(long, 80), truncRunes(long, 80)}}
	d.Logs = &PackLogs{Samples: []string{truncRunes(long, 100), truncRunes(long, 100), truncRunes(long, 100)}}
	s := ChatPack{Digest: d}.DigestJSON()
	if len(s) > ChatPackDigestMax || !json.Valid([]byte(s)) || !strings.Contains(s, `"exceptions"`) {
		t.Fatalf("özet %d B", len(s))
	}
	if ChatPackWave != 4*time.Second || ChatPackStepLimit != 3*time.Second || ChatPackDigestMax != 2048 {
		t.Fatal("bütçe sabitleri tasarımdan saptı")
	}
}

// v0.10.2378 — özet eşiği parametreli: varsayılan eşik DigestJSON ile bayt
// bayt aynı; geniş eşikte (modelin kanıt ölçeği) hiçbir alan kırpılmaz.
func TestChatPackDigestWithin(t *testing.T) {
	long := strings.Repeat("ş", 400)
	mk := func() ChatPack { // her çağrıya taze paket: kırpma işaretçili alanları (Logs/Trace) yerinde kısaltır
		d := ChatPackDigest{Service: "checkout", RED: &PackRED{ErrPct: 1}}
		for i := 0; i < 3; i++ {
			d.ErrorOps = append(d.ErrorOps, PackErrorOp{Name: truncRunes(long, 100)})
			d.Exceptions = append(d.Exceptions, PackException{Type: truncRunes(long, 120), Message: truncRunes(long, 120), SampleTrace: packTraceID})
			d.Deps = append(d.Deps, PackDep{Target: truncRunes(long, 80)})
		}
		d.Logs = &PackLogs{Samples: []string{truncRunes(long, 100), truncRunes(long, 100), truncRunes(long, 100)}}
		return ChatPack{Digest: d}
	}
	full, _ := json.Marshal(mk().Digest)
	if len(full) <= ChatPackDigestMax {
		t.Fatalf("fikstür eşiği aşmalı: %d", len(full))
	}
	base := mk().DigestJSON()
	if base != mk().DigestJSONWithin(ChatPackDigestMax) || len(base) > ChatPackDigestMax || base == string(full) {
		t.Fatalf("varsayılan eşik: %d bayt", len(base))
	}
	if wide := mk().DigestJSONWithin(8 * ChatPackDigestMax); wide != string(full) {
		t.Fatalf("geniş eşik: özet kırpılmadan dönmeli (%d / %d bayt)", len(wide), len(full))
	}
}
