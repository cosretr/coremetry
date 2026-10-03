package oracle

// exgroups_test.go — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün"). Tazeleyici pinleri: her dakika TAM BİR KEZ (yeniden okuma, aynı
// "şimdi", yeni tazeleyici örneği ikinci kez saymaz), kip kapısı (off → yazım
// yok, imleç durur; shadow → grup var, bildirim yok; live → bildirim), yazım
// hatasında imleç ilerlemez, yoksayılan kod sayılmaz, servis oyları.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// minuteRow — sahte oracle_error_log'un bir dakikalık toplamı.
type minuteRow struct {
	t                 time.Time
	code, op, channel string
	w                 uint64
}

// fakeExStore — OracleGroupAggregates yarı açık [from, to) aralığını sahte
// tablodan toplar; Upsert, merge'in yaptığı gibi ARTIMI toplar.
type fakeExStore struct {
	rows     []minuteRow
	upserts  [][]chstore.ExceptionGroup
	total    map[string]uint64
	failNext bool
	aggCalls int
}

func (f *fakeExStore) OracleGroupAggregates(_ context.Context, _ string, from, to time.Time) ([]chstore.OracleGroupAgg, error) {
	f.aggCalls++
	// Gerçek SQL gibi (kod, operasyon, kanal, DAKİKA) başına, dakika sıralı.
	idx := map[[4]string]int{}
	var out []chstore.OracleGroupAgg
	for _, r := range f.rows {
		if r.t.Before(from) || !r.t.Before(to) {
			continue
		}
		m := r.t.UTC().Truncate(time.Minute)
		k := [4]string{r.code, r.op, r.channel, m.String()}
		i, ok := idx[k]
		if !ok {
			out = append(out, chstore.OracleGroupAgg{Code: r.code, Op: r.op, Channel: r.channel, Minute: m, First: r.t, Last: r.t})
			i = len(out) - 1
			idx[k] = i
		}
		out[i].Weight += r.w
		if r.t.Before(out[i].First) {
			out[i].First = r.t
		}
		if r.t.After(out[i].Last) {
			out[i].Last = r.t
		}
	}
	return out, nil
}

func (f *fakeExStore) UpsertExceptionGroups(_ context.Context, gs []chstore.ExceptionGroup) error {
	if f.failNext {
		f.failNext = false
		return errors.New("ch down")
	}
	f.upserts = append(f.upserts, gs)
	if f.total == nil {
		f.total = map[string]uint64{}
	}
	for _, g := range gs {
		f.total[g.Fingerprint] += g.Occurrences
	}
	return nil
}

func exSource(mode string) SourceConfig {
	s := syntheticCustomSource()
	s.ProblemMode = mode
	s.WindowMin = 15
	return s
}

// Her dakika sabit ağırlık: 10:00..10:59, (APP_ERR_001, OP_A) MOB=3 + WEB=2,
// (APP_ERR_002, OP_B) MOB=1.
func steadyRows(base time.Time) []minuteRow {
	var out []minuteRow
	for i := 0; i < 60; i++ {
		t := base.Add(time.Duration(i) * time.Minute)
		out = append(out,
			minuteRow{t, "APP_ERR_001", "OP_A", "MOB", 3},
			minuteRow{t, "APP_ERR_001", "OP_A", "WEB", 2},
			minuteRow{t, "APP_ERR_002 ", "OP_B", "MOB", 1}) // CHAR dolgusu
	}
	return out
}

func TestExGroupClosedEnd(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 30, 42, 0, time.UTC)
	if got := ExGroupClosedEnd(exSource(ProblemModeShadow), now); !got.Equal(time.Date(2026, 10, 3, 10, 14, 0, 0, time.UTC)) {
		t.Fatalf("özel kip: trunc(now) − 15 dk − 1 dk = 10:14, got %v", got)
	}
	tbl := testSource() // tablo kipi, aralık 60 sn
	if got := ExGroupClosedEnd(tbl, now); !got.Equal(time.Date(2026, 10, 3, 10, 26, 0, 0, time.UTC)) {
		t.Fatalf("tablo kipi: trunc(now) − (2 dk overlap + 1 dk aralık) − 1 dk = 10:26, got %v", got)
	}
}

