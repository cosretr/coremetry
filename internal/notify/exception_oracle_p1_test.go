package notify

// exception_oracle_p1_test.go — v0.10.1109 (operatör: "oracledan gelen teknik
// hatalar problems sayfasında da gözüksün … P1 tipinde gözüken oracle teknik
// hataları notifikasyon da gönderilmiş olur").
//
// Kök neden: kanal yolu (routeGroups) her gruba span grubunun tazelik kapısını
// uyguluyordu — `new` grup yalnız ilk görülmesinden sonraki 15 dk aday. Oracle
// grubunun P1'i yapışkan DEĞİL, saatlik toplamlardan doğan bir OLAY (patlama:
// son 1 sa ≥ eşik ve ≥ 3× önceki saat; ya da 4 sa'lik P1 penceresinde yeni grup
// ve son 1 sa ≥ eşik). (kaynak, kod, operasyon) grupları kalıcı olduğundan
// patlama hemen her zaman günler önce doğmuş bir grupta olur → Exceptions'ta P1
// görünen Oracle grubu hiçbir kanala gitmiyordu.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestOracleP1EventEntersChannels(t *testing.T) {
	gnow := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC) // gecikme kaydırılmış "şimdi"
	ora := func(firstAgo, lastAgo time.Duration) chstore.ExceptionGroup {
		return chstore.ExceptionGroup{
			Fingerprint: chstore.OracleGroupFingerprint("src-crm", "ORA-00001", "OP_ORDERS"),
			Type:        "ORA-00001", Message: "OP_ORDERS", Service: "svc-orders",
			Occurrences: 250_000, FirstSeen: gnow.Add(-firstAgo).UnixNano(), LastSeen: gnow.Add(-lastAgo).UnixNano(),
		}
	}
	span := chstore.ExceptionGroup{Fingerprint: "a1b2c3d4e5f60718", Type: "SyntheticTimeoutError",
		Service: "svc-orders", Occurrences: 90_000,
		FirstSeen: gnow.Add(-48 * time.Hour).UnixNano(), LastSeen: gnow.Add(-time.Minute).UnixNano()}
	p1ID := "exception-group:" + ora(0, 0).Fingerprint + ":p1"

	cases := []struct {
		name  string
		g     chstore.ExceptionGroup
		state string
		prio  string
		want  string
	}{
		{"Oracle patlaması, grup 2 gün önce doğmuş → P1 olayı kanala", ora(48*time.Hour, time.Minute), chstore.ExStateNew, "P1", p1ID},
		{"Oracle yeni grup P1, ilk görülme 40 dk önce → P1 olayı kanala", ora(40*time.Minute, time.Minute), chstore.ExStateNew, "P1", p1ID},
		{"Oracle regressed + P1 → P1 olayı (tek kimlik)", ora(48*time.Hour, time.Minute), chstore.ExStateRegressed, "P1", p1ID},
		{"Oracle P1 ama 30 dk'dır sustu → aday değil", ora(48*time.Hour, 30*time.Minute), chstore.ExStateNew, "P1", ""},
		{"Oracle P2 eski grup → aday değil (P2 kuralı aynen)", ora(48*time.Hour, time.Minute), chstore.ExStateNew, "P2", ""},
		{"Oracle P2 yeni grup ≤15 dk → eski kimlik", ora(5*time.Minute, time.Minute), chstore.ExStateNew, "P2", "exception-group:" + ora(0, 0).Fingerprint},
		{"span yapışkan P1, ilk görülme 2 gün önce → aday değil (değişmedi)", span, chstore.ExStateNew, "P1", ""},
	}
	for _, c := range cases {
		if got := exceptionChannelKey(c.g, c.state, c.prio, gnow); got != c.want {
			t.Errorf("%s: %q, istenen %q", c.name, got, c.want)
		}
	}
}

// oraHarness — routeOracleP1'in sahte kenarları: notification_log ikizi (son
// başarılı gönderim), Sustur listesi, kaynak çözümü, gönderim kaydı.
type oraHarness struct {
	t       *testing.T
	e       *ExceptionNotifier
	log     map[string]time.Time
	ignored map[string]bool
	refs    map[string]OracleGroupRef
	reads   int // lastSent + ignored okuması (CH maliyeti ikizi)
	out     []chstore.Problem
}

func newOraHarness(t *testing.T) *oraHarness {
	h := &oraHarness{t: t, log: map[string]time.Time{}, ignored: map[string]bool{}, refs: map[string]OracleGroupRef{}}
	h.restart()
	return h
}

// restart — boş defter (yeni lider / yeniden başlatma), kalıcı kayıtlar aynı.
func (h *oraHarness) restart() {
	h.e = &ExceptionNotifier{sent: map[string]int64{},
		lastSent: func(_ context.Context, id string, within time.Duration) (time.Time, error) {
			h.reads++
			if within != exOracleP1Cooldown {
				h.t.Fatalf("log penceresi soğumayla sınırlı olmalı: %v", within)
			}
			return h.log[id], nil
		},
		ignored: func(_ context.Context, id string) (bool, error) { h.reads++; return h.ignored[id], nil },
		oracleRef: func(g chstore.ExceptionGroup) (OracleGroupRef, bool) {
			r, ok := h.refs[g.Fingerprint]
			return r, ok
		},
	}
}

