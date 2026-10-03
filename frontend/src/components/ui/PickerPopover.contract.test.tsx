// @vitest-environment jsdom
//
// PickerPopover.contract.test.tsx — v0.10.1089 (operatör, prod, Services
// "Filter services…" / Traces "Filter by service…": "sanki geçici bir menü
// açılmış gibi hissiyat var, iframe içinde geliyor").
//
// Seçici ailesinin ortak açılır listesi Combobox üzerinden GERÇEK mount ile
// ölçülüyor (seçiciler onu Combobox'tan miras alıyor): body portalı +
// yerleşim (çapa dikdörtgeni taklit), klavye satırı (aria-activedescendant)
// + Enter seçimi, Esc kapanışı, "Son kullanılan" (localStorage taklit +
// atan depolama), "N sonuç" başlığı ve durumlar, uzun adda üç nokta +
// `title`, yatay taşma yokluğu (globals.css'in `.pick-*` kuralları jsdom'a
// enjekte edilerek). Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { Combobox } from '@/components/Combobox';
import { STORAGE_KEYS } from '@/lib/storage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('@/lib/api', () => ({
  api: { serviceNames: vi.fn() },
}));
import { api } from '@/lib/api';
import { ServicePicker } from '@/components/ServicePicker';

let host: HTMLDivElement;
let root: Root;

// Bellek içi localStorage taklidi (ortamın kendi global'i — Node'un deneysel
// `localStorage`ı — dosyasız çalışmıyor; testin gerçek bir depoya dokunmaması
// da zaten istenen).
function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() { return m.size; },
    clear: () => m.clear(),
    getItem: k => (m.has(k) ? m.get(k)! : null),
    key: i => [...m.keys()][i] ?? null,
    removeItem: k => { m.delete(k); },
    setItem: (k, v) => { m.set(k, String(v)); },
  };
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

type CbProps = React.ComponentProps<typeof Combobox>;
let changes: string[] = [];
function Harness(props: Partial<CbProps>) {
  const [v, setV] = useState(props.value ?? '');
  return (
    <Combobox options={['alpha', 'alpine', 'beta']} placeholder="pick…" serverFiltered
      {...props} value={v} onChange={x => { changes.push(x); setV(x); }} />
  );
}
function mount(props: Partial<CbProps> = {}, wrap?: (n: React.ReactNode) => React.ReactNode) {
  changes = [];
  const el = <Harness {...props} />;
  act(() => { root.render(wrap ? wrap(el) : el); });
  return host.querySelector('input') as HTMLInputElement;
}
const pop = () => document.querySelector<HTMLElement>('.pick-pop');
const opts = () => Array.from(document.querySelectorAll<HTMLElement>('.pick-pop [role="option"]'));
function focusIn(el: HTMLElement) {
  act(() => { el.focus(); el.dispatchEvent(new FocusEvent('focusin', { bubbles: true })); });
}
function key(el: HTMLElement, k: string) {
  let consumed = false;
  act(() => { consumed = !el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true })); });
  return consumed;
}
function stubRect(el: Element, r: { left: number; top: number; width: number; height: number }) {
  el.getBoundingClientRect = () => ({
    ...r, right: r.left + r.width, bottom: r.top + r.height, x: r.left, y: r.top, toJSON: () => r,
  }) as DOMRect;
}

describe('PickerPopover — portal + yerleşim', () => {
  it('body’ye portal edilir, çapanın altına sol kenara hizalı konur, en az çapa genişliğinde', () => {
    const input = mount();
    stubRect(host.querySelector('.cb-wrap')!, { left: 40, top: 100, width: 220, height: 30 });
    focusIn(input);
    const p = pop()!;
    expect(p, 'popover açılmadı').toBeTruthy();
    expect(p.parentElement).toBe(document.body);
    expect(host.contains(p), 'liste hâlâ alanın içinde — overflow kırpar').toBe(false);
    expect(p.style.left).toBe('40px');
    expect(p.style.top).toBe('134px');
    expect(p.style.minWidth).toBe('220px');
    expect(p.dataset.side).toBe('bottom');
  });

  it('dipte açılınca üste çevrilir ve viewport içinde kalır', () => {
    const h = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get')
      .mockImplementation(function (this: HTMLElement) { return this.classList.contains('pick-pop') ? 300 : 0; });
    const input = mount();
    stubRect(host.querySelector('.cb-wrap')!, { left: 40, top: window.innerHeight - 40, width: 220, height: 30 });
    focusIn(input);
    const p = pop()!;
    expect(p.dataset.side).toBe('top');
    expect(parseInt(p.style.top, 10) + 300).toBeLessThanOrEqual(window.innerHeight - 40);
    h.mockRestore();
  });

  it('çapa bir diyaloğun içindeyse liste üst rung’a çıkar (data-layer=over)', () => {
    const input = mount({}, n => <div role="dialog">{n}</div>);
    focusIn(input);
    expect(pop()!.dataset.layer).toBe('over');
  });

  it('sayfadaki "dışarı tık" dinleyicileri satır tıklamasını dışarı saymaz', () => {
    const outside = vi.fn();
    document.addEventListener('mousedown', outside);
    const input = mount();
    focusIn(input);
    act(() => { opts()[1].dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); });
    document.removeEventListener('mousedown', outside);
    expect(changes).toEqual(['alpine']);
    expect(outside).not.toHaveBeenCalled();
  });
});

