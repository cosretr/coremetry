package sre

// sre_fixture_eval_test.go — v0.10.2492 (sre-investigation-audit R1):
// fikstür tabanlı, LLM'siz inceleme ölçümü.
//
// SÖZLEŞME: donmuş problem + donmuş okuyucu sonuçları (testdata/fixture_eval/
// *.json) → üretimdeki AYNI deterministik hat (runInvestigation, Kind=replay:
// planSteps + oyun kitabı, adım dönüştürücüleri, Score, bantlar, erişim
// beyanı, kanıt kapısı; anlatım YOK, model çağrısı YOK) → yapısal sonuç.
// ClickHouse / ES / Redis / ağ yok: okuyucular readers dikişine (zaten var
// olan test dikişi, orchestrator_test.go okReaders ile aynı) fikstürden
// bağlanır. Ürün kodu değişmedi.
//
// Beklenti YAPISALDIR (S6 a; anahtar kelime eşleşmesi yok): okunan adımlar,
// errors adımının erişim durumu, en olası hipotez türü / kategorisi, bandı,
// kapı kararı, kör nokta satırları, mesaj sınıfı.
//
// Her vakanın iki sütunu var:
//   - today  — bugünkü kodun GERÇEK sonucu. Test bunu doğrular; kayarsa kırmızı.
//   - target — onaylı programın (R2–R4) istediği. Test yalnız boşluk tablosu
//     yazar (go test -v -run TestSREFixtureEval), hedef tutmadı diye KIRILMAZ.
//
// Sonraki dilim bir satırı nasıl çevirir: R2 (errors adımı) / R3 (mesaj
// sınıfı) / R4 (bant tavanı, kör nokta ayrımı) davranışı değiştirince bu test
// `today` kayması diye kırmızıya düşer. Dilim aynı commit'te ilgili vakanın
// `today` alanını yeni gerçek sonuçla günceller (çıktıdaki "got" satırı) ve
// boşluk tablosunda o hücre "tuttu" olur. `target` yalnız operatör kararıyla
// değişir (ör. S7 sözlük kararı). Kategori sözlüğü değişmedi (S7 c):
// business_rule / data yalnız target sütununda dize olarak geçer.
//
// Hedef sütunu sözdizimi: "" = beklenti yok · "≤orta" = bant en çok orta ·
// "⊇a,b" = liste a ve b'yi içerir · başka her şey = eşitlik.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// fxStart — fikstürlerin çıpası (log artışı penceresi bu ana göre yazıldı).
var fxStart = time.Date(2026, 3, 10, 2, 0, 0, 0, time.UTC)

// fixtureReads — tek vakanın donmuş okuyucu sonuçları.
type fixtureReads struct {
	About      string                      `json:"about"`
	Problem    chstore.Problem             `json:"problem"`
	Sweep      *mcptools.SweepResult       `json:"sweep"`
	Deploys    *mcptools.DeployCheck       `json:"deploys"`
	Edges      []chstore.ServiceEdge       `json:"edges"`
	Changes    []chstore.ChangedService    `json:"changes"`
	Exemplar   string                      `json:"exemplarTraceId"`
	Pivot      *mcptools.PivotResult       `json:"pivot"`
	Spike      *mcptools.LogSpikeResult    `json:"spike"`
	Events     []mcptools.ProblemWindowRow `json:"events"`
	ErrorOps   []chstore.ErrorRank         `json:"errorOps"`
	Exceptions []chstore.ExceptionRow      `json:"exceptions"`
}

// fxOutcome — yapısal sonuç (today) ya da beklenti (target).
type fxOutcome struct {
	Steps    string // okunan adımlar (checked / partial), alfabetik
	Errors   string // hata mesajını okuyan errors adımının erişim durumu
	TopKind  string // en olası hipotez türü
	Category string // en olası hipotezin vaka kategorisi
	Band     string // en olası hipotezin bandı
	Gate     string // kanıt kapısı kararı
	Blind    string // kör noktalar: checked/partial dışı erişim satırları "kaynak:durum", alfabetik
	Class    string // mesaj sınıfı (kayıtta alan yok → "—"; R3 ekler)
}

