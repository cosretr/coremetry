package chstore

// compare_periods.go — v0.10.944 (CoSRE araştırma asistanı, compare_periods).
//
// Bir servisin İKİ pencereyi (sorun ↔ referans) kıyaslayan okuması: giriş
// span'lerinin RED'i, operasyon karışımı, aşağı-akış bağımlılıkları, pod ve
// sürüm dağılımı. Tool zarfı (argüman, referans penceresi aritmetiği, notlar,
// kaynak durumu) mcptools/compare_periods.go'da; burada yalnız SQL kurucular
// (saf, tablo-testli), okuma ve sayısal birleştirme (saf) yaşar.
//
// ── MV mi ham mı (CLAUDE.md invariant #3, clickhouse-schema §7) ─────────────
// RED + top_operations MV-ÖNCE: spanmetrics_1m (service_name, name, KIND,
// status_code) boyutlu, quantilesTDigestState(0.5, 0.9, 0.95, 0.99) taşır,
// 30 g TTL. Giriş-span popülasyonu (kind server/consumer) bu MV'de ifade
// EDİLİR — "MV'de kind yok" tuzağı service_summary_5m / operation_summary_5m
// içindir. v0.10.944'ün ilk hâli bu başlıkta tersini söylüyordu ve her okuma
// ham spans'e gidiyordu (en fazla 8 ardışık sorgu, 20 s bütçe). Ham `spans`
// yalnız iki durumda: (a) env / cluster / namespace süzgeci — spanmetrics_1m
// bu boyutları taşımaz; (b) pencerelerden biri MV kapsamından
// (spanmetricsCoverageStart) önce başlıyor. Karar periodSpanmetricsGate'te
// (saf), gerekçesi PeriodCompareRaw.SourceReason'da.
// Bağımlılıklar her zaman ham: hedef (db / messaging / peer) çağırana göre
// hiçbir MV'de yok — LIMIT'li, düşük tavanlı. Pod'lar terfi kolonu k8s_pod'dan
// (res dizisi yok). Sürümler kapsam + deployMVCovers izin verirse
// service_version_5m'den (servisin TÜM span'leri, 5 dk ızgarası), yoksa ham
// effectiveVersionExpr (en düşük tavan). Ortam keşfi service_env_summary_5m
// (EnvSummaryCovers) ya da ham — iki pencere TEK okuma.
//
// ── Süre bütçesi ────────────────────────────────────────────────────────────
// Tool çağrısı ToolCallBudget'a (20 s) tabidir. Eskiden her okuma aynı
// max_execution_time = 20'yi taşıyordu: yavaş bir ikincil okuma kalanları aç
// bırakıyordu. Artık okuma başına tavan (RED 8 s, ikinciller ve ortam keşfi
// 4 s, ham sürüm 3 s), ctx'te kalan süre daha kısaysa o (periodCapSeconds).
// Yavaş bir ikincil "kısmi" olur, diğerleri yine koşar.
//
// ── Yüzdelik ─────────────────────────────────────────────────────────────
// p50/p95/p99 her pencerenin TAMAMI üzerinden: MV yolunda 1 dk kovalarının
// tdigest durumları quantilesTDigestMerge ile BİRLEŞİR, ham yolda tek
// quantileTDigest. Kova yüzdeliklerinin ortalaması (aggRED'in v0.10.944'e dek
// yaptığı) YASAK — bir kovanın p99'u ile diğerinin p99'unun ağırlıklı
// ortalaması hiçbir popülasyonun p99'u değildir.
//
// ── Dağıtık ─────────────────────────────────────────────────────────────────
// Alt sorgu, IN-alt-sorgusu ya da JOIN YOK: her sorgu tek tablo üzerinde
// agregat; `cluster IN (?)` bağlanmış bir DEĞER listesi, alt sorgu değil —
// GLOBAL gerekmez. Agregat durumları (tdigest, uniqExact) shard'lar arasında
// başlatıcıda birleşir; ORDER BY / LIMIT BY / pencere fonksiyonu birleşmiş
// sonuç üstünde koşar.
//
// ── Bağlantı ────────────────────────────────────────────────────────────────
// v0.10.944 — telemetryReadConn (RoundRobin telemetri okuma havuzu). Dosya
// yalnız telemetri okur (spans, spanmetrics_1m, service_version_5m,
// service_env_summary_5m, service_summary_5m) — state tablosu YOK; dosya
// conn_strategy_test.go beyaz listesinde ve state-tablosu taramasında.
// ServiceWindowRED'in yerini aldığı GetServiceSummary5m (analyze-service ve
// guided window_compare) de bu havuzdan okuyordu: AI okuma trafiği ana
// bağlantıya kaymaz.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PeriodScope — kıyasın kapsamı. Boş alan = süzgeç yok.
type PeriodScope struct {
	Service   string
	Env       string   // spans.deploy_env, tam eşleşme
	Clusters  []string // spans.cluster değerleri (Remote Cluster kaydından ya da birebir)
	Namespace string   // k8s_namespace terfi kolonu
	Operation string   // giriş span'inin adı (name); bağımlılık okumasına UYGULANAMAZ
}

// PeriodWindow — yarı açık [From, To).
type PeriodWindow struct{ From, To time.Time }

// Dönem indeksleri: SQL'deki `period` ifadesi sorun penceresine 0, referansa 1 verir.
const (
	PeriodProblem   = 0
	PeriodReference = 1
)

// Eşikler — notlar ve bayraklar bunlardan türer (tool açıklaması da ilan eder).
const (
	PeriodLowSampleMin    = 50   // bu sayının altındaki giriş span'i: yüzdelik güvenilmez
	PeriodCoverageNoteMin = 0.9  // 5 dk kovalarının bu oranından azı doluysa not
	PeriodMixShiftMin     = 0.10 // operasyon payında ≥10 puan kayma = karışım kaydı
	PeriodP95MovedMin     = 0.20 // p95'te ≥%20 göreli değişim = "p95 oynadı"
	PeriodTopN            = 10   // operasyon / bağımlılık / pod / sürüm satır tavanı
	periodBucket          = 5 * time.Minute
)

// Okuma kaynakları — PeriodCompareRaw.Source / VersionsSource değerleri.
const (
	PeriodSourceSpanmetrics = "spanmetrics_1m"
	PeriodSourceSpans       = "spans"
	PeriodSourceVersionsMV  = "service_version_5m"
)

// Ham yol gerekçeleri — PeriodCompareRaw.SourceReason (tool metne çevirir).
const (
	PeriodRawReasonScope    = "scope"    // env / cluster / namespace MV boyutu değil
	PeriodRawReasonCoverage = "coverage" // pencere spanmetrics_1m kapsamından önce başlıyor
)

