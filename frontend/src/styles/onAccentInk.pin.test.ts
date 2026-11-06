import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from './zLayers.test';

// onAccentInk.pin — v0.10.1848 (docs/frontend/themes-proposal.md §3 tuzak,
// §5 adım 4). Dolu vurgu / tehlike zemini üstündeki yazı rol tokenından
// gelir: `--on-accent` (shadcn `text-primary-foreground` /
// `text-destructive-foreground`). Sabit beyaz, Yüksek Kontrast temasında
// (`--on-accent` siyah, açık mavi vurgu) okunmaz olur.
// Ölçüm: tokens.css Tailwind paletini sıfırlar (`--color-*: initial`), yani
// shadcn'in `text-white`'ı HİÇ CSS üretmiyordu — tehlike düğmesi/rozeti yazı
// rengini atadan alıyordu (açık temada koyu metin kırmızı dolguda).
const SRC = resolve(__dirname, '..');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(p) && !p.includes('.' + 'test' + '.')) out.push(p);
  }
  return out;
}

describe('dolgu üstü yazı rol tokenından (v0.10.1848)', () => {
  const files = walk(SRC).map(p => ({ rel: p.slice(SRC.length + 1), src: stripTsComments(readFileSync(p, 'utf8')) }));

  it('Tailwind palet yazı sınıfı yok (text-white / text-black CSS üretmez)', () => {
    const hits = files.filter(f => /(?<![\w-])text-(white|black)(?![\w-])/.test(f.src)).map(f => f.rel);
    expect(hits).toEqual([]);
  });

  it('satır içi sabit beyaz yazı rengi yok (color: #fff / white)', () => {
    const hits = files.filter(f => /\bcolor:\s*['"](#fff|#ffffff|white)['"]/i.test(f.src)).map(f => f.rel);
    expect(hits).toEqual([]);
  });

  it('shadcn tehlike düğmesi ve rozeti text-destructive-foreground; eşleme --on-accent', () => {
    for (const f of ['components/shadcn/button.tsx', 'components/shadcn/badge.tsx']) {
      const src = readFileSync(resolve(SRC, f), 'utf8');
      expect(src, f).toMatch(/destructive:\s*"bg-destructive text-destructive-foreground /);
    }
    const tokens = readFileSync(resolve(__dirname, 'tokens.css'), 'utf8');
    expect(tokens).toContain('--destructive-foreground: var(--on-accent);');
    expect(tokens).toContain('--primary-foreground: var(--on-accent);');
    expect(tokens).toContain('--color-destructive-foreground: var(--destructive-foreground);');
  });

  it('kenar çubuğu avatar harfi vurgu dolgusunda --on-accent', () => {
    const sb = readFileSync(resolve(SRC, 'components/Sidebar.tsx'), 'utf8');
    expect(sb).toContain("background: 'var(--accent)', color: 'var(--on-accent)',");
  });
});
