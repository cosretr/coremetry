package logstore

// query_error_test.go — v0.10.1818 (Log Search Faz 1 dilim 1.2).
//
// (1) ES sözdizimi reddi yapısal: gerekçe cümlesi, yankılanan sorgu,
//     satır/sütun → kullanıcı sorgusundaki karakter konumu. Önceden düz dize
//     → /api/logs 500 + ham metin (audit §2 yan bulgu).
// (2) Regresyon: ES Search `limit > 1000` 1000'e kırpılır. Önceki semptom:
//     limit=2000 → 100 satır (CH backend'i aynı istekte 1000 döndürüyordu).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Gerçek ES 8.x zarfı biçimi (alanlar kısaltılmış): üst seviye sarmalayıcı,
// root_cause'ta "Failed to parse query [...]", failed_shards[].reason.caused_by
// içinde Lucene'in konumlu cümlesi.
const esParseFailBody = `{"error":{"root_cause":[{"type":"query_shard_exception","reason":"Failed to parse query [level:error AND (foo OR bar]","index":"app-orders-api"}],` +
	`"type":"search_phase_execution_exception","reason":"all shards failed","phase":"query","grouped":true,` +
	`"failed_shards":[{"shard":0,"index":"app-orders-api","reason":{"type":"query_shard_exception","reason":"Failed to parse query [level:error AND (foo OR bar]",` +
	`"caused_by":{"type":"parse_exception","reason":"Cannot parse 'level:error AND (foo OR bar': Encountered \"<EOF>\" at line 1, column 26.\nWas expecting one of:\n    <AND> ...\n    <OR> ...",` +
	`"caused_by":{"type":"parse_exception","reason":"Encountered \"<EOF>\" at line 1, column 26."}}}}]},"status":400}`

func TestNewBadQueryError(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		reason    string
		echo      string
		line, col int
	}{
		{"failed_shards caused_by konumlu", esParseFailBody,
			`Cannot parse 'level:error AND (foo OR bar': Encountered "<EOF>" at line 1, column 26.`,
			"level:error AND (foo OR bar", 1, 26},
		{"yalnız root_cause (konumsuz)", `{"error":{"root_cause":[{"type":"query_shard_exception","reason":"Failed to parse query [level:(error OR]"}],` +
			`"type":"search_phase_execution_exception","reason":"all shards failed"},"status":400}`,
			"Failed to parse query [level:(error OR]", "level:(error OR", 0, 0},
		{"JSON değil", `<html>bad</html>`, "", "", 0, 0},
	}
	for _, c := range cases {
		e := NewBadQueryError("msg", []byte(c.body))
		if e.Reason != c.reason || e.Echo != c.echo || e.Line != c.line || e.Col != c.col {
			t.Errorf("%s: got reason=%q echo=%q line=%d col=%d", c.name, e.Reason, e.Echo, e.Line, e.Col)
		}
		if !errors.Is(e, ErrBadQuery) || e.Error() != "msg" {
			t.Errorf("%s: Is/Error sözleşmesi bozuk", c.name)
		}
	}
}

func TestQueryErrorPos(t *testing.T) {
	be := func(echo string, line, col int) error {
		return fmt.Errorf("sarılı: %w", &BadQueryError{msg: "x", Echo: echo, Line: line, Col: col})
	}
	cases := []struct {
		name  string
		err   error
		query string
		pos   int
		ok    bool
	}{
		{"ASCII sütun → 0 tabanlı", be("a AND (b", 1, 9), "a AND (b", 8, true},
		{"rune sayımı (Türkçe karakter)", be(`mesaj:"ödeme" AND (`, 1, 20), `mesaj:"ödeme" AND (`, 19, true},
		{"ikinci satır", be("a\nb (", 2, 4), "a\nb (", 5, true},
		{"sütun satır sonunu aşar → kırpılır", be("ab", 1, 9), "ab", 2, true},
		{"yankı farklı (kısayol açılımı) → konum yok", be("(log.level:error OR level:error) AND (", 1, 39), "level:error AND (", 0, false},
		{"satır/sütun yok", be("a (", 0, 0), "a (", 0, false},
		{"BadQueryError değil", errors.New("boom"), "a", 0, false},
	}
	for _, c := range cases {
		pos, ok := QueryErrorPos(c.err, c.query)
		if pos != c.pos || ok != c.ok {
			t.Errorf("%s: got (%d,%v) want (%d,%v)", c.name, pos, ok, c.pos, c.ok)
		}
	}
}

func TestESSearchLimit(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{-5, 100}, {0, 100}, {1, 1}, {100, 100}, {500, 500}, {1000, 1000}, {1001, 1000}, {2000, 1000}, {1 << 30, 1000},
	} {
		if got := esSearchLimit(c.in); got != c.want {
			t.Errorf("esSearchLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Uçtan uca (stub ES): 400 parse reddi BadQueryError olarak yüzeye çıkar ve
// limit=2000 ES'e size:1000 olarak gider.
func TestESSearchBadQueryStructuredAndLimitClamp(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{})
	st.set(func(s *esStubServer) { s.searchStatus, s.searchBody = 400, esParseFailBody })
	now := time.Now()
	_, err := es.Search(context.Background(), Filter{Service: "orders-api", Search: "level:error AND (foo OR bar", From: now.Add(-time.Hour), To: now})
	var be *BadQueryError
	if !errors.As(err, &be) || !errors.Is(err, ErrBadQuery) {
		t.Fatalf("BadQueryError beklendi: %v", err)
	}
	if be.Line != 1 || be.Col != 26 {
		t.Fatalf("konum: %d:%d", be.Line, be.Col)
	}

	st.set(func(s *esStubServer) {
		s.searchStatus, s.searchBody = 200, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`
	})
	if _, err := es.Search(context.Background(), Filter{Service: "orders-api", Limit: 2000, From: now.Add(-time.Hour), To: now}); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	raw := st.lastSearchBody
	st.mu.Unlock()
	var body struct {
		Size int `json:"size"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("gövde: %v (%s)", err, raw)
	}
	if body.Size != 1000 {
		t.Fatalf("limit=2000 → size %d, 1000 beklendi (v0.10.1818 regresyonu: eskiden 100)", body.Size)
	}
}
