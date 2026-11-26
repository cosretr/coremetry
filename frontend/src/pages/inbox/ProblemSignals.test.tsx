// @vitest-environment jsdom
//
// v0.10.2481 — Problem satırının sinyalleri yerinde: kapalı başlar, istek
// yalnız İLK açılışta (kapat / aç tekrar okumaz), en çok 5 satır
// "saat · servis · ne oldu (normalde X → şimdi Y)" + "tümünü gör" Problem
// sayfasına; düğme aria-expanded taşır ve satır tıkını tetiklemez.
// Servis adları SENTETİK.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, useEffect } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { ProblemEvent } from '@/lib/types';

const m = vi.hoisted(() => ({ mounts: 0, items: [] as ProblemEvent[] }));
vi.mock('@/lib/i18n', async (orig) => {
  const mod = await orig<typeof import('@/lib/i18n')>();
  return { ...mod, useLang: () => 'tr' };
});
vi.mock('@/lib/queries/problems', () => ({
  useProblemEvents: (id: string) => {
    useEffect(() => { m.mounts++; }, [id]);
    return { data: { problemId: id, items: m.items, total: m.items.length, detectors: 1 }, isLoading: false, isError: false };
  },
}));

import { ProblemSignalsRow, ProblemSignalsToggle, useRowExpand } from './ProblemSignals';
import { problemSignalLines } from '@/features/anomalies/problemEvents';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ev = (i: number, live = true): ProblemEvent => ({
  detector: 'svc_slowdown', ruleId: `r${i}`, startedAt: (Date.UTC(2026, 10, 26, 10, i)) * 1e6, lastSeen: 0,
  observed: 480, baseline: 120, direction: 'up', live,
});

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.mounts = 0; m.items = []; });

function Harness({ onRow }: { onRow: () => void }) {
  const expand = useRowExpand();
  const id = 'problem:p1';
  return (
    <table><tbody>
      <tr onClick={onRow}><td><ProblemSignalsToggle n={7} open={expand.isOpen(id)} controls="ib-sig-p1" onToggle={() => expand.toggle(id)} /></td></tr>
      {expand.wasOpened(id) && <ProblemSignalsRow id="ib-sig-p1" open={expand.isOpen(id)} problemId="p1" service="checkout-api" href="/inbox?problem=p1" colSpan={3} />}
      <tr><td>sonraki satır</td></tr>
    </tbody></table>
  );
}

describe('ProblemSignals', () => {
  it('kapalı başlar; ilk açılışta yükler, kapat/aç tekrar yüklemez; ≤5 satır + tümünü gör', () => {
    m.items = Array.from({ length: 7 }, (_, i) => ev(i, i !== 0));
    const onRow = vi.fn();
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => { root!.render(<MemoryRouter><Harness onRow={onRow} /></MemoryRouter>); });
    const btn = host.querySelector('button')!;
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    expect(btn.getAttribute('aria-controls')).toBe('ib-sig-p1');
    expect(host.querySelector('[data-testid="problem-signals"]')).toBeNull();
    expect(m.mounts).toBe(0);

    act(() => { btn.click(); });
    expect(onRow).not.toHaveBeenCalled();
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    expect(m.mounts).toBe(1);
    const row = host.querySelector('[data-testid="problem-signals"]') as HTMLElement;
    expect(row.hidden).toBe(false);
    const lis = Array.from(row.querySelectorAll('li'));
    expect(lis).toHaveLength(5);
    expect(lis[0].textContent).toMatch(/^\d\d:\d\d · checkout-api · Yaygın yavaşlama \(normalde 120 → şimdi 480\)$/);
    const all = row.querySelector('a[href="/inbox?problem=p1"]')!;
    expect(all.textContent).toBe('tümünü gör (7)');

    act(() => { btn.click(); });
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    expect((host.querySelector('[data-testid="problem-signals"]') as HTMLElement).hidden).toBe(true);
    act(() => { btn.click(); });
    expect(m.mounts).toBe(1); // ikinci açılış yeni istek değil
  });

  it('olağan değer yoksa parantez yazılmaz; süren önce', () => {
    const e0 = { ...ev(1, false), baseline: 0 };
    const e1 = ev(2, true);
    const { lines, total } = problemSignalLines([e0, e1], 'checkout-api', () => 'HH:MM', 'tr');
    expect(total).toBe(2);
    expect(lines.map(l => l.live)).toEqual([true, false]);
    expect(lines[1].vals).toBeNull();
    expect(lines[0].vals).toBe('normalde 120 → şimdi 480');
  });
});
