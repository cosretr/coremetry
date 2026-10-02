// anomaly_events TOPLU YAZIMI (v0.9.957) — taşınan semantiğin pinleri.
//
// ─── Hangi olayı zorunlu kılıyor ─────────────────────────────────────
// Davranış motoru (v0.9.936) bir tikte 37 olay yazıyordu ve bunu 37
// ardışık UpsertAnomalyEvent çağrısıyla yapıyordu. ÖLÇÜLDÜ (lokal,
// 2026-08-11): 25 618 ms'lik tikin ~20 saniyesi bu döngüdeydi — 28
// günlük MV sorgusu değil, olay YAZIMI. Her çağrı iki gidiş-dönüş
// (bir FINAL SELECT + bir INSERT) demekti, yani 74 round-trip; her biri
// TEK satır için, ve her FINAL bir ReplacingMergeTree birleştirmesi
// ödüyordu.
//
// Yazım yolu tek toplu ifadeye indirilirken taşınması gereken iki
// semantik vardı ve ikisi de sessizce bozulabilirdi:
//
//	started_at KORUNUR — "süregelen anomali TEK satırdır" sözleşmesinin
//	  tamamı. Tazelenirse /anomalies'te 4 saattir açık bir olay her
//	  tikte "az önce başladı" der ve P1 triyajı ("open ≥ 4h") çöker.
//	peak_ratio yalnız YÜKSELİR — terfi kapısının (promoteStrongAnomalies
//	  15× eşiği) girdisi. Geri düşerse zirvesi geçmiş bir olay Problem
//	  açmaz.
//
// İkisi de MergeAnomalyCarry'de tek yerde duruyor; bu dosya orayı
// tablo-testliyor.
//
// v0.10.1045 — ikisi de artık BÖLÜM İÇİN geçerli. Operatör: "Eski yüksek
// oran taşınmasın: kapanıp yeniden tetiklenen anomali, eski en yüksek
// oranıyla (ör. '66×', P1) görünüyor. Yeni tetiklenme sıfırdan başlasın."
// Olay-saati boşluğu anomalyEpisodeGap'i (22 dk 30 sn; aktif yaş DEĞİL)
// aşan yazım YENİ BÖLÜM açar: started_at ve peak_ratio yalnız gelen olaydan.
// Terfi kapısı ve /inbox önceliği aynı birleşimin üstünden kendi
// paketlerinde pinli (evaluator/anomaly_episode_test.go,
// api/inbox_anomaly_episode_test.go).
package chstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// TestMergeAnomalyCarry — carry-forward tablosu. HER dal ayrı bir
// üretim davranışı; biri sessizce ters dönerse üstteki iki sistem
// bozulur ve hiçbir ekran bunu söylemez. Bu tablodaki satırların
// hepsi AYNI bölümde (saklı satır aktif: son görülme gelen olaydan 1 dk
// önce) — v0.10.1045 öncesi davranış burada birebir korunur.
func TestMergeAnomalyCarry(t *testing.T) {
	// Sabit anlar — "şimdi"ye bağlı test, gece yarısı kırılan testtir.
	oldStartNs := int64(1_700_000_000) * int64(time.Second)
	newStartNs := int64(1_700_090_000) * int64(time.Second)
	lastNs := newStartNs + int64(30*time.Second)
	storedLastNs := lastNs - int64(time.Minute) // aktif: boşluk 1 dk

	cases := []struct {
		name      string
		ev        AnomalyEvent
		prevStart int64
		prevPeak  float64
		exists    bool
		wantStart int64
		wantPeak  float64
	}{
		{
			name:      "ilk görülme — olayın kendi değerleri",
			ev:        AnomalyEvent{StartedAt: newStartNs, LastSeen: lastNs, CurrentRatio: 3.0},
			exists:    false,
			wantStart: newStartNs,
			wantPeak:  3.0,
		},
		{
			name:      "var olan satır — started_at KORUNUR",
			ev:        AnomalyEvent{StartedAt: newStartNs, LastSeen: lastNs, CurrentRatio: 3.0},
			prevStart: oldStartNs, prevPeak: 2.0, exists: true,
			wantStart: oldStartNs,
			wantPeak:  3.0, // yeni oran daha yüksek → zirve yükselir
		},
		{
			name:      "oran DÜŞTÜ — zirve korunur (monotonluk)",
			ev:        AnomalyEvent{StartedAt: newStartNs, LastSeen: lastNs, CurrentRatio: 1.2},
			prevStart: oldStartNs, prevPeak: 9.0, exists: true,
			wantStart: oldStartNs,
			wantPeak:  9.0,
		},
		{
			name:      "oran EŞİT — zirve değişmez",
			ev:        AnomalyEvent{StartedAt: newStartNs, LastSeen: lastNs, CurrentRatio: 4.0},
			prevStart: oldStartNs, prevPeak: 4.0, exists: true,
			wantStart: oldStartNs,
			wantPeak:  4.0,
		},
		{
			// DÜŞÜŞ adayları (davranış motoru, ratio < 1). Zirve ilk
			// görülen orana takılır ve terfi eşiğini geçmez — bilinçli:
			// bir düşüşü "5× peak" diye raporlamak yalan olurdu.
			name:      "düşüş adayı — zirve 1'in altında kalır",
			ev:        AnomalyEvent{StartedAt: newStartNs, LastSeen: lastNs, CurrentRatio: 0.4},
			prevStart: oldStartNs, prevPeak: 0.6, exists: true,
			wantStart: oldStartNs,
			wantPeak:  0.6,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stored := AnomalyEvent{StartedAt: c.prevStart, LastSeen: storedLastNs, PeakRatio: c.prevPeak}
			got := MergeAnomalyCarry(c.ev, stored, c.exists)
			if got.StartedAt != c.wantStart {
				t.Errorf("started_at = %d, want %d", got.StartedAt, c.wantStart)
			}
			if got.PeakRatio != c.wantPeak {
				t.Errorf("peak_ratio = %v, want %v", got.PeakRatio, c.wantPeak)
			}
			if got.LastSeen != lastNs {
				t.Errorf("last_seen = %d, want %d (gelen olayınki)", got.LastSeen, lastNs)
			}
		})
	}
}