func (o fxOutcome) fields() [][2]string {
	return [][2]string{{"adımlar", o.Steps}, {"errors", o.Errors}, {"en olası", o.TopKind}, {"kategori", o.Category},
		{"bant", o.Band}, {"kapı", o.Gate}, {"kör", o.Blind}, {"sınıf", o.Class}}
}

type fxCase struct {
	name   string
	file   string
	today  fxOutcome
	target fxOutcome
}

var fixtureEvalCases = []fxCase{
	{
		// 1 — %12 hata, mesaj beklenen iş kuralı reddi (bakiye yetersiz).
		name: "orders_is_kurali", file: "orders_business_reject.json",
		today: fxOutcome{
			Steps: "deploys,deps,events,log_spike,pivot,sweep", Errors: "not_run",
			// Desteklenen hipotez yok (kapı no_supported); "en olası" yalnız
			// öncülden kalan external 0.10 — skor sırası, kanıt değil.
			TopKind: "external", Category: "external", Band: "düşük", Gate: "insufficient",
			Blind: "errors:not_run", Class: "—",
		},
		target: fxOutcome{Steps: "⊇errors", Errors: "checked", Category: "business_rule", Band: "≤orta", Class: "business_rule"},
	},
	{
		// 2 — günlük toplu iş, %100 uyum beklemesi / kuyruğa erteleme; hacim çok düşük.
		name: "batch_settle_uyum", file: "batch_settle_compliance_hold.json",
		today: fxOutcome{
			Steps: "deploys,deps,events,log_spike,pivot,sweep", Errors: "not_run",
			TopKind: "code_regression", Category: "code_bug", Band: "düşük", Gate: "insufficient",
			Blind: "errors:not_run", Class: "—",
		},
		target: fxOutcome{Steps: "⊇errors", Errors: "checked", Category: "business_rule", Band: "≤orta", Class: "business_rule"},
	},
	{
		// 3 — kalite kapısı düşer, toplu iş sürer, rapor adımları başarılı; risk devam etmesi.
		name: "inventory_kalite_kapisi", file: "inventory_quality_gate_continues.json",
		today: fxOutcome{
			Steps: "deploys,deps,events,log_spike,pivot,sweep", Errors: "not_run",
			TopKind: "code_regression", Category: "code_bug", Band: "düşük", Gate: "insufficient",
			Blind: "errors:not_run", Class: "—",
		},
		// Sınıf hedefi audit'te yok (R3 çıkışı yalnız vaka 1, 2, 4'ü adlandırır).
		target: fxOutcome{Steps: "⊇errors", Errors: "checked", Category: "data", Band: "≤orta"},
	},
	{
		// 4 — hata oranı yüksek, log yok, trace saklama dışı, istisna grubu yok.
		name: "payments_kor_nokta", file: "payments_blind_spot.json",
		today: fxOutcome{
			Steps: "deploys,deps,events,sweep", Errors: "not_run",
			TopKind: "external", Category: "external", Band: "düşük", Gate: "insufficient",
			Blind: "errors:not_run,log_spike:no_data,pivot:no_data", Class: "—",
		},
		target: fxOutcome{Steps: "⊇errors", Errors: "checked", Band: "≤orta", Gate: "insufficient",
			Blind: "⊇log_spike:empty_source,pivot:empty_source", Class: "unclear"},
	},
}

func loadFixture(t *testing.T, file string) fixtureReads {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fixture_eval", file))
	if err != nil {
		t.Fatal(err)
	}
	var fx fixtureReads
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	fx.Problem.StartedAt = fxStart.UnixNano()
	return fx
}

