// @vitest-environment jsdom
// TraceChip.link — v0.10.1968 (operatör: "eskiden log sayfasında trace'e basınca gidiyordu, artık çalışmıyor").
// href verilince çip gerçek bağlantıdır (trace sayfası, yeni sekme çalışır); verilmezse düğme (kart aç/kapa).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { TraceChip } from './TraceChip';
import { traceHref } from '@/lib/traceHref';

const ID = '832034cbbae74c7793db02f77781f9c1';
let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(el: React.ReactElement): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{el}</MemoryRouter>); });
  return host;
}
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

describe('TraceChip href', () => {
  it('href varsa çip trace sayfasına bağlantı (örneklenmiş trace dahil)', () => {
    const el = mount(<TraceChip traceId={ID} preview={{ state: 'sampled' }} href={traceHref(ID)} />);
    const a = el.querySelector('a[data-testid="trace-chip"]');
    expect(a?.getAttribute('href')).toBe(`/trace?id=${ID}`);
  });
  it('href yoksa düğme', () => {
    const el = mount(<TraceChip traceId={ID} />);
    expect(el.querySelector('button[data-testid="trace-chip"]')).not.toBeNull();
    expect(el.querySelector('a')).toBeNull();
  });
});
