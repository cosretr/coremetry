package chstore

import (
	"fmt"
	"io/fs"
	"log"
	"regexp"
	"strings"
	"sync"

	"github.com/cilcenk/coremetry/migrations"
)

// table_catalog.go — "ürün bu tabloyu yönetiyor mu?" (v0.10.846).
//
// Operatör-bildirimli (prod, 2026-09-20): replika tutarlılığı kartı
// `feedbacks` için "eksik replika" diyor ve kırmızı bir "İlk replikayı kur"
// düğmesi basıyordu. `feedbacks` ürünün v0.8.240'ta KALDIRDIĞI bir tablo
// (removed_tables.go) — yani doğru eylem kurmak değil temizlemekti.
//
// İKİNCİ KUSUR bunun sınıfıydı: kart envanteri CANLI `system.tables`'tan
// alıyor (replica_consistency.go, `engine LIKE '%MergeTree%'`) ve ürünün o
// tabloyu BEKLEYİP beklemediğine HİÇ bakmıyordu. `shardCoverage`'ın ölçüsü
// — "bu tablo shard'ın HER host'unda olmalı" — yalnız ÜRÜNÜN KURDUĞU
// tablolar için tanımlıdır. Kaldırılmış bir tablo için ters (hiçbir yerde
// olmamalı), operatörün kendi bıraktığı bir tablo için ise bilinmezdir.
// Düğme zaten çalışamıyordu (`seedCanonicalArgs` kanonik tanım bulamayıp
// engelliyor) — yani yanıltıcıydı: operatöre var olmayan bir iş gösteriyordu.
//
// Burada üretilen karar ÜÇ değerlidir:
//
//	""                 → ürün yönetiyor (ya da emin değiliz) → BUGÜNKÜ davranış
//	ReplicaRemoved     → defterde: kalıntı, bir sonraki boot küme genelinde siler
//	ReplicaUnmanaged   → katalogda yok: Coremetry yönetmiyor, eylem önerilmez
//
// FAIL-OPEN: katalog okunamazsa (Store{} kuran testler, migrate koşmamış)
// hiçbir şey "katalog dışı" sayılmaz. Yanlış bir "yönetmiyoruz" kararı
// GERÇEK bir eksik replikayı gizlerdi; yanlış bir "yönetiyoruz" kararı ise
// yalnız bugünkü davranışı sürdürür.

// catalogSuffixes — ürün adının ÜSTÜNE gelen türev ekler. Ad araması
// bunları soyar, çünkü hepsi kanonik bir ürün adından TÜRER:
//
//	_local    → adaptDDL'in shard-yerel telemetri adı (spans → spans_local)
//	_fix      → replica_repair sihirbazının geçici tablosu
//	_old      → 0009/0010 göçünün doğrulama bitene dek YAŞAYAN eski tablosu
//	_unified  → 0009 göçünün birleşik-grup geçici tablosu
//	_repart   → 0010'un PARTITION BY onarımının hedef tablosu
//	_pathfix  → 0010'un ZK yolu onarımının hedef tablosu
//	            (`_pathfix_old` iki ekin üst üste binmesidir — döngü ikisini
//	             de soyar, ayrı satır gerekmez)
//
// Katalog bir SUSTURUCU olduğu için bu listenin her eksiği GERÇEK bir eksik
// replikayı gizler: türevi tanınmayan CANLI bir yedek (`problems_repart`)
// "katalog dışı" sayılır ve kapsama ölçüsü o satırda hiç koşmaz.
// TestProductCatalogueCoversEveryProducedName bunu tüm migration aileleri
// için zorluyor.
//
// TEK GÖVDE (v0.10.846 incelemesi): state_replication.go'daki
// stateProbeTable de aynı soruyu soruyordu ve kendi (eksik) listesini
// taşıyordu — iki liste ayrışmıştı ve hiçbir kapı onları birbirine
// bağlamıyordu. Artık ikisi de catalogDerivedName'den geçiyor.
var catalogSuffixes = []string{"_local", "_fix", "_old", "_unified", "_repart", "_pathfix"}

