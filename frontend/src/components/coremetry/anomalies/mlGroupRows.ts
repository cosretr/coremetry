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
import { t, tf, type Lang } from '@/lib/i18n';
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
  /** v0.10.2359: bitti çünkü veri gelmedi (banda dönmedi) — durum metni ayrı. */
  noData?: boolean;
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
    out.push({ key, lead, members, service: lead.service, start, end, signals: SIGNAL_ORDER.filter(s => sig.has(s)), status: groupStatus(members, nowNs), noData: groupNoData(members, nowNs) });
  }
  return out;
}

/** SAF. */
/** SAF (v0.10.2031; operatör: rozet "İzleniyor · 4 sa 59 dk", cümle "5
 *  dakika sürdü"): üye hâlâ açık mı — sunucu kartının (ml cardOpen) AYNI
 *  kuralı: kapalı damgası yoksa VE son görülme 4 kovadan taze. Kapanış
 *  satırı süreç içi koşu durumuna bağlı (yeniden başlatmada yazılmaz);
 *  yalnız damgaya bakmak bitmiş olayı saatlerce "açık" gösteriyordu. */
export function memberOpen(m: Pick<MLEvent, 'stage' | 'lastSeen' | 'windowSec' | 'evidence'>, nowNs: number): boolean {
  if (m.stage === 'closed') return false;
  const { since } = memberNoData(m);
  if (since > 0 && nowNs - since * 1e9 <= NO_DATA_HOLD_NS) return true;
  const bucketNs = (m.windowSec > 0 ? m.windowSec : 300) * 1e9;
  return nowNs - m.lastSeen <= 4 * bucketNs;
}

/** v0.10.2359 — sunucunun noDataHoldMax'ı (ml/no_data.go): veri yokken açık tutma sınırı. */
const NO_DATA_HOLD_NS = 3 * 3600 * 1e9;

/** SAF (v0.10.2359): kanıttaki veri-yok durumu — since: tutma başı (unix sn), ended: "veri gelmedi" ile kapandı. */
export function memberNoData(m: Pick<MLEvent, 'evidence'>): { since: number; ended: boolean } {
  if (!m.evidence || !m.evidence.includes('noData') && !m.evidence.includes('endReason')) return { since: 0, ended: false };
  try {
    const e = JSON.parse(m.evidence) as { noDataSince?: number; endReason?: string };
    return { since: Number(e.noDataSince) || 0, ended: e.endReason === 'no_data' };
  } catch {
    return { since: 0, ended: false };
  }
}

/** SAF (v0.10.2359): grup bitti VE en son biten üyesi "veri gelmedi" ile kapandı. */
export function groupNoData(members: MLEvent[], nowNs: number): boolean {
  if (members.some(m => memberOpen(m, nowNs))) return false;
  const last = [...members].sort((a, b) => b.lastSeen - a.lastSeen)[0];
  return !!last && memberNoData(last).ended;
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
function dur(ns: number, lang: Lang = 'tr'): string {
  const m = Math.max(0, Math.round(ns / 60e9));
  if (m < 60) return tf('anomalies.dur.m', lang, { m });
  const h = Math.floor(m / 60);
  return h < 48 ? tf('anomalies.dur.hm', lang, { h, m: m % 60 }) : tf('anomalies.dur.d', lang, { d: Math.round(h / 24) });
}

/** SAF (test): durum metni. nowNs: şimdi. */
// v0.10.2258 — lang (varsayılan 'tr'; TR metin aynı).
export function groupStatusText(r: Pick<MLGroupRow, 'status' | 'start' | 'end' | 'noData'>, nowNs: number, lang: Lang = 'tr'): string {
  switch (r.status) {
    case 'running': return tf('anomalies.status.running', lang, { d: dur(nowNs - r.start, lang) });
    case 'watching': return tf('anomalies.status.watching', lang, { d: dur(nowNs - r.start, lang) });
    case 'rejected': return t('anomalies.status.rejected', lang);
    default: {
      // v0.10.2034 (operatör: "Bittiyse bitişini de göstersin") — bitiş SAATİ
      // açıkça; başka güne taşıyorsa tarihle. Bitiş = son anormal kovanın sonu
      // (olayın last_seen'i; kapanış kararının verildiği an DEĞİL).
      if (r.noData) return tf('anomalies.status.noData', lang, { end: endClock(r.end, nowNs) });
      if (!(r.end > r.start)) return tf('anomalies.status.doneSingle', lang, { end: endClock(r.end, nowNs) });
      return tf('anomalies.status.done', lang, { end: endClock(r.end, nowNs), d: dur(r.end - r.start, lang) });
    }
  }
}

/** SAF: bitiş saati; bugün değilse "12.11 16:12". */
export function endClock(endNs: number, nowNs: number): string {
  const d = new Date(endNs / 1e6);
  const n = new Date(nowNs / 1e6);
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  if (d.toDateString() === n.toDateString()) return hm;
  return `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')} ${hm}`;
}

/** SAF: durum sıralama değeri (süren önce). */
export function groupStatusRank(s: MLGroupStatus): number {
  return s === 'running' ? 3 : s === 'watching' ? 2 : s === 'done' ? 1 : 0;
}
