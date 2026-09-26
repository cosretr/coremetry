package api

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.949 — operatör 2026-09-26: "aynı anda farklı servislerden
// gelmiyorsa 5'ten düşük exception'ı göstermeye gerek yok". Varsayılan taban
// 5, çoklu-servis istisnalı; P1 gizlenemez (min(5, P1MinOccurrences)).

func TestEffectiveDefaultFloor(t *testing.T) {
	cases := []struct {
		p1   int
		want uint64
	}{
		{0, 5},   // ayarlanmamış → varsayılan 500 → 5
		{500, 5}, // varsayılan ayar hiçbir şeyi değiştirmez
		{5, 5},
		{3, 3}, // hacim-P1 kapısı 5'in altında: o gruplar P1, taban onları gizleyemez
		{1, 1},
	}
	for _, tc := range cases {
		cfg := chstore.DefaultExceptionTriage()
		cfg.P1MinOccurrences = tc.p1
		if got := effectiveDefaultFloor(cfg); got != tc.want {
			t.Errorf("P1MinOccurrences=%d → %d, want %d", tc.p1, got, tc.want)
		}
	}
	if got := effectiveDefaultFloor(chstore.DefaultExceptionTriage()); got != inboxDefaultMinOcc {
		t.Errorf("varsayılan ayar → %d, want inboxDefaultMinOcc (%d)", got, inboxDefaultMinOcc)
	}
}

func testSpread() *chstore.ExceptionSpread {
	return chstore.NewExceptionSpread(map[string]chstore.SpreadInfo{
		"multi": {Services: 3, Partners: []string{"svc-b", "svc-c"}, Occurrences: 2, LastSeen: 10},
	}, false)
}

func TestAnnotateExceptionSpread(t *testing.T) {
	sp := testSpread()
	items := []chstore.ExceptionGroup{{Fingerprint: "multi"}, {Fingerprint: "solo"}}
	annotateExceptionSpread(items, sp)
	if items[0].Spread != 3 || !reflect.DeepEqual(items[0].SpreadServices, []string{"svc-b", "svc-c"}) {
		t.Fatalf("multi işaretlenmeli: %+v", items[0])
	}
	if items[1].Spread != 0 || items[1].SpreadServices != nil {
		t.Fatalf("solo işaretlenmemeli: %+v", items[1])
	}
	// nil anlık görüntü (soft-fail) → dokunma.
	plain := []chstore.ExceptionGroup{{Fingerprint: "multi"}}
	annotateExceptionSpread(plain, nil)
	if plain[0].Spread != 0 {
		t.Fatal("nil yayılım işaret üretmemeli")
	}
	// Dönen dilim memo'yla paylaşılmaz.
	items[0].SpreadServices[0] = "bozuldu"
	again := []chstore.ExceptionGroup{{Fingerprint: "multi"}}
	annotateExceptionSpread(again, sp)
	if again[0].SpreadServices[0] != "svc-b" {
		t.Fatal("ortak dilimi paylaşılmamalı")
	}
}

func TestAnnotateInboxSpread(t *testing.T) {
	items := []InboxItem{
		{ID: "e", Kind: "exception", Exception: &InboxExceptionRef{Fingerprint: "multi"}},
		{ID: "h", Kind: "httperror", Exception: &InboxExceptionRef{Fingerprint: "multi"}},
		{ID: "s", Kind: "exception", Exception: &InboxExceptionRef{Fingerprint: "solo"}},
		{ID: "p", Kind: "problem"},
		{ID: "n", Kind: "exception"},
	}
	annotateInboxSpread(items, testSpread())
	if items[0].Exception.Spread != 3 || items[1].Exception.Spread != 3 {
		t.Fatalf("exception + httperror işaretlenmeli: %+v %+v", items[0].Exception, items[1].Exception)
	}
	if items[2].Exception.Spread != 0 {
		t.Fatal("tek servisli grup işaretlenmemeli")
	}
	annotateInboxSpread(items, nil) // nil güvenli
}

