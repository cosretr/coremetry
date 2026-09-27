// @vitest-environment jsdom
//
// settingsTableRhythm.pin — v0.10.977 (tablo standardı dilim 7; T2 / T5 / T6 /
// T8 / T10).
//
// NE ÇİVİLİYOR: dilim 3'ün (v0.10.942) "sınıf karşılığı yok, satır içi kalır"
// diye bıraktığı üç ayar tablosu tabana döndü; geri gelmesin.
//   • LdapUserPicker sonuç tablosu: satır ve hücrelerde satır içi stil yok
//     (eski `padding: 6` ve `<tr style={{ borderTop }}>` — `tbody tr` alt
//     çizgisiyle çift ayraç); eylem hücresi `col-actions`, Provision düğmesi
//     ve P-2 durum satırı yerinde.
//   • LdapTab eşleme tablosu: `<td style={{ padding: 4 }}>` yok; silme hücresi
//     `col-actions` (T8 tek sağ eylem sütunu); başlık dolgusu satır içi değil
//     (hücreyle hizalı kalsın diye ikisi birlikte tabana indi).
//   • ZoomChannelPicker: liste kabı `table-wrap is-scroll` (T10 tek çerçeve;
//     yapışkan başlık `.is-scroll thead th` kuralından), `<thead>` satır içi
//     stil taşımaz; yükseklik hâlâ diyaloğun flex sütunundan (`flex: 1`).
// Kaynak çivileri styles/jsxTags'in süslü parantez farkında yürüyücüsüyle
// (ratchet'in tdStyle sayımıyla aynı ölçü); DOM çivileri iki seçicinin
// gerçek çizimi üzerinde (states testleriyle aynı mock deseni).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { jsxOpenTags } from '@/styles/jsxTags';
import type { LDAPDirectoryUser } from '@/lib/types';

const h = vi.hoisted(() => ({
  search: vi.fn<(q: string, limit: number) => Promise<{ users: unknown[] | null }>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    searchLDAPUsers: (q: string, limit: number) => h.search(q, limit),
    provisionLDAPUser: () => Promise.resolve({}),
  },
}));

import { LDAPUserPicker } from './LdapUserPicker';
import { ZoomChannelPicker } from './ZoomChannelPicker';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// ── Kaynak çivileri ────────────────────────────────────────────────────────
// JSX yorumları + satır başı blok/satır yorumları (settingsPalette.pin ile
// aynı soyucu): gerekçe yorumları eski deseni adıyla anıyor, sayılmasın.
function stripComments(src: string): string {
  return src
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\*[\s\S]*?\*\//gm, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\/.*$/gm, '');
}
const read = (f: string) => stripComments(readFileSync(resolve(__dirname, f), 'utf8'));
const withStyle = (src: string, tag: string) => jsxOpenTags(src, tag).filter(t => /\sstyle=\{/.test(t.tag));

describe('ayar tabloları taban ritminde (v0.10.977)', () => {
  it('LdapTab: hiçbir <td> satır içi stil taşımaz; silme hücresi col-actions; başlık dolgusu satır içi değil', () => {
    const src = read('LdapTab.tsx');
    expect(withStyle(src, 'td').map(t => t.line)).toEqual([]);
    expect(jsxOpenTags(src, 'th').filter(t => /padding/.test(t.tag)).map(t => t.line)).toEqual([]);
    expect(src).toContain('<th className="col-actions" aria-label="Eylemler" style={{ width: 32 }} />');
    expect(src).toContain('<td className="col-actions">');
  });

  it('LdapUserPicker: <td>/<th> dolgusuz, satırın kendi üst çizgisi yok (tek ayraç)', () => {
    const src = read('LdapUserPicker.tsx');
    expect(withStyle(src, 'td').map(t => t.line)).toEqual([]);
    expect(jsxOpenTags(src, 'th').filter(t => /padding/.test(t.tag)).map(t => t.line)).toEqual([]);
    expect(jsxOpenTags(src, 'tr').filter(t => /borderTop/.test(t.tag)).map(t => t.line)).toEqual([]);
    expect(src).toContain('<th className="col-actions" aria-label="Eylemler" style={{ width: 100 }} />');
  });

  it('ZoomChannelPicker: kap table-wrap is-scroll (flex: 1), <thead> ve <th> satır içi stil taşımaz', () => {
    const src = read('ZoomChannelPicker.tsx');
    expect(src).toContain('<div className="table-wrap is-scroll" style={{ flex: 1 }}>');
    expect(withStyle(src, 'thead').map(t => t.line)).toEqual([]);
    expect(withStyle(src, 'th').map(t => t.line)).toEqual([]);
    expect(src).not.toMatch(/overflowY:\s*'auto'/);
    // zLayers çivisi: modal perdesi hâlâ `position: 'fixed'` (sınıfa devredilen
    // başlık yapışkanlığı onu değiştirmez).
    expect(src).toContain("position: 'fixed'");
  });
});

// ── DOM çivileri ───────────────────────────────────────────────────────────
let host: HTMLDivElement | null = null;
let root: Root | null = null;
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.search.mockReset();
});

