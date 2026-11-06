// @vitest-environment jsdom
// buttonStateSliceB — v0.10.1342 (K3 dilim B: durum ≠ birincil).
//
// Ne çiviliyor: buttonStateRatchet'in son 10 koşullu-birincil sitesi göçtü
// (tavan 0). Kaynak taraması "üçlü yok"u söyler; burada seçilen kalıbın
// DAVRANIŞI çivileniyor:
//   • FilterGroupBuilder AND|OR → SegmentedControl (radiogroup, aria-checked,
//     seçim onChange'e gider) — eskiden seçili birleştirici dolu birincildi.
//   • Metrics editör anahtarı + Explore Repeats preset'leri → ikincil +
//     `active` (Button `active` → aria-pressed + .is-active; Button.contract
//     ölçüyor). Tam sayfayı jsdom'da kurmak yerine etiket düzeyinde: aynı
//     <Button> hem `variant="secondary"` hem `active=` taşıyor.
//   • AdminClickhouse onay ağırlığı → CONFIRM_VARIANT tablosu (apply dolu
//     birincil, rollback danger — görsel sonuç değişmedi).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { jsxOpenTags } from '@/styles/jsxTags';
import { Button } from '@/components/ui/Button';
import type { FilterGroup } from '@/lib/types';

// FilterBuilder sunucu-tarafı otomatik tamamlama taşır; burada yalnız
// birleştirici anahtarı ölçülüyor.
vi.mock('./FilterBuilder', () => ({ FilterBuilder: () => null }));

import { FilterGroupBuilder } from './FilterGroupBuilder';

const SRC = resolve(__dirname, '..');
const read = (rel: string) => readFileSync(resolve(SRC, rel), 'utf8');

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(node: React.ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('v0.10.1342 — FilterGroupBuilder AND|OR tek seçim (SegmentedControl)', () => {
  it('radiogroup + aria-checked; dolu birincil buton yok; seçim onChange ile', () => {
    const onChange = vi.fn();
    const value: FilterGroup = { join: 'AND', filters: [] };
    const el = mount(<FilterGroupBuilder value={value} onChange={onChange} />);
    const groups = el.querySelectorAll('[role="radiogroup"][aria-label="Join operator"]');
    expect(groups.length).toBe(1);
    const radios = [...groups[0].querySelectorAll('button[role="radio"]')] as HTMLButtonElement[];
    expect(radios.map(r => r.textContent)).toEqual(['AND', 'OR']);
    expect(radios.map(r => r.getAttribute('aria-checked'))).toEqual(['true', 'false']);
    // Eski kalıp: seçili birleştirici sınıfsız (= primary) <Button> + aria-pressed.
    expect(el.querySelector('[aria-pressed]')).toBeNull();
    act(() => { radios[1].click(); });
    expect(onChange).toHaveBeenCalledWith({ ...value, join: 'OR' });
  });
});

describe('v0.10.1342 — Metrics / Explore anahtarları ikincil + active', () => {
  const tagWith = (rel: string, needle: string) => {
    const tags = jsxOpenTags(read(rel), 'Button').filter(t => t.tag.includes(needle));
    expect(tags.length, `${rel}: ${needle}`).toBe(1);
    return tags[0].tag;
  };

  // v0.10.1863 — Metrics editör anahtarı kalktı (Metrics Explorer: Sorgu/Keşif sekmeleri).
  it('Explore Repeats preset: variant="secondary" active={active}', () => {
    const t = tagWith('pages/Explore.tsx', 'active={active}');
    expect(t).toContain('variant="secondary"');
    expect(t).not.toMatch(/'primary'/);
  });

  it('ikincil + active render: aria-pressed iki hâlde de basılır, `sec` korunur', () => {
    const el = mount(<>
      <Button variant="secondary" active={false} size="sm">off</Button>
      <Button variant="secondary" active size="sm">on</Button>
    </>);
    const [off, on] = [...el.querySelectorAll('button')];
    expect(off.getAttribute('aria-pressed')).toBe('false');
    expect(on.getAttribute('aria-pressed')).toBe('true');
    expect(off.className).toBe('sec sm');
    expect(on.className).toBe('sec sm is-active');
  });
});

describe('v0.10.1342 — AdminClickhouse onay ağırlığı tablodan', () => {
  it('CONFIRM_VARIANT: apply → primary, rollback → danger; iki "Evet/Uygula" sitesi tabloyu okuyor', () => {
    const src = read('pages/AdminClickhouse.tsx');
    expect(src).toMatch(/const CONFIRM_VARIANT: Record<ConfirmKind, 'primary' \| 'danger'> = \{\s*apply: 'primary',\s*rollback: 'danger',\s*\};/);
    expect(src.match(/variant=\{CONFIRM_VARIANT\[/g)?.length).toBe(2);
  });
});
