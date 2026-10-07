import type { RolloutEvidence, RootCause, RootCauseBubbleUp } from '@/lib/types';

// rootCauseProgressive.ts — v0.10.1119 (operatör: kök-neden paneli soğuk
// açılışta 30–45 sn). Panel tam /rootcause demeti yerine /rootcause/core
// (hızlı) + /rootcause/bubbleup (tek ham-spans kıyası) okur ve çekirdeği
// gelir gelmez çizer. Bu iki saf yardımcı, birleşik ekranın tam demetin
// çizdiğiyle aynı kalmasını sağlar (RootCausePanel.progressive.test.tsx).

// withBubbleUp — çekirdek demet + ayrı bubbleUp yanıtı → tam demetin şekli.
// bubbleUp yoksa / okunamadıysa alan YOK — tam demette düşen alt-okuma da
// alanı boş bırakırdı (aynı çizim).
export function withBubbleUp(core: RootCause, bu: RootCauseBubbleUp | null | undefined): RootCause {
  const { bubbleUp: _ignored, ...rest } = core;
  void _ignored;
  return bu?.bubbleUp ? { ...rest, bubbleUp: bu.bubbleUp } : rest;
}

// headlineNeedsBubble — RootCausePanel likelyCause sırası deploy → sıcak
// rollout → bubbleUp → …; ilk ikisi yoksa manşet bubbleUp'ı beklemeli (önce
// "localized" deyip sonra fikir değiştirmesin).
export function headlineNeedsBubble(rc: RootCause, rollouts: RolloutEvidence[]): boolean {
  return !rc.recentDeploy && !rollouts.some(r => r.band === 'high');
}
