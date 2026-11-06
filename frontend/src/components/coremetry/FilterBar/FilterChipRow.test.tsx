// @vitest-environment jsdom
//
// coremetry/FilterBar FilterChipRow + FilterChipEditor — v0.10.1854 (Log
// Search Faz 2 dilim 2.3). Hap eylemleri (sabitle / ters / kapat / sil) tam
// listeyi onChange'e verir; aralık hapı ters çevrilmez; ✎ düzenleyici sunucu
// önerisini gecikmeli + iptal sinyaliyle ister ve Uygula yeni çipi yazar.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { FilterChipRow, type FilterChipRowProps } from './index';
import type { FilterChip } from '@/lib/filterChips';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null; let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; document.body.innerHTML = ''; vi.useRealTimers(); });

function render(p: Partial<FilterChipRowProps> & { chips: FilterChip[] }) {
  const onChange = vi.fn();
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => root!.render(<FilterChipRow onChange={onChange} {...p} />));
  return { h: host, onChange };
}
const btn = (label: string) => document.querySelector(`button[aria-label^="${label}"]`) as HTMLButtonElement;
const CHIPS: FilterChip[] = [
  { key: 'level', op: '=', value: 'ERROR', pinned: true },
  { key: 'CHANNEL_CODE', op: '!=', value: 'WEB' },
  { key: 'pod', op: '=', value: 'orders-api-7d9f6-x2k4p', disabled: true },
  { key: 'http.status', op: '>=', value: '500' },
];

describe('FilterChipRow', () => {
  it('etiketler: sabit / DEĞİL / kapalı', () => {
    const { h } = render({ chips: CHIPS });
    const pills = [...h.querySelectorAll('[data-testid="filter-chip"]')];
    expect(pills).toHaveLength(4);
    expect(pills[0].textContent).toContain('⊙ sabit');
    expect(pills[0].getAttribute('data-pinned')).toBe('1');
    expect(pills[1].textContent).toContain('DEĞİL');
    expect(pills[2].textContent).toContain('kapalı');
    expect(pills[2].getAttribute('data-off')).toBe('1');
  });

  it('sabitle / ters / kapat / sil → tam liste', () => {
    const { onChange } = render({ chips: CHIPS });
    act(() => btn('Sabitle: DEĞİL CHANNEL_CODE').click());
    expect(onChange).toHaveBeenLastCalledWith([CHIPS[0], { ...CHIPS[1], pinned: true }, CHIPS[2], CHIPS[3]]);
    act(() => btn('Sabitlemeyi kaldır: level').click());
    expect(onChange.mock.lastCall![0][0]).toEqual({ ...CHIPS[0], pinned: false });
    act(() => btn('Ters çevir: DEĞİL CHANNEL_CODE').click());
    expect(onChange.mock.lastCall![0][1]).toEqual({ key: 'CHANNEL_CODE', op: '=', value: 'WEB', disabled: false });
    act(() => btn('Aç: pod').click());
    expect(onChange.mock.lastCall![0][2].disabled).toBe(false);
    act(() => btn('Filtreyi kaldır: level').click());
    expect(onChange.mock.lastCall![0]).toEqual(CHIPS.slice(1));
  });

  it('aralık hapı ters çevrilmez', () => {
    render({ chips: CHIPS });
    expect(btn('Ters çevir: http.status').disabled).toBe(true);
  });

  it('✎ düzenleyici: öneri gecikmeli + sinyalli; Uygula çipi değiştirir, sabit bayrağı korunur', async () => {
    vi.useFakeTimers();
    const suggest = vi.fn((_k: string, _q: string, signal: AbortSignal) => { expect(signal).toBeInstanceOf(AbortSignal); return Promise.resolve(['FATAL', 'ERROR']); });
    const { onChange } = render({ chips: CHIPS, suggestValues: suggest });
    act(() => btn('Düzenle: level').click());
    expect(suggest).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(200); await Promise.resolve(); });
    expect(suggest).toHaveBeenCalledTimes(1);
    expect(suggest.mock.calls[0].slice(0, 2)).toEqual(['level', 'ERROR']);
    const fatal = [...document.querySelectorAll('button')].find(b => b.textContent === 'FATAL')!;
    act(() => fatal.click());
    const apply = [...document.querySelectorAll('button')].find(b => b.textContent === 'Uygula')!;
    act(() => apply.click());
    expect(onChange.mock.lastCall![0][0]).toEqual({ key: 'level', op: '=', value: 'FATAL', pinned: true });
  });
});
