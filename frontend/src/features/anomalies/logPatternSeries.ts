// logPatternSeries — log deseni anomalisinin "desen sayısı" grafiğinin SAF
// çekirdeği (v0.10.1060).
//
// Operatör (prod, "Log deseni" detayı): "Bunu doğru yakalamış ama artışın ne
// zaman başladığını göstermiyor. Elastic'e gidip bakınca barlardan net
// görüyorum." Sayfa yalnız sayılar gösteriyordu; artışın başlangıcı ancak
// Discover'ın kova histogramında görünüyordu. Bu modül hangi olayın grafiği
// olduğunu, hangi pencereyi soracağını ve cevabın nasıl çizileceğini söyler;
// bileşen (LogPatternCountSection) yalnız çizer.

import type { AnomalyEvent, LogPatternSeries, SpanMetricSeries } from '@/lib/types';
import type { ChartTimeRegion } from '@/lib/chart/overlays';
import { logsHref } from '@/lib/logsUrl';
import { anomalyChartWindow } from './anomalyDetail';

// Grafiği olan türler: YALNIZ log_pattern. Desen adı dedektörün küratörlü
// listesinden (internal/anomaly/log_patterns.go) — sunucu adı dedektörün kendi
// eşleşme tanımına çevirip sayar. Bilerek DIŞARIDA:
//   • log_template_new — Drain şablonu örneklemeyle bulunur (1000/5 dk) ve
//     log_templates tablosunda zaman kovası yok; şablon metnini log arka
//     ucunda saymanın dedektörle aynı anlamı olmazdı.
//   • elastic_ml — desen bir ML işinin adı, sayılabilir bir log eşleşmesi değil.
// Bu ikisi bugünkü "Log hacmi" grafiğini korur.
export function hasLogPatternSeries(e: Pick<AnomalyEvent, 'kind' | 'pattern'>): boolean {
  return e.kind === 'log_pattern' && !!e.pattern;
}

const MINUTE_NS = 60e9;
const HOUR_NS = 60 * MINUTE_NS;

// logPatternSeriesWindow — sorulacak pencere. Başlangıç: olay başlangıcının
// 1 sa öncesi ya da olayın kendi grafik penceresinin başı (hangisi önceyse) —
// artıştan önceki taban barların SOLUNDA görünsün. Bitiş: bitmiş olayda olay
// penceresinin sonu (son gözlem + 10 dk), aktif olayda null = sunucu "şimdi"
// alır (yoklamada sorgu anahtarı sabit kalır). Dakikaya yuvarlı; sunucu
// pencereyi ayrıca kovaya hizalar.
export function logPatternSeriesWindow(
  e: Pick<AnomalyEvent, 'startedAt' | 'lastSeen' | 'status'>,
): { fromNs: number; toNs: number | null } {
  const ev = anomalyChartWindow(e);
  const lead = Math.floor((e.startedAt - HOUR_NS) / MINUTE_NS) * MINUTE_NS;
  return {
    fromNs: Math.min(lead, ev.fromNs),
    toNs: e.status === 'active' ? null : ev.toNs,
  };
}

/** Desen sayısı okumasının argümanları — grafik bölümü ve "Ne yapabilirim"
 *  kartı AYNI anahtarla sorar (tek istek, React Query paylaşır). */
export function logPatternSeriesArgs(
  e: Pick<AnomalyEvent, 'pattern' | 'startedAt' | 'lastSeen' | 'status'>,
): { pattern: string; fromNs: number; toNs: number | null } {
  return { pattern: e.pattern, ...logPatternSeriesWindow(e) };
}

// patternLogsPivot — v0.10.1062 (operatör, prod ES: servissiz log deseni
// anomalisinde "Ne yapabilirim" yalnız "servis adı yok" diyordu; operatör
// Kibana'ya elle gidiyordu). Servissiz log_pattern olayının TEK eylemi:
// olay penceresinde, desene uyan satırlarla /logs.
//
// v0.10.1071 (operatör, prod ES: "Logları aç" 80 bin ilgisiz satır açtı,
// grafik ~24 bin sayıyordu) — bağlantı artık deseni ARAMA METNİNE ÇEVİRMEZ
// (1062'nin sunucudan gelen `logsQuery`'si silindi): `pattern=<desen adı>`
// yazar, /logs sunucusu dedektörün kendi yüklemini uygular — grafikle aynı
// sayım. Ad olayın kendisinden; okuma beklenmez. Boş ad → null (sahte
// bağlantı yok). topServices: en çok ≤3 ad ("En çok: …"; sunucu ≤5 döner).
export interface PatternLogsPivot { href: string; topServices: string[] }

