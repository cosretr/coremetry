// lib/palette — v0.10.1658 (etkileşim dilimi P2, plan §1.1/§1.4). Saf parite:
// v0.10.1653 gövdesindeki sıra (eylemler üstte, trace id en üstte, varlıklar
// önce → sayfalar, kimlik satırları sayfaların ardından, "tüm sonuçlar" EN
// SONDA), gruplama sırayı değiştirmez, eylem kipi modeli ACTIONS'ı 1:1 taşır,
// sunucu araması eşikleri (≥2 / ≥3 / trace id hariç) aynı.
import { describe, it, expect } from 'vitest';
import { buildPaletteResults, pivotServiceRows, type PaletteResultInput } from './results';
import { groupPaletteResults } from './groups';
import { PAGES, localizePages, type PaletteResult } from './pages';
import { promptModel } from './prompt';
import { paletteServiceQuery, PALETTE_SERVICE_LIMIT, PALETTE_DEBOUNCE_MS } from './usePaletteServices';
import { paletteOracleQuery, PALETTE_ENDPOINT_LIMIT, PALETTE_ORACLE_LIMIT } from './usePaletteEndpoints';
import { ACTIONS } from '@/lib/actions';

const svc = (n: string): PaletteResult => ({ kind: 'service', label: n, hint: 'Service', to: `/service?name=${n}` });
const ep = (n: string): PaletteResult => ({ kind: 'endpoint', label: n, hint: 'Endpoint', to: `/traces?op=${n}` });
const op = (n: string): PaletteResult => ({ kind: 'operation', label: n, hint: 'Operasyon', to: `/traces?fc=${n}` });
const base = (o: Partial<PaletteResultInput>): PaletteResultInput => ({
  query: '', pages: localizePages(k => k, false), services: [], endpoints: [], oracleOps: [], pivotSvcs: [],
  role: 'editor', seeAllLabel: 'See all results for “{q}” →', ...o,
});
const groupsOf = (rs: PaletteResult[]) => groupPaletteResults(rs).map(g => g.group);

describe('buildPaletteResults — sıra paritesi', () => {
  it('boş sorgu: ★ + son bakılanlar, sonra sayfalar; eylem / tüm sonuçlar yok', () => {
    const pivots = pivotServiceRows(['a'], ['b', 'a'], n => `/service?name=${n}`);
    const rs = buildPaletteResults(base({ pivotSvcs: pivots }));
    expect(rs.slice(0, 2).map(r => [r.label, r.hint])).toEqual([['a', '★ Pinned'], ['b', 'Recent']]);
    expect(groupsOf(rs)).toEqual(['pinned', 'recent', 'pages']);
  });

  it('servis → endpoint → operasyon → sayfa; "tüm sonuçlar" en sonda', () => {
    const rs = buildPaletteResults(base({ query: 'check', services: [svc('checkout')], endpoints: [ep('GET /check')], oracleOps: [op('CHECK_OP')] }));
    expect(groupsOf(rs)).toEqual(['services', 'endpoints', 'operations', 'search']);
    expect(rs[rs.length - 1]).toMatchObject({ kind: 'page', group: 'search', label: 'See all results for “check” →', to: '/search?q=check' });
  });

  it('fiil sorgusu: eylemler EN ÜSTTE (eski davranış), rol süzgeçli', () => {
    const rs = buildPaletteResults(base({ query: 'ack' }));
    expect(groupsOf(rs)[0]).toBe('actions');
    expect(rs[0].action?.id).toBe('ack-problem');
    expect(buildPaletteResults(base({ query: 'ack', role: 'viewer' })).some(r => r.kind === 'action')).toBe(false);
  });

  it('trace id: "Open trace" en üstte; servis/endpoint araması yok', () => {
    const id = '4bf92f3577b34da6a3ce929d0e0e4736';
    const rs = buildPaletteResults(base({ query: id }));
    expect(rs[0]).toMatchObject({ kind: 'trace', label: id, hint: 'Open trace' });
    expect(groupsOf(rs)[0]).toBe('trace');
    expect(paletteServiceQuery(id)).toBeNull();
    expect(paletteOracleQuery(id)).toBeNull();
  });

  it('kimlik değeri HAM sorgudan (harf korunur), sayfaların ardından', () => {
    const rs = buildPaletteResults(base({ query: '060203vzXA0051685896' }));
    const idRow = rs.find(r => r.group === 'identity');
    expect(idRow?.label).toBe('060203vzXA0051685896');
    expect(groupsOf(rs).slice(-2)).toEqual(['identity', 'search']);
  });

  it('admin değilse Settings/System sayfaları yok; admin → Settings sekmeleri', () => {
    expect(localizePages(k => k, false).some(p => p.adminOnly)).toBe(false);
    const admin = localizePages(k => k, true);
    expect(admin.some(p => p.to?.startsWith('/settings/'))).toBe(true);
    expect(PAGES.some(p => p.to === '/search' && p.navKey === 'nav.search')).toBe(true);
  });
});