// catalogBaseName — SAF: türev ekleri soyulmuş ürün adı.
// Zincirlenmiş ekleri de çözer (`spans_local_fix` → `spans`,
// `anomaly_events_pathfix_old` → `anomaly_events`), çünkü ekler üst üste
// binebiliyor: onarım sihirbazı `_fix`'i ZATEN `_local` olan bir adın
// üstüne koyar, 0010 da `_pathfix`'in yedeğini `_pathfix_old` yapar.
func catalogBaseName(name string) string {
	for again := true; again; {
		again = false
		for _, suf := range catalogSuffixes {
			if b := strings.TrimSuffix(name, suf); b != name && b != "" {
				name, again = b, true
				break
			}
		}
	}
	return name
}

// catalogDerivedName — SAF: ad bir ürün adının TÜREVİ mi (kanonik adın
// kendisi değil)? stateProbeTable ile ORTAK gövde.
func catalogDerivedName(name string) bool { return catalogBaseName(name) != name }

// catalogInnerName — SAF: ad bir MV iç tablosu mu? `isInnerTable` (v0.10.824)
// DAR ve öyle kalmalı — `.inner_id.<uuid>` sözleşmesi o sürümlerin uuid
// çözümlemesini taşıyor. Katalog sınıflandırmasının sorusu FARKLI ve daha
// geniş: "bu ad ürünün doğrudan kurduğu bir nesne mi, yoksa CH'nin bir
// view için ürettiği iç tablo mu". Ordinary motorlu veritabanında ikincisi
// `.inner.<view adı>` olur ve dar önekle eşleşmezdi.
func catalogInnerName(name string) bool { return strings.HasPrefix(name, ".inner") }

// catalogObjectRe — bir SQL METNİNDEKİ her `CREATE TABLE|VIEW|MATERIALIZED
// VIEW [IF NOT EXISTS] [db.]<ad>` için nesne adı.
//
// ddlObjectRe'den (ddl_skip_existing.go) AYRI olmak zorunda: o, TEK bir
// ifadenin BAŞINA çapalı (`^`) ve `IF NOT EXISTS` ŞART koşuyor — çünkü orada
// soru "bu ifade elenebilir mi", burada ise "bu dosya hangi adları kuruyor".
// Migration dosyaları çok ifadeli ve bazıları `ON CLUSTER` taşıyor.
//
// Ad'dan önce isteğe bağlı `db.` niteliği eşleşir ve YUTULUR — aranan hep
// çıplak ad. `concat('CREATE TABLE ', database, …)` gibi kaçak eşleşmeler
// olmaz: "CREATE TABLE " sonrası `'` tanımlayıcı karakteri değil.
var catalogObjectRe = regexp.MustCompile(
	`(?is)CREATE\s+(?:MATERIALIZED\s+VIEW|VIEW|TABLE)\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
		"`?" + `(?:[A-Za-z0-9_]+` + "`?" + `\.` + "`?" + `)?([A-Za-z0-9_]+)`)

// catalogTableNames — SAF: SQL metinlerinden kurulan nesne adları kümesi.
func catalogTableNames(sqls []string) map[string]bool {
	out := map[string]bool{}
	for _, q := range sqls {
		for _, m := range catalogObjectRe.FindAllStringSubmatch(q, -1) {
			out[m[1]] = true
		}
	}
	return out
}

// migrationTableNames — migrations/*.sql'in kurduğu adlar. Dosyalar gömülü
// (migrations.AllSQL) ve içerik derleme anında sabit olduğu için bir kez
// taranır.
//
// Okuma hatası PANİK DEĞİL: boş küme döner ve o adlar "ürün kataloğunda yok"
// tarafına düşerdi — bu yüzden productTableNames, migration kümesi boş
// gelirse tüm sınıflandırmayı kapatır (fail-open).
var migrationTableNames = sync.OnceValue(func() map[string]bool {
	var sqls []string
	err := fs.WalkDir(migrations.AllSQL, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sql") {
			return err
		}
		b, rerr := migrations.AllSQL.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		sqls = append(sqls, string(b))
		return nil
	})
	if err != nil {
		log.Printf("[chstore] gömülü migration'lar taranamadı (%v) — tablo kataloğu sınıflandırması KAPALI", err)
		return nil
	}
	return catalogTableNames(sqls)
})

