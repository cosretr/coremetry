// mlEvidenceRows — v0.10.2044 (operatör, prod): kanıt bölümü başlıktaki
// cümlenin AYNISINI tekrar ediyordu; "2 sinyal aynı olayda" diyordu ama iki
// satır da aynı dedektörün art arda iki olayıydı. SAF.
//   - Aynı dedektörün olayları TEK satır: en güçlü üyenin sunucu cümlesi +
//     "(+N olay daha: 16:23, 17:20)".
//   - Satır cümlesi başlık cümlesiyle aynıysa yerine kartın "Ne kadar"ı
//     (cümlede olmayan bilgi); o da yoksa satır yalnız kaynakla kalır.
//   - Alt başlık farklı sinyal TÜRÜ (trace / log / metrik) sayısı; tek türde
//     birden çok dedektör varsa dedektör sayısı.
// Cümle metni sunucudan (ml.ExplainSummary); burada cümle kurulmaz.
import type { MLEvent } from '@/lib/types';
import { signalKindOf, type SignalKind } from '@/lib/signalKind';
import { stripServicePrefix } from './unifiedRows';

export interface EvidenceRow { key: string; kind: SignalKind; detector: string; text: string; ids: string[] }

const clock = (ns: number) => {
  const d = new Date(ns / 1e6);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
};

/** SAF. headline: detayın başlık cümlesi (servis öneki kırpılmış). */
export function evidenceRows(members: MLEvent[], headline: string, howMuch: string, service: string): EvidenceRow[] {
  const byDet = new Map<string, MLEvent[]>();
  for (const m of members) {
    const l = byDet.get(m.detector);
    if (l) l.push(m); else byDet.set(m.detector, [m]);
  }
  const out: EvidenceRow[] = [];
  for (const [det, ms] of byDet) {
    const lead = [...ms].sort((a, b) => b.score - a.score || b.lastSeen - a.lastSeen)[0];
    let text = stripServicePrefix(lead.summary ?? '', service) || '';
    if (!text || text === headline) text = howMuch && howMuch !== headline ? howMuch : '';
    const others = ms.filter(m => m.id !== lead.id).sort((a, b) => a.startedAt - b.startedAt);
    if (others.length) text = `${text}${text ? ' ' : ''}(+${others.length} olay daha: ${others.map(m => clock(m.startedAt)).join(', ')})`;
    out.push({ key: `det:${det}`, kind: signalKindOf(det, lead.signal), detector: det, text, ids: ms.map(m => m.id) });
  }
  return out;
}

/** SAF: kanıt alt başlığı. */
export function evidenceSub(members: MLEvent[]): string | undefined {
  const kinds = new Set(members.map(m => signalKindOf(m.detector, m.signal)));
  if (kinds.size > 1) return `${kinds.size} sinyal türü aynı olayda`;
  const dets = new Set(members.map(m => m.detector));
  if (dets.size > 1) return `${dets.size} dedektör aynı olayda`;
  return undefined;
}
