package anomaly

// log_patterns_verify_test.go — v0.10.1080 (operatör, prod ES: "Oracle TNS
// error diyor ama loglarda öyle bir şey yok, hatalı desen buluyor.").
//
// ES sayımı token-OR'dur (çıplak `tns` terimi), regex'i uygulamaz. Tetiklemek
// üzere olan adaylar örneklemle doğrulanır: r=0 bastırır, 0<r<1 cur VE tabanı
// ölçekler, r=1 dokunmaz; tik başına en çok N örnek, tek VerifyPatterns.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func patternIdx(t *testing.T, name string) int {
	t.Helper()
	for i, p := range patterns {
		if p.Name == name {
			return i
		}
	}
	t.Fatalf("desen yok: %s", name)
	return -1
}

func TestApplyLogPatternVerification(t *testing.T) {
	cases := []struct {
		name            string
		cur             uint64
		base            float64
		v               logstore.PatternVerification
		wantCur         uint64
		wantBase, wantR float64
		wantSuppress    bool
	}{
		{"örnek yok → aynen, bilinmiyor", 100, 10, logstore.PatternVerification{}, 100, 10, 0, false},
		{"r=0 → bastır", 100, 10, logstore.PatternVerification{Sampled: 13, Matched: 0}, 0, 0, 0, true},
		{"r=0.4 → ikisi de ölçeklenir", 100, 10, logstore.PatternVerification{Sampled: 50, Matched: 20}, 40, 4, 0.4, false},
		{"r=1 → aynen", 100, 10, logstore.PatternVerification{Sampled: 50, Matched: 50}, 100, 10, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur, base, r, sup := applyLogPatternVerification(c.cur, c.base, c.v)
			if cur != c.wantCur || base != c.wantBase || r != c.wantR || sup != c.wantSuppress {
				t.Fatalf("got (%d, %v, %v, %v), want (%d, %v, %v, %v)",
					cur, base, r, sup, c.wantCur, c.wantBase, c.wantR, c.wantSuppress)
			}
			// Aynı r ile ölçekleme spike oranını korur (tabanlı dalda).
			if !sup && c.base > 0 {
				before, _ := qualifyLogPattern(c.cur, c.base)
				after, _ := qualifyLogPattern(cur, base)
				if before != after {
					t.Errorf("oran ölçeklemeyle değişmemeli: %s → %s", before, after)
				}
			}
		})
	}
}

func TestPickLogPatternVerify_BudgetTopByRatio(t *testing.T) {
	cands := make([]logPatternCand, 15)
	for i := range cands {
		cands[i] = logPatternCand{idx: i, cur: 100, ratio: float64(i + 1)}
	}
	pick := pickLogPatternVerify(cands, logPatternVerifyBudget)
	if len(pick) != 10 {
		t.Fatalf("bütçe 10, seçilen %d", len(pick))
	}
	if cands[pick[0]].ratio != 15 || cands[pick[9]].ratio != 6 {
		t.Errorf("oran azalan ilk 10 bekleniyordu: %v", pick)
	}
	if got := pickLogPatternVerify(cands, 0); len(got) != 0 {
		t.Errorf("bütçe 0 → örnek yok: %v", got)
	}
}

// verifyStore — CountPatterns/VerifyPatterns'ı betikleyen sahte backend.
type verifyStore struct {
	logstore.Store
	stats    []logstore.PatternStats
	verify   func([]logstore.PatternSpec) ([]logstore.PatternVerification, error)
	calls    int
	lastPats []logstore.PatternSpec
}

func (s *verifyStore) CountPatterns(context.Context, []logstore.PatternSpec, time.Time, time.Time, time.Time) ([]logstore.PatternStats, error) {
	return s.stats, nil
}

func (s *verifyStore) VerifyPatterns(_ context.Context, pats []logstore.PatternSpec, _, _ time.Time) ([]logstore.PatternVerification, error) {
	s.calls++
	s.lastPats = pats
	return s.verify(pats)
}

func TestVerifyLogPatternCands_BudgetAndOneCall(t *testing.T) {
	cands := make([]logPatternCand, 15)
	for i := range cands {
		cands[i] = logPatternCand{idx: i % len(patterns), cur: 100, base: 10, kind: "spike", ratio: float64(i + 1)}
	}
	st := &verifyStore{verify: func(p []logstore.PatternSpec) ([]logstore.PatternVerification, error) {
		out := make([]logstore.PatternVerification, len(p))
		for i := range out {
			out[i] = logstore.PatternVerification{Sampled: 50, Matched: 50}
		}
		return out, nil
	}}
	got := verifyLogPatternCands(context.Background(), st, cands, time.Now(), time.Now(), logPatternVerifyBudget, time.Now())
	if st.calls != 1 || len(st.lastPats) != logPatternVerifyBudget {
		t.Fatalf("tek çağrı, %d desen bekleniyordu: calls=%d pats=%d", logPatternVerifyBudget, st.calls, len(st.lastPats))
	}
	verified := 0
	for _, c := range got {
		if c.verified == 1 {
			verified++
		}
	}
	if len(got) != 15 || verified != 10 {
		t.Errorf("15 aday kalmalı (10'u doğrulanmış): len=%d verified=%d", len(got), verified)
	}

	// Aday yoksa backend'e hiç gidilmez.
	st2 := &verifyStore{verify: st.verify}
	verifyLogPatternCands(context.Background(), st2, nil, time.Now(), time.Now(), logPatternVerifyBudget, time.Now())
	if st2.calls != 0 {
		t.Errorf("adaysız tikte VerifyPatterns çağrılmamalı: %d", st2.calls)
	}
}

