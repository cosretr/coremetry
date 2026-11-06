// @vitest-environment jsdom
//
// SqlCode sözleşmesi — v0.10.1859. Renkli SQL bloğu: görünen metin = girdi
// (kayıpsız), renk yalnız token sınıfından, kopyala düğmesi girdinin
// KENDİSİNİ kopyalar ve erişilebilir adı vardır.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { SqlCode } from './SqlCode';
import { SQL_TOKEN_CLASS, SQL_CODE_STRINGS } from './sqlCodeClass';

const copyToClipboard = vi.fn(async (_text: string) => true);
vi.mock('@/lib/clipboard', () => ({ copyToClipboard: (t: string) => copyToClipboard(t) }));

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  copyToClipboard.mockClear();
});
function render(node: React.ReactNode): HTMLElement {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host.querySelector('[data-sql-code]')!;
}

const SQL = "SELECT COUNT(*) FROM orders o WHERE o.status = 'OPEN' AND o.id > ? -- açık siparişler";

describe('SqlCode', () => {
  it('shows the statement verbatim', () => {
    const el = render(<SqlCode sql={SQL} />);
    expect(el.querySelector('pre')!.textContent).toBe(SQL);
  });

  it('colours each token kind from the token class map', () => {
    const el = render(<SqlCode sql={SQL} />);
    const by = (k: string) => Array.from(el.querySelectorAll(`[data-sql="${k}"]`));
    expect(by('kw').map(n => n.textContent)).toEqual(['SELECT', 'FROM', 'WHERE', 'AND']);
    expect(by('fn').map(n => n.textContent)).toEqual(['COUNT']);
    expect(by('str').map(n => n.textContent)).toEqual(["'OPEN'"]);
    expect(by('param').map(n => n.textContent)).toEqual(['?']);
    expect(by('cmt').map(n => n.textContent)).toEqual(['-- açık siparişler']);
    for (const k of ['kw', 'fn', 'str', 'param', 'cmt'] as const) {
      expect(by(k)[0]!.className).toBe(SQL_TOKEN_CLASS[k]);
    }
    // anahtar sözcükte renk tek taşıyıcı değil
    expect(SQL_TOKEN_CLASS.kw).toContain('font-semibold');
  });

  it('every colour is an existing token, no literal colours', () => {
    for (const cls of Object.values(SQL_TOKEN_CLASS)) {
      expect(cls).toMatch(/text-\[color:var\(--[a-z0-9-]+\)\]/);
      expect(cls).not.toMatch(/#[0-9a-fA-F]{3,8}|rgb|hsl/);
    }
  });

  it('copy button has an accessible name and copies the raw statement', async () => {
    const el = render(<SqlCode sql={SQL} />);
    const btn = el.querySelector('button')!;
    expect(btn.getAttribute('aria-label')).toBe(SQL_CODE_STRINGS.copy);
    await act(async () => { btn.click(); });
    expect(copyToClipboard).toHaveBeenCalledWith(SQL);
    expect(btn.getAttribute('aria-label')).toBe(SQL_CODE_STRINGS.copied);
  });

  it('dense variant keeps the same content', () => {
    const el = render(<SqlCode sql={SQL} dense />);
    expect(el.querySelector('pre')!.textContent).toBe(SQL);
    expect(el.querySelector('pre')!.className).toContain('text-xs');
  });
});
