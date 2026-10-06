// @vitest-environment jsdom
//
// ProblemNewLogTemplates.render — v0.10.1113 (operatör onaylı kuyruk maddesi
// "Log şablonu kök neden bağlantısı").
//
// NE ÇİVİLİYOR:
//   • satırlar: şablon (tek satır, title'da tam metin + örnek), servis,
//     başlangıca göre "+40 sn" / "−2 dk", örnek sayısı, "Logları aç" pivotu
//     (servis + sunucunun arama metni);
//   • başlıkta bakılan servis kümesi;
//   • boş liste / ilk okuma hatası → bölüm HİÇ yok (boş kart yok), hatada
//     yeniden deneme yok (retry: 0);
//   • düşen arka plan tazelemesi eldeki satırları silmez;
//   • tek istek, Problem kimliğiyle.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider, keepPreviousData } from '@tanstack/react-query';
import type { Problem, ProblemLogTemplatesResponse } from '@/lib/types';

const T0 = 1_759_399_980 * 1e9;

const m = vi.hoisted(() => ({
  calls: [] as string[],
  mode: 'ok' as 'ok' | 'empty' | 'fail',
}));

function resp(): ProblemLogTemplatesResponse {
  return {
    services: ['payments-api', 'svc-orders'],
    startedAt: T0, fromNs: T0 - 600e9, toNs: T0 + 300e9,
    templates: [
      {
        templateId: 't-near', template: 'ledger write rejected for account <*> because reservation conflict',
        service: 'payments-api', firstSeen: T0 - 40e9, lastSeen: T0 + 60e9, totalCount: 734, offsetSec: -40,
        query: '"ledger write rejected for account" AND "because reservation conflict"',
        sample: 'ledger write rejected for account 42 because reservation conflict',
      },
      {
        templateId: 't-far', template: 'upstream svc-orders returned <*> after <*> ms',
        service: 'svc-orders', firstSeen: T0 - 120e9, lastSeen: T0, totalCount: 7, offsetSec: -120,
        query: '"upstream svc-orders returned" AND "after"',
      },
    ],
  };
}

vi.mock('@/lib/api', () => ({
  api: {
    problemLogTemplates: async (id: string) => {
      m.calls.push(id);
      switch (m.mode) {
        case 'fail': throw new Error('HTTP 500: known templates read failed');
        case 'empty': return { ...resp(), templates: [] };
        default: return resp();
      }
    },
  },
  isCanceled: () => false,
}));

import { ProblemNewLogTemplates } from './ProblemNewLogTemplates';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const problem = (over: Partial<Problem> = {}): Problem => ({
  id: 'p-1', ruleId: 'svc-slowdown:payments-api', ruleName: 'Yaygın yavaşlama', severity: 'critical',
  service: 'payments-api', metric: 'p99_ms', value: 9000, threshold: 120, status: 'resolved',
  description: '', startedAt: T0, resolvedAt: T0 + 900e9,
  ...over,
} as Problem);

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient | null = null;

// retryDefault: istemcinin VARSAYILAN yeniden denemesi — kancanın kendi
// `retry: 0`ının onu ezdiğini görmek için 3 ile de kurulur.
async function mount(p: Problem, retryDefault: number | false = false): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: retryDefault, retryDelay: 0, placeholderData: keepPreviousData } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc!}>
        <MemoryRouter>
          <ProblemNewLogTemplates problem={p} window={{ fromNs: T0 - 3600e9, toNs: T0 + 1500e9 }} />
        </MemoryRouter>
      </QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 30)); });
  return host;
}

beforeEach(() => { m.calls = []; m.mode = 'ok'; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  qc?.clear();
  host = null; root = null; qc = null;
});

describe('ProblemNewLogTemplates', () => {
  it('satırlar: şablon, servis, göreli zaman, sayı ve pivot', async () => {
    const el = await mount(problem());
    expect(m.calls).toEqual(['p-1']);
    const head = el.querySelector('.pb-sect .h')?.textContent ?? '';
    expect(head).toContain('Başlangıçta doğan log şablonları');
    expect(head).toContain('payments-api, svc-orders');
    const rows = Array.from(el.querySelectorAll<HTMLElement>('.pd-newtpl-row'));
    expect(rows.map(r => r.dataset.templateId)).toEqual(['t-near', 't-far']);

    const tpl = rows[0].querySelector<HTMLElement>('.pd-newtpl-tpl')!;
    expect(tpl.textContent).toBe('ledger write rejected for account <*> because reservation conflict');
    expect(tpl.title).toContain('Örnek satır:');
    expect(tpl.title).toContain('account 42');
    expect(rows[0].querySelector('.pd-newtpl-svc')?.textContent).toBe('payments-api');
    const off = rows[0].querySelector<HTMLElement>('.pd-newtpl-off')!;
    expect(off.textContent).toBe('−40 sn');
    expect(off.title).toContain('başlangıçtan 40 sn önce doğdu');
    expect(rows[0].textContent).toContain('734 örnek');
    expect(rows[1].querySelector('.pd-newtpl-off')?.textContent).toBe('−2 dk');

    const a = rows[0].querySelector<HTMLAnchorElement>('a')!;
    expect(a.textContent).toBe('Logları aç');
    const u = new URL(a.getAttribute('href')!, 'http://x');
    expect(u.pathname).toBe('/logs');
    expect(u.searchParams.get('service')).toBe('payments-api');
    expect(u.searchParams.get('q')).toBe('"ledger write rejected for account" AND "because reservation conflict"');
    expect(u.searchParams.get('range')).toBe(`custom:${(T0 - 100e9) / 1e6}-${(T0 + 1500e9) / 1e6}`);
    expect(new URL(rows[1].querySelector('a')!.getAttribute('href')!, 'http://x').searchParams.get('service')).toBe('svc-orders');
  });

  it('boş liste → bölüm yok (boş kart yok)', async () => {
    m.mode = 'empty';
    const el = await mount(problem());
    expect(m.calls).toEqual(['p-1']);
    expect(el.innerHTML).toBe('');
  });

  it('ilk okuma hatası → bölüm yok, yeniden deneme fırtınası yok (retry: 0)', async () => {
    m.mode = 'fail';
    const el = await mount(problem(), 3); // istemci varsayılanı 3 deneme
    expect(el.innerHTML).toBe('');
    expect(m.calls).toEqual(['p-1']); // kancanın retry: 0'ı varsayılanı ezer
  });

  it('arka plan tazelemesi düşerse eldeki satırlar kalır', async () => {
    const el = await mount(problem());
    expect(el.querySelectorAll('.pd-newtpl-row')).toHaveLength(2);
    m.mode = 'fail';
    await act(async () => { await qc!.refetchQueries(); });
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    expect(m.calls).toEqual(['p-1', 'p-1']);
    expect(qc!.getQueryState(['problems', 'log-templates', 'p-1'])?.status).toBe('error');
    expect(el.querySelectorAll('.pd-newtpl-row')).toHaveLength(2);
  });
});
