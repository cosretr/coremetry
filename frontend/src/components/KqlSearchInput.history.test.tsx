// @vitest-environment jsdom
//
// v0.10.1820 (Log Search Faz 1 dilim 1.4) — sorgu kutusunun "Geçmiş" listesi:
// boş + odaklı kutuda açılır, seçim metni yerleştirir (aramayı başlatmaz),
// Esc kapatır, yazmaya başlayınca kaybolur.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { KqlSearchInput } from './KqlSearchInput';

vi.mock('@/lib/api', () => ({ api: { logsFieldValues: () => Promise.resolve({ values: [] }) } }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; });

function Harness({ onSubmit }: { onSubmit: () => void }) {
  const [v, setV] = useState('');
  return <KqlSearchInput value={v} onChange={setV} onSubmit={onSubmit} history={['level:error', 'service.name:orders-api']} />;
}

describe('KqlSearchInput geçmiş', () => {
  it('boş + odak → Geçmiş; seçim yerleştirir, aramaz; Esc kapatır', async () => {
    const onSubmit = vi.fn();
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => { root!.render(<Harness onSubmit={onSubmit} />); });
    const input = host.querySelector('input')!;
    act(() => { input.focus(); });
    expect(host.textContent).toContain('Geçmiş');
    expect(host.textContent).toContain('service.name:orders-api');
    const row = [...host.querySelectorAll('div.mono')].find(d => d.textContent === 'service.name:orders-api')!;
    act(() => { row.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); });
    expect(input.value).toBe('service.name:orders-api');
    expect(onSubmit).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain('Geçmiş');

    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, '');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(host.textContent).toContain('Geçmiş');
    act(() => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); });
    expect(host.textContent).not.toContain('Geçmiş');
  });
});
