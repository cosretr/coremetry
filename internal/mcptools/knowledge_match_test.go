package mcptools

// Log Search Faz 6 dilim 6.4 — MatchRunbooks: search_knowledge'ın runbook
// sıralamasıyla aynı; RAG belgesi yok.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMatchRunbooks(t *testing.T) {
	books := []chstore.Runbook{
		{ID: "heap", Title: "JVM heap tükenmesi", Enabled: true, Steps: []chstore.RunbookStep{{Title: "OutOfMemoryError görürsen heap dump al"}}},
		{ID: "pool", Title: "Bağlantı havuzu", Enabled: true, Description: "HikariPool connection timeout"},
		{ID: "off", Title: "OutOfMemoryError eski", Enabled: false},
	}
	cases := []struct {
		name    string
		query   string
		min     float64
		max     int
		wantIDs []string
	}{
		{"exception adı", "OutOfMemoryError heap", 0.25, 3, []string{"heap"}},
		{"havuz", "HikariPool connection", 0.25, 3, []string{"pool"}},
		{"eşik", "OutOfMemoryError alpha beta gamma delta epsilon", 0.9, 3, nil},
		{"terim yok", "a b", 0, 3, nil},
		{"tavan", "OutOfMemoryError HikariPool", 0, 1, []string{"heap"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MatchRunbooks(books, c.query, c.min, c.max)
			if len(got) != len(c.wantIDs) {
				t.Fatalf("got %+v want %v", got, c.wantIDs)
			}
			for i, id := range c.wantIDs {
				if got[i].ID != id || got[i].Href != "/runbook?id="+id {
					t.Errorf("[%d] %+v", i, got[i])
				}
			}
		})
	}
}
