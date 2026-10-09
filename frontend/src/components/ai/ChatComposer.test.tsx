// @vitest-environment jsdom
//
// v0.10.1145 — ChatComposer (markdown bilen textarea) davranış sözleşmesi:
//   (1) kısayollar + araç çubuğu aç/kapa, seçim korunur, odak textarea'ya döner;
//       araç çubuğu VARSAYILAN KAPALI (operatör kararı 2026-10-09): "Aa" açar /
//       kapatır, durum kullanıcı başına localStorage'da; kapalıyken kısayollar,
//       akıllı yapıştırma, liste sürdürme ve çit içi Enter çalışmaya devam eder;
//   (2) geri al: execCommand('insertText') varsa yerel yığın, yoksa elle geçmiş;
//   (3) Enter: listede sürdür / boş maddede bitir, kod çitinde ASLA gönderme,
//       IME birleştirmesinde gönderme, dışarıda gönder; Tab listede girinti;
//   (4) Ctrl/Cmd+K global ⌘K katmanına sızmaz; Türkçe Q / AltGr;
//   (5) akıllı yapıştırma + "Geri al";
//   (6) önizleme cevap çizicisiyle (dış link doğrulanmamış metin);
//   (7) açık popup'ın tuşları önce (onBeforeKey).
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { ChatComposer } from './ChatComposer';
import { composerShortcut } from './composerKeys';

let host: HTMLDivElement;
let root: Root;

// Bellek içi depolama (Node sürümüne göre global depolama yok ya da atıyor; testEnvContract).
function memStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? m.get(k)! : null),
    setItem: (k: string, v: string) => { m.set(k, String(v)); },
    removeItem: (k: string) => { m.delete(k); },
    clear: () => m.clear(),
    key: (i: number) => [...m.keys()][i] ?? null,
    get length() { return m.size; },
  };
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.stubGlobal('localStorage', memStorage());
  vi.stubGlobal('sessionStorage', memStorage());
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (document as unknown as { execCommand?: unknown }).execCommand;
});

function Harness({ initial = '', onSubmit, onBeforeKey }: {
  initial?: string;
  onSubmit: () => void;
  onBeforeKey?: (e: ReactKeyboardEvent<HTMLTextAreaElement>) => boolean;
}) {
  const [v, setV] = useState(initial);
  return (
    <MemoryRouter>
      <div className="cm-composer">
        <ChatComposer value={v} onChange={setV} onSubmit={onSubmit} onBeforeKey={onBeforeKey}
          placeholder="sor" ariaLabel="CoSRE'ye mesaj" actions={<span data-actions="" />} />
      </div>
    </MemoryRouter>
  );
}

async function mount(initial = '', onBeforeKey?: (e: ReactKeyboardEvent<HTMLTextAreaElement>) => boolean) {
  const onSubmit = vi.fn();
  await act(async () => { root.render(<Harness initial={initial} onSubmit={onSubmit} onBeforeKey={onBeforeKey} />); });
  return onSubmit;
}

const ta = () => host.querySelector<HTMLTextAreaElement>('textarea')!;
const toolbar = () => host.querySelector<HTMLElement>('[role="toolbar"]');
const tool = (label: string) => host.querySelector<HTMLButtonElement>(`[role="toolbar"] button[aria-label="${label}"]`);
const aa = () => host.querySelector<HTMLButtonElement>('button.cm-composer__aa')!;
async function openTools() {
  if (!toolbar()) await act(async () => { aa().click(); });
  expect(toolbar()).not.toBeNull();
}

async function type(value: string, start = value.length, end = start) {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
  await act(async () => {
    setter.call(ta(), value);
    ta().dispatchEvent(new Event('input', { bubbles: true }));
    ta().setSelectionRange(start, end);
    ta().dispatchEvent(new Event('select', { bubbles: true }));
  });
}

async function select(start: number, end = start) {
  await act(async () => {
    ta().setSelectionRange(start, end);
    ta().dispatchEvent(new Event('select', { bubbles: true }));
  });
}

async function focus() {
  await act(async () => { ta().focus(); });
}

