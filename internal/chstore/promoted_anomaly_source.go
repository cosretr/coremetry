package chstore

// promoted_anomaly_source.go — v0.10.1054: anomaliden terfi eden Problem de
// "yinelenen" kuralına uyar.
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun;
// bugün deploy'a hâlâ eski kurala göre bağlanıyor."
//
// v0.10.1049 deploy atfının TEK kuralını (AnomalyPredatesDeploy) anomali
// olayının tüketicilerine bağladı. Güçlü olaydan terfi eden Problem (rule id
// `anomaly-auto:<fp>`, evaluator promoteStrongAnomalies) ise kendi deploy
// atfını taşıyordu ve bölüm sayacını hiç bilmiyordu: Problems kuyruğunda
// anomali satırı "yinelenen" der ve deploy çipi göstermezken hemen yanındaki
// terfi Problem'i aynı deploy'u "olası neden" diye gösteriyordu (satır çipi,
// detay sayfasının deploy kutusu, kök-neden çıpası, deploy raporu).
//
// Bu dosya iki şey taşır:
//
//   - kural id'sinden kaynak olay kimliğini çıkaran SAF ayrıştırıcı — olay
//     kimliği id'nin içinde (`anomaly-auto:` + FingerprintAnomaly), sorgu yok;
//   - eldeki Problem'lerin kaynak olaylarını TEK toplu okumayla getiren
//     okuyucu (çağrı başına bir okuma; satır başına okuma YOK).
//
// Bölüm eşleşmesi: terfi Problem'inin StartedAt'i açılışta olayın bölüm
// başlangıcıdır ve bölüm içinde değişmez (MergeAnomalyCarry started_at'i
// korur, terfi tazelemesi açık satırın StartedAt'ini taşır). Olay o zamandan
// beri YENİ bir bölüme geçtiyse (kapanmış, eski bir terfi Problem'i) sayaç o
// Problem'in bölümünü anlatmaz → eşleşme yok, atıf bugünkü gibi.
//
// Yumuşak-hata yönü: okuma hatası, TTL ile düşmüş olay, başka bölüm, bölüm
// kolonlarının bu süreçte henüz görülmemesi → iliştirme yok, deploy atfı
// BUGÜNKÜ gibi (okunamayan süzgeç süzmez). Bilinçli: yanlış "yinelenen"
// gerçek bir deploy gerilemesini gizler; eksik işaret yalnız bugünkü atfı
// bırakır.

import (
	"context"
	"strings"
)

// PromotedAnomalyEventID — terfi Problem'inin kural id'sinden kaynak anomali
// olayının kimliği (FingerprintAnomaly). SAF, sorgusuz.
//
// Kabul edilen biçim: PromotedAnomalyRulePrefix + 16 küçük harf hex (ve
// isteğe bağlı ":<servis>" — Problem kimliğinin şekli, `anomaly-auto:<fp>:<servis>`;
// saklanan rule_id son eki taşımaz). Önek yoksa ("anomaly:", "anomaly-cluster:",
// kural kimlikleri), parmak izi şekli tutmuyorsa ya da parmak izinden sonra
// ':' dışında bir karakter geliyorsa ok=false — o Problem terfi Problem'i
// sayılmaz ve atfı bugünkü gibi kalır.
func PromotedAnomalyEventID(ruleID string) (string, bool) {
	rest, ok := strings.CutPrefix(ruleID, PromotedAnomalyRulePrefix)
	if !ok || len(rest) < anomalyFingerprintLen {
		return "", false
	}
	fp := rest[:anomalyFingerprintLen]
	if !IsAnomalyFingerprint(fp) {
		return "", false
	}
	if len(rest) > anomalyFingerprintLen && rest[anomalyFingerprintLen] != ':' {
		return "", false
	}
	return fp, true
}

// promotedSourceLookupMax — tek okumanın kimlik tavanı. Bir problem sayfası
// (kuyruk, rapor, kök-neden tiki) bundan fazla TEKİL terfi Problem'i
// taşırsa fazlası iliştirilmez ve atfı bugünkü gibi kalır (yumuşak yön).
const promotedSourceLookupMax = 1000

