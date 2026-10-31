// cosreLookA.pin — v0.10.1729 (CoSRE v2 görünüm turu A, operatör onayı:
// mockup "CoSRE sohbet shadcn"). Pinler: "↓ En alta" ortada küçük hap
// (cosre-jump-pill), turun CSS'i YALNIZ [data-cosre-v2] altında (v1 çekmecesi
// de .cosre-root--drawer taşır, dokunulmaz), Temizle hayalet düğme, kapsam
// rozeti sert kenarsız, Girdiler · Sorgu · Ham çıktı tek satır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const SRC = resolve(__dirname, '..', '..');
const read = (p: string) => readFileSync(resolve(SRC, p), 'utf8');

function lookBlock(css: string, marker: string): string {
  const i = css.indexOf(marker);
  expect(i).toBeGreaterThan(-1);
  const rest = css.slice(css.indexOf('*/', i) + 2);
  const end = rest.indexOf('/* ──');
  return end > 0 ? rest.slice(0, end) : rest;
}

describe('CoSRE v2 görünüm turu A (1729)', () => {
  const css = read('styles/globals.css');
  const block = lookBlock(css, 'v0.10.1729 — CoSRE v2 çekmecesi görünüm turu A');

  it('turun her kuralı [data-cosre-v2] kapsamında (v1 dokunulmaz)', () => {
    const body = block.replace(/\/\*[\s\S]*?\*\//g, '');
    const selectors = [...body.matchAll(/([^{}]+)\{/g)].map(m => m[1].trim()).filter(Boolean);
    expect(selectors.length).toBeGreaterThan(5);
    for (const sel of selectors) for (const one of sel.split(',')) expect(one.trim().startsWith('[data-cosre-v2]')).toBe(true);
    expect(body).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
  });

  // v0.10.1740 (operator-reported) — KARAR bloğu 1729'da sol çizgi + --muted
  // dolguya çekilmişti, sayfa yüzeyinden ayırt edilmiyordu ("düz beyaz").
  // v2 1729 öncesi mavi çağrı kutusunu temel kuraldan alır; tur bloğu ezmez.
  it('KARAR: v2 ezmez, temel --accent tonlu çağrı kutusu', () => {
    const body = block.replace(/\/\*[\s\S]*?\*\//g, '');
    expect(body).not.toMatch(/\.cx-verdict/);
    expect(css).not.toMatch(/\[data-cosre-v2\][^{]*\.cx-verdict/);
    expect(css).toMatch(/\n\.cx-verdict \{[^}]*border-left: 3px solid var\(--accent\);[^}]*background: color-mix\(in srgb, var\(--accent\) 10%, transparent\)/);
    expect(css).toMatch(/\n\.cx-verdict-k \{[^}]*text-transform: uppercase;[^}]*color: var\(--accent2\)/);
  });

  it('↓ En alta: cosre-jump-pill sınıfı + ortada hap kuralı', () => {
    expect(read('components/cosre/conversation/CosreChatV2.tsx')).toMatch(/className="cm-jump cosre-jump-pill/);
    expect(block).toMatch(/\.cosre-jump-pill \{[^}]*border-radius: 999px[^}]*box-shadow/);
    expect(css).toMatch(/\.cm-jump \{[^}]*left: 50%[^}]*translateX\(-50%\)/);
  });

  it('Temizle hayalet; kapsam rozeti outline değil', () => {
    expect(read('components/cosre/conversation/CosreChatV2.tsx')).toMatch(/variant="ghost" size="sm" className="cm-head-btn" onClick=\{clear\}/);
    const badge = read('components/cosre/evidence/ScopeBadge.tsx');
    expect(badge).not.toContain('variant="outline"');
    expect(badge).toContain('bg-status-ok-bg');
  });

  it('adım kartı: Girdiler · Sorgu · Ham çıktı tek satır', () => {
    const card = read('components/cosre/evidence/StepCard.tsx');
    const row = card.slice(card.indexOf('data-cosre-step-io'));
    expect(row.indexOf('data-cosre-inputs')).toBeGreaterThan(-1);
    expect(row.indexOf('<QueryBlock')).toBeGreaterThan(row.indexOf('data-cosre-inputs'));
    expect(row.indexOf('<RawOutput')).toBeGreaterThan(row.indexOf('<QueryBlock'));
  });
});
