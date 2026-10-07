package chstore

// log_templates_ttl.go — v0.10.1116: `log_templates` defterine 30 günlük TTL.
//
// SORUN. log_templates `ReplacingMergeTree(version) ORDER BY id`, partition
// YOK, TTL YOK. v0.10.1030'un bulgusu: templater puller'ı Drain'i her tik
// örneklemden yeniden kurduğu için şablon kimlikleri tikten tike kayar, yani
// her tik YENİ satırlar gelir ve tablo sınırsız büyür. Okuyucuların hepsi —
// log_template_new dedektörü (varsayılan kapalı, defter yine de yazılır),
// v0.10.1113'ün Problem kanıtı ucu (anomaly/log_template_evidence.go), /logs
// Şablonlar sekmesi — tablonun TAM FINAL taramasını yapar; sınır yalnız
// LIMIT + max_execution_time.
//
// İFADE: toDateTime(last_seen) + INTERVAL 30 DAY.
//   - last_seen DateTime64(9). TTL ifadesinin sonucu Date ya da DateTime olmak
//     ZORUNDA (CH code 450; retention.go v0.6.37 dersi) → toDateTime().
//     toDate() DEĞİL: partition yok, gün hizalamasının kazancı yok; toDateTime
//     tam kayan pencere ve CLAUDE.md'nin "toDate() alt-gün matematiğinin
//     etrafına sarılmaz" tuzağına hiç yaklaşmaz. Emsal: ai_eval_runs
//     `toDateTime(started_at) + INTERVAL 180 DAY`, trace_snapshots.
//   - 30 gün: dedektörün bilinen-şablon ufku 7 gün (knownTemplatesHorizon);
//     1113 kanıtı problem başlangıcının çevresindeki pencereyi ister. TTL
//     last_seen'e bağlı: hâlâ görülen şablonun last_seen'i her tikte tazelenir,
//     asla düşmez; yalnız 30 gün boyunca HİÇ örneklenmemiş şablon düşer.
//   - AYAR DEĞİL, SABİT: retention.* mekanizması (retention.go SetRetention)
//     yalnız SİNYAL tablolarını listeler (spans/logs/metric_points/profiles ve
//     yanlarında gezenler); state tabloları DDL'de sabit TTL taşır
//     (anomaly_events / root_cause_hypotheses 30g, ai_calls 90g, …).
//   - ttl_only_drop_parts UYGULANAMAZ: partition yok, süresi tümüyle dolan bir
//     part oluşmaz. Satırlar TTL merge'lerinde silinir (merge_with_ttl_timeout,
//     CH varsayılanı 4 sa — süresi dolan satır en çok birkaç saat gecikir).
//
// ANLAM DEĞİŞİKLİĞİ (bilinçli): first_seen "ilk görülme" değil artık "TTL
// penceresinde kesintisiz görülmeye başladığı an". 30 gün susup dönen bir
// şablonun satırı düşmüş olur ve yeniden DOĞAR (puller'ın yapışkan first_seen
// okuması satırı bulamaz). Dedektör zaten 7 gün susmuş şablonu "bilinen"
// saymıyordu; 1113 kanıtı ise o şablonu dönüş anında "doğan" görür — 30 gün
// susmuş bir hata mesajının dönüşü kanıt olarak doğru okunur.
//
// YENİ KURULUM: CREATE (store.go canonicalTables) TTL'i taşır.
// MEVCUT KURULUM: CREATE `IF NOT EXISTS` olduğu ve planDDL var olan nesnenin
// CREATE'ini hiç göndermediği için TTL oraya ULAŞMAZ → tek seferlik
// `ALTER TABLE log_templates MODIFY TTL …`, system_settings işaretiyle
// korunur (göç emsali: anomaly_sensitivity_oplatency_migrate.go — işaret
// varken bir daha koşmaz; düşen tur işaret YAZMAZ, sonraki boot yeniden dener).
//
// NEDEN `alters` diliminde DEĞİL: MODIFY TTL planDDL'de elenmez (no-op olduğu
// kanıtlanamaz), yani her boot'ta, küme kipinde her pod'dan bir dağıtık DDL
// kuyruğu turu olurdu (trace_snapshots satırı bugün bunu ödüyor) — ve
// materialize_ttl_after_modify=1 ile her boot'ta bir MATERIALIZE TTL mutasyonu.
//
// NEDEN migrate() İÇİNDE DEĞİL: küme kipinde şema yerindeyken execDDL ifadeyi
// ERTELER ve nil döner (ddl_defer.go). İşaret o anda yazılsaydı ertelenen ALTER
// arka planda düştüğünde göç "bitti" sayılır ve TTL HİÇ uygulanmazdı. Bu yüzden
// adım New()'in sonunda, erteleme kapandıktan SONRA ayrı bir goroutine'de koşar:
// execDDL artık senkron, işaret ancak ALTER döndükten sonra yazılır. Boot'u
// beklemez.
//
// KÜME: execDDL → adaptDDL. log_templates bir state tablosu (highVolumeTables /
// shard kayıtlarında YOK): `_local` yeniden adlandırması ve Distributed
// sarmalayıcı YOK, yalnız `ON CLUSTER <ad>` enjekte edilir — birleşik state
// yolunda da (tek replikasyon grubu) eski shard'lı yolda da doğru: Replicated
// ALTER metaveriyi Keeper üzerinden tüm replikalara yayar. Dağıtık DDL
// "kuyruğa alındı" (code 159) execWithReadonlyRetry'de BAŞARI sayılır (CH
// ifadeyi arka planda uygular) → işaret yazılır.
//
// MATERIALIZE: bu tek seferlik ALTER `materialize_ttl_after_modify = 1` taşır —
// retention.go'nun (0) TERSİ, bilinçli. Orada sorun spans ölçeğinde bir admin
// PUT'unun dakikalarca beklemesiydi; burada adım arka planda, tek seferlik ve
// küçük bir state tablosunda. 0 olsaydı eski part'ların TTL bilgisi hesaplanmamış
// kalır, birikmiş satırlar yalnız o part'lar bir gün normal merge'e girdiğinde
// düşerdi — en büyük (en eski) part'lar en geç birleşenlerdir, yani tam da
// büyümeyi oluşturan birikim en uzun yaşardı. `alter_sync = 0`: ALTER mutasyonun
// bitmesini beklemez; MATERIALIZE TTL arka plan mutasyonu olarak koşar.
//
// ÇOKLU POD: işaret bir CAS değil. Aynı anda boot eden iki pod işareti yok
// görürse ikisi de ALTER gönderir — MODIFY TTL idempotent, bedel küçük bir
// tabloda ikinci bir MATERIALIZE TTL mutasyonu. İşaret yazıldıktan sonra
// hiçbir pod göndermez.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"time"
)

