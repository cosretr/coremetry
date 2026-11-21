import type { Status } from '@/lib/statusTone';
import { t, tf, type Lang } from '@/lib/i18n';
import type {
  SRECoverageGate, SREGateGap, SREHypothesis, SREHypothesisState, SREInvestigation, SREReachEntry,
} from '@/lib/types';
import { STEP_ORDER, stepLabel } from './investigationResultView';

// investigationQuality — CoSRE Faz B (araştırma kalitesi) saf yardımcıları:
// hipotez ağacı (durum sırası), erişim beyanı, kanıt kapısı bandı. Go
// internal/sre hypothesis_state.go / reach.go / coverage.go aynası; karar
// Go'da verilir, burada yalnız çizilir.

/** Ağaç sırası: desteklenen önce, elenen en sonda (katlı). */
export const STATE_ORDER: readonly SREHypothesisState[] = ['supported', 'tested', 'untested', 'ruled_out'];

export function stateTone(s: SREHypothesisState | undefined): Status {
  switch (s) {
    case 'supported': return 'warn'; // olası neden dikkat çeker (bant rozetiyle aynı dil)
    case 'tested': return 'info';
    default: return 'unknown';
  }
}

export function stateLabel(s: SREHypothesisState | undefined, lang: Lang = 'tr'): string {
  return s ? t(`iq.state.${s}`, lang) : '';
}

/** hypothesisTree — durumlu kartlar: açık dallar (durum sırası, içinde skor
 *  sırası korunur) + elenenler (katlı). Durumsuz kayıt (eski / plain) → boş. */
export function hypothesisTree(inv: SREInvestigation): { open: SREHypothesis[]; ruledOut: SREHypothesis[] } {
  const hs = (inv.hypotheses ?? []).filter(h => !!h.state);
  const rank = (h: SREHypothesis) => STATE_ORDER.indexOf(h.state as SREHypothesisState);
  const open = hs.filter(h => h.state !== 'ruled_out').sort((a, b) => rank(a) - rank(b));
  return { open, ruledOut: hs.filter(h => h.state === 'ruled_out') };
}

/** Kaynak (adım) adı: bilinen adım katalogdan, errors ayrıca; bilinmeyen olduğu gibi. */
export function sourceLabel(name: string, lang: Lang = 'tr'): string {
  if (name === 'errors') return t('inv.step.errors', lang);
  return (STEP_ORDER as readonly string[]).includes(name) ? stepLabel(name, lang) : name;
}

/** Erişim beyanı: bakılan / bakılamayan (durum etiketiyle). */
export function reachSplit(reach: readonly SREReachEntry[] | undefined): { checked: SREReachEntry[]; blind: SREReachEntry[] } {
  const rs = reach ?? [];
  return {
    checked: rs.filter(r => r.status === 'checked' || r.status === 'partial'),
    blind: rs.filter(r => r.status !== 'checked' && r.status !== 'partial'),
  };
}

export function reachStatusLabel(r: SREReachEntry, lang: Lang = 'tr'): string {
  return r.status === 'checked' ? '' : t(`iq.reach.${r.status}`, lang);
}

/** Kanıt kapısı bandı YALNIZ kip "show" ve karar "insufficient" iken (shadow = kayıtta, gösterilmez). */
export function showGate(g: SRECoverageGate | undefined): g is SRECoverageGate {
  return !!g && g.mode === 'show' && g.verdict === 'insufficient';
}

export function gapText(g: SREGateGap, lang: Lang = 'tr'): string {
  switch (g.code) {
    case 'no_supported': return t('iq.gate.no_supported', lang);
    case 'single_source': return tf('iq.gate.single_source', lang, { s: g.source ? sourceLabel(g.source, lang) : '—' });
    case 'blind_source': return tf('iq.gate.blind_source', lang, {
      s: g.source ? sourceLabel(g.source, lang) : '—', st: g.status ? t(`iq.reach.${g.status}`, lang) : '',
    });
    default: return g.code;
  }
}

export function playbookLabel(inv: SREInvestigation, lang: Lang = 'tr'): string {
  const pb = inv.playbook;
  if (!pb) return '';
  return tf('iq.playbook', lang, { p: ['failure_rate', 'latency', 'consumer_lag', 'generic'].includes(pb.type) ? t(`iq.pb.${pb.type}`, lang) : pb.label });
}
