package api

// notify_actions_test.go — v0.10.749: onay sayfası kaçışı + kablolama
// pinleri (route defterde, audit DOĞRUDAN AppendAudit ile — s.audit
// public uçta claims nil olduğu için sessizce hiçbir şey yazmaz).

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWriteIgnorePageEscapes(t *testing.T) {
	rec := httptest.NewRecorder()
	writeIgnorePage(rec, 200, "Susturuldu", `<script>alert(1)</script>`, "a@example.com", "/problems?problem=p1&x=<y>")
	body := rec.Body.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("başlık kaçışsız:\n%s", body)
	}
	if !strings.Contains(body, "Susturan: a@example.com") || !strings.Contains(body, `href="/problems?problem=p1&amp;x=&lt;y&gt;"`) {
		t.Fatalf("içerik:\n%s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type: %s", ct)
	}
}

func TestNotifyIgnoreWiring(t *testing.T) {
	src, err := os.ReadFile("notify_actions.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		`"GET /api/public/notify/ignore/{token}"`,
		`registerRoutesExtra("notify-actions"`,
		"s.store.AppendAudit(",
		`Action: "notify.ignore"`,
		"MarkNotificationIgnored(",
		"AcknowledgeProblems(",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("notify_actions.go %q içermeli", want)
		}
	}
	if strings.Contains(s, "s.audit(") {
		t.Error("public uçta s.audit sessizce yazmaz — AppendAudit kullanılmalı")
	}
}
