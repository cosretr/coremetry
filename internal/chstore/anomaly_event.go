package chstore

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"time"
)

// AnomalyEvent is one continuously-occurring anomaly tracked over
// time. Same fingerprint (kind, pattern, service) keeps re-using
// the row — last_seen advances on every detection, started_at and
// peak_ratio capture the CURRENT EPISODE (v0.10.1045: a re-fire after a
// gap longer than anomalyEpisodeGap starts a new one, MergeAnomalyCarry). An
// event is "active" iff last_seen is recent; the "cleared" status is
// derived in the query layer from last_seen freshness so we don't need
// a separate sweep.
type AnomalyEvent struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`    // "log_pattern" | "trace_op"
	Pattern      string  `json:"pattern"` // pattern name (logs) or operation name (trace ops)
	Service      string  `json:"service"`
	StartedAt    int64   `json:"startedAt"`    // unix ns — first observation of the current episode
	LastSeen     int64   `json:"lastSeen"`     // unix ns — most recent observation
	PeakRatio    float64 `json:"peakRatio"`    // worst ratio seen during the current episode
	CurrentRatio float64 `json:"currentRatio"` // ratio at last_seen
	CurrentCount uint64  `json:"currentCount"`
	Sample       string  `json:"sample"`
	// EpisodeCount / FirstStartedAt — v0.10.1049, yinelenen anomali ayrımı
	// (taşıma kuralı MergeAnomalyCarry'de). EpisodeCount: satırın ömründe bu
	// kaçıncı bölüm (1 = ilk; 0 = kolon okunmadı, 1 sayılır). FirstStartedAt:
	// İLK bölümün başlangıcı, unix ns; 0 = bilinmiyor (kolondan önce yazılmış
	// satır) → okuyucu StartedAt sayar (AnomalyPredatesDeploy). Satır son
	// bölümün başlangıcından 30 gün sonra TTL ile düştüğü için "yinelenen" =
	// "satırın ömrü içinde yeniden tetiklenmiş" demek.
	EpisodeCount   uint32 `json:"episodeCount,omitempty"`
	FirstStartedAt int64  `json:"firstStartedAt,omitempty"`
	// PredatesDeploy — v0.10.1049, SAKLANMAZ: yalnız deploy raporu / rollout
	// çekmecesinin "deploy sonrası anomaliler" satırında, o deploy'a göre
	// AnomalyPredatesDeploy true ise (düzenli yinelenen olay) dolar. Satır
	// listeden düşmez; ekran onu "yinelenen" diye işaretler.
	PredatesDeploy bool `json:"predatesDeploy,omitempty"`
	// Status is computed in the query, not stored. "active" while
	// last_seen >= now() - anomalyActiveAge (10m), otherwise "cleared".
	Status string `json:"status"`
	// Clusters — k8s/openshift cluster names the anomaly's
	// service was active in around the time of detection.
	// Enriched at read time (no schema migration); empty for
	// services without cluster attrs.
	Clusters []string `json:"clusters,omitempty"`
	// RecentDeploy — v0.5.286. Most recent deploy of this
	// service observed within `lookback` (default 30m) before
	// StartedAt. Populated at READ time by
	// EnrichAnomaliesWithDeploys so the /anomalies page can
	// show a "deployed v1.2.3 · 4m before" chip — collapses
	// the "did this break because of a deploy?" question into
	// a single glance.
	RecentDeploy *RecentDeploy `json:"recentDeploy,omitempty"`
	// v0.10.181 — operatör kararı (anomaly_verdicts, okuma zamanı eklenir):
	// 'anomaly' | 'not_anomaly'; yoksa boş. Susturmadan bağımsız.
	Verdict   string `json:"verdict,omitempty"`
	VerdictBy string `json:"verdictBy,omitempty"`
	VerdictAt int64  `json:"verdictAt,omitempty"` // unix ns
	// RootCause — compact top-suspect summary of the persisted
	// root-cause hypothesis the worker synthesized for this anomaly
	// (rc #3 of the anomaly → root-cause feature). Attached at READ
	// time by the /anomalies events handler via a single batch
	// GetHypotheses join (NO per-row fetch); nil when the worker
	// hasn't synthesized a hypothesis for this anchor yet. The
	// RootCauseRibbon renders the collapsed chip from this; the
	// expand fetches the full /anomalies/{id}/rootcause fan-out.
	RootCause *RootCauseSummary `json:"rootCause,omitempty"`
}

// anomalyFingerprintLen — FingerprintAnomaly kimliğinin uzunluğu: sha1'in
// küçük harf hex dökümünün ilk 16 karakteri. Kimlik ŞEKLİ tek yerde
// (v0.10.1042): üretici ve IsAnomalyFingerprint aynı sabiti okur.
const anomalyFingerprintLen = 16

// FingerprintAnomaly stitches the same (kind, pattern, service)
// detections into one event row across detector ticks. Stable
// across process restarts — sha1 is deterministic.
func FingerprintAnomaly(kind, pattern, service string) string {
	h := sha1.New()
	h.Write([]byte(kind))
	h.Write([]byte("|"))
	h.Write([]byte(pattern))
	h.Write([]byte("|"))
	h.Write([]byte(service))
	return hex.EncodeToString(h.Sum(nil))[:anomalyFingerprintLen]
}

// IsAnomalyFingerprint — s, FingerprintAnomaly'nin ürettiği şekilde bir olay
// kimliği mi: tam anomalyFingerprintLen karakter, yalnız [0-9a-f] (hex.Encode
// küçük harf yazar). v0.10.1042: susturma yazımı istemcinin gönderdiği olay
// kimliğini ancak bu şekildeyse olduğu gibi saklar; düz `kind|pattern|service`
// metni, büyük harf, kısa/uzun ya da boş değer kimlik SAYILMAZ.
func IsAnomalyFingerprint(s string) bool {
	if len(s) != anomalyFingerprintLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// uniqueAnomalyIDs — toplu okumanın IN listesi: her id BİR KEZ.
//
// Tekilleştirme gerekli çünkü aynı tikte aynı fingerprint'e iki olay
// gelebilir (aynı servis+metrik iki kez aday olursa) ve `id IN (x, x)`
// hem gereksiz bind argümanı hem de okurken çift satır demek. Saf +
// SIRA KORUNUR: bind argümanlarının sırası deterministik olmalı, yoksa
// aynı yazım iki podda iki farklı sorgu metni üretir ve query_log'da
// tek bir ifade olarak görünmez.
func uniqueAnomalyIDs(evs []AnomalyEvent) []string {
	ids := make([]string, 0, len(evs))
	seen := make(map[string]struct{}, len(evs))
	for _, e := range evs {
		if _, ok := seen[e.ID]; ok {
			continue
		}
		seen[e.ID] = struct{}{}
		ids = append(ids, e.ID)
	}
	return ids
}

// anomalyActiveAge — bir olayın "active" sayıldığı last_seen tazeliği.
// Okuma tarafındaki status türetmesinin TEK varsayılanı (GetAnomalyEvent,
// CountActiveAnomalyEvents, CountAnomalyEventsByStatus, ListAnomalyEvents —
// dördü de eskiden kendi `10 * time.Minute` literal'ini taşıyordu). Bölüm
// kararı bu sabiti DOĞRUDAN kullanmaz, ondan türeyen anomalyEpisodeGap'i
// kullanır (aşağıda neden).
const anomalyActiveAge = 10 * time.Minute

// anomalyEpisodeGap — yeni bölüm için gereken olay-saati boşluğu (v0.10.1045):
// 2 × aktif yaş + 150 sn = 22 dk 30 sn.
//
// Aktif yaşa EŞİT DEĞİL, bilinçli. 5 dk kovaya hizalı yazıcılar (trace_op,
// trace_op_latency) TEK kova kaçırınca (ör. kovada hata sayısı tabanın bir
// altında kaldı) ~10 dk ± saniyelik bir boşluk üretir; sınır aktif yaş olsaydı
// sürekli ateşleyen bir anomali yazı-tura ile "yeni bölüm" açardı. Satır o
// arada yalnız saniyelerce "cleared" görünür, evaluator'ın 60 sn'lik tiki
// çoğu zaman bunu hiç görmez; terfi Problem'i tazelenmeden kalır, 300 sn
// kapısı yeniden başlar, bayat süpürme onu yanlış gerekçeyle ("source
// silent") kapatır — ack, atanan kişi ve AI özeti gider — ve taze bir
// sayfayla warning olarak yeniden açılır.
//
// 22 dk 30 sn: en az aktif yaş + birkaç evaluator tiki + bir kova, ve 5 dk'nın
// katı DEĞİL — hizalı yazıcıların boşlukları (5 dk'nın katları ± saniye)
// sınıra hiç oturmaz: 1, 2 ve 3 kaçırılmış kova (≈10/15/20 dk) aynı bölüm, 4
// kova (≈25 dk) yeni bölüm. Sıralı yazıcıda her sıfırlamadan önce satır en az
// 12 dk 30 sn "cleared" görünmüştür; değişmeyen resolveClearedAnomalyPromotions
// geçişi terfi Problem'ini o sürede deterministik olarak "anomaly cleared" ile
// kapatmıştır. Sıfırlama yalnız YENİDEN terfiyi etkiler (taze başlangıç, 300 sn
// bekleme, yeni tepe) — operatörün istediği tam olarak bu.
//
// Kısıt: bir yazıcının yazım aralığı bu boşluktan uzunsa (ör. yapılandırılmış
// anomaly_record_interval > 22 dk 30 sn) her yazımı yeni bölüm açar ve kayıtçı
// olayları hiç terfi etmez (started_at = tik anı, 300 sn kapısı hiç dolmaz).
const anomalyEpisodeGap = 2*anomalyActiveAge + 150*time.Second

// anomalyNewEpisode — gelen gözlem saklı satırın bölümünü mü sürdürüyor,
// yoksa satır uzun süre "cleared" kalmış ve bu YENİ bir bölüm mü? SAF.
//
// Operatör (v0.10.1045): "Eski yüksek oran taşınmasın: kapanıp yeniden
// tetiklenen anomali, eski en yüksek oranıyla (ör. '66×', P1) görünüyor.
// Yeni tetiklenme sıfırdan başlasın."
//
// SAAT: olay saati, duvar saati DEĞİL — gelen olayın last_seen'i ile
// saklı last_seen arasındaki boşluk:
//   - sıralı yazıcılarda gelen last_seen ≤ yazım anı, yani olay-saati
//     boşluğu > anomalyEpisodeGap ⇒ yazım anında satır en az
//     (anomalyEpisodeGap − anomalyActiveAge) süredir "cleared" görünüyordu
//     (pod–CH saat kayması payı hariç). Tersi doğru değil: ingest gecikmesi
//     ya da geç kalan tik duvar saatinde "cleared" gösterip veride
//     kesintisiz olabilir — o hâlde bölüm korunur, bugünkü davranış;
//   - pod ile CH arasındaki saat kayması ve yazıcının tik gecikmesi karara
//     girmez; karar iki satırın saf işlevi, testte now() yok;
//   - sırası bozuk yazıcı (elastic_ml kayıtları skora göre sıralı gelir)
//     negatif boşluk üretir → yeni bölüm DEĞİL.
//
// SINIR: boşluk == anomalyEpisodeGap → AYNI bölüm; bir nanosaniye fazlası
// yeni bölüm.
func anomalyNewEpisode(incomingLastSeen, storedLastSeen int64) bool {
	return incomingLastSeen-storedLastSeen > int64(anomalyEpisodeGap)
}

// MergeAnomalyCarry — yazılacak satır: gelen olayın saklı satırla
// birleşimi (StartedAt / LastSeen / PeakRatio / EpisodeCount /
// FirstStartedAt; diğer alanlar gelen olaydan). SAF ve tablo-testli
// (anomaly_event_test.go, anomaly_episode_test.go; terfi kapısı ve
// /inbox önceliği kendi paketlerinde bunun üstünden pinli), çünkü toplu
// yazıma geçerken (v0.9.957) sessizce bozulabilecek TEK semantik bu:
// started_at'ın bölüm içinde korunması "süregelen anomali tek satırdır"
// sözleşmesinin tamamı, peak_ratio'nun bölüm içi monotonluğu da terfi
// kapısının girdisi.
//
//	exists=false                → ilk görülme: olayın kendi değerleri.
//	boşluk > anomalyEpisodeGap  → YENİ BÖLÜM (v0.10.1045): started_at ve
//	  (anomalyNewEpisode)          peak_ratio YALNIZ gelen olaydan; hiçbir
//	                               şey taşınmaz. Önceden tepe ve ilk
//	                               başlangıç aynı parmak izi için 30 gün
//	                               (TTL) taşınıyordu: günler sonra yeniden
//	                               tetiklenen olay eski, yük kaynaklı
//	                               tepeyle /inbox'ta P1 görünüyor, terfi
//	                               kapısının 300 sn'lik yaş şartını eski
//	                               started_at yüzünden ilk tikte geçip
//	                               yaş tabanlı eskalasyonla doğrudan
//	                               critical açılıyordu.
//	aynı bölüm                  → started_at KORUNUR, peak_ratio yalnız
//	                               YÜKSELİR, last_seen GERİ GİTMEZ.
//
// last_seen'in geri gitmemesi bölüm kararının parçası: sırası bozuk ya da
// last_seen'i 0 olan bir yazım (örnek sorgusu düşen trace_op_latency,
// skora göre sıralı elastic_ml kayıtları) saklı last_seen'i geriye
// çekseydi, BİR SONRAKİ normal yazım sahte bir boşluk görür ve süren
// bölümü sıfırlardı.
//
// v0.10.1049 — bölüm SAYACI ve İLK başlangıç. Operatör: "Yinelenen anomali
// ayrımı: her gece tekrar eden bir anomali artık her seferinde 'yeni'
// görünüyor ve önceki deploy'a bağlanıyor." v0.10.1045 yeni bölümde hiçbir
// şey taşımıyordu; "yeni mi, yinelenen mi" sorusunun cevabı satırda yoktu.
// İki alan, aynı üç dal:
//
//	ilk görülme  → episode_count 1, first_started_at = gelen started_at.
//	YENİ BÖLÜM   → episode_count = saklı + 1; first_started_at = saklı
//	               (saklı 0 = kolondan önceki satır → saklı started_at).
//	               started_at / tepe yine YALNIZ gelen olaydan (1045 aynen).
//	aynı bölüm   → ikisi de saklı satırdan, değişmeden.
//
// Sırası bozuk yazım bölüm açmadığı için sayacı da artırmaz (aynı karar).
func MergeAnomalyCarry(e, stored AnomalyEvent, exists bool) AnomalyEvent {
	out := e
	if !exists {
		out.PeakRatio = e.CurrentRatio
		out.EpisodeCount = 1
		out.FirstStartedAt = e.StartedAt
		return out
	}
	if anomalyNewEpisode(e.LastSeen, stored.LastSeen) {
		out.PeakRatio = e.CurrentRatio
		out.EpisodeCount = storedEpisodeCount(stored) + 1
		out.FirstStartedAt = stored.FirstStartedAt
		if out.FirstStartedAt <= 0 {
			out.FirstStartedAt = stored.StartedAt
		}
		return out
	}
	out.EpisodeCount = storedEpisodeCount(stored)
	out.FirstStartedAt = stored.FirstStartedAt
	out.StartedAt = stored.StartedAt
	out.PeakRatio = stored.PeakRatio
	if e.CurrentRatio > out.PeakRatio {
		out.PeakRatio = e.CurrentRatio
	}
	if stored.LastSeen > out.LastSeen {
		out.LastSeen = stored.LastSeen
	}
	return out
}

// storedEpisodeCount — saklı satırın bölüm sayısı; 0 (kolon okunmadı) 1
// sayılır. Kolon varken eski satır DEFAULT 1 okur; 0 yalnız probe'suz
// okumada görülür ve o yazım kolonu zaten INSERT listesine koymaz.
func storedEpisodeCount(stored AnomalyEvent) uint32 {
	if stored.EpisodeCount == 0 {
		return 1
	}
	return stored.EpisodeCount
}

// Düzenli yinelenme eşikleri (AnomalyPredatesDeploy). Bir anomali, deploy'dan
// SONRA başlamış bir bölümünde ancak en az anomalyRegularMinEpisodes bölüm
// görülmüşse VE bölümler arası ortalama aralık anomalyRegularMaxMeanGap'i
// aşmıyorsa "deploy'dan önce de görülüyordu" sayılır: her gece koşan iş
// üçüncü gecesinden itibaren (ortalama 24 sa). İki bölüm (deploy kırdı →
// rollback → bozuk yeniden deploy) ya da seyrek bir eski kıpırtı (20 gün
// önce bir kez) bu tanıma GİRMEZ — deploy atfı korunur.
const (
	anomalyRegularMinEpisodes = 3
	anomalyRegularMaxMeanGap  = 48 * time.Hour
)

// AnomalyPredatesDeploy — bu deploy anomaliyi AÇIKLAMIYOR mu, çünkü anomali
// deploy'dan önce de DÜZENLİ olarak görülüyordu? SAF, tablo-testli.
// v0.10.1049; deploy atfının TEK kuralı — üç tüketici aynı işlevden okur:
//
//   - deploy raporu / rollout çekmecesi (api anomaliesSinceDeploy): satır
//     listede KALIR, yalnız "yinelenen" diye işaretlenir (gizlenmez);
//   - kök-neden işçisi (anomaly synthInputForAnomaly): deploy adayı DÜŞMEZ,
//     diğer kanıt katmanlarının altına iner — ölçülen etki gerileme
//     gösterirse normal puanını geri alır (correlator.Synthesize);
//   - deploy çipi (EnrichAnomaliesWithDeploys / pickAnomalyDeploy): o deploy
//     satıra "olası neden" diye iliştirilmez.
//
// Kural (sırayla):
//
//	earliest = min(firstStartedAt (> 0 ise), startedAt)
//	earliest ≥ deploy            → false (anomali deploy'dan sonra doğdu)
//	startedAt < deploy           → true  (bu bölüm deploy'dan önce başladı —
//	                                     bugünkü `StartedAt >= since` kuralı,
//	                                     tek bölümlü satırda birebir)
//	count ≥ 3 VE (startedAt − earliest) / (count − 1) ≤ 48 sa → true
//	aksi                         → false (deploy atfı korunur)
//
// Kabul edilen bedel: ortalama aralığa deploy'dan SONRAKİ bölümler de girer
// (sayaç bölümlerin deploy'a göre nerede olduğunu bilmez). Deploy'dan bir
// gün önce tek bölüm görülüp deploy sonrası saatte bir yeniden tetiklenen
// bir anomali üçüncü bölümünde (ortalama ~12 sa) "düzenli" görünür; o hâlde
// rapor satırı işaretli kalır (gizlenmez) ve kök-neden adayı ölçülen
// gerilemeyle normal puanını geri alır.
//
// Sınır: earliest == deploy anı → false (bugünkü kapsayıcılık). firstStartedAt
// 0 (kolondan önceki satır) → started_at; bozuk satırda first > started olsa
// da küçüğü alınır.
func AnomalyPredatesDeploy(firstStartedAt, startedAt int64, episodeCount uint32, deployTime int64) bool {
	earliest := startedAt
	if firstStartedAt > 0 && firstStartedAt < earliest {
		earliest = firstStartedAt
	}
	if earliest >= deployTime {
		return false
	}
	if startedAt < deployTime {
		return true
	}
	if episodeCount < anomalyRegularMinEpisodes {
		return false
	}
	meanGap := (startedAt - earliest) / int64(episodeCount-1)
	return meanGap <= int64(anomalyRegularMaxMeanGap)
}

// probeAnomalyEpisodeCols — anomaly_events episode_count + first_started_at
// kolonlarını taşıyor mu (v0.10.1049). ai_calls probe'larının şekli
// (probeAICallsCachedColumn): system.columns METADATA okuması — veri
// hacminden bağımsız, tek geçici zaman aşımı yanlış-false üretmez (v0.10.834
// dersi). İki kolon birlikte: ikisi aynı ALTER turunda gidiyor, biri yoksa
// ikisi de yok sayılır.
//
// İKİ yerde koşar: boot (migrate, ALTER'lardan sonra) ve ertelenen DDL indikten
// sonra reprobePromotedAttrs → reprobeAnomalyEpisodeCols (ddl_defer.go).
// İkincisi şart: küme kipinde kolonu EKLEYEN boot burayı false okur ve sayaç /
// ilk görülme BİRİKİMLİ olduğu için o pod'un her yazımı onları DEFAULT'a (1 /
// 0) geri yazar — bayrak süreç ömrü boyunca donsaydı bu bir sonraki restart'a
// dek sürerdi.
func (s *Store) probeAnomalyEpisodeCols(ctx context.Context) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = currentDatabase() AND table = 'anomaly_events'
		   AND name IN ('episode_count', 'first_started_at')
		 SETTINGS max_execution_time = 5`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 2, nil
}