describe('groupPaletteResults', () => {
  it('ardışık türleri birleştirir, sırayı korur; tekrar eden grup ayrı kimlik alır', () => {
    const rs: PaletteResult[] = [svc('a'), svc('b'), { kind: 'trace', label: 't', to: '/trace?id=t' }, ep('e'), { kind: 'trace', label: 'u', to: '/trace?id=u' }];
    const gs = groupPaletteResults(rs);
    expect(gs.map(g => [g.id, g.label, g.rows.map(r => r.label).join(',')])).toEqual([
      ['services', 'Services', 'a,b'], ['trace', 'Trace', 't'], ['endpoints', 'Endpoints', 'e'], ['trace-2', 'Trace', 'u'],
    ]);
    expect(gs.flatMap(g => g.rows)).toEqual(rs);
  });
});

describe('promptModel — eylem kipi (Q3/Q12)', () => {
  const ack = ACTIONS.find(a => a.id === 'ack-problem')!;
  const silence = ACTIONS.find(a => a.params.some(p => p.kind === 'duration'))!;
  const st = (o: Partial<Parameters<typeof promptModel>[0]>) => promptModel({
    action: ack, paramIdx: 0, paramValues: {}, suggestQuery: '', suggestResults: [], suggestLoading: false, running: false, ...o,
  });

  it('ACTIONS 1:1 — altı eylem, kimlikler aynı', () => {
    expect(ACTIONS.map(a => a.id).sort()).toEqual(['ack-problem', 'disable-rule', 'enable-rule', 'mark-event', 'save-view', 'silence-anomaly']);
  });
  it('text: tek gönder satırı; boş + zorunlu → ipucu "required", başlık Esc ipucunu taşır', () => {
    const m = st({});
    expect(m.heading).toBe('Acknowledge problem — esc: back to search');
    expect(m.placeholder).toBe('problem-1234');
    expect(m.rows).toEqual([{ id: 'text:submit', label: 'Run: Acknowledge problem', hint: 'Problem ID required', pick: { type: 'text' } }]);
    expect(st({ paramValues: { problemId: 'p-1' } }).rows[0].hint).toBe('↵');
  });
  it('duration: süre seçenekleri satır olur, girdi süzer', () => {
    const idx = silence.params.findIndex(p => p.kind === 'duration');
    const m = promptModel({ action: silence, paramIdx: idx, paramValues: {}, suggestQuery: '', suggestResults: [], suggestLoading: false, running: false });
    expect(m.rows.length).toBeGreaterThan(1);
    expect(m.rows.every(r => r.pick.type === 'duration')).toBe(true);
    const f = promptModel({ action: silence, paramIdx: idx, paramValues: {}, suggestQuery: '24', suggestResults: [], suggestLoading: false, running: false });
    expect(f.rows.map(r => r.label)).toEqual(['24h']);
  });
  it('çalışırken satır yok, "Running…"', () => {
    expect(st({ running: true })).toMatchObject({ rows: [], loading: true, empty: 'Running…' });
  });
});

describe('sunucu araması eşikleri (eager katalog yok)', () => {
  it.each([
    ['', null], ['c', null], ['ch', 'ch'], ['  checkout ', 'checkout'],
  ])('servis/endpoint sorgusu %j → %j', (q, want) => {
    expect(paletteServiceQuery(q)).toBe(want);
  });
  it.each([
    ['ef', null], ['EFT', 'EFT'], ['x'.repeat(81), null],
  ])('oracle sorgusu %j → %j', (q, want) => {
    expect(paletteOracleQuery(q)).toBe(want);
  });
  it('limitler + debounce aynı (20 / 6 / 6, 200 ms)', () => {
    expect([PALETTE_SERVICE_LIMIT, PALETTE_ENDPOINT_LIMIT, PALETTE_ORACLE_LIMIT, PALETTE_DEBOUNCE_MS]).toEqual([20, 6, 6, 200]);
  });
});