func TestExceptionPageBodyFloorFields(t *testing.T) {
	if got := floorHidden(10, 7); got != 3 {
		t.Fatalf("floorHidden(10,7)=%d, want 3", got)
	}
	if got := floorHidden(5, 7); got != 0 {
		t.Fatalf("yarış: tabansız sayım toplamdan küçük → 0, got %d", got)
	}
	pg := exceptionPage{Limit: 50, Offset: 100, Capped: true, MinOcc: 5, Hidden: 3, FloorDefault: true}
	b := pg.body([]chstore.ExceptionGroup{}, 7)
	want := map[string]any{
		"total": int64(7), "limit": 50, "offset": 100, "capped": true,
		"minOcc": uint64(5), "hiddenByMinOcc": int64(3), "floorDefault": true,
		"spreadWindowMin": chstore.DefaultExceptionTriage().StormWindowMinutes,
	}
	for k, v := range want {
		if !reflect.DeepEqual(b[k], v) {
			t.Errorf("body[%q]=%v (%T), want %v (%T)", k, b[k], b[k], v, v)
		}
	}
	if _, ok := b["items"]; !ok {
		t.Error("items alanı kaybolmamalı")
	}
	// v0.10.949 — soft-fail dürüstlüğü: yayılım okunamadıysa (SpreadOK=false)
	// yanıt bunu söyler; UI "çoklu-servis" dilini düşürür.
	if b["spreadAvailable"] != false {
		t.Errorf("SpreadOK=false → spreadAvailable=false, got %v", b["spreadAvailable"])
	}
	pg.SpreadOK = true
	if pg.body(nil, 0)["spreadAvailable"] != true {
		t.Error("SpreadOK=true → spreadAvailable=true")
	}
	if !strings.Contains(readSrc(t, "exception_priority_sort.go"), "pg.SpreadOK = sp != nil") {
		t.Error("listExceptionGroupsPage SpreadOK'u yayılım okumasından kurmalı")
	}
}

// Kablolama pinleri: cache anahtarı kipi taşır, rozet listeyle aynı kümeyi
// sayar, taban-altı çekimi istisnalı satırları iki kez eklemez, /problems
// floor=default'u sunucuda çözer.
func TestExceptionSpreadWiring(t *testing.T) {
	src := readSrc(t, "inbox.go")
	if !strings.Contains(src, `+ ":floorDefault=" + strconv.FormatBool(floorDefault)`) {
		t.Error("inbox cache anahtarı floorDefault taşımalı — varsayılan 5 ile açık ?minOcc=5 aynı minOcc'la farklı satır döner")
	}
	i := strings.Index(src, "func (s *Server) computeInboxCountFor(")
	if i < 0 {
		t.Fatal("computeInboxCountFor bulunamadı")
	}
	badge := src[i:]
	if j := strings.Index(badge, "\nfunc "); j > 0 {
		badge = badge[:j]
	}
	if strings.Count(badge, "MinOccurrences: badgeFloor,") != 2 || strings.Count(badge, "FloorExempt:    badgeExempt,") != 2 {
		t.Error("rozetin İKİ exception sayımı da etkin taban + istisnayı kullanmalı (v0.9.322 sözleşmesi)")
	}
	below := strings.Index(src, "MaxOccurrences: minOcc")
	dedupe := strings.Index(src, "if fetched[g.Fingerprint] {")
	if below < 0 || dedupe < below {
		t.Error("taban-altı çekimi üstte gelen (istisnalı) parmak izlerini atlamalı")
	}
	if !strings.Contains(src, "floorExempt = sp.ExemptBelow(minOcc)") {
		t.Error("istisna listesi yalnız varsayılan kipte kurulmalı")
	}

	helper := readSrc(t, "exception_priority_sort.go")
	for _, want := range []string{
		`if q.Get("floor") == "default" {`,
		"f.MinOccurrences = effectiveDefaultFloor(currentExceptionTriage())",
		"f.FloorExempt = sp.ExemptBelow(f.MinOccurrences)",
		"nf.MinOccurrences, nf.FloorExempt, nf.FloorExemptRegressed = 0, nil, false",
		"annotateExceptionSpread(items, sp)",
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("exception_priority_sort.go %q içermeli", want)
		}
	}
}