// tick — bir tikin Oracle P1 yolu; gönderilenler log'a (ok=1) yazılır.
func (h *oraHarness) tick(at time.Time, gs ...chstore.ExceptionGroup) []chstore.Problem {
	var tickOut []chstore.Problem
	h.e.send = func(_ context.Context, p chstore.Problem) {
		tickOut = append(tickOut, p)
		h.log[p.ID] = at
	}
	cands := make([]oracleP1Cand, 0, len(gs))
	for _, g := range gs {
		cands = append(cands, oracleP1Cand{g: g, state: chstore.ExStateNew, reason: "Teknik hata patlaması"})
	}
	h.e.routeOracleP1(context.Background(), at, cands, 0)
	h.out = append(h.out, tickOut...)
	return tickOut
}

func oraGroup(src, code string, lastHour uint64, h *oraHarness) chstore.ExceptionGroup {
	g := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint(src, code, "OP_ORDERS"),
		Type: code, Message: "OP_ORDERS", Service: "svc-orders", Occurrences: 250_000}
	if h != nil && src != "" {
		h.refs[g.Fingerprint] = OracleGroupRef{SourceID: src, SourceName: src + "-db", LastHour: lastHour}
	}
	return g
}

// Bölüm başına bir bildirim: soğuma (defter + log), restart'ta çift yok, soğuma
// sonrası yeniden; taban kimliği de defterde (aynı bölümün P2'si çalmaz);
// grubun Sustur'u P1'i susturur ve susturulmuş grup her tik CH okumaz.
func TestOracleP1EventCooldown(t *testing.T) {
	h := newOraHarness(t)
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	g := oraGroup("src-crm", "ORA-00001", 20_000, h)
	id := exceptionOracleP1ID(g.Fingerprint)
	if exceptionGroupFingerprint(id) != g.Fingerprint {
		t.Fatalf("P1 kimliğinden parmak izi geri okunmalı (Sustur / incident linki): %q", id)
	}
	if out := h.tick(now, g); len(out) != 1 || out[0].ID != id {
		t.Fatalf("ilk P1 tiki gönderilmeli: %+v", out)
	}
	if _, ok := h.e.sent[exceptionGroupID(g.Fingerprint, chstore.ExStateNew)]; !ok {
		t.Fatal("P1 gidince grubun taban kimliği de defterde olmalı (N3)")
	}
	if out := h.tick(now.Add(time.Minute), g); len(out) != 0 {
		t.Fatalf("aynı bölüm tekrar çaldı: %+v", out)
	}
	h.restart()
	if out := h.tick(now.Add(30*time.Minute), g); len(out) != 0 {
		t.Fatalf("restart sonrası çift bildirim: %+v", out)
	}
	if !h.e.sentWithin(context.Background(), id, now.Add(2*time.Hour), exOracleP1Cooldown) {
		t.Fatal("P1 gittiyse aynı bölümün P2'si susmalı")
	}
	if out := h.tick(now.Add(exOracleP1Cooldown+time.Minute), g); len(out) != 1 {
		t.Fatalf("soğuma sonrası yeni patlama yeniden bildirilmeli: %+v", out)
	}

	// Sustur: P1 gitmez; sonraki tiklerde okuma yok (negatif önbellek).
	h.ignored[exceptionGroupID(g.Fingerprint, chstore.ExStateNew)] = true
	h.restart()
	later := now.Add(20 * time.Hour)
	if out := h.tick(later, g); len(out) != 0 {
		t.Fatalf("Sustur'lanmış grubun P1'i gitti: %+v", out)
	}
	reads := h.reads
	for i := 1; i <= 30; i++ {
		h.tick(later.Add(time.Duration(i)*time.Minute), g)
	}
	if h.reads != reads {
		t.Errorf("susturulmuş grup 30 tikte %d CH okuması yaptı, want 0", h.reads-reads)
	}

	// Sentetik Problem: kullanıcıya görünen ad (branding etiketi) + operasyon,
	// P1 → critical; tür exception (kanal "Exception" türüyle eşleşir).
	p := exceptionGroupProblem(g, chstore.ExStateNew, "P1", "Teknik hata patlaması: son 1 sa 20,000")
	if p.Severity != "critical" || !strings.HasPrefix(p.RuleName, chstore.CurrentOracleGroupLabel()+" · ORA-00001 · OP_ORDERS") ||
		chstore.ProblemNotifyKind(p) != chstore.NotifyKindException || !strings.Contains(p.Description, "P1 olayı") {
		t.Errorf("Oracle P1 sentetik problemi: %+v (kind %s)", p, chstore.ProblemNotifyKind(p))
	}
}