func TestVerifyLogPatternCands_CHNilAndErrorLeaveCandsUnchanged(t *testing.T) {
	base := []logPatternCand{{idx: 0, cur: 100, base: 10, kind: "spike", ratio: 10, sample: "s"}}
	for name, fn := range map[string]func([]logstore.PatternSpec) ([]logstore.PatternVerification, error){
		"CH nil": func([]logstore.PatternSpec) ([]logstore.PatternVerification, error) { return nil, nil },
		"hata": func([]logstore.PatternSpec) ([]logstore.PatternVerification, error) {
			return nil, errors.New("es down")
		},
	} {
		cands := append([]logPatternCand(nil), base...)
		got := verifyLogPatternCands(context.Background(), &verifyStore{verify: fn}, cands, time.Now(), time.Now(), 10, time.Now())
		if len(got) != 1 || got[0] != base[0] {
			t.Errorf("%s: aday değişmemeli: %+v", name, got)
		}
	}
}

// Operatör senaryosu uçtan uca: TNS deseni token'la 60 kat "yeni", örneklemin
// hiçbiri TNS-NNNN değil → olay YOK. Yarısı doğrulanırsa sayı ölçeklenir ve
// oran + örnek doğrulanmış satırdan gelir.
func TestDetectLogPatterns_ESVerification(t *testing.T) {
	tns := patternIdx(t, "Oracle TNS errors")
	stats := make([]logstore.PatternStats, len(patterns))
	stats[tns] = logstore.PatternStats{Cur: 60, Base: 0, Service: "demo-svc", Sample: "cache warmup done, tns alias resolved"}

	cases := []struct {
		name      string
		v         logstore.PatternVerification
		wantN     int
		wantCur   uint64
		wantR     float64
		wantSampl string
	}{
		{"r=0 → bastırılır", logstore.PatternVerification{Sampled: 13}, 0, 0, 0, ""},
		{"r=0.4 → 60→24 < 30 'new' tabanı, düşer", logstore.PatternVerification{Sampled: 50, Matched: 20, Sample: "TNS-12541: no listener"}, 0, 0, 0, ""},
		{"r=0.6 → 36, yazılır", logstore.PatternVerification{Sampled: 50, Matched: 30, Sample: "TNS-12541: no listener"}, 1, 36, 0.6, "TNS-12541: no listener"},
		{"r=1 → aynen", logstore.PatternVerification{Sampled: 50, Matched: 50, Sample: "TNS-12541: no listener"}, 1, 60, 1, "TNS-12541: no listener"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &verifyStore{stats: stats, verify: func(p []logstore.PatternSpec) ([]logstore.PatternVerification, error) {
				if len(p) != 1 || p[0].Regex != `TNS-[0-9]+` {
					t.Fatalf("yalnız tetikleyecek desen örneklenmeli: %+v", p)
				}
				return []logstore.PatternVerification{c.v}, nil
			}}
			got, err := DetectLogPatterns(context.Background(), st, 5*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.wantN {
				t.Fatalf("len = %d, want %d: %+v", len(got), c.wantN, got)
			}
			if c.wantN == 1 {
				a := got[0]
				if a.CurrentCount != c.wantCur || a.VerifiedRatio != c.wantR || a.Sample != c.wantSampl || a.Kind != "new" {
					t.Errorf("got %+v", a)
				}
				if a.Ratio != float64(c.wantCur) {
					t.Errorf("'new' oranı tahmini sayı olmalı: %v", a.Ratio)
				}
			}
		})
	}
}

func TestUnverifiedLogLimiter_OncePerHour(t *testing.T) {
	l := &perKeyLimiter{every: time.Hour, last: map[string]time.Time{}}
	t0 := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	if !l.allow("Oracle TNS errors", t0) {
		t.Fatal("ilk satır basılmalı")
	}
	if l.allow("Oracle TNS errors", t0.Add(59*time.Minute)) {
		t.Error("saat dolmadan ikinci satır basılmamalı")
	}
	if !l.allow("Oracle errors (ORA-)", t0.Add(time.Minute)) {
		t.Error("anahtar desen başına")
	}
	if !l.allow("Oracle TNS errors", t0.Add(time.Hour)) {
		t.Error("saat dolunca yeniden basılmalı")
	}
}