const (
	// LogTemplatesTTLDays — log_templates satırının last_seen'den sonraki
	// ömrü. logTemplatesTTLExpr'deki sayıyla aynı (test pinler).
	LogTemplatesTTLDays = 30
	// logTemplatesTTLExpr — CREATE ve tek seferlik ALTER'ın PAYLAŞTIĞI ifade.
	logTemplatesTTLExpr = "toDateTime(last_seen) + INTERVAL 30 DAY"
	// logTemplatesTTLMarkerKey — göçün system_settings işareti; varlığı =
	// göç bitti.
	logTemplatesTTLMarkerKey = "log_templates_ttl_v1"
	// logTemplatesTTLVersion — işaret damgası.
	logTemplatesTTLVersion = "v0.10.1116"
	// logTemplatesTTLTimeout — arka plan adımının toplam bütçesi: tıkalı
	// kuyrukta sunucu bütçesi (ddlTaskTimeoutSeconds) + readonly geri çekilmesi.
	logTemplatesTTLTimeout = 3 * time.Minute
)

// Göç sonuçları (işarete ve loga yazılır).
const (
	LogTemplatesTTLDone    = "done"    // işaret zaten var — hiçbir şey yapılmadı
	LogTemplatesTTLAbsent  = "absent"  // tablo yok (ertelenmiş CREATE) — işaret YOK, sonraki boot
	LogTemplatesTTLPresent = "present" // DDL zaten TTL taşıyor (yeni kurulum / elle) — yalnız işaret
	LogTemplatesTTLApplied = "applied" // ALTER gönderildi — işaret
)

// logTemplatesTTLAlterSQL — tek seferlik ALTER (çıplak ad; küme kipinde
// adaptDDL ON CLUSTER enjekte eder). SAF, test pinler.
func logTemplatesTTLAlterSQL() string {
	return "ALTER TABLE log_templates MODIFY TTL " + logTemplatesTTLExpr +
		" SETTINGS materialize_ttl_after_modify = 1, alter_sync = 0"
}

// ttlClauseRe — system.tables.engine_full içinde tablo düzeyi TTL cümlesi.
// CH biçimi: "… ORDER BY id TTL toDateTime(last_seen) + toIntervalDay(30)
// SETTINGS …" (INTERVAL normalize edilir, bu yüzden ifadenin kendisi değil
// cümlenin varlığı aranır). Kelime sınırı ZK yolu / ayar adlarını eşlemesin.
var ttlClauseRe = regexp.MustCompile(`\sTTL\s`)

