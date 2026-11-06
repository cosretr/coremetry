// logPatternsTrend — v0.10.508 (log arama denetimi C6): Desenler panelinin
// "Önceki pencere" hücresi. Taban = hemen önceki eşit pencerenin ÖRNEKLEMESİ
// (tek ES sayfası); bu yüzden "önceki pencerede yok" bir kanıt değil ipucudur.
//
// v0.10.1896 (Log Search Faz 4 dilim 4.2):
//   - Oran PAY üzerinden: (sayı / örnek) ÷ (önceki sayı / önceki örnek). İki
//     örneklem farklı boyda (pencere 2000'e kadar, taban 500) — ham sayı oranı
//     bu farkı artış diye gösteriyordu. Örnek boyu bilinmiyorsa ham oran.
//   - Metin oran ifadesi kuralıyla (lib/ratioPhrase): "yaklaşık 3 kat",
//     "biraz yüksek"; "×3.0" yok.
//   - "önceki pencerede yok" ile "ilk kez görüldü" (7 günlük ES yoklaması,
//     yalnız ayar açıkken) AYRI etiket: ilki örneklem ipucu, ikincisi yoklama.
import type { LogPatternGroup, LogPatternInsights, LogPatternsBaseline } from './types';
import { ratioPhrase } from './ratioPhrase';

export type TrendCell =
  | { kind: 'none' }                     // taban istenmedi / okunamadı
  | { kind: 'new' }                      // önceki örneklemede yok
  | { kind: 'ratio'; ratio: number; up: boolean; flat: boolean };

export function trendCell(g: LogPatternGroup, base: LogPatternsBaseline | undefined, curSampled?: number): TrendCell {
  if (!base || base.degraded) return { kind: 'none' };
  if (g.new) return { kind: 'new' };
  const prev = g.prevCount ?? 0;
  let r = g.ratio ?? 0;
  if (curSampled && curSampled > 0 && base.sampled > 0 && prev > 0) {
    r = (g.count / curSampled) / (prev / base.sampled);
  }
  if (!(r > 0)) return { kind: 'none' };
  return { kind: 'ratio', ratio: r, up: r >= 1.8, flat: r >= 1 / 1.12 && r < 1.12 };
}

export function trendLabel(c: TrendCell): string {
  if (c.kind === 'new') return 'önceki pencerede yok';
  if (c.kind === 'ratio') return ratioPhrase(c.ratio, 1);
  return '—';
}

// trendSortValue — sıralama: yeni en üstte, sonra oran; tabansız 0.
export function trendSortValue(g: LogPatternGroup, base: LogPatternsBaseline | undefined, curSampled?: number): number {
  const c = trendCell(g, base, curSampled);
  if (c.kind === 'new') return Number.MAX_SAFE_INTEGER;
  if (c.kind === 'ratio') return c.ratio;
  return 0;
}

export type FirstSeenBadge = { text: string; tone: 'warn' | 'muted'; title: string } | null;

// firstSeenBadge — SAF: 7 günlük yoklamanın etiketi. "first" → "ilk kez
// görüldü"; "seen" → etiket yok (yalnız "önceki pencerede yok" kalır);
// "unknown" → "ölçülemedi" (yokluk iddia edilmez).
export function firstSeenBadge(g: LogPatternGroup, lookbackDays = 7): FirstSeenBadge {
  switch (g.firstSeen7d) {
    case 'first':
      return { text: 'ilk kez görüldü', tone: 'warn', title: `Pencereden geriye ${lookbackDays} gün içinde bu desene uyan satır yok (Elasticsearch yoklaması).` };
    case 'unknown':
      return { text: 'ilk görülme ölçülemedi', tone: 'muted', title: `${lookbackDays} günlük yoklama zaman aşımına uğradı ya da kısmi döndü; "ilk kez" denemez.` };
    default:
      return null;
  }
}

// insightsNotes — SAF: eğilim / ilk görülme zarfının düz Türkçe notları.
// Kısmi ve hata AYRI söylenir ("yok" diye sunulmaz); sorun yoksa boş.
export function insightsNotes(m: LogPatternInsights | undefined): { text: string; warn: boolean }[] {
  if (!m) return [];
  const out: { text: string; warn: boolean }[] = [];
  switch (m.trend) {
    case 'unsupported': out.push({ text: 'eğilim bu log arka ucunda yok', warn: false }); break;
    case 'error': out.push({ text: 'eğilim alınamadı', warn: true }); break;
    case 'partial': out.push({ text: 'eğilim kısmi (zaman aşımı ya da shard hatası)', warn: true }); break;
  }
  switch (m.firstSeen) {
    case 'no-service': out.push({ text: 'ilk görülme ölçülmedi: tek servis seçin', warn: false }); break;
    case 'no-baseline': out.push({ text: 'ilk görülme için "Önceki pencere"yi açın', warn: false }); break;
    case 'error': out.push({ text: 'ilk görülme yoklaması alınamadı', warn: true }); break;
    case 'partial': out.push({ text: 'ilk görülme yoklaması kısmi', warn: true }); break;
  }
  return out;
}