// productTableNames — ürünün kurduğu TÜM nesne adları. ÜÇ kaynak:
//
//  1. boot tablo kataloğu  — s.canonicalTableDDL (`tables` dilimi)
//  2. boot MV kataloğu     — canonicalMVs() (`mvs` dilimi)
//  3. operatör migration'ları — migrations/*.sql
//
// (2) v0.10.846 incelemesinin KRİTİK bulgusuydu ve onsuz bu dosya kendi
// kurulduğu kartı kör ediyordu: kart envanteri `engine = 'MaterializedView'`
// satırlarını da topluyor, `catalogBaseName` `_local`'i soyunca geriye
// `service_summary_5m` gibi bir MV adı kalıyor ve o ad YALNIZ `mvs`
// diliminde geçiyor. Katalog onu tanımayınca `unmanaged` → kapsama ölçüsü
// SUSTURULUYOR → v0.10.818-835 programının ölçtüğü MV replika eksikliği
// sessizce ölçülmez oluyordu.
//
// Koşullu MV'ler (entity_seen_1m/5m, workload_revision_activity_1m) migrate
// içinde `mvs`'e append ediliyor ama adları migrations/0011 + 0012'de de
// geçiyor, yani (3) onları kapsıyor — bütünlük testi bunu açıkça arıyor.
//
// nil dönerse sınıflandırma KAPALI (fail-open). Üç koşul da bilinçli:
// katalog eksikse "katalogda yok" demek GERÇEK bir eksik replikayı
// gizleyebilirdi; emin olmadığımızda bugünkü davranış doğru taraftır.
func (s *Store) productTableNames() map[string]bool {
	if len(s.canonicalTableDDL) == 0 {
		return nil
	}
	mig := migrationTableNames()
	if len(mig) == 0 {
		return nil
	}
	mvs := catalogTableNames(canonicalMVs())
	if len(mvs) == 0 {
		return nil
	}
	out := catalogTableNames(s.canonicalTableDDL)
	for _, src := range []map[string]bool{mvs, mig} {
		for n := range src {
			out[n] = true
		}
	}
	return out
}

// catalogVerdictFor — SAF: bir tablo adının ürün kataloğundaki yeri.
// Dönen karar "" ise BUGÜNKÜ yol koşar.
//
// MV İÇ TABLOLARINA DOKUNMAZ: onların sahibi v0.10.824/830/833 yollarıdır ve
// adları uuid'den (ya da Ordinary DB'de view adından) doğduğu için hiçbir
// katalogda geçmez — sınıflandırılsalardı hepsi "katalog dışı" olurdu ve o
// üç sürümün ölçtüğü kararlar (öksüz / view çözülemedi / MV onarımı) yok
// olurdu. Burada `.inner` ÖNEKİNİN TAMAMI elenir, yalnız `.inner_id.` değil:
// Ordinary motorlu bir veritabanında iç tablo `.inner.<view adı>` olur ve
// v0.10.846 incelemesi o adların `unmanaged` olup kapsama ölçüsünden
// düştüğünü saptadı. Aynı geniş önek canRepair/seedEligibility'de de
// kullanılıyor.
//
// DEFTER ARAMASI TAM ADLA: türev adlara (`feedbacks_old`) yayılmaz, çünkü
// kaldırma yalnız çıplak adı düşürür — "bir sonraki boot temizler" sözünü
// tutamayacağımız bir satıra yazmayız (v0.10.846 incelemesi).
func catalogVerdictFor(table string, managed map[string]bool) (verdict, since string) {
	if catalogInnerName(table) {
		return "", ""
	}
	if rt, ok := removedTableEntry(table); ok {
		return ReplicaRemoved, rt.Since
	}
	base := catalogBaseName(table)
	if len(managed) == 0 {
		return "", "" // katalog okunamadı → fail-open
	}
	if managed[base] || managed[table] {
		return "", ""
	}
	return ReplicaUnmanaged, ""
}

