// overviewSummary — Clusters detay Overview sekmesinin düz cümlesi, dört kutu
// metni ve "en yüksek node" altyazısı (Clusters S7; plan
// docs/frontend/clusters-shadcn-plan.md §0 D2). SAF: girdi zaten çekilen
// summary / resource-trend verisi; model yok, ek sorgu yok.
import type { CapacityForecastDays, ClusterNamedSeries, ClusterSummary } from '@/lib/types';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { fmtEtaDays } from '@/lib/fmtEta';
import { cpuUsageText, memUsageText } from './fleetSummary';
import { safePct } from './thresholds';

// "14 gün" → "14 günde", "9 saat" → "9 saatte", "30 dakika" → "30 dakikada".
function etaLocative(days: number): string {
  const s = fmtEtaDays(days);
  if (s.endsWith(' gün')) return `${s}de`;
  if (s.endsWith(' saat')) return `${s}te`;
  if (s.endsWith(' dakika')) return `${s}da`;
  return s;
}

/** Kapasite projeksiyonu cümle parçası: "yaklaşık 14 günde dolar"; tahmin yoksa ''. */
export function etaPhrase(f: CapacityForecastDays | undefined): string {
  if (!f) return '';
  if (f.status === 'at_limit') return 'eğilim tavanda';
  if (f.status !== 'ok' || f.days == null || f.days <= 0) return '';
  return `yaklaşık ${etaLocative(f.wide ? (f.loDays ?? f.days) : f.days)}${f.wide ? ' ya da daha geç' : ''} dolar`;
}

/** "Çalışıyor. Bellek %72 dolu ve bu hızla yaklaşık 14 günde dolar; 2 kritik alarm açık." */
export function overviewSentence(d: ClusterSummary | undefined): string {
  if (!d) return '';
  const parts: string[] = [];
  const memP = safePct(d.memUsedBytes, d.memCapacityBytes);
  if (memP != null) {
    const eta = etaPhrase(d.memForecast);
    parts.push(`bellek %${Math.round(memP)} dolu${eta ? ` ve bu hızla ${eta}` : ''}`);
  }
  // 0 kritik yazılmaz: ALERTS serisi kuralsız cluster ile alarmsızı ayırt edemez.
  if ((d.alertsCritical ?? 0) > 0) parts.push(`${fmtNum(d.alertsCritical!)} kritik alarm açık`);
  if (parts.length === 0) return 'Çalışıyor.';
  const rest = parts.join('; ');
  return `Çalışıyor. ${rest.charAt(0).toLocaleUpperCase('tr-TR')}${rest.slice(1)}.`;
}

export interface OverviewBox {
  label: string;
  value: string;
  sub: string;
  tone: 'err' | 'warn' | null;
  title?: string;
}

const join = (...xs: string[]) => xs.filter(Boolean).join(' · ');
const usageTone = (p: number | null): 'err' | 'warn' | null => (p == null ? null : p > 85 ? 'err' : p > 65 ? 'warn' : null);

/** D2 dört kutu: CPU / Memory (kullanılan / kapasite) · Pods · Alerts. Verisi olmayan kutu düşer. */
export function overviewBoxes(d: ClusterSummary | undefined, alertsKnown: boolean): OverviewBox[] {
  if (!d) return [];
  const out: OverviewBox[] = [];
  const cpuP = safePct(d.cpuUsedCores, d.cpuCapacityCores);
  const cpuText = cpuUsageText(d.cpuUsedCores, d.cpuCapacityCores);
  if (cpuText) {
    out.push({
      label: 'CPU', value: cpuText.split(' · ')[0], tone: usageTone(cpuP),
      sub: join(cpuP != null ? `%${Math.round(cpuP)}` : '', d.nodes ? `${fmtNum(d.nodes)} node` : '', etaPhrase(d.cpuForecast)),
    });
  }
  const memP = safePct(d.memUsedBytes, d.memCapacityBytes);
  const memText = memUsageText(d.memUsedBytes, d.memCapacityBytes);
  if (memText) {
    // Kutu değeri iki tarafı da birimle yazar ("922 GiB / 1.28 TiB").
    const value = d.memCapacityBytes ? `${fmtBytes(d.memUsedBytes!)} / ${fmtBytes(d.memCapacityBytes)}` : memText;
    out.push({
      label: 'Memory', value, tone: usageTone(memP),
      sub: join(memP != null ? `%${Math.round(memP)}` : '', etaPhrase(d.memForecast)),
    });
  }
  const phaseTotal = (d.podsRunning ?? 0) + (d.podsPending ?? 0) + (d.podsFailed ?? 0);
  if (phaseTotal > 0) {
    out.push({
      label: 'Pods', value: `${fmtNum(d.podsRunning ?? 0)} çalışıyor`,
      tone: (d.podsFailed ?? 0) > 0 ? 'warn' : null,
      sub: `${fmtNum(d.podsPending ?? 0)} bekliyor · ${fmtNum(d.podsFailed ?? 0)} başarısız`,
    });
  }
  const crit = d.alertsCritical ?? 0, warn = d.alertsWarning ?? 0;
  if (alertsKnown || crit + warn > 0) {
    out.push({
      label: 'Alerts', value: `${fmtNum(crit)} kritik`, sub: `${fmtNum(warn)} uyarı`,
      tone: crit > 0 ? 'err' : null,
      title: 'Prometheus ALERTS serisinden (firing / pending). Alarm kuralı tanımsız cluster da 0 gösterir — ALERTS ikisini ayırt edemez.',
    });
  }
  return out;
}

/** By node grafiğinin altyazısı: son değeri en yüksek node; seri tekse ''. */
export function topSeriesText(series: ClusterNamedSeries[] | null | undefined, fmt: (v: number) => string): string {
  if (!series || series.length < 2) return '';
  let best: { name: string; v: number } | null = null;
  for (const s of series) {
    const last = s.points.length > 0 ? s.points[s.points.length - 1].value : NaN;
    if (Number.isFinite(last) && (!best || last > best.v)) best = { name: s.name, v: last };
  }
  return best ? `En yüksek: ${best.name} · ${fmt(best.v)}` : '';
}
