// cosrePageLook.pin — v0.10.1786 (CoSRE v2 tam sayfası, mockup A, operatör
// onayı 2026-11-05). Pinler: sayfa düzeni CSS'i YALNIZ
// [data-cosre-v2].cosre-root--page kapsamında (v1 sayfası data-cosre-v2
// taşımaz, v2 Sheet'i .cosre-root--drawer); 620px okuma kolonu mesajlar,
// çipler ve composer için aynı; composer yuvarlak 18px; renk yalnız token;
// üst şerit sticky değil.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const SRC = resolve(__dirname, '..', '..', '..');
const read = (p: string) => readFileSync(resolve(SRC, p), 'utf8');

function block(css: string, marker: string): string {
  const i = css.indexOf(marker);
  expect(i).toBeGreaterThan(-1);
  const rest = css.slice(css.indexOf('*/', i) + 2);
  const end = rest.indexOf('/* ──');
  return (end > 0 ? rest.slice(0, end) : rest).replace(/\/\*[\s\S]*?\*\//g, '');
}

describe('CoSRE v2 tam sayfası düzeni (1786)', () => {
  const css = read('styles/globals.css');
  const body = block(css, 'v0.10.1786 — CoSRE v2 TAM SAYFASI düzeni');

  it('her kural [data-cosre-v2].cosre-root--page kapsamında; hex/rgba yok', () => {
    const selectors = [...body.matchAll(/([^{}]+)\{/g)].map(m => m[1].trim()).filter(Boolean);
    expect(selectors.length).toBeGreaterThan(4);
    for (const sel of selectors) for (const one of sel.split(',')) {
      expect(one.trim().startsWith('[data-cosre-v2].cosre-root--page'), one).toBe(true);
    }
    expect(body).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
    expect(body).not.toMatch(/position:\s*(sticky|fixed)/);
  });

  it('620px kolon: mesajlar + çipler + composer aynı genişlikte ortalı; composer 18px', () => {
    expect(body).toMatch(/--cosre-page-col: 620px/);
    const col = body.slice(body.indexOf('.cm-thread__col'), body.indexOf('max-width: var(--cosre-page-col)'));
    for (const c of ['.cm-thread__col', '.cm-followups', '.cm-composer__box', '.cm-composer__scope']) expect(col).toContain(c);
    expect(body).toMatch(/\.cm-composer__box--card \{ border-radius: 18px; \}/);
    expect(body).toMatch(/\.cm-msg-user \{ border-radius: 18px; \}/);
  });

  it('çekmece turu A/B kuralları sayfaya seçici listesiyle genellenmiş (Sheet kuralı aynen)', () => {
    for (const k of ['.cm-msg-user', '.cosre-jump-pill', '.cm-composer__box--card', '.cm-mode-group']) {
      expect(css).toContain(`[data-cosre-v2] .cosre-root--drawer ${k}`);
      expect(css).toContain(`[data-cosre-v2].cosre-root--page ${k}`);
    }
  });

  it('kök: CosrePageLayout data-cosre-v2 + cosre-root--page taşır; v1 CopilotChat taşımaz', () => {
    expect(read('components/cosre/page/CosrePageLayout.tsx')).toMatch(/className="cosre-page cosre-root cosre-root--page" data-cosre-v2=""/);
    expect(read('components/CopilotChat.tsx')).not.toContain('data-cosre-v2');
  });

  // v0.10.1787 — adımlar katlı tek satır YALNIZ sayfada; Sheet eski özet başlığını korur.
  it('adımlar: compactSteps yalnız sayfa kipinde (isPage)', () => {
    const chat = read('components/cosre/conversation/CosreChatV2.tsx');
    expect(chat).toMatch(/compactSteps=\{isPage \|\| undefined\}/);
    expect(chat.match(/compactSteps=/g)).toHaveLength(1);
  });
});
