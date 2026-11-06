// v0.10.1825 — operatör: komut paletinde (üst arama kutusu) "yine siyah
// çerçeve". Kök neden sınıfı: Tailwind preflight yok → renksiz `border*`
// yardımcıları currentColor. Tek tek yüzey yamaları (1721, 1726) yerine
// legacy katmanının başında sıfır özgüllüklü varsayılan kenar rengi.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const G = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');
const INDEX = readFileSync(resolve(__dirname, 'index.css'), 'utf8');
const RULE = '*, ::before, ::after, ::backdrop, ::file-selector-button { border-color: var(--border); }';

describe('varsayılan kenar rengi (preflight\'ın tek satırı)', () => {
  it('globals.css kuralı taşır ve dosyadaki İLK kuraldır (her legacy kural onu yener)', () => {
    const rules = G.replace(/\/\*[\s\S]*?\*\//g, '').trim();
    expect(rules.startsWith(RULE)).toBe(true);
  });
  it('globals.css legacy katmanında; utilities katmanı sonra gelir (border-<renk> yener)', () => {
    expect(INDEX).toContain("@layer legacy, theme, base, components, utilities;");
    expect(INDEX).toContain("@import './globals.css' layer(legacy);");
  });
  it('üretilmiş shadcn bileşenlerinde hâlâ renksiz kenar var: kural bu yüzden şart', () => {
    const cmd = readFileSync(resolve(__dirname, '..', 'components', 'shadcn', 'command.tsx'), 'utf8');
    expect(cmd).toContain('"flex h-9 items-center gap-2 border-b px-3"');
  });
});
