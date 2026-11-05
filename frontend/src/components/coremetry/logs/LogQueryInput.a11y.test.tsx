// @vitest-environment jsdom
//
// LogQueryInput a11y + klavye sözleşmesi — v0.10.1821 (Log Search Faz 1 dilim
// 1.5). combobox ↔ listbox bağı (aria-controls / -expanded /
// -activedescendant), tek listede Değerler / Alanlar / Geçmiş, ↑↓ / Tab /
// Enter / Esc, odak her seçimden sonra kutuda kalır, `/` hedefi işaretli,
// dil rozeti aria-pressed, konumlu hata satırı role=alert.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { LogQueryInput } from './LogQueryInput';
import { LogQueryErrorLine } from './LogQueryErrorLine';

const m = vi.hoisted(() => ({ fv: [] as { field: string; q: string; signal?: AbortSignal }[] }));
vi.mock('@/lib/api', () => ({
  api: {
    logsFieldValues: (field: string, q: string, _limit: number, _since: string | undefined, signal?: AbortSignal) => {
      m.fv.push({ field, q, signal });
      return Promise.resolve({ values: ['ERROR', 'WARN'] });
    },
  },
  isCanceled: () => false,
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
Element.prototype.scrollIntoView = function scrollIntoView() {};

let root: Root | null = null;
let host: HTMLDivElement | null = null;
beforeEach(() => { m.fv = []; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; vi.useRealTimers(); });

const onSubmit = vi.fn();
const onToggleLang = vi.fn();
const onLint = vi.fn();
function Harness({ initial = '', forced = false }: { initial?: string; forced?: boolean }) {
  const [v, setV] = useState(initial);
  return (
    <LogQueryInput value={v} onChange={setV} onSubmit={onSubmit} fields={['level', 'log.level', 'service.name']}
      history={['level:error and service.name:orders-api', 'payments-mobile timeout']}
      lang={forced ? 'lucene' : 'kql'} forcedLucene={forced} onToggleLang={onToggleLang} onLint={onLint}
      placeholder="Ara…" />
  );
}
function mount(el: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(el); });
  return host;
}
const input = () => host!.querySelector('input[role="combobox"]') as HTMLInputElement;
const type = (s: string) => act(() => {
  const el = input();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(el, s);
  el.setSelectionRange(s.length, s.length);
  el.dispatchEvent(new Event('input', { bubbles: true }));
});
const key = (k: string) => act(() => { input().dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true })); });
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