// reprobeAnomalyEpisodeCols — ertelenen DDL sonrası yeniden deneme. Bayrak
// yalnız false → true döner (kolon düşmez); geçiş BİR KEZ loglanır
// (CompareAndSwap — eşzamanlı iki deneme iki satır basmaz). Dönüş: bayrağın
// SON hâli. Sonraki her çağrı (UpsertAnomalyEvents / Get / List) bayrağı kendi
// başında bir kez okur, yani geçiş bir sonraki çağrıdan itibaren geçerli.
func (s *Store) reprobeAnomalyEpisodeCols(ctx context.Context) bool {
	if s.hasAnomalyEpisodeCols.Load() {
		return true
	}
	if ok, _ := s.probeAnomalyEpisodeCols(ctx); ok && s.hasAnomalyEpisodeCols.CompareAndSwap(false, true) {
		log.Printf("[chstore] anomaly_events episode_count/first_started_at ertelenen DDL sonrası görüldü — bölüm sayacı yazımı ve okuması devrede (restart gerekmedi)")
	}
	return s.hasAnomalyEpisodeCols.Load()
}

// anomalyEventSelectExpr — GetAnomalyEvent / ListAnomalyEvents'in ortak satır
// listesi (status ayrı, çağıran ekler). episode = store'un probe'u
// (hasAnomalyEpisodeCols): küme kipinde kolonu ekleyen boot DDL'i arka plana
// ertelediği için (v0.9.614) kolonu koşulsuz okumak her anomali okumasını
// "no such column" ile düşürürdü. Sıra anomalyEventScanDest'le birebir aynı
// (pozisyonel Scan); ikisi AYNI bool'dan türer.
func anomalyEventSelectExpr(episode bool) string {
	expr := `id, kind, pattern, service,
		       toUnixTimestamp64Nano(started_at),
		       toUnixTimestamp64Nano(last_seen),
		       peak_ratio, current_ratio, current_count, sample`
	if episode {
		expr += `,
		       episode_count, toUnixTimestamp64Nano(first_started_at)`
	}
	return expr
}

