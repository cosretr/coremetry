// detailSummary — detay sayfalarının "ne oldu" cümlesi ve "ne zaman" satırı,
// SAF (v0.10.1032).
//
// Operatör: "Anlaşılır olsun. Çok detay verince daha anlaşılır olmuyor — alert
// ve anomaliler de." Alarm kuralı ve anomali detayının ilk satırı, nöbetçinin
// üç saniyede okuduğu tek Türkçe cümle. Cümle olayın GERÇEKTEN taşıdığı
// alanlardan kurulur; bir alan eksikse bir sonraki daha sade cümleye düşülür —
// ekranda asla "NaN", "0 katına" ya da boş parantez basılmaz (tablo testi:
// detailSummary.test.ts, her tür + her eksik-alan düşüşü + yukarı/aşağı yön).

import type { AnomalyEvent, BehaviorChangeDetails, PriorDeploy, Problem } from '@/lib/types';
import { fmtFixed, fmtNum, tsMinute } from '@/lib/utils';
import { subjectLabel, isAnomalyDetectorRule, PROMOTED_ANOMALY_RULE_PREFIX } from '@/lib/problemSubject';
import { fmtDurationNs } from './problemTime';
import { anomalyDurationNs } from './anomalyDetail';

const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v);

/** Oran: ≥10 tam sayı, ≥1 tek ondalık, <1 iki ondalık ("0.35" — düşüşte anlamlı
 *  basamak); sondaki sıfırlar düşer ("3.0" → "3", "0.50" → "0.5"). */
export function fmtRatio(r: number): string {
  if (r >= 10) return String(Math.round(r));
  const s = fmtFixed(r, r >= 1 ? 1 : 2);
  return s.includes('.') ? s.replace(/\.?0+$/, '') : s;
}

// Davranış motorunun metrik adları (internal/anomaly/behavior.go
// behaviorMetricValue) → düz Türkçe. Dış kaynak serisi "ext:" önekiyle gelir
// (anomaly.ExternalMetricPrefix); bilinmeyen ad olduğu gibi basılır.
const EXT_PREFIX = 'ext:';
export function behaviorMetricLabel(metric: string): string {
  switch (metric) {
    case 'error_rate': return 'hata oranı';
    case 'p99_ms': return 'p99 gecikme';
    case 'request_rate': return 'istek hızı';
  }
  if (metric.startsWith(EXT_PREFIX)) return `${metric.slice(EXT_PREFIX.length)} (dış kaynak)`;
  return metric;
}

// Kanıt kutusuyla AYNI biçim (anomalyDetailParts BehaviorDetailsBox): iki
// ondalığa yuvarla, birim yapışık.
function fmtBehaviorValue(v: number, unit: string): string {
  return `${fmtNum(Math.round(v * 100) / 100)}${unit}`;
}

const inService = (svc: string) => (svc ? `${svc} servisinde` : 'Bir serviste');
const inLogs = (svc: string) => (svc ? `${svc} loglarında` : 'Loglarda');

/** Oranın taşınmadığı / güvenilmediği her durumun cümlesi. */
export function genericAnomalySentence(service: string): string {
  return `${inService(service)} olağan dışı bir değişim saptandı.`;
}

