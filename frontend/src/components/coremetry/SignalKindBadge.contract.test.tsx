// @vitest-environment jsdom
//
// SignalKindBadge sözleşmesi — v0.10.1844 (öğrenme döngüsü Faz 2 dilim 2.4).
// Renk hiçbir zaman tek taşıyıcı değil: her tür ikon (dekoratif) + METİN
// çizer; renk yalnız --sig-* token sınıfından.
import { vi, describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { SignalKindBadge } from './SignalKindBadge';
import type { SignalKind } from '@/lib/signalKind';
// v0.10.2258 — tablo durum metinleri / rozet arayüz dilinde; bu dosya Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));


let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});
function render(node: React.ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host.querySelector('[data-slot="badge"]')!;
}

describe('SignalKindBadge', () => {
  const cases: [SignalKind, string, string][] = [
    ['metric', 'Metrik', '--sig-metric'], ['trace', 'Trace', '--sig-trace'], ['log', 'Log', '--sig-log'], ['group', 'Grup', '--text2'],
  ];
  for (const [kind, text, token] of cases) {
    it(`${kind}: icon + text + token colour`, () => {
      const el = render(<SignalKindBadge kind={kind} />);
      expect(el.textContent).toBe(text);
      expect(el.getAttribute('data-signal')).toBe(kind);
      const svg = el.querySelector('svg');
      expect(svg).not.toBeNull();
      expect(svg!.getAttribute('aria-hidden')).toBe('true');
      expect(el.className).toContain(`text-[color:var(${token})]`);
    });
  }
  it('custom label keeps icon', () => {
    const el = render(<SignalKindBadge kind="group" label="Anomali grubu · 4 sinyal" />);
    expect(el.textContent).toBe('Anomali grubu · 4 sinyal');
    expect(el.querySelector('svg')).not.toBeNull();
  });
});