// anomalyEventScanDest — anomalyEventSelectExpr'in hedefleri, aynı sırayla.
func anomalyEventScanDest(e *AnomalyEvent, episode bool) []any {
	dst := []any{
		&e.ID, &e.Kind, &e.Pattern, &e.Service,
		&e.StartedAt, &e.LastSeen,
		&e.PeakRatio, &e.CurrentRatio, &e.CurrentCount, &e.Sample,
	}
	if episode {
		dst = append(dst, &e.EpisodeCount, &e.FirstStartedAt)
	}
	return dst
}

// anomalyCarrySelectSQL — UpsertAnomalyEvents'in taşıma okuması (n id).
func anomalyCarrySelectSQL(n int, episode bool) string {
	cols := `id, toUnixTimestamp64Nano(started_at), toUnixTimestamp64Nano(last_seen), peak_ratio`
	if episode {
		cols += `, episode_count, toUnixTimestamp64Nano(first_started_at)`
	}
	return `SELECT ` + cols + `
		 FROM anomaly_events FINAL
		 WHERE id IN (` + chPlaceholders(n) + `)`
}

// anomalyCarryScanDest — anomalyCarrySelectSQL'in hedefleri, aynı sırayla.
func anomalyCarryScanDest(id *string, p *AnomalyEvent, episode bool) []any {
	dst := []any{id, &p.StartedAt, &p.LastSeen, &p.PeakRatio}
	if episode {
		dst = append(dst, &p.EpisodeCount, &p.FirstStartedAt)
	}
	return dst
}