describe('LogQueryInput — a11y + klavye', () => {
  it('combobox ↔ listbox bağı; kapalıyken aria-expanded=false, `/` hedefi işaretli', () => {
    mount(<Harness />);
    const el = input();
    expect(el.getAttribute('aria-expanded')).toBe('false');
    expect(el.hasAttribute('data-shortcut-search')).toBe(true);
    const list = document.getElementById(el.getAttribute('aria-controls')!)!;
    expect(list).not.toBeNull();
    expect(list.getAttribute('role')).toBe('listbox');
    expect(list.hidden).toBe(true);
  });

  it('boş + odak → Geçmiş; ↓ aktif öğeyi taşır (aria-activedescendant); Esc kapatır, odak kutuda', async () => {
    mount(<Harness />);
    act(() => { input().focus(); });
    await flush();
    const el = input();
    expect(el.getAttribute('aria-expanded')).toBe('true');
    expect(host!.textContent).toContain('Geçmiş');
    const first = el.getAttribute('aria-activedescendant');
    expect(first && document.getElementById(first)?.getAttribute('role')).toBe('option');
    key('ArrowDown');
    await flush();
    const second = input().getAttribute('aria-activedescendant');
    expect(second).not.toBe(first);
    expect(document.getElementById(second!)!.textContent).toBe('payments-mobile timeout');
    key('Escape');
    expect(input().getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(input());
  });

  it('Alanlar: önek → alan önerisi; Enter seçer (aramaz), "alan:" yerleşir, odak kutuda', async () => {
    onSubmit.mockClear();
    mount(<Harness />);
    act(() => { input().focus(); });
    type('lev');
    await flush();
    expect(host!.textContent).toContain('Alanlar');
    key('Enter');
    await flush();
    expect(input().value).toBe('level:');
    expect(onSubmit).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(input());
  });

  it('Değerler: sunucu araması (debounce, iptal sinyali) → Tab tamamlar', async () => {
    mount(<Harness />);
    act(() => { input().focus(); });
    type('level:E');
    await act(async () => { await new Promise(r => setTimeout(r, 220)); });
    expect(m.fv.at(-1)).toMatchObject({ field: 'level', q: 'E' });
    expect(m.fv.at(-1)!.signal).toBeInstanceOf(AbortSignal);
    expect(host!.textContent).toContain('Değerler · level');
    key('Tab');
    await flush();
    expect(input().value).toBe('level:ERROR');
  });

  it('liste kapalıyken Enter arar; kırık sorgu istemcide durur (onLint)', async () => {
    onSubmit.mockClear(); onLint.mockClear();
    mount(<Harness initial="level:error" />);
    key('Enter');
    expect(onSubmit).toHaveBeenCalledTimes(1);
    mount(<Harness initial="(level:error OR" />);
    onSubmit.mockClear();
    act(() => { (host!.querySelectorAll('input[role="combobox"]')[0] as HTMLInputElement).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })); });
    expect(onSubmit).not.toHaveBeenCalled();
    expect(onLint).toHaveBeenLastCalledWith(expect.stringMatching(/./));
  });

  it('dil rozeti: KQL / Lucene metni, aria-pressed = zorla Lucene, tık → onToggleLang', () => {
    onToggleLang.mockClear();
    mount(<Harness forced />);
    const b = host!.querySelector('[data-testid="log-query-lang"]') as HTMLButtonElement;
    expect(b.textContent).toBe('Lucene');
    expect(b.getAttribute('aria-pressed')).toBe('true');
    act(() => { b.click(); });
    expect(onToggleLang).toHaveBeenCalledTimes(1);
  });
});

describe('LogQueryErrorLine', () => {
  it('konumlu sunucu reddi: role=alert, "konum N", ^ doğru sütunda, çalıştırılmadı notu', () => {
    const h = mount(<LogQueryErrorLine query="level:error AND (foo" error={{ error: '( kapatılmamış. Sonuna ) ekleyin.', pos: 16, stage: 'precheck', detail: 'sorgu sözdizimi (konum 16): x' }} />);
    const a = h.querySelector('[role="alert"]')!;
    expect(a.textContent).toContain('Sorgu sözdizimi (konum 17):');
    expect(a.textContent).toContain('( kapatılmamış. Sonuna ) ekleyin. Sorgu çalıştırılmadı.');
    const pre = a.querySelector('pre')!;
    expect(pre.textContent!.split('\n')[1]).toBe(' '.repeat(16) + '^');
  });
  it('hata yoksa hiçbir şey çizmez', () => {
    const h = mount(<LogQueryErrorLine />);
    expect(h.innerHTML).toBe('');
  });
});

// v0.9.971 sözleşmesi (eski KqlSearchInput.contract.test.tsx'ten taşındı):
// `level:` → `level` (iki noktayı geri silmek) alan listesini AÇAR; değer
// konumunda alan adı önerilmez (iki kaynak çakışmaz).
describe('LogQueryInput — v0.9.971 alan listesi sözleşmesi', () => {
  const options = () => [...host!.querySelectorAll('[role="option"][data-kind="field"]')].map(o => o.textContent);
  it('düz önekte açılır; iki nokta geri silinince de açılır', async () => {
    mount(<Harness />);
    act(() => { input().focus(); });
    type('lev');
    await flush();
    expect(options()).toContain('level');
    type('level:');
    await flush();
    type('level');
    await flush();
    expect(options()).toContain('level');
  });
  it('değer konumunda alan adı önerilmez', async () => {
    mount(<Harness />);
    act(() => { input().focus(); });
    type('level:er');
    await flush();
    expect(options()).not.toContain('level');
  });
});
