import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// tailwindSources.pin — v0.10.1613 (shadcn Faz 1 R5; operatör kararı).
// Tailwind depoyu kendiliğinden TARAMAZ (`source(none)`, R4): taransaydı eski
// sınıf adlarından (`hidden`, `table`, `border`, `sticky` …) legacy katmanını
// yenen yardımcılar üretirdi ve geçirilmemiş sayfalar piksel değiştirirdi.
// Taranan dizinler: üretilen shadcn, CoSRE v2 ai-elements, coremetry
// bileşikleri ve (Faz 2b B1) CoSRE v2 sohbet ağacı components/cosre; Faz 2 dilim 1 R1’den beri + geçirilen sayfa dizinleri
// (pages/settings). Başka bir yol (components/ui, geçirilmemiş pages/ …) = kırmızı.
const css = readFileSync(resolve(__dirname, 'index.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

describe('Tailwind kaynak listesi kilitli', () => {
  it('@source satırları: üç bileşik dizini + geçirilen sayfa dizinleri, bu sırayla', () => {
    const sources = [...css.matchAll(/@source\s+([^;]+);/g)].map(m => m[1].trim());
    expect(sources).toEqual([
      "'../components/shadcn'",
      "'../components/ai-elements'",
      "'../components/coremetry'",
      // CoSRE v2 Faz 2b B1: v2 sohbet ağacı (components/cosre).
      "'../components/cosre'",
      // Faz 2 dilim 1 R1 (G11): geçirilen sayfa dizinleri, birer satır.
      "'../pages/settings'",
    ]);
  });

  it('otomatik tarama kapalı: utilities source(none), başka source() yok', () => {
    expect(css).toContain("@import 'tailwindcss/utilities.css' layer(utilities) source(none);");
    expect(css.match(/source\(/g)?.length).toBe(1);
    expect(css).not.toMatch(/@source\s+not\b|@source\s+inline\(/);
  });
});