// TestMergeAnomalyCarryEpisode — v0.10.1045 bölüm sınırı. Saklı satırın
// last_seen'i gelen olayınkinden anomalyEpisodeGap'ten (22 dk 30 sn) FAZLA
// gerideyse satır uzun süre "cleared" kalmıştır ve gelen yazım sıfırdan
// başlar. Sınır aktif yaşa (10 dk) EŞİT DEĞİL — tek kova kaçıran hizalı
// yazıcı tam orada durur (TestMergeAnomalyCarryBucketWriter).
//
// Mutasyon kontrolü: MergeAnomalyCarry'deki anomalyNewEpisode dalı
// silinirse (koşulsuz taşıma geri gelirse) "yeni bölüm" satırları
// eski tepeyi (66) ve eski started_at'i döndürür ve bu test kırılır.
func TestMergeAnomalyCarryEpisode(t *testing.T) {
	const s = int64(time.Second)
	// Saklı satır: iki gün önce yük kaynaklı bir sıçrama, tepe 66×.
	storedStart := int64(1_700_000_000) * s
	storedLast := storedStart + 40*60*s
	stored := AnomalyEvent{StartedAt: storedStart, LastSeen: storedLast, PeakRatio: 66}
	active := int64(anomalyActiveAge)
	gapB := int64(anomalyEpisodeGap)
	if gapB != 22*60*s+30*s {
		t.Fatalf("anomalyEpisodeGap = %v, want 22m30s", anomalyEpisodeGap)
	}

	cases := []struct {
		name      string
		gap       int64 // gelen last_seen − saklı last_seen
		ratio     float64
		wantNew   bool
		wantPeak  float64
		wantLast  int64 // 0 → gelen olayınki
		zeroIncLS bool  // gelen olayın last_seen'i 0 (örnek sorgusu düşmüş)
	}{
		// Yeniden tetiklenme: iki gün sonra 3.2× — tepe 66 TAŞINMAZ.
		{name: "iki gün sonra yeniden tetiklenme — yeni bölüm", gap: 2 * 24 * 3600 * s, ratio: 3.2, wantNew: true, wantPeak: 3.2},
		// Yeni bölümün oranı eskisinden yüksek olsa da tepe gelen orandır
		// (max değil): eski bölümün değeri karşılaştırmaya hiç girmez.
		{name: "yeni bölüm — gelen oran yüksek de olsa yalnız kendisi", gap: 2 * 24 * 3600 * s, ratio: 80, wantNew: true, wantPeak: 80},
		// Sınırın bir nanosaniye ötesi: yeni bölüm.
		{name: "bölüm boşluğu + 1 ns — yeni bölüm", gap: gapB + 1, ratio: 3.2, wantNew: true, wantPeak: 3.2},
		// SINIR SEÇİMİ: tam bölüm boşluğu → AYNI bölüm.
		{name: "tam bölüm boşluğu — aynı bölüm (sınır kapsayıcı)", gap: gapB, ratio: 3.2, wantNew: false, wantPeak: 66},
		// Aktif yaşı aşan ama bölüm boşluğunun altında kalan boşluk: satır
		// kısa süre "cleared" göründü, bölüm SÜRER (tek kaçırılmış kova).
		{name: "aktif yaş + 1 ns — satır kısa süre cleared, bölüm sürer", gap: active + 1, ratio: 3.2, wantNew: false, wantPeak: 66},
		{name: "aktif satır, düşük oran — taşıma (bugünkü davranış)", gap: 60 * s, ratio: 3.2, wantNew: false, wantPeak: 66},
		// Sırası bozuk yazıcı (elastic_ml skora göre sıralı; tekrar okunan
		// pencere): gelen olay saklı olandan ESKİ. Zaman yolculuğu yok:
		// negatif boşluk asla yeni bölüm değil, last_seen GERİ GİTMEZ.
		{name: "gelen olay daha eski (1 sa) — aynı bölüm, last_seen geri gitmez", gap: -3600 * s, ratio: 3.2, wantNew: false, wantPeak: 66, wantLast: storedLast},
		{name: "gelen olay daha eski (45 dk) — bölüm boşluğunu aşsa da yeni bölüm değil", gap: -2 * gapB, ratio: 90, wantNew: false, wantPeak: 90, wantLast: storedLast},
		// last_seen'i 0 olan yazım (trace_op_latency örnek sorgusu düşünce
		// LastSeenNs 0 kalır): saklı last_seen'i 1970'e çekmez; çekseydi
		// bir sonraki normal tik sahte bir boşluk görüp bölümü sıfırlardı.
		{name: "gelen last_seen 0 — aynı bölüm, saklı last_seen korunur", zeroIncLS: true, ratio: 3.2, wantNew: false, wantPeak: 66, wantLast: storedLast},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			incLast := storedLast + c.gap
			if c.zeroIncLS {
				incLast = 0
			}
			// Kayıtçı started_at'i tespit anı yazar (≈ gelen last_seen).
			incStart := incLast - 30*s
			in := AnomalyEvent{ID: "fp", StartedAt: incStart, LastSeen: incLast, CurrentRatio: c.ratio, CurrentCount: 42}
			got := MergeAnomalyCarry(in, stored, true)

			wantStart := storedStart
			if c.wantNew {
				wantStart = incStart
			}
			if got.StartedAt != wantStart {
				t.Errorf("started_at = %d, want %d (yeni bölüm=%v)", got.StartedAt, wantStart, c.wantNew)
			}
			if got.PeakRatio != c.wantPeak {
				t.Errorf("peak_ratio = %v, want %v", got.PeakRatio, c.wantPeak)
			}
			wantLast := c.wantLast
			if wantLast == 0 {
				wantLast = incLast
			}
			if got.LastSeen != wantLast {
				t.Errorf("last_seen = %d, want %d", got.LastSeen, wantLast)
			}
			// Bölüm dışı alanlar her dalda gelen olaydan.
			if got.CurrentRatio != c.ratio || got.CurrentCount != 42 || got.ID != "fp" {
				t.Errorf("gelen alanlar taşınmadı: %+v", got)
			}
		})
	}
}

