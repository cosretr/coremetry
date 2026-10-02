package evaluator

// selfhealth_disk_eta_switch_test.go — v0.10.1031 (operatör 2026-10-02:
// "Disk dolacak niye geliyor, gerek yok."). self-disk-eta varsayılan
// KAPALI (SelfHealthConfig.DiskEta, nil = kapalı).
//
// İki sözleşme pinlenir:
//  1. diskETAProblems (SAF): kapalıyken HİÇBİR satır üretmez — ne ETA
//     dalı ne ısınma taşıması (diskCarryOver). Taşıma kapının dışında
//     kalsaydı lider değişiminden sonraki yarım saatte kapalı kuralın
//     açık satırı yaşamaya devam ederdi.
//  2. selfDiskETA (kaynak pini): anahtar ÖLÇÜMDEN SONRA okunur — disk
//     okuması, kalıcı seri yazımı (/admin/stats rozeti, v0.10.911) ve
//     bellek serisi kapalıyken de sürer; kapalıyken ok=true döner ki
//     reconcileSelfHealth açık satırları bir sonraki tikte kapatsın.

import (
	"math"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestDiskETAProblemsSwitch(t *testing.T) {
	gib := float64(uint64(1) << 30)
	const capBytes = uint64(100) << 30
	disk := chstore.DiskFree{Host: "ch-1", Disk: "default", Total: capBytes, Free: capBytes / 2}
	id := selfDiskRuleID + ":" + chstore.DiskKey(disk.Host, disk.Disk)

	// 50 GiB'den günde 10 GiB büyüyen 70 dk'lık seri → ETA ≈ 4.95 gün
	// (TestDiskETADays'in ilk vakası) — 7 günlük eşiğin altında.
	breach := map[string][]diskSample{chstore.DiskKey(disk.Host, disk.Disk): diskSeries(8, 600, 50*gib, 10*gib/86400)}
	// Günde 4 GiB → ETA ≈ 12.4 gün — eşiğin üstünde.
	calm := map[string][]diskSample{chstore.DiskKey(disk.Host, disk.Disk): diskSeries(8, 600, 50*gib, 4*gib/86400)}
	// Isınma: yeni liderin ilk tiki (bellek serisi tek nokta).
	warm := map[string][]diskSample{chstore.DiskKey(disk.Host, disk.Disk): diskSeries(1, 600, 50*gib, 0)}

	openRow := chstore.NewOpenProblems([]chstore.Problem{{
		ID: id, RuleID: chstore.SelfDiskRuleID, RuleName: "Coremetry · disk dolacak",
		Metric: "self.disk_eta_days", Severity: "warning", Status: "open",
		Value: 6.83, Threshold: 7, Description: "ch-1 · default diski 6.8 gün içinde DOLACAK",
	}})

	cfgWith := func(on *bool) chstore.SelfHealthConfig {
		c := chstore.DefaultSelfHealth()
		c.DiskEta = on
		return c
	}
	on, off := true, false

	tests := []struct {
		name      string
		cfg       chstore.SelfHealthConfig
		series    map[string][]diskSample
		snap      *chstore.OpenProblems
		wantIDs   []string
		wantValue float64 // yalnız tek satırlı vakalarda; 0 = bakma
	}{
		// ── KAPALI (v0.10.1031 varsayılanı) ──
		{"varsayılan (nil) + eşik altı ETA → satır YOK", cfgWith(nil), breach, nil, nil, 0},
		{"açıkça false + eşik altı ETA → satır YOK", cfgWith(&off), breach, nil, nil, 0},
		// Asıl tuzak: ısınma taşıması kapının ARDINDA kalmalı.
		{"kapalı + ısınma + açık satır → taşıma YOK (satır kapanacak)", cfgWith(nil), warm, openRow, nil, 0},
		{"kapalı + eşik altı ETA + açık satır → tazeleme YOK", cfgWith(nil), breach, openRow, nil, 0},

		// ── AÇIK (operatör anahtarı açtı) — v0.10.901 davranışı aynen ──
		{"açık + eşik altı ETA → satır AÇILIR", cfgWith(&on), breach, nil, []string{id}, 0},
		{"açık + eşik üstü ETA → satır YOK", cfgWith(&on), calm, nil, nil, 0},
		{"açık + ısınma + açık satır → son değerle TAŞINIR", cfgWith(&on), warm, openRow, []string{id}, 6.83},
		{"açık + ısınma + satır yok → sessiz", cfgWith(&on), warm, nil, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diskETAProblems(tt.cfg, []chstore.DiskFree{disk}, tt.series, tt.snap)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("satır sayısı %d, beklenen %d: %+v", len(got), len(tt.wantIDs), got)
			}
			for i, w := range tt.wantIDs {
				if got[i].id != w || got[i].ruleID != selfDiskRuleID {
					t.Fatalf("satır %d = %s/%s, beklenen %s", i, got[i].id, got[i].ruleID, w)
				}
				if got[i].comparator != "<" {
					t.Fatalf("comparator %q — kural sözleşmesi \"<\"", got[i].comparator)
				}
			}
			if tt.wantValue != 0 && math.Abs(got[0].value-tt.wantValue) > 1e-9 {
				t.Fatalf("taşınan değer %v, beklenen %v", got[0].value, tt.wantValue)
			}
		})
	}

	// Stat edilemeyen disk anahtar açıkken de satır üretmez (mevcut kapı).
	bad := chstore.DiskFree{Host: "ch-1", Disk: "default", Total: 0, Free: 0}
	if got := diskETAProblems(cfgWith(&on), []chstore.DiskFree{bad}, breach, openRow); len(got) != 0 {
		t.Fatalf("stat edilemeyen disk satır üretti: %+v", got)
	}
}

