// mlGroupRows — v0.10.2021 (anomali tekilleştirme, mockup
// docs/frontend/mockups/anomaly-unified.html "Liste: tek sayfa"): ML
// sekmesinin olay tablosunda satır = GRUP (D2 gruplaması; tek üyeli grup da
// bir satır). Sunucu her olay satırına grup kimliği, aralığı, sinyalleri ve
// D3 gölge kararını taşır; bu dosya satırları gruba katlar. SAF.
//
// "Ne oldu": grubun EN GÜÇLÜ üyesinin sunucu özeti (ml.ExplainSummary —
// detay kartıyla aynı cümle). En güçlü = en yüksek skor (robust z); eşitse
// en son görülen. Dedektör adı yazılmaz.
//
// Durum (yalnız etiket; canlı Problem / bildirim YOK):
//   Değil dendi  — bir üye "değil" işaretli, hiçbiri "gerçek" değil
//   Sürüyor      — açık üye var VE D3 gölge kararı "açardı"
//   İzleniyor    — açık üye var, karar "açmazdı" (tek dedektör vb.) ya da yok
//   Bitti        — bütün üyeler kapandı ("N sürdü")
import type { MLEvent } from '@/lib/types';
import { signalKindOf, type SignalKind } from '@/lib/signalKind';

export type MLGroupStatus = 'running' | 'watching' | 'done' | 'rejected';

export interface MLGroupRow {
  key: string;
  lead: MLEvent;
  members: MLEvent[];
  service: string;
  start: number; // unix ns
  end: number;   // unix ns
  signals: SignalKind[];
  status: MLGroupStatus;
}

const SIGNAL_ORDER: SignalKind[] = ['trace', 'log', 'metric'];

/** SAF: grubun en güçlü üyesi (skor, sonra son görülme, sonra kimlik). */
export function leadMember(ms: MLEvent[]): MLEvent {
  return [...ms].sort((a, b) => b.score - a.score || b.lastSeen - a.lastSeen || a.id.localeCompare(b.id))[0];
}

/** SAF: olay satırları → grup satırları (girdi sırası korunmaz; varsayılan sıralama tabloda). */
export function groupRows(events: MLEvent[], nowNs = Date.now() * 1e6): MLGroupRow[] {
  const byKey = new Map<string, MLEvent[]>();
  for (const e of events) {
    const k = e.groupId || `ev:${e.id}`;
    const list = byKey.get(k);
    if (list) list.push(e); else byKey.set(k, [e]);
  }
  const out: MLGroupRow[] = [];
  for (const [key, members] of byKey) {
    const lead = leadMember(members);
    const start = lead.groupStart || Math.min(...members.map(m => m.startedAt));
    const end = lead.groupEnd || Math.max(...members.map(m => m.lastSeen));
    const sig = new Set<SignalKind>();
    for (const s of lead.groupSignals ?? []) sig.add(s === 'trace' || s === 'log' || s === 'metric' ? s : 'log');
    for (const d of lead.groupDetectors ?? []) sig.add(signalKindOf(d));
    for (const m of members) sig.add(signalKindOf(m.detector, m.signal));
    out.push({ key, lead, members, service: lead.service, start, end, signals: SIGNAL_ORDER.filter(s => sig.has(s)), status: groupStatus(members, nowNs) });
  }
  return out;
}

/** SAF. */
/** SAF (v0.10.2031; operatör: rozet "İzleniyor · 4 sa 59 dk", cümle "5
 *  dakika sürdü"): üye hâlâ açık mı — sunucu kartının (ml cardOpen) AYNI
 *  kuralı: kapalı damgası yoksa VE son görülme 4 kovadan taze. Kapanış
 *  satırı süreç içi koşu durumuna bağlı (yeniden başlatmada yazılmaz);
 *  yalnız damgaya bakmak bitmiş olayı saatlerce "açık" gösteriyordu. */
export function memberOpen(m: Pick<MLEvent, 'stage' | 'lastSeen' | 'windowSec'>, nowNs: number): boolean {
  if (m.stage === 'closed') return false;
  const bucketNs = (m.windowSec > 0 ? m.windowSec : 300) * 1e9;
  return nowNs - m.lastSeen <= 4 * bucketNs;
}

export function groupStatus(members: MLEvent[], nowNs = Date.now() * 1e6): MLGroupStatus {
  const real = members.some(m => m.verdict === 'anomaly');
  if (!real && members.some(m => m.verdict === 'not_anomaly')) return 'rejected';
  if (members.some(m => memberOpen(m, nowNs))) {
    return members.some(m => m.groupShadow?.wouldOpen) ? 'running' : 'watching';
  }
  return 'done';
}

/** SAF: süre (ns) → kısa metin. */
function dur(ns: number): string {
  const m = Math.max(0, Math.round(ns / 60e9));
  if (m < 60) return `${m} dk`;
  const h = Math.floor(m / 60);
  return h < 48 ? `${h} sa ${m % 60} dk` : `${Math.round(h / 24)} g`;
}

/** SAF (test): durum metni. nowNs: şimdi. */
export function groupStatusText(r: Pick<MLGroupRow, 'status' | 'start' | 'end'>, nowNs: number): string {
  switch (r.status) {
    case 'running': return `Sürüyor · ${dur(nowNs - r.start)}`;
    case 'watching': return `İzleniyor · ${dur(nowNs - r.start)}`;
    case 'rejected': return 'Değil dendi';
    default: return `Bitti · ${dur(r.end - r.start)} sürdü`;
  }
}

/** SAF: durum sıralama değeri (süren önce). */
export function groupStatusRank(s: MLGroupStatus): number {
  return s === 'running' ? 3 : s === 'watching' ? 2 : s === 'done' ? 1 : 0;
}