// anomalyInsertSQL / anomalyInsertRow — toplu yazımın kolon listesi ve bir
// satırın değerleri, AYNI bool'dan. Probe false iken (kolon henüz inmemiş)
// iki kolon listeden düşer: satır DEFAULT'u alır (1 / 0 = "yinelenmemiş,
// ilk görülme bilinmiyor"), yazım code 16 ile ölmez.
func anomalyInsertSQL(episode bool) string {
	cols := `id, kind, pattern, service, started_at, last_seen,
		 peak_ratio, current_ratio, current_count, sample`
	if episode {
		cols += `,
		 episode_count, first_started_at`
	}
	return `INSERT INTO anomaly_events
		(` + cols + `)`
}

func anomalyInsertRow(w AnomalyEvent, episode bool) []any {
	row := []any{
		w.ID, w.Kind, w.Pattern, w.Service,
		time.Unix(0, w.StartedAt),
		time.Unix(0, w.LastSeen),
		w.PeakRatio, w.CurrentRatio, w.CurrentCount, w.Sample,
	}
	if episode {
		row = append(row, w.EpisodeCount, time.Unix(0, w.FirstStartedAt))
	}
	return row
}

// foldAnomalyCarryRow — taşıma okumasının bir satırını haritaya katar; aynı
// id İKİ kez gelirse last_seen'i BÜYÜK olan kalır (v0.10.1045). SAF.
//
// migrations/0010'u uygulamamış (hâlâ `PARTITION BY toDate(started_at)`)
// bir kurulumda FINAL partition sınırını aşmayan bir ayarla koşarsa aynı
// id'nin iki sürümü döner. Son yazan kazansaydı ve o bayat sürüm olsaydı,
// bölüm kararı her tikte sahte bir boşluk görüp yeni bölüm açardı.
func foldAnomalyCarryRow(prev map[string]AnomalyEvent, id string, p AnomalyEvent) {
	if old, ok := prev[id]; ok && old.LastSeen >= p.LastSeen {
		return
	}
	prev[id] = p
}

