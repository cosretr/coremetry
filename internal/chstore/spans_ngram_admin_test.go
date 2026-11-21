package chstore

import (
	"os"
	"strings"
	"testing"
)

// Boot ALTER (store.go) ile kartın ADD INDEX'i aynı tanım: iki yol ayrışırsa
// dış Distributed kurulum başka bir indeks alırdı.
func TestSpansNgramAddSQLMatchesBoot(t *testing.T) {
	single := spansNgramAddSQL("spans", "")
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "`"+single+"`") {
		t.Fatalf("store.go boot ALTER'ı kartla aynı değil; beklenen:\n%s", single)
	}
	if got := spansNgramAddSQL("spans_local", "c1"); got != "ALTER TABLE spans_local ON CLUSTER c1 ADD INDEX IF NOT EXISTS idx_db_stmt_ngram db_statement TYPE ngrambf_v1(3, 8192, 3, 0) GRANULARITY 1" {
		t.Fatalf("cluster sql: %s", got)
	}
}

func TestSpansNgramPending(t *testing.T) {
	j := &SkipMatJobView{}
	cases := []struct {
		state string
		job   *SkipMatJobView
		want  bool
	}{
		{"missing", nil, true},
		{"partial", j, true},
		{"ok", nil, true}, // eski partition kararı verilmedi
		{"ok", j, false},  // karar verildi (iş ya da declined)
		{"unknown", j, true},
	}
	for _, c := range cases {
		if got := spansNgramPending(c.state, c.job); got != c.want {
			t.Fatalf("%s/%v: %v", c.state, c.job != nil, got)
		}
	}
}