// İmleç idempotensi: aynı "şimdi"de ikinci tur sorgu bile koşmaz; dakika
// ilerledikçe yalnız YENİ kapanan dakika sayılır; yeni tazeleyici örneği
// (lider değişimi / yeniden başlatma) kalıcı imleçten devam eder. Sonunda her
// dakika tam bir kez: toplam = dakika sayısı × ağırlık.
func TestExGroupRefreshCountsEachMinuteOnce(t *testing.T) {
	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	st := &fakeExStore{rows: steadyRows(base)}
	state := &fakeState{}
	src := exSource(ProblemModeShadow)
	r := NewExGroupRefresher(st, state, nil)
	now := base.Add(76 * time.Minute) // kapanış 11:00 → [09:00, 11:00) ilk turda (geriye bakış 2 sa)
	r.now = func() time.Time { return now }
	res, err := r.Refresh(context.Background(), src)
	if err != nil || res.Groups != 2 || !res.From.Equal(base.Add(-time.Hour)) || !res.To.Equal(base.Add(60*time.Minute)) {
		t.Fatalf("ilk tur: %+v err=%v", res, err)
	}
	fpA := chstore.OracleGroupFingerprint(src.ID, "APP_ERR_001", "OP_A")
	fpB := chstore.OracleGroupFingerprint(src.ID, "APP_ERR_002", "OP_B")
	if st.total[fpA] != 60*5 || st.total[fpB] != 60 {
		t.Fatalf("ilk tur toplamları: %v", st.total)
	}
	// Aynı şimdi: yeni kapanan dakika yok → toplama sorgusu YOK.
	calls := st.aggCalls
	if res, _ := r.Refresh(context.Background(), src); res.Skipped == "" || st.aggCalls != calls {
		t.Fatalf("aynı şimdi yeniden saymamalı: %+v", res)
	}
	// Yeni satırlar (11:00..11:04) gelir, şimdi 3 dk ilerler → yalnız 11:00..11:02.
	st.rows = append(st.rows, steadyRows(base.Add(60 * time.Minute))[:15]...)
	now = now.Add(3 * time.Minute)
	if _, err := r.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if st.total[fpA] != 63*5 || st.total[fpB] != 63 {
		t.Fatalf("yalnız yeni kapanan 3 dakika: %v", st.total)
	}
	// Yeni örnek (lider değişti) — kalıcı imleçten devam, aynı şimdide sayım yok.
	r2 := NewExGroupRefresher(st, state, nil)
	r2.now = func() time.Time { return now }
	if res, _ := r2.Refresh(context.Background(), src); res.Groups != 0 || st.total[fpA] != 63*5 {
		t.Fatalf("kalıcı imleç: %+v %v", res, st.total)
	}
	now = now.Add(2 * time.Minute)
	if _, err := r2.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if st.total[fpA] != 65*5 {
		t.Fatalf("yeni örnek kaldığı yerden: %v", st.total)
	}
	// Grup alanları: tip = kod (kırpılmış), mesaj = operasyon, servis sentetik.
	g := st.upserts[0][0]
	if g.Fingerprint != fpA || g.Type != "APP_ERR_001" || g.Message != "OP_A" || g.Service != "oracle:app-err" ||
		g.FirstSeen != base.UnixNano() || g.LastSeen != base.Add(59*time.Minute).UnixNano() {
		t.Fatalf("grup: %+v", g)
	}
	// Kanal kırılımı blob'da (kapanmış dakikaların ağırlığı).
	stt := DecodeExGroupState(state.kv[ExGroupKey(src.ID)])
	if b := stt.Groups[fpA]; b == nil || b.Channels["MOB"] != 65*3 || b.Channels["WEB"] != 65*2 {
		t.Fatalf("kanal kırılımı: %+v", stt.Groups[fpA])
	}
}

// Kip kapısı + hata yönü.
func TestExGroupRefreshModeGatingAndFailure(t *testing.T) {
	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	st := &fakeExStore{rows: steadyRows(base)}
	state := &fakeState{}
	r := NewExGroupRefresher(st, state, nil)
	now := base.Add(76 * time.Minute)
	r.now = func() time.Time { return now }
	// off → hiçbir şey yazılmaz, sorgu yok, imleç yok.
	if res, err := r.Refresh(context.Background(), exSource(ProblemModeOff)); err != nil || res.Skipped == "" || st.aggCalls != 0 || len(st.upserts) != 0 {
		t.Fatalf("off: %+v", res)
	}
	if _, ok := state.kv[ExGroupKey(exSource(ProblemModeOff).ID)]; ok {
		t.Fatal("off imleç yazmamalı")
	}
	// Yazım düşerse imleç ilerlemez; sonraki tur AYNI aralığı yeniden toplar.
	st.failNext = true
	if _, err := r.Refresh(context.Background(), exSource(ProblemModeLive)); err == nil {
		t.Fatal("yazım hatası dönmeli")
	}
	if res, err := r.Refresh(context.Background(), exSource(ProblemModeLive)); err != nil || res.Groups != 2 || !res.From.Equal(base.Add(-time.Hour)) {
		t.Fatalf("aynı aralık yeniden: %+v %v", res, err)
	}
	fpA := chstore.OracleGroupFingerprint(exSource("").ID, "APP_ERR_001", "OP_A")
	if st.total[fpA] != 60*5 {
		t.Fatalf("başarısız tur sayılmamalı: %v", st.total)
	}
}