// UpsertAnomalyEvent records (or refreshes) one event. Thin wrapper over
// UpsertAnomalyEvents so the peak_ratio / started_at carry-forward
// semantics live in EXACTLY ONE place — the two paths silently drifting
// apart would show up as anomalies that reset their own start time.
func (s *Store) UpsertAnomalyEvent(ctx context.Context, e AnomalyEvent) error {
	return s.UpsertAnomalyEvents(ctx, []AnomalyEvent{e})
}

// UpsertAnomalyEvents records (or refreshes) a BATCH of events in two
// round-trips total: one FINAL lookup for the whole id set, one INSERT.
//
// ── Neden toplu (v0.9.957) ───────────────────────────────────────────
// Davranış motoru (internal/anomaly/behavior_scan.go) bir tikte 37 olay
// yazıyordu ve bunu 37 ardışık UpsertAnomalyEvent çağrısıyla yapıyordu.
// ÖLÇÜLDÜ: 25.6 saniyelik tikin ~20 saniyesi buradaydı — MV sorgusu
// değil, olay YAZIMI. Her çağrı iki gidiş-dönüş (FINAL SELECT + INSERT)
// demek, yani 74 round-trip; her biri tek satır için.
//
// FINAL bir ReplacingMergeTree üzerinde ucuz değildir: parçaları okuma
// anında birleştirir. Tek `id` için çalıştırmak da, 37 `id` için
// çalıştırmak da neredeyse aynı maliyeti öder — bu yüzden kazanç
// doğrusal değil, N katı.
//
// IN bir DEĞER LİSTESİ, alt sorgu DEĞİL: Distributed bir kurulumda
// GLOBAL IN gerektiren şey shard-yerel koşacak ALT SORGUdur (v0.5.427);
// bind edilmiş sabit liste her shard'a olduğu gibi gider.
//
// Boş dilim → HİÇ sorgu yok. "Aday üretmeyen tik CH'ye dokunmamalı"
// hem ucuz hem de dürüst: query_log'da iz bırakmayan bir tik gerçekten
// bir şey yazmamıştır.
func (s *Store) UpsertAnomalyEvents(ctx context.Context, evs []AnomalyEvent) error {
	if len(evs) == 0 {
		return nil
	}
	// TEK okuma: mevcut satırların started_at + last_seen + peak_ratio'su.
	// Süren bir bölümde started_at KORUNUR (satır tek sürekli bir anomaliyi
	// temsil eder) ve peak_ratio yalnız yükselir — CH'de bu motorda atomik
	// max-on-upsert primitifi yok, uygulama katmanı taşıyor. last_seen
	// v0.10.1045'te eklendi: bölüm kararı (MergeAnomalyCarry) olay-saati
	// boşluğunu ondan ölçer — ek sorgu değil, aynı satırın bir kolonu daha.
	// v0.10.1049 — episode_count + first_started_at da aynı okumada (probe
	// true iken); okuma ve yazım çağrı başına TEK anlık görüntüden (`ep`)
	// kurulur — yeniden probe (ddl_defer.go) çağrının ortasında bayrağı
	// çevirse bile bir çağrı ya baştan sona kolonlu ya kolonsuz.
	ep := s.hasAnomalyEpisodeCols.Load()
	prev := make(map[string]AnomalyEvent, len(evs))
	ids := uniqueAnomalyIDs(evs)
	rows, err := s.conn.Query(ctx, anomalyCarrySelectSQL(len(ids), ep), toAnySlice(ids)...)
	if err != nil {
		// v0.10.1045 — okuma hatasında YAZMA YOK (yumuşak-hata yönü
		// bilinçli ters çevrildi). Eskiden prev boş bırakılıp her olay
		// "ilk görülme" gibi yazılıyordu: çağrıdaki TÜM olayların
		// started_at'i ve tepesi sıfırlanıyordu — davranış motorunun toplu
		// yazımında bütün filo için. Yazıcılar durumsuz ve bir sonraki
		// tikte aynı olayları yeniden üretir; bir tiklik gecikme, süren
		// bölümleri sessizce sıfırlamaktan ucuz. Hata çağırana döner, o
		// loglar ve diğer işine devam eder.
		return fmt.Errorf("anomaly_events carry read (write skipped this tick): %w", err)
	}
	for rows.Next() {
		var id string
		var p AnomalyEvent
		if err := rows.Scan(anomalyCarryScanDest(&id, &p, ep)...); err != nil {
			rows.Close()
			return err
		}
		foldAnomalyCarryRow(prev, id, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	// Explicit column list: anomaly_events has a `version` column with a
	// DEFAULT (toUnixTimestamp64Nano(now64(9))). The bare-form
	// "INSERT INTO anomaly_events" requires EVERY column (11 before
	// v0.10.1049, 13 once episode_count / first_started_at land); supplying
	// fewer args trips clickhouse-go's "expected N arguments" error,
	// which spammed the logs once the table grew that DEFAULT column.
	// Naming the columns we actually populate lets the DEFAULT do its
	// job and the recorder stays in sync without handcrafting a version
	// value here. DEFAULT toplu yazımda da doğru çalışır: her satır
	// KENDİ now64()'ünü alır, yani ReplacingMergeTree'nin version
	// semantiği (aynı id için en son sürüm kazanır) korunur.
	// v0.10.1049 — liste ve satır anomalyInsertSQL/anomalyInsertRow'dan, aynı
	// `ep` ile (arite şekil testi: anomaly_episode_test.go).
	batch, err := s.conn.PrepareBatch(ctx, anomalyInsertSQL(ep))
	if err != nil {
		return err
	}
	for _, e := range evs {
		p, exists := prev[e.ID]
		w := MergeAnomalyCarry(e, p, exists)
		if err := batch.Append(anomalyInsertRow(w, ep)...); err != nil {
			return err
		}
	}
	return batch.Send()
}

// GetAnomalyEvent reads one event by id (the FingerprintAnomaly hash).
// FINAL collapses the ReplacingMergeTree versions to the latest row.
// Returns (nil, nil) on no-match so the API layer can answer a clean 404
// instead of treating "not found" as an error. The status is derived the
// same way ListAnomalyEvents derives it — active iff last_seen is fresh
// within ActiveAge (default 10m) — so an event-anchored read agrees with
// the list view. Bounded by the id equality on the PK; anomaly_events is a
// small state table, not spans/metric_points, so no time-bound is needed.
func (s *Store) GetAnomalyEvent(ctx context.Context, id string, activeAge time.Duration) (*AnomalyEvent, error) {
	if activeAge == 0 {
		activeAge = anomalyActiveAge
	}
	var e AnomalyEvent
	ep := s.hasAnomalyEpisodeCols.Load() // v0.10.1049 — liste ve hedefler aynı anlık görüntüden
	row := s.conn.QueryRow(ctx, `
		SELECT `+anomalyEventSelectExpr(ep)+`,
		       if(last_seen >= now64() - INTERVAL ? SECOND, 'active', 'cleared') AS status
		FROM anomaly_events FINAL
		WHERE id = ?
		LIMIT 1`,
		int64(activeAge.Seconds()),
		id,
	)
	if err := row.Scan(append(anomalyEventScanDest(&e, ep), &e.Status)...); err != nil {
		// clickhouse-go's QueryRow surfaces an empty result as this exact
		// string (no typed sentinel) — the same no-rows idiom the other
		// by-id reads use (dashboard.go, monitor.go). Soft "not found".
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// ListAnomalyEventsFilter is the read-side cut. SinceNs filters by
// last_seen >= … so a 24h window returns both currently-active
// events and ones that cleared up to 24h ago.
type ListAnomalyEventsFilter struct {
	SinceNs   int64         // unix ns; 0 = last 24h default
	ActiveAge time.Duration // last_seen freshness for "active" status; 0 = 10m default
	Limit     int
	// Services (v0.9.353) constrains rows to this service set IN SQL, so the
	// LIMIT lands on rows the team filter would keep. The inbox used to fetch
	// up to srcLimit rows and drop other teams' rows in Go — the
	// filter-after-LIMIT class, and the reason an owner pick was slow: the
	// scan pulled the whole estate to find one team's rows. STRICT IN — a
	// service-less anomaly row does not match a team filter (same semantics
	// as the Go pass it backs). nil = no constraint; EMPTY = match nothing
	// (a team with no member services must yield an empty page, never an
	// unfiltered one).
	Services []string
	// FromNs/ToNs (v0.9.394, annotation şeridi Ş1) — started_at pencere
	// sorgusu: "bu pencerede BAŞLAYAN anomaliler". 0 = sınırsız (eski
	// davranış SinceNs üzerinden aynen sürer).
	FromNs int64
	ToNs   int64
	// ActiveOnly (v0.9.335) narrows to firing events IN SQL, so the LIMIT
	// lands on rows the caller will actually keep.
	//
	// The inbox's "open" pivot keeps only active events and dropped the rest
	// in Go — AFTER the limit. That is the same silent-scope shape already
	// fixed for problems and incidents (v0.9.322): a caller asking for 2000
	// rows to find 20 active ones spends its whole budget on cleared history.
	// "Active" is exactly the freshness predicate the status column is
	// derived from, so this adds no new notion — it moves an existing one to
	// where the LIMIT can respect it.
	ActiveOnly bool
	// ExcludeIDs (v0.10.1042, operatör: "Anomalide 'Mute' sonrası satır
	// listeden düşsün") — bu olay kimlikleri (FingerprintAnomaly = susturma
	// parmak izi) SQL'de elenir. Inbox'ın open görünümü aktif susturmaları
	// buradan geçirir: Go'da LIMIT'ten SONRA düşürmek ActiveOnly'nin
	// (v0.9.335) kapattığı sınıfı geri açardı — susturulmuş satırlar tarama
	// bütçesini yer, scanCapped yalan söyler. nil/boş = kısıt yok.
	// CountActiveAnomalyEvents aynı yüklemi (anomalyExcludeIDsSQL) kullanır
	// ki rozet ile liste aynı kümeyi saysın.
	ExcludeIDs []string
}

// anomalyExcludeIDsSQL — ExcludeIDs'in TEK SQL karşılığı (v0.10.1042). Liste
// ve rozet sayımı aynı işlevden geçer: iki ayrı yazım, rozet ile listenin
// ayrışması demek olurdu (v0.9.322 sınıfı). Boş küme koşul ÜRETMEZ (bugünkü
// SQL birebir).
func anomalyExcludeIDsSQL(ids []string) (string, []any) {
	if len(ids) == 0 {
		return "", nil
	}
	return " AND id NOT IN (" + chPlaceholders(len(ids)) + ")", toAnySlice(ids)
}

// CountActiveAnomalyEvents returns the number of anomaly events currently
// "active" — i.e. last_seen fresher than activeAge (the same derivation the
// list/detail reads use). For the /inbox badge (v0.8.288): a cheap COUNT(*)
// FINAL on the small state table, no row scan. activeAge 0 → 10m default.
// envServices follows the same nil/empty contract as
// CountProblemsInStatuses: nil = unscoped, empty = env resolved to no
// services (only service-less rows count), otherwise membership.
//
// excludeIDs (v0.10.1042) — aktif susturmaların parmak izleri: inbox open
// listesi bu olayları SQL'de eliyor (ListAnomalyEventsFilter.ExcludeIDs),
// rozet de aynı yüklemle (anomalyExcludeIDsSQL) saymazsa kenar çubuğu
// listede olmayan satırı vaat eder. nil/boş = kısıt yok.
func (s *Store) CountActiveAnomalyEvents(ctx context.Context, activeAge time.Duration, envServices []string, excludeIDs []string) (uint64, error) {
	if activeAge == 0 {
		activeAge = anomalyActiveAge
	}
	args := []any{int64(activeAge.Seconds())}
	envSQL := ""
	if envServices != nil {
		if len(envServices) == 0 {
			envSQL = " AND service = ''"
		} else {
			envSQL = " AND (service = '' OR service IN ?)"
			args = append(args, envServices)
		}
	}
	exclSQL, exclArgs := anomalyExcludeIDsSQL(excludeIDs)
	args = append(args, exclArgs...)
	var n uint64
	err := s.conn.QueryRow(ctx, `
		SELECT count() FROM anomaly_events FINAL
		WHERE last_seen >= now64() - INTERVAL ? SECOND`+envSQL+exclSQL,
		args...,
	).Scan(&n)
	return n, err
}

// CountAnomalyEventsByStatus (v0.9.465, dürüstlük A9) — pencere içi
// GERÇEK aktif/cleared toplamları: /anomalies sayfası sayıları yüklü
// 200'lük sayfadan türetiyordu; gürültülü günde ikisi de yalan
// söylüyordu. Status hesabı ListAnomalyEvents ile AYNI ifade
// (last_seen tazeliği, 10dk varsayılan).
func (s *Store) CountAnomalyEventsByStatus(ctx context.Context, sinceNs int64, activeAge time.Duration) (active, cleared uint64, err error) {
	if activeAge == 0 {
		activeAge = anomalyActiveAge
	}
	row := s.conn.QueryRow(ctx, `
		SELECT countIf(last_seen >= now64() - INTERVAL ? SECOND),
		       countIf(last_seen <  now64() - INTERVAL ? SECOND)
		FROM anomaly_events FINAL
		WHERE toUnixTimestamp64Nano(last_seen) >= ?
		SETTINGS max_execution_time = 5`,
		int64(activeAge.Seconds()), int64(activeAge.Seconds()), sinceNs)
	if err := row.Scan(&active, &cleared); err != nil {
		return 0, 0, err
	}
	return active, cleared, nil
}

func (s *Store) ListAnomalyEvents(ctx context.Context, f ListAnomalyEventsFilter) ([]AnomalyEvent, error) {
	if f.Limit == 0 {
		f.Limit = 200
	}
	if f.ActiveAge == 0 {
		f.ActiveAge = anomalyActiveAge
	}
	since := f.SinceNs
	if since == 0 {
		since = time.Now().Add(-24 * time.Hour).UnixNano()
	}

	activeSQL := ""
	if f.ActiveOnly {
		activeSQL = " AND last_seen >= now64() - INTERVAL ? SECOND"
	}
	svcSQL := ""
	if f.Services != nil {
		if len(f.Services) == 0 {
			svcSQL = " AND 1 = 0"
		} else {
			svcSQL = " AND service IN (" + chPlaceholders(len(f.Services)) + ")"
		}
	}
	// v0.9.394 (annotation şeridi Ş1) — started_at pencere sorgusu:
	// şerit "bu pencerede BAŞLAYAN anomaliler"i ister; SinceNs (last_seen
	// tabanlı) bunu ifade edemiyordu.
	winSQL := ""
	if f.FromNs > 0 {
		winSQL += " AND toUnixTimestamp64Nano(started_at) >= ?"
	}
	if f.ToNs > 0 {
		winSQL += " AND toUnixTimestamp64Nano(started_at) < ?"
	}
	// v0.10.1042 — susturulmuş olaylar LIMIT'ten ÖNCE elenir (ExcludeIDs).
	exclSQL, exclArgs := anomalyExcludeIDsSQL(f.ExcludeIDs)
	args := []any{int64(f.ActiveAge.Seconds()), since}
	if f.ActiveOnly {
		args = append(args, int64(f.ActiveAge.Seconds()))
	}
	args = append(args, toAnySlice(f.Services)...)
	if f.FromNs > 0 {
		args = append(args, f.FromNs)
	}
	if f.ToNs > 0 {
		args = append(args, f.ToNs)
	}
	args = append(args, exclArgs...)
	args = append(args, f.Limit)
	ep := s.hasAnomalyEpisodeCols.Load() // v0.10.1049 — liste ve hedefler aynı anlık görüntüden
	rows, err := s.conn.Query(ctx, `
		SELECT `+anomalyEventSelectExpr(ep)+`,
		       if(last_seen >= now64() - INTERVAL ? SECOND, 'active', 'cleared') AS status
		FROM anomaly_events FINAL
		WHERE toUnixTimestamp64Nano(last_seen) >= ?`+activeSQL+svcSQL+winSQL+exclSQL+`
		-- v0.9.326 — this used to order by the status STRING descending,
		-- which puts CLEARED FIRST.
		-- ClickHouse compares these lexically and 'active' < 'cleared', so
		-- descending is the exact inverse of the intent. Every caller wants
		-- the firing ones: the inbox keeps only active rows, the evaluator
		-- and root-cause worker act on them, /anomalies leads with them.
		-- With the LIMIT filled by cleared rows first, a busy window pushes
		-- the active ones off the end entirely — the list shows none while
		-- CountActiveAnomalyEvents (a SQL count, no ordering) still reports
		-- them. Local, 24h: 181 cleared vs 10 active — 191 of a 200 default.
		-- One more day of history and the active rows vanish silently.
		-- Written as a boolean so the lexical trap can't come back.
		ORDER BY status = 'active' DESC, last_seen DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AnomalyEvent
	for rows.Next() {
		var e AnomalyEvent
		if err := rows.Scan(append(anomalyEventScanDest(&e, ep), &e.Status)...); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
