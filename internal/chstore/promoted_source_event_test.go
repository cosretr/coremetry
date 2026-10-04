// promoted_source_event_test.go — v0.10.1106: terfi Problem'inin detayı kaynak
// olayın "Desen sayısı" grafiğini çizer; kaynak olay TEK sınırlı okumayla gelir.
//
// Pinler: okuma sınırlı (FINAL + PK eşitliği + en yeni sürüm + LIMIT 1 +
// max_execution_time), yalnız grafiğin kolonları (sample / oranlar / bölüm
// kolonları yok), verified_ratio yalnız probe true iken; satır yok → (nil, nil),
// boş kimlik → okuma yok, hata → (nil, err).
//
// Mutasyon kontrolleri: LIMIT / SETTINGS / FINAL / ORDER BY silinirse sınır
// alt testi; probe yokken verified_ratio okunursa arite alt testi kırılır.
package chstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type sourceEventFakeConn struct {
	driver.Conn
	rows    [][]any
	err     error
	queries *[]string
	args    *[][]any
}

func (c sourceEventFakeConn) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	*c.queries = append(*c.queries, q)
	*c.args = append(*c.args, args)
	if c.err != nil {
		return nil, c.err
	}
	return &promotedFakeRows{rows: c.rows}, nil
}

func TestPromotedSourceEventSQLBounded(t *testing.T) {
	for _, vr := range []bool{false, true} {
		q := promotedSourceEventSQL(vr)
		for _, must := range []string{
			"FROM anomaly_events FINAL",
			"WHERE id = ?",
			"ORDER BY last_seen DESC",
			"LIMIT 1",
			"SETTINGS max_execution_time = 2",
		} {
			if !strings.Contains(q, must) {
				t.Errorf("vr=%v: okuma %q içermiyor:\n%s", vr, must, q)
			}
		}
		for _, never := range []string{"sample", "peak_ratio", "current_ratio", "current_count", "episode_count", "first_started_at"} {
			if strings.Contains(q, never) {
				t.Errorf("vr=%v: okuma grafiğin kullanmadığı %q kolonunu çekiyor:\n%s", vr, never, q)
			}
		}
		if strings.Count(q, "SELECT") != 1 {
			t.Errorf("vr=%v: alt sorgu var:\n%s", vr, q)
		}
		want := 7
		if vr {
			want = 8
		}
		if got := sqlColumnCount(selectColumnList(t, q)); got != want {
			t.Errorf("vr=%v: %d kolon, want %d:\n%s", vr, got, want, q)
		}
		if strings.Contains(q, "verified_ratio") != vr {
			t.Errorf("vr=%v: verified_ratio yalnız probe true iken okunmalı:\n%s", vr, q)
		}
	}
}

func TestGetPromotedSourceEvent(t *testing.T) {
	ctx := context.Background()
	fp := FingerprintAnomaly("log_pattern", "oracle-tns", "svc-orders")
	row := []any{fp, "log_pattern", "oracle-tns", "svc-orders", int64(1_000), int64(2_000), "cleared"}

	t.Run("satır var → özet, tek okuma, bind = aktif yaş + kimlik", func(t *testing.T) {
		var qs []string
		var args [][]any
		st := &Store{conn: sourceEventFakeConn{rows: [][]any{row}, queries: &qs, args: &args}}
		got, err := st.GetPromotedSourceEvent(ctx, fp)
		if err != nil {
			t.Fatal(err)
		}
		want := PromotedSourceEvent{ID: fp, Kind: "log_pattern", Pattern: "oracle-tns", Service: "svc-orders",
			StartedAt: 1_000, LastSeen: 2_000, Status: "cleared"}
		if got == nil || *got != want {
			t.Fatalf("özet = %+v, want %+v", got, want)
		}
		if len(qs) != 1 {
			t.Fatalf("%d okuma, want 1", len(qs))
		}
		if len(args[0]) != 2 || args[0][0] != int64(600) || args[0][1] != fp {
			t.Errorf("bind = %v, want [600 %s]", args[0], fp)
		}
	})
	t.Run("verified_ratio kolonu varken okunur", func(t *testing.T) {
		var qs []string
		var args [][]any
		st := &Store{conn: sourceEventFakeConn{rows: [][]any{append(append([]any{}, row...), 0.4)}, queries: &qs, args: &args}}
		st.hasAnomalyVerifiedCol.Store(true)
		got, err := st.GetPromotedSourceEvent(ctx, fp)
		if err != nil || got == nil || got.VerifiedRatio != 0.4 {
			t.Fatalf("got=%+v err=%v, want verifiedRatio 0.4", got, err)
		}
	})
	t.Run("satır yok (TTL) → (nil, nil)", func(t *testing.T) {
		var qs []string
		var args [][]any
		st := &Store{conn: sourceEventFakeConn{queries: &qs, args: &args}}
		if got, err := st.GetPromotedSourceEvent(ctx, fp); got != nil || err != nil {
			t.Errorf("got=%+v err=%v, want (nil, nil)", got, err)
		}
	})
	t.Run("boş kimlik → okuma yok", func(t *testing.T) {
		var qs []string
		var args [][]any
		st := &Store{conn: sourceEventFakeConn{rows: [][]any{row}, queries: &qs, args: &args}}
		if got, err := st.GetPromotedSourceEvent(ctx, ""); got != nil || err != nil || len(qs) != 0 {
			t.Errorf("got=%+v err=%v okuma=%d, want okuma yok", got, err, len(qs))
		}
	})
	t.Run("hata → (nil, err)", func(t *testing.T) {
		var qs []string
		var args [][]any
		st := &Store{conn: sourceEventFakeConn{err: errors.New("CH down"), queries: &qs, args: &args}}
		if got, err := st.GetPromotedSourceEvent(ctx, fp); got != nil || err == nil {
			t.Errorf("hata yutuldu: got=%+v err=%v", got, err)
		}
	})
}
