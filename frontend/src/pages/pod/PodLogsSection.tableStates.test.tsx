// @vitest-environment jsdom
//
// PodLogsSection.tableStates.test.tsx — v0.10.967 (tablo standardı dilim 5,
// P-1, P6 LogTable `state`).
//
// NE ÇİVİLİYOR: pod sayfasının log bölümü durumlarını LogTable'ın İÇİNDE
// çizer, sütun başlıkları yerinde:
//   • boş: CH gövde-araması ipucu + "Loglar sayfasında" BAĞLANTISI durum
//     satırının detail yuvasında (silinmedi, tablonun dışına park edilmedi);
//   • arama ya da seviye seçiliyken boş sonuç "eşleşme yok" — aynı ipucu ve
//     bağlantı orada da;
//   • backend yanıtsız ve degraded (yavaş/erişilemez) = HATA türü, "log yok"
//     değil; degraded nedeni satırda;
//   • yenileme düşüp önbellek eski satırları tutuyorsa hata görünür; bayat
//     satır, bayat alt not ("N satır") ve bayat seviye sayıları kalmaz.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogRow } from '@/lib/types';

const m = vi.hoisted(() => ({
  logs: [] as unknown[],
  degraded: false,
  fail: false,
  pending: false,
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      logs: () => {
        if (m.pending) return new Promise(() => {});
        if (m.fail) return Promise.reject(new Error('boom'));
        return Promise.resolve(m.degraded
          ? { total: 0, logs: [], degraded: true, reason: 'ES timeout 30s' }
          : { total: m.logs.length, logs: m.logs });
      },
    },
  };
});

import { PodLogsSection } from './PodLogsSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const LOGS_LINK = '/logs?pod=p-1';
const log = (id: number, severityText = 'ERROR'): LogRow => ({
  id, timestamp: 1.7e18 + id, severity: 17, severityText, body: `line ${id}`, serviceName: 'checkout',
  traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
});

let host: HTMLElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(url = '/pod?logs=1'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <PodLogsSection pod="p-1" from={1} to={2} logsLink={LOGS_LINK} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
const stateRow = (el: HTMLElement) => el.querySelector('table tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr:not([data-dt-state])').length;
const detailLink = (row: HTMLElement) => row.querySelector('[data-dt-state-detail] a') as HTMLAnchorElement | null;

beforeEach(() => { m.logs = []; m.degraded = false; m.fail = false; m.pending = false; });
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('PodLogsSection — durumlar LogTable\'ın içinde (v0.10.967)', () => {
  it('yükleniyor: iskelet satırı tabloda, sütun başlıkları yerinde', async () => {
    m.pending = true;
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('table thead th').length).toBeGreaterThan(0);
  });

  it('boş: ipucu + "Loglar sayfasında" bağlantısı detail yuvasında, tek hücrede', async () => {
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(row.textContent).toContain('Bu pod için bu pencerede log yok.');
    expect(row.textContent).toContain('pod adı log gövdesinde aranır');
    expect(detailLink(row)?.getAttribute('href')).toBe(LOGS_LINK);
    expect(detailLink(row)?.textContent).toBe('Loglar sayfasında');
  });

  it('arama / seviye seçili ve boş: eşleşme yok, bağlantı yine detail\'de', async () => {
    const el = await mount('/pod?logs=1&plvl=error&plq=timeout');
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(row.textContent).toContain('süzgeçle eşleşen log yok');
    expect(detailLink(row)?.getAttribute('href')).toBe(LOGS_LINK);
  });

  it('backend yanıtsız: hata türü, "log yok" değil, bağlantı yok', async () => {
    m.fail = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain("Log backend'i yanıt vermedi.");
    expect(row.textContent).not.toContain('log yok');
    expect(detailLink(row)).toBeNull();
  });

  it('degraded: hata türü, nedeni satırda', async () => {
    m.degraded = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain('liste boş gösterilmedi: ES timeout 30s');
  });

  it('yenileme düştü, önbellek eski satırları tutuyor: hata görünür; bayat satır, not ve sayılar yok', async () => {
    m.logs = [log(1), log(2, 'WARN')];
    const el = await mount();
    expect(dataRows(el)).toBeGreaterThanOrEqual(2);
    expect(el.querySelector('.pod-cap')?.textContent).toContain('2 satır');
    expect(el.textContent).toContain('ERROR · 1');
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['pod-logs'] }); });
    await wait();
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(dataRows(el)).toBe(0);
    expect(el.querySelector('.pod-cap')).toBeNull();
    expect(el.textContent).not.toContain('ERROR · 1');
  });
});
