package evaluator

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// anomaly_episode_test.go — v0.10.1045.
//
// Operatör: "Eski yüksek oran taşınmasın: kapanıp yeniden tetiklenen
// anomali, eski en yüksek oranıyla (ör. '66×', P1) görünüyor. Yeni
// tetiklenme sıfırdan başlasın."
//
// anomaly_events aynı parmak izi için tepeyi ve İLK started_at'i 30 gün
// (TTL) taşıyordu. Terfi tarafındaki sonucu: iki gün sonra 6× ile yeniden
// tetiklenen bir olay (a) 300 sn'lik sürme şartını eski started_at
// yüzünden İLK tikte geçiyor, (b) eski tepeyle (66 ≥ 20) critical
// açılıyor, (c) yeni Problem StartedAt'i eski başlangıçtan aldığı için yaş
// tabanlı eskalasyon da onu critical'a kelepçeliyordu. Artık olay-saati
// boşluğu 22 dk 30 sn'yi aşan yazım yeni bölüm açar (chstore.MergeAnomalyCarry) ve
// bu test zinciri GERÇEK birleşim üstünden pinler: elle kurulmuş bir
// "birleşmiş satır" değil, kayıtçının yazacağı satır.
//
// Mutasyon kontrolü: MergeAnomalyCarry koşulsuz taşımaya döndürülürse
// "ilk tik" alt testi kırılır (kapı geçer, şiddet critical olur).

// TestPromotionGateRefireWaitsAgain — yeniden tetiklenen olay kapıyı ilk
// tikte GEÇMEZ; aynı bölümde 5 dk sonra geçer ve şiddeti yeni bölümün
// tepesinden + yeni bölümün yaşından gelir.
func TestPromotionGateRefireWaitsAgain(t *testing.T) {
	cfg := chstore.DefaultAnomalyPromotion()  // 5×, 300 sn, 10 olay, critical 20×
	esc := chstore.DefaultProblemEscalation() // 15 / 30 dk

	day0 := time.Unix(1_700_000_000, 0)
	// Saklı satır: iki gün önceki yük kaynaklı sıçrama, tepe 66×.
	stored := chstore.AnomalyEvent{
		ID: "fp", StartedAt: day0.UnixNano(),
		LastSeen: day0.Add(40 * time.Minute).UnixNano(), PeakRatio: 66,
	}

	// Kayıtçı (recorder.go trace_op): StartedAt = tik anı, LastSeen =
	// son tamamlanmış 5 dk kovasındaki son span.
	refire := day0.Add(48 * time.Hour)
	tick1 := chstore.AnomalyEvent{
		ID: "fp", StartedAt: refire.UnixNano(),
		LastSeen: refire.Add(-30 * time.Second).UnixNano(), CurrentRatio: 6, CurrentCount: 50,
	}
	ev1 := chstore.MergeAnomalyCarry(tick1, stored, true)
	ev1.Status = "active"

	t.Run("ilk tik — kapı geçmez", func(t *testing.T) {
		if promotionGate(ev1, cfg, refire) {
			t.Fatalf("yeniden tetiklenen olay 300 sn beklemeden terfi kapısını geçti "+
				"(StartedAt=%v, PeakRatio=%v) — eski bölümün started_at'i taşınıyor",
				time.Unix(0, ev1.StartedAt), ev1.PeakRatio)
		}
		if ev1.PeakRatio >= cfg.CriticalPeakRatio {
			t.Errorf("yeni bölümün tepesi %v — eski 66× taşınmış (critical eşiği %v)",
				ev1.PeakRatio, cfg.CriticalPeakRatio)
		}
	})

	// Aynı bölüm, 6 dk sonra (bir sonraki 5 dk kovası, boşluk 5 dk).
	now2 := refire.Add(6 * time.Minute)
	tick2 := chstore.AnomalyEvent{
		ID: "fp", StartedAt: now2.UnixNano(),
		LastSeen: refire.Add(5*time.Minute - 30*time.Second).UnixNano(), CurrentRatio: 7, CurrentCount: 60,
	}
	ev2 := chstore.MergeAnomalyCarry(tick2, ev1, true)
	ev2.Status = "active"

	t.Run("aynı bölümde 5 dk sonra — kapı geçer, warning", func(t *testing.T) {
		if ev2.StartedAt != ev1.StartedAt {
			t.Fatalf("bölüm içinde started_at tazelendi: %v → %v",
				time.Unix(0, ev1.StartedAt), time.Unix(0, ev2.StartedAt))
		}
		if !promotionGate(ev2, cfg, now2) {
			t.Fatal("bölüm 6 dk sürdü, tepe 7× ≥ 5×, sayım 60 ≥ 10 — kapı geçmeliydi")
		}
		sev := "warning"
		if ev2.PeakRatio >= cfg.CriticalPeakRatio {
			sev = "critical"
		}
		// Yeni Problem'in StartedAt'i = ev.StartedAt (evaluator.go terfi dalı).
		got := effectiveSeverity(sev, now2.Sub(time.Unix(0, ev2.StartedAt)), esc)
		if got != "warning" {
			t.Errorf("şiddet = %q, want warning — yaş tabanı eski bölümden mi geliyor?", got)
		}
	})

	// Karşı örnek: kapı eski started_at'li bir satırı GERÇEKTEN anında
	// geçirir — yukarıdaki "geçmez" sonucu kapının değil birleşimin eseri.
	t.Run("karşı örnek — eski taşıma kapıyı anında geçirirdi", func(t *testing.T) {
		legacy := ev1
		legacy.StartedAt, legacy.PeakRatio = stored.StartedAt, 66
		if !promotionGate(legacy, cfg, refire) {
			t.Fatal("kapı eski started_at'li satırı geçirmedi — test artık bir şey kanıtlamıyor")
		}
		if got := effectiveSeverity("warning", refire.Sub(time.Unix(0, legacy.StartedAt)), esc); got != "critical" {
			t.Errorf("eski StartedAt'in yaş tabanı = %q, want critical (hatanın kendisi)", got)
		}
	})
}