// v0.10.944 — okuma başına max_execution_time (saniye). Toplamları 20 s'yi
// aşabilir; ctx son tarihi zaten keser — tavanın işi, tek yavaş okumanın
// bütçenin tamamını yemesini engellemek.
const (
	periodCapRED       = 8
	periodCapSecondary = 4
	periodCapVersions  = 3 // ham effectiveVersionExpr: satır başına 14 indexOf
	periodMVGrid       = time.Minute
)

// periodEntryKinds / periodClientKinds — popülasyon tanımları (SQL'e gömülü,
// bind edilmez; tool açıklaması aynı metni söyler).
const (
	periodEntryKinds  = "kind IN ('server', 'consumer')"
	periodClientKinds = "kind IN ('client', 'producer')"
)

// periodSettings — SAF: okumanın tavanı (saniye, en az 1).
func periodSettings(capS int) string {
	if capS < 1 {
		capS = 1
	}
	return "SETTINGS max_execution_time = " + strconv.Itoa(capS) + ", distributed_aggregation_memory_efficient = 1"
}

// periodCapSeconds — SAF: sabit tavan ile ctx'te kalan sürenin küçüğü (en az
// 1 s). Son tarih yoksa sabit tavan.
func periodCapSeconds(fixed int, deadline time.Time, hasDeadline bool, now time.Time) int {
	if !hasDeadline {
		return fixed
	}
	left := int(deadline.Sub(now) / time.Second)
	if left < 1 {
		left = 1
	}
	if left < fixed {
		return left
	}
	return fixed
}

// periodScopeWhere — SAF: zaman DIŞINDAKİ yüklem + argümanlar. entry=false
// (bağımlılık okuması) operation süzgecini UYGULAMAZ: istemci span'i giriş
// operasyonunun adını taşımaz; çağıran bunu nota çevirir. MV kurucuları da
// bunu kullanır: env/cluster/namespace kapıdan kaçıp MV'ye gelirse sorgu
// olmayan kolona takılıp YÜKSEK SESLE düşer — ortamlar sessizce birleşmez.
func periodScopeWhere(sc PeriodScope, entry bool) (string, []any) {
	parts := []string{"service_name = ?"}
	args := []any{sc.Service}
	if entry {
		parts = append(parts, periodEntryKinds)
	} else {
		parts = append(parts, periodClientKinds)
	}
	if sc.Env != "" {
		parts = append(parts, "deploy_env = ?")
		args = append(args, sc.Env)
	}
	if len(sc.Clusters) > 0 {
		parts = append(parts, "cluster IN (?)")
		args = append(args, sc.Clusters)
	}
	if sc.Namespace != "" {
		parts = append(parts, "k8s_namespace = ?")
		args = append(args, sc.Namespace)
	}
	if entry && sc.Operation != "" {
		parts = append(parts, "name = ?")
		args = append(args, sc.Operation)
	}
	return strings.Join(parts, " AND "), args
}

// periodTimeWhere — SAF: iki yarı açık pencerenin birleşimi, `col` üzerinde
// (ham: time, MV: time_bucket). Birincil anahtar / partition analizi OR'lu
// aralıkları budar.
func periodTimeWhere(col string, cur, ref PeriodWindow) (string, []any) {
	return "((" + col + " >= ? AND " + col + " < ?) OR (" + col + " >= ? AND " + col + " < ?))",
		[]any{cur.From.UTC(), cur.To.UTC(), ref.From.UTC(), ref.To.UTC()}
}

// periodWith — SAF: `period` ifadesi (sorun penceresi 0, değilse 1). Metinde
// EN BAŞTA durur, argümanları da ilk sırada bağlanır (positional bind).
func periodWith(col string, cur PeriodWindow) (string, []any) {
	return "WITH if(" + col + " >= ? AND " + col + " < ?, 0, 1) AS period", []any{cur.From.UTC(), cur.To.UTC()}
}

// periodGrid — SAF: pencerenin İKİ ucu da aynı ızgaraya aşağı yuvarlanır.
// Bitişik bir `previous` çifti ([a,b) ve [b,c)) böylece hiçbir kovayı
// paylaşmaz: alt uç aşağı, üst uç YUKARI yuvarlansaydı b'yi içeren kova iki
// dönemde birden sayılırdı. Uzunluk korunur (ızgara adımı kadar kayar).
func periodGrid(w PeriodWindow, step time.Duration) PeriodWindow {
	return PeriodWindow{From: w.From.UTC().Truncate(step), To: w.To.UTC().Truncate(step)}
}

func periodEarliest(a, b PeriodWindow) time.Time {
	if b.From.Before(a.From) {
		return b.From
	}
	return a.From
}

// periodScopeOnMV — SAF: kapsam MV boyutlarıyla ifade edilebilir mi.
// spanmetrics_1m / service_version_5m env, cluster ve namespace TAŞIMAZ.
func periodScopeOnMV(sc PeriodScope) bool {
	return sc.Env == "" && len(sc.Clusters) == 0 && sc.Namespace == ""
}

// periodSpanmetricsGate — SAF: RED + top_operations spanmetrics_1m'den mi.
// need = iki pencerenin (1 dk ızgarasındaki) en erken başı; coverage =
// spanmetricsCoverageStart (sıfır = bilinmiyor → ham). Ham ise gerekçe döner.
func periodSpanmetricsGate(sc PeriodScope, need, coverage time.Time) (bool, string) {
	if !periodScopeOnMV(sc) {
		return false, PeriodRawReasonScope
	}
	if coverage.IsZero() || need.Before(coverage) {
		return false, PeriodRawReasonCoverage
	}
	return true, ""
}

// periodVersionsGate — SAF: sürüm dağılımı service_version_5m'den mi. MV
// kind ve operasyon taşımaz: operation süzgeci varken ham yol (giriş
// span'lerinin o operasyonu) doğru popülasyondur.
func periodVersionsGate(sc PeriodScope, mvCovers bool) bool {
	return periodScopeOnMV(sc) && sc.Operation == "" && mvCovers
}

// ── Ham kurucular (spans) ────────────────────────────────────────────────────

// comparePeriodsREDSQL — SAF: dönem başına giriş-span RED'i, ham spans.
// Yüzdelikler pencerenin TAMAMI üzerinden tek quantilesTDigest (kova
// ortalaması değil); `buckets` = verisi olan 5 dk kova sayısı.
func comparePeriodsREDSQL(sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time", cur)
	sw, sargs := periodScopeWhere(sc, true)
	tw, targs := periodTimeWhere("time", cur, ref)
	args = append(append(args, sargs...), targs...)
	return with + `
		SELECT period,
		       count()                                               AS requests,
		       countIf(status_code = 'error')                        AS errors,
		       arrayElement(quantilesTDigest(0.5, 0.95, 0.99)(duration) AS q, 1) / 1e6 AS p50_ms,
		       arrayElement(q, 2) / 1e6                              AS p95_ms,
		       arrayElement(q, 3) / 1e6                              AS p99_ms,
		       uniqExact(toStartOfInterval(time, INTERVAL 5 MINUTE)) AS buckets
		FROM spans
		WHERE ` + sw + ` AND ` + tw + `
		GROUP BY period
		ORDER BY period
		LIMIT 2
		` + periodSettings(capS), args
}

