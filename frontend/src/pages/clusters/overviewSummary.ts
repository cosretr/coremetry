// overviewSummary — Clusters detay Overview sekmesinin düz cümlesi, dört kutu
// metni ve "en yüksek node" altyazısı (Clusters S7; plan
// docs/frontend/clusters-shadcn-plan.md §0 D2). SAF: girdi zaten çekilen
// summary / resource-trend verisi; model yok, ek sorgu yok.
import type { CapacityForecastDays, ClusterNamedSeries, ClusterSummary } from '@/lib/types';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { fmtEtaDays } from '@/lib/fmtEta';
import { cpuUsageText, memUsageText } from './fleetSummary';
import { safePct } from './thresholds';
import { t, tf, fmtPctLang, type Lang } from '@/lib/i18n';

// "14 gün" → "14 günde", "9 saat" → "9 saatte", "30 dakika" → "30 dakikada".
// v0.10.2189 — EN: "14 days", "9 hours", "30 minutes" (fmtEtaDays'in sayısı aynen).
const EN_UNIT: Record<string, string> = { 'gün': 'day', 'saat': 'hour', 'dakika': 'minute' };
function etaLocative(days: number, lang: Lang): string {
  const s = fmtEtaDays(days);
  if (lang !== 'tr') {
    const [v, u] = s.split(' ');
    return EN_UNIT[u] ? `${v} ${EN_UNIT[u]}${v === '1' ? '' : 's'}` : s;
  }
  if (s.endsWith(' gün')) return `${s}de`;
  if (s.endsWith(' saat')) return `${s}te`;
  if (s.endsWith(' dakika')) return `${s}da`;
  return s;
}

/** Kapasite projeksiyonu cümle parçası: "yaklaşık 14 günde dolar"; tahmin yoksa ''. */
export function etaPhrase(f: CapacityForecastDays | undefined, lang: Lang = 'tr'): string {
  if (!f) return '';
  if (f.status === 'at_limit') return t('clusters.eta.atLimit', lang);
  if (f.status !== 'ok' || f.days == null || f.days <= 0) return '';
  const eta = etaLocative(f.wide ? (f.loDays ?? f.days) : f.days, lang);
  return tf(f.wide ? 'clusters.eta.fillsWide' : 'clusters.eta.fills', lang, { eta });
}

/** "Çalışıyor. Bellek %72 dolu ve bu hızla yaklaşık 14 günde dolar; 2 kritik alarm açık." */
export function overviewSentence(d: ClusterSummary | undefined, lang: Lang = 'tr'): string {
  if (!d) return '';
  const parts: string[] = [];
  const memP = safePct(d.memUsedBytes, d.memCapacityBytes);
  if (memP != null) {
    const eta = etaPhrase(d.memForecast, lang);
    parts.push(tf('clusters.ov.memFull', lang, { pct: fmtPctLang(Math.round(memP), lang) }) + (eta ? tf('clusters.ov.atRate', lang, { eta }) : ''));
  }
  // 0 kritik yazılmaz: ALERTS serisi kuralsız cluster ile alarmsızı ayırt edemez.
  if ((d.alertsCritical ?? 0) > 0) parts.push(tf('clusters.ov.critOpen', lang, { n: fmtNum(d.alertsCritical!) }));
  const head = t('clusters.ov.running', lang);
  if (parts.length === 0) return head;
  const rest = parts.join('; ');
  return `${head} ${rest.charAt(0).toLocaleUpperCase(lang === 'tr' ? 'tr-TR' : 'en-US')}${rest.slice(1)}.`;
}

export interface OverviewBox {
  label: string;
  value: string;
  sub: string;
  tone: 'err' | 'warn' | null;
  title?: string;
  /** CPU / Memory: kapasite biliniyorsa halka yüzdesi (v0.10.2190). */
  pct?: number | null;
  /** Pods: faz sayıları — kutu PhaseDonut çizer (v0.10.2190). */
  phase?: { running: number; pending: number; failed: number };
}

