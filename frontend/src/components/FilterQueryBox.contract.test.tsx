// @vitest-environment jsdom
//
// FilterQueryBox.contract.test.tsx — v0.10.264 (Traces "Add filter" Öneri A).
// jsdom sözleşmesi, @testing-library yok. Kapsam: çipler üç parçalı ve × kaldırır;
// satır içi "anahtar=değer" + Enter filtre ekler ve kutu boşalır; Backspace boş
// kutuda son çipi düzenlemeye alır; anahtar adımı önerileri gözlem sayısıyla
// sıralar; op adımında ∃ değer istemeden ekler. Ağ: api/attribute keşfi mock.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router';

vi.mock('@/lib/api', () => ({
  api: {
    attributeValues: vi.fn(async () => [{ value: 'TRN_TRANSFER_EFT', count: 48200 }, { value: 'TRN_CARD', count: 6000 }]),
    metricAttrKeys: vi.fn(async () => ['pod', 'namespace']),
    metricLabels: vi.fn(async () => ['api-a', 'api-b']),
  },
}));
vi.mock('@/lib/useAttributeKeys', () => ({
  useAttributeKeys: () => ({ keys: ['http.route', 'http.method', 'channel_code'], observed: [{ key: 'channel_code', count: 900 }, { key: 'http.route', count: 800 }] }),
}));

import { FilterQueryBox } from './FilterQueryBox';
import { topEscLayer } from '@/lib/escLayer';
import type { FilterExpr } from '@/lib/types';
// v0.10.2317 — Türkçe metin pinleri: arayüz dili 'tr'.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

function setInput(el: HTMLInputElement, text: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  setter.call(el, text);
  el.dispatchEvent(new Event('input', { bubbles: true }));
}
function key(el: HTMLElement, k: string) {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
}

const two: FilterExpr[] = [
  { k: 'http.route', op: '=', v: ['/api/v1/accounts/{id}/balance'] },
  { k: 'channel_code', op: 'IN', v: ['MOBILE', 'WEB'] },
];

