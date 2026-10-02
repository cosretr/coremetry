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
import type { CosreChartSpec } from '@/components/cosreChartSpec';
import { logsHref } from '@/lib/logsUrl';
import { tracesPivotHref, operationTracesHref } from '@/lib/pivotHref';
import { traceHref } from '@/lib/traceHref';
import { serviceHref } from '@/lib/serviceHref';

export type AnomalyKind = AnomalyEvent['kind'];

export const ANOMALY_KIND_LABEL: Record<AnomalyKind, string> = {
  log_pattern: 'LOG PATTERN',
  trace_op: 'TRACE OP',
  trace_op_latency: 'TRACE OP · LATENCY',
  elastic_ml: 'ELASTIC ML',
  log_template_new: 'NEW LOG SHAPE',
  behavior_change: 'BEHAVIOR',
};

// v0.10.1032 (operatör: "Anlaşılır olsun") — tam sayfanın tür rozeti düz
// Türkçe. Çekmece ve /anomalies tablosu yukarıdaki kısa İngilizce etiketleri
// korur (değişmeyen yüzeyler).
export const ANOMALY_KIND_PLAIN: Record<AnomalyKind, string> = {
  log_pattern: 'Log deseni',
  log_template_new: 'Yeni log biçimi',
  trace_op: 'Operasyon hatası',
  trace_op_latency: 'Operasyon gecikmesi',
  behavior_change: 'Davranış değişimi',
  elastic_ml: 'Elastic ML',
};

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
const HOW_DAYS = ['Pzt', 'Sal', 'Çar', 'Per', 'Cum', 'Cmt', 'Paz'];
export function hourOfWeekLabel(how: number): string {
  if (!Number.isFinite(how) || how < 0 || how > 167) return '—';
  const day = HOW_DAYS[Math.floor(how / 24)] ?? '—';
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
  e: Pick<AnomalyEvent, 'kind' | 'service' | 'pattern'>,
  win: { fromNs: number; toNs: number },
): AnomalySignalHrefs | null {
  if (!e.service) return null;
  return {
    logs: logsHref({ window: win, service: e.service }),
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

const BEHAVIOR_CHART: Record<string, { agg: CosreChartSpec['agg']; label: string }> = {
  error_rate: { agg: 'error_rate', label: 'hata oranı' },
  p99_ms: { agg: 'p99', label: 'p99 gecikme' },
  request_rate: { agg: 'rate', label: 'istek hızı' },
};

export function anomalyChart(
  e: Pick<AnomalyEvent, 'kind' | 'service' | 'pattern'>,
  win: { fromNs: number; toNs: number },
  details: BehaviorChangeDetails | null,
): AnomalyChart | null {
  if (!e.service) return null;
  const base = { service: e.service, fromNs: win.fromNs, toNs: win.toNs };
  switch (e.kind) {
    case 'trace_op':
      return e.pattern
        ? { spec: { ...base, operation: e.pattern, agg: 'errors' }, title: `${e.pattern} · hata sayısı`, seriesName: 'hata sayısı' }
        : null;
    case 'trace_op_latency':
      return e.pattern
        ? { spec: { ...base, operation: e.pattern, agg: 'p99' }, title: `${e.pattern} · p99 gecikme`, seriesName: 'p99 gecikme' }
        : null;
    case 'behavior_change': {
      const c = details ? BEHAVIOR_CHART[details.metric] : undefined;
      return c ? { spec: { ...base, agg: c.agg }, title: `${e.service} · ${c.label}`, seriesName: c.label } : null;
    }
    default:
      return null;
  }
}

/** Seyir grafiği boşken sayfanın kendi notu (sohbetin çit-dili notu değil). */
export const ANOMALY_CHART_EMPTY = 'Bu pencere için seri yok — olay saklama süresinin dışında olabilir.';