async function key(k: string, opts: KeyboardEventInit & { keyCode?: number } = {}) {
  const ev = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...opts });
  if (opts.keyCode !== undefined) Object.defineProperty(ev, 'keyCode', { value: opts.keyCode });
  await act(async () => { ta().dispatchEvent(ev); });
  return ev;
}

const sel = () => [ta().selectionStart, ta().selectionEnd];

describe('(1) kısayollar ve araç çubuğu', () => {
  it('Ctrl+B seçimi kalın yapar, tekrar basınca kaldırır; seçim korunur', async () => {
    await mount();
    await focus();
    await type('svc-orders yavaş', 0, 10);
    const ev = await key('b', { ctrlKey: true });
    expect(ev.defaultPrevented).toBe(true);
    expect(ta().value).toBe('**svc-orders** yavaş');
    expect(sel()).toEqual([2, 12]);
    await key('b', { metaKey: true });
    expect(ta().value).toBe('svc-orders yavaş');
    expect(sel()).toEqual([0, 10]);
  });

  it('Ctrl+I italik, Ctrl+E satır içi kod, Ctrl+Shift+C kod bloğu, Ctrl+Shift+7/8 listeler', async () => {
    await mount();
    await focus();
    await type('a b', 0, 1);
    await key('i', { ctrlKey: true });
    expect(ta().value).toBe('*a* b');
    await type('a b', 2, 3);
    await key('e', { ctrlKey: true });
    expect(ta().value).toBe('a `b`');
    await type('SELECT 1', 0, 8);
    await key('C', { ctrlKey: true, shiftKey: true, code: 'KeyC' });
    expect(ta().value).toBe('```\nSELECT 1\n```');
    // Türkçe Q: Shift+7 = "/" , Shift+8 = "(" — fiziksel Digit tuşundan okunur.
    await type('x\ny', 0, 3);
    await key('/', { ctrlKey: true, shiftKey: true, code: 'Digit7' });
    expect(ta().value).toBe('1. x\n2. y');
    await key('(', { ctrlKey: true, shiftKey: true, code: 'Digit8' });
    expect(ta().value).toBe('- x\n- y');
  });

  it('araç çubuğu: düğmeler gerçek <button>, aria-label + aria-keyshortcuts; tık uygular, odak textarea\'ya döner', async () => {
    await mount('yavaş servis');
    await focus();
    await openTools();
    const labels = Array.from(toolbar()!.querySelectorAll('button')).map(b => b.getAttribute('aria-label') ?? b.textContent);
    expect(labels).toEqual(['Kalın', 'İtalik', 'Satır içi kod', 'Kod bloğu', 'Madde listesi', 'Numaralı liste', 'Alıntı', 'Bağlantı', 'Önizleme']);
    expect(tool('Kalın')!.getAttribute('aria-keyshortcuts')).toBe('Control+B Meta+B');
    expect(tool('Kod bloğu')!.getAttribute('aria-keyshortcuts')).toBe('Control+Shift+C Meta+Shift+C');
    await select(0, 5);
    const down = new MouseEvent('mousedown', { bubbles: true, cancelable: true });
    await act(async () => { tool('Kalın')!.dispatchEvent(down); });
    expect(down.defaultPrevented).toBe(true); // tık odağı/seçimi çalmaz
    await act(async () => { tool('Kalın')!.click(); });
    expect(ta().value).toBe('**yavaş** servis');
    expect(document.activeElement).toBe(ta());
    expect(tool('Kalın')!.getAttribute('aria-pressed')).toBe('true'); // seçim kalın içinde
    await act(async () => { tool('Madde listesi')!.click(); });
    expect(ta().value).toBe('- **yavaş** servis');
  });

  it('araç çubuğu klavyesi: tek sekme durağı, ←/→ gezinir', async () => {
    await mount('x');
    await focus();
    await openTools();
    const btns = Array.from(toolbar()!.querySelectorAll<HTMLButtonElement>('button[data-tool]'));
    expect(btns.filter(b => b.tabIndex === 0)).toHaveLength(1);
    await act(async () => { btns[0].focus(); });
    await act(async () => { toolbar()!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true })); });
    expect(document.activeElement).toBe(btns[1]);
    await act(async () => { toolbar()!.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true, cancelable: true })); });
    expect(document.activeElement?.textContent).toContain('Önizleme');
  });

  it('araç çubuğu VARSAYILAN KAPALI — odakta da; composer düz sohbet kutusu (metin + Aa + eylemler)', async () => {
    await mount('x');
    expect(toolbar()).toBeNull();
    await focus();
    expect(toolbar()).toBeNull();
    expect(host.querySelector('.cm-composer__preview')).toBeNull();
    const box = host.querySelector('.cm-composer__box')!;
    expect(box.querySelector('textarea')).not.toBeNull();
    // Aa eylem kümesinde, kabuğun eylemlerinden (model hapı + Gönder) hemen önce.
    const actions = box.querySelector('.cm-composer__actions')!;
    expect(actions.firstElementChild).toBe(aa());
    expect(actions.querySelector('[data-actions]')).not.toBeNull();
    expect(aa().getAttribute('aria-pressed')).toBe('false');
    expect(aa().getAttribute('aria-label')).toBe('Biçimlendirme araçları');
  });

  it('"Aa" açar/kapatır; durum kullanıcı başına localStorage\'da ve yeniden açılışta korunur', async () => {
    await mount('x');
    await act(async () => { aa().click(); });
    expect(toolbar()).not.toBeNull();
    expect(aa().getAttribute('aria-pressed')).toBe('true');
    expect(aa().getAttribute('aria-controls')).toBe(toolbar()!.id);
    expect(window.localStorage.getItem('cosre.composerTools.anon')).toBe('1');
    // yeniden mount (sayfa yenileme / çekmece yeniden açılış): açık kalır
    act(() => root.unmount());
    root = createRoot(host);
    await mount('x');
    expect(toolbar()).not.toBeNull();
    await act(async () => { aa().click(); });
    expect(toolbar()).toBeNull();
    expect(window.localStorage.getItem('cosre.composerTools.anon')).toBe('0');
    act(() => root.unmount());
    root = createRoot(host);
    await mount('x');
    expect(toolbar()).toBeNull();
  });

  it('depolama atarsa araç çubuğu kapalı başlar ve Aa yine çalışır', async () => {
    const boom = () => { throw new Error('SecurityError'); };
    vi.stubGlobal('localStorage', { getItem: boom, setItem: boom, removeItem: boom, clear: boom, key: boom, length: 0 });
    await mount('x');
    expect(toolbar()).toBeNull();
    await act(async () => { aa().click(); });
    expect(toolbar()).not.toBeNull();
  });

  it('araç çubuğu KAPALIYKEN: kısayollar, liste sürdürme, çit içi Enter ve akıllı yapıştırma çalışır', async () => {
    const onSubmit = await mount();
    await focus();
    expect(toolbar()).toBeNull();
    await type('svc-orders', 0, 10);
    await key('b', { ctrlKey: true });
    expect(ta().value).toBe('**svc-orders**');
    await type('- a');
    await key('Enter');
    expect(ta().value).toBe('- a\n- ');
    await type('```\nkod');
    await key('Enter');
    expect(onSubmit).not.toHaveBeenCalled();
    await type('');
    const ev = await paste({ 'text/plain': '{\n  "a": 1,\n  "b": 2\n}' });
    expect(ev.defaultPrevented).toBe(true);
    expect(ta().value).toBe('```json\n{\n  "a": 1,\n  "b": 2\n}\n```');
    expect(host.querySelector('.cm-composer__note')?.textContent).toContain('Geri al');
    expect(toolbar()).toBeNull();
  });
});

