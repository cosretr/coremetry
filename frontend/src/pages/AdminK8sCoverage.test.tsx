// @vitest-environment jsdom
// AdminK8sCoverage.test.tsx — v0.10.964 (Rollouts v2 P5.3,
// docs/rollouts/v2-audit.md §9): servis tablosunda sürüm/ortam sayaçları
// (`tag` container.image.tag, `ver` service.version, `env`
// deployment.environment.name — yalnız yeni anahtar, eski
// deployment.environment sayılmaz; §11.8 T1, §9.3.2).
//
// Sözleşme:
//   1. Üç yeni kolon başlıkta kısa etiketle; tam anahtar başlığın title'ında.
//   2. Sayılar sağa yaslı (`num`), renk yalnız SAPMADA: kısmi warn, yok err;
//      tam nötr (hiyerarşi rengi), ölçülmedi soluk.
//   3. Yükte OLMAYAN alan (eski sunucu / eski önbellek yükü) "—" +
//      "ölçülmedi" basar, ✗ ("yok") DEĞİL; filo özetinde hiçbir kovaya girmez.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { K8sCoverage, K8sCoverageRow, PodInventory } from '@/lib/types';

const m = vi.hoisted(() => ({ cov: null as K8sCoverage | null }));
const calls = vi.hoisted(() => ({
  k8sCoverage: async (): Promise<K8sCoverage> => {
    if (!m.cov) throw new Error('kapsama yok');
    return m.cov;
  },
  k8sPods: async (): Promise<PodInventory> => ({ rows: [], sampleRows: 1000, windowSec: 3600 }),
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), ...calls } };
});

import AdminK8sCoveragePage from './AdminK8sCoverage';

const base = (service: string, sampled: number): K8sCoverageRow => ({
  service, sampled,
  namespace: sampled, deployment: sampled, pod: sampled, podUid: 0, node: sampled, container: sampled, cluster: sampled,
  replicaset: sampled, image: sampled, clusterK8s: sampled, clusterOpenshift: 0,
});

const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

let host: HTMLElement | null = null;
let root: Root | null = null;
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><AdminK8sCoveragePage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}

// Servis tablosu: storageKey'i thead'de (data-table-id).
const serviceTable = (el: HTMLElement) =>
  el.querySelector('thead[data-table-id="admin-k8s-coverage"]')!.closest('table')!;

// Kolonun başlık indeksi — kısa etiketin title'ındaki tam anahtar ile.
function colIndex(table: HTMLTableElement, titlePrefix: string): number {
  const ths = Array.from(table.querySelectorAll('thead th'));
  const i = ths.findIndex(th => th.querySelector(`span[title^="${titlePrefix}"]`) !== null);
  expect(i, `başlık yok: ${titlePrefix}`).toBeGreaterThan(-1);
  return i;
}

function cell(table: HTMLTableElement, service: string, titlePrefix: string): HTMLTableCellElement {
  const idx = colIndex(table, titlePrefix);
  const tr = Array.from(table.querySelectorAll('tbody tr'))
    .find(r => r.querySelector('td')?.textContent === service);
  expect(tr, `satır yok: ${service}`).toBeTruthy();
  return tr!.querySelectorAll('td')[idx] as HTMLTableCellElement;
}

// Filo özeti: attr hücresi → (Tam, Kısmi, Yok).
function fleet(el: HTMLElement, attr: string): [string, string, string] {
  const tr = Array.from(el.querySelectorAll('tbody tr'))
    .find(r => r.querySelector('td.mono')?.textContent === attr);
  expect(tr, `filo satırı yok: ${attr}`).toBeTruthy();
  const tds = tr!.querySelectorAll('td');
  return [tds[1].textContent ?? '', tds[2].textContent ?? '', tds[3].textContent ?? ''];
}

const VER = 'service.version';
const TAG = 'container.image.tag';
const ENV = 'deployment.environment.name';