// TestAnomalyPromotionStep — v0.10.1045: kapı yalnız AÇILIŞI yönetir.
// Olayı aktif ve susturulmamış açık bir `anomaly-auto:` Problem'i, kapı bu
// tik geçmese de tazelenir; eskiden tazelenmeyen satır ~3 dk sonra bayat
// süpürmede "source silent" ile (ack/atanan/AI özeti kaybıyla) kapanıp
// kapı yeniden geçince taze bir sayfayla açılıyordu. Aktif olmayan ve
// susturulan olay eski yollarından kapanır; onlar için açık-Problem
// araması (anlık görüntü okuması) HİÇ yapılmaz.
//
// Mutasyon kontrolü: açık-Problem tazelemesi kapıya bağlanırsa
// (`gateOK && hasOpen()`), "açık + kapı geçmedi" satırı kırılır.
func TestAnomalyPromotionStep(t *testing.T) {
	cases := []struct {
		name                  string
		active, muted, gateOK bool
		hasOpen               bool
		want                  promotionStep
		wantLookup            bool
	}{
		{"açık + aktif + kapı geçmedi → tazele", true, false, false, true, promoteRefresh, true},
		{"açık yok + kapı geçmedi → hiçbir şey", true, false, false, false, promoteSkipGate, true},
		{"açık + aktif + kapı geçti → tazele", true, false, true, true, promoteRefresh, true},
		{"açık yok + kapı geçti → yeni Problem", true, false, true, false, promoteCreate, true},
		{"açık + olay cleared → resolve geçişi", false, false, true, true, promoteSkipCleared, false},
		{"açık + susturulmuş → mute yolu", true, true, true, true, promoteSkipMuted, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			looked := false
			got := anomalyPromotionStep(c.active, c.muted, c.gateOK, func() bool {
				looked = true
				return c.hasOpen
			})
			if got != c.want {
				t.Errorf("anomalyPromotionStep = %d, want %d", got, c.want)
			}
			if looked != c.wantLookup {
				t.Errorf("açık-Problem araması yapıldı=%v, want %v", looked, c.wantLookup)
			}
		})
	}

	// Gövde kararı bu işlevden alır ve bildirimi YALNIZ yeni açılışta atar:
	// tazeleme (kapı geçmese de) bildirim üretmez.
	src := mustReadEvaluatorSource(t, "evaluator.go")
	i := strings.Index(src, "func (e *Evaluator) promoteStrongAnomalies")
	if i < 0 {
		t.Fatal("promoteStrongAnomalies not found")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j > 0 {
		body = body[:j]
	}
	for _, want := range []string{
		"anomalyPromotionStep(ev.Status == \"active\", muted[ev.ID], promotionGate(ev, cfg, now),",
		"isNew := step == promoteCreate",
		"if isNew {",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("promoteStrongAnomalies gövdesinde %q yok", want)
		}
	}
}

// TestPromotionGate — çıkarılan saf kapının davranışı gövdedeki eski üç
// `continue` ile birebir: tepe < eşik, sayım < taban, süre < sürme şartı
// eler; süre TAM şart değerinde GEÇER (eski kod `<` ile eliyordu).
func TestPromotionGate(t *testing.T) {
	cfg := chstore.DefaultAnomalyPromotion()
	now := time.Unix(1_700_000_000, 0)
	at := func(ago time.Duration) int64 { return now.Add(-ago).UnixNano() }
	cases := []struct {
		name string
		ev   chstore.AnomalyEvent
		want bool
	}{
		{"hepsi geçer", chstore.AnomalyEvent{PeakRatio: 5, CurrentCount: 10, StartedAt: at(10 * time.Minute)}, true},
		{"tepe eşiğin altında", chstore.AnomalyEvent{PeakRatio: 4.99, CurrentCount: 10, StartedAt: at(10 * time.Minute)}, false},
		{"sayım tabanın altında", chstore.AnomalyEvent{PeakRatio: 9, CurrentCount: 9, StartedAt: at(10 * time.Minute)}, false},
		{"süre 299 sn", chstore.AnomalyEvent{PeakRatio: 9, CurrentCount: 10, StartedAt: at(299 * time.Second)}, false},
		{"süre tam 300 sn — geçer", chstore.AnomalyEvent{PeakRatio: 9, CurrentCount: 10, StartedAt: at(300 * time.Second)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := promotionGate(c.ev, cfg, now); got != c.want {
				t.Errorf("promotionGate = %v, want %v", got, c.want)
			}
		})
	}

	// Gövde kapıyı kullanmaya devam etmeli — satır içi bir kopya bu
	// pinlerin dışında kalırdı.
	src := mustReadEvaluatorSource(t, "evaluator.go")
	i := strings.Index(src, "func (e *Evaluator) promoteStrongAnomalies")
	if i < 0 {
		t.Fatal("promoteStrongAnomalies not found")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "promotionGate(ev, cfg, now)") {
		t.Error("promoteStrongAnomalies terfi kapısını promotionGate üzerinden uygulamıyor")
	}
}
