package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.9.1342 — /inbox'ın DB özne şeridi (operatör kararı: db problemleri
// servis problemleriyle öncelik sırasında YARIŞMASIN).

func TestNormalizeInboxSubject(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", inboxSubjectService},
		{"service", inboxSubjectService},
		{"db", inboxSubjectDB},
		{" db ", inboxSubjectDB},
		// v0.10.1017 — dış kaynak şeridi (Oracle / Influx özneleri).
		{"external", inboxSubjectExternal},
		// Bilinmeyen → varsayılan ŞERİT, db DEĞİL. Elle düzenlenmiş bir
		// link operatörü tanımadığı bir şeride düşürmemeli.
		{"queue", inboxSubjectService},
		{"DB", inboxSubjectService},
		{"db,service", inboxSubjectService},
	}
	for _, tc := range tests {
		if got := normalizeInboxSubject(tc.in); got != tc.want {
			t.Errorf("normalizeInboxSubject(%q) = %q, beklenen %q", tc.in, got, tc.want)
		}
	}
}

// ŞERİT CACHE ANAHTARINA GİRMELİ — ve `kind` onun yerine geçemez.
//
// db şeridi kinds'i ["problem"]e ZORLUYOR. Yani servis şeridinde yalnız
// "Problems" türünü seçen bir operatör ile db şeridindeki operatör AYNI
// kind dizisini üretir; şerit anahtarda olmasaydı ikisi tek cache
// girdisini paylaşır ve biri diğerinin satırlarını görürdü — v0.5.187
// çapraz-zehirlenmesinin birebir şekli.
func TestInboxListKeyCarriesSubject(t *testing.T) {
	only := []string{"problem"}
	svc := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 0,
		only, inboxPriosAll, inboxSubjectService)
	db := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 0,
		only, inboxPriosAll, inboxSubjectDB)
	if svc == db {
		t.Fatal("şerit cache anahtarını değiştirmiyor — aynı kind seçimiyle iki şerit " +
			"TEK girdiyi paylaşır ve biri diğerinin satırlarını servis eder (v0.5.187)")
	}
	if !strings.Contains(db, "subject=db") {
		t.Errorf("anahtarda şerit alanı yok: %s", db)
	}
	// Gövde şekli değişti (dbSubjectCount) → sürüm damgası ilerlemeli,
	// yoksa yükseltme öncesi cache'lenmiş bir gövde yeni sözleşmeymiş
	// gibi deserialize edilir.
	// v0.10.1026 — :v8:: varsayılan şeridin SATIR kümesi değişti (dış
	// kaynak problemleri de giriyor); eski sürümün cache'lediği sayfa yeni
	// anahtardan servis edilmemeli (:v6: emsali).
	if !strings.HasPrefix(svc, "inbox:v8:") {
		t.Errorf("anahtar sürümü ilerlememiş: %s", svc)
	}
}

// Şeridin uçtan uca bağlandığının kapısı. Saf fonksiyonlar doğru
// olabilir ama HİÇ ÇAĞRILMIYORSA şerit yoktur.
func TestInboxSubjectLaneIsWired(t *testing.T) {
	src := readSrc(t, "inbox.go")
	for _, want := range []struct{ name, frag string }{
		{"param okunuyor", `subject := normalizeInboxSubject(q.Get("subject"))`},
		// KOŞULUN KENDİSİ pinli, yalnız gövdesi değil. Mutasyon testi
		// gösterdi ki `if subject == inboxSubjectDB {` → `if false {`
		// hiçbir kapıyı ısırmıyordu: zorlama satırı kaynakta DURUYOR,
		// yalnız ölü bir dalın içinde. Kaynak taraması canlılığı
		// kanıtlayamaz, ama koşulu pinlemek bu şekli kapatır.
		// v0.10.1017 — koşul "servis DEĞİL" oldu: dış kaynak şeridi de
		// (kind=external) tek kaynaklı ve aynı zorlamayı ister.
		{"zorlama canlı bir dalda", "if subject != inboxSubjectService {"},
		// DB özneli satır YALNIZ problems kaynağında var. Sayfanın tür
		// facet varsayılanı ['exception'] — zorlanmasa db şeridi HİÇ
		// problem çekmez ve BOŞ açılırdı.
		{"db şeridinde tür zorlanıyor", `kinds = []string{"problem"}`},
		// v0.10.1026 — şerit chstore değerine ÇEVRİLEREK iniyor: varsayılan
		// şerit = servis + dış kaynak. Çıplak `SubjectKind: subject,` geri
		// gelirse varsayılan liste yine yalnız servis gösterir.
		{"şerit ProblemFilter'a iniyor", "SubjectKind: inboxProblemLane(subject),"},
		// Atlanan problem çipinin sayısı listeyle AYNI evreni sayıyor.
		{"varsayılan şeridin sayısı servis + dış kaynak", "inboxLaneProblemCount(subjectCounts, subject)"},
		{"şerit anahtarda", "subject)"},
		{"db sayısı gövdede", `"dbSubjectCount": subjectCounts[inboxSubjectDB],`},
		{"dış kaynak sayısı gövdede", `"externalSubjectCount": subjectCounts[inboxSubjectExternal],`},
	} {
		if !strings.Contains(src, want.frag) {
			t.Errorf("%s: %q bulunamadı — şerit yarım bağlanmış", want.name, want.frag)
		}
	}
	// Sayı `counts` sözlüğüne YAZILMAMALI: orası kind/prio evreni.
	// İkisini tek haritada karıştırmak okuyucuya hangi evrene baktığını
	// söyleyemez hâle getirir.
	if strings.Contains(src, `counts["db"]`) {
		t.Error("şerit sayısı kind/prio sözlüğüne yazılmış — iki ayrı evren tek haritada")
	}
	if strings.Contains(src, "SubjectKind: subject,") {
		t.Error("şerit chstore'a ÇEVRİLMEDEN iniyor — varsayılan şerit dış kaynak satırlarını kaybeder (v0.10.1026)")
	}
}

