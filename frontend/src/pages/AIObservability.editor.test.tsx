// @vitest-environment jsdom
//
// v0.10.1484 (operatör kararı) — editör /ai'de YALNIZ "Şüpheli yönlendirmeler"
// panelini görür; admin panellerinin okumaları (çağrılar, istatistik) editöre
// hiç atılmaz. Sidebar: /ai girişi editöre görünür, viewer'a görünmez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const m = vi.hoisted(() => ({ role: 'editor' as string | undefined, calls: [] as string[] }));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));
vi.mock('./ai/RoutingReportPanel', () => ({ RoutingReportPanel: () => <div data-testid="routing-report">RR</div> }));
vi.mock('@/components/Topbar', () => ({ Topbar: ({ title }: { title: string }) => <h1>{title}</h1> }));
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, { get: (_t, k: string) => () => { m.calls.push(k); return new Promise(() => {}); } }),
}));

import AIObservabilityPage from './AIObservability';

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null; m.calls = [];
});

describe('AIObservability editör görünümü (v0.10.1484)', () => {
  it('editör yalnız yönlendirme raporunu görür, admin okumaları atılmaz', () => {
    m.role = 'editor';
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => root!.render(<MemoryRouter><AIObservabilityPage /></MemoryRouter>));
    expect(host.querySelector('[data-testid="routing-report"]')).not.toBeNull();
    expect(host.textContent).toContain('Editör görünümü');
    expect(m.calls).toEqual([]);
  });

  it('sidebar: /ai editöre açık (editorToo), filtre editör rolünü tanır', () => {
    const src = readFileSync(resolve(__dirname, '../components/Sidebar.tsx'), 'utf8');
    expect(src).toMatch(/href: '\/ai',[^\n]*adminOnly: true, editorToo: true/);
    expect(src).toContain("if (n.adminOnly && user?.role !== 'admin' && !(n.editorToo && user?.role === 'editor')) return false;");
  });
});
