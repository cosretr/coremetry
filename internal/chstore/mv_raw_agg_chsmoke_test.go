//go:build chsmoke

package chstore

import (
	"context"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"
)

// v0.10.2402 — GERÇEK ClickHouse'ta ham sorgu ↔ MV eşitliği. Taze şema
// (smokeStore → New) iki MV'yi kurar; tohum satırlar INSERT anında MV'ye
// akar. Aynı 5 dk hizalı pencerede ham yedek sorgu ile MV okuması aynı
// satırları dönmeli. Çalıştırma: chsmoke_test.go başlığı.

func TestCHSmokeServiceMetaComboMVMatchesRaw(t *testing.T) {
	st := smokeStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	type sp struct {
		svc            string
		at             time.Duration
		rk, rv, ak, av []string
	}
	seed := []sp{
		{"svc-a", 1 * time.Minute, []string{"ug-team"}, []string{"team-one"}, nil, nil},
		{"svc-a", 7 * time.Minute, []string{"ug-team", "k8s.namespace.name"}, []string{"team-one", "ns-a"}, nil, nil},
		{"svc-a", 20 * time.Minute, nil, nil, []string{"ug_team", "k8s.deployment.name"}, []string{"team-two", "dep-a"}},
		{"svc-b", 33 * time.Minute, []string{"sy-team", "service.namespace"}, []string{"sre-x", ""}, []string{"k8s.namespace.name"}, []string{"ns-b"}},
		{"svc-b", 58 * time.Minute, []string{"k8s.statefulset.name"}, []string{"sts-b"}, []string{"sy_team"}, []string{"sre-y"}},
		{"svc-c", 40 * time.Minute, []string{"unrelated"}, []string{"x"}, nil, nil}, // guard dışı
	}
	for i, s := range seed {
		if err := st.conn.Exec(ctx, `INSERT INTO spans (trace_id, span_id, service_name, time, res_keys, res_values, attr_keys, attr_values) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"t-meta", "s"+string(rune('a'+i)), s.svc, base.Add(s.at), nz(s.rk), nz(s.rv), nz(s.ak), nz(s.av)); err != nil {
			t.Fatalf("tohum span: %v", err)
		}
	}
	from, to := base, base.Add(time.Hour)
	raw := metaCombos(t, ctx, st, deriveMetadataAllSQL, from, to)
	mv := metaCombos(t, ctx, st, deriveMetadataMVSQL, from, to)
	if len(raw) == 0 || !reflect.DeepEqual(raw, mv) {
		t.Fatalf("MV ham sorguyla aynı kombo sayımını vermeli:\n raw=%v\n  mv=%v", raw, mv)
	}
}

func metaCombos(t *testing.T, ctx context.Context, st *Store, q string, from, to time.Time) []metaComboRow {
	t.Helper()
	rows, err := st.conn.Query(ctx, q, from, to)
	if err != nil {
		t.Fatalf("sorgu: %v", err)
	}
	defer rows.Close()
	var out []metaComboRow
	for rows.Next() {
		var r metaComboRow
		if err := rows.Scan(&r.Svc, &r.UG, &r.SY, &r.NS, &r.Dep, &r.C); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Svc+out[i].UG+out[i].SY+out[i].NS+out[i].Dep < out[j].Svc+out[j].UG+out[j].SY+out[j].NS+out[j].Dep
	})
	return out
}

func TestCHSmokeDBReceiverInstMVMatchesRaw(t *testing.T) {
	st := smokeStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	type mp struct {
		metric string
		at     time.Duration
		ak, av []string
		rk, rv []string
	}
	seed := []mp{
		{"oracledb.sessions", 2 * time.Minute, []string{"oracledb.instance.name"}, []string{"db-one"}, nil, nil},
		{"oracledb.sessions", 9 * time.Minute, []string{"oracledb.instance.name"}, []string{"db-one"}, nil, nil},
		{"oracledb.cpu", 15 * time.Minute, []string{"instance"}, []string{"db-two"}, nil, nil},
		{"oracledb.cpu", 16 * time.Minute, []string{"oracledb.instance.name", "instance"}, []string{"", "db-two"}, nil, nil},
		{"oracledb.cpu", 30 * time.Minute, nil, nil, []string{"service.name"}, []string{"db-three"}},
		{"oracledb.cpu", 31 * time.Minute, nil, nil, nil, nil}, // boş instance → düşer
		{"postgresql.backends", 12 * time.Minute, []string{"postgresql.instance.name"}, []string{"pg-one"}, nil, nil},
		{"postgresql.backends", 13 * time.Minute, []string{"server.address"}, []string{"pg-two"}, nil, nil},
		{"jvm.memory", 14 * time.Minute, []string{"instance"}, []string{"not-a-db"}, nil, nil},
	}
	for _, m := range seed {
		if err := st.conn.Exec(ctx, `INSERT INTO metric_points (metric, service_name, time, value, attr_keys, attr_values, res_keys, res_values) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.metric, "receiver", base.Add(m.at), 1.0, nz(m.ak), nz(m.av), nz(m.rk), nz(m.rv)); err != nil {
			t.Fatalf("tohum metrik: %v", err)
		}
	}
	from, to := base, base.Add(time.Hour)
	for _, p := range dbReceiverPrefixes[:2] {
		raw := recvInsts(t, ctx, st, dbReceiverInstRawSQL, []any{p.metric + "instance.name", from, to, p.metric})
		mv := recvInsts(t, ctx, st, dbReceiverInstMVSQL, []any{p.metric, from, to})
		if len(raw) == 0 || !reflect.DeepEqual(raw, mv) {
			t.Fatalf("%s: MV ham sorguyla aynı (instance, n) listesini vermeli:\n raw=%v\n  mv=%v", p.metric, raw, mv)
		}
	}
}

func recvInsts(t *testing.T, ctx context.Context, st *Store, q string, args []any) []string {
	t.Helper()
	rows, err := st.conn.Query(ctx, q, args...)
	if err != nil {
		t.Fatalf("sorgu: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var inst string
		var n uint64
		if err := rows.Scan(&inst, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, inst+"="+strconv.FormatUint(n, 10))
	}
	return out
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