// Fırtına: bir kaynaktan aynı tikte > 5 grup → TEK kaynak özeti (critical,
// ilk 10 grup son 1 sa'e göre), grup başına bildirim yok — sonraki tiklerde de.
// ≤ 5 → tek tek. Kaynaklar bağımsız; kaynağı bilinmeyen grup tek tek.
func TestOracleP1SourceRollup(t *testing.T) {
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	label := chstore.CurrentOracleGroupLabel()

	t.Run("8 grup tek kaynak → tek özet", func(t *testing.T) {
		h := newOraHarness(t)
		var gs []chstore.ExceptionGroup
		for i := 0; i < 8; i++ {
			gs = append(gs, oraGroup("src-crm", fmt.Sprintf("ORA-%05d", i+1), uint64(1000*(i+1)), h))
		}
		out := h.tick(now, gs...)
		if len(out) != 1 {
			t.Fatalf("tek özet beklenirdi, gelen %d: %+v", len(out), out)
		}
		p := out[0]
		if p.ID != exceptionOracleSourceP1ID("src-crm") || p.Severity != "critical" || p.Priority != "P1" ||
			p.RuleName != label+" · src-crm-db · 8 grup P1" || chstore.ProblemNotifyKind(p) != chstore.NotifyKindException {
			t.Errorf("özet şekli: %+v", p)
		}
		if !strings.HasPrefix(p.Description, "ORA-00008 · OP_ORDERS — son 1 sa 8000") {
			t.Errorf("özet en yüksek son-1-sa grubuyla başlamalı: %q", p.Description)
		}
		if exceptionGroupFingerprint(p.ID) != "" || !isOracleSourceRollupID(p.ID) {
			t.Errorf("özet kimliği tek grup parmak izi taşımamalı: %q", p.ID)
		}
		if out := h.tick(now.Add(time.Minute), gs...); len(out) != 0 {
			t.Fatalf("özete giren gruplar sonraki tikte tek tek çaldı: %+v", out)
		}
	})

	t.Run("3 grup → 3 tekil", func(t *testing.T) {
		h := newOraHarness(t)
		out := h.tick(now, oraGroup("src-crm", "ORA-00001", 9000, h), oraGroup("src-crm", "ORA-00002", 8000, h),
			oraGroup("src-crm", "ORA-00003", 7000, h))
		if len(out) != 3 {
			t.Fatalf("3 tekil beklenirdi: %+v", out)
		}
		for _, p := range out {
			if isOracleSourceRollupID(p.ID) || !strings.HasSuffix(p.ID, ":p1") {
				t.Errorf("tekil P1 kimliği bekleniyordu: %q", p.ID)
			}
		}
	})

	t.Run("kaynaklar bağımsız", func(t *testing.T) {
		h := newOraHarness(t)
		var gs []chstore.ExceptionGroup
		for i := 0; i < 12; i++ {
			gs = append(gs, oraGroup("src-crm", fmt.Sprintf("ORA-%05d", i+1), uint64(100+i), h))
		}
		gs = append(gs, oraGroup("src-cards", "ORA-01400", 6000, h), oraGroup("src-cards", "ORA-01401", 5000, h))
		gs = append(gs, oraGroup("", "ORA-00060", 0, nil)) // kaynağı çözülemeyen
		out := h.tick(now, gs...)
		var rollups, singles int
		for _, p := range out {
			if isOracleSourceRollupID(p.ID) {
				rollups++
				if !strings.HasSuffix(p.Description, "… +2 grup") || p.RuleName != label+" · src-crm-db · 12 grup P1" {
					t.Errorf("12 gruplu özet: %q / %q", p.RuleName, p.Description)
				}
				continue
			}
			singles++
		}
		if rollups != 1 || singles != 3 {
			t.Fatalf("1 özet + 3 tekil beklenirdi, gelen %d + %d: %+v", rollups, singles, out)
		}
	})
}

// Oracle grupları ayrı taramada: span taraması onları dışlar, Oracle taraması
// yalnız onları okur (iki durum için de); Oracle taraması son görülmeyle
// sınırlı, span taramasının şekli değişmedi.
func TestExceptionChannelPassesSplitOracle(t *testing.T) {
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	seen := map[string]bool{}
	for _, p := range exceptionChannelPasses {
		seen[p.state+"/"+p.oracle] = true
		f := exceptionChannelFilter(p.state, p.oracle, now)
		if f.State != p.state || f.Limit != 300 || f.MinOccurrences != exChannelMinOccur || f.Oracle != p.oracle {
			t.Errorf("süzgeç: %+v", f)
		}
		want := int64(0)
		if p.oracle == "only" {
			want = now.Add(-40 * time.Minute).UnixNano()
		}
		if f.ActiveFromNs != want {
			t.Errorf("%s/%s ActiveFromNs=%d want %d", p.state, p.oracle, f.ActiveFromNs, want)
		}
	}
	for _, want := range []string{"new/exclude", "regressed/exclude", "new/only", "regressed/only"} {
		if !seen[want] {
			t.Errorf("tarama eksik: %s", want)
		}
	}
	if len(exceptionChannelPasses) != 4 {
		t.Errorf("tarama sayısı %d, want 4", len(exceptionChannelPasses))
	}
}