// comparePeriodsOpsSQL — SAF: operasyon başına iki dönemin sayısı, hatası ve
// p95'i (-If birleştiricileriyle aynı tarama). Sıra: iki dönemden BÜYÜK olan
// sayıya göre — sorun penceresinde kaybolan bir operasyon da karışım kaymasına
// girer. LIMIT N+1: N'den fazlası varsa çağıran "kırpıldı" der.
func comparePeriodsOpsSQL(sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time", cur)
	sw, sargs := periodScopeWhere(sc, true)
	tw, targs := periodTimeWhere("time", cur, ref)
	args = append(append(args, sargs...), targs...)
	return with + `
		SELECT name                                                  AS op,
		       countIf(period = 0)                                   AS c0,
		       countIf(period = 1)                                   AS c1,
		       countIf(period = 0 AND status_code = 'error')         AS e0,
		       countIf(period = 1 AND status_code = 'error')         AS e1,
		       quantileTDigestIf(0.95)(duration, period = 0) / 1e6   AS p95_0,
		       quantileTDigestIf(0.95)(duration, period = 1) / 1e6   AS p95_1
		FROM spans
		WHERE ` + sw + ` AND ` + tw + `
		GROUP BY op
		ORDER BY greatest(c0, c1) DESC, op ASC
		LIMIT ` + strconv.Itoa(PeriodTopN+1) + `
		` + periodSettings(capS), args
}

// periodPeerHostSQL — dış düğüm hedefi; external_paths.go externalPeerHostSQL
// ile AYNI zincir (topology'nin infra_host'u): grafikte görülen hedefle burada
// görülen aynı adı taşısın.
const periodPeerHostSQL = externalPeerHostSQL

// comparePeriodsDepsSQL — SAF: servisin KENDİ istemci/üretici span'lerinden
// aşağı-akış hedefleri. Hedef türü db (db.system) > messaging (messaging.
// system) > service (peer.service → server.address → net.peer.name); p95
// çağıranın gördüğü süredir. Her zaman ham: hedef çağırana göre hiçbir MV'de
// yok (v0.10.944).
func comparePeriodsDepsSQL(sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time", cur)
	sw, sargs := periodScopeWhere(sc, false)
	tw, targs := periodTimeWhere("time", cur, ref)
	args = append(append(args, sargs...), targs...)
	return with + `,
		     ` + periodPeerHostSQL + ` AS peer_host,
		     multiIf(db_system != '', 'db', msg_system != '', 'messaging', 'service') AS dep_kind,
		     multiIf(db_system != '', concat(db_system, if(peer_host != '', concat('@', peer_host), '')),
		             msg_system != '', concat(msg_system, if(attr_values[indexOf(attr_keys, 'messaging.destination.name')] != '',
		                 concat(':', attr_values[indexOf(attr_keys, 'messaging.destination.name')]), '')),
		             peer_host != '', peer_host,
		             '(bilinmiyor)') AS target
		SELECT dep_kind,
		       target,
		       countIf(period = 0)                                   AS c0,
		       countIf(period = 1)                                   AS c1,
		       countIf(period = 0 AND status_code = 'error')         AS e0,
		       countIf(period = 1 AND status_code = 'error')         AS e1,
		       quantileTDigestIf(0.95)(duration, period = 0) / 1e6   AS p95_0,
		       quantileTDigestIf(0.95)(duration, period = 1) / 1e6   AS p95_1
		FROM spans
		WHERE ` + sw + ` AND ` + tw + `
		GROUP BY dep_kind, target
		ORDER BY greatest(c0, c1) DESC, target ASC
		LIMIT ` + strconv.Itoa(PeriodTopN+1) + `
		` + periodSettings(capS), args
}

// comparePeriodsPodsSQL — SAF (v0.10.944 — sürümden ayrıldı): dönem başına pod
// dağılımı, yalnız terfi kolonu k8s_pod (res dizisi taranmaz). İlk N sayıya
// göre + DISTINCT sayısı (pencere fonksiyonu HAVING'den sonra, LIMIT BY'dan
// önce: kırpılmış listenin gerçek boyutu kaybolmaz).
func comparePeriodsPodsSQL(sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time", cur)
	sw, sargs := periodScopeWhere(sc, true)
	tw, targs := periodTimeWhere("time", cur, ref)
	args = append(append(args, sargs...), targs...)
	return with + `
		SELECT period,
		       'pod'                                   AS dim,
		       CAST(k8s_pod, 'String')                 AS value,
		       count()                                 AS c,
		       count() OVER (PARTITION BY period)      AS distinct_n
		FROM spans
		WHERE ` + sw + ` AND ` + tw + `
		GROUP BY period, value
		HAVING value != ''
		ORDER BY period, c DESC, value ASC
		LIMIT ` + strconv.Itoa(PeriodTopN) + ` BY period
		` + periodSettings(capS), args
}

// comparePeriodsVersionsSQL — SAF: ham sürüm dağılımı (MV kapısı kapalıyken:
// env/cluster/namespace/operation süzgeci ya da MV kapsamı yok). Sürüm
// deploys.go'nun effectiveVersionExpr zinciri (image tag → service.version →
// k8s/helm etiketleri; yer tutucular elenir). Pahalı ifade → en düşük tavan.
func comparePeriodsVersionsSQL(sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time", cur)
	sw, sargs := periodScopeWhere(sc, true)
	tw, targs := periodTimeWhere("time", cur, ref)
	args = append(append(args, sargs...), targs...)
	return with + `
		SELECT period,
		       'version'                               AS dim,
		       ` + effectiveVersionExpr + `            AS value,
		       count()                                 AS c,
		       count() OVER (PARTITION BY period)      AS distinct_n
		FROM spans
		WHERE ` + sw + ` AND ` + tw + `
		GROUP BY period, value
		HAVING value != ''
		ORDER BY period, c DESC, value ASC
		LIMIT ` + strconv.Itoa(PeriodTopN) + ` BY period
		` + periodSettings(capS), args
}

// ── MV kurucuları (v0.10.944) ────────────────────────────────────────────────

