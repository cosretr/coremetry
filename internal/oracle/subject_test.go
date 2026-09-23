package oracle

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.896 (Aşama 3 dilim C) — özne çözücü: ① bu tikin trace çoğunluğu,
// ② öğrenilmiş harita (≥3 teyit, ≥%70, TTL, canlılık), ③ bilinmiyor; harita
// kalıcı (state), aday id önceliği haritada olmayan op'lara, TTL/tavan budaması.
func TestSubjectResolverOrder(t *testing.T) {
	now := time.Date(2026, 9, 23, 13, 0, 0, 0, time.UTC)
	state := &fakeState{kv: map[string][]byte{}}
	facts := map[string]chstore.TraceFact{"t1": {Service: "loan-svc"}, "t2": {Service: "loan-svc"}, "t3": {Service: "loan-svc"}, "t4": {Service: "crm-svc"}}
	var looked [][]string
	lookup := func(_ context.Context, ids []string, _, _ time.Time) (map[string]chstore.TraceFact, error) {
		looked = append(looked, ids)
		out := map[string]chstore.TraceFact{}
		for _, id := range ids {
			if f, ok := facts[id]; ok {
				out[id] = f
			}
		}
		return out, nil
	}
	alive := map[string]bool{"loan-svc": true}
	aliveFn := func(context.Context, time.Duration) ([]string, error) {
		out := []string{}
		for s := range alive {
			out = append(out, s)
		}
		return out, nil
	}
	r := NewSubjectResolver(state, lookup, aliveFn)
	r.now = func() time.Time { return now }
	src := SourceConfig{ID: "o-1", Name: "oracle-prod"}
	rows := []chstore.OracleErrorRow{
		{OperationCode: "OP_A", TraceID: "t1"}, {OperationCode: "OP_A", TraceID: "t2"}, {OperationCode: "OP_A", TraceID: "t3"},
		{OperationCode: "OP_B", TraceID: "t4"}, {OperationCode: "OP_C"}, {OperationCode: "OP_A", TraceID: "missing"},
	}
	res := r.Observe(context.Background(), src, rows, now.Add(-5*time.Minute), now)
	if res.Rows != 6 || res.WithTrace != 5 || res.Looked != 5 || res.Found != 4 || res.Learned != 2 {
		t.Fatalf("observe: %+v", res)
	}
	// Aday sırası: önce her op'tan bir id (t1 OP_A, t4 OP_B), sonra kalanlar.
	if looked[0][0] != "t1" || looked[0][1] != "t4" {
		t.Fatalf("aday önceliği: %v", looked[0])
	}
	// ① trace'ten (3/3).
	if got := r.Resolve(context.Background(), "o-1", []string{"OP_A", "E", "C", "-"}); got.Service != "loan-svc" || got.Source != "trace" || got.Note != "trace'ten (3/3)" {
		t.Fatalf("trace'ten: %+v", got)
	}
	// OP_C: bu tikte trace yok, harita yok → bilinmiyor.
	if got := r.Resolve(context.Background(), "o-1", []string{"OP_C", "E", "C", "-"}); got.Service != "" || got.Note == "" {
		t.Fatalf("bilinmiyor: %+v", got)
	}
	// Harita kalıcı: OP_A 3 teyit → onaylı; yeni çözücü state'ten yükler ve
	// trace'siz tikte ② öğrenilmiş verir; OP_B 1 teyit → onaysız.
	r2 := NewSubjectResolver(state, lookup, aliveFn)
	r2.now = func() time.Time { return now.Add(time.Hour) }
	r2.Observe(context.Background(), src, []chstore.OracleErrorRow{{OperationCode: "OP_A"}, {OperationCode: "OP_B"}}, now, now.Add(time.Hour))
	if got := r2.Resolve(context.Background(), "o-1", []string{"OP_A", "E", "C", "-"}); got.Service != "loan-svc" || got.Source != "learned" {
		t.Fatalf("öğrenilmiş: %+v", got)
	}
	if got := r2.Resolve(context.Background(), "o-1", []string{"OP_B", "E", "C", "-"}); got.Service != "" {
		t.Fatalf("onaysız girdi özne vermemeli: %+v", got)
	}
	// Canlı değilse öğrenilmiş servis yazılmaz.
	delete(alive, "loan-svc")
	r3 := NewSubjectResolver(state, lookup, aliveFn)
	r3.now = func() time.Time { return now.Add(2 * time.Hour) }
	if got := r3.Resolve(context.Background(), "o-1", []string{"OP_A", "E", "C", "-"}); got.Service != "" || got.Note == "" {
		t.Fatalf("ölü servis: %+v", got)
	}
	// TTL: 31 gün sonra girdi düşer.
	r4 := NewSubjectResolver(state, lookup, aliveFn)
	r4.now = func() time.Time { return now.Add(31 * 24 * time.Hour) }
	r4.Observe(context.Background(), src, nil, now, now)
	if len(r4.Learned(context.Background(), "o-1").Entries) != 0 {
		t.Fatal("TTL budaması")
	}
	// Farklı servis tek tikte ≥3 oy ve ≥%70 ise girdi döner.
	r5 := NewSubjectResolver(&fakeState{kv: map[string][]byte{}}, lookup, aliveFn)
	r5.now = func() time.Time { return now }
	r5.Observe(context.Background(), src, rows[:3], now.Add(-5*time.Minute), now) // OP_A → loan-svc 3
	facts["t1"], facts["t2"], facts["t3"] = chstore.TraceFact{Service: "new-svc"}, chstore.TraceFact{Service: "new-svc"}, chstore.TraceFact{Service: "new-svc"}
	r5.Observe(context.Background(), src, rows[:3], now.Add(-5*time.Minute), now)
	if e := r5.Learned(context.Background(), "o-1").Entries["OP_A"]; e == nil || e.Service != "new-svc" {
		t.Fatalf("servis dönmeli: %+v", e)
	}
	r5.Reset(context.Background(), "o-1")
	if len(r5.Learned(context.Background(), "o-1").Entries) != 0 {
		t.Fatal("reset")
	}
}