// funcBody — kaynakta `sig` ile başlayan üst düzey fonksiyonun gövdesi:
// imzadan, sütun 0'daki ilk kapanan paranteze ("\n}\n") dek. gofmt üst
// düzey kapanışı sütun 0'a, iç blokları girintili yazar; bir sonraki
// fonksiyonun doc yorumu gövdeye karışmaz.
func funcBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("kaynakta %q yok", sig)
	}
	body := src[i:]
	j := strings.Index(body, "\n}\n")
	if j < 0 {
		t.Fatalf("%q gövdesinin sonu bulunamadı", sig)
	}
	return body[:j+2]
}

// codeOnly — yorum satırlarını atar ("return" kelimesi bir yorumda
// geçerse sayım şaşmasın).
func codeOnly(body string) string {
	var b strings.Builder
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	reReturn     = regexp.MustCompile(`\breturn\b`)
	reRawDiskEta = regexp.MustCompile(`\.DiskEta\b`) // DiskEtaDays / DiskEtaOn eşleşmez
)

func TestSelfDiskETASwitchAfterSeriesWrite(t *testing.T) {
	b, err := os.ReadFile("selfhealth.go")
	if err != nil {
		t.Fatalf("selfhealth.go okunamadı: %v", err)
	}
	src := string(b)

	body := funcBody(t, src, "func (e *Evaluator) selfDiskETA(")
	read := strings.Index(body, "e.store.CollectDisks(ctx)")
	failed := strings.Index(body, "return nil, false")
	write := strings.Index(body, "e.store.InsertMetrics(ctx, pts)")
	upkeep := strings.Index(body, "e.diskSeries[k] = s")
	decide := strings.Index(body, "return diskETAProblems(cfg, disks, series, snap), true")
	for name, idx := range map[string]int{
		"disk okuması": read, "okuma hatası dalı": failed, "kalıcı seri yazımı": write,
		"bellek serisi bakımı": upkeep, "karar (ok=true)": decide,
	} {
		if idx < 0 {
			t.Fatalf("selfDiskETA'da %s bulunamadı — sözleşme değişti mi?", name)
		}
	}
	// Okuma hatası bugünkü gibi: nil,false (kapsanmaz, hiçbir şey kapanmaz),
	// ve TEK erken dönüş o.
	if strings.Count(body, "return nil, false") != 1 || !(read < failed && failed < write) {
		t.Fatal("selfDiskETA'da okuma hatası dışında erken dönüş var ya da sıra bozuk")
	}
	// Anahtar ölçümden SONRA: kalıcı seri ve bellek serisi karardan önce.
	if !(write < decide && upkeep < decide) {
		t.Fatal("karar kalıcı seri yazımından / bellek serisi bakımından ÖNCE — " +
			"kural kapalıyken /admin/stats rozeti beslenmez, açılınca seri boş başlar")
	}
	// selfDiskETA anahtarı kendisi okumaz (okursa erken dönüş riski) — kapı
	// saf çekirdekte. Ham alan da yasak: `if cfg.DiskEta == nil || …
	// { return nil, true }` CollectDisks'ten önce dursa kalıcı seri yazılmaz
	// ve "DiskEtaOn" aramasından kaçardı (inceleme bulgusu, v0.10.1031).
	code := codeOnly(body)
	if strings.Contains(code, "DiskEtaOn") || reRawDiskEta.MatchString(code) {
		t.Fatal("selfDiskETA anahtarı (DiskEtaOn ya da ham .DiskEta) doğrudan okuyor — kapı diskETAProblems'ta olmalı")
	}
	// Tam İKİ dönüş: okuma hatası (nil,false) ve son karar (…, true). Üçüncü
	// bir dönüş, seri yazımını atlayabilen bir erken çıkıştır.
	if n := len(reReturn.FindAllStringIndex(code, -1)); n != 2 {
		t.Fatalf("selfDiskETA'da %d return var, 2 bekleniyordu (okuma hatası + son karar)", n)
	}

	core := funcBody(t, src, "func diskETAProblems(")
	gate := strings.Index(core, "if !cfg.DiskEtaOn() {")
	carry := strings.Index(core, "diskCarryOver(")
	eta := strings.Index(core, "diskETADays(")
	if gate < 0 || carry < 0 || eta < 0 {
		t.Fatalf("diskETAProblems gövdesi beklenen parçaları taşımıyor (kapı %d, taşıma %d, eta %d)", gate, carry, eta)
	}
	if !(gate < eta && gate < carry) {
		t.Fatal("anahtar kapısı ETA ya da taşıma dalından SONRA — kapalı kural satır taşıyabilir")
	}
}
