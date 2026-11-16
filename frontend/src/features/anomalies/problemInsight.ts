// problemInsight.ts — v0.10.562 SAF: ProblemInsight → şerit hücreleri.
// Bilinmeyen hücre '—' (sebep uydurma yok); zaman yerel HH:MM; süre kısa.
import type { ProblemInsight } from '@/lib/types';
import { fmtEvidenceTime } from '@/lib/chatEvidence';
import { t, tf, type Lang } from '@/lib/i18n'; // v0.10.2290 — hücre metni isteğe bağlı lang (varsayılan 'tr')

// v0.10.929 (K5) — 'ok' tonu kalktı: "ilk kez" ne geçiş ne veri; yeşil yok.
export interface InsightCell { key: string; label: string; text: string; href?: string; tone?: 'warn' | 'muted' }

// v0.10.2290 — birimler lib/i18n'de (aed.unit.*), isteğe bağlı lang (varsayılan 'tr').
export function fmtDurationShort(s: number, lang: Lang = 'tr'): string {
  if (!(s > 0)) return '—';
  if (s < 90) return tf('aed.unit.s', lang, { n: Math.round(s) });
  if (s < 5400) return tf('aed.unit.m', lang, { n: Math.round(s / 60) });
  if (s < 172800) return tf('aed.unit.h', lang, { n: (s / 3600).toFixed(1) });
  return tf('aed.unit.d', lang, { n: Math.round(s / 86400) });
}

export function fmtDeltaTR(deltaS: number, lang: Lang = 'tr'): string {
  const a = Math.abs(deltaS);
  const d = a < 90 ? tf('aed.unit.s', lang, { n: Math.round(a) })
    : a < 5400 ? tf('aed.unit.m', lang, { n: Math.round(a / 60) })
    : tf('aed.unit.h', lang, { n: (a / 3600).toFixed(1) });
  return tf(deltaS <= 0 ? 'aed.ins.before' : 'aed.ins.after', lang, { d });
}

export function insightCells(ins: ProblemInsight | null | undefined, serviceHref: (svc: string) => string, lang: Lang = 'tr'): InsightCell[] {
  if (!ins) return [];
  const sus = t('hyp.synth.suspect', lang);
  const first = t('aed.ins.firstAnomaly', lang);
  const before = t('aed.ins.before.label', lang);
  const suspect: InsightCell = ins.hypothesisComputed && ins.topSuspect
    ? { key: 'suspect', label: sus, text: `${ins.topSuspect} (${(ins.confidence ?? 0).toFixed(2)})`, href: serviceHref(ins.topSuspect), tone: 'warn' }
    : { key: 'suspect', label: sus, text: t(ins.hypothesisComputed ? 'aed.ins.noCause' : 'aed.ins.noHyp', lang), tone: 'muted' };
  const anomaly: InsightCell = ins.firstAnomaly
    ? { key: 'anomaly', label: first, text: `${fmtEvidenceTime(ins.firstAnomaly.at)} · ${ins.firstAnomaly.kind}` }
    : { key: 'anomaly', label: first, text: '—', tone: 'muted' };
  const rollout: InsightCell = ins.rollout
    ? { key: 'rollout', label: 'rollout', text: `${ins.rollout.workload}${ins.rollout.version ? ' → ' + ins.rollout.version : ''} · ${fmtDeltaTR(ins.rollout.deltaS, lang)}`, tone: Math.abs(ins.rollout.deltaS) <= 1800 ? 'warn' : undefined }
    : { key: 'rollout', label: 'rollout', text: '—', tone: 'muted' };
  const similar: InsightCell = ins.similar
    ? { key: 'similar', label: before, text: `${ins.similar.count}× · ${tf('aed.ins.last', lang, { d: fmtDurationShort(ins.similar.lastDurationS, lang) })}${ins.similar.lastAssignee ? ' · ' + ins.similar.lastAssignee : ''}`, href: `/problems?problem=${encodeURIComponent(ins.similar.lastId)}` }
    : { key: 'similar', label: before, text: t('aed.ins.firstTime', lang), tone: 'muted' };
  return [suspect, anomaly, rollout, similar];
}