// comparePeriodsREDMVSQL — SAF: RED'in spanmetrics_1m hızlı yolu. Pencereler
// ÇAĞIRANDA 1 dk ızgarasına yuvarlanmış olmalı (periodGrid). Sayılar
// countMerge, yüzdelikler kovaların tdigest durumlarının BİRLEŞİMİ — MV
// quantilesTDigestState(0.5, 0.9, 0.95, 0.99) taşır: indeks 1 = p50,
// 3 = p95, 4 = p99 (2 = p90, kullanılmaz; evaluator_reads.go aynı düzen).
func comparePeriodsREDMVSQL(src string, sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time_bucket", cur)
	sw, sargs := periodScopeWhere(sc, true)
	tw, targs := periodTimeWhere("time_bucket", cur, ref)
	args = append(append(args, sargs...), targs...)
	return with + `
		SELECT period,
		       countMerge(calls_state)                                                   AS requests,
		       countMerge(error_state)                                                   AS errors,
		       arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state) AS q, 1) / 1e6 AS p50_ms,
		       arrayElement(q, 3) / 1e6                                                  AS p95_ms,
		       arrayElement(q, 4) / 1e6                                                  AS p99_ms,
		       uniqExact(toStartOfInterval(time_bucket, INTERVAL 5 MINUTE))              AS buckets
		FROM ` + src + `
		WHERE ` + sw + ` AND ` + tw + `
		GROUP BY period
		ORDER BY period
		LIMIT 2
		` + periodSettings(capS), args
}

// comparePeriodsOpsMVSQL — SAF: top_operations'ın spanmetrics_1m hızlı yolu;
// satır şekli comparePeriodsOpsSQL ile aynı (tek tarayıcı). p95 dönem başına
// -MergeIf ile kendi durumlarının birleşimi.
func comparePeriodsOpsMVSQL(src string, sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time_bucket", cur)
	sw, sargs := periodScopeWhere(sc, true)
	tw, targs := periodTimeWhere("time_bucket", cur, ref)
	args = append(append(args, sargs...), targs...)
	return with + `
		SELECT name                                                                           AS op,
		       countMergeIf(calls_state, period = 0)                                          AS c0,
		       countMergeIf(calls_state, period = 1)                                          AS c1,
		       countMergeIf(error_state, period = 0)                                          AS e0,
		       countMergeIf(error_state, period = 1)                                          AS e1,
		       arrayElement(quantilesTDigestMergeIf(0.5, 0.9, 0.95, 0.99)(duration_q_state, period = 0), 3) / 1e6 AS p95_0,
		       arrayElement(quantilesTDigestMergeIf(0.5, 0.9, 0.95, 0.99)(duration_q_state, period = 1), 3) / 1e6 AS p95_1
		FROM ` + src + `
		WHERE ` + sw + ` AND ` + tw + `
		GROUP BY op
		ORDER BY greatest(c0, c1) DESC, op ASC
		LIMIT ` + strconv.Itoa(PeriodTopN+1) + `
		` + periodSettings(capS), args
}

// comparePeriodsVersionsMVSQL — SAF: sürüm dağılımı service_version_5m'den.
// Pencereler ÇAĞIRANDA 5 dk ızgarasına yuvarlanmış olmalı. MV kind ve
// operasyon taşımaz: sayılar servisin TÜM span'leridir (sürüm anahtarı
// taşıyanlar), giriş span'leri değil — tool bunu not eder. Popülasyon
// ayrımı yalnız sayılarda; hangi sürümlerin koştuğu sorusunun cevabı aynı.
func comparePeriodsVersionsMVSQL(sc PeriodScope, cur, ref PeriodWindow, capS int) (string, []any) {
	with, args := periodWith("time_bucket", cur)
	tw, targs := periodTimeWhere("time_bucket", cur, ref)
	args = append(append(args, sc.Service), targs...)
	return with + `
		SELECT period,
		       'version'                               AS dim,
		       version                                 AS value,
		       countMerge(span_count_state)            AS c,
		       count() OVER (PARTITION BY period)      AS distinct_n
		FROM service_version_5m
		WHERE service_name = ? AND ` + tw + `
		GROUP BY period, value
		HAVING value != ''
		ORDER BY period, c DESC, value ASC
		LIMIT ` + strconv.Itoa(PeriodTopN) + ` BY period
		` + periodSettings(capS), args
}

// periodEnvsSQL — SAF (v0.10.944): iki pencerenin ortam keşfi TEK okumada.
// mv=true → service_env_summary_5m (alt uç 5 dk kovasına hizalı, üst uç `<`:
// alignBucketStart sözleşmesi); mv=false → ham spans. Eskiden
// GetServiceEnvironments pencere başına bir kez, iki ham tarama.
func periodEnvsSQL(mv bool, service string, cur, ref PeriodWindow, capS int) (string, []any) {
	col, from := "time", "spans"
	if mv {
		col, from = "time_bucket", "service_env_summary_5m"
		cur = PeriodWindow{From: alignBucketStart(cur.From.UTC()), To: cur.To}
		ref = PeriodWindow{From: alignBucketStart(ref.From.UTC()), To: ref.To}
	}
	tw, targs := periodTimeWhere(col, cur, ref)
	return `
		SELECT deploy_env
		FROM ` + from + `
		WHERE service_name = ? AND deploy_env != '' AND ` + tw + `
		GROUP BY deploy_env
		ORDER BY deploy_env
		LIMIT 10
		` + periodSettings(capS), append([]any{service}, targs...)
}

// ── Okuma ────────────────────────────────────────────────────────────────────

// PeriodRED — bir dönemin ham RED satırı.
type PeriodRED struct {
	Requests uint64
	Errors   uint64
	P50Ms    float64
	P95Ms    float64
	P99Ms    float64
	Buckets  uint64 // verisi olan 5 dk kova
}

// PeriodPairRow — operasyon ya da bağımlılık: iki dönem yan yana.
type PeriodPairRow struct {
	Kind   string // bağımlılıkta db|messaging|service; operasyonda boş
	Key    string // operasyon adı ya da hedef
	Count  [2]uint64
	Errors [2]uint64
	P95Ms  [2]float64 // sayı 0 olan dönemde anlamsız (0)
}

// PeriodValueRow — pod/sürüm satırı.
type PeriodValueRow struct {
	Period   int
	Dim      string // pod | version
	Value    string
	Count    uint64
	Distinct uint64 // o dönem+boyuttaki toplam distinct değer
}

