package chstore

import (
	"os"
	"strings"
	"testing"
	"time"
)

// v0.10.1015 — Problems sekmesinde öğretme: karar İMZAYA bağlıdır ve ortak
// durum tablosunda (saved_views, page="problem-verdict") saklanır.
// v0.10.1024 — okuma yalnız sistem satırına güvenir (owner_id = '', id =
// "pv:" + imza); sistem sayfaları genel /api/views ucuna kapalı.

func TestValidProblemSignature(t *testing.T) {
	ok := []string{
		"p:rule-high-error-rate|payments-api",
		"e:9f8a7c6d5e4b3a21",
		"a:latency|orders-api|p99 up",
		"p:anomaly:ext:oracle-prod/OP_A/E1/MOB/-:ext:error_count|eft-svc",
	}
	bad := []string{"", "p:", "p:   ", "x:abc", "rule|svc", "e:" + strings.Repeat("a", 400), "p:a\nb", "e:\x00"}
	for _, s := range ok {
		if !ValidProblemSignature(s) {
			t.Errorf("geçerli olmalı: %q", s)
		}
	}
	for _, s := range bad {
		if ValidProblemSignature(s) {
			t.Errorf("geçersiz olmalı: %q", s)
		}
	}
	if !ValidProblemVerdict("real") || !ValidProblemVerdict("noise") || ValidProblemVerdict("") || ValidProblemVerdict("anomaly") {
		t.Error("karar yalnız real | noise")
	}
}

func TestProblemVerdictRowRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	if got := problemVerdictID("e:abc"); got != "pv:e:abc" {
		t.Fatalf("satır kimliği: %q", got)
	}
	q := `{"signature":"p:rule-1|payments-api","label":"Error rate > 5%","kind":"problem","service":"payments-api","by":"op@example.test"}`
	id := problemVerdictID("p:rule-1|payments-api")
	v, ok := problemVerdictFromRow(id, "noise", q, at)
	if !ok || v.Signature != "p:rule-1|payments-api" || v.Verdict != "noise" || v.Label != "Error rate > 5%" || v.Kind != "problem" || v.Service != "payments-api" || v.By != "op@example.test" || v.At != at.UnixNano() {
		t.Fatalf("satır → karar: %+v ok=%v", v, ok)
	}
	// Silinmiş (ad boş), tanınmayan karar, bozuk gövde, geçersiz imza → atlanır.
	for name, row := range map[string][2]string{
		"mezar taşı":       {"", q},
		"bilinmeyen karar": {"maybe", q},
		"bozuk JSON":       {"real", `{"signature":`},
		"geçersiz imza":    {"real", `{"signature":"x:1"}`},
	} {
		if _, ok := problemVerdictFromRow(id, row[0], row[1], at); ok {
			t.Errorf("%s: karar sayılmamalı", name)
		}
	}
}

// v0.10.1024 — kod incelemesi: viewer genel POST /api/views ucuyla
// page="problem-verdict" satırı yazıp ekip kararı taklit edebiliyordu. Okuma
// artık yalnız SİSTEM satırına güvenir: kimlik = "pv:" + gövdedeki imza.
// Sahte satır (rastgele kimlik / başka imzanın kimliği) karar sayılmaz.
func TestProblemVerdictFromRowRejectsForgedID(t *testing.T) {
	at := time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	q := `{"signature":"e:9f8a7c6d5e4b3a21","label":"NullPointerException","kind":"exception","service":"orders-api"}`
	cases := []struct {
		name string
		id   string
		ok   bool
	}{
		{"sistem satırı (pv:<imza>)", "pv:e:9f8a7c6d5e4b3a21", true},
		{"başka imzanın kimliği", "pv:e:0000000000000000", false},
		{"önek doğru, imza eksik", "pv:", false},
		{"rastgele kimlik (genel uç, newRandID)", "a1b2c3d4e5f6", false},
		{"öneksiz imza kimliği", "e:9f8a7c6d5e4b3a21", false},
		{"boş kimlik", "", false},
		{"büyük harf önek", "PV:e:9f8a7c6d5e4b3a21", false},
		{"sonda boşluk", "pv:e:9f8a7c6d5e4b3a21 ", false},
	}
	for _, c := range cases {
		v, ok := problemVerdictFromRow(c.id, "noise", q, at)
		if ok != c.ok {
			t.Errorf("%s (id=%q): ok=%v, %v beklenir", c.name, c.id, ok, c.ok)
			continue
		}
		if !ok && v != (ProblemVerdict{}) {
			t.Errorf("%s: reddedilen satır sıfır değer dönmeli, %+v", c.name, v)
		}
		if ok && (v.Signature != "e:9f8a7c6d5e4b3a21" || v.Verdict != "noise" || v.Service != "orders-api") {
			t.Errorf("%s: kabul edilen satır yanlış okundu: %+v", c.name, v)
		}
	}
}