function mountEl(node: React.ReactElement): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
function typeInto(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

const USERS: LDAPDirectoryUser[] = [
  { dn: 'cn=ayse,ou=people', username: 'ayse', displayName: 'Ayşe Y.', email: 'ayse@example.com' },
  { dn: 'cn=mehmet,ou=people', username: 'mehmet', displayName: 'Mehmet K.', email: '' },
];

describe('LDAPUserPicker — sonuç satırları taban ritminde (DOM)', () => {
  it('veri satırları ve hücreleri stil özniteliği taşımaz; eylem hücresi col-actions + Provision', async () => {
    h.search.mockResolvedValue({ users: USERS });
    const el = mountEl(<LDAPUserPicker />);
    typeInto(el.querySelector('form input')!, 'a');
    await act(async () => { el.querySelector('form')!.requestSubmit(); });
    await flush();

    const ths = Array.from(el.querySelectorAll<HTMLTableCellElement>('thead th'));
    expect(ths).toHaveLength(4);
    for (const th of ths) expect(th.style.padding, 'başlık dolgusu satır içi').toBe('');
    expect(ths[3].classList.contains('col-actions')).toBe(true);

    const rows = Array.from(el.querySelectorAll<HTMLTableRowElement>('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));
    expect(rows).toHaveLength(2);
    for (const tr of rows) {
      expect(tr.getAttribute('style'), 'satırın kendi çizgisi').toBeNull();
      const tds = Array.from(tr.querySelectorAll('td'));
      expect(tds).toHaveLength(4);
      for (const td of tds) expect(td.getAttribute('style'), 'hücre dolgusu satır içi').toBeNull();
      expect(tds[3].classList.contains('col-actions')).toBe(true);
      expect(tds[3].querySelector('button')?.textContent).toBe('Provision');
    }
    // Boş e-posta eskisi gibi "—" (davranış aynı).
    expect(rows[1].querySelectorAll('td')[2].textContent).toBe('—');
  });
});

interface FakeResponse { ok: boolean; status: number; text: () => Promise<string>; json: () => Promise<unknown> }
const okJson = (body: unknown): FakeResponse =>
  ({ ok: true, status: 200, text: () => Promise.resolve(JSON.stringify(body)), json: () => Promise.resolve(body) });
const CHANNELS = [
  { id: 'c1', jid: 'ops@conference.xmpp.zoom.us', name: 'ops-alerts', type: 3 },
];

describe('ZoomChannelPicker — liste kabı tek çerçeve (DOM)', () => {
  const realFetch = globalThis.fetch;
  afterEach(() => { globalThis.fetch = realFetch; });

  it('tablo table-wrap.is-scroll içinde; thead/th satır içi stil taşımaz; satır tıkı korunur', async () => {
    const fetchMock = vi.fn<(url: string, init?: unknown) => Promise<FakeResponse>>();
    fetchMock.mockResolvedValue(okJson({ channels: CHANNELS }));
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    const picked: string[] = [];
    const el = mountEl(<ZoomChannelPicker accountId="acc" clientId="cid" clientSecret="sec"
      oauthBaseUrl="" apiBaseUrl="" onPick={j => picked.push(j)} />);
    await act(async () => {
      Array.from(el.querySelectorAll('button')).find(b => /List my channels/.test(b.textContent ?? ''))!.click();
    });
    await flush();

    const table = el.querySelector('table')!;
    const wrap = table.parentElement!;
    expect(wrap.classList.contains('table-wrap')).toBe(true);
    expect(wrap.classList.contains('is-scroll')).toBe(true);
    expect(wrap.style.flex, 'yükseklik flex sütunundan').not.toBe('');
    expect(wrap.style.border, 'elle kenarlık').toBe('');
    expect(table.querySelector('thead')!.getAttribute('style')).toBeNull();
    for (const th of Array.from(table.querySelectorAll('thead th'))) expect(th.getAttribute('style')).toBeNull();

    const row = table.querySelector<HTMLTableRowElement>('tbody tr[role="button"]')!;
    expect(row, 'satır tıkı işareti').not.toBeNull();
    await act(async () => { row.click(); });
    expect(picked).toEqual(['ops@conference.xmpp.zoom.us']);
  });
});
