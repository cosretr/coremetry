// @vitest-environment jsdom
//
// v0.10.2317 — pin: arayüz dili İngilizceyken Traces'in ortak alt bileşenleri
// Pager (imleç / ofset / ters kip) ve FilterQueryBox (dinlenme, anahtar / op /
// değer adımları, ipuçları, Hızlı) bilinen Türkçe metin ÇİZMEZ (metin + title /
// aria-label / placeholder); Türkçe arayüzde metin aynen.
import { describe, it, expect, vi } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    attributeValues: (k: unknown) => Promise.resolve(k === 'http.status_code' ? [{ value: '0', count: 3 }, { value: '', count: 1 }] : [{ value: '/a', count: 5 }]),
    attributeKeys: () => Promise.resolve([]),
  };
  return { api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve([])) }), isCanceled: () => false };
});
import { Pager } from '@/components/Pager';
import { FilterQueryBox } from '@/components/FilterQueryBox';
import type { FilterExpr } from '@/lib/types';
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const setVal = (el: HTMLInputElement, v: string) => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(el, v);
  el.dispatchEvent(new Event('input', { bubbles: true }));
};
const key = (el: HTMLElement, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 260)); });

async function all(): Promise<string> {
  const out: string[] = [];
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const draw = async (n: ReactNode) => { await act(async () => { root.render(<QueryClientProvider client={qc}><MemoryRouter>{n}</MemoryRouter></QueryClientProvider>); }); await tick(); };
  const snap = (l: string) => { void l; const attrs = [...host.querySelectorAll('[title],[aria-label],[placeholder]')].flatMap(e => ['title', 'aria-label', 'placeholder'].map(a => e.getAttribute(a) ?? '')); out.push(host.textContent ?? '', ...attrs); };
  const noop = () => {};
  for (const [l, el] of [
    ['cursor-more', <Pager mode="cursor" count="skip" hasMore onMore={noop} loaded={50} />],
    ['cursor-done-n', <Pager mode="cursor" count="skip" hasMore={false} onMore={noop} loaded={1234} />],
    ['cursor-done', <Pager mode="cursor" count="skip" hasMore={false} onMore={noop} />],
    ['offset', <Pager mode="offset" count="exact" total={500} pageSize={50} page={2} hasMore onPage={noop} lastReachablePage={9} />],
    ['offset-jump', <Pager mode="offset" count="skip" pageSize={50} page={2} hasMore onPage={noop} lastReachablePage={9} />],
    ['offset-end', <Pager mode="offset" count="skip" pageSize={50} page={0} hasMore onPage={noop} onEnd={noop} />],
    ['rev1', <Pager mode="offset" count="skip" pageSize={50} page={0} hasMore onPage={noop} onEnd={noop} reverse reverseTitle="rt" />],
    ['rev3', <Pager mode="offset" count="exact" total={500} pageSize={50} page={2} hasMore onPage={noop} onEnd={noop} reverse />],
  ] as [string, ReactNode][]) { await draw(el); snap(l); }

  const value: FilterExpr[] = [{ k: 'http.route', op: '=', v: ['/x'] }, { k: 'svc', op: 'IN', v: ['a', 'b'] }];
  const quick: FilterExpr[] = [{ k: 'kind', op: '=', v: ['server'] }];
  await draw(<FilterQueryBox value={value} onChange={noop} quick={quick} recentKey="t" keyOptions={['http.route', 'http.status_code', 'user.id']} />);
  snap('fq-rest');
  const inp = () => host.querySelector('input.fq-input') as HTMLInputElement;
  act(() => { setVal(inp(), 'ht'); }); await tick(); snap('fq-key');
  act(() => { setVal(inp(), 'zzz'); }); await tick(); snap('fq-nokey');
  act(() => { setVal(inp(), ''); }); await tick(); snap('fq-empty');
  act(() => { setVal(inp(), 'http.status_code'); }); await tick();
  act(() => { key(inp(), 'Enter'); }); await tick(); snap('fq-op');
  act(() => { key(inp(), 'Enter'); }); await tick(); snap('fq-val');
  act(() => { setVal(inp(), 'q'); }); await tick(); snap('fq-val-typed');
  act(() => { key(inp(), 'Escape'); }); await tick();
  await draw(<FilterQueryBox value={value} onChange={noop} keyOptions={['http.route']} placeholder="pp" />);
  act(() => { setVal(inp(), 'http.route'); }); await tick();
  act(() => { key(inp(), 'Enter'); }); await tick();
  act(() => { setVal(inp(), 'IN'); }); await tick();
  act(() => { key(inp(), 'Enter'); }); await tick(); snap('fq-list-val');
  act(() => { root.unmount(); });
  host.remove();
  return out.join('\n');
}

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;

describe('Pager + FilterQueryBox — İngilizce arayüz (v0.10.2317)', () => {
  it('Türkçe metin yok', async () => {
    m.lang = 'en';
    const text = await all();
    expect(text.split('\n').filter(l => TR_CHARS.test(l))).toEqual([]);
    for (const en of [
      'all matches in the window loaded (1,234 rows)', 'all loaded', 'Press Enter to go', 'Go to the end of the list', 'Go to the last page (10)',
      'Last page', '3 from the end', 'One position back towards "Last page"', 'Next page from the end (towards the start of the list)',
      'Filter: type a key or http.route=/x', ' select · ', ' step · ', ' add · ', ' cancel', 'Quick:', 'Add as filter',
      'http.route = /x — click: edit', 'Change operator', 'Remove filter: http.route', 'Filter', '1 · Key', '3 · Value',
      'No matching key — press Enter to use it as typed', ' in an empty box: last chip / previous step', 'Inline', ' key=value · key in a,b · key exists',
      'values seen in this window', 'no response / non-HTTP span', '(empty)', 'key…', 'value…',
    ]) expect(text, en).toContain(en);
  }, 30000);

  it('Türkçe arayüzde bilinen metin aynen', async () => {
    m.lang = 'tr';
    const text = await all();
    for (const tr of [
      'penceredeki tüm eşleşmeler yüklendi (1,234 satır)', 'tümü yüklendi', 'Enter ile git', 'Listenin sonuna git', 'Son sayfaya git (10)',
      'Son sayfa', 'Sondan 3.', '"Son sayfa" yönünde bir konum geri', 'Sondan bir sonraki sayfa (listenin başına doğru)',
      'Filtre: anahtar yaz ya da http.route=/x', ' seç · ', ' adım · ', ' ekle · ', ' vazgeç', 'Hızlı:', 'Filtre olarak ekle',
      'http.route = /x — tık: düzenle', 'Operatörü değiştir', 'Filtreyi kaldır: http.route', '1 · Anahtar', '3 · Değer',
      'Eşleşen anahtar yok — Enter ile olduğu gibi kullan', ' boş kutuda son çip / önceki adım', 'Satır içi',
      'bu pencerede görülen değerler', 'yanıt yok / HTTP dışı span', '(boş)', 'anahtar…', 'değer…',
    ]) expect(text, tr).toContain(tr);
  }, 30000);
});