// fixtureReaders — okuyucular fikstürden; errors adımının okuyucuları
// (errorOps / exceptions) da bağlı: bugün plan onları çağırmıyorsa sayaç 0
// kalır, R2 açınca aynı fikstür mesajı verir.
func fixtureReaders(fx fixtureReads, c *calls) readers {
	return readers{
		problem: func(context.Context, string) (*chstore.Problem, error) {
			p := fx.Problem
			return &p, nil
		},
		sweep: func(context.Context, mcptools.SweepRequest) (mcptools.SweepResult, error) {
			c.hit("sweep")
			if fx.Sweep == nil {
				return mcptools.SweepResult{}, fmt.Errorf("fikstürde tarama yok")
			}
			return *fx.Sweep, nil
		},
		deploys: func(context.Context, string, time.Time, time.Time) (mcptools.DeployCheck, error) {
			c.hit("deploys")
			if fx.Deploys == nil {
				return mcptools.DeployCheck{}, fmt.Errorf("fikstürde deploy yok")
			}
			return *fx.Deploys, nil
		},
		graph: func(context.Context, string, time.Time, time.Time) ([]chstore.ServiceEdge, error) {
			return fx.Edges, nil
		},
		changes: func(context.Context, time.Time, int) ([]chstore.ChangedService, error) {
			return fx.Changes, nil
		},
		exemplar: func(context.Context, chstore.ExemplarReq) (*chstore.Exemplar, error) {
			if fx.Exemplar == "" {
				return nil, nil
			}
			return &chstore.Exemplar{TraceID: fx.Exemplar}, nil
		},
		pivot: func(context.Context, mcptools.PivotRequest) (mcptools.PivotResult, error) {
			c.hit("pivot")
			if fx.Pivot == nil {
				return mcptools.PivotResult{}, fmt.Errorf("fikstürde trace yok")
			}
			return *fx.Pivot, nil
		},
		spike: func(context.Context, mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error) {
			c.hit("spike")
			if fx.Spike == nil {
				return mcptools.LogSpikeResult{}, fmt.Errorf("fikstürde log yok")
			}
			return *fx.Spike, nil
		},
		events: func(context.Context, string, time.Time, time.Time, int) (mcptools.ProblemWindowData, error) {
			return mcptools.ProblemWindowData{Rows: fx.Events}, nil
		},
		incident: func(context.Context, string) (string, int64, error) { return "", 0, nil },
		errorOps: func(context.Context, string, time.Time, time.Time, int) ([]chstore.ErrorRank, error) {
			c.hit("errorOps")
			return fx.ErrorOps, nil
		},
		exceptions: func(context.Context, chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) {
			c.hit("exceptions")
			return fx.Exceptions, nil
		},
		settings: func() Settings { return NormalizeSettings(DefaultSettings()) },
		pack:     func() ContextPackSettings { return DefaultContextPackSettings() },
	}
}

// runFixture — değerlendirme tekrarıyla aynı yol (Kind=replay, anlatım yok).
func runFixture(t *testing.T, fx fixtureReads) (*Investigation, *calls) {
	t.Helper()
	c := &calls{}
	inv := &Investigation{ProblemID: fx.Problem.ID, DisplayID: "P-FX", Service: fx.Problem.Service, Env: "prod", Priority: "P1",
		Kind: KindReplay, State: StateRunning, ProblemAt: fxStart.UnixNano(), AsOf: fxStart.Add(30 * time.Minute).UnixNano()}
	wall := fxStart.Add(2 * time.Hour)
	runInvestigation(context.Background(), fixtureReaders(fx, c), inv, func(*Investigation) {}, func() time.Time { return wall })
	return inv, c
}

// fxMessageClass — kayıtta mesaj sınıfı alanı yok (R3 gölgede ekleyecek);
// R3 bu işlevi kayıttaki alanı okuyacak şekilde değiştirir.
func fxMessageClass(*Investigation) string { return "—" }

// outcomeOf — SAF: kayıt → yapısal sonuç.
func outcomeOf(inv *Investigation) fxOutcome {
	var read, blind []string
	for _, r := range inv.Reach {
		if r.Status == ReachChecked || r.Status == ReachPartial {
			read = append(read, r.Source)
		} else {
			blind = append(blind, r.Source+":"+r.Status)
		}
	}
	sort.Strings(read)
	sort.Strings(blind)
	o := fxOutcome{Steps: strings.Join(read, ","), Errors: reachOf(inv.Reach, stepErrors), Blind: strings.Join(blind, ","),
		Class: fxMessageClass(inv)}
	if len(inv.Hypotheses) > 0 {
		h := inv.Hypotheses[0]
		o.TopKind, o.Category, o.Band = h.Kind, h.Category, h.Band
	}
	if inv.Coverage != nil {
		o.Gate = inv.Coverage.Verdict
	}
	return o
}

var bandRank = map[string]int{BandLow: 0, BandMedium: 1, BandHigh: 2}