// PeriodCompareRaw — okumaların ham sonucu. RED birincil: o düşerse
// ReadComparePeriods hata döner. Diğerleri ikincil: hatası kendi alanında
// taşınır, çağıran kaynak durumuna "kısmi" notu olarak yazar.
type PeriodCompareRaw struct {
	RED    [2]PeriodRED
	Ops    []PeriodPairRow
	Deps   []PeriodPairRow
	Values []PeriodValueRow // pod + sürüm satırları (iki ayrı okuma)
	// v0.10.944 — okuma yolu (tool zarfı ilan eder): Source RED +
	// top_operations'ın tablosu (spanmetrics_1m | spans), SourceReason ham
	// yolun gerekçesi (scope | coverage), VersionsSource sürüm tablosu
	// (service_version_5m | spans). Windows okumanın GERÇEK pencereleri: MV
	// yolunda 1 dk ızgarası (hız ve kapsama bunlarla hesaplanır); sıfırsa
	// çağıranın pencereleri.
	Source         string
	SourceReason   string
	VersionsSource string
	Windows        [2]PeriodWindow
	OpsErr         error
	DepsErr        error
	PodsErr        error
	VersionsErr    error
}

// ReadComparePeriods — okumalar sırayla, okuma başına tavanla. İPTAL
// (context.Canceled) görülünce KALAN sorgular koşmaz ve hata hemen döner. Süre
// bütçesi (DeadlineExceeded) RED'den SONRA dolarsa RED atılmaz: kalan ikincil
// okumalar hızla düşer, hataları kendi alanlarına yazılır ve çağıran "kısmi"
// der.
func (s *Store) ReadComparePeriods(ctx context.Context, sc PeriodScope, cur, ref PeriodWindow) (PeriodCompareRaw, error) {
	var raw PeriodCompareRaw
	if sc.Service == "" {
		return raw, fmt.Errorf("compare periods: servis zorunlu")
	}
	capFor := func(fixed int) int {
		dl, ok := ctx.Deadline()
		return periodCapSeconds(fixed, dl, ok, time.Now())
	}
	// v0.10.944 — MV kapısı: kapsam MV'de ifade edilebiliyorsa kapsama probu
	// (önbellekli) sorulur; değilse prob hiç koşmaz.
	mcur, mref := periodGrid(cur, periodMVGrid), periodGrid(ref, periodMVGrid)
	var coverage time.Time
	if periodScopeOnMV(sc) {
		coverage = s.spanmetricsCoverageStart(ctx)
	}
	useMV, reason := periodSpanmetricsGate(sc, periodEarliest(mcur, mref), coverage)
	raw.Source, raw.SourceReason, raw.Windows = PeriodSourceSpans, reason, [2]PeriodWindow{cur, ref}
	if useMV {
		raw.Source, raw.Windows = PeriodSourceSpanmetrics, [2]PeriodWindow{mcur, mref}
	}
	// Tüm okumalar AYNI etkin pencereleri tarar (MV yolunda ızgaralı):
	// bağımlılık ve pod sayıları RED'le aynı zaman dilimini kapsar.
	rcur, rref := raw.Windows[0], raw.Windows[1]
	conn := s.telemetryReadConn()

	q, args := comparePeriodsREDSQL(sc, rcur, rref, capFor(periodCapRED))
	if useMV {
		q, args = comparePeriodsREDMVSQL(s.spanmetricsSourceFor("spanmetrics_1m"), sc, rcur, rref, capFor(periodCapRED))
	}
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return raw, fmt.Errorf("compare periods red (%s): %w", raw.Source, err)
	}
	for rows.Next() {
		var period uint8
		var r PeriodRED
		if err := rows.Scan(&period, &r.Requests, &r.Errors, &r.P50Ms, &r.P95Ms, &r.P99Ms, &r.Buckets); err != nil {
			rows.Close()
			return raw, fmt.Errorf("compare periods red scan: %w", err)
		}
		if int(period) <= PeriodReference {
			r.P50Ms, r.P95Ms, r.P99Ms = nanToZero(r.P50Ms), nanToZero(r.P95Ms), nanToZero(r.P99Ms)
			raw.RED[period] = r
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return raw, fmt.Errorf("compare periods red: %w", err)
	}
	cancelled := func() bool { return errors.Is(ctx.Err(), context.Canceled) }
	if cancelled() {
		return raw, ctx.Err()
	}

	q, args = comparePeriodsOpsSQL(sc, rcur, rref, capFor(periodCapSecondary))
	if useMV {
		q, args = comparePeriodsOpsMVSQL(s.spanmetricsSourceFor("spanmetrics_1m"), sc, rcur, rref, capFor(periodCapSecondary))
	}
	raw.Ops, raw.OpsErr = s.readPeriodPairs(ctx, false, q, args)
	if cancelled() {
		return raw, ctx.Err()
	}
	q, args = comparePeriodsDepsSQL(sc, rcur, rref, capFor(periodCapSecondary))
	raw.Deps, raw.DepsErr = s.readPeriodPairs(ctx, true, q, args)
	if cancelled() {
		return raw, ctx.Err()
	}
	q, args = comparePeriodsPodsSQL(sc, rcur, rref, capFor(periodCapSecondary))
	pods, perr := s.readPeriodValues(ctx, q, args)
	raw.Values, raw.PodsErr = append(raw.Values, pods...), perr
	if cancelled() {
		return raw, ctx.Err()
	}
	// Sürüm: MV kapısı (kapsam + operasyonsuz + deployMVCovers) 5 dk
	// ızgarasında; kapsama probu yalnız kapsam MV'de ifade edilebiliyorsa.
	vcur, vref := periodGrid(rcur, periodBucket), periodGrid(rref, periodBucket)
	versionsMV := periodScopeOnMV(sc) && sc.Operation == "" &&
		periodVersionsGate(sc, s.deployMVCovers(ctx, periodEarliest(vcur, vref)))
	raw.VersionsSource = PeriodSourceSpans
	if versionsMV {
		raw.VersionsSource = PeriodSourceVersionsMV
		q, args = comparePeriodsVersionsMVSQL(sc, vcur, vref, capFor(periodCapSecondary))
	} else {
		q, args = comparePeriodsVersionsSQL(sc, rcur, rref, capFor(periodCapVersions))
	}
	vers, verr := s.readPeriodValues(ctx, q, args)
	raw.Values, raw.VersionsErr = append(raw.Values, vers...), verr
	if cancelled() {
		return raw, ctx.Err()
	}
	return raw, nil
}