// ── v0.10.949 — regressed istisnası (operatör kararı 2026-09-26) ──────────
//
// Regressed grup (resolve edilmiş, sonra yeniden görülmüş → P2 "regressed")
// 5'in altında ve tek serviste de olsa VARSAYILAN görünümde kalır. Açık
// ?minOcc=N ve 0 ("show all") bugünkü gibi. Regressed, yayılımdan
// bağımsızdır (yayılım okunamasa da geçerli) ve bir satır tek sayaçta
// sayılır (regressed ÖNCE).

// regressedFloorEx — handler'ın kurulumunun aynası: normalizeInboxMinOcc +
// varsayılan kipte newFloorExemption(sp.ExemptBelow(...)).
func regressedFloorEx(raw string, spreadFPs []string) (uint64, floorExemption) {
	minOcc, def := normalizeInboxMinOcc(raw)
	var ex floorExemption
	if def {
		ex = newFloorExemption(spreadFPs)
	}
	return minOcc, ex
}

func TestApplyInboxMinOccRegressedExempt(t *testing.T) {
	exc := func(id, kind, state string, occ uint64) InboxItem {
		return InboxItem{ID: id, Kind: kind, Status: state, Exception: &InboxExceptionRef{Fingerprint: id, Occurrences: occ}}
	}
	cases := []struct {
		name          string
		raw           string   // ?minOcc= ham değeri ("" = varsayılan kip)
		spread        []string // ExemptBelow kümesi (nil = yayılım soft-fail)
		row           InboxItem
		wantKept      bool
		wantHidden    int
		wantBySpread  int
		wantRegressed int
	}{
		{"regressed tek servis 3 — varsayılan kipte görünür", "", nil, exc("r3", "exception", "regressed", 3), true, 0, 0, 1},
		{"regressed tek servis 3 — yayılım okunmuş, kümede değil", "", []string{"other"}, exc("r3", "exception", "regressed", 3), true, 0, 0, 1},
		{"regressed tek servis 3 — açık ?minOcc=5 gizler", "5", nil, exc("r3", "exception", "regressed", 3), false, 1, 0, 0},
		{"regressed tek servis 3 — açık ?minOcc=10 gizler", "10", nil, exc("r3", "exception", "regressed", 3), false, 1, 0, 0},
		{"regressed tek servis 3 — show all (0) görünür, sayaç yok", "0", nil, exc("r3", "exception", "regressed", 3), true, 0, 0, 0},
		{"regressed httperror 2 — tür kapısı yok", "", nil, exc("h2", "httperror", "regressed", 2), true, 0, 0, 1},
		{"regressed + çoklu-servis — regressed'e sayılır (tek sayaç)", "", []string{"rm"}, exc("rm", "exception", "regressed", 3), true, 0, 0, 1},
		{"regressed tabanın üstünde — istisna sayılmaz", "", nil, exc("r7", "exception", "regressed", 7), true, 0, 0, 0},
		{"new tek servis 3 — gizli", "", nil, exc("n3", "exception", "new", 3), false, 1, 0, 0},
		{"acknowledged tek servis 3 — gizli", "", nil, exc("a3", "exception", "acknowledged", 3), false, 1, 0, 0},
		{"resolved tek servis 3 — gizli", "", nil, exc("z3", "exception", "resolved", 3), false, 1, 0, 0},
		{"new çoklu-servis 3 — yayılımla görünür", "", []string{"m3"}, exc("m3", "exception", "new", 3), true, 0, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			minOcc, ex := regressedFloorEx(tc.raw, tc.spread)
			kept, hidden := applyInboxMinOcc([]InboxItem{tc.row}, minOcc, ex)
			if got := len(kept) == 1; got != tc.wantKept || hidden != tc.wantHidden {
				t.Fatalf("kept=%v hidden=%d, want kept=%v hidden=%d", got, hidden, tc.wantKept, tc.wantHidden)
			}
			bs, br := countFloorKept(kept, minOcc, ex)
			if bs != tc.wantBySpread || br != tc.wantRegressed {
				t.Fatalf("countFloorKept=(%d, %d), want (%d, %d)", bs, br, tc.wantBySpread, tc.wantRegressed)
			}
		})
	}
}