export function patternLogsPivot(
  pattern: string | null | undefined,
  series: Pick<LogPatternSeries, 'topServices'> | null | undefined,
  win: { fromNs: number; toNs: number },
): PatternLogsPivot | null {
  const name = (pattern ?? '').trim();
  if (!name) return null;
  const href = logsHref({ window: win, pattern: name });
  const topServices = (series?.topServices ?? [])
    .map(s => (s?.service ?? '').trim())
    .filter(s => s !== '')
    .slice(0, 3);
  return { href, topServices };
}

/** Kova genişliği düz Türkçe ("1 dk", "2 sa", "1 gün"). */
export function bucketLabel(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return '—';
  if (sec % 86400 === 0) return `${sec / 86400} gün`;
  if (sec % 3600 === 0) return `${sec / 3600} sa`;
  if (sec % 60 === 0) return `${sec / 60} dk`;
  return `${sec} sn`;
}

/** Cevap → CorePanelMulti'nin tek serisi (zaman ns, değer = kovadaki eşleşme). */
export function logPatternSeriesToSpan(s: LogPatternSeries): SpanMetricSeries[] {
  return [{ groupKey: [], points: (s.points ?? []).map(p => ({ time: p.t, value: p.v })) }];
}

// anomalyRegion — olayın grafikteki işareti: başlangıçtan bitişe bölge, sol
// kenarı başlangıç ANINDA (etiket "▮ başladı" tam o x'ten başlar). Genişliksiz
// bölge (fromSec === toSec) standart çizimde 1 px'lik soluk şerittir ve etiket
// sığmadığı için hiç basılmaz — görünmeyen işaret işaret değil. Bitiş: bitmiş
// olayda son gözlem, aktif olayda grafiğin sağ ucu (chartToNs). endSec gerçek
// bitişi taşır (aktif olayda yok — "sürüyor").
export function anomalyRegion(
  e: Pick<AnomalyEvent, 'startedAt' | 'lastSeen' | 'status'>,
  chartToNs: number,
): ChartTimeRegion {
  const fromSec = e.startedAt / 1e9;
  const active = e.status === 'active';
  const endNs = active ? Math.max(chartToNs, e.startedAt) : Math.max(e.lastSeen, e.startedAt);
  return {
    fromSec,
    toSec: endNs / 1e9,
    color: 'var(--err)',
    label: 'başladı',
    ...(active ? {} : { endSec: endNs / 1e9 }),
  };
}

export type LogPatternSeriesState = 'loading' | 'error' | 'gone' | 'empty' | 'ready';

// logPatternSeriesState — sorgu → çizim durumu. Sıra önemli: hata boşluğu
// kapsar; null = sunucu 404 (desen tanımı artık yok, eski satır) — "sıfır
// eşleşme" diye okunmamalı; hepsi 0 = gerçekten sayıldı, eşleşme yok.
export function logPatternSeriesState(q: {
  isPending: boolean; isError: boolean; data: LogPatternSeries | null | undefined;
}): LogPatternSeriesState {
  if (q.isError) return 'error';
  if (q.data === null) return 'gone';
  if (q.isPending || q.data === undefined) return 'loading';
  return (q.data.points ?? []).some(p => p.v > 0) ? 'ready' : 'empty';
}

export const LOG_PATTERN_SERIES_TEXT = {
  error: 'Desen sayısı okunamadı.',
  empty: 'Bu pencerede desene uyan log yok.',
  gone: 'Bu desenin tanımı artık yok — sayım çizilemiyor.',
  partial: 'Log arka ucu zaman aşımına uğradı — sayımlar eksik olabilir.',
} as const;