// anomalySummary — anomali olayının cümlesi, türe göre:
//   behavior_change (kanıt ayrıştırıldıysa)
//       "<servis> servisinde <metrik> normalin <oran> katına çıktı/düştü (<taban> → <şimdi>)."
//   trace_op / trace_op_latency
//       "<servis> servisinde <operasyon> için hata/gecikme normalin <tepe> katına çıktı."
//   log_pattern / elastic_ml
//       "<servis> loglarında bu desen normalin <tepe> katına çıktı."
//   log_template_new (oranı yok — "ilk görülme")
//       "<servis> loglarında daha önce görülmemiş bir satır biçimi çıktı."
//   oran yok / güvenilmez
//       "<servis> servisinde olağan dışı bir değişim saptandı."
//
// `details` çağıranın ayrıştırdığı davranış kanıtı (anomalyDetail.behaviorDetailsOf);
// ayrıştırılamadıysa null → genel cümle. Artış türlerinde (log / trace) tepe
// oranı 1'in ALTINDAYSA "katına çıktı" yalan olurdu → genel cümle.
export function anomalySummary(
  e: Pick<AnomalyEvent, 'kind' | 'service' | 'pattern' | 'peakRatio'>,
  details: BehaviorChangeDetails | null,
): string {
  const svc = e.service ?? '';
  const peak = finite(e.peakRatio) && e.peakRatio > 1 ? e.peakRatio : null;
  switch (e.kind) {
    case 'behavior_change': {
      if (!details || !details.metric) return genericAnomalySentence(svc);
      const metric = behaviorMetricLabel(details.metric);
      const ratio = finite(details.ratio) && details.ratio > 0 ? details.ratio : null;
      // Yön kanıttan; eksik / tanımsızsa orandan (≥1 yukarı).
      const up = details.direction === 'up' ? true
        : details.direction === 'down' ? false
        : ratio !== null ? ratio >= 1 : null;
      if (up === null) return genericAnomalySentence(svc);
      const unit = typeof details.unit === 'string' ? details.unit : '';
      const values = finite(details.baseline) && finite(details.current)
        ? ` (${fmtBehaviorValue(details.baseline, unit)} → ${fmtBehaviorValue(details.current, unit)})`
        : '';
      // Oran = şimdi / taban (behavior.go: düşüşte < 1). Yönle çelişen oran
      // ("yukarı" ama 0.5) basılırsa cümle kendi kendini yalanlar → oransız biçim.
      // Yuvarlanınca "0" kalan oran ("normalin 0 katına") da oransız biçime düşer.
      const ratioFits = ratio !== null && (up ? ratio > 1 : ratio < 1) && fmtRatio(ratio) !== '0';
      const move = ratioFits
        ? `normalin ${fmtRatio(ratio)} katına ${up ? 'çıktı' : 'düştü'}`
        : `normalin ${up ? 'üstüne çıktı' : 'altına düştü'}`;
      return `${inService(svc)} ${metric} ${move}${values}.`;
    }
    case 'trace_op':
    case 'trace_op_latency': {
      if (peak === null) return genericAnomalySentence(svc);
      const what = e.kind === 'trace_op' ? 'hata' : 'gecikme';
      const op = e.pattern ? ` ${e.pattern} için` : '';
      return `${inService(svc)}${op} ${what} normalin ${fmtRatio(peak)} katına çıktı.`;
    }
    case 'log_pattern':
    case 'elastic_ml':
      if (peak === null) return genericAnomalySentence(svc);
      return `${inLogs(svc)} bu desen normalin ${fmtRatio(peak)} katına çıktı.`;
    case 'log_template_new':
      return `${inLogs(svc)} daha önce görülmemiş bir satır biçimi çıktı.`;
    default:
      return genericAnomalySentence(svc);
  }
}