// Liste, gizli sayı ve kept sayaçları AYNI kuraldan: bir karışık küme
// üstünde varsayılan kip ↔ açık ?minOcc=5. /problems gizli sayısı
// (floorHidden: tabansız sayım − tabanlı toplam) Go ayrımının hidden'ı ile
// aynı çıkmalı — SQL tarafı aynı istisnayı (FloorExemptRegressed +
// FloorExempt) taşıdığı sürece (bkz. TestRegressedFloorExemptWiring).
func TestRegressedFloorCountsAgree(t *testing.T) {
	mk := func() []InboxItem {
		return []InboxItem{
			{ID: "reg-3", Kind: "exception", Status: "regressed", Exception: &InboxExceptionRef{Fingerprint: "reg-3", Occurrences: 3}},
			{ID: "reg-http-1", Kind: "httperror", Status: "regressed", Exception: &InboxExceptionRef{Fingerprint: "reg-http-1", Occurrences: 1}},
			{ID: "solo-3", Kind: "exception", Status: "new", Exception: &InboxExceptionRef{Fingerprint: "solo-3", Occurrences: 3}},
			{ID: "multi-2", Kind: "exception", Status: "acknowledged", Exception: &InboxExceptionRef{Fingerprint: "multi-2", Occurrences: 2}},
			{ID: "big-9", Kind: "exception", Status: "new", Exception: &InboxExceptionRef{Fingerprint: "big-9", Occurrences: 9}},
			{ID: "prob", Kind: "problem"},
		}
	}
	spread := []string{"multi-2"}
	cases := []struct {
		raw                              string
		wantIDs                          string
		wantHidden, wantSpread, wantRegr int
	}{
		{"", "reg-3reg-http-1multi-2big-9prob", 1, 1, 2}, // varsayılan: yalnız solo-3 gizli
		{"5", "big-9prob", 4, 0, 0},                      // açık 5: istisnasız
		{"0", "reg-3reg-http-1solo-3multi-2big-9prob", 0, 0, 0},
	}
	for _, tc := range cases {
		minOcc, ex := regressedFloorEx(tc.raw, spread)
		all := mk()
		kept, hidden := applyInboxMinOcc(mk(), minOcc, ex)
		bs, br := countFloorKept(kept, minOcc, ex)
		if got := inboxIDs(kept); got != tc.wantIDs || hidden != tc.wantHidden || bs != tc.wantSpread || br != tc.wantRegr {
			t.Errorf("minOcc=%q: kept=%q hidden=%d spread=%d regressed=%d, want %q %d %d %d",
				tc.raw, got, hidden, bs, br, tc.wantIDs, tc.wantHidden, tc.wantSpread, tc.wantRegr)
		}
		// /problems gizli sayısının aritmetiği aynı kümede aynı sayıyı verir.
		if got := floorHidden(int64(len(all)), int64(len(kept))); got != int64(hidden) {
			t.Errorf("minOcc=%q: floorHidden=%d, Go hidden=%d — sayımlar ayrıştı", tc.raw, got, hidden)
		}
	}
}