// Yoksayılan kod sayılmaz; servis = birikimli oyların baskını.
func TestExGroupIgnoreAndServiceVotes(t *testing.T) {
	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	st := &fakeExStore{rows: steadyRows(base)}
	src := exSource(ProblemModeShadow)
	src.IgnoreCodes = []string{"APP_ERR_002"}
	votes := func(_ context.Context, _ string, op, _ string) map[string]int {
		if op == "OP_A" {
			return map[string]int{"svc-payments": 4, "svc-cards": 1}
		}
		return nil
	}
	r := NewExGroupRefresher(st, &fakeState{}, votes)
	r.now = func() time.Time { return base.Add(76 * time.Minute) }
	res, err := r.Refresh(context.Background(), src)
	if err != nil || res.Groups != 1 || len(st.upserts) != 1 {
		t.Fatalf("yoksayılan kod grup açmamalı: %+v %v", res, err)
	}
	if g := st.upserts[0][0]; g.Service != "svc-payments" || g.Type != "APP_ERR_001" {
		t.Fatalf("baskın servis: %+v", g)
	}
}

// Bildirim süzgeci: span grubu her zaman; Oracle grubu yalnız kaynağı etkin
// ve live iken; kaynak bulunamazsa (silinmiş) sessiz.
func TestGroupNotifiesByMode(t *testing.T) {
	live, shadow := exSource(ProblemModeLive), exSource(ProblemModeShadow)
	shadow.ID, shadow.Name = "o-22222222", "app-err-2"
	cfg := Settings{Sources: []SourceConfig{live, shadow}}
	gLive := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint(live.ID, "APP_ERR_001", "OP_A"), Type: "APP_ERR_001", Message: "OP_A"}
	gShadow := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint(shadow.ID, "APP_ERR_001", "OP_A"), Type: "APP_ERR_001", Message: "OP_A"}
	gGone := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-deleted", "APP_ERR_001", "OP_A"), Type: "APP_ERR_001", Message: "OP_A"}
	if !GroupNotifies(cfg, chstore.ExceptionGroup{Fingerprint: "0a1b2c3d4e5f6a7b"}) {
		t.Fatal("span grubu bildirilir")
	}
	if !GroupNotifies(cfg, gLive) || GroupNotifies(cfg, gShadow) || GroupNotifies(cfg, gGone) {
		t.Fatal("live bildirir; shadow ve sahipsiz bildirmez")
	}
	if src, ok := GroupSource(cfg, gShadow); !ok || src.ID != shadow.ID {
		t.Fatalf("kaynak çözümü: %+v %v", src, ok)
	}
	// Kapının kayması = kapanmış dakika gecikmesi (özel kip: WindowMin + pay).
	now := time.Date(2026, 10, 3, 10, 30, 42, 0, time.UTC)
	if ok, lag := GroupNotifyGate(cfg, gLive, now); !ok || lag != 16*time.Minute+42*time.Second {
		t.Fatalf("live kayma: ok=%v lag=%v", ok, lag)
	}
	if ok, _ := GroupNotifyGate(cfg, gShadow, now); ok {
		t.Fatal("shadow kapıdan geçmez")
	}
	if ok, lag := GroupNotifyGate(cfg, chstore.ExceptionGroup{Fingerprint: "0a1b"}, now); !ok || lag != 0 {
		t.Fatal("span grubu kaymasız")
	}
	live.Enabled = false
	if GroupNotifies(Settings{Sources: []SourceConfig{live}}, gLive) {
		t.Fatal("kapalı kaynak bildirmez")
	}
}

// Kırılım tavanları: kanal/servis ≤ 12, grup ≤ 1000 (en eskiler düşer).
func TestExGroupStateCaps(t *testing.T) {
	m := map[string]uint64{}
	for i := 0; i < 30; i++ {
		m[string(rune('a'+i%26))+string(rune('0'+i/26))] = uint64(i)
	}
	if got := topBreakdown(m, exGroupMaxBreakdwn); len(got) != exGroupMaxBreakdwn || got["d1"] != 29 {
		t.Fatalf("tavan: %v", got)
	}
	st := &ExGroupState{Groups: map[string]*ExGroupBreakdown{}}
	for i := 0; i < 5; i++ {
		st.Groups[string(rune('a'+i))] = &ExGroupBreakdown{LastSeen: int64(i)}
	}
	pruneExGroupState(st, 3)
	if len(st.Groups) != 3 || st.Groups["a"] != nil || st.Groups["e"] == nil {
		t.Fatalf("budama en eskiyi atmalı: %v", st.Groups)
	}
}