describe('(2) geri al', () => {
  it('execCommand varsa düzenleme insertText ile (yerel geri al yığını); elle geçmiş devreye girmez', async () => {
    const exec = vi.fn((cmd: string, _ui: boolean, text: string) => {
      const el = document.activeElement as HTMLTextAreaElement;
      el.setRangeText(text, el.selectionStart, el.selectionEnd, 'end');
      el.dispatchEvent(new Event('input', { bubbles: true }));
      return cmd === 'insertText';
    });
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true, writable: true });
    await mount();
    await focus();
    await type('kalın', 0, 5);
    await key('b', { ctrlKey: true });
    expect(exec).toHaveBeenCalledWith('insertText', false, '**kalın**');
    expect(ta().value).toBe('**kalın**');
    expect(sel()).toEqual([2, 7]);
    const z = await key('z', { ctrlKey: true });
    expect(z.defaultPrevented).toBe(false); // tarayıcının kendi geri alı
  });

  it('execCommand yoksa setRangeText + elle geçmiş: Ctrl+Z geri alır, Ctrl+Shift+Z yineler', async () => {
    await mount();
    await focus();
    await type('kalın metin', 0, 5);
    await key('b', { ctrlKey: true });
    expect(ta().value).toBe('**kalın** metin');
    const z = await key('z', { ctrlKey: true });
    expect(z.defaultPrevented).toBe(true);
    expect(ta().value).toBe('kalın metin');
    expect(sel()).toEqual([0, 5]);
    await key('Z', { ctrlKey: true, shiftKey: true });
    expect(ta().value).toBe('**kalın** metin');
    await key('y', { ctrlKey: true });
    expect(ta().value).toBe('**kalın** metin'); // yinelenecek bir şey kalmadı
  });

  it('elle düzenleme sonrası yazılan metin Ctrl+Z ile EZİLMEZ (yerel geri ala bırakılır)', async () => {
    await mount();
    await focus();
    await type('a', 0, 1);
    await key('b', { ctrlKey: true });
    await type('**a** sonra');
    const z = await key('z', { ctrlKey: true });
    expect(z.defaultPrevented).toBe(false);
    expect(ta().value).toBe('**a** sonra');
  });
});

