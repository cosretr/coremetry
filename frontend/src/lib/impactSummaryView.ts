// impactSummaryView — v0.10.2482 (Problems "Etki" sütunu; "etki öncelikte"
// adım 1, yalnız gösterim). Sunucunun etki özeti (Go chstore.StdImpactSummary)
// → tek satır: "~340 istek/dk başarısız · trafiğin %14'ü", "~1.200 istek/dk ·
// +680 ms", "18.213 mesaj birikmiş", "3/5 pod". TR sunucu metni aynen (iyelik
// eki Go'da tek tanım); EN sayılardan. Renk YALNIZ sapan değerde (hot parça):
// başarısız istek, gecikme, biriken mesaj, düşen pod.
import { t, tf, type Lang } from '@/lib/i18n';
import type { ProblemStandard, StdImpactSummary } from '@/lib/types';

export interface ImpactPart { text: string; hot: boolean }

/** Hangi parça sapan değer (renklenir): errors → 0, latency → 1, lag → 0, oom → 0. */
const HOT_INDEX: Record<StdImpactSummary['kind'], number> = { errors: 0, latency: 1, lag: 0, oom: 0 };

function approx(v: number, lang: Lang): string {
  if (v < 0.5) return '<1';
  return `~${Math.round(v).toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US')}`;
}
const int = (v: number) => Math.round(v).toLocaleString('en-US');
function pctEn(p: number): string {
  const d = p > 0 && p < 0.1 ? 2 : 1;
  return `${Number(p.toFixed(d))}%`;
}

function enParts(s: StdImpactSummary): string[] {
  switch (s.kind) {
    case 'errors':
      return [tf('imp.sum.errors', 'en', { n: approx(s.perMin ?? 0, 'en') }),
        tf(s.scope === 'route' ? 'imp.sum.shareRoute' : 'imp.sum.share', 'en', { pct: pctEn(s.sharePct ?? 0) })];
    case 'latency': {
      const d = Math.round(s.delayMs ?? 0);
      const out = [tf('imp.sum.latency', 'en', { n: approx(s.perMin ?? 0, 'en') })];
      if (d > 0) out.push(tf('imp.sum.delay', 'en', { ms: int(d) }));
      return out;
    }
    case 'lag': {
      const out = [tf('imp.sum.lag', 'en', { n: int(s.backlog ?? 0) })];
      if ((s.growthPerMin ?? 0) >= 0.5) out.push(tf('imp.sum.lagGrowth', 'en', { n: int(s.growthPerMin ?? 0) }));
      return out;
    }
    case 'oom':
      return [tf('imp.sum.oom', 'en', { k: s.killed ?? 0, n: s.pods ?? 0 })];
  }
  return [];
}

/** Özet → parçalar; özet yoksa null. */
export function impactSummaryParts(s: StdImpactSummary | null | undefined, lang: Lang = 'tr'): ImpactPart[] | null {
  if (!s) return null;
  const texts = lang === 'tr' && s.text ? s.text.split(' · ') : enParts(s);
  if (texts.length === 0 || !texts[0]) return null;
  // latency'de gecikme yoksa sapan parça yok (istek sayısı bağlamdır).
  return texts.map((text, i) => ({ text, hot: i === HOT_INDEX[s.kind] }));
}

/** Düz metin (title / erişilebilirlik). */
export const impactSummaryText = (s: StdImpactSummary | null | undefined, lang: Lang = 'tr'): string =>
  (impactSummaryParts(s, lang) ?? []).map(p => p.text).join(' · ');

/** Etki sütununun sıralama değeri: özetin büyüklüğü; özetsiz satır −1 (en altta). */
export const impactSortValue = (std: ProblemStandard | null | undefined): number => std?.impactSummary?.rank ?? -1;

/** Özetsiz satırın açıklaması (title). */
export const impactNoneTitle = (lang: Lang = 'tr') => t('imp.sum.none', lang);
