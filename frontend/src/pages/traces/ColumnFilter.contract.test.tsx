// @vitest-environment jsdom
//
// ColumnFilter.contract.test.tsx — v0.10.1511 (trace list column filters).
//
// Pinned promises:
//   - a closed header never reads (/api/traces/column-values only on OPEN);
//   - the search box is SERVER-side and DEBOUNCED (PICKER_DEBOUNCE_MS): a
//     burst of keystrokes sends ONE request carrying the final q;
//   - the request carries the page's scope (window + filters) and the column;
//   - + / − / checkbox+Apply / Clear hand the page include / exclude / multi /
//     clear actions (the page applies them with applyColFilter).
// Pattern: createRoot + act (the repo does not use @testing-library), fake timers.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
// Traces shadcn S5 — kabuk artık Radix Popover (Popper ResizeObserver ister); ortak jsdom polyfill'i.
import '@/test/radixJsdom';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { TraceColumnValue, TraceColumnValuesParams } from '@/lib/types';
import type { ColFilterAction } from './colFilter';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const m = vi.hoisted(() => ({ calls: [] as TraceColumnValuesParams[] }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      traceColumnValues: async (p: TraceColumnValuesParams): Promise<TraceColumnValue[]> => {
        m.calls.push(p);
        return [{ value: 'A100', count: 12 }, { value: 'B200', count: 3 }];
      },
    },
  };
});

import { ColumnFilter } from './ColumnFilter';
import { PICKER_DEBOUNCE_MS } from '@/components/usePickerSearch';

const SCOPE = { from: 1, to: 2, service: 'orders-api', filters: '[{"k":"CHANNEL_CODE","op":"=","v":["WEB"]}]' };
const OFF = { active: false, summary: '', selected: [], excluded: [] };

let root: Root;
let host: HTMLDivElement;
let actions: ColFilterAction[];

async function flush(ms = 0) {
  await act(async () => {
    vi.advanceTimersByTime(ms);
    // RQ's notifyManager batches through setTimeout(0): alternate timer ticks and microtasks.
    for (let i = 0; i < 6; i++) { await Promise.resolve(); vi.advanceTimersByTime(1); }
  });
}

function render(colId = 'FUNCTION_CODE') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <ColumnFilter colId={colId} label={colId} status={OFF} scope={SCOPE}
          onAction={a => actions.push(a)} />
      </QueryClientProvider>,
    );
  });
}

function trigger(): HTMLButtonElement {
  return host.querySelector<HTMLButtonElement>('button.tcf-btn')!;
}

function typeInto(input: HTMLInputElement, v: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    set.call(input, v);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  m.calls = [];
  actions = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

describe('ColumnFilter popover contract', () => {
  it('does not fetch until the popover opens', async () => {
    render();
    await flush(5000);
    expect(m.calls).toHaveLength(0);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    act(() => trigger().click());
    await flush(PICKER_DEBOUNCE_MS + 10);
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0]).toMatchObject({ col: 'attr', key: 'FUNCTION_CODE', service: 'orders-api', from: 1, to: 2 });
    expect(m.calls[0].filters).toBe(SCOPE.filters);
    expect(m.calls[0].q).toBeUndefined();
  });

  it('server search is debounced: a keystroke burst sends one request with the final q', async () => {
    render();
    act(() => trigger().click());
    await flush(PICKER_DEBOUNCE_MS + 10);
    expect(m.calls).toHaveLength(1);
    const input = document.querySelector<HTMLInputElement>('[role="dialog"] input[type="search"]')!;
    for (const v of ['A', 'A1', 'A10']) {
      typeInto(input, v);
      await flush(PICKER_DEBOUNCE_MS / 3);
    }
    expect(m.calls).toHaveLength(1); // still inside the debounce window
    await flush(PICKER_DEBOUNCE_MS + 10);
    expect(m.calls).toHaveLength(2);
    expect(m.calls[1].q).toBe('A10');
  });

  it('+ / − / checkbox+Apply hand include / exclude / multi to the page', async () => {
    render();
    act(() => trigger().click());
    await flush(PICKER_DEBOUNCE_MS + 10);
    const dialog = () => document.querySelector('[role="dialog"]')!;
    const btn = (label: string) => dialog().querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!;
    act(() => btn('Include A100').click());
    expect(actions.at(-1)).toEqual({ kind: 'include', value: 'A100' });

    act(() => trigger().click());
    await flush(10);
    act(() => btn('Exclude B200').click());
    expect(actions.at(-1)).toEqual({ kind: 'exclude', value: 'B200' });

    act(() => trigger().click());
    await flush(10);
    const boxes = dialog().querySelectorAll<HTMLInputElement>('input[type="checkbox"]');
    act(() => { boxes[0].click(); });
    act(() => { boxes[1].click(); });
    const apply = [...dialog().querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent?.startsWith('Apply'))!;
    act(() => apply.click());
    expect(actions.at(-1)).toEqual({ kind: 'multi', values: ['A100', 'B200'] });
  });

  it('non-filterable columns render no affordance', () => {
    render('time');
    expect(trigger()).toBeNull();
  });

  it('the status column toggles errors without any fetch', async () => {
    render('status');
    act(() => trigger().click());
    await flush(1000);
    expect(m.calls).toHaveLength(0);
    const box = document.querySelector<HTMLInputElement>('[role="dialog"] input[type="checkbox"]')!;
    act(() => box.click());
    expect(actions.at(-1)).toEqual({ kind: 'errors', on: true });
  });
});
