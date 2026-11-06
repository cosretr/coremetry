package api

// logs_sort_test.go — v0.10.1833 (Log Search Faz 1 dilim 1.10): /api/logs
// `sort` — bozuk parametre ve sıralanamaz alan 400 {error, field} (500
// değil); önbellek anahtarı sıralama SIRASINI taşır, sıralamasız anahtar
// eskisiyle aynı.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestApplyLogsSortParam(t *testing.T) {
	for _, c := range []struct {
		raw   string
		ok    bool
		field string
	}{
		{"", true, ""},
		{"service:desc,level:asc", true, ""},
		{"a,b,c,d", false, ""},
		{"service:sideways", false, "service"},
	} {
		w := httptest.NewRecorder()
		var f logstore.Filter
		got := applyLogsSortParam(w, url.Values{"sort": {c.raw}}, &f)
		if got != c.ok {
			t.Errorf("%q: ok=%v", c.raw, got)
			continue
		}
		if !c.ok {
			if w.Code != 400 {
				t.Errorf("%q: %d", c.raw, w.Code)
			}
			var b logQueryErrorBody
			_ = json.Unmarshal(w.Body.Bytes(), &b)
			if b.Error == "" || b.Field != c.field || b.Stage != "precheck" {
				t.Errorf("%q: gövde %+v", c.raw, b)
			}
		}
	}
}

func TestWriteBadQueryErrSortError(t *testing.T) {
	// Arka uçtan dönen sıralanamaz alan (ES text / CH öznitelik) → 400 + field.
	err := fmt.Errorf("search: %w", &logstore.SortError{Field: "message", Msg: "“message” alanına göre sıralanamaz.", Reason: "Metin (text) alanı; keyword alt alanı yok."})
	w := httptest.NewRecorder()
	if !writeBadQueryErr(w, httptest.NewRequest("GET", "/api/logs", nil), err) {
		t.Fatal("yakalanmalı")
	}
	var b logQueryErrorBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 400 || b.Field != "message" || b.Detail == "" || b.Pos != nil {
		t.Errorf("%d %+v", w.Code, b)
	}
}

func TestLogsSortKey(t *testing.T) {
	base := logstore.Filter{Service: "orders-api", Limit: 500}
	if logsSortKey(base) != "" {
		t.Error("sıralamasız anahtar eskisiyle aynı kalmalı")
	}
	a, b := base, base
	a.Sort = []logstore.SortKey{{Field: "service"}, {Field: "level", Desc: true}}
	b.Sort = []logstore.SortKey{{Field: "level", Desc: true}, {Field: "service"}}
	if logsSearchKey(a, "x", "y") == logsSearchKey(b, "x", "y") || logsSearchKey(a, "x", "y") == logsSearchKey(base, "x", "y") {
		t.Error("anahtar sıralamayı (sırasıyla) taşımalı")
	}
}
