// @vitest-environment jsdom
// TraceHonesty.test.tsx — v0.10.922 (sade palet adım 1). K5: sağlıklı hâl
// NÖTR, ama söylenir. Tek kök + orphan yok → "W3C tracecontext: complete"
// yeşil değil nötr çip (bilgi renge/yokluğa bırakılmaz); orphan varken
// "complete" DENMEZ (eski yeşil çip derdi). Sapma çipleri (kök yok, orphan,
// Tempo fallback) kalır; örnekleme bilgisi nötr çip olarak görünür.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { TraceHonesty } from './TraceHonesty';
import type { SpanRow } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

function span(p: Partial<SpanRow> & { spanId: string }): SpanRow {
  return { traceId: 't1', parentSpanId: '', serviceName: 'orders', name: 'op', startTime: 0, endTime: 10, statusCode: 'ok', kind: 'server', attributes: {}, ...p } as unknown as SpanRow;
}
const HEALTHY = [span({ spanId: 'r' }), span({ spanId: 'c', parentSpanId: 'r' })];

describe('TraceHonesty × sade palet', () => {
  it('sağlıklı trace → nötr "complete" çipi (yeşil değil)', () => {
    act(() => { root.render(<TraceHonesty spans={HEALTHY} source="clickhouse" />); });
    const chip = Array.from(host.querySelectorAll<HTMLElement>('span[title]'))
      .find(el => el.textContent === 'W3C tracecontext: complete');
    expect(chip).toBeDefined();
    expect(chip!.style.color).toBe('var(--text2)');
    expect(chip!.outerHTML).not.toContain('--ok');
  });
  it('orphan → Provenance satırı + uyarı çipi; "complete" yine yok', () => {
    const spans = [...HEALTHY, span({ spanId: 'o', parentSpanId: 'gone' })];
    act(() => { root.render(<TraceHonesty spans={spans} source="clickhouse" />); });
    expect(host.textContent).toContain('Provenance');
    expect(host.textContent).toContain('1 orphan span');
    expect(host.textContent).not.toContain('complete');
  });
  it('kök yok ve Tempo fallback çipleri kalır', () => {
    const spans = [span({ spanId: 'a', parentSpanId: 'x' })];
    act(() => { root.render(<TraceHonesty spans={spans} source="tempo" />); });
    expect(host.textContent).toContain('No root span');
    expect(host.textContent).toContain('source: Tempo fallback');
  });
  it('örnekleme bilgisi nötr çip: --text2 metin, --border çizgi, zemin yok', () => {
    const spans = [span({ spanId: 'r', attributes: { 'sampling.priority': '1' } })];
    act(() => { root.render(<TraceHonesty spans={spans} />); });
    const chip = Array.from(host.querySelectorAll<HTMLElement>('span[title]'))
      .find(el => el.textContent === 'sampling.priority 1');
    expect(chip).toBeDefined();
    expect(chip!.style.color).toBe('var(--text2)');
    expect(chip!.style.border).toContain('var(--border)');
    expect(chip!.style.background).toBe('');
  });
});
