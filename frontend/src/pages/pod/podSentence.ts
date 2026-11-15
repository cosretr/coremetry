// podSentence — /pod'un durum sözcüğü, düz durum cümlesi ve kardeş kıyası
// cümlesi (Clusters S11; plan docs/frontend/clusters-shadcn-plan.md §10.2,
// §10.3). DETERMİNİSTİK, model yok; girdi yalnız zaten yüklü ClusterPodRow +
// EntityContainerStatus + kardeş satırları. Her parça alanı yoksa düşer.
import type { ClusterPodRow, EntityContainerStatus } from '@/lib/types';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { trPossLoc } from '@/lib/trSuffix';

const WAITING_WORD: Record<string, string> = {
  ImagePullBackOff: 'imaj çekilemedi',
  ErrImagePull: 'imaj çekilemedi',
  CrashLoopBackOff: 'başlatılamıyor',
  CreateContainerConfigError: 'yapılandırma eksik',
};

export type PodStatusWord = 'Sağlıklı' | 'Dikkat' | 'Sorunlu';

type Row = Pick<ClusterPodRow, 'phase' | 'restarts' | 'restartsUnknown' | 'lastTermReason' | 'memPct' | 'cpuPct'>;

function waitingOf(ctrs: readonly EntityContainerStatus[] | undefined): string {
  for (const c of ctrs ?? []) if (c.waitingReason && WAITING_WORD[c.waitingReason]) return c.waitingReason;
  for (const c of ctrs ?? []) if (c.waitingReason) return c.waitingReason;
  return '';
}
function restartsOf(row: Row | undefined, ctrs: readonly EntityContainerStatus[] | undefined): number | null {
  if (row && !row.restartsUnknown && row.restarts != null) return row.restarts;
  if (ctrs && ctrs.length > 0) return ctrs.reduce((a, c) => a + (c.restarts ?? 0), 0);
  return null;
}
function termOf(row: Row | undefined, ctrs: readonly EntityContainerStatus[] | undefined): string {
  if (row?.lastTermReason) return row.lastTermReason;
  if ((ctrs ?? []).some(c => c.lastTermReason === 'OOMKilled')) return 'OOMKilled';
  return (ctrs ?? []).find(c => c.lastTermReason)?.lastTermReason ?? '';
}

/** §10.2 durum sözcüğü: Pending / Failed / OOM / bekleme → Sorunlu; restart ya da bellek ≥ %90 → Dikkat; Running → Sağlıklı. Faz yoksa null. */
export function podStatusWord(row: Row | undefined, ctrs?: readonly EntityContainerStatus[]): PodStatusWord | null {
  const phase = row?.phase ?? '';
  const waiting = waitingOf(ctrs);
  if (phase === 'Pending' || phase === 'Failed' || waiting || termOf(row, ctrs) === 'OOMKilled') return 'Sorunlu';
  if ((restartsOf(row, ctrs) ?? 0) > 0 || (row?.memPct ?? 0) >= 90) return 'Dikkat';
  if (phase === 'Running') return 'Sağlıklı';
  return null;
}

/** §10.2 düz cümle: "Çalışıyor, toplam 7 yeniden başlama, bellek limitini aştı, bellek limitinin %99'unda." */
export function podSentence(row: Row | undefined, ctrs?: readonly EntityContainerStatus[]): string {
  const parts: string[] = [];
  const phase = row?.phase ?? '';
  const waiting = waitingOf(ctrs);
  if (waiting) parts.push(`bekliyor (${WAITING_WORD[waiting] ?? waiting})`);
  else if (phase === 'Running') parts.push('çalışıyor');
  else if (phase === 'Pending') parts.push('bekliyor');
  else if (phase === 'Failed' || phase === 'Succeeded') parts.push('sonlandı');
  const rst = restartsOf(row, ctrs);
  if (rst != null && rst > 0) parts.push(`toplam ${fmtNum(rst)} yeniden başlama`);
  const term = termOf(row, ctrs);
  if (term === 'OOMKilled') parts.push('bellek limitini aştı');
  else if (term === 'Error') parts.push('hata ile sonlandı');
  if ((row?.memPct ?? 0) >= 90) parts.push(`bellek limitinin %${trPossLoc(Math.round(row!.memPct!))}`);
  if ((row?.cpuPct ?? 0) >= 90) parts.push(`CPU limitinin %${trPossLoc(Math.round(row!.cpuPct!))}`);
  if (parts.length === 0) return '';
  const s = parts.join(', ');
  return `${s.charAt(0).toLocaleUpperCase('tr-TR')}${s.slice(1)}.`;
}

function median(v: readonly number[]): number {
  const s = [...v].sort((a, b) => a - b);
  const m = s.length >> 1;
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

/**
 * §10.3 kardeş kıyası: "Aynı iş yükündeki 19 pod'un bellek ortancası %72, bu
 * pod %99 — en yüksek." Limit yüzdesi hem bu pod hem ≥1 kardeş için varsa
 * yüzdeyle, yoksa baytla. Kardeş (ölçüsü bilinen) yoksa ''.
 */
export function siblingSentence(self: { memPct?: number; memBytes?: number } | undefined,
  siblings: readonly { memPct?: number | null; memBytes?: number | null }[]): string {
  if (!self) return '';
  const pcts = siblings.map(s => s.memPct).filter((v): v is number => v != null && v > 0);
  const usePct = self.memPct != null && self.memPct > 0 && pcts.length > 0;
  const vals = usePct ? pcts : siblings.map(s => s.memBytes).filter((v): v is number => v != null && v > 0);
  const mine = usePct ? self.memPct! : self.memBytes;
  if (vals.length === 0 || mine == null || mine <= 0) return '';
  const fmt = (v: number) => (usePct ? `%${Math.round(v)}` : fmtBytes(v));
  const tail = mine > Math.max(...vals) ? 'en yüksek' : 'ortalamanın içinde';
  return `Aynı iş yükündeki ${fmtNum(vals.length)} pod'un bellek ortancası ${fmt(median(vals))}, bu pod ${fmt(mine)} — ${tail}.`;
}
