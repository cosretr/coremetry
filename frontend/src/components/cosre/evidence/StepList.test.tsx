// @vitest-environment jsdom
//
// StepList.test — CoSRE v2 araç seti dilim 5 (mockup onayı 2026-10-31):
//   • akarken liste açık, cevap bitince tek özet satırı (tıkla → açılır);
//   • kartlar varsayılan kapalı (yalnız başlık satırı);
//   • sorgu açılırı kapalı, açılınca salt okunur metin + Kopyala;
//   • page_url → alt bağlantı + tıklanır satırlar; yoksa satır tıklanmaz;
//   • skipped gri + gerekçe, error "okunamadı ≠ yok", data-stop tek satır;
//   • kapsam rozeti: etiket, popover açıklama + servis çipleri.
import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { StepList } from './StepList';
import { ScopeBadge } from './ScopeBadge';
import type { StepsView } from './stepView';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

async function draw(node: React.ReactNode) {
  if (!host) {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  }
  await act(async () => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host!;
}
const click = async (el: Element | null) => { await act(async () => { (el as HTMLElement).click(); }); };

const VIEW: StepsView = {
  totalMs: 3500,
  stop: { reason: 'call_cap', calls: 3, rounds: 2, elapsed_ms: 4200 },
  steps: [
    {
      id: 's1', i: 1, tool: 'endpoint_metrics', kind: 'evidence', title: 'Endpoint metrikleri', subtitle: 'orders-api, en yoğun 10 rota',
      durationMs: 1100, tags: [{ label: 'endpoint_metrics' }, { label: 'VictoriaMetrics' }, { label: 'kısaltıldı', warn: true }],
      query: 'topk(10, sum by (http_route) (rate(x[5m])))', queryKind: 'promql', source: 'VictoriaMetrics',
      pageUrl: '/endpoints?service=orders-api&src=metric', pageLabel: "Endpoints'te aç (kaynak: metrik)",
      preview: { columns: [{ label: 'Rota' }, { label: 'istek/sn', numeric: true }], more: 7, rows: [
        { key: '/v1/orders', cells: [{ text: '/v1/orders', mono: true }, { text: '212' }], href: '/endpoints?search=%2Fv1%2Forders' },
      ] },
    },
    { id: 's2', i: 2, tool: 'find_traces', kind: 'text', title: "Trace'ler", tags: [{ label: 'find_traces' }],
      preview: { columns: [{ label: 'Kök operasyon' }], more: 0, rows: [{ key: 'a', cells: [{ text: 'GET /x' }] }] } },
    { id: 's3', i: 3, tool: 'search_logs', kind: 'skipped', title: 'Log arama', subtitle: 'çalıştırılmadı', tags: [{ label: 'search_logs' }], reason: 'çağrı bütçesi doldu' },
    { id: 's4', i: 4, tool: 'list_my_services', kind: 'error', title: 'Servislerim', tags: [{ label: 'list_my_services' }], errorText: 'ClickHouse zaman aşımı' },
  ],
};

describe('StepList', () => {
  it('akarken açık; bitince tek özet satırı, tıklayınca açılır', async () => {
    let el = await draw(<StepList view={VIEW} live />);
    expect(el.querySelectorAll('[data-cosre-step]')).toHaveLength(4);
    expect(el.querySelector('[data-cosre-stop]')?.textContent).toContain('çağrı tavanı · 3 çağrı · 2 tur · 4,2 sn');
    await act(async () => { root?.unmount(); });
    host?.remove(); host = null; root = null;
    el = await draw(<StepList view={VIEW} live={false} />);
    expect(el.querySelectorAll('[data-cosre-step]')).toHaveLength(0);
    const head = el.querySelector('[data-cosre-steps] > button');
    expect(head?.textContent).toContain('Adımlar · 4 çağrı');
    expect(head?.textContent).toContain('Durdu: çağrı tavanı');
    await click(head);
    expect(el.querySelectorAll('[data-cosre-step]')).toHaveLength(4);
  });

  it('kartlar kapalı; açılan evidence kartı etiket, önizleme, kapalı sorgu, bağlantı taşır', async () => {
    const el = await draw(<StepList view={VIEW} live />);
    const card = el.querySelector('[data-cosre-step="s1"]')!;
    expect(card.textContent).toContain('Endpoint metrikleri');
    expect(card.textContent).not.toContain('VictoriaMetrics');
    await click(card.querySelector('button'));
    expect(card.textContent).toContain('VictoriaMetrics');
    expect(card.textContent).toContain('+7 satır · satıra tıkla');
    const row = card.querySelector('[data-cosre-preview] tbody tr')!;
    expect(row.getAttribute('data-href')).toBe('/endpoints?search=%2Fv1%2Forders');
    expect(row.className).toContain('cursor-pointer');
    expect(card.querySelector('[data-cosre-page-url]')?.getAttribute('href')).toBe('/endpoints?service=orders-api&src=metric');
    const q = card.querySelector('[data-cosre-query]')!;
    expect(q.querySelector('pre')).toBeNull();
    await click(q.querySelector('button'));
    expect(q.querySelector('pre')?.textContent).toContain('topk(10');
    expect(q.textContent).toContain('salt okunur');
    expect(q.textContent).toContain('Kopyala');
  });

  it('page_url yoksa satır tıklanmaz görünür ve bağlantı yok', async () => {
    const el = await draw(<StepList view={VIEW} live />);
    const card = el.querySelector('[data-cosre-step="s2"]')!;
    await click(card.querySelector('button'));
    const row = card.querySelector('[data-cosre-preview] tbody tr')!;
    expect(row.getAttribute('data-href')).toBeNull();
    expect(row.className).not.toContain('cursor-pointer');
    expect(card.querySelector('[data-cosre-page-url]')).toBeNull();
  });

  it('skipped gri + gerekçe; error "okunamadı ≠ yok"', async () => {
    const el = await draw(<StepList view={VIEW} live />);
    const sk = el.querySelector('[data-cosre-step="s3"]')!;
    expect(sk.getAttribute('data-kind')).toBe('skipped');
    expect(sk.querySelector('button')?.className).toContain('text-muted-foreground');
    expect(sk.textContent).toContain('atlandı');
    await click(sk.querySelector('button'));
    expect(sk.textContent).toContain('Neden: çağrı bütçesi doldu');
    const er = el.querySelector('[data-cosre-step="s4"]')!;
    await click(er.querySelector('button'));
    expect(er.querySelector('[role="alert"]')?.textContent).toBe('Okunamadı ≠ yok: ClickHouse zaman aşımı');
  });

  it('adım ve durdurma yoksa hiçbir şey çizilmez', async () => {
    const el = await draw(<StepList view={{ steps: [], totalMs: 0 }} live={false} />);
    expect(el.querySelector('[data-cosre-steps]')).toBeNull();
  });
});

describe('ScopeBadge', () => {
  it('team: etiket + popover servis çipleri (+N)', async () => {
    const el = await draw(<ScopeBadge scope={{ reason: 'team', unscoped: false, count: 3, team: 'acme-orders', services: ['orders-api', 'payments-core'] }} />);
    const b = el.querySelector('[data-cosre-scope]')!;
    expect(b.textContent).toBe('Kapsam: acme-orders · 3 servis');
    expect(b.getAttribute('data-tone')).toBe('ok');
    await click(b);
    const chips = document.querySelector('[data-cosre-scope-services]');
    expect(chips?.textContent).toContain('orders-api');
    expect(chips?.textContent).toContain('+1');
  });

  it('kapsamsız uyarı, okuma hatası kırmızı tonda', async () => {
    let el = await draw(<ScopeBadge scope={{ reason: 'no_team', unscoped: true, count: 0 }} />);
    expect(el.querySelector('[data-cosre-scope]')?.getAttribute('data-tone')).toBe('warn');
    el = await draw(<ScopeBadge scope={{ reason: 'error', unscoped: true, count: 0 }} />);
    expect(el.querySelector('[data-cosre-scope]')?.getAttribute('data-tone')).toBe('err');
    expect(el.textContent).toContain('Kapsam okunamadı');
  });
});