// alertProblemSummary — alarm kuralı problemi:
//   "<kural adı> — <servis>: <metrik> değeri <değer>, eşik <eşik>."
// Sayı biçimi detay sayfasının "Metric" bölümüyle aynı (iki ondalık, fmtFixed).
// Düşüşler: eşik yoksa "değeri <değer>"; değer yoksa "<metrik> eşiği <eşik>
// aşıldı"; metrik / ikisi de yoksa "alarm tetiklendi". Kural adı boşsa metrik,
// o da boşsa "Alarm". Servis (özne) varsa okunur etiketiyle (db / dış kaynak
// öznesi ham kimlik değil, subjectLabel).
//
// v0.10.1032 (inceleme) — `anomaly:` önekli kurallarda (z-skor dedektörünün
// açtığı problemler) ikinci sayı bir EŞİK değil, olağan / medyan değerdir:
// "eşik 5.00" demek operatöre aşılmış bir sınır varmış gibi okutur. O
// kurallarda kelime "olağan değer".
// v0.10.1055 — yüklem isAnomalyDetectorRule (rozetin isAnomalyProblem'i DEĞİL):
// terfi Problem'inde threshold gerçek kapı (MinPeakRatio), kümede üye alt
// sınırı — "eşik" orada doğru kelime.
export function alertProblemSummary(
  p: Pick<Problem, 'ruleName' | 'metric' | 'value' | 'threshold' | 'service'> & { ruleId?: string },
): string {
  const metric = (p.metric ?? '').trim();
  const name = (p.ruleName ?? '').trim() || metric || 'Alarm';
  const svc = p.service ? ` — ${subjectLabel(p.service)}` : '';
  const head = `${name}${svc}`;
  const hasV = finite(p.value);
  const hasT = finite(p.threshold);
  const anomalyRule = isAnomalyDetectorRule(p.ruleId);
  if (!metric) return `${head}: alarm tetiklendi.`;
  if (anomalyRule) {
    if (hasV && hasT) return `${head}: ${metric} değeri ${fmtFixed(p.value, 2)}, olağan değer ${fmtFixed(p.threshold, 2)}.`;
    if (hasV) return `${head}: ${metric} değeri ${fmtFixed(p.value, 2)}.`;
    if (hasT) return `${head}: ${metric} olağan dışı (olağan değer ${fmtFixed(p.threshold, 2)}).`;
    return `${head}: ${metric} olağan dışı.`;
  }
  if (hasV && hasT) return `${head}: ${metric} değeri ${fmtFixed(p.value, 2)}, eşik ${fmtFixed(p.threshold, 2)}.`;
  if (hasV) return `${head}: ${metric} değeri ${fmtFixed(p.value, 2)}.`;
  if (hasT) return `${head}: ${metric} eşiği ${fmtFixed(p.threshold, 2)} aşıldı.`;
  return `${head}: alarm tetiklendi.`;
}

// Saat:dakika (tsMinute'ın saat yarısı) — bitiş başlangıçla aynı gündeyse
// tarih tekrarlanmaz.
const clockMinute = (ns: number) => tsMinute(ns).slice(11);
const sameLocalDay = (a: number, b: number) =>
  new Date(a / 1e6).toDateString() === new Date(b / 1e6).toDateString();

// detailWhenLine — "<başlangıç> başladı · <süre> · sürüyor" ya da, bittiyse,
// "<başlangıç> başladı · <süre> · <bitiş> bitti" (v0.10.1032 inceleme:
// çözülmüş problemde NE ZAMAN bittiği söylenmeli). Bitiş başlangıçla aynı
// gündeyse yalnız saat:dakika. Başlangıç yoksa (bozuk satır) o parça ve süre
// düşer, durum kalır; süre 0 ise ("az önce başladı, tek gözlem") basılmaz —
// "0s" bilgi değil. Bitiş yoksa / başlangıçtan önceyse yalnız "bitti".
// Biçimciler testte enjekte edilir (saat dilimi bağımsız); varsayılan tsMinute.
export function detailWhenLine(
  w: { startedAt: number; durationNs: number; ongoing: boolean; endedAt?: number },
  fmtTs: (ns: number) => string = tsMinute,
  fmtClock: (ns: number) => string = clockMinute,
): string {
  const parts: string[] = [];
  const hasStart = finite(w.startedAt) && w.startedAt > 0;
  if (hasStart) {
    parts.push(`${fmtTs(w.startedAt)} başladı`);
    if (finite(w.durationNs) && w.durationNs >= 1e9) parts.push(fmtDurationNs(w.durationNs));
  }
  if (w.ongoing) {
    parts.push('sürüyor');
  } else if (finite(w.endedAt) && w.endedAt > 0 && (!hasStart || w.endedAt >= w.startedAt)) {
    const end = hasStart && sameLocalDay(w.startedAt, w.endedAt) ? fmtClock(w.endedAt) : fmtTs(w.endedAt);
    parts.push(`${end} bitti`);
  } else {
    parts.push('bitti');
  }
  return parts.join(' · ');
}

