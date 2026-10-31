// briefingView.test — v0.10.1700 (CoSRE v2 karşılaması × Briefing Faz 1 ucu).
// Cevap → BriefingFeed görünümü; renk yalnız sapan sayıda (sunucu şablonları
// internal/cosre/briefing/items.go ile aynı biçimde fikstür).
import { describe, it, expect } from 'vitest';
import type { CosreBriefing, CosreBriefingLine, CosreBriefingResponse } from '@/lib/types';
import { briefingParts, briefingTraceHref, briefingView, briefingWindowLabel } from './briefingView';

const NOW = 1_800_000_000_000;
const line = (item: CosreBriefingLine['item'], text: string, service?: string, page_url = '/service?name=orders-api'): CosreBriefingLine => ({
  item, text, page_url, total: 1,
  rows: [{ item, kind: 'k', service, now: 1, prev: 0, score: 1, text, page_url }],
});
const card = (over: Partial<CosreBriefing> = {}): CosreBriefing => ({
  id: 'b1', team: 'payments-core', team_key: 'payments-core', env: 'prod', window_s: 10800,
  generated_at_ms: NOW - 4 * 60_000, scope_hash: 'h', services: 12, trimmed: 0,
  lines: [], silence: true, summary: '', phrasing: 'template', ...over,
});
const ready = (b: CosreBriefing): CosreBriefingResponse => ({ status: 'ready', env: 'prod', ttl_s: 600, briefing: b });

describe('briefingParts', () => {
  const cases: Array<[string, string, string | undefined, Array<[string, string?]>]> = [
    ['hata oranı', 'orders-api hata oranı %2.1 (önceki %0.4, 5.2×)', 'orders-api',
      [['orders-api', 'subject'], [' hata oranı '], ['%2.1', 'num'], [' (önceki %0.4, 5.2×)']]],
    ['p99 ms', 'orders-api p99 980 ms (önceki 410 ms, 2.4×)', 'orders-api',
      [['orders-api', 'subject'], [' p99 '], ['980 ms', 'num'], [' (önceki 410 ms, 2.4×)']]],
    ['p99 s', 'orders-api p99 1.2 s (önceki 410 ms, 2.9×)', 'orders-api',
      [['orders-api', 'subject'], [' p99 '], ['1.2 s', 'num'], [' (önceki 410 ms, 2.9×)']]],
    ['trafik yüzde', 'orders-api trafiği %40 düştü (500 → 300 span)', 'orders-api',
      [['orders-api', 'subject'], [' trafiği '], ['%40', 'num'], [' düştü (500 → 300 span)']]],
    ['rollout sürümü sayı sanılmaz', 'checkout-api 1.2.3→1.3.0 sonrası hata oranı %3.1 (öncesi %1.8, +1.3 puan)', 'checkout-api',
      [['checkout-api', 'subject'], [' 1.2.3→1.3.0 sonrası hata oranı '], ['%3.1', 'num'], [' (öncesi %1.8, +1.3 puan)']]],
    ['sayısız + "+N daha"', 'P2 · orders-api: gecikme sapması (+2 daha)', undefined,
      [['P2 · orders-api: gecikme sapması'], [' (+2 daha)', 'muted']]],
  ];
  it.each(cases)('%s', (_n, text, svc, want) => {
    expect(briefingParts(text, svc).map(p => (p.tone ? [p.text, p.tone] : [p.text]))).toEqual(want);
    expect(briefingParts(text, svc).map(p => p.text).join('')).toBe(text);
  });
});

describe('briefingWindowLabel', () => {
  it.each([[10800, 'Son 3 sa'], [3600, 'Son 1 sa'], [2700, 'Son 45 dk'], [5400, 'Son 90 dk']])('%i → %s', (s, want) => {
    expect(briefingWindowLabel(s)).toBe(want);
  });
});

