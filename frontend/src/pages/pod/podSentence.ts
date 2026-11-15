// podSentence — /pod'un durum sözcüğü, düz durum cümlesi ve kardeş kıyası
// cümlesi (Clusters S11; plan docs/frontend/clusters-shadcn-plan.md §10.2,
// §10.3). DETERMİNİSTİK, model yok; girdi yalnız zaten yüklü ClusterPodRow +
// EntityContainerStatus + kardeş satırları. Her parça alanı yoksa düşer.
import type { ClusterPodRow, EntityContainerStatus } from '@/lib/types';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { trPossLoc } from '@/lib/trSuffix';
import { t, tf, fmtPctLang, type Lang } from '@/lib/i18n';

// v0.10.2246 — metinler arayüz dilinde (lib/i18n podpage.*); varsayılan 'tr'
// (nsDetailModel emsali), sayfa her çağrıda useLang() geçirir.
const WAITING_WORD: Record<string, string> = {
  ImagePullBackOff: 'podpage.wait.imagePull',
  ErrImagePull: 'podpage.wait.imagePull',
  CrashLoopBackOff: 'podpage.wait.crashLoop',
  CreateContainerConfigError: 'podpage.wait.config',
};

/** Kimlik değerleri (karşılaştırma için Türkçe kalır); görünen metin podStatusWordText. */
export type PodStatusWord = 'Sağlıklı' | 'Dikkat' | 'Sorunlu';

const STATUS_KEY: Record<PodStatusWord, string> = { 'Sağlıklı': 'podpage.status.ok', 'Dikkat': 'podpage.status.warn', 'Sorunlu': 'podpage.status.err' };

/** Durum sözcüğünün arayüz dilindeki metni (EN Healthy / Warning / Failing). */
export function podStatusWordText(w: PodStatusWord, lang: Lang = 'tr'): string {
  return t(STATUS_KEY[w], lang);
}

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
export function podSentence(row: Row | undefined, ctrs?: readonly EntityContainerStatus[], lang: Lang = 'tr'): string {
  const parts: string[] = [];
  const phase = row?.phase ?? '';
  const waiting = waitingOf(ctrs);
  if (waiting) parts.push(tf('podpage.sent.waiting', lang, { reason: WAITING_WORD[waiting] ? t(WAITING_WORD[waiting], lang) : waiting }));
  else if (phase === 'Running') parts.push(t('podpage.sent.running', lang));
  else if (phase === 'Pending') parts.push(t('podpage.sent.pending', lang));
  else if (phase === 'Failed' || phase === 'Succeeded') parts.push(t('podpage.sent.terminated', lang));
  const rst = restartsOf(row, ctrs);
  if (rst != null && rst > 0) parts.push(tf('podpage.sent.restarts', lang, { n: fmtNum(rst) }));
  const term = termOf(row, ctrs);
  if (term === 'OOMKilled') parts.push(t('podpage.sent.oom', lang));
  else if (term === 'Error') parts.push(t('podpage.sent.error', lang));
  // TR "%99'unda" (iyelik+bulunma eki), EN "99%".
  const pct = (v: number) => (lang === 'tr' ? `%${trPossLoc(Math.round(v))}` : fmtPctLang(Math.round(v), lang));
  if ((row?.memPct ?? 0) >= 90) parts.push(tf('podpage.sent.memPct', lang, { pct: pct(row!.memPct!) }));
  if ((row?.cpuPct ?? 0) >= 90) parts.push(tf('podpage.sent.cpuPct', lang, { pct: pct(row!.cpuPct!) }));
  if (parts.length === 0) return '';
  const s = parts.join(', ');
  return `${s.charAt(0).toLocaleUpperCase(lang === 'tr' ? 'tr-TR' : 'en-US')}${s.slice(1)}.`;
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
  siblings: readonly { memPct?: number | null; memBytes?: number | null }[], lang: Lang = 'tr'): string {
  if (!self) return '';
  const pcts = siblings.map(s => s.memPct).filter((v): v is number => v != null && v > 0);
  const usePct = self.memPct != null && self.memPct > 0 && pcts.length > 0;
  const vals = usePct ? pcts : siblings.map(s => s.memBytes).filter((v): v is number => v != null && v > 0);
  const mine = usePct ? self.memPct! : self.memBytes;
  if (vals.length === 0 || mine == null || mine <= 0) return '';
  const fmt = (v: number) => (usePct ? fmtPctLang(Math.round(v), lang) : fmtBytes(v));
  const tail = t(mine > Math.max(...vals) ? 'podpage.sib.highest' : 'podpage.sib.normal', lang);
  return tf('podpage.sib.sentence', lang, { n: fmtNum(vals.length), median: fmt(median(vals)), mine: fmt(mine), tail });
}