// planLogTemplatesTTL — SAF: (işaret var mı, tablo var mı, engine_full) → sonuç.
// Operatörün elle koyduğu başka bir TTL EZİLMEZ: herhangi bir tablo TTL'i
// "present" sayılır.
func planLogTemplatesTTL(markerPresent, tableExists bool, engineFull string) string {
	switch {
	case markerPresent:
		return LogTemplatesTTLDone
	case !tableExists:
		return LogTemplatesTTLAbsent
	case ttlClauseRe.MatchString(engineFull):
		return LogTemplatesTTLPresent
	default:
		return LogTemplatesTTLApplied
	}
}

// logTemplatesTTLStore — göçün dokunduğu yüzey (*Store karşılar; testler
// bellek içi sahteyle).
type logTemplatesTTLStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
	logTemplatesEngineFull(ctx context.Context) (engineFull string, exists bool, err error)
	execDDL(ctx context.Context, sql string) error
}

// logTemplatesTTLMarker — işaretin JSON gövdesi (teşhis için).
type logTemplatesTTLMarker struct {
	Version   string `json:"version"`
	AppliedAt int64  `json:"appliedAt"`
	Outcome   string `json:"outcome"`
	TTLDays   int    `json:"ttlDays"`
}

// applyLogTemplatesTTL — tek seferlik göç. İdempotent: işaret varken hiçbir
// şey yapmaz (Done). Hata yönü: işaret / tablo okunamazsa ya da ALTER düşerse
// işaret YAZILMAZ ve hata döner — sonraki boot yeniden dener (MODIFY TTL
// idempotent). Tablo yoksa (küme kipinde CREATE'i ertelenmiş) işaret yazılmaz:
// CREATE TTL'i zaten taşır, sonraki boot "present" görür.
func applyLogTemplatesTTL(ctx context.Context, st logTemplatesTTLStore, now time.Time) (string, error) {
	marker, err := st.GetSetting(ctx, logTemplatesTTLMarkerKey)
	if err != nil {
		return "", fmt.Errorf("read marker: %w", err)
	}
	if len(marker) > 0 {
		return LogTemplatesTTLDone, nil
	}
	engineFull, exists, err := st.logTemplatesEngineFull(ctx)
	if err != nil {
		return "", fmt.Errorf("probe log_templates: %w", err)
	}
	outcome := planLogTemplatesTTL(false, exists, engineFull)
	switch outcome {
	case LogTemplatesTTLAbsent:
		return outcome, nil
	case LogTemplatesTTLApplied:
		if err := st.execDDL(ctx, logTemplatesTTLAlterSQL()); err != nil {
			return "", fmt.Errorf("alter log_templates TTL: %w", err)
		}
	}
	body, _ := json.Marshal(logTemplatesTTLMarker{
		Version: logTemplatesTTLVersion, AppliedAt: now.UnixNano(),
		Outcome: outcome, TTLDays: LogTemplatesTTLDays,
	})
	if err := st.PutSetting(ctx, logTemplatesTTLMarkerKey, body); err != nil {
		return outcome, fmt.Errorf("write marker: %w", err)
	}
	return outcome, nil
}

// logTemplatesEngineFull — bağlı host'ta tablonun engine_full'u. Satır yok =
// tablo yok (hata değil).
func (s *Store) logTemplatesEngineFull(ctx context.Context) (string, bool, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT engine_full FROM system.tables
		WHERE database = currentDatabase() AND name = 'log_templates'
		LIMIT 1
		SETTINGS max_execution_time = 5`)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", false, rows.Err()
	}
	var ef string
	if err := rows.Scan(&ef); err != nil {
		return "", false, err
	}
	return ef, true, rows.Err()
}

// applyLogTemplatesTTLOnce — New()'in sonunda, erteleme kapandıktan sonra
// goroutine olarak koşar. Yalnız log; boot'u asla düşürmez.
func (s *Store) applyLogTemplatesTTLOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), logTemplatesTTLTimeout)
	defer cancel()
	outcome, err := applyLogTemplatesTTL(ctx, s, time.Now())
	if err != nil {
		log.Printf("[chstore] log_templates TTL göçü (sonraki boot'ta yeniden denenecek): %v", err)
		return
	}
	switch outcome {
	case LogTemplatesTTLApplied:
		log.Printf("[chstore] log_templates: %d günlük TTL (last_seen) uygulandı — birikmiş satırlar arka plan MATERIALIZE TTL mutasyonuyla düşer", LogTemplatesTTLDays)
	case LogTemplatesTTLPresent:
		log.Printf("[chstore] log_templates: tablo TTL'i zaten tanımlı — ALTER gönderilmedi, işaret yazıldı")
	case LogTemplatesTTLAbsent:
		log.Printf("[chstore] log_templates henüz yok (CREATE ertelenmiş olabilir) — TTL göçü sonraki boot'ta")
	}
}
