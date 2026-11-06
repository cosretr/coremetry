// mlSingleSource.pin.test.ts — v0.10.1843 (öğrenme döngüsü Faz 2 dilim 2.3,
// audit docs/anomaly/learning-loop-audit.md §7.3 "tek kaynak testi").
//
// ML olayının Türkçe anlatımı TEK yerde kurulur: sunucudaki
// ml.BuildExplainCard / ml.ExplainSummary (internal/ml/explain_card.go).
// FE yalnız `card` / `summary` alanlarını gösterir. Bu test, ML'e dokunan
// FE kaynaklarında (yorumlar ayıklanmış) ikinci bir cümle kurucunun izlerini
// arar: silinen üretici adları ve oran / süre cümlesi parçaları. Kapsam ML
// tipini ya da ML bileşenini anan her .ts/.tsx (testler hariç) — yeni bir
// dosya da yakalanır.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { stripTsComments } from '@/styles/zLayers.test';

const SRC = resolve(__dirname, '..', '..');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !/\.test\.tsx?$/.test(p)) out.push(p);
  }
  return out;
}

/** ML anlatımına dokunan kaynak mı (tip, uç ya da bileşen adı). */
export const ML_SCOPE = /\bML(Event|Snapshot|Replay)\b|\bExplainCard\b|lib\/mlReplay|anomalies\/ML[A-Z]/;
/** Silinen FE üreticileri — geri gelirse kırmızı. */
export const BANNED_NAMES = /\b(mlHeadline|mlSummary|mlStatusLine|mlWhatChanged|mlValuePair|snapNarrative)\b/;
/** Oran / süre cümlesi parçaları (sunucu sözlüğünün dili). */
export const SENTENCE_BITS = /normalin|\bkatı\b|kattan|katından|dakikadır|saattir|gündür|'(?:dan|den|tan|ten) beri|servisinde|servisine|normalden yaklaşık|normalde hiç/;

const files = walk(SRC).map(p => ({ rel: relative(SRC, p).split(sep).join('/'), code: stripTsComments(readFileSync(p, 'utf8')) }));
const scoped = files.filter(f => ML_SCOPE.test(f.code) || /^components\/coremetry\/anomalies\/ML/.test(f.rel) || f.rel === 'lib/mlReplay.ts');

describe('ML anlatımı tek kaynak (sunucu ExplainCard)', () => {
  it('kapsam boş değil (ölçüm gerçekten koşuyor)', () => {
    expect(scoped.map(f => f.rel)).toEqual(expect.arrayContaining([
      'lib/mlReplay.ts', 'components/coremetry/anomalies/MLTab.tsx', 'components/coremetry/anomalies/MLReplaySheet.tsx',
    ]));
  });
  it('negatif kontrol: eşleyiciler ikinci bir kurucuyu yakalar', () => {
    expect(SENTENCE_BITS.test('`${svc}: hata logları normalin yaklaşık ${n} katı`')).toBe(true);
    expect(SENTENCE_BITS.test("`${m} dakikadır (${t}'dan beri)`")).toBe(true);
    expect(BANNED_NAMES.test('export function mlHeadline(n) {}')).toBe(true);
  });
  it('silinen üretici adları hiçbir kaynakta yok', () => {
    expect(files.filter(f => BANNED_NAMES.test(f.code)).map(f => f.rel)).toEqual([]);
  });
  it('ML kaynaklarında oran / süre cümlesi kurulmaz', () => {
    expect(scoped.filter(f => SENTENCE_BITS.test(f.code)).map(f => f.rel)).toEqual([]);
  });
});