// TestMergeAnomalyCarrySequence — kayıtçının gerçek akışı, ardışık
// upsert'ler (her biri bir öncekinin YAZDIĞI satırı saklı satır olarak
// görür): süren bölümde tepe birikir, bölüm boşluğundan uzun sessizlikten
// sonra sıfırdan başlar, yeni bölümde yeniden birikir. Tek çağrılık test
// "yazılan satır bir sonraki kararın girdisidir" zincirini göremez.
func TestMergeAnomalyCarrySequence(t *testing.T) {
	const s = int64(time.Second)
	t0 := int64(1_700_000_000) * s
	steps := []struct {
		at        int64 // gelen last_seen (t0'dan)
		zeroLS    bool  // örnek sorgusu düştü: gelen LastSeen 0
		ratio     float64
		wantStart int64 // t0'dan
		wantPeak  float64
	}{
		{at: 0, ratio: 12, wantStart: 0, wantPeak: 12},
		{at: 5 * 60 * s, ratio: 66, wantStart: 0, wantPeak: 66}, // 5 dk kova adımı
		{at: 10 * 60 * s, ratio: 8, wantStart: 0, wantPeak: 66},
		// Örnek sorgusu düşen tik (LastSeen 0): bölüm sürer ve saklı
		// last_seen 1970'e İNMEZ — inseydi bir sonraki normal tik 50+ yıllık
		// "boşluk" görüp süren bölümü sıfırlardı.
		{at: 11 * 60 * s, zeroLS: true, ratio: 9, wantStart: 0, wantPeak: 66},
		{at: 15 * 60 * s, ratio: 7, wantStart: 0, wantPeak: 66},
		// 2 gün sessizlik → yeni bölüm, 66 taşınmaz.
		{at: 2*24*3600*s + 10*60*s, ratio: 3.2, wantStart: 2*24*3600*s + 10*60*s, wantPeak: 3.2},
		{at: 2*24*3600*s + 15*60*s, ratio: 4.5, wantStart: 2*24*3600*s + 10*60*s, wantPeak: 4.5},
		{at: 2*24*3600*s + 20*60*s, ratio: 2.0, wantStart: 2*24*3600*s + 10*60*s, wantPeak: 4.5},
	}
	var stored AnomalyEvent
	exists := false
	for i, st := range steps {
		in := AnomalyEvent{StartedAt: t0 + st.at, LastSeen: t0 + st.at, CurrentRatio: st.ratio}
		if st.zeroLS {
			in.LastSeen = 0
		}
		got := MergeAnomalyCarry(in, stored, exists)
		if got.StartedAt != t0+st.wantStart || got.PeakRatio != st.wantPeak {
			t.Fatalf("adım %d: started_at=+%ds peak=%v, want +%ds peak=%v",
				i, (got.StartedAt-t0)/s, got.PeakRatio, st.wantStart/s, st.wantPeak)
		}
		stored, exists = got, true
	}
}

