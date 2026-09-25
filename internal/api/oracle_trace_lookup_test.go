package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.917 — Tempo'dan gelen trace'in servisi CH kuralıyla aynı seçilir.
func TestServiceFromSpans(t *testing.T) {
	sp := func(svc, parent, status string, start int64) chstore.SpanRow {
		return chstore.SpanRow{ServiceName: svc, ParentSpanID: parent, StatusCode: status, StartTime: start}
	}
	cases := []struct {
		name  string
		spans []chstore.SpanRow
		want  string
	}{
		{"boş", nil, ""},
		{"hata yok → kök", []chstore.SpanRow{sp("child", "p", "ok", 2), sp("gw", "", "unset", 1)}, "gw"},
		{"son hata veren kazanır", []chstore.SpanRow{sp("gw", "", "error", 1), sp("a", "p", "error", 5), sp("b", "p", "error", 3)}, "a"},
		{"kök yok (orphan) → ilk span", []chstore.SpanRow{sp("x", "p1", "ok", 1), sp("y", "p2", "ok", 2)}, "x"},
	}
	for _, c := range cases {
		if got := serviceFromSpans(c.spans); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
