// anomalyDetail — anomali olayı detayının SAF çekirdeği (v0.10.1032).
//
// Operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor. Exception
// gibi detay gözükmüyor." Anomali olayının artık İKİ yüzeyi var: /anomalies
// (ve servis Overview'u) üstündeki 560px çekmece (AnomalyDetailDrawer, v0.8.267)
// ve Problems kuyruğunun içinde açılan tam sayfa (AnomalyEventDetail). İkisi
// aynı olayı anlatıyor; tür etiketi, grafik penceresi, davranış kanıtının
// ayrıştırılması ve pivot linkleri bu modülde TEK yerde — iki kopya ayrışırsa
// aynı olay iki ekranda iki ayrı pencere / iki ayrı etiketle okunur.
//
// Bileşen parçaları (Fact, BehaviorDetailsBox, …) kardeş anomalyDetailParts.tsx'te.

import type { AnomalyEvent, BehaviorChangeDetails } from '@/lib/types';
import { SUBJECT_METRIC_TR, subjectMetricOf } from '@/lib/anomalySubjectMetric';
import type { CosreChartSpec } from '@/components/cosreChartSpec';
import { logsHref } from '@/lib/logsUrl';
import { tracesPivotHref, operationTracesHref } from '@/lib/pivotHref';
import { traceHref } from '@/lib/traceHref';
import { serviceHref } from '@/lib/serviceHref';
import { t, tf, type Lang } from '@/lib/i18n'; // v0.10.2290 — görünen metin isteğe bağlı lang (varsayılan 'tr')

export type AnomalyKind = AnomalyEvent['kind'];

export const ANOMALY_KIND_LABEL: Record<AnomalyKind, string> = {
  log_pattern: 'LOG PATTERN',
  trace_op: 'TRACE OP',
  trace_op_latency: 'TRACE OP · LATENCY',
  elastic_ml: 'ELASTIC ML',
  log_template_new: 'NEW LOG SHAPE',
  behavior_change: 'BEHAVIOR',
  calendar_traffic_drop: 'CALENDAR DROP', // v0.10.1483
  subject_red: 'SUBJECT', // v0.10.1792 — Davis F1.4 özne tabanı
};

// v0.10.1032 (operatör: "Anlaşılır olsun") — tam sayfanın tür rozeti düz
// Türkçe. Çekmece ve /anomalies tablosu yukarıdaki kısa İngilizce etiketleri
// korur (değişmeyen yüzeyler).
// v0.10.2290 — metin lib/i18n'de (aed.kind.*); bu kayıt TR görünümü.
const ANOMALY_KINDS: AnomalyKind[] = [
  'log_pattern', 'log_template_new', 'trace_op', 'trace_op_latency', 'behavior_change',
  'calendar_traffic_drop', // v0.10.1483
  'elastic_ml',
  'subject_red', // v0.10.1792 — endpoint / veritabanı / kuyruk
];
export const ANOMALY_KIND_PLAIN = Object.fromEntries(
  ANOMALY_KINDS.map(k => [k, t(`aed.kind.${k}`, 'tr')]),
) as Record<AnomalyKind, string>;

/** Tür rozeti düz dilde; bilinmeyen tür ham kimliğiyle. */
export function anomalyKindPlain(kind: string, lang: Lang = 'tr'): string {
  return (ANOMALY_KINDS as string[]).includes(kind) ? t(`aed.kind.${kind}`, lang) : String(kind);
}

// ── Yön · taban (v0.10.1792, Davis F1.4) ────────────────────────────────
// Detay sayfasında TEK sade satır (operatör: detay sayfaları sade). Eski satır
// (alanlar yok) → "bilinmiyor"; tabansız olayda oran yazılmaz ("×0.0" değil).

// v0.10.2290 — yön / taban metni lib/i18n'de (aed.dir.*, aed.base.*).

/** "artış · mevsimsel taban (…) 120.5" — iki parça da yoksa "bilinmiyor".
 *  v0.10.1804 — özne metriği biliniyorsa taban değeri birimiyle ("1200 kayıt"). */
