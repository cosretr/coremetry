// mlGroupDetail — v0.10.2022 (anomali tekilleştirme, mockup
// docs/frontend/mockups/anomaly-unified.html "Hedef: tek anomali" kartı):
// /anomalies/ml/:id grubun sayfasıdır. Saf yardımcılar: açılan olayın grubu,
// kanıt satırının kaynak notu, başlık operasyonu. Grup üyeleri liste ucunun
// (son 24 sa, aynı önbellek) satırlarından; ek sunucu okuması YOK.
import type { MLEvent, MLGroupDecision, MLSvcShadow } from '@/lib/types';
import { parseObj } from '@/lib/mlReplay';
import { groupRows, type MLGroupRow } from './mlGroupRows';

/** SAF: açılan olay kimliğinin grubu (liste satırlarında yoksa null — tek olay görünümü). */
export function findGroup(events: MLEvent[] | undefined, id: string): MLGroupRow | null {
  if (!events || !id) return null;
  const me = events.find(e => e.id === id);
  if (!me) return null;
  const key = me.groupId || `ev:${me.id}`;
  return groupRows(events.filter(e => (e.groupId || `ev:${e.id}`) === key))[0] ?? null;
}

/** SAF (test): kanıt satırının kaynak notu — sinyal nereden okundu. */
export function evidenceSource(detector: string, signal: string, evidence?: string): string {
  if (detector === 'trace_error_count') return "ClickHouse span'leri";
  if (signal === 'log') return 'Elasticsearch';
  if (signal === 'metric') {
    const ev = parseObj(evidence ?? '');
    if (ev.source === 'thanos') return `Thanos · ${typeof ev.cluster === 'string' && ev.cluster ? ev.cluster : 'cluster'}`;
  }
  return 'VictoriaMetrics';
}

/** v0.10.2053 (D4) — SAF: üyelerin en son grup kararı (gölge ya da canlı). */
export function latestGroupDecision(members: Pick<MLEvent, 'groupShadow'>[]): MLGroupDecision | undefined {
  return members.map(m => m.groupShadow).filter((d): d is MLGroupDecision => !!d).sort((x, y) => y.at - x.at)[0];
}

/** v0.10.2053 (D4) — SAF: grup kararı çipi. Canlı → "Problem açtı" + Problem
 *  bağlantısı; gölge "açardı" (anahtar kapalı) → bağlantısız; açmaz → null. */
export function groupProblemChip(d: MLGroupDecision | undefined): { label: string; href?: string } | null {
  if (!d) return null;
  if (d.live && d.problemId) return { label: `Problem açtı${d.priority ? ` · ${d.priority}` : ''}`, href: `/inbox?problem=${encodeURIComponent(d.problemId)}` };
  if (d.wouldOpen) return { label: 'Problem açardı (canlı kip kapalı)' };
  return null;
}

/** v0.10.2089 — SAF: üyelerin en son servis düzeyi gölge kararı (svc_error_rate / svc_breadth). */
export function latestSvcShadow(members: Pick<MLEvent, 'svcShadow'>[]): MLSvcShadow | undefined {
  return members.map(m => m.svcShadow).filter((d): d is MLSvcShadow => !!d).sort((x, y) => y.at - x.at)[0];
}

/** v0.10.2089 — SAF: servis düzeyi gölge kapı satırı (Problem AÇILMAZ; 14 günlük gölge). */
export function svcGateLine(d: MLSvcShadow): string {
  const verb = !d.wouldOpen ? 'açmazdı' : d.joinsRule ? 'grup Problem\'ine üye olurdu (gölge)' : 'Problem açardı (gölge)';
  return `${verb}${d.priority ? ` · ${d.priority}` : ''} — ${d.reason}`;
}

/** v0.10.2053 (D4) — SAF: teknik ayrıntıdaki "Problem kapısı" satırı. */
export function groupGateLine(d: MLGroupDecision | undefined): string {
  if (!d) return 'karar yok';
  // v0.10.2092: tek sinyal kolu canlı — "açtı" / "açardı" ortak; kol yalnız ekte.
  const verb = (d.live ? 'Problem açtı' : d.wouldOpen ? 'açardı' : 'açmazdı') + (d.singleWouldOpen && d.wouldOpen ? ' (tek sinyal)' : '');
  return `${verb}${d.priority ? ` · ${d.priority}` : ''} — ${d.reason}`;
}

/** SAF: trace olayında başlığın operasyonu (Code = endpoint / operasyon); diğerlerinde boş. */
export function headerOperation(e: Pick<MLEvent, 'signal' | 'code'> | null | undefined): string {
  return e && e.signal === 'trace' && e.code ? e.code : '';
}
