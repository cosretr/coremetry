// @vitest-environment jsdom
//
// v0.10.1377 — metrik kipi ortam süzgeci bandı (adlar SENTETİK): uygulandı →
// b-info, uygulanamadı / çözülemedi → b-warn, bekliyor → b-gray; env/cluster
// seçili değilse hiçbir şey çizilmez (bayt bayt eski sayfa).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { HttpEnvScopeBand } from './httpMetricsViz';
import type { HttpEnvScopeView } from '@/lib/httpEnvScopeNote';
// v0.10.2305 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render(view: HttpEnvScopeView) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<HttpEnvScopeBand view={view} />); });
  return host.querySelector('[data-testid="http-env-scope-band"]') as HTMLElement | null;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

describe('HttpEnvScopeBand (v0.10.1377)', () => {
  it('off → bant yok', () => {
    expect(render({ state: 'off' })).toBeNull();
    expect(host!.innerHTML).toBe('');
  });

  it('v0.10.2211: uygulandı → bant çizilmez (seçici yalnız ipucu metinlerinde)', () => {
    const el = render({
      state: 'applied', envSel: 'deployment_environment_name="prod-cl-a"',
      scope: {
        label: 'deployment_environment_name', source: 'default', applied: true, matcher: 'deployment_environment_name="prod-cl-a"',
        terms: [{ role: 'cluster', want: 'cl-a', label: 'deployment_environment_name', values: ['prod-cl-a'], tier: 'entity' }],
      },
    });
    expect(el).toBeNull();
  });

  it('uygulanamadı / çözülemedi → b-warn + TÜM ortamlar', () => {
    const un = render({
      state: 'unresolved',
      scope: {
        label: 'deployment_environment_name', source: 'configured', applied: false,
        terms: [{ role: 'cluster', want: 'eu-z', label: 'deployment_environment_name', tier: 'none' }],
      },
    })!;
    expect(un.className).toContain('b-warn');
    expect(un.textContent).toContain('metrikte bu ortam yok: deployment_environment_name değerleri arasında "eu-z" eşleşmedi');
    expect(un.textContent).toContain('seriler TÜM ortamları kapsıyor');
    act(() => { root?.unmount(); });
    host?.remove();
    const err = render({ state: 'error', reason: 'VM etiket sorgusu başarısız: timeout' })!;
    expect(err.className).toContain('b-warn');
    expect(err.textContent).toBe('Ortam süzgeci çözülemedi (VM etiket sorgusu başarısız: timeout) — süzgeç uygulanmadı, seriler TÜM ortamları kapsıyor');
  });

  it('bekliyor → nötr', () => {
    const el = render({ state: 'pending' })!;
    expect(el.className).toContain('b-gray');
    expect(el.textContent).toBe('Ortam süzgeci çözülüyor…');
  });
});