export function anomalyDirectionBaselineLine(
  e: Pick<AnomalyEvent, 'direction' | 'baselineKind' | 'baselineValue'> & Partial<Pick<AnomalyEvent, 'kind' | 'pattern'>>,
  lang: Lang = 'tr',
): string {
  const dir = e.direction ? t(`aed.dir.${e.direction}`, lang) : '';
  let base = e.baselineKind ? t(`aed.base.${e.baselineKind}`, lang) : '';
  if (base && e.baselineKind !== 'none' && e.baselineValue != null && Number.isFinite(e.baselineValue)) {
    base += ` ${Math.round(e.baselineValue * 100) / 100}`;
    const sm = e.kind && e.pattern != null ? subjectMetricOf({ kind: e.kind, pattern: e.pattern }) : null;
    if (sm) base += ` ${lang === 'tr' ? SUBJECT_METRIC_TR[sm].unit : t(`aed.subjUnit.${sm}`, lang)}`;
  }
  if (!dir && !base) return t('aed.unknown', lang);
  return [dir || t('aed.dir.unknown', lang), base || t('aed.base.unknown', lang)].join(' · ');
}

// ── Terfi izi (v0.10.1799, Davis FAZ 3) ─────────────────────────────────
// Özne olayının terfi kapısı izi: 'shadow' = gölge kipinde kapıyı geçti
// (Problem açılmadı), 'live' = Problem açtı. Endpoint adayında rota sayısı
// okuma anında (promotionGroupSize); db / queue tek özne, sayı yazılmaz.

/** "Gölge: Problem açılırdı (3 rota)" | "Problem açıldı (3 rota)" | null. */
export function anomalyPromotionBadge(e: Pick<AnomalyEvent, 'promotion' | 'promotionGroupSize' | 'subjectKind'>, lang: Lang = 'tr'): string | null {
  if (e.promotion !== 'shadow' && e.promotion !== 'live') return null;
  const n = e.subjectKind === 'endpoint' && e.promotionGroupSize && e.promotionGroupSize > 0
    ? tf('aed.promo.routes', lang, { n: e.promotionGroupSize }) : '';
  return tf(e.promotion === 'shadow' ? 'aed.promo.shadow' : 'aed.promo.live', lang, { r: n });
}

/** Log biçimli türler: grafik servisin log hacmidir (tek sınırlı /api/logs/timeseries). */
export function isLogAnomalyKind(kind: string): boolean {
  return kind === 'log_pattern' || kind === 'log_template_new' || kind === 'elastic_ml';
}

/** Operasyon özneli türler: `pattern` bir span adıdır (recorder.go Pattern = a.Operation). */
export function isTraceAnomalyKind(kind: string): boolean {
  return kind === 'trace_op' || kind === 'trace_op_latency';
}

// v0.9.936 — davranış olaylarının `sample` alanı serbest metin DEĞİL,
// yapılandırılmış kanıt (BehaviorChangeDetails JSON). Ham JSON'u <pre>
// içinde göstermek teknik olarak "boş panel değil" ama operasyonel
// olarak okunamaz; kutu onu tek bakışta okunur hâle getiriyor.
//
// AYRIŞTIRMA HER ZAMAN KORUMALI: eski bir satır, elle düzenlenmiş bir
// kayıt ya da ileride değişen bir şekil geçerli JSON olmayabilir.
// Ayrıştırılamazsa null döner ve çağıran ham <pre>'ye düşer — çekmece /
// sayfa asla patlamaz, en kötü ihtimalle daha az güzel görünür.
export function parseBehaviorDetails(sample: string): BehaviorChangeDetails | null {
  try {
    const d = JSON.parse(sample) as BehaviorChangeDetails;
    if (!d || typeof d.metric !== 'string' || typeof d.ratio !== 'number') return null;
    return d;
  } catch {
    return null;
  }
}

/** Olayın davranış kanıtı (yalnız behavior_change; ayrıştırılamazsa null). */
export function behaviorDetailsOf(e: Pick<AnomalyEvent, 'kind' | 'sample'>): BehaviorChangeDetails | null {
  return e.kind === 'behavior_change' && e.sample ? parseBehaviorDetails(e.sample) : null;
}

