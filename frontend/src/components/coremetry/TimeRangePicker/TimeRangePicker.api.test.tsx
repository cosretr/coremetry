// @vitest-environment jsdom
//
// coremetry/TimeRangePicker API — v0.10.1617 (shadcn Faz 1 R9, plan §6).
import { describe, it, expect, expectTypeOf, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { TimeRange } from '@/lib/types';
import { QUICK_PRESETS } from '@/lib/rangePicker';
import { TimeRangePicker, windowOptions, type TimeRangePickerProps } from './index';

vi.mock('@/components/TimeRangePicker', () => ({
  TimeRangePicker: ({ value }: { value: TimeRange }) => <span data-testid="global-picker">{value.preset}</span>,
}));

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; });
function render(p: TimeRangePickerProps) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<TimeRangePicker {...p} />); });
  return host;
}

describe('coremetry/TimeRangePicker', () => {
  it('tip: bugünkü küresel seçicinin { value, onChange } biçimi alt küme', () => {
    expectTypeOf<Pick<TimeRangePickerProps, 'value' | 'onChange'>>().toMatchTypeOf<{ value: TimeRange; onChange: (r: TimeRange) => void }>();
  });
  it('variant=global (varsayılan) bugünkü components/TimeRangePicker\'ı çizer', () => {
    const h = render({ value: { preset: '1h' }, onChange: () => {} });
    expect(h.querySelector('[data-testid="global-picker"]')!.textContent).toBe('1h');
  });
  it('windowOptions: QUICK_PRESETS alt kümesi, onun sırasıyla; bilinmeyen düşer', () => {
    expect(windowOptions()).toEqual(QUICK_PRESETS);
    expect(windowOptions(['24h', '1h', 'bogus'])).toEqual(QUICK_PRESETS.filter(p => p === '1h' || p === '24h'));
  });
  it('variant=window: native-select, seçim onChange({ preset })', () => {
    const onChange = vi.fn();
    const h = render({ value: { preset: '1h' }, onChange, variant: 'window', presets: ['1h', '24h'], label: 'Trend window' });
    const sel = h.querySelector('select[data-slot="native-select"]') as HTMLSelectElement;
    expect(sel.getAttribute('aria-label')).toBe('Trend window');
    expect([...sel.options].map(o => o.value)).toEqual(['1h', '24h']);
    act(() => { sel.value = '24h'; sel.dispatchEvent(new Event('change', { bubbles: true })); });
    expect(onChange).toHaveBeenCalledWith({ preset: '24h' });
  });
});