func TestPruneLearnedCap(t *testing.T) {
	now := time.Now()
	m := &LearnedMap{V: 1, Entries: map[string]*LearnedEntry{}}
	for i := 0; i < learnedMaxEntries+5; i++ {
		m.Entries[itoa(i)+"x"] = &LearnedEntry{Service: "s", Hits: 3, Total: 3, LastConfirmed: now.Add(-time.Duration(i) * time.Minute).UnixNano()}
	}
	if d := pruneLearned(m, now); d != 5 || len(m.Entries) != learnedMaxEntries {
		t.Fatalf("tavan: dropped=%d n=%d", d, len(m.Entries))
	}
}

// v0.10.899 — Observe bu poll'un trace → exception tipini de tutar (qualifier).
func TestSubjectExTypeFor(t *testing.T) {
	r := NewSubjectResolver(&fakeState{kv: map[string][]byte{}}, func(_ context.Context, ids []string, _, _ time.Time) (map[string]chstore.TraceFact, error) {
		return map[string]chstore.TraceFact{"t1": {Service: "s", ExType: "java.sql.SQLTimeoutException"}, "t2": {Service: "s"}}, nil
	}, nil)
	src := SourceConfig{ID: "o-1", Name: "o"}
	r.Observe(context.Background(), src, []chstore.OracleErrorRow{{OperationCode: "OP", TraceID: "t1"}, {OperationCode: "OP", TraceID: "t2"}}, time.Now().Add(-time.Minute), time.Now())
	ex := r.ExTypeFor("o-1")
	if ex("t1") != "java.sql.SQLTimeoutException" || ex("t2") != "" || ex("yok") != "" || r.ExTypeFor("o-2")("t1") != "" {
		t.Fatal("ExTypeFor")
	}
}