// TestMergeAnomalyCarryBucketWriter — 5 dk kovaya hizalı gerçek yazıcı
// (trace_op / trace_op_latency): last_seen = kovanın son span'i, yani
// ardışık kovalar ~5 dk arayla; kayıtçı 60 sn'de bir yazar, aynı kova beş
// kez aynı last_seen'le gelir. Kaçırılan kova (ör. hata sayısı tabanın bir
// altında) boşluğu 5 dk'nın katı ± saniye yapar.
//
// Mutasyon kontrolü: anomalyEpisodeGap aktif yaşa (10 dk) geri çekilirse
// "tek kova kaçırıldı" dizisi bölümü sıfırlar ve bu test kırılır.
func TestMergeAnomalyCarryBucketWriter(t *testing.T) {
	const s = int64(time.Second)
	t0 := int64(1_700_000_000) * s
	bucket := 5 * 60 * s
	// lastSpan — k. kovanın son span'i: kova sonundan birkaç saniye önce,
	// kovadan kovaya oynar (sınır yazı-turasını üreten şey bu).
	lastSpan := func(k int64, jitter int64) int64 { return t0 + (k+1)*bucket - jitter }

	type obs struct {
		k      int64 // kova sırası
		jitter int64
		ratio  float64
	}
	run := func(seq []obs) AnomalyEvent {
		var stored AnomalyEvent
		exists := false
		for _, o := range seq {
			last := lastSpan(o.k, o.jitter)
			for tick := 0; tick < 5; tick++ { // aynı kova, 60 sn'lik beş yazım
				in := AnomalyEvent{StartedAt: last + int64(tick+1)*60*s, LastSeen: last, CurrentRatio: o.ratio}
				stored, exists = MergeAnomalyCarry(in, stored, exists), true
			}
		}
		return stored
	}

	cases := []struct {
		name     string
		seq      []obs
		wantNew  bool // son bölüm ilk kovadan farklı mı başladı
		wantPeak float64
	}{
		{
			name:     "kesintisiz kovalar — tek bölüm",
			seq:      []obs{{0, 2 * s, 12}, {1, 1 * s, 30}, {2, 3 * s, 8}},
			wantPeak: 30,
		},
		{
			// Kova 1 kaçırıldı: boşluk 10 dk + (jitter farkı) — aktif yaşın
			// hemen üstü, satır saniyelerce "cleared". Bölüm SÜRER.
			name:     "tek kova kaçırıldı — aynı bölüm, tepe ve başlangıç taşınır",
			seq:      []obs{{0, 9 * s, 30}, {2, 1 * s, 6}},
			wantPeak: 30,
		},
		{
			name:     "iki kova kaçırıldı (~15 dk) — aynı bölüm",
			seq:      []obs{{0, 9 * s, 30}, {3, 1 * s, 6}},
			wantPeak: 30,
		},
		{
			// 30 dk sessizlik (5 kova kaçırıldı): satır ~20 dk "cleared"
			// kaldı, terfi Problem'i çoktan kapandı → yeni bölüm.
			name:    "30 dk sessizlik — yeni bölüm, eski tepe taşınmaz",
			seq:     []obs{{0, 9 * s, 30}, {6, 1 * s, 6}},
			wantNew: true, wantPeak: 6,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := run(c.seq)
			first := c.seq[0]
			firstStart := lastSpan(first.k, first.jitter) + 60*s // ilk yazımın tik anı
			if !c.wantNew && got.StartedAt != firstStart {
				t.Errorf("started_at = +%ds, want ilk kovanınki +%ds (bölüm sürmeliydi)",
					(got.StartedAt-t0)/s, (firstStart-t0)/s)
			}
			if c.wantNew && got.StartedAt == firstStart {
				t.Error("started_at ilk kovadan taşındı — 30 dk sessizlikten sonra yeni bölüm beklenirdi")
			}
			if got.PeakRatio != c.wantPeak {
				t.Errorf("peak_ratio = %v, want %v", got.PeakRatio, c.wantPeak)
			}
		})
	}
}