describe('briefingView', () => {
  it('yok / pending / disabled / unavailable → null', () => {
    expect(briefingView(undefined, '')).toBeNull();
    for (const status of ['pending', 'disabled', 'unavailable'] as const) {
      expect(briefingView({ status, env: 'prod', ttl_s: 600 }, '')).toBeNull();
    }
  });

  it('unscoped no_team → unknownTeam; tarayıcıda takım seçiliyse ya da başka nedense null', () => {
    const r: CosreBriefingResponse = { status: 'unscoped', reason: 'no_team', env: 'prod', ttl_s: 600 };
    expect(briefingView(r, '')).toEqual({ state: 'unknownTeam' });
    expect(briefingView(r, 'payments-core')).toBeNull();
    expect(briefingView({ ...r, reason: 'no_services' }, '')).toBeNull();
  });

  it('sessizlik: takım + servis sayısı + pencere + yaş', () => {
    expect(briefingView(ready(card()), '', NOW)).toEqual({
      state: 'silence', windowLabel: 'Son 3 sa', agoLabel: '4 dk önce', team: 'payments-core', serviceCount: 12,
    });
  });

  it('bulgular: kalem etiketi, önem, pivot yalnız güvenli uygulama yolu, soru metni', () => {
    const v = briefingView(ready(card({
      silence: false,
      lines: [
        line('red', 'orders-api hata oranı %2.1 (önceki %0.4, 5.2×)', 'orders-api'),
        line('rollout', 'checkout-api 1.2.3→1.3.0 sonrası hata oranı %3.1 (öncesi %1.8, +1.3 puan)', 'checkout-api', 'https://evil.example/x'),
      ],
    })), '', NOW);
    expect(v?.state).toBe('findings');
    if (v?.state !== 'findings') return;
    expect(v.lines.map(l => [l.kind, l.severity])).toEqual([['RED', 'critical'], ['ROLLOUT', 'info']]);
    expect(v.lines[0].pivot).toEqual({ label: 'Servis', href: '/service?name=orders-api' });
    expect(v.lines[1].pivot).toBeUndefined();
    expect(v.lines[0].ask).toBe('orders-api hata oranı %2.1 (önceki %0.4, 5.2×) — neden? Kanıtıyla açıkla.');
  });

  it('tarayıcıda başka takım seçildiyse kart gösterilmez; aynı takım (büyük/küçük harf) gösterilir', () => {
    expect(briefingView(ready(card()), 'orders-platform', NOW)).toBeNull();
    expect(briefingView(ready(card()), 'Payments-Core', NOW)?.state).toBe('silence');
  });
});

// v0.10.1718 — kalem 5: Oracle satırının trace çipi yalnız 32 hex kimlikte;
// filo satırı (Q6) takım satırının yanında ayrı satır olarak çizilir.
describe('briefing trace çipi', () => {
  const ora = (trace_id?: string, scope?: 'fleet'): CosreBriefingLine => ({
    item: 'oracle', text: 'orders-api Oracle ORA-00060 · ORDER_SAVE: 40 hata (önceki 10, 4.0×)', page_url: '/problems?exception=ora%3Aab', total: 1,
    ...(scope ? { scope } : {}),
    rows: [{ item: 'oracle', kind: 'code_up', service: 'orders-api', now: 40, prev: 10, score: 4, text: '', page_url: '', ...(trace_id ? { trace_id } : {}) }],
  });
  const cases: Array<[string, string | undefined, string]> = [
    ['geçerli', '4bf92f3577b34da6a3ce929d0e0e4736', '/trace?id=4bf92f3577b34da6a3ce929d0e0e4736'],
    ['yok', undefined, ''],
    ['kısa', '4bf92f35', ''],
    ['büyük harf (sunucu küçültür)', '4BF92F3577B34DA6A3CE929D0E0E4736', ''],
    ['yol enjeksiyonu', '/\\evil.com/0000000000000000000000', ''],
  ];
  it.each(cases)('%s', (_n, id, want) => {
    expect(briefingTraceHref(ora(id))).toBe(want);
    const v = briefingView(ready(card({ silence: false, lines: [ora(id)] })), '', NOW);
    const l = v?.state === 'findings' ? v.lines[0] : undefined;
    expect(l?.trace?.href ?? '').toBe(want);
  });
  it('filo satırı ayrı satır', () => {
    const v = briefingView(ready(card({ silence: false, lines: [ora(), ora(undefined, 'fleet')] })), '', NOW);
    expect(v?.state === 'findings' ? v.lines.map(l => l.kind) : []).toEqual(['ORACLE', 'ORACLE']);
  });
});
