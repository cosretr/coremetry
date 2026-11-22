// @vitest-environment jsdom
//
// v0.10.2451 — span'siz (metrik-yalnız) servis sayfası: boş span bölümleri
// hata değil, "no traces in window" notuyla ilan edilir; okuma hatası ve
// yükleme anında not çıkmaz (QueryError / Spinner kendi işini yapar).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { ServiceNoTracesNote } from './ServiceNoTracesNote';
import { serviceHasNoTraces, type NoTracesInput } from './serviceNoTraces';
import type { Service } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const base: NoTracesInput = { loading: false, bundleErr: null, info: null, operations: [], endpoints: [] };
const info: Service = { name: 'svc-a', spanCount: 10, errorCount: 0, errorRate: 0, avgDurationMs: 1, p99DurationMs: 2, apdex: 1, apdexThresholdMs: 200 };

describe('serviceHasNoTraces', () => {
  it('yalnız okuma bitti, hata yok ve span verisi yokken', () => {
    expect(serviceHasNoTraces(base)).toBe(true);
    expect(serviceHasNoTraces({ ...base, loading: true })).toBe(false);
    expect(serviceHasNoTraces({ ...base, bundleErr: 'boom' })).toBe(false);
    expect(serviceHasNoTraces({ ...base, info })).toBe(false);
    expect(serviceHasNoTraces({ ...base, operations: [{}] })).toBe(false);
    expect(serviceHasNoTraces({ ...base, endpoints: [{}] })).toBe(false);
  });
});

let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { if (root) act(() => root!.unmount()); host?.remove(); root = null; host = null; });

describe('ServiceNoTracesNote', () => {
  it('span yokken status notu, varken hiçbir şey', () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => root!.render(<ServiceNoTracesNote {...base} />));
    const note = host.querySelector('[data-testid="service-no-traces"]')!;
    expect(note.getAttribute('role')).toBe('status');
    expect(note.textContent).toContain('No traces in this window');
    act(() => root!.render(<ServiceNoTracesNote {...base} info={info} />));
    expect(host.querySelector('[data-testid="service-no-traces"]')).toBeNull();
  });
});