// TestFoldAnomalyCarryRow — v0.10.1045: taşıma okuması aynı id için iki
// sürüm döndürürse (0010 uygulanmamış kurulum, partition-yerel FINAL)
// last_seen'i BÜYÜK olan kalır — geliş sırasından bağımsız. Bayat sürüm
// kazansaydı bölüm kararı her tikte sahte bir boşluk görürdü.
func TestFoldAnomalyCarryRow(t *testing.T) {
	stale := AnomalyEvent{StartedAt: 1, LastSeen: 100, PeakRatio: 66}
	fresh := AnomalyEvent{StartedAt: 1, LastSeen: 5000, PeakRatio: 70}
	for _, order := range [][2]AnomalyEvent{{stale, fresh}, {fresh, stale}} {
		prev := map[string]AnomalyEvent{}
		foldAnomalyCarryRow(prev, "fp", order[0])
		foldAnomalyCarryRow(prev, "fp", order[1])
		if prev["fp"].LastSeen != fresh.LastSeen {
			t.Errorf("sıra %v/%v: kalan last_seen = %d, want %d",
				order[0].LastSeen, order[1].LastSeen, prev["fp"].LastSeen, fresh.LastSeen)
		}
	}
	// Farklı id'ler birbirini etkilemez.
	prev := map[string]AnomalyEvent{}
	foldAnomalyCarryRow(prev, "a", stale)
	foldAnomalyCarryRow(prev, "b", fresh)
	if len(prev) != 2 || prev["a"].LastSeen != 100 {
		t.Errorf("farklı id'ler karıştı: %+v", prev)
	}
}