describe('PickerPopover — klavye', () => {
  it('↑/↓ satırı aria-activedescendant ile işaretler, Enter seçer ve kapatır', () => {
    const input = mount({ serverFiltered: false });
    focusIn(input);
    expect(input.getAttribute('role')).toBe('combobox');
    expect(input.getAttribute('aria-expanded')).toBe('true');
    key(input, 'ArrowDown');
    key(input, 'ArrowDown');
    key(input, 'ArrowUp');
    const on = opts()[0];
    expect(input.getAttribute('aria-activedescendant')).toBe(on.id);
    expect(on.getAttribute('aria-selected')).toBe('true');
    expect(on.classList.contains('is-on')).toBe(true);
    expect(key(input, 'Enter')).toBe(true);
    expect(changes).toEqual(['alpha']);
    expect(pop(), 'Enter sonrası liste açık kaldı').toBeNull();
  });

  it('Esc listeyi kapatır (olay tüketilir)', () => {
    const input = mount();
    focusIn(input);
    expect(pop()).toBeTruthy();
    expect(key(input, 'Escape')).toBe(true);
    expect(pop()).toBeNull();
    expect(input.getAttribute('aria-expanded')).toBe('false');
  });
});

describe('PickerPopover — Son kullanılan', () => {
  it('seçim yazılır; alan boşken grup sonuçların üstünde, tekrar etmeden', () => {
    let input = mount({ recentKey: 'service' });
    focusIn(input);
    act(() => { opts()[2].dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); });
    expect(JSON.parse(localStorage.getItem(STORAGE_KEYS.pickerRecents)!)).toEqual({ service: ['beta'] });

    act(() => root.unmount());
    root = createRoot(host);
    input = mount({ recentKey: 'service' });
    focusIn(input);
    expect(document.querySelector('.pick-group')?.textContent).toBe('Son kullanılan');
    expect(opts().map(o => o.textContent)).toEqual(['beta', 'alpha', 'alpine']);
  });

  it('yazınca grup kaybolur (yalnız boş alanda)', () => {
    localStorage.setItem(STORAGE_KEYS.pickerRecents, JSON.stringify({ service: ['beta'] }));
    const input = mount({ recentKey: 'service', value: 'al' });
    focusIn(input);
    expect(document.querySelector('.pick-group')).toBeNull();
  });

  it('depolama ATARSA seçim yine çalışır, grup çıkmaz', () => {
    const boom = () => { throw new Error('QuotaExceededError'); };
    vi.stubGlobal('localStorage', { getItem: boom, setItem: boom, removeItem: boom, clear: boom, key: boom, length: 0 });
    const input = mount({ recentKey: 'service' });
    focusIn(input);
    expect(document.querySelector('.pick-group')).toBeNull();
    act(() => { opts()[0].dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); });
    expect(changes).toEqual(['alpha']);
  });
});

describe('PickerPopover — başlık ve durumlar', () => {
  it('"N sonuç" sunucu toplamını gösterir', () => {
    const input = mount({ resultCount: 1234 });
    focusIn(input);
    expect(document.querySelector('.pick-head')?.textContent).toBe('1.234 sonuç');
  });

  it('istemci süzgecinde eşleşme sayısı; eşleşme yoksa "eşleşme yok"', () => {
    const input = mount({ serverFiltered: false, value: 'alp' });
    focusIn(input);
    expect(document.querySelector('.pick-head')?.textContent).toBe('2 sonuç');
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'zzz');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(document.querySelector('.pick-state')?.textContent).toContain('eşleşme yok');
  });

  it('aranıyor… ve arama başarısız', () => {
    const input = mount({ status: 'loading', options: [] });
    focusIn(input);
    expect(document.querySelector('.pick-head')?.textContent).toBe('aranıyor…');
    act(() => { root.render(<Harness status="error" options={[]} />); });
    expect(document.querySelector('.pick-head')?.textContent).toBe('arama başarısız');
    expect(document.querySelector('.pick-state.is-err')).toBeTruthy();
  });
});