describe('FilterQueryBox', () => {
  it('çipler üç parçalı; × ilgili filtreyi kaldırır', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={two} onChange={onChange} />);
    const chips = el.querySelectorAll('.fq-chip');
    expect(chips.length).toBe(2);
    expect(chips[1].querySelector('.fq-k')!.textContent).toBe('channel_code');
    expect(chips[1].querySelector('.fq-o')!.textContent).toBe('IN');
    expect(chips[1].querySelector('.fq-v')!.textContent).toContain('MOBILE, WEB');
    act(() => { (chips[0].querySelector('.fq-x') as HTMLButtonElement).click(); });
    expect(onChange).toHaveBeenCalledWith([two[1]]);
  });

  it('satır içi anahtar=değer + Enter filtre ekler, kutu boşalır', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={[]} onChange={onChange} />);
    const input = el.querySelector('input.fq-input') as HTMLInputElement;
    act(() => { setInput(input, 'kind=server'); });
    expect(el.querySelector('.fq-pop')).not.toBeNull();
    act(() => { key(input, 'Enter'); });
    expect(onChange).toHaveBeenCalledWith([{ k: 'kind', op: '=', v: ['server'] }]);
    expect((el.querySelector('input.fq-input') as HTMLInputElement).value).toBe('');
    expect(el.querySelector('.fq-pop')).toBeNull();
  });

  it('Backspace boş kutuda son çipi düzenlemeye alır', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={two} onChange={onChange} />);
    const input = el.querySelector('input.fq-input') as HTMLInputElement;
    act(() => { key(input, 'Backspace'); });
    expect(onChange).toHaveBeenCalledWith([two[0]]);
    expect((el.querySelector('input.fq-input') as HTMLInputElement).value).toBe('MOBILE, WEB');
    expect(el.querySelector('.fq-steps .on')!.textContent).toContain('Değer');
  });

  it('anahtar önerileri gözlem sayısıyla sıralı; Tab op adımına geçer; ∃ değersiz ekler', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={[]} onChange={onChange} />);
    const input = el.querySelector('input.fq-input') as HTMLInputElement;
    act(() => { setInput(input, 'http'); });
    const names = Array.from(el.querySelectorAll('.fq-opt .name')).map(n => n.textContent);
    expect(names[0]).toBe('http.route'); // 800 gözlem > http.method (0)
    act(() => { key(input, 'Tab'); });
    expect(el.querySelector('.fq-steps .on')!.textContent).toContain('Op');
    act(() => { setInput(input, 'exists'); });
    act(() => { key(input, 'Enter'); });
    expect(onChange).toHaveBeenCalledWith([{ k: 'http.route', op: 'EXISTS', v: [] }]);
  });

  // v0.10.927 — op seçenekleri anahtar/değer kardeşleriyle aynı desen:
  // role=option <div> (ham <button> değil), id = girdinin activedescendant'ı,
  // vurgulu olan `.fq-op.sel`; fareyle seçim onMouseDown'da, odak girdide.
  it('op seçenekleri role=option div (button değil); mousedown seçer', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={[]} onChange={onChange} />);
    const input = el.querySelector('input.fq-input') as HTMLInputElement;
    act(() => { setInput(input, 'http'); });
    act(() => { key(input, 'Tab'); });
    const ops = Array.from(el.querySelectorAll<HTMLElement>('.fq-ops .fq-op'));
    expect(ops.length).toBeGreaterThan(1);
    expect(el.querySelector('.fq-list button')).toBeNull();
    for (const o of ops) {
      expect(o.tagName).toBe('DIV');
      expect(o.getAttribute('role')).toBe('option');
    }
    expect(ops[0].classList.contains('sel')).toBe(true);
    expect(ops[0].getAttribute('aria-selected')).toBe('true');
    expect(input.getAttribute('aria-activedescendant')).toBe(ops[0].id);
    act(() => { key(input, 'ArrowDown'); });
    const ops2 = Array.from(el.querySelectorAll<HTMLElement>('.fq-ops .fq-op'));
    expect(ops2[1].classList.contains('sel')).toBe(true);
    expect(ops2[0].classList.contains('sel')).toBe(false);
    expect(input.getAttribute('aria-activedescendant')).toBe(ops2[1].id);
    const exists = ops2.find(o => o.getAttribute('title') === 'EXISTS')!;
    const md = new MouseEvent('mousedown', { bubbles: true, cancelable: true });
    act(() => { exists.dispatchEvent(md); });
    expect(md.defaultPrevented).toBe(true);
    expect(onChange).toHaveBeenCalledWith([{ k: 'http.route', op: 'EXISTS', v: [] }]);
  });

  // v0.10.1160 — Operator-reported: op'u değiştirmek için çipi kaldırıp yeniden
  // eklemek gerekiyordu. Op'a tık op listesini açar; değer korunur.
  it('çipin op\'una tık op listesini açar; seçilen op değeri koruyarak yerinde yazılır', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={two} onChange={onChange} />);
    const pick = (chip: number, title: string) => {
      act(() => { (el.querySelectorAll('.fq-chip .fq-o')[chip] as HTMLElement).click(); });
      const opt = Array.from(el.querySelectorAll('.fq-ops [role="option"]')).find(o => o.getAttribute('title') === title) as HTMLElement;
      act(() => { opt.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); });
    };
    pick(0, '!=');
    expect(onChange).toHaveBeenLastCalledWith([{ ...two[0], op: '!=' }, two[1]]);
    pick(1, 'NOT IN');
    expect(onChange).toHaveBeenLastCalledWith([two[0], { ...two[1], op: 'NOT IN' }]);
    // Liste → tek değerli op: sessizce değer atmaz, değer adımını eldeki metinle açar.
    onChange.mockClear();
    pick(1, '=');
    expect(onChange).not.toHaveBeenCalled();
    expect((el.querySelector('.fq-input') as HTMLInputElement).value).toBe('MOBILE, WEB');
  });

  // v0.10.2203 — Operator-reported: düzenlenen çipin yanında ikinci bir
  // "name =" çipi açılıyordu. Düzenleme yerinde: sayı ve konum korunur.
  it('değere tık çipi KENDİ konumunda düzenleyiciye çevirir; ikinci çip yok; Enter yerinde değiştirir (tek onChange)', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={two} onChange={onChange} />);
    act(() => { (el.querySelectorAll('.fq-chip .fq-v')[0] as HTMLElement).click(); });
    const chips = el.querySelectorAll('.fq-bar > .fq-chip');
    expect(chips.length).toBe(2);
    expect(el.querySelector('.fq-chip.is-draft')).toBeNull();
    expect(chips[0].classList.contains('is-editing')).toBe(true);
    const ed = chips[0].querySelector('input.fq-edit') as HTMLInputElement;
    expect(ed).not.toBeNull();
    expect(ed.value).toBe('/api/v1/accounts/{id}/balance');
    expect(chips[1].querySelector('.fq-k')!.textContent).toBe('channel_code');
    expect(el.querySelector('.fq-steps .on')!.textContent).toContain('Değer');
    act(() => { setInput(ed, '/health'); });
    act(() => { key(ed, 'Enter'); });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith([{ ...two[0], v: ['/health'] }, two[1]]);
  });

  it('Esc düzenlemeden vazgeçer: çip aynen geri gelir, onChange yok', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={two} onChange={onChange} />);
    act(() => { (el.querySelectorAll('.fq-chip .fq-v')[1] as HTMLElement).click(); });
    const ed = el.querySelector('input.fq-edit') as HTMLInputElement;
    act(() => { setInput(ed, 'X'); });
    // Tek document dinleyicisi keyboard.ts'te (testte kurulu değil): en üst Esc katmanı çağrılır.
    act(() => { topEscLayer()?.(); });
    expect(el.querySelector('input.fq-edit')).toBeNull();
    const chips = el.querySelectorAll('.fq-bar > .fq-chip');
    expect(chips.length).toBe(2);
    expect(chips[1].querySelector('.fq-v')!.textContent).toContain('MOBILE, WEB');
    expect(onChange).not.toHaveBeenCalled();
  });

  it('anahtara tık anahtar adımını yerinde açar; Enter yeni anahtarı op + değerle aynı konuma yazar', () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={two} onChange={onChange} />);
    act(() => { (el.querySelectorAll('.fq-chip .fq-k')[1] as HTMLElement).click(); });
    expect(el.querySelector('.fq-steps .on')!.textContent).toContain('Anahtar');
    const ed = el.querySelector('.fq-chip.is-editing input.fq-edit') as HTMLInputElement;
    expect(ed.value).toBe('channel_code');
    act(() => { setInput(ed, 'http.method'); });
    act(() => { key(ed, 'Enter'); });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith([two[0], { k: 'http.method', op: 'IN', v: ['MOBILE', 'WEB'] }]);
  });

  it('metrik modu (v0.10.270): anahtarlar metricAttrKeys, değerler metricLabels, sayım çubuğu yok', async () => {
    const onChange = vi.fn();
    const el = render(<FilterQueryBox value={[]} onChange={onChange} metricName="http_requests_total" metricService="api" />);
    await act(async () => { await Promise.resolve(); });
    const input = el.querySelector('input.fq-input') as HTMLInputElement;
    act(() => { setInput(input, 'po'); });
    const names = Array.from(el.querySelectorAll('.fq-opt .name')).map(n => n.textContent);
    expect(names).toEqual(['pod']);
    act(() => { key(input, 'Tab'); });
    act(() => { key(input, 'Enter'); }); // op = varsayılan '='
    await act(async () => { await new Promise(r => setTimeout(r, 250)); });
    const vals = Array.from(el.querySelectorAll('.fq-opt .name')).map(n => n.textContent);
    expect(vals).toEqual(['api-a', 'api-b']);
    expect(el.querySelector('.fq-opt .cnt')).toBeNull();
    act(() => { key(input, 'Enter'); });
    expect(onChange).toHaveBeenCalledWith([{ k: 'pod', op: '=', v: ['api-a'] }]);
  });
});