// carryReadFailConn — taşıma okuması hata verir; PrepareBatch çağrılırsa
// kaydeder. driver.Conn GÖMÜLÜ: kullanılmayan bir metot çağrılırsa nil
// üzerinde panikler, sessizce geçmez.
type carryReadFailConn struct {
	driver.Conn
	prepared *bool
}

func (c carryReadFailConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	return nil, errors.New("ch: read timeout")
}

func (c carryReadFailConn) PrepareBatch(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	*c.prepared = true
	return nil, errors.New("PrepareBatch çağrılmamalıydı")
}

// TestUpsertAnomalyEventsSkipsWriteOnCarryReadError — v0.10.1045: taşıma
// okuması hata verirse yazım YOK, hata çağırana döner. Eskiden prev boş
// bırakılıp her olay "ilk görülme" gibi yazılıyordu: çağrıdaki tüm olayların
// started_at'i ve tepesi sıfırlanıyordu (davranış motorunda bütün filo).
func TestUpsertAnomalyEventsSkipsWriteOnCarryReadError(t *testing.T) {
	prepared := false
	s := &Store{conn: carryReadFailConn{prepared: &prepared}}
	err := s.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{
		{ID: "a", LastSeen: 1, CurrentRatio: 3}, {ID: "b", LastSeen: 1, CurrentRatio: 9},
	})
	if err == nil {
		t.Fatal("taşıma okuması hata verdi ama UpsertAnomalyEvents nil döndü")
	}
	if prepared {
		t.Error("taşıma okuması hatasında INSERT hazırlandı — olaylar 'ilk görülme' gibi yazılıp bölümleri sıfırlanırdı")
	}
}

// TestUniqueAnomalyIDs — IN listesi tekil VE sıra korunur.
//
// Sıra ŞART: bind argümanlarının sırası deterministik değilse aynı
// yazım iki podda iki farklı sorgu METNİ üretir ve query_log'da tek bir
// ifade olarak görünmez — perf ölçümünün dayandığı şey tam olarak o
// gruplama.
func TestUniqueAnomalyIDs(t *testing.T) {
	cases := []struct {
		name string
		in   []AnomalyEvent
		want []string
	}{
		{"boş", nil, []string{}},
		{"tekil", []AnomalyEvent{{ID: "a"}, {ID: "b"}}, []string{"a", "b"}},
		{
			// Aynı tikte aynı fingerprint iki kez aday olabilir; `id IN
			// (x, x)` hem fazladan bind hem de çift satır demek.
			"tekrarlı — ilk görülme sırası korunur",
			[]AnomalyEvent{{ID: "b"}, {ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "a"}},
			[]string{"b", "a", "c"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := uniqueAnomalyIDs(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("uniqueAnomalyIDs = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("uniqueAnomalyIDs = %v, want %v", got, c.want)
				}
			}
		})
	}
}

// TestUpsertAnomalyEventsEmptyIsNoop — boş dilim CH'ye HİÇ dokunmamalı.
// Aday üretmeyen bir tik query_log'da iz bırakmamalı: hem ucuz hem de
// dürüst. nil conn'la çağrılıyor — bir sorgu denenirse panik/hata olur,
// yani "dokunmadı" gerçekten kanıtlanıyor.
func TestUpsertAnomalyEventsEmptyIsNoop(t *testing.T) {
	s := &Store{} // conn nil
	if err := s.UpsertAnomalyEvents(context.Background(), nil); err != nil {
		t.Errorf("boş dilim hata döndürdü: %v", err)
	}
	if err := s.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{}); err != nil {
		t.Errorf("boş dilim hata döndürdü: %v", err)
	}
}