// ── Yinelenen anomali (v0.10.1049) ───────────────────────────────────────
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor." Satır artık
// bölüm sayacını (episodeCount) ve İLK bölümün başlangıcını (firstStartedAt)
// taşıyor (chstore MergeAnomalyCarry). Ekranda YENİ BÖLÜM YOK: detay
// sayfasının "ne zaman" satırına tek kısa ek, satırlarda nötr tek kelime
// (RecurringMarker). Renk yok — renk yalnız sapan değerde (durum paleti).

/** Yinelenme bilgisi; null = yinelenmemiş (sayaç yok / ≤ 1). firstNs null =
 *  ilk görülme bilinmiyor (sütundan önce yazılmış satır). */
export interface AnomalyRecurrence { count: number; firstNs: number | null }

export function anomalyRecurrence(e: { episodeCount?: number; firstStartedAt?: number }): AnomalyRecurrence | null {
  const n = e.episodeCount;
  if (!finite(n) || n <= 1) return null;
  const first = finite(e.firstStartedAt) && e.firstStartedAt > 0 ? e.firstStartedAt : null;
  return { count: Math.floor(n), firstNs: first };
}

/** v0.10.1054 — "hiçbir şey kaybolmaz": kuralın "olası neden" saymadığı deploy
 *  (priorDeploy) nötr metin olarak: "deploy <sürüm> <N> dk önce". Sürüm ya da
 *  yaş taşımayan (bozuk) girdi → boş dize (hiçbir şey basılmaz). */
export function priorDeployText(d: PriorDeploy | null | undefined): string {
  if (!d || typeof d.version !== 'string' || d.version.trim() === '' || !finite(d.ageSeconds)) return '';
  return `deploy ${d.version} ${Math.max(1, Math.round(d.ageSeconds / 60))} dk önce`;
}

/** "yinelenen · bu N. kez · ilk kez <tarih>"; ilk görülme bilinmiyorsa son
 *  parça düşer; yinelenmemişse boş dize. v0.10.1054 — priorDeploy varsa tek ek:
 *  " · deploy <sürüm> <N> dk önce (öncesinde de görülüyordu)"; yoksa metin
 *  bayt bayt aynı. */
export function recurrenceClause(
  r: AnomalyRecurrence | null,
  fmtTs: (ns: number) => string = tsMinute,
  prior?: PriorDeploy | null,
): string {
  if (!r) return '';
  const parts = ['yinelenen', `bu ${r.count}. kez`];
  if (r.firstNs !== null) parts.push(`ilk kez ${fmtTs(r.firstNs)}`);
  const dep = priorDeployText(prior);
  if (dep) parts.push(`${dep} (öncesinde de görülüyordu)`);
  return parts.join(' · ');
}

/** Satır işaretinin ipucu: sayı + ilk tarih + sayacın sınırı (kayıt son
 *  tetiklenmeden 30 gün sonra düşer). v0.10.1054 — priorDeploy varsa yeni
 *  satırda "deploy <sürüm> <N> dk önce — öncesinde de görülüyordu". */
export function recurrenceTitle(
  r: AnomalyRecurrence | null,
  fmtTs: (ns: number) => string = tsMinute,
  prior?: PriorDeploy | null,
): string {
  if (!r) return '';
  const first = r.firstNs !== null ? `, ilk kez ${fmtTs(r.firstNs)}` : '';
  const base = `Yinelenen anomali: bu ${r.count}. kez${first}. Sayaç kaydın ömrüyle sınırlı (son tetiklenmeden 30 gün sonra kayıt düşer).`;
  const dep = priorDeployText(prior);
  return dep ? `${base}\n${dep} — öncesinde de görülüyordu` : base;
}

