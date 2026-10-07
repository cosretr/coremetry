// @vitest-environment jsdom
//
// RootCausePanel.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR: "Blast radius" statik tablosunun boş hâli (sunucu
// totalCallers > 0 dedi ama çağıran listesi boş geldi) tablonun İÇİNDE,
// başlık dururken standart DataTableState satırı — dilim 4'ün elle yazılmış
// ara işaretlemesi değil (DOM ikisini ayıramaz, aynı işaretleme; bu ayrımı
// kaynak çivisi tutar). Panel düzeyi yükleniyor / hata (yanıt hangi
// bölümlerin var olduğunu belirliyor) dışarıda kalır.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { BlastRadiusCaller, RootCause } from '@/lib/types';

const rcHolder = vi.hoisted(() => ({ value: null as unknown }));
vi.mock('@/lib/api', () => ({
  api: {
    problemRootCauseCore: () => Promise.resolve(rcHolder.value),
    problemRootCauseBubbleUp: () => Promise.resolve(null),
  },
}));

import { RootCausePanel } from './RootCausePanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function rc(callers: BlastRadiusCaller[]): RootCause {
  return {
    problemId: 'p1', service: 'checkout', metric: 'error_rate',
    startedAt: 1.7e18, fromNs: 1.7e18, toNs: 1.7e18 + 6e11,
    correlations: [],
    blastRadius: {
      service: 'checkout', windowSec: 600, totalCallers: 2, cascadingCallers: 1,
      totalRps: 3, totalErrorsPerSec: 0.1, callers,
    },
  };
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(value: RootCause): Promise<HTMLElement> {
  rcHolder.value = value;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter><RootCausePanel problemId="p1" service="checkout" /></MemoryRouter>);
  });
  await act(async () => { await Promise.resolve(); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const blastTable = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('table')).find(t => t.querySelector('thead')?.textContent?.includes('Caller'));

describe('RootCausePanel — blast radius boş hâli tablonun içinde (P-2)', () => {
  it('çağıran listesi boş: başlık durur, tek boş satır colSpan = 4 <th>, servis adı cümlede', async () => {
    const el = await mount(rc([]));
    const table = blastTable(el);
    expect(table, 'blast radius tablosu çizilmedi').toBeDefined();
    expect(table!.querySelectorAll('thead th')).toHaveLength(4);
    const rows = table!.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(1);
    const row = rows[0] as HTMLTableRowElement;
    expect(row.getAttribute('data-dt-state')).toBe('empty');
    expect(row.querySelector('td')!.colSpan).toBe(4);
    expect(row.querySelector('td')!.className).toContain('dt-state');
    expect(row.textContent).toBe('Bu pencerede çağıran yok — checkout bir giriş noktası');
    expect(row.hasAttribute('data-row-action')).toBe(false);
  });

  it('çağıranlar varken: satırlar, durum satırı yok', async () => {
    const el = await mount(rc([
      { service: 'gateway', calls: 100, errors: 5, rps: 2, errorRate: 5, hasOpenProblem: true },
      { service: 'billing', calls: 50, errors: 0, rps: 1, errorRate: 0, hasOpenProblem: false },
    ]));
    const table = blastTable(el)!;
    expect(table.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(table.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(table.textContent).toContain('gateway');
  });

  // DOM, DataTableState'in boş satırını dilim 4'ün elle yazılmış ara
  // işaretlemesinden AYIRAMAZ (ikisi aynı <tr data-dt-state><td.dt-state>);
  // göçün geri alınmasını yalnız kaynak yakalar.
  it('kaynak: statik durum satırı DataTableState, elle yazılmış data-dt-state yok', () => {
    const src = readFileSync(resolve(__dirname, './RootCausePanel.tsx'), 'utf8');
    expect(src).not.toMatch(/data-dt-state=/);
    expect(src.match(/<DataTableState colSpan=\{4\}/g) ?? []).toHaveLength(1);
  });
});