beforeEach(() => {
  m.cov = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('AdminK8sCoverage — sürüm/ortam sayaçları (v0.10.964)', () => {
  it('üç yeni kolon kısa etiketle başlıkta; tam anahtar title\'da', async () => {
    m.cov = { rows: [{ ...base('svc-a', 100), serviceVersion: 100, imageTag: 100, envName: 100 }], sampleRows: 200_000, windowSec: 3600 };
    const t = serviceTable(await mount());
    const label = (p: string) => t.querySelectorAll('thead th')[colIndex(t, p)].textContent;
    expect(label(TAG)).toContain('tag');
    expect(label(VER)).toContain('ver');
    expect(label(ENV)).toContain('env');
    // env başlığı yalnız yeni anahtarı söyler; eski yazım / deploy_env yok.
    const envTitle = t.querySelector(`span[title^="${ENV}"]`)!.getAttribute('title') ?? '';
    expect(envTitle).toBe(`${ENV} — env`);
    expect(envTitle).not.toContain('deploy_env');
    expect(envTitle).not.toContain('deployment.environment |');
  });

  it('tam / kısmi / yok: sağa yaslı, renk yalnız sapmada', async () => {
    m.cov = {
      rows: [{ ...base('svc-a', 100), serviceVersion: 100, imageTag: 40, envName: 0 }],
      sampleRows: 200_000, windowSec: 3600,
    };
    const t = serviceTable(await mount());

    const ver = cell(t, 'svc-a', VER);
    expect(ver.textContent).toBe('✓');
    expect(ver.classList.contains('num')).toBe(true);
    expect(ver.classList.contains('cell-err')).toBe(false);
    expect(ver.classList.contains('cell-warn')).toBe(false);
    expect(ver.getAttribute('title')).toBe('100/100 (%100)');

    const tag = cell(t, 'svc-a', TAG);
    expect(tag.textContent).toBe('%40');
    expect(tag.classList.contains('num')).toBe(true);
    expect(tag.classList.contains('cell-warn')).toBe(true);

    const env = cell(t, 'svc-a', ENV);
    expect(env.textContent).toBe('✗');
    expect(env.classList.contains('num')).toBe(true);
    expect(env.classList.contains('cell-err')).toBe(true);
  });

  it('yükte olmayan alanlar: "—" + ölçülmedi, ✗ DEĞİL; filo özetinde sayılmaz', async () => {
    // Eski önbellek yükü: yeni alanlar HİÇ yok.
    m.cov = { rows: [base('svc-legacy', 50)], sampleRows: 200_000, windowSec: 3600 };
    const el = await mount();
    const t = serviceTable(el);
    for (const p of [VER, TAG, ENV]) {
      const c = cell(t, 'svc-legacy', p);
      expect(c.textContent).toBe('—');
      expect(c.getAttribute('title')).toBe('ölçülmedi');
      expect(c.classList.contains('cell-err')).toBe(false);
      expect(c.classList.contains('cell-warn')).toBe(false);
      expect(c.classList.contains('num')).toBe(true);
    }
    expect(fleet(el, 'service.version')).toEqual(['0', '0', '0']);
    // Aynı satırın ÖLÇÜLMÜŞ alanı (pod.uid = 0) "yok" sayılır — ayrım korunuyor.
    expect(fleet(el, 'k8s.pod.uid')).toEqual(['0', '0', '1']);
  });

  it('filo özetinde üç yeni satır', async () => {
    m.cov = {
      rows: [
        { ...base('svc-a', 100), serviceVersion: 100, imageTag: 100, envName: 100 },
        { ...base('svc-b', 100), serviceVersion: 0, imageTag: 30, envName: 100 },
      ],
      sampleRows: 200_000, windowSec: 3600,
    };
    const el = await mount();
    expect(fleet(el, 'service.version')).toEqual(['1', '0', '1']);
    expect(fleet(el, 'container.image.tag | k8s.container.image.tag')).toEqual(['1', '1', '0']);
    expect(fleet(el, 'deployment.environment.name')).toEqual(['2', '0', '0']);
  });
});