// ── Anomaliden terfi eden Problem (v0.10.1054) ───────────────────────────
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun; bugün
// deploy'a hâlâ eski kurala göre bağlanıyor." Sunucu terfi Problem'ine kaynak
// olayın bölüm sayacını ve ilk görülmesini iliştirir (yalnız olay hâlâ bu
// Problem'in bölümündeyken); satır ve detay sayfası anomali tarafıyla AYNI işaret
// ve AYNI ek. Yeni bölüm yok.

// Terfi öneki (PROMOTED_ANOMALY_RULE_PREFIX) v0.10.1055'te lib/problemSubject.ts'e
// taşındı: anomali rozeti yüklemi (isAnomalyProblem) de onu okuyor, tek yazım.

/** Terfi Problem'inin yinelenme bilgisi; diğer her Problem null — alan gelse
 *  bile (kural, exception, `anomaly:` dedektörü). */
export function promotedRecurrence(
  p: Pick<Problem, 'ruleId' | 'episodeCount' | 'firstStartedAt'>,
): AnomalyRecurrence | null {
  if (!(p.ruleId ?? '').startsWith(PROMOTED_ANOMALY_RULE_PREFIX)) return null;
  return anomalyRecurrence(p);
}

// problemWhenLine — alarm detay sayfasının "ne zaman" satırı: detailWhenLine
// (bitiş = resolvedAt; açıkken sürüyor) + terfi Problem'i yineleniyorsa
// anomaliyle aynı TEK ek (" · yinelenen · bu N. kez · ilk kez <tarih>"). Diğer
// her Problem'de çıktı detailWhenLine'ınkiyle BAYT BAYT aynı (tablo testli).
export function problemWhenLine(
  p: Pick<Problem, 'ruleId' | 'startedAt' | 'status' | 'resolvedAt' | 'episodeCount' | 'firstStartedAt' | 'priorDeploy'>,
  endNs: number,
  fmtTs: (ns: number) => string = tsMinute,
  fmtClock: (ns: number) => string = clockMinute,
): string {
  const base = detailWhenLine({
    startedAt: p.startedAt, durationNs: endNs - p.startedAt,
    ongoing: p.status !== 'resolved', endedAt: p.resolvedAt,
  }, fmtTs, fmtClock);
  const clause = recurrenceClause(promotedRecurrence(p), fmtTs, p.priorDeploy);
  return clause ? `${base} · ${clause}` : base;
}

// anomalyWhenLine — anomali detay sayfasının "ne zaman" satırı:
// detailWhenLine (bitmiş olayda bitiş = son gözlem; durum last_seen
// tazeliğinden, chstore GetAnomalyEvent) + yinelenen olayda TEK ek
// (" · yinelenen · bu N. kez · ilk kez <tarih>"). Sayaç ≤ 1 / yoksa çıktı
// detailWhenLine'ınkiyle BAYT BAYT aynı (tablo testli). v0.10.1054 — kuralın
// bastırdığı deploy (priorDeploy) ekin sonunda nötr: " · deploy <sürüm> <N> dk
// önce (öncesinde de görülüyordu)".
export function anomalyWhenLine(
  e: Pick<AnomalyEvent, 'startedAt' | 'lastSeen' | 'status' | 'episodeCount' | 'firstStartedAt' | 'priorDeploy'>,
  fmtTs: (ns: number) => string = tsMinute,
  fmtClock: (ns: number) => string = clockMinute,
): string {
  const ongoing = e.status === 'active';
  const base = detailWhenLine({
    startedAt: e.startedAt, durationNs: anomalyDurationNs(e), ongoing,
    endedAt: ongoing ? undefined : e.lastSeen,
  }, fmtTs, fmtClock);
  const clause = recurrenceClause(anomalyRecurrence(e), fmtTs, e.priorDeploy);
  return clause ? `${base} · ${clause}` : base;
}
