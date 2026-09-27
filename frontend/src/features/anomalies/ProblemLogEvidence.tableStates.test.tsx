// @vitest-environment jsdom
//
// ProblemLogEvidence.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR: Problem detayındaki "Log desenleri" tablosu açıldığında
// yükleniyor / hata / degraded / boş DURUMLARINI tablonun İÇİNDE çizer
// (başlık kalır); kısmi-sonuç rozeti tablonun üstünde kalır.
//   • Yenileme hatası, önbellekte bayat desen olsa da HATA satırı gösterir —
//     eski kod hata rozeti ile bayat satırları yan yana basıyordu.
//   • degraded ("hiçbir sayı gerçek değil") hata satırıdır, sebebiyle.
//   • Sayım satırı ve kısmi-sonuç rozeti bayat yanıtı anlattığı için hatada
//     çizilmez.
// NEDEN GERÇEK MOUNT: kapı tip-doğru biçimde yanlış yazılabilir
// (`rows.length === 0`); tsc de eslint de sessiz kalır.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { LogPatternGroup, LogPatternsResult } from '@/lib/types';

interface FakeQ { data: LogPatternsResult | undefined; isPending: boolean; isError: boolean; error: unknown }
const m = vi.hoisted(() => ({
  q: { data: undefined, isPending: true, isError: false, error: null } as FakeQ,
}));
vi.mock('@/lib/queries/logs', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useLogsPatterns: () => m.q };
});

import { ProblemLogEvidence } from './ProblemLogEvidence';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const group = (hash: string): LogPatternGroup => ({
  hash, template: `timeout <*> ${hash}`, count: 12, sample: `timeout 30s ${hash}`, severity: 17,
  firstSeen: 1, lastSeen: 2, services: ['checkout'], serviceCount: 1, query: `"timeout" "${hash}"`,
});
const result = (groups: LogPatternGroup[], over: Partial<LogPatternsResult> = {}): LogPatternsResult => ({
  groups, sampled: 40, total: 40, cap: 500, truncated: false, distinct: groups.length, ...over,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function mountOpen(): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root!.render(
      <MemoryRouter>
        <ProblemLogEvidence service="checkout" startedAt={1_700_000_000_000_000_000} linkWindow="1h" />
      </MemoryRouter>,
    );
  });
  // Varsayılan KAPALI (ES maliyeti); açılır düğmeye basılınca tablo çizilir.
  const btn = host.querySelector('button') as HTMLButtonElement;
  act(() => { btn.click(); });
  return host;
}

const stateRow = (el: HTMLElement) => el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('tbody tr:not([data-dt-state])').length;
const headers = (el: HTMLElement) => [...el.querySelectorAll('thead th')].map(th => th.textContent ?? '');

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  m.q = { data: undefined, isPending: true, isError: false, error: null };
});

describe('ProblemLogEvidence — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık + iskelet satırı', () => {
    const el = mountOpen();
    expect(headers(el).join('|')).toContain('Şiddet');
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(dataRows(el)).toBe(0);
  });

  it('hata (ilk okuma): sunucu metniyle hata satırı', () => {
    m.q = { data: undefined, isPending: false, isError: true, error: new Error('ES 503') };
    const el = mountOpen();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('error');
    expect(row?.textContent).toContain('Log kanıtı alınamadı: ES 503');
    expect(headers(el).join('|')).toContain('Kayıt');
  });

  it('yenileme hatası + önbellekte bayat desenler: hata satırı, bayat satır/sayım/rozet YOK', () => {
    m.q = { data: result([group('a'), group('b')], { partial: true, shardsFailed: 2 }), isPending: false, isError: true, error: new Error('timeout') };
    const el = mountOpen();
    expect(stateRow(el)?.dataset.dtState, 'bayat satırlar hatayı gizledi').toBe('error');
    expect(dataRows(el)).toBe(0);
    expect(el.textContent).not.toContain('ERROR+ log ·');
    expect(el.textContent).not.toContain('kısmi sonuç');
  });

  it('degraded: tablo içinde hata satırı, sebep korunur', () => {
    m.q = { data: result([group('a')], { degraded: true, reason: 'ES yavaş' }), isPending: false, isError: false, error: null };
    const el = mountOpen();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('error');
    expect(row?.textContent).toContain('hiçbir sayı gerçek değil');
    expect(row?.textContent).toContain('ES yavaş');
    expect(dataRows(el)).toBe(0);
  });

  it('boş: servis adıyla boş satır; kısmi sonuç rozeti tablonun ÜSTÜNDE kalır', () => {
    m.q = { data: result([], { partial: true, shardsFailed: 1 }), isPending: false, isError: false, error: null };
    const el = mountOpen();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toBe('Bu pencerede checkout için ERROR+ log deseni yok');
    const badge = el.querySelector('.badge.b-warn');
    expect(badge?.textContent).toContain('kısmi sonuç · 1 shard');
    expect(badge?.closest('table')).toBeNull();
  });

  it('satırlar: durum satırı yok, desen bağlantısı ve sayım satırı var', () => {
    m.q = { data: result([group('a'), group('b')]), isPending: false, isError: false, error: null };
    const el = mountOpen();
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(2);
    expect(el.querySelector('tbody a[href*="/logs"]')).not.toBeNull();
    expect(el.textContent).toContain('40 ERROR+ log');
  });
});