// hourOfWeekLabel — 0..167 kovasını insan diline çevirir. Kova UTC'de
// hesaplanıyor (Go ve SQL tarafı da öyle), etiket de öyle diyor:
// operatörün "10:00 dedin ama bizde 13:00'tü" demesi bir hata raporu
// değil, bir birim karışıklığı olurdu.
// v0.10.2290 — gün kısaltmaları lib/i18n'de (aed.how.0..6, Pazartesi'den).
export function hourOfWeekLabel(how: number, lang: Lang = 'tr'): string {
  if (!Number.isFinite(how) || how < 0 || how > 167) return '—';
  const day = t(`aed.how.${Math.floor(how / 24)}`, lang);
  return `${day} ${String(how % 24).padStart(2, '0')}:00 UTC`;
}

const MINUTE_NS = 60 * 1e9;
const MIN_LEAD_NS = 30 * MINUTE_NS;
const MAX_LEAD_NS = 6 * 60 * MINUTE_NS;
const TAIL_NS = 10 * MINUTE_NS;

// anomalyChartWindow — spike'ın grafik / pivot penceresi (v0.8.267 çekmece
// formülü): sola spike süresinin 3 katı kadar giriş (en az 30 dk) ki taban
// spike'ın SOLUNDA görünsün, sağa 10 dk kuyruk. Çağıran useMemo'lar — her
// render'da yeni nesne histogram / grafik fetch'ini yeniden tetiklerdi
// (v0.5.184 sınıfı). Bozuk sıra (lastSeen < startedAt) süreyi 0 sayar,
// pencere ters dönmez.
//
// v0.10.1032 (inceleme) — iki sınır eklendi:
//   • giriş en çok 6 sa: günlerce süren bir olay 3× kuralıyla HAFTALARCA
//     geriye sorgu atardı (log histogramı ES'te, Seyir span rollup'ında);
//   • uçlar DAKİKAYA yuvarlanır (başlangıç aşağı, bitiş yukarı — pencere asla
//     daralmaz): açık bir olayda lastSeen her tazelemede kayıyor, ham hâli
//     sorgu anahtarını her seferinde değiştirip yeniden çekerdi (v0.8.270
//     sınırlı-kardinalite kuralı).
export function anomalyChartWindow(e: Pick<AnomalyEvent, 'startedAt' | 'lastSeen'>): { fromNs: number; toNs: number } {
  const durationNs = Math.max(0, e.lastSeen - e.startedAt);
  const lead = Math.min(Math.max(3 * durationNs, MIN_LEAD_NS), MAX_LEAD_NS);
  const from = e.startedAt - lead;
  const to = Math.max(e.lastSeen, e.startedAt) + TAIL_NS;
  return {
    fromNs: Math.floor(from / MINUTE_NS) * MINUTE_NS,
    toNs: Math.ceil(to / MINUTE_NS) * MINUTE_NS,
  };
}

// findAnomalyEventInCache — tam sayfa host'unun BEDAVA ilk basamağı
// (AlertProblemHost'un cache → tekil okuma sırasının ikizi). /anomalies ya
// da servis Overview'u açılmışsa olay listesi önbellekte durur ve o satır
// tekil uçtan ZENGİNDİR: cluster, son deploy ve kök-neden özeti okuma anında
// eklenmiş (getAnomalyEvents zenginleştirme zinciri; tekil uç eklemiyor).
// Bulunamazsa undefined → host tekil okumaya (GET /api/anomalies/event) düşer.
export function findAnomalyEventInCache(
  data: { items?: AnomalyEvent[] | null } | null | undefined,
  id: string,
): AnomalyEvent | undefined {
  if (!id) return undefined;
  return (data?.items ?? []).find(e => e.id === id);
}

/** Spike süresi (ns): lastSeen − startedAt, negatif değil. */
export function anomalyDurationNs(e: Pick<AnomalyEvent, 'startedAt' | 'lastSeen'>): number {
  return Math.max(0, e.lastSeen - e.startedAt);
}