// catalogHint — SAF: kararın operatöre gösterilen gerekçesi. "Ne göreceğim"
// ve "ne yapmalıyım" tek cümlede; ikisi de eylem ÖNERMEZ.
func catalogHint(table, verdict, since string) string {
	switch verdict {
	case ReplicaRemoved:
		return fmt.Sprintf("%s: ürün bu tabloyu KALDIRDI (%s) — bu satır bir KALINTI, eksik replika DEĞİL. "+
			"Kaldırma eskiden ON CLUSTER taşımıyordu, o yüzden yalnız bir host'ta koştu; bir sonraki boot "+
			"küme genelinde (ON CLUSTER … SYNC) tamamlar. Onarım/ilk replika kurulumu bu satırda uygulanmaz.",
			table, since)
	case ReplicaUnmanaged:
		return fmt.Sprintf("%s ürün kataloğunda YOK — bu tabloyu Coremetry yönetmiyor (operatörün kendi tablosu "+
			"ya da elle bırakılmış bir kalıntı olabilir). \"Shard'ın her host'unda olmalı\" ölçüsü yalnız ürünün "+
			"kurduğu tablolar için tanımlı, o yüzden burada kapsama kararı verilmez ve eylem önerilmez.", table)
	}
	return ""
}

// catalogSuppressesCoverage — SAF: `shardCoverage` (kapsama ölçüsü) koşsun mu?
//
// Kapsama ölçüsünün TEK dayanağı "ürün bu tabloyu bu shard'ın her host'unda
// kurar" varsayımıdır. Katalog dışı iki sınıfın ikisinde de o varsayım
// geçersiz: kaldırılmış tabloda TERS (hiçbir host'ta olmamalı), yönetilmeyen
// tabloda BİLİNMEZ. Ölçülen kararlar (replicaVerdict: ıraksama, readonly,
// oturum, gecikme, farklı ZK yolu) kayıtlı GERÇEK replikaları ölçer ve
// bastırılmaz.
func catalogSuppressesCoverage(class string) bool { return class != "" }

// catalogShardVerdict — SAF: katalog kararı ÖLÇÜLEN kararın üstüne yazılsın mı?
//
//   - ReplicaRemoved → HER ZAMAN. Kalıntının replikasyon sağlığı konu dışı;
//     tek gerçek "ürün bunu kaldırdı, bir sonraki boot temizler".
//   - ReplicaUnmanaged → yalnız shard'da hiç kayıtlı replika YOKKEN. Kayıt
//     varsa ölçüm gerçektir ve durur (operatörün kendi tablosunda ıraksama
//     görmesi bilgi; onarım düğmesi zaten kapsama kararına bağlı). Kayıt
//     yokken replicaVerdict boş dilime "ok" der — "tutarlı" demek yalan
//     olurdu, ölçülecek hiçbir şey yok.
func catalogShardVerdict(class string, replicas int) (verdict string, override bool) {
	switch class {
	case ReplicaRemoved:
		return ReplicaRemoved, true
	case ReplicaUnmanaged:
		if replicas == 0 {
			return ReplicaUnmanaged, true
		}
	}
	return "", false
}

// catalogRepairReject — SAF: onarım/seed uçlarının RET metni.
//
// Düğmeyi gizlemek YETMEZ (istemci uydurabilir, eski sekme bayat rapor
// taşıyabilir): PlanReplicaRepair bu kapıyı taze raporu OKUMADAN ÖNCE
// geçirir, çünkü karar yalnız ADA ve katalogda bağlıdır.
func catalogRepairReject(table, verdict, since string) string {
	switch verdict {
	case ReplicaRemoved:
		return fmt.Sprintf("%s ürünün KALDIRDIĞI bir tablo (%s) — onarılmaz/kurulmaz; bir sonraki boot "+
			"küme genelinde düşürür (ON CLUSTER … SYNC)", table, since)
	case ReplicaUnmanaged:
		return fmt.Sprintf("%s ürün kataloğunda yok — Coremetry bu tabloyu yönetmiyor, sihirbaz kanonik "+
			"tanımını bilmiyor; elle (runbook) incele", table)
	}
	return ""
}
