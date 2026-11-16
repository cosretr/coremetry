import type { Tone } from '@/components/ui';
import { t, type Lang } from '@/lib/i18n'; // v0.10.2285 — etiketler lang alır (varsayılan 'tr')
import type { ChatQueryEvidence, SREInvestigation, SREInvestigationFinding, SREInvestigationStep } from '@/lib/types';

// investigationResultView — v0.10.1287 (AI SRE Faz 1b). InvestigationResult'ın
// saf yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın —
// react-refresh). Adım adları Go internal/sre/plan.go sabitlerinin aynası.

/** Boru hattı sırası (dalga 1 → dalga 2 → anlatım); tablo bu sırayla, sıralanmaz. */
export const STEP_ORDER = ['sweep', 'deploys', 'deps', 'pivot', 'log_spike', 'events', 'node', 'summary'] as const;

// v0.10.2285 — adım adları katalogda (inv.step.*): events (v0.10.1288) = alarmlar +
// aşağı akış db/ext problemleri + incident; node (v0.10.1288) tembel, yalnız
// doygunluk hipotezi isterse. Bilinmeyen ad olduğu gibi.
export function stepLabel(name: string, lang: Lang = 'tr'): string {
  return (STEP_ORDER as readonly string[]).includes(name) ? t(`inv.step.${name}`, lang) : name;
}

/** orderedSteps — adımlar boru hattı sırasıyla (bilinmeyen sonda), en çok 8. */
export function orderedSteps(steps: readonly SREInvestigationStep[]): SREInvestigationStep[] {
  const rank = (n: string) => {
    const i = (STEP_ORDER as readonly string[]).indexOf(n);
    return i < 0 ? STEP_ORDER.length : i;
  };
  return [...steps].sort((a, b) => rank(a.name) - rank(b.name)).slice(0, STEP_ORDER.length);
}

/** Adım durumu: yalnız sapan durum renk alır (tablo standardı — ok düz metin). */
export function stepStateView(state: string, lang: Lang = 'tr'): { label: string; tone: Tone | null } {
  switch (state) {
    case 'ok': return { label: t('inv.stepState.ok', lang), tone: null };
    case 'partial': return { label: t('inv.stepState.partial', lang), tone: 'warning' };
    case 'error': return { label: t('inv.stepState.error', lang), tone: 'danger' };
    case 'skipped': return { label: t('inv.stepState.skipped', lang), tone: null };
    default: return { label: state, tone: null };
  }
}

/** Adım süresi: 950 ms / 1,2 sn. */
export function fmtStepDuration(ms: number, lang: Lang = 'tr'): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return lang === 'tr' ? `${(ms / 1000).toFixed(1).replace('.', ',')} sn` : `${(ms / 1000).toFixed(1)} s`;
}

/** findingEvidence — bulgunun kanıt künyeleri: `evidences[i]` → bulguyu üreten
 *  adımın evidence[i]'si (adım yoksa sweep). Tekrarsız, bulunamayan düşer. */
export function findingEvidence(inv: SREInvestigation, f: SREInvestigationFinding): ChatQueryEvidence[] {
  const step = inv.steps.find(s => s.name === (f.step || 'sweep'));
  const evs = step?.evidence ?? [];
  const out: ChatQueryEvidence[] = [];
  const seen = new Set<number>();
  for (const ref of f.evidenceRefs ?? []) {
    const m = /^evidences\[(\d+)\]$/.exec(ref);
    if (!m) continue;
    const i = Number(m[1]);
    if (seen.has(i) || !evs[i]) continue;
    seen.add(i);
    out.push(evs[i]);
  }
  return out;
}

/** Bulgu türünün Türkçe adı (Go sre kindTR aynası). */
export function findingKindLabel(kind: string, lang: Lang = 'tr'): string {
  switch (kind) {
    case 'operation': return t('inv.kind.operation', lang);
    case 'function': return t('inv.kind.function', lang);
    case 'dependency': return t('inv.kind.dependency', lang);
    case 'k8s': return 'Kubernetes';
    case 'jvm': return 'JVM';
    case 'node': return 'node';
    default: return kind;
  }
}

/** askInChatQuestion — "Sohbette sor": problem kapsamı (@problem:) + kayıtlı
 *  araştırmayı (get_problem `investigation`) kullanma talimatı. */
export function askInChatQuestion(inv: SREInvestigation): string {
  return `@problem:${inv.problemId} için kayıtlı AI SRE araştırmasını değerlendir — bulgular neyi gösteriyor, sonraki adım ne olmalı?`;
}