// "Regressed" olgusunun TEK kaynağı: öncelik merdiveninin P2 "regressed"
// dalı ile taban istisnası aynı yüklemi (exceptionIsRegressed → state
// kolonu) kullanır. Her durum için: taban-altı bir grup öncelikte
// "regressed" gerekçesini alıyorsa istisnada da regressed sayılır, almıyorsa
// sayılmaz. exceptionToInbox Status'u g.State'ten taşır (zincirin halkası).
func TestRegressedSameSourceAsPriority(t *testing.T) {
	cfg := chstore.DefaultExceptionTriage()
	now := time.Now()
	ex := newFloorExemption(nil)
	for _, state := range []string{
		chstore.ExStateNew, chstore.ExStateAcknowledged, chstore.ExStateResolved,
		chstore.ExStateRegressed, chstore.ExStateIgnored,
	} {
		g := chstore.ExceptionGroup{
			Fingerprint: "fp-" + state, Type: "java.lang.IllegalStateException",
			Message: "boom", Service: "svc-a", State: state, Occurrences: 3,
			FirstSeen: now.Add(-3 * time.Hour).UnixNano(), LastSeen: now.Add(-2 * time.Hour).UnixNano(),
		}
		prio, reason := exceptionPriorityAt(g, cfg, now)
		prioRegressed := reason == "regressed"
		floorRegressed := ex.reason(exceptionToInbox(g)) == floorKeptRegressed
		if prioRegressed != floorRegressed {
			t.Errorf("state=%s: öncelik (%s %q) ile taban istisnası (%v) ayrıştı", state, prio, reason, floorRegressed)
		}
		if state == chstore.ExStateRegressed && (prio != "P2" || !floorRegressed) {
			t.Errorf("regressed 3'lük grup P2 \"regressed\" + tabandan muaf olmalı: %s %q, muaf=%v", prio, reason, floorRegressed)
		}
	}
}

// Kablolama: regressed istisnası HER yolda — üst (taban) SQL çekimi, iki
// chip sayımı, rozetin iki sayımı, /problems floor=default (+ gizli sayı
// için tabansız sayımda temizlenir); taban-altı çekimde YOK (occurrences
// bölüşümü korunur, üstte gelen parmak izi tekilleşir). Öncelik merdiveni
// aynı yüklemi kullanır; yanıt keptRegressed'i taşır.
func TestRegressedFloorExemptWiring(t *testing.T) {
	src := readSrc(t, "inbox.go")
	if n := strings.Count(src, "FloorExemptRegressed: floorDefault,"); n != 3 {
		t.Errorf("inbox.go FloorExemptRegressed: floorDefault %d kez, want 3 (2 chip sayımı + üst çekim)", n)
	}
	below := strings.Index(src, "MaxOccurrences: minOcc")
	if below < 0 {
		t.Fatal("taban-altı çekimi bulunamadı")
	}
	belowEnd := strings.Index(src[below:], "})")
	if belowEnd < 0 || strings.Contains(src[below:below+belowEnd], "FloorExemptRegressed") {
		t.Error("taban-altı çekimi regressed bayrağı taşımamalı (bölüşüm occurrences üstünden)")
	}
	i := strings.Index(src, "func (s *Server) computeInboxCountFor(")
	if i < 0 {
		t.Fatal("computeInboxCountFor bulunamadı")
	}
	badge := src[i:]
	if j := strings.Index(badge, "\nfunc "); j > 0 {
		badge = badge[:j]
	}
	if n := strings.Count(badge, "FloorExemptRegressed: true,"); n != 2 {
		t.Errorf("rozetin iki exception sayımı regressed istisnasını taşımalı: %d, want 2", n)
	}
	if !strings.Contains(src, "if exceptionIsRegressed(g.State) {") || strings.Contains(src, `g.State == "regressed"`) {
		t.Error("öncelik merdiveni regressed'i exceptionIsRegressed ile okumalı (taban istisnasıyla tek kaynak)")
	}
	if !strings.Contains(src, `"keptRegressed":   keptRegressed,`) {
		t.Error("inbox yanıtı keptRegressed taşımalı")
	}
	helper := readSrc(t, "exception_priority_sort.go")
	blk := helper[strings.Index(helper, `if q.Get("floor") == "default" {`):]
	blk = blk[:strings.Index(blk, "\n\t}")]
	if !strings.Contains(blk, "f.FloorExemptRegressed = true") {
		t.Error("/problems floor=default regressed istisnasını açmalı")
	}
	if !strings.Contains(helper, "nf.MinOccurrences, nf.FloorExempt, nf.FloorExemptRegressed = 0, nil, false") {
		t.Error("gizli sayı için tabansız sayım istisnaları temizlemeli")
	}
	spreadSrc := readSrc(t, "exception_spread.go")
	if !strings.Contains(spreadSrc, "return state == chstore.ExStateRegressed") {
		t.Error("exceptionIsRegressed state kolonunu chstore.ExStateRegressed ile okumalı")
	}
}