describe('(3) Enter / Tab', () => {
  it('düz metinde Enter gönderir; Shift+Enter göndermez', async () => {
    const onSubmit = await mount();
    await focus();
    await type('svc-orders neden yavaş');
    expect((await key('Enter', { shiftKey: true })).defaultPrevented).toBe(false);
    expect(onSubmit).not.toHaveBeenCalled();
    expect((await key('Enter')).defaultPrevented).toBe(true);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('kod çitinin içinde Enter GÖNDERMEZ (yerel satır sonu, preventDefault yok)', async () => {
    const onSubmit = await mount();
    await focus();
    await type('```sql\nSELECT 1');
    const ev = await key('Enter');
    expect(ev.defaultPrevented).toBe(false);
    expect(onSubmit).not.toHaveBeenCalled();
    // açılış çiti satırında da (blok açılıyor)
    await type('```json');
    await key('Enter');
    expect(onSubmit).not.toHaveBeenCalled();
    // kapanmış bloktan sonra gönderir
    await type('```\nx\n```');
    await key('Enter');
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('IME birleştirmesi sırasında Enter göndermez (isComposing / keyCode 229)', async () => {
    const onSubmit = await mount();
    await focus();
    await type('にほん');
    await key('Enter', { isComposing: true });
    await key('Enter', { keyCode: 229 });
    expect(onSubmit).not.toHaveBeenCalled();
    // birleştirme sırasında kısayol da yok
    await type('ab', 0, 2);
    await key('b', { ctrlKey: true, isComposing: true });
    expect(ta().value).toBe('ab');
  });

  it('listede Enter maddeyi sürdürür (numara artar), boş maddede listeyi bitirir; gönderMEZ', async () => {
    const onSubmit = await mount();
    await focus();
    await type('1. aç');
    await key('Enter');
    expect(ta().value).toBe('1. aç\n2. ');
    expect(sel()).toEqual([9, 9]);
    await key('Enter');
    expect(ta().value).toBe('1. aç\n');
    expect(onSubmit).not.toHaveBeenCalled();
    await key('Enter'); // liste bitti: artık gönderir
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('Tab listede girinti / Shift+Tab geri; liste dışında varsayılan odak gezinmesi', async () => {
    await mount();
    await focus();
    await type('- a\n- b');
    const t = await key('Tab');
    expect(t.defaultPrevented).toBe(true);
    expect(ta().value).toBe('- a\n  - b');
    await key('Tab', { shiftKey: true });
    expect(ta().value).toBe('- a\n- b');
    const top = await key('Tab', { shiftKey: true }); // en dışta: değişmez ama odak kalır
    expect(top.defaultPrevented).toBe(true);
    expect(ta().value).toBe('- a\n- b');
    await type('düz metin');
    expect((await key('Tab')).defaultPrevented).toBe(false);
  });
});

describe('(4) kısayol çatışmaları', () => {
  it('Ctrl/Cmd+K bağlantı ekler ve belge düzeyindeki ⌘K katmanına SIZMAZ', async () => {
    const docKeys = vi.fn();
    document.addEventListener('keydown', docKeys);
    try {
      await mount();
      await focus();
      await type('runbook', 0, 7);
      await key('k', { metaKey: true });
      expect(ta().value).toBe('[runbook]()');
      expect(sel()).toEqual([10, 10]);
      expect(docKeys).not.toHaveBeenCalled();
      // URL seçiliyse <url>
      await type('https://wiki.example.test/a', 0, 27);
      await key('k', { ctrlKey: true });
      expect(ta().value).toBe('<https://wiki.example.test/a>');
    } finally {
      document.removeEventListener('keydown', docKeys);
    }
  });

  it('Türkçe Q: Ctrl+ı italik; AltGr (Ctrl+Alt) kısayol değil (köşeli ayraç yazılabilsin)', () => {
    expect(composerShortcut({ key: 'ı', ctrlKey: true })).toBe('italic');
    expect(composerShortcut({ key: 'b', ctrlKey: true, altKey: true })).toBeNull();
    expect(composerShortcut({ key: 'б', code: 'KeyB', ctrlKey: true })).toBe('bold'); // Latin olmayan düzen
    expect(composerShortcut({ key: 'b' })).toBeNull();
    expect(composerShortcut({ key: ':', code: 'Period', ctrlKey: true, shiftKey: true })).toBe('quote');
  });
});

async function paste(data: Record<string, string>) {
  const ev = new Event('paste', { bubbles: true, cancelable: true });
  Object.defineProperty(ev, 'clipboardData', { value: { getData: (t: string) => data[t] ?? '' } });
  await act(async () => { ta().dispatchEvent(ev); });
  return ev;
}

describe('(5) akıllı yapıştırma', () => {
  const STACK = 'java.lang.IllegalStateException: boom\n    at com.example.orders.Pool.get(Pool.java:4)\n    at com.example.orders.Api.run(Api.java:9)';

  it('kod gibi metin → çitli blok + "Kod olarak yapıştırıldı · Geri al"; Geri al ham metne döner', async () => {
    await mount();
    await focus();
    const ev = await paste({ 'text/plain': STACK });
    expect(ev.defaultPrevented).toBe(true);
    expect(ta().value).toBe('```text\n' + STACK + '\n```');
    const note = host.querySelector('.cm-composer__note');
    expect(note?.getAttribute('role')).toBe('status');
    expect(note?.textContent).toContain('Kod olarak yapıştırıldı');
    const undo = Array.from(note!.querySelectorAll('button')).find(b => b.textContent === 'Geri al')!;
    await act(async () => { undo.click(); });
    expect(ta().value).toBe(STACK);
    expect(host.querySelector('.cm-composer__note')).toBeNull();
  });

  it('kod çitinin içinde ham yapıştırma (dönüşüm yok)', async () => {
    await mount();
    await focus();
    await type('```\n\n```', 4);
    const ev = await paste({ 'text/plain': STACK });
    expect(ev.defaultPrevented).toBe(false);
  });

  it('URL seçimin üstüne → [seçim](url), not yok', async () => {
    await mount();
    await focus();
    await type('bkz runbook', 4, 11);
    await paste({ 'text/plain': 'https://wiki.example.test/r' });
    expect(ta().value).toBe('bkz [runbook](https://wiki.example.test/r)');
    expect(host.querySelector('.cm-composer__note')).toBeNull();
  });

  it('HTML (wiki) → markdown; javascript: bağlantısı düşer', async () => {
    await mount();
    await focus();
    await paste({
      'text/html': '<p><b>Adım</b> <a href="javascript:alert(1)">tıkla</a> <a href="https://wiki.example.test/x">wiki</a></p>',
      'text/plain': 'Adım tıkla wiki',
    });
    expect(ta().value).toBe('**Adım** tıkla [wiki](https://wiki.example.test/x)');
    expect(host.querySelector('.cm-composer__note')?.textContent).toContain("markdown'a çevrildi");
  });

  it('tek satır düz metin dokunulmadan (tarayıcı yapıştırır)', async () => {
    await mount();
    await focus();
    const ev = await paste({ 'text/plain': 'svc-orders' });
    expect(ev.defaultPrevented).toBe(false);
  });
});

describe('(6) önizleme — cevaplarla aynı çizici', () => {
  it('kalın, tablo, kod; dış link doğrulanmamış METİN, aynı köken tıklanır; ```chart grafik değil kod', async () => {
    await mount();
    await focus();
    await type([
      '**kalın** ve https://evil.example.test/steal?q=1 ve [iç](/services)',
      '',
      '| a | b |',
      '|---|---|',
      '| 1 | 2 |',
      '',
      '```chart',
      '{"service":"svc-orders","agg":"p95"}',
      '```',
    ].join('\n'));
    await openTools();
    const btn = Array.from(toolbar()!.querySelectorAll('button')).find(b => b.textContent?.includes('Önizleme'))!;
    await act(async () => { btn.click(); });
    expect(btn.getAttribute('aria-pressed')).toBe('true');
    const pv = host.querySelector<HTMLElement>('.cm-composer__preview')!;
    expect(pv.getAttribute('aria-label')).toBe('Önizleme');
    expect(ta().hidden).toBe(true);
    expect(pv.querySelector('b')?.textContent).toBe('kalın');
    expect(pv.querySelector('table.cm-md-table')).not.toBeNull();
    expect(pv.querySelector('a[href^="https://evil"]')).toBeNull();
    expect(pv.querySelector('.cm-md-unverified')?.textContent).toContain('https://evil.example.test/steal?q=1');
    expect(pv.querySelector('a[href="/services"]')).not.toBeNull();
    expect(pv.querySelector('.cm-md-code')?.textContent).toContain('"service":"svc-orders"');
    // araç düğmeleri önizlemede devre dışı; tekrar basınca düzenlemeye döner
    expect(tool('Kalın')!.disabled).toBe(true);
    await act(async () => { btn.click(); });
    expect(host.querySelector('.cm-composer__preview')).toBeNull();
    expect(ta().hidden).toBe(false);
    // araç çubuğu kapanınca önizleme de kapanır (önizleme düğmesi onun içinde)
    await act(async () => { btn.click(); });
    expect(host.querySelector('.cm-composer__preview')).not.toBeNull();
    await act(async () => { aa().click(); });
    expect(toolbar()).toBeNull();
    expect(host.querySelector('.cm-composer__preview')).toBeNull();
    expect(ta().hidden).toBe(false);
  });
});

describe('(7) açık popup önce', () => {
  it('onBeforeKey Enter/Tab\'ı alırsa gönderim, liste sürdürme ve girinti olmaz', async () => {
    const before = vi.fn((e: ReactKeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); return true; }
      return false;
    });
    const onSubmit = await mount('', before);
    await focus();
    await type('- @sv');
    await key('Enter');
    await key('Tab');
    expect(before).toHaveBeenCalledTimes(2);
    expect(ta().value).toBe('- @sv');
    expect(onSubmit).not.toHaveBeenCalled();
    // popup Ctrl+B'yi almaz → kısayol çalışır
    await select(2, 5);
    await key('b', { ctrlKey: true });
    expect(ta().value).toBe('- **@sv**');
  });
});
