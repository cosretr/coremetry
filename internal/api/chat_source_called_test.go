package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/tempo"
)

// v0.10.2293 — operatör: trace takibinde trace'in ÇAĞIRDIĞI servisin kodu
// okunamıyordu (search_code buluyor, read_source_code "bu trace'te
// bulunamadı" diyordu). Kapsam: trace'teki servisler + trace'in span'lerinin
// birebir peer.service hedefi. db / messaging / http host / ad hedefleri
// servis OLMAZ (ad sezgisi yok).

// scCalledSpan — öznenin trace'inde tek kaynak altında bir span.
type scCalledSpan struct {
	id    string
	attrs map[string]string
}

// scTempoCalls — tek kaynak (resource) + birden çok span (span attr'larıyla).
func scTempoCalls(t *testing.T, res map[string]string, spans []scCalledSpan) *tempo.Service {
	t.Helper()
	kv := func(m map[string]string) string {
		var out []string
		for k, v := range m {
			out = append(out, fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, k, v))
		}
		return strings.Join(out, ",")
	}
	ns := time.Now().Add(-time.Hour).UnixNano()
	var ss []string
	for _, sp := range spans {
		ss = append(ss, fmt.Sprintf(`{"traceId":%q,"spanId":%q,"name":"call","kind":3,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":{"code":2},"attributes":[%s]}`,
			tfTrace, sp.id, ns, ns+1_000_000, kv(sp.attrs)))
	}
	body := `{"batches":[{"resource":{"attributes":[` + kv(res) + `]},"scopeSpans":[{"spans":[` + strings.Join(ss, ",") + `]}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	tp := tempo.New()
	tp.Configure(tempo.Settings{Enabled: true, BaseURL: srv.URL})
	return tp
}

func scCalledServer(t *testing.T) (*Server, *scGit) {
	t.Helper()
	git, dv := newSCGit(t)
	git.tags["tags/1.4.2"] = scCommit
	git.trees[scCommit] = []string{scPath}
	tp := scTempoCalls(t, map[string]string{"service.name": "svc-a", "service.version": "1.4.2"}, []scCalledSpan{
		{id: tfSpan, attrs: map[string]string{"peer.service": "svc-b"}},
		{id: "00f067aa0ba902c1", attrs: map[string]string{"server.address": "host-1.example"}},
		{id: "00f067aa0ba902c2", attrs: map[string]string{"db.system": "svc-db", "db.name": "ns-1"}},
		{id: "00f067aa0ba902c3", attrs: map[string]string{"messaging.system": "svc-mq", "messaging.destination.name": "ns-1"}},
	})
	return &Server{devops: dv, tempo: tp}, git
}

// Çağrılan servis (peer.service, kendi span'i yok) okunur: dal ucu, basis bunu söyler.
func TestReadSourceCodeCalledServiceByPeerService(t *testing.T) {
	s, git := scCalledServer(t)
	ctx := withSourceSubject(context.Background(), tfTrace, tfSpan)
	sr, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "svc-b", File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.Outcome != devops.SourceOK {
		t.Fatalf("trace'in çağırdığı servis okunmalı: %v %+v", err, sr)
	}
	// Sürüm "" → dal ucu; ref gerekçesi modele "dal — çalışan sürüm bilinmiyor"
	// (mcptools.sourceRefBasis; kod turu basis'iyle aynı kalıp).
	if sr.RefKind != "branch" || sr.Version != "" || sr.Commit == scCommit {
		t.Fatalf("çağrılan servis dal ucundan okunmalı (sürüm bilinmiyor): %+v", sr)
	}
	for _, r := range git.requests() {
		if strings.Contains(r, "tags/") {
			t.Fatalf("çağrılan servis için sürüm tag'i aranmamalı: %s", r)
		}
	}
}

// Trace'teki servis sürümünü trace'ten almaya devam eder.
func TestReadSourceCodeTraceServiceKeepsTraceVersion(t *testing.T) {
	s, _ := scCalledServer(t)
	ctx := withSourceSubject(context.Background(), tfTrace, tfSpan)
	sr, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "svc-a", File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.Outcome != devops.SourceOK || sr.Commit != scCommit || sr.VersionBasis != "sohbet öznesi (trace)" {
		t.Fatalf("trace'teki servis trace sürümünden okunmalı: %v %+v", err, sr)
	}
}

// Ne trace'te ne çağrılan → ret; mesaj iki grubu da anar. db / messaging /
// http host / ad hedefleri servis olmaz; git'e istek gitmez.
func TestReadSourceCodeNonServicePeersRefused(t *testing.T) {
	s, git := scCalledServer(t)
	ctx := withSourceSubject(context.Background(), tfTrace, tfSpan)
	_, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "svc-c", File: "X.java"})
	if err == nil || !strings.Contains(err.Error(), "bu trace'te bulunamadı") {
		t.Fatalf("trace dışı servis reddedilmeli: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"trace'teki", "çağırdığı", "svc-a", "svc-b"} {
		if !strings.Contains(msg, want) {
			t.Errorf("ret mesajında %q yok: %s", want, msg)
		}
	}
	for _, target := range []string{"host-1.example", "svc-db", "svc-db/ns-1", "svc-mq", "svc-mq/ns-1", "call"} {
		if _, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: target, File: "X.java"}); err == nil {
			t.Errorf("%q servis olmayan hedef — okunmamalı", target)
		}
	}
	if n := len(git.requests()); n != 0 {
		t.Fatalf("kapsam dışı çağrılarda git'e %d istek gitti", n)
	}
}

// traceCalledServices — SAF: yalnız birebir peer.service hedefleri; kendi
// span'i olan servis çağrılan grupta tekrar edilmez.
func TestTraceCalledServices(t *testing.T) {
	spans := []chstore.SpanRow{
		{ServiceName: "svc-a", PeerService: "svc-b"},
		{ServiceName: "svc-a", Attributes: map[string]string{"peer.service": "svc-c"}},
		{ServiceName: "svc-a", PeerService: "svc-b"},
		{ServiceName: "svc-b", PeerService: "svc-a"}, // kendi span'i var → çağrılan grupta değil
		{ServiceName: "svc-a", Attributes: map[string]string{"server.address": "host-1.example"}},
		{ServiceName: "svc-a", Attributes: map[string]string{"url.full": "http://host-1.example/x"}},
		{ServiceName: "svc-a", DBSystem: "svc-db"},
		{ServiceName: "svc-a", Attributes: map[string]string{"messaging.system": "svc-mq"}},
		{ServiceName: "svc-a", Name: "svc-d"},
	}
	own := traceServiceNames(spans)
	if got := fmt.Sprint(traceCalledServices(spans, own)); got != "[svc-c]" {
		t.Fatalf("traceCalledServices: %s", got)
	}
}
