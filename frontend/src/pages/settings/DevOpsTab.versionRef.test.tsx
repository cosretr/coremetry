// @vitest-environment jsdom
//
// DevOpsTab.versionRef — v0.10.1047. Operatör: "Mono-repo'da sürüm etiketi:
// aynı depoda birden çok servis varsa, başka servisin etiketi bu servisin
// sürümü sanılabiliyor." Sürüm → ref deseni ikinci yer tutucu {service}'i
// tanıyor (yeni alan yok); ipucu varsayılanı ve mono-repo örneğini söyler.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { DevOpsSnapshot } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<DevOpsSnapshot>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getDevOpsSettings: () => h.get(),
    putDevOpsSettings: () => h.get(),
    testDevOpsSettings: () => Promise.resolve({ ok: true, projectCount: 1 }),
    resolveDevOpsDryRun: () => Promise.resolve(null),
    getSchemaCatalog: () => Promise.resolve({ tables: 0, columns: 0, importedAt: 0, snapshotSql: {} }),
  },
}));

import { ConfirmProvider } from '@/components/ui';
import { DevOpsTab } from './DevOpsTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.get.mockReset();
});

describe('DevOpsTab — sürüm → ref deseni ipucu (v0.10.1047)', () => {
  it('varsayılanı ve mono-repo {service} örneğini düz Türkçe söyler', async () => {
    h.get.mockResolvedValue({
      baseUrl: 'https://devops.example.local/tfs', collection: 'DefaultCollection',
      hasPat: true, flavor: 'auto', effectiveLookupLimit: 6,
      versionRef: 'tags/{service}-{version}',
    });
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<ConfirmProvider><DevOpsTab /></ConfirmProvider>); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });

    const label = Array.from(host.querySelectorAll('label'))
      .find(l => (l.textContent || '').startsWith('Sürüm → ref deseni'));
    expect(label, 'sürüm → ref deseni alanı yok').toBeTruthy();
    const input = label!.querySelector('input')!;
    expect(input.value).toBe('tags/{service}-{version}'); // kayıtlı desen kutuda
    expect(input.placeholder).toBe('tags/{version}');
    const hint = label!.querySelector('div:last-child')!.textContent || '';
    expect(hint).toContain(
      'Boş = tags/{version}. Aynı depoda birden çok servis varsa {service} kullanın, ör. tags/{service}-{version}.',
    );
  });
});
