import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.711 — Traces Pager'ı: kesin son sayfa yoksa "Last ⇥" sırayı tersine
// çevirip sayfa 0'a döner; ters sıradayken "⇤ First". Kaynak pini.
const src = readFileSync(resolve(__dirname, './Traces.tsx'), 'utf8');

describe('Traces Pager sona git', () => {
  it('onEnd sırayı çevirir ve sayfayı sıfırlar; etiket sıraya göre', () => {
    expect(src).toContain('onEnd={() => {');
    expect(src).toContain("endLabel={order === 'desc' ? 'Last ⇥' : '⇤ First'}");
    expect(src).toContain('setPage(0);');
    expect(src).toMatch(/order === 'desc' \? 'asc' : 'desc'/);
    // lastReachablePage hâlâ verilir: kesin+ulaşılabilir son sayfa varsa o kazanır.
    expect(src).toContain('lastReachablePage={lastReachablePage(countRes?.value');
  });
});