const join = (...xs: string[]) => xs.filter(Boolean).join(' · ');
const usageTone = (p: number | null): 'err' | 'warn' | null => (p == null ? null : p > 85 ? 'err' : p > 65 ? 'warn' : null);

/** D2 dört kutu: CPU / Memory (kullanılan / kapasite) · Pods · Alerts. Verisi olmayan kutu düşer. */
export function overviewBoxes(d: ClusterSummary | undefined, alertsKnown: boolean, lang: Lang = 'tr'): OverviewBox[] {
  if (!d) return [];
  const out: OverviewBox[] = [];
  const cpuP = safePct(d.cpuUsedCores, d.cpuCapacityCores);
  const cpuText = cpuUsageText(d.cpuUsedCores, d.cpuCapacityCores, lang);
  // v0.10.2190 — yüzde halkanın ortasında; değer satırı "kullanılan / kapasite · N node".
  if (cpuText) {
    out.push({
      label: 'CPU', tone: usageTone(cpuP), pct: cpuP,
      value: join(cpuText.split(' · ')[0], d.nodes ? tf('clusters.ov.nodes', lang, { n: fmtNum(d.nodes) }) : ''),
      sub: etaPhrase(d.cpuForecast, lang),
    });
  }
  const memP = safePct(d.memUsedBytes, d.memCapacityBytes);
  const memText = memUsageText(d.memUsedBytes, d.memCapacityBytes, lang);
  if (memText) {
    // Kutu değeri iki tarafı da birimle yazar ("922 GiB / 1.28 TiB").
    const value = d.memCapacityBytes ? `${fmtBytes(d.memUsedBytes!)} / ${fmtBytes(d.memCapacityBytes)}` : memText;
    out.push({
      label: 'Memory', value, tone: usageTone(memP), pct: memP,
      sub: etaPhrase(d.memForecast, lang),
    });
  }
  const phaseTotal = (d.podsRunning ?? 0) + (d.podsPending ?? 0) + (d.podsFailed ?? 0);
  if (phaseTotal > 0) {
    out.push({
      label: 'Pods', value: tf('clusters.pods.running', lang, { n: fmtNum(d.podsRunning ?? 0) }),
      tone: (d.podsFailed ?? 0) > 0 ? 'warn' : null,
      sub: `${tf('clusters.pods.pending', lang, { n: fmtNum(d.podsPending ?? 0) })} · ${tf('clusters.pods.failed', lang, { n: fmtNum(d.podsFailed ?? 0) })}`,
      phase: { running: d.podsRunning ?? 0, pending: d.podsPending ?? 0, failed: d.podsFailed ?? 0 },
    });
  }
  const crit = d.alertsCritical ?? 0, warn = d.alertsWarning ?? 0;
  if (alertsKnown || crit + warn > 0) {
    out.push({
      label: 'Alerts', value: tf('clusters.alerts.crit', lang, { n: fmtNum(crit) }), sub: tf('clusters.alerts.warn', lang, { n: fmtNum(warn) }),
      tone: crit > 0 ? 'err' : null,
      title: t('clusters.alerts.title', lang),
    });
  }
  return out;
}

/** By node grafiğinin altyazısı: son değeri en yüksek node; seri tekse ''. */
export function topSeriesText(series: ClusterNamedSeries[] | null | undefined, fmt: (v: number) => string, lang: Lang = 'tr'): string {
  if (!series || series.length < 2) return '';
  let best: { name: string; v: number } | null = null;
  for (const s of series) {
    const last = s.points.length > 0 ? s.points[s.points.length - 1].value : NaN;
    if (Number.isFinite(last) && (!best || last > best.v)) best = { name: s.name, v: last };
  }
  return best ? tf('clusters.ov.top', lang, { name: best.name, v: fmt(best.v) }) : '';
}