// v0.10.1026 (operatör kararı 2026-10-01, kuyruk maddesi 14: "14 girsin") —
// dış kaynak (Oracle / Influx) problemleri VARSAYILAN listede de görünür.
//
// inboxProblemLane: inbox şeridi → chstore.ProblemFilter.SubjectKind.
// Varsayılan şerit (ve normalizeInboxSubject'in bilinmeyen değerleri
// düşürdüğü yer) birleşim değerine çevrilir; db / external kendi türleriyle
// birebir. `service` chstore'a ÇIPLAK gitmez: orada "yalnız servis" demek.
func TestInboxProblemLane(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"", chstore.ProblemLaneServiceOrExternal},
		{"service", chstore.ProblemLaneServiceOrExternal},
		{"queue", chstore.ProblemLaneServiceOrExternal}, // bilinmeyen → varsayılan şerit
		{"db", chstore.ProblemKindDB},
		{"external", chstore.ProblemKindExternal},
	}
	for _, tc := range tests {
		if got := inboxProblemLane(normalizeInboxSubject(tc.raw)); got != tc.want {
			t.Errorf("inboxProblemLane(normalizeInboxSubject(%q)) = %q, beklenen %q", tc.raw, got, tc.want)
		}
	}
	// Birleşim değeri bir TÜR adı olmamalı — olsaydı bir satırın kind'ı
	// onunla eşleşebilir ve "service" sıkı kalmazdı.
	for _, k := range []string{chstore.ProblemKindService, chstore.ProblemKindDB, chstore.ProblemKindExternal} {
		if chstore.ProblemLaneServiceOrExternal == k {
			t.Fatalf("varsayılan şerit değeri bir tür adıyla çakışıyor: %q", k)
		}
	}
}

// inboxLaneProblemCount: şeridin problem sayısı listenin evreniyle AYNI.
// Varsayılan şerit servis + dış kaynak satırlarını birlikte listelediği için
// atlanan problem çipi ("Problems N", tür facet'inde problem kapalıyken) iki
// kovanın TOPLAMINI söyler; db / external şeritleri kendi kovalarını
// (dbSubjectCount / externalSubjectCount ile aynı sayı).
func TestInboxLaneProblemCount(t *testing.T) {
	counts := map[string]uint64{
		chstore.ProblemKindService:  41,
		chstore.ProblemKindDB:       5,
		chstore.ProblemKindExternal: 7,
	}
	tests := []struct {
		subject string
		want    uint64
	}{
		{inboxSubjectService, 48}, // 41 + 7 — db AYRI şeritte, sayılmaz
		{inboxSubjectDB, 5},
		{inboxSubjectExternal, 7},
	}
	for _, tc := range tests {
		if got := inboxLaneProblemCount(counts, tc.subject); got != tc.want {
			t.Errorf("inboxLaneProblemCount(%q) = %d, beklenen %d", tc.subject, got, tc.want)
		}
	}
	// Sayım düşmüşken (boş harita) ve kolon yokken (CountProblemsBySubject
	// yalnız service kovasını doldurur): toplam yine tanımlı, eksik kova 0.
	if got := inboxLaneProblemCount(map[string]uint64{}, inboxSubjectService); got != 0 {
		t.Errorf("boş harita → %d, beklenen 0", got)
	}
	if got := inboxLaneProblemCount(map[string]uint64{chstore.ProblemKindService: 3}, inboxSubjectService); got != 3 {
		t.Errorf("yalnız servis kovası (kolon yok) → %d, beklenen 3", got)
	}
}

// DB şeridinde tür facet'inin zorlanması, ÖNCE cache anahtarı kurulmalı.
// Sonra zorlansaydı iki farklı istek (kind=exception&subject=db ile
// kind=problem&subject=db) FARKLI anahtar üretip AYNI cevabı döndürürdü —
// zararsız ama cache'i ikiye böler; daha kötüsü, zorlama anahtardan sonra
// gelirse `narrowed` hesabı da yanlış türden okur.
func TestInboxSubjectForcesKindBeforeTheCacheKey(t *testing.T) {
	src := readSrc(t, "inbox.go")
	force := strings.Index(src, `kinds = []string{"problem"}`)
	key := strings.Index(src, "cacheKey := inboxListKey(")
	if force < 0 || key < 0 {
		t.Fatal("şerit zorlaması ya da cache anahtarı bulunamadı")
	}
	if force > key {
		t.Error("tür zorlaması cache anahtarından SONRA — anahtar, sunucunun " +
			"gerçekte kullandığı tür kümesini yansıtmaz")
	}
}