// v0.10.900 (inceleme) — oy trace başına (aynı trace'in 3 satırı = 1 oy); karışık
// tikte mevcut servisin oyları Hits'e eklenir (girdi haksız düşmez); API'den
// sıfırlanan blob bir sonraki Observe'da bellekteki kopyayı ezer.
func TestSubjectVotesPerTraceAndMixedTick(t *testing.T) {
	now := time.Date(2026, 9, 23, 13, 0, 0, 0, time.UTC)
	state := &fakeState{kv: map[string][]byte{}}
	facts := map[string]chstore.TraceFact{"t1": {Service: "loan"}, "t2": {Service: "loan"}, "t3": {Service: "loan"}, "t4": {Service: "crm"}, "t5": {Service: "crm"}}
	lookup := func(_ context.Context, ids []string, _, _ time.Time) (map[string]chstore.TraceFact, error) {
		out := map[string]chstore.TraceFact{}
		for _, id := range ids {
			if f, ok := facts[id]; ok {
				out[id] = f
			}
		}
		return out, nil
	}
	r := NewSubjectResolver(state, lookup, nil)
	r.now = func() time.Time { return now }
	src := SourceConfig{ID: "o-1", Name: "o"}
	// Aynı trace'in 3 satırı → tek oy.
	r.Observe(context.Background(), src, []chstore.OracleErrorRow{{OperationCode: "OP", TraceID: "t1"}, {OperationCode: "OP", TraceID: "t1"}, {OperationCode: "OP", TraceID: "t1"}}, now, now)
	if e := r.Learned(context.Background(), "o-1").Entries["OP"]; e == nil || e.Hits != 1 || e.Total != 1 || e.Confirmed(now) {
		t.Fatalf("trace başına oy: %+v", e)
	}
	// İki tik daha loan → 3/3 onaylı.
	r.Observe(context.Background(), src, []chstore.OracleErrorRow{{OperationCode: "OP", TraceID: "t2"}}, now, now)
	r.Observe(context.Background(), src, []chstore.OracleErrorRow{{OperationCode: "OP", TraceID: "t3"}}, now, now)
	if e := r.Learned(context.Background(), "o-1").Entries["OP"]; e == nil || !e.Confirmed(now) {
		t.Fatalf("onay: %+v", e)
	}
	// Karışık tik: loan 2 (t1, t2 yeniden) vs crm 1 — crm eşiği geçmez; loan oyları Hits'e girer (5/6 onaylı kalır).
	r.Observe(context.Background(), src, []chstore.OracleErrorRow{{OperationCode: "OP", TraceID: "t1"}, {OperationCode: "OP", TraceID: "t2"}, {OperationCode: "OP", TraceID: "t4"}}, now, now)
	e := r.Learned(context.Background(), "o-1").Entries["OP"]
	if e == nil || e.Service != "loan" || e.Hits != 5 || e.Total != 6 || !e.Confirmed(now) {
		t.Fatalf("karışık tik: %+v", e)
	}
	// Karışık tikte rakip çoğunlukta ama eşik altı (crm 2 vs loan 1): mevcut servis kalır, Hits 6/9.
	r.Observe(context.Background(), src, []chstore.OracleErrorRow{{OperationCode: "OP", TraceID: "t3"}, {OperationCode: "OP", TraceID: "t4"}, {OperationCode: "OP", TraceID: "t5"}}, now, now)
	if e := r.Learned(context.Background(), "o-1").Entries["OP"]; e == nil || e.Service != "loan" || e.Hits != 6 || e.Total != 9 {
		t.Fatalf("rakip eşik altı: %+v", e)
	}
	// API sıfırlaması (blob boş) → sonraki Observe bellekteki haritayı ezer.
	state.kv[learnedKey("o-1")] = []byte(`{"v":1,"entries":{}}`)
	r.Observe(context.Background(), src, nil, now, now)
	if len(r.Learned(context.Background(), "o-1").Entries) != 0 {
		t.Fatal("sıfırlama görünmeli")
	}
}
