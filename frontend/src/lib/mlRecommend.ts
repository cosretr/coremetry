// lib/mlRecommend — v0.10.1886 (operatör 2026-11-06: "anomali ayarları çok
// karmaşık, sen default kur"). SAF: önerilen ML kurulumu yanıtının düz Türkçe
// görünümü. Kural sunucuda (chstore.RecommendMLConfig); burada yalnız metin.
import type { MLRecommended } from './types';

/** "A", "A ve B", "A, B ve C". */
export function joinTr(items: string[]): string {
  if (items.length <= 1) return items.join('');
  return `${items.slice(0, -1).join(', ')} ve ${items[items.length - 1]}`;
}

export interface MLRecommendView {
  summary: string;
  present: string[];
  absent: string[];
  /** Ölçülemeyen kaynaklar — "yok" değil; öneri onlara dokunmaz. */
  unknown: string[];
  turnOn: string[];
  turnOff: string[];
  mainSwitch: boolean;
  canApply: boolean;
}

export function mlRecommendView(r: MLRecommended, labelOf: (detector: string) => string): MLRecommendView {
  const present = r.checks.filter(c => c.checked && c.present).map(c => c.label);
  const absent = r.checks.filter(c => c.checked && !c.present).map(c => c.label);
  const unknown = r.checks.filter(c => !c.checked).map(c => c.label);
  const dets = r.changes.filter(c => c.key !== 'enabled');
  const turnOn = dets.filter(c => c.to).map(c => labelOf(c.key));
  const turnOff = dets.filter(c => !c.to).map(c => labelOf(c.key));
  const mainSwitch = r.changes.some(c => c.key === 'enabled' && c.to);
  let summary: string;
  if (!r.vmConfigured) {
    summary = 'VictoriaMetrics bağlı değil; kaynaklar ölçülemedi ve öneri üretilemedi.';
  } else if (r.changes.length === 0) {
    summary = 'Bugünkü ayar önerilen kurulumla aynı; değişiklik gerekmiyor.';
  } else {
    const parts: string[] = [];
    if (turnOn.length) parts.push(`${turnOn.length} dedektörü açar`);
    if (turnOff.length) parts.push(`${turnOff.length} dedektörü kapatır`);
    if (mainSwitch) parts.push('ML ana anahtarını açar');
    summary = `Önerilen kurulum ${joinTr(parts)}.`;
  }
  return { summary, present, absent, unknown, turnOn, turnOff, mainSwitch, canApply: r.vmConfigured && r.changes.length > 0 };
}
