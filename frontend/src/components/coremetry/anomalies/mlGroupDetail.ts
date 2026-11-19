// mlGroupDetail — v0.10.2022 (anomali tekilleştirme, mockup
// docs/frontend/mockups/anomaly-unified.html "Hedef: tek anomali" kartı):
// /anomalies/ml/:id grubun sayfasıdır. Saf yardımcılar: açılan olayın grubu,
// kanıt satırının kaynak notu, başlık operasyonu. Grup üyeleri liste ucunun
// (son 24 sa, aynı önbellek) satırlarından; ek sunucu okuması YOK.
import type { MLEvent, MLGroupDecision, MLSvcShadow } from '@/lib/types';
import { parseObj } from '@/lib/mlReplay';
import { groupRows, type MLGroupRow } from './mlGroupRows';
import { t, type Lang } from '@/lib/i18n'; // v0.10.2291 — görünen metin isteğe bağlı lang (varsayılan 'tr')

/** SAF: açılan olay kimliğinin grubu (liste satırlarında yoksa null — tek olay görünümü). */
export function findGroup(events: MLEvent[] | undefined, id: string): MLGroupRow | null {
  if (!events || !id) return null;
  const me = events.find(e => e.id === id);
  if (!me) return null;
  const key = me.groupId || `ev:${me.id}`;
  return groupRows(events.filter(e => (e.groupId || `ev:${e.id}`) === key))[0] ?? null;
}

/** Kaynağı VM RED ailesi olan trace dedektörleri (ml/trace_red.go, trace_pods.go). */
const TRACE_VM_DETECTORS = new Set(['op_latency', 'op_error_rate', 'throughput_drop', 'trace_pod_outlier']);

/** SAF (test): kanıt satırının kaynak notu — sinyal nereden okundu. */
export function evidenceSource(detector: string, signal: string, evidence?: string, lang: Lang = 'tr'): string {
  if (detector === 'trace_error_count') return t('mlx.src.chSpans', lang);
  if (signal === 'log') return 'Elasticsearch';
  const ev = parseObj(evidence ?? '');
  if (ev.source === 'thanos') return `Thanos · ${typeof ev.cluster === 'string' && ev.cluster ? ev.cluster : 'cluster'}`;
  // v0.10.2204 (Örnek 4) — trace olayında kaynak olayın kendi kaynağından:
  // VM RED serisi (span metrikleri) ya da ClickHouse span'leri; "Trace"
  // rozetinin yanında çıplak "VictoriaMetrics" ham trace sanılıyordu.
  if (signal === 'trace') {
    const vm = ev.source === 'vm' || (ev.source === undefined && TRACE_VM_DETECTORS.has(detector));
    return t(vm ? 'mlx.src.vmRed' : 'mlx.src.chSpans', lang);
  }
  return 'VictoriaMetrics';
}

/** v0.10.2053 (D4) — SAF: üyelerin en son grup kararı (gölge ya da canlı). */
export function latestGroupDecision(members: Pick<MLEvent, 'groupShadow'>[]): MLGroupDecision | undefined {
  return members.map(m => m.groupShadow).filter((d): d is MLGroupDecision => !!d).sort((x, y) => y.at - x.at)[0];
}

/** v0.10.2053 (D4) — SAF: grup kararı çipi. Canlı → "Problem açtı" + Problem
 *  bağlantısı; gölge "açardı" (anahtar kapalı) → bağlantısız; açmaz → null. */
export function groupProblemChip(d: MLGroupDecision | undefined, lang: Lang = 'tr'): { label: string; href?: string } | null {
  if (!d) return null;
  if (d.live && d.problemId) return { label: `${t('mlx.gate.opened', lang)}${d.priority ? ` · ${d.priority}` : ''}`, href: `/inbox?problem=${encodeURIComponent(d.problemId)}` };
  if (d.wouldOpen) return { label: t('mlx.gate.wouldLiveOff', lang) };
  return null;
}

/** v0.10.2089 — SAF: üyelerin en son servis düzeyi gölge kararı (svc_error_rate / svc_breadth). */
export function latestSvcShadow(members: Pick<MLEvent, 'svcShadow'>[]): MLSvcShadow | undefined {
  return members.map(m => m.svcShadow).filter((d): d is MLSvcShadow => !!d).sort((x, y) => y.at - x.at)[0];
}

/** v0.10.2089 — SAF: servis düzeyi kapı satırı. v0.10.2130: canlı — Problem'e
 *  bağlandıysa "Problem açtı" (ya da servisin ML Problem'ine üye); kapalıyken gölge. */
export function svcGateLine(d: MLSvcShadow, lang: Lang = 'tr'): string {
  const verb = t(d.live ? (d.problemId && d.problemId !== d.rule ? 'mlx.gate.svcMember' : 'mlx.gate.opened')
    : !d.wouldOpen ? 'mlx.gate.wouldNot' : d.joinsRule ? 'mlx.gate.wouldJoin' : 'mlx.gate.wouldShadow', lang);
  return `${verb}${d.priority ? ` · ${d.priority}` : ''} — ${d.reason}`;
}

/** v0.10.2053 (D4) — SAF: teknik ayrıntıdaki "Problem kapısı" satırı. */
export function groupGateLine(d: MLGroupDecision | undefined, lang: Lang = 'tr'): string {
  if (!d) return t('mlx.gate.none', lang);
  // v0.10.2092: tek sinyal kolu canlı — "açtı" / "açardı" ortak; kol yalnız ekte.
  const verb = t(d.live ? 'mlx.gate.opened' : d.wouldOpen ? 'mlx.gate.would' : 'mlx.gate.wouldNot', lang) + (d.singleWouldOpen && d.wouldOpen ? t('mlx.gate.single', lang) : '');
  return `${verb}${d.priority ? ` · ${d.priority}` : ''} — ${d.reason}`;
}

/** v0.10.2371 — Problem kapısının GERÇEK kararı, kayıtlı olay kartı için:
 *  çip başlıkta, satırlar kanıt bölümünde. Kaynak grubun en son grup kararı ve
 *  servis düzeyi kararı (olay kanıtındaki damgalar; ek istek yok). Grup ya da
 *  damga yoksa boş döner — kart o zaman karar hakkında bir şey söylemez. */
export interface RecordDecision { chip: { label: string; href?: string } | null; lines: { k: string; v: string }[] }
export function recordDecision(
  group: { members: Pick<MLEvent, 'groupShadow' | 'svcShadow'>[]; lead?: Pick<MLEvent, 'groupShadow' | 'svcShadow'> | null } | null | undefined,
  lang: Lang = 'tr',
): RecordDecision {
  const members = group?.members ?? [];
  const all = group?.lead ? [...members, group.lead] : members;
  const gd = latestGroupDecision(all);
  const sd = latestSvcShadow(all);
  return {
    chip: groupProblemChip(gd, lang),
    lines: [
      ...(gd ? [{ k: t('mlx.tech.gate', lang), v: groupGateLine(gd, lang) }] : []),
      ...(sd ? [{ k: t('mlx.tech.svcGate', lang), v: svcGateLine(sd, lang) }] : []),
    ],
  };
}

/** SAF: trace olayında başlığın operasyonu (Code = endpoint / operasyon); diğerlerinde boş. */
export function headerOperation(e: Pick<MLEvent, 'signal' | 'code'> | null | undefined): string {
  return e && e.signal === 'trace' && e.code ? e.code : '';
}