// promotedAnomalyIDs — probs'taki terfi Problem'lerinin TEKİL kaynak olay
// kimlikleri, ilk görülme sırasıyla (bind argümanları deterministik — aynı
// sayfa iki podda aynı sorgu metnini üretir), en çok promotedSourceLookupMax.
// SAF.
func promotedAnomalyIDs(probs []Problem) []string {
	var ids []string
	seen := map[string]struct{}{}
	for _, p := range probs {
		id, ok := PromotedAnomalyEventID(p.RuleID)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		if len(ids) >= promotedSourceLookupMax {
			break
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// promotedSourceSQL — PromotedAnomalySources'ın SAF kurucusu (n kimlik).
// YALNIZ kullanılan beş kolon (id, started_at, last_seen, iki bölüm kolonu —
// pattern / sample / oranlar okunmaz; yalnız probe true iken çağrılır).
// `id IN (…)` bind edilmiş değer listesi, alt sorgu değil (Distributed'da
// GLOBAL gerekmez, UpsertAnomalyEvents'in taşıma okumasıyla aynı şekil);
// ORDER BY id üstünde PK okuması. LIMIT 2n: 0010'u uygulamamış kurulumda
// FINAL aynı id'nin iki gün-partition kopyasını döndürebilir
// (foldAnomalyCarryRow gerekçesi). Tavan 2 sn: sıcak okuma yollarında
// (Problems kuyruğu, detay) yavaş bir okuma sayfayı bekletmez, düşer ve
// atıf bugünkü gibi kalır.
func promotedSourceSQL(n int) string {
	return `SELECT id, toUnixTimestamp64Nano(started_at), toUnixTimestamp64Nano(last_seen),
		       episode_count, toUnixTimestamp64Nano(first_started_at)
		FROM anomaly_events FINAL
		WHERE id IN (` + chPlaceholders(n) + `)
		LIMIT ?
		SETTINGS max_execution_time = 2`
}

// PromotedAnomalySources — probs içindeki anomaliden terfi etmiş Problem'lerin
// kaynak olayları, olay kimliğine göre; TEK toplu okuma.
//
// Okuma YOK (nil, nil): terfi Problem'i yoksa ya da bölüm kolonları bu
// süreçte henüz görülmediyse (hasAnomalyEpisodeCols false — okunacak sayaç
// yok, kural zaten bugünkü atfı verir). Hata → (nil, err): çağıran bugünkü
// atfa düşer. Aynı id iki kez dönerse last_seen'i büyük olan kalır.
func (s *Store) PromotedAnomalySources(ctx context.Context, probs []Problem) (map[string]AnomalyEvent, error) {
	if !s.hasAnomalyEpisodeCols.Load() {
		return nil, nil
	}
	ids := promotedAnomalyIDs(probs)
	if len(ids) == 0 {
		return nil, nil
	}
	args := append(toAnySlice(ids), 2*len(ids))
	rows, err := s.conn.Query(ctx, promotedSourceSQL(len(ids)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]AnomalyEvent, len(ids))
	for rows.Next() {
		var e AnomalyEvent
		if err := rows.Scan(&e.ID, &e.StartedAt, &e.LastSeen, &e.EpisodeCount, &e.FirstStartedAt); err != nil {
			return nil, err
		}
		foldAnomalyCarryRow(out, e.ID, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// PromotedProblemSource — p'nin kaynak anomali olayı, yalnız p terfi
// Problem'iyse ve olay HÂLÂ p'nin bölümündeyse (ev.StartedAt == p.StartedAt).
// SAF. Diğer her Problem (kural, exception, `anomaly:` metrik dedektörü,
// küme) ok=false — haritada aynı kimlikte bir olay olsa bile.
func PromotedProblemSource(p Problem, srcs map[string]AnomalyEvent) (AnomalyEvent, bool) {
	if len(srcs) == 0 {
		return AnomalyEvent{}, false
	}
	id, ok := PromotedAnomalyEventID(p.RuleID)
	if !ok {
		return AnomalyEvent{}, false
	}
	ev, ok := srcs[id]
	if !ok || ev.StartedAt != p.StartedAt {
		return AnomalyEvent{}, false
	}
	return ev, true
}

// attachPromotedEpisodes — kaynak olayı eşleşen terfi Problem'ine olayın
// bölüm sayacını ve ilk başlangıcını iliştirir. Yalnız sayaç > 1: tek
// bölümlü olayda kural zaten bugünkü atfı verir (pencere içi deploy ≤
// StartedAt) ve tel bugünküyle bayt bayt aynı kalır. SAF (yerinde).
func attachPromotedEpisodes(probs []Problem, srcs map[string]AnomalyEvent) {
	for i := range probs {
		ev, ok := PromotedProblemSource(probs[i], srcs)
		if !ok || ev.EpisodeCount <= 1 {
			continue
		}
		probs[i].EpisodeCount = ev.EpisodeCount
		probs[i].FirstStartedAt = ev.FirstStartedAt
	}
}