// anomalySignalHrefs — olayın penceresiyle pivotlar. Üreticiler AlertProblemDetail
// ile aynı (logsHref / tracesPivotHref): el-yapımı /logs ya da /trace linki
// traceLogsLinkGate'te kırmızı. Servis yoksa null — servis adı gerektiren her
// pivot boş bir sayfa açardı (v0.9.1331 "boş liste yanlış cevaptır").
export interface AnomalySignalHrefs {
  logs: string;
  errorTraces: string;
  /** Servis sayfası, olayın penceresiyle (v0.9.860 K1 — "şimdi" ile açılırsa iz görünmez). */
  servicePage: string;
  /** Yalnız trace türlerinde: operasyonun trace'leri (desen = span adı). */
  operationTraces: string | null;
}

export function anomalySignalHrefs(
  e: Pick<AnomalyEvent, 'kind' | 'service' | 'pattern'> & Partial<Pick<AnomalyEvent, 'logQuery'>>,
  win: { fromNs: number; toNs: number },
): AnomalySignalHrefs | null {
  if (!e.service) return null;
  return {
    // v0.10.1071 — log_pattern olayında "Logları aç" servise EK olarak deseni
    // de taşır (`pattern=` — sunucu dedektörün yüklemini uygular; token'lar
    // arama metnine çevrilmez). Diğer türlerde desen bir log yüklemi değil.
    // v0.10.2026 (operatör: "Logs tüm servis loglarını getiriyor, o spesifik
    // logu değil") — log_template_new olayı şablonun arama metniyle (`q=`,
    // sunucunun logQuery'si; Şablonlar sekmesiyle aynı türetici) açılır.
    // Metin türetilemediyse bağlantı yalnız servisle kalır.
    logs: logsHref({
      window: win, service: e.service,
      pattern: e.kind === 'log_pattern' && e.pattern ? e.pattern : undefined,
      q: e.kind === 'log_template_new' && e.logQuery ? e.logQuery : undefined,
    }),
    errorTraces: tracesPivotHref({ window: win, service: e.service, hasError: true }),
    servicePage: serviceHref(e.service, { range: win }),
    // v0.10.1032 (inceleme) — trace_op bir HATA anomalisi: operasyonun
    // trace'leri hasError ile açılır (yoksa hatalı istekler sağlıklıların
    // arasında kaybolur). Gecikme türünde süzgeç yok — yavaş istek çoğu zaman
    // hatasızdır.
    operationTraces: isTraceAnomalyKind(e.kind) && e.pattern
      ? operationTracesHref({
        window: win, operation: e.pattern, service: e.service,
        hasError: e.kind === 'trace_op' ? true : undefined,
      })
      : null,
  };
}

// sampleTraceHref — trace türlerinde `sample` serbest metin değil, en çarpıcı
// cari span'in TRACE KİMLİĞİ (recorder.go Sample = a.SampleTraceID). Ham
// <pre> içinde 32 haneli hex okunur bir kanıt değil; tıklanır bir trace
// bağlantısı olmalı. Kimlik biçiminde değilse (eski satır, boş) null — çağıran
// ham örneğe düşer.
const TRACE_ID_RE = /^[0-9a-f]{16}([0-9a-f]{16})?$/i;
export function sampleTraceHref(e: Pick<AnomalyEvent, 'kind' | 'sample'>): string | null {
  const s = (e.sample ?? '').trim();
  if (!isTraceAnomalyKind(e.kind) || !TRACE_ID_RE.test(s)) return null;
  return traceHref(s);
}