// meets — SAF: bugünkü değer hedefi karşılıyor mu (hedef sözdizimi dosya başında).
func meets(today, target string) bool {
	switch {
	case strings.HasPrefix(target, "≤"):
		r, ok := bandRank[today]
		return ok && r <= bandRank[strings.TrimPrefix(target, "≤")]
	case strings.HasPrefix(target, "⊇"):
		have := map[string]bool{}
		for _, s := range strings.Split(today, ",") {
			have[s] = true
		}
		for _, s := range strings.Split(strings.TrimPrefix(target, "⊇"), ",") {
			if !have[s] {
				return false
			}
		}
		return true
	}
	return today == target
}

func TestSREFixtureEvalMeets(t *testing.T) {
	for _, tc := range []struct {
		today, target string
		want          bool
	}{
		{BandLow, "≤orta", true}, {BandMedium, "≤orta", true}, {BandHigh, "≤orta", false}, {"", "≤orta", false},
		{"a,b,c", "⊇a,c", true}, {"a,b", "⊇a,d", false},
		{"code_bug", "business_rule", false}, {"unclear", "unclear", true},
	} {
		if got := meets(tc.today, tc.target); got != tc.want {
			t.Errorf("meets(%q, %q) = %v", tc.today, tc.target, got)
		}
	}
}

// TestSREFixtureEval — dört vaka: today sapmazsa yeşil; boşluk tablosu -v ile.
func TestSREFixtureEval(t *testing.T) {
	var table strings.Builder
	met, total := 0, 0
	fmt.Fprintf(&table, "\n%-24s %-9s %-46s %-42s %s\n", "vaka", "alan", "today", "target", "durum")
	for _, tc := range fixtureEvalCases {
		fx := loadFixture(t, tc.file)
		inv, c := runFixture(t, fx)
		got := outcomeOf(inv)
		if got != tc.today {
			t.Errorf("%s: today kaydı (sonraki dilim bilerek değiştirdiyse today'i güncelle)\n got  %+v\n want %+v", tc.name, got, tc.today)
		}
		// Bugün errors adımı planlanmıyor → hata mesajı okuyucuları hiç çağrılmaz.
		if tc.today.Errors == ReachNotRun && (c.get("errorOps") != 0 || c.get("exceptions") != 0) {
			t.Errorf("%s: errors adımı not_run ama okuyucu çağrıldı: %v", tc.name, c.n)
		}
		if inv.Summary == nil || inv.Summary.Text != ReplayNarrationText {
			t.Errorf("%s: model çağrısı yolu açılmamalı (anlatım = tekrar metni): %+v", tc.name, inv.Summary)
		}
		gf, tf := got.fields(), tc.target.fields()
		for i := range gf {
			if tf[i][1] == "" {
				continue
			}
			total++
			st := "TUTMADI"
			if meets(gf[i][1], tf[i][1]) {
				met++
				st = "tuttu"
			}
			fmt.Fprintf(&table, "%-24s %-9s %-46s %-42s %s\n", tc.name, gf[i][0], gf[i][1], tf[i][1], st)
		}
	}
	fmt.Fprintf(&table, "hedef: %d/%d tuttu\n", met, total)
	t.Log(table.String())
}

// TestSREFixtureEvalNeutralNames — ad korkuluğu: fikstürde yalnız nötr
// servis adları (checkout, orders, payments, inventory, batch-settle ve
// bunların db:/ext:/q: türevleri).
func TestSREFixtureEvalNeutralNames(t *testing.T) {
	allowed := []string{"checkout", "orders", "payments", "inventory", "batch-settle", "settle-"}
	ok := func(name string) bool {
		for _, p := range []string{"db:", "ext:", "q:"} {
			name = strings.TrimPrefix(name, p)
		}
		for _, a := range allowed {
			if strings.HasPrefix(name, a) {
				return true
			}
		}
		return false
	}
	for _, tc := range fixtureEvalCases {
		fx := loadFixture(t, tc.file)
		names := []string{fx.Problem.Service}
		for _, e := range fx.Edges {
			names = append(names, e.Source, e.Target)
		}
		for _, ch := range fx.Changes {
			names = append(names, ch.Service)
		}
		for _, x := range fx.Exceptions {
			names = append(names, x.Service)
		}
		for _, ev := range fx.Events {
			names = append(names, ev.Service)
		}
		for _, n := range names {
			if n != "" && !ok(n) {
				t.Errorf("%s: nötr olmayan servis adı %q", tc.file, n)
			}
		}
	}
}