describe('PickerPopover — uzun adlar', () => {
  const LONG = 'synthetic-checkout-orchestrator-with-a-very-long-deployment-name-v2';

  it('ad .pick-name içinde, tam ad (ve ek etiket) title’da', () => {
    const input = mount({ options: [LONG], optionMeta: () => 'Go 1.22 · 1.2K span' });
    focusIn(input);
    const row = opts()[0];
    expect(row.querySelector('.pick-name')?.textContent).toBe(LONG);
    expect(row.querySelector('.pick-meta')?.textContent).toBe('Go 1.22 · 1.2K span');
    expect(row.title).toBe(`${LONG} · Go 1.22 · 1.2K span`);
  });

  it('yatay taşma yok: liste overflow-x hidden, ad üç noktayla kısalır', () => {
    // globals.css'in `.pick-*` kuralları (gerçek kaynak, kopya değil) enjekte edilir.
    const css = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '');
    const rules = css.match(/(^|\n)\.pick-[^{]*\{[^}]*\}/g) ?? [];
    expect(rules.length).toBeGreaterThan(5);
    const style = document.createElement('style');
    style.textContent = rules.join('\n').replace(/::-webkit-scrollbar[^{]*\{[^}]*\}/g, '');
    document.head.appendChild(style);
    try {
      const input = mount({ options: [LONG] });
      focusIn(input);
      const list = document.querySelector<HTMLElement>('.pick-list')!;
      const name = document.querySelector<HTMLElement>('.pick-name')!;
      const row = opts()[0];
      expect(getComputedStyle(list).overflowX).toBe('hidden');
      expect(getComputedStyle(row).whiteSpace).toBe('nowrap');
      expect(getComputedStyle(name).overflow).toBe('hidden');
      expect(getComputedStyle(name).textOverflow).toBe('ellipsis');
      expect(getComputedStyle(pop()!).maxHeight).toBe('50vh');
    } finally {
      style.remove();
    }
  });
});

describe('ServicePicker — ortak popover’ı miras alır', () => {
  it('sunucu cevabı gelene dek "aranıyor…", sonra toplam; "Son kullanılan" seçimi commit eder', async () => {
    localStorage.setItem(STORAGE_KEYS.pickerRecents, JSON.stringify({ service: ['svc-zeta'] }));
    vi.mocked(api.serviceNames).mockResolvedValue({ names: ['svc-alpha', 'svc-beta'], total: 742, hasMore: true });
    const entered: Array<string | undefined> = [];
    function P() {
      const [v, setV] = useState('');
      return <ServicePicker value={v} onChange={setV} onEnter={x => entered.push(x)} placeholder="Filter services…" />;
    }
    act(() => { root.render(<P />); });
    const input = host.querySelector('input') as HTMLInputElement;
    focusIn(input);
    expect(document.querySelector('.pick-head')?.textContent).toBe('aranıyor…');
    await act(async () => { await new Promise(r => setTimeout(r, 220)); });
    expect(api.serviceNames).toHaveBeenCalledWith('', 200);
    expect(document.querySelector('.pick-head')?.textContent).toBe('742 sonuç');
    expect(opts().map(o => o.textContent)).toEqual(['svc-zeta', 'svc-alpha', 'svc-beta']);
    expect(document.querySelector('.pick-foot')?.textContent).toContain('+740 more');
    act(() => { opts()[0].dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(entered).toEqual(['svc-zeta']);
  });

  it('sunucu düşerse kısa hata satırı', async () => {
    vi.mocked(api.serviceNames).mockRejectedValue(new Error('503'));
    act(() => { root.render(<ServicePicker value="" onChange={() => {}} />); });
    const input = host.querySelector('input') as HTMLInputElement;
    focusIn(input);
    await act(async () => { await new Promise(r => setTimeout(r, 220)); });
    expect(document.querySelector('.pick-head')?.textContent).toBe('arama başarısız');
    expect(document.querySelector('.pick-state.is-err')).toBeTruthy();
  });
});