// v0.10.1024 — ListProblemVerdicts SQL'i yalnız sistem satırlarını okur
// (kişiye ait ya da rastgele kimlikli satır LIMIT penceresine girmez) ve
// ListProblemVerdicts bu SQL'i kullanır.
func TestProblemVerdictListSQLOnlySystemRows(t *testing.T) {
	for _, want := range []string{
		"SELECT id, name, query_string, created_at",
		"page = ?",
		"owner_id = ''",
		"startsWith(id, 'pv:')",
		"name != ''",
		"LIMIT ?",
		"SETTINGS max_execution_time",
	} {
		if !strings.Contains(problemVerdictListSQL, want) {
			t.Errorf("problemVerdictListSQL %q taşımalı:\n%s", want, problemVerdictListSQL)
		}
	}
	src, err := os.ReadFile("problem_verdict.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "s.conn.Query(ctx, problemVerdictListSQL, ProblemVerdictPage, ProblemVerdictMax)") {
		t.Error("ListProblemVerdicts problemVerdictListSQL'i kullanmalı")
	}
	if !strings.Contains(string(src), "problemVerdictFromRow(id, name, query, createdAt)") {
		t.Error("ListProblemVerdicts satır kimliğini codec'e geçirmeli")
	}
}

// v0.10.1024 — sistem sayfası defteri: genel /api/views ucu bunları yazamaz /
// silemez. Kişisel sayfalar (kendi uçları sahibin owner_id'siyle yazar) BİLEREK
// defterde değil.
func TestIsSystemSavedViewPage(t *testing.T) {
	cases := []struct {
		page string
		want bool
	}{
		{ProblemVerdictPage, true},
		{"problem-verdict", true},
		{"Problem-Verdict", false}, // okuyucu tam eşitlikle süzer; bu satır karar sayılmaz
		{"", false},
		{"traces", false},
		{"logs", false},
		{"problems", false},
		{"inbox", false},
		{"alert-template", false},
		{"dashboard-star", false},
		{"ai-chat", false},
		{"promql-history", false},
		{"table:traces", false},
	}
	for _, c := range cases {
		if got := IsSystemSavedViewPage(c.page); got != c.want {
			t.Errorf("IsSystemSavedViewPage(%q) = %v, %v beklenir", c.page, got, c.want)
		}
	}
}

// v0.10.1016 — politika: boş / bozuk blob susturmayı AÇMAZ (alarm kaybettirmez).
func TestParseProblemVerdictPolicy(t *testing.T) {
	for name, raw := range map[string]string{"boş": "", "bozuk": "{", "yanlış tür": `{"muteNotifications":"yes"}`, "kapalı": `{"muteNotifications":false}`} {
		if parseProblemVerdictPolicy([]byte(raw)).MuteNotifications {
			t.Errorf("%s blob susturmayı açmamalı", name)
		}
	}
	p := parseProblemVerdictPolicy([]byte(`{"muteNotifications":true,"updatedBy":"a@b","updatedAt":7}`))
	if !p.MuteNotifications || p.UpdatedBy != "a@b" || p.UpdatedAt != 7 {
		t.Fatalf("açık politika okunmalı: %+v", p)
	}
}
