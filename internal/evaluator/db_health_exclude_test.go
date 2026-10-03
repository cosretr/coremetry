package evaluator

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1084 — db-health hariç sistemler (operatör: "%100 hata oranı gerçek
// değil"). Prod'da birkaç Couchbase veritabanı %100 / %67 / %11 hata oranıyla
// P1 açıyordu: SDK KV "bulunamadı" cevabını span'de ERROR işaretliyor. Pinler:
// açık hariç satırın tek seferlik "system excluded" kapanışı (normal yol,
// incident kaskadı görür), hariç olmayanın dokunulmazlığı, Couchbase senaryosu
// uçtan uca (okuma hariç sistemi SQL'de düşürür → karar yok → problem yok),
// evaluateDBHealth'in sırası.

const cbID = "db-health:couchbase@cb-node-1/orders"

func cbRow(bucketAgo int, calls, errs uint64) chstore.DBHealthBucket {
	r := dbhRow(bucketAgo, calls, errs, 5, 3)
	r.DBSystem, r.Instance, r.DBName = "couchbase", "cb-node-1", "orders"
	return r
}

func TestDBHealthSplitExcluded(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	open := []*chstore.Problem{
		{ID: cbID, RuleID: cbID, Status: "open"},
		{ID: dbhID, RuleID: dbhID, Status: "acknowledged"},
		{ID: "db-health:COUCHBASE@cb-node-2/carts", RuleID: "db-health:COUCHBASE@cb-node-2/carts", Status: "open"},
		{ID: "db-health:bozuk", RuleID: "db-health:bozuk", Status: "open"},
	}
	ex, kept := dbHealthSplitExcluded(open, cfg)
	if len(ex) != 2 || ex[0].ID != cbID || ex[1].ID != "db-health:COUCHBASE@cb-node-2/carts" {
		t.Errorf("couchbase satırları (harf duyarsız) hariç olmalı: %+v", ex)
	}
	if len(kept) != 2 || kept[0].ID != dbhID || kept[1].ID != "db-health:bozuk" {
		t.Errorf("oracle + çözülemeyen id KALIR (bugünkü yol karar verir): %+v", kept)
	}
	// Liste boşaltılmışsa hiçbir şey hariç değil.
	none := cfg
	empty := []string{}
	none.ExcludeSystems = &empty
	if ex, kept := dbHealthSplitExcluded(open, none); len(ex) != 0 || len(kept) != 4 {
		t.Errorf("boş listede hariç yok: %d/%d", len(ex), len(kept))
	}
}

// Kapanış: gerekçe "system excluded (db-health)", resolved + ResolvedAt, Value
// ezilmez, anlık görüntü satırı değişmez, gerekçe iki kez eklenmez; kapanmış
// satır bir sonraki tikte açık listesine (dbHealthOpenRows) GİRMEZ → tek sefer.
func TestDBHealthExcludedResolution(t *testing.T) {
	snap := []*chstore.Problem{
		{ID: cbID, RuleID: cbID, Status: "open", Value: 100, Threshold: 5,
			Description: "couchbase orders (cb-node-1) veritabanında hata oranı %100.0 (eşik %5), 3 çağıran servis etkilendi"},
	}
	ex, _ := dbHealthSplitExcluded(dbHealthOpenRows(snap), chstore.DefaultDBHealth())
	res := dbHealthResolutionsWith(ex, dbHealthExcludedNote, dbhCur.UnixNano())
	if len(res) != 1 {
		t.Fatalf("tek kapanış bekleniyor: %d", len(res))
	}
	q := res[0]
	if q.Status != "resolved" || q.ResolvedAt == nil || !strings.HasSuffix(q.Description, "· resolved: system excluded (db-health)") {
		t.Errorf("kapanış: %+v", q)
	}
	if q.Value != 100 {
		t.Error("kapanış Value'yu ezmemeli (v0.9.977)")
	}
	if snap[0].Status != "open" {
		t.Error("anlık görüntü satırı değişmemeli")
	}
	if again := dbHealthResolutionsWith([]*chstore.Problem{&q}, dbHealthExcludedNote, dbhCur.UnixNano()); strings.Count(again[0].Description, "system excluded") != 1 {
		t.Error("gerekçe iki kez eklenmemeli")
	}
	// Bir sonraki tik: kapanmış satır açık listesinde yok → yeniden kapanmaz.
	if next := dbHealthOpenRows([]*chstore.Problem{&q}); len(next) != 0 {
		t.Errorf("kapanmış satır yeniden aday oldu: %+v", next)
	}
}

// Couchbase senaryosu uçtan uca: iki kova %100 hata, 3 çağıran. Hariç liste
// olmasaydı critical P1 açılırdı (karar kontrolü). Varsayılan ayarla ana okuma
// couchbase'i SQL'de düşürür (chstore TestDBHealthExcludeSystemsLiveEngine canlı
// motorla çiviler; burada Go ikizi SystemExcluded okumanın yerine geçer) →
// karar yok → problem yok; aynı okumadaki oracle olayı etkilenmez.
func TestDBHealthCouchbaseScenarioNoProblem(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	fixture := dbhRows(cbRow(2, 400, 400), cbRow(1, 400, 400), dbhRow(2, 1000, 100, 50, 2), dbhRow(1, 1000, 100, 50, 2))

	// Kontrol: süzgeçsiz okuma couchbase'i P1 açardı (koruma gerçekten iş görüyor).
	none := cfg
	empty := []string{}
	none.ExcludeSystems = &empty
	var cbFire bool
	for _, v := range dbHealthDecide(fixture, none, dbhCur, nil, false) {
		if v.ID == cbID && v.Fire {
			cbFire = true
			p := chstore.EnrichProblemsWithPriority([]chstore.Problem{dbHealthProblem(v, none, dbhCur)})[0]
			if p.Priority != "P1" {
				t.Fatalf("kontrol: %%100 hata P1 olmalıydı, %s", p.Priority)
			}
		}
	}
	if !cbFire {
		t.Fatal("kontrol: süzgeçsiz okumada couchbase açılmalıydı")
	}

	// Varsayılan: okuma (SQL `NOT IN ['couchbase']`) couchbase satırlarını döndürmez.
	var read []chstore.DBHealthBucket
	for _, r := range fixture {
		if !cfg.SystemExcluded(r.DBSystem) {
			read = append(read, r)
		}
	}
	verdicts := dbHealthDecide(read, cfg, dbhCur, nil, false)
	for _, v := range verdicts {
		if v.ID == cbID {
			t.Fatalf("couchbase için karar üretildi: %+v", v)
		}
	}
	if len(verdicts) != 1 || verdicts[0].ID != dbhID || !verdicts[0].Fire {
		t.Errorf("oracle olayı aynen açılmalı: %+v", verdicts)
	}
	for _, id := range dbHealthRefCandidates(read, cfg) {
		if id == cbID {
			t.Errorf("hariç sistem referans adayı olmamalı: %s", id)
		}
	}
}

// Sıra: ayar → kapalıysa rule-disabled → HARİÇ ayrımı + kapanış → okumalar
// (açık id'ler hariçsiz kümeden); referans okuması cfg'yi (hariç liste) taşır.
func TestDBHealthExcludeWiring(t *testing.T) {
	b, err := os.ReadFile("db_health.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "func (e *Evaluator) evaluateDBHealth(")
	j := strings.Index(s, "func (e *Evaluator) reconcileDBHealth(")
	body := s[i:j]
	disabled := strings.Index(body, "e.resolveDBHealthDisabled(ctx, open, now)")
	split := strings.Index(body, "excluded, open := dbHealthSplitExcluded(open, cfg)")
	resolve := strings.Index(body, "e.resolveDBHealthExcluded(ctx, excluded, now)")
	ids := strings.Index(body, "openIDs = append(openIDs, p.ID)")
	read := strings.Index(body, "e.store.DBHealthBuckets(")
	if disabled < 0 || split < 0 || resolve < 0 || ids < 0 || read < 0 ||
		!(disabled < split && split < resolve && resolve < ids && ids < read) {
		t.Errorf("sıra bozuk: disabled=%d split=%d resolve=%d ids=%d read=%d", disabled, split, resolve, ids, read)
	}
	if !strings.Contains(body, "cfg, dbHealthRefCandidates(rows, cfg))") {
		t.Error("referans okuması hariç listeyi (cfg) taşımalı")
	}
	k := strings.Index(s, "func (e *Evaluator) resolveDBHealthExcluded(")
	if k < 0 || !strings.Contains(s[k:], "e.store.UpsertProblem(ctx, q)") {
		t.Error("hariç kapanış normal yazım yolundan (UpsertProblem) gitmeli — incident kaskadı onu görür")
	}
}