// anomalyChart — "Seyir" grafiği (v0.10.1032). Operatör: "Anlaşılır olsun.
// Çok detay verince daha anlaşılır olmuyor" → türe göre TEK grafik, olayın
// KENDİ sinyali, düz Türkçe başlıkla. Mevcut, kendi kendine yeten CosreChart
// bileşeni (sohbetin "chart" çiti) servis / operasyon serisini MUTLAK bir
// pencereyle çiziyor: tek sınırlı span sorgusu (spanMetricBatch), staleTime
// 30 s, refetchInterval YOK — yeni uç ve yoklama döngüsü gerekmez.
//
//   trace_op          → operasyonun HATA SAYISI (agg "errors"). İnceleme
//                       bulgusu: dedektör hata SAYISINI tabanla kıyaslıyor
//                       (recorder.go CurrentErrors); hata ORANI (%) çizmek,
//                       hata yüzdesi sabitken trafiği artan bir operasyonda
//                       "5 katına çıktı" cümlesinin altına düz bir çizgi
//                       koyardı — yanlış metrikli grafik, grafiksizden kötü.
//   trace_op_latency  → operasyonun p99'u (dedektörün kendi metriği)
//   behavior_change   → kayan servis metriği (hata oranı / p99 / istek hızı);
//                       dış kaynak / bilinmeyen metrik ya da ayrıştırılamayan
//                       kanıt → grafik YOK (span serisi o metriği anlatmaz)
//   log türleri       → null (onların tek grafiği log hacmi histogramı)
export interface AnomalyChart {
  spec: CosreChartSpec;
  /** Düz Türkçe başlık — ham metrik kimliği değil. */
  title: string;
  /** Lejanttaki tek serinin adı. */
  seriesName: string;
}

// v0.10.2290 — etiketler lib/i18n'de (aed.metric.*; detailSummary.behaviorMetricLabel ile ortak).
const BEHAVIOR_CHART: Record<string, CosreChartSpec['agg']> = {
  error_rate: 'error_rate',
  p99_ms: 'p99',
  request_rate: 'rate',
};

export function anomalyChart(
  e: Pick<AnomalyEvent, 'kind' | 'service' | 'pattern'>,
  win: { fromNs: number; toNs: number },
  details: BehaviorChangeDetails | null,
  lang: Lang = 'tr',
): AnomalyChart | null {
  if (!e.service) return null;
  const base = { service: e.service, fromNs: win.fromNs, toNs: win.toNs };
  switch (e.kind) {
    case 'trace_op': {
      const l = t('aed.metric.errors', lang);
      return e.pattern
        ? { spec: { ...base, operation: e.pattern, agg: 'errors' }, title: `${e.pattern} · ${l}`, seriesName: l }
        : null;
    }
    case 'trace_op_latency': {
      const l = t('aed.metric.p99_ms', lang);
      return e.pattern
        ? { spec: { ...base, operation: e.pattern, agg: 'p99' }, title: `${e.pattern} · ${l}`, seriesName: l }
        : null;
    }
    case 'behavior_change': {
      const agg = details ? BEHAVIOR_CHART[details.metric] : undefined;
      if (!agg || !details) return null;
      const label = t(`aed.metric.${details.metric}`, lang);
      return { spec: { ...base, agg }, title: `${e.service} · ${label}`, seriesName: label };
    }
    default:
      return null;
  }
}

/** Seyir grafiği boşken sayfanın kendi notu (sohbetin çit-dili notu değil). */
export const ANOMALY_CHART_EMPTY = t('aed.chart.empty', 'tr');
export const anomalyChartEmpty = (lang: Lang = 'tr') => t('aed.chart.empty', lang);

/** SAF (v0.10.2027; operatör: "0s · sürüyor" anlamsız): süre metni. Sıfır
 *  süre = tek gözlem → "tek ölçüm"; açık olay "· sürüyor" eki alır. */
export function spikeDurationLabel(status: string, durationNs: number, formatted: string, lang: Lang = 'tr'): string {
  if (!(durationNs > 0)) return t('aed.dur.single', lang);
  return status === 'active' ? `${formatted} · ${t('pd.when.ongoing', lang)}` : formatted;
}

/** SAF (v0.10.2027): kök neden özetinin özet bölümünde gösterilip
 *  gösterilmeyeceği — güveni %50'nin altındaki aday yalnız teknik ayrıntıda. */
export const ROOT_CAUSE_SUMMARY_MIN_CONFIDENCE = 0.5;
export function showRootCauseInSummary(confidence: number | undefined | null): boolean {
  return typeof confidence === 'number' && confidence >= ROOT_CAUSE_SUMMARY_MIN_CONFIDENCE;
}
