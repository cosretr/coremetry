import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// tailwindSources.pin — v0.10.1613 (shadcn Faz 1 R5; operatör kararı).
// Tailwind depoyu kendiliğinden TARAMAZ (`source(none)`, R4): taransaydı eski
// sınıf adlarından (`hidden`, `table`, `border`, `sticky` …) legacy katmanını
// yenen yardımcılar üretirdi ve geçirilmemiş sayfalar piksel değiştirirdi.
// Taranan dizinler TAM OLARAK üç: üretilen shadcn, CoSRE v2 ai-elements ve
// coremetry bileşikleri. Başka bir yol (pages/, components/ui …) = kırmızı.
const css = readFileSync(resolve(__dirname, 'index.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

describe('Tailwind kaynak listesi kilitli', () => {
  it('@source satırları tam olarak üç dizin, bu sırayla', () => {
    const sources = [...css.matchAll(/@source\s+([^;]+);/g)].map(m => m[1].trim());
    expect(sources).toEqual([
      "'../components/shadcn'",
      "'../components/ai-elements'",
      "'../components/coremetry'",
    ]);
  });

  it('otomatik tarama kapalı: utilities source(none), başka source() yok', () => {
    expect(css).toContain("@import 'tailwindcss/utilities.css' layer(utilities) source(none);");
    expect(css.match(/source\(/g)?.length).toBe(1);
    expect(css).not.toMatch(/@source\s+not\b|@source\s+inline\(/);
  });
});