// ReadPeriodEnvironments — v0.10.944: servisin iki pencerede görüldüğü
// ortamlar, TEK okuma. service_env_summary_5m en erken pencere başını
// kapsıyorsa MV, değilse ham spans (OR'lu iki aralık). En çok 10 ortam.
func (s *Store) ReadPeriodEnvironments(ctx context.Context, service string, cur, ref PeriodWindow) ([]string, error) {
	dl, ok := ctx.Deadline()
	mv := s.EnvSummaryCovers(ctx, alignBucketStart(periodEarliest(cur, ref).UTC()))
	q, args := periodEnvsSQL(mv, service, cur, ref, periodCapSeconds(periodCapSecondary, dl, ok, time.Now()))
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("compare periods envs: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, fmt.Errorf("compare periods envs scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) readPeriodPairs(ctx context.Context, deps bool, q string, args []any) ([]PeriodPairRow, error) {
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PeriodPairRow{}
	for rows.Next() {
		var r PeriodPairRow
		dest := []any{&r.Key, &r.Count[0], &r.Count[1], &r.Errors[0], &r.Errors[1], &r.P95Ms[0], &r.P95Ms[1]}
		if deps {
			dest = append([]any{&r.Kind}, dest...)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		r.P95Ms[0], r.P95Ms[1] = nanToZero(r.P95Ms[0]), nanToZero(r.P95Ms[1])
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) readPeriodValues(ctx context.Context, q string, args []any) ([]PeriodValueRow, error) {
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PeriodValueRow{}
	for rows.Next() {
		var r PeriodValueRow
		var period uint8
		if err := rows.Scan(&period, &r.Dim, &r.Value, &r.Count, &r.Distinct); err != nil {
			return nil, err
		}
		r.Period = int(period)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Saf birleştirme ─────────────────────────────────────────────────────────

// PeriodOperation — bir dönemdeki operasyon satırı.
type PeriodOperation struct {
	Operation string  `json:"operation"`
	Count     uint64  `json:"count"`
	Share     float64 `json:"share"` // dönemin giriş span'lerindeki payı, 0..1
	Errors    uint64  `json:"errors"`
	ErrorRate float64 `json:"error_rate_pct"` // v0.10.944 — yüzde (0..100)
	P95Ms     float64 `json:"p95_ms"`
}

// PeriodDependency — bir dönemdeki aşağı-akış hedefi.
type PeriodDependency struct {
	Kind      string  `json:"kind"` // db | messaging | service
	Target    string  `json:"target"`
	Calls     uint64  `json:"calls"`
	Errors    uint64  `json:"errors"`
	ErrorRate float64 `json:"error_rate_pct"` // v0.10.944 — yüzde (0..100)
	P95Ms     float64 `json:"p95_ms"`
}

// PeriodValue — pod / sürüm değeri ve sayısı.
type PeriodValue struct {
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// PeriodStats — bir dönemin kıyas satırı (tool zarfına olduğu gibi girer).
type PeriodStats struct {
	Requests         uint64             `json:"requests"`
	RatePerS         float64            `json:"rate_per_s"`
	Errors           uint64             `json:"errors"`
	ErrorRate        float64            `json:"error_rate_pct"` // v0.10.944 — yüzde (0..100)
	P50Ms            float64            `json:"p50_ms"`
	P95Ms            float64            `json:"p95_ms"`
	P99Ms            float64            `json:"p99_ms"`
	Samples          uint64             `json:"samples"` // yüzdeliklerin dayandığı giriş span'i
	LowSample        bool               `json:"low_sample"`
	Coverage         float64            `json:"coverage"` // verisi olan 5 dk kova oranı, 0..1
	BucketsWithData  uint64             `json:"buckets_with_data"`
	BucketsTotal     int                `json:"buckets_total"`
	TopOperations    []PeriodOperation  `json:"top_operations"`
	Dependencies     []PeriodDependency `json:"dependencies"`
	PodsDistinct     uint64             `json:"pods_distinct"`
	Pods             []PeriodValue      `json:"pods"`
	VersionsDistinct uint64             `json:"versions_distinct"`
	Versions         []PeriodValue      `json:"versions"`
}

// PeriodDelta — tek ölçünün farkı. RelPct nil: referans 0 (göreli fark
// tanımsız). NoData: dönemlerden birinde örnek yok, fark anlamsız.
type PeriodDelta struct {
	Problem   float64  `json:"problem"`
	Reference float64  `json:"reference"`
	Abs       float64  `json:"abs"`
	RelPct    *float64 `json:"rel_pct"`
	NoData    bool     `json:"no_data,omitempty"`
}

// PeriodDeltas — sorun − referans.
type PeriodDeltas struct {
	Requests  PeriodDelta `json:"requests"`
	RatePerS  PeriodDelta `json:"rate_per_s"`
	Errors    PeriodDelta `json:"errors"`
	ErrorRate PeriodDelta `json:"error_rate_pct"` // v0.10.944 — abs yüzde puanı; rel_pct ölçekten bağımsız
	P50Ms     PeriodDelta `json:"p50_ms"`
	P95Ms     PeriodDelta `json:"p95_ms"`
	P99Ms     PeriodDelta `json:"p99_ms"`
}

// PeriodMixShift — bir operasyonun iki dönemdeki payı.
type PeriodMixShift struct {
	Operation      string  `json:"operation"`
	ShareProblem   float64 `json:"share_problem"`
	ShareReference float64 `json:"share_reference"`
	DeltaPP        float64 `json:"delta_pp"` // yüzde puan
}

// PeriodComparison — saf birleştirmenin çıktısı.
type PeriodComparison struct {
	Problem              PeriodStats      `json:"problem"`
	Reference            PeriodStats      `json:"reference"`
	Deltas               PeriodDeltas     `json:"deltas"`
	TrafficMixShift      []PeriodMixShift `json:"traffic_mix_shift"`
	OperationsTruncated  bool             `json:"operations_truncated"`
	DependenciesTrunc    bool             `json:"dependencies_truncated"`
	MaxMixShiftPP        float64          `json:"-"`
	MaxMixShiftOperation string           `json:"-"`
}

// PeriodBucketsTouched — SAF: [from, to) aralığının dokunduğu 5 dk kova
// sayısı (hizalanmamış uçlardaki kısmi kovalar dahil — SQL'deki
// toStartOfInterval da onları ayrı kova sayar).
func PeriodBucketsTouched(from, to time.Time) int {
	if !to.After(from) {
		return 0
	}
	first := from.UTC().Truncate(periodBucket)
	last := to.UTC().Add(-time.Nanosecond).Truncate(periodBucket)
	return int(last.Sub(first)/periodBucket) + 1
}

func periodRound(v float64, places int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func periodRatio(n, d uint64) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// BuildPeriodComparison — SAF: ham satırlar → iki dönem + farklar + karışım
// kayması. Hiçbir yüzdelik burada YENİDEN hesaplanmaz ya da ortalanmaz:
// değerler SQL'in tüm-pencere tdigest'inden olduğu gibi geçer. v0.10.944 —
// hız ve kapsama okumanın GERÇEK pencereleriyle (raw.Windows; MV yolunda 1 dk
// ızgarası) hesaplanır; boşsa çağıranın pencereleri.
func BuildPeriodComparison(raw PeriodCompareRaw, cur, ref PeriodWindow) PeriodComparison {
	var out PeriodComparison
	wins := [2]PeriodWindow{cur, ref}
	if !raw.Windows[0].To.IsZero() && !raw.Windows[1].To.IsZero() {
		wins = raw.Windows
	}
	stats := [2]*PeriodStats{&out.Problem, &out.Reference}
	for p := 0; p < 2; p++ {
		r := raw.RED[p]
		st := stats[p]
		secs := wins[p].To.Sub(wins[p].From).Seconds()
		st.Requests, st.Errors, st.Samples = r.Requests, r.Errors, r.Requests
		if secs > 0 {
			st.RatePerS = periodRound(float64(r.Requests)/secs, 3)
		}
		// v0.10.944 — hata oranı YÜZDE (0..100): diğer sağlık araçları
		// (error_rate_pct) ve guided demetler '%' basıyor; kesir birimi modele
		// ShortDescription'da söylenmiyordu.
		st.ErrorRate = periodRound(periodRatio(r.Errors, r.Requests)*100, 2)
		st.P50Ms, st.P95Ms, st.P99Ms = periodRound(r.P50Ms, 1), periodRound(r.P95Ms, 1), periodRound(r.P99Ms, 1)
		st.LowSample = r.Requests < PeriodLowSampleMin
		st.BucketsWithData = r.Buckets
		st.BucketsTotal = PeriodBucketsTouched(wins[p].From, wins[p].To)
		if st.BucketsTotal > 0 {
			st.Coverage = periodRound(math.Min(1, float64(r.Buckets)/float64(st.BucketsTotal)), 3)
		}
		st.TopOperations = []PeriodOperation{}
		st.Dependencies = []PeriodDependency{}
		st.Pods = []PeriodValue{}
		st.Versions = []PeriodValue{}
	}

	ops := raw.Ops
	if len(ops) > PeriodTopN {
		ops = ops[:PeriodTopN]
		out.OperationsTruncated = true
	}
	out.TrafficMixShift = []PeriodMixShift{}
	for _, o := range ops {
		var shares [2]float64
		for p := 0; p < 2; p++ {
			shares[p] = periodRatio(o.Count[p], raw.RED[p].Requests)
			if o.Count[p] == 0 {
				continue
			}
			stats[p].TopOperations = append(stats[p].TopOperations, PeriodOperation{
				Operation: o.Key, Count: o.Count[p], Share: periodRound(shares[p], 4),
				Errors: o.Errors[p], ErrorRate: periodRound(periodRatio(o.Errors[p], o.Count[p])*100, 2),
				P95Ms: periodRound(o.P95Ms[p], 1),
			})
		}
		// Karışım kayması ancak iki dönemde de istek varsa anlamlı.
		if raw.RED[PeriodProblem].Requests > 0 && raw.RED[PeriodReference].Requests > 0 {
			d := (shares[PeriodProblem] - shares[PeriodReference]) * 100
			out.TrafficMixShift = append(out.TrafficMixShift, PeriodMixShift{
				Operation: o.Key, ShareProblem: periodRound(shares[PeriodProblem], 4),
				ShareReference: periodRound(shares[PeriodReference], 4), DeltaPP: periodRound(d, 1),
			})
			if math.Abs(d) > math.Abs(out.MaxMixShiftPP) {
				out.MaxMixShiftPP, out.MaxMixShiftOperation = d, o.Key
			}
		}
	}
	sort.SliceStable(out.TrafficMixShift, func(i, j int) bool {
		return math.Abs(out.TrafficMixShift[i].DeltaPP) > math.Abs(out.TrafficMixShift[j].DeltaPP)
	})
	for p := 0; p < 2; p++ {
		sort.SliceStable(stats[p].TopOperations, func(i, j int) bool {
			return stats[p].TopOperations[i].Count > stats[p].TopOperations[j].Count
		})
	}

	deps := raw.Deps
	if len(deps) > PeriodTopN {
		deps = deps[:PeriodTopN]
		out.DependenciesTrunc = true
	}
	for _, d := range deps {
		for p := 0; p < 2; p++ {
			if d.Count[p] == 0 {
				continue
			}
			stats[p].Dependencies = append(stats[p].Dependencies, PeriodDependency{
				Kind: d.Kind, Target: d.Key, Calls: d.Count[p], Errors: d.Errors[p],
				ErrorRate: periodRound(periodRatio(d.Errors[p], d.Count[p])*100, 2), P95Ms: periodRound(d.P95Ms[p], 1),
			})
		}
	}
	for p := 0; p < 2; p++ {
		sort.SliceStable(stats[p].Dependencies, func(i, j int) bool {
			return stats[p].Dependencies[i].Calls > stats[p].Dependencies[j].Calls
		})
	}

	for _, v := range raw.Values {
		if v.Period < 0 || v.Period > PeriodReference {
			continue
		}
		st := stats[v.Period]
		switch v.Dim {
		case "pod":
			st.PodsDistinct = v.Distinct
			st.Pods = append(st.Pods, PeriodValue{Value: v.Value, Count: v.Count})
		case "version":
			st.VersionsDistinct = v.Distinct
			st.Versions = append(st.Versions, PeriodValue{Value: v.Value, Count: v.Count})
		}
	}

	pr, rf := out.Problem, out.Reference
	out.Deltas = PeriodDeltas{
		Requests:  periodDelta(float64(pr.Requests), float64(rf.Requests), 0, false),
		RatePerS:  periodDelta(pr.RatePerS, rf.RatePerS, 3, false),
		Errors:    periodDelta(float64(pr.Errors), float64(rf.Errors), 0, false),
		ErrorRate: periodDelta(pr.ErrorRate, rf.ErrorRate, 2, pr.Samples == 0 || rf.Samples == 0), // yüzde puanı
		P50Ms:     periodDelta(pr.P50Ms, rf.P50Ms, 1, pr.Samples == 0 || rf.Samples == 0),
		P95Ms:     periodDelta(pr.P95Ms, rf.P95Ms, 1, pr.Samples == 0 || rf.Samples == 0),
		P99Ms:     periodDelta(pr.P99Ms, rf.P99Ms, 1, pr.Samples == 0 || rf.Samples == 0),
	}
	return out
}

// periodDelta — SAF. noData: oran/yüzdelik bir dönemde örneksiz → fark
// hesaplanmaz (0 ms "hızlandı" demek değildir).
func periodDelta(problem, reference float64, places int, noData bool) PeriodDelta {
	d := PeriodDelta{Problem: periodRound(problem, places), Reference: periodRound(reference, places)}
	if noData {
		d.NoData = true
		return d
	}
	d.Abs = periodRound(problem-reference, places)
	if reference != 0 {
		rel := periodRound((problem-reference)/reference*100, 1)
		d.RelPct = &rel
	}
	return d
}

// ── Sürüm çözümü (Go ikizi) ─────────────────────────────────────────────────

// effectiveVersionKeys — effectiveVersionExpr'in (deploys.go) SIRASI, Go'da.
// İkisi ayrışırsa trace aracı ile kıyas aracı aynı pod için farklı sürüm
// söyler; compare_periods_test.go iki listeyi SQL metninden karşılaştırır.
var effectiveVersionKeys = []string{
	"container.image.tag",
	"k8s.container.image.tag",
	"service.version",
	"k8s.deployment.labels.app_kubernetes_io_version",
	"k8s.pod.labels.app_kubernetes_io_version",
	"k8s.deployment.labels.version",
	"helm.chart.version",
}

// placeholderVersions — placeholderVersionList'in (deploys.go) Go ikizi; test
// SQL listesini ayrıştırıp bununla eşitliğini doğrular.
var placeholderVersions = map[string]bool{
	"": true, "0.0.1": true, "0.0.1-SNAPSHOT": true, "0.1.0-SNAPSHOT": true,
	"1.0-SNAPSHOT": true, "1.0.0-SNAPSHOT": true,
	"${project.version}": true, "${version}": true,
	"latest": true, "dev": true, "unknown": true, "snapshot": true,
	"main": true, "master": true, "HEAD": true,
	"null": true, "none": true, "n/a": true, "NULL": true,
}

// EffectiveVersion — SAF: bir span'in resource öznitelik haritasından gerçek
// sürüm (effectiveVersionExpr ile aynı zincir; yer tutucular atlanır). "" =
// sürüm sinyali yok.
func EffectiveVersion(res map[string]string) string {
	for _, k := range effectiveVersionKeys {
		if v, ok := res[k]; ok && !placeholderVersions[v] {
			return v
		}
	}
	return ""
}

// ── Pencere RED'i (aggRED düzeltmesi) ───────────────────────────────────────

// ServiceWindowRED — service_summary_5m üzerinden TEK pencere: sayılar
// countMerge/sumMerge, yüzdelikler kovaların durumlarının quantilesTDigestMerge
// ile BİRLEŞİMİ. v0.10.944 öncesi AI yüzeyleri 5 dk kova yüzdeliklerinin span
// ağırlıklı ortalamasını alıyordu (aggRED) — bu hiçbir popülasyonun p99'u
// değildir. Popülasyon MV'nin kendisi: servisin TÜM span'leri (kind ayrımı
// yok); çağıran bunu metinde söyler.
type ServiceWindowRED struct {
	Spans   uint64
	Errors  uint64
	AvgMs   float64
	P50Ms   float64
	P95Ms   float64
	P99Ms   float64
	Buckets uint64 // veri taşıyan 5 dk kova sayısı
}

// serviceWindowREDSelect — iki pencere RED okumasının ORTAK SELECT'i:
// service_summary_5m ile service_env_summary_5m'in state kolonları birebir
// (store.go; service_env_summary_test pinler).
const serviceWindowREDSelect = `
		SELECT countMerge(span_count_state)                                         AS spans,
		       countIfMerge(error_count_state)                                      AS errs,
		       if(spans = 0, 0, sumMerge(duration_sum_state) / spans / 1e6)          AS avg_ms,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q, 1) / 1e6 AS p50_ms,
		       arrayElement(q, 2) / 1e6                                             AS p95_ms,
		       arrayElement(q, 3) / 1e6                                             AS p99_ms,
		       uniqExact(time_bucket)                                               AS buckets`

// serviceWindowREDSQL — SAF (pinli). Alt sınır kova başına hizalanır, üst
// sınır `<` (alignBucketStart sözleşmesi, summary_bucket_bound_test.go).
func serviceWindowREDSQL() string {
	return serviceWindowREDSelect + `
		FROM service_summary_5m
		WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?
		SETTINGS max_execution_time = 15, ` + mvQuantileMemSettings
}

// serviceEnvWindowREDSQL — SAF (v0.10.944): ServiceWindowRED'in ortam
// kapsamlı ikizi. service_env_summary_5m cluster boyutu da taşır; burada
// süzülmez, cluster'lar arası birleşir (tek ortamın tamamı). Aynı sınır
// sözleşmesi.
func serviceEnvWindowREDSQL() string {
	return serviceWindowREDSelect + `
		FROM service_env_summary_5m
		WHERE service_name = ? AND deploy_env = ? AND time_bucket >= ? AND time_bucket < ?
		SETTINGS max_execution_time = 15, ` + mvQuantileMemSettings
}

func scanServiceWindowRED(row interface{ Scan(dest ...any) error }) (ServiceWindowRED, error) {
	var out ServiceWindowRED
	if err := row.Scan(&out.Spans, &out.Errors, &out.AvgMs, &out.P50Ms, &out.P95Ms, &out.P99Ms, &out.Buckets); err != nil {
		return ServiceWindowRED{}, err
	}
	// Boş pencerede tdigest NaN döner; JSON'a NaN yazılamaz ve "0 ms" bir
	// ölçüm gibi okunmasın diye Spans=0 çağıranın işaretidir.
	out.AvgMs, out.P50Ms, out.P95Ms, out.P99Ms = nanToZero(out.AvgMs), nanToZero(out.P50Ms), nanToZero(out.P95Ms), nanToZero(out.P99Ms)
	return out, nil
}

// ServiceWindowRED — tek servis, tek pencere, tüm-pencere yüzdelikleri.
// v0.10.944 — telemetri okuma havuzu (yerini aldığı GetServiceSummary5m'in
// havuzu; ana bağlantı state tablolarına kalır).
func (s *Store) ServiceWindowRED(ctx context.Context, service string, from, to time.Time) (ServiceWindowRED, error) {
	out, err := scanServiceWindowRED(s.telemetryReadConn().QueryRow(ctx, serviceWindowREDSQL(), service, alignBucketStart(from), to))
	if err != nil {
		return ServiceWindowRED{}, fmt.Errorf("service window red: %w", err)
	}
	return out, nil
}

// ServiceEnvWindowRED — v0.10.944: tek servis + TEK ortam, tek pencere
// (service_env_summary_5m). MV geriye dolmaz: çağıran pencere başını
// EnvSummaryCovers ile ölçer; kapsamıyorsa ortamları birleşik okuma +
// dürüst not (guided window_compare). İki pencereden biri ortamlı, diğeri
// birleşik OKUNMAZ.
func (s *Store) ServiceEnvWindowRED(ctx context.Context, service, env string, from, to time.Time) (ServiceWindowRED, error) {
	out, err := scanServiceWindowRED(s.telemetryReadConn().QueryRow(ctx, serviceEnvWindowREDSQL(), service, env, alignBucketStart(from), to))
	if err != nil {
		return ServiceWindowRED{}, fmt.Errorf("service env window red: %w", err)
	}
	return out, nil
}
