// @vitest-environment jsdom
// AiProfilesPanel.localOnly.test.tsx — v0.10.1451 (karar 8, R1): "Yerel
// (self-hosted) model" kutusu yalnız OpenAI-uyumlu profilde; işaret gövdeye
// localOnly olarak gider; işaretli profil tabloda «yerel» rozeti taşır.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import '@/test/radixJsdom';
import type { AIProfilesPayload } from '@/lib/types';

const api = vi.hoisted(() => ({
  putAIProfile: vi.fn(async (..._a: unknown[]) => ({ profiles: [], defaultProfile: 'loc', surfaceMap: {} })),
}));
vi.mock('@/lib/api', () => ({ api }));

import { AiProfilesPanel } from './AiProfilesPanel';
import { ConfirmGate } from '@/components/coremetry/Confirm';

let host: HTMLDivElement | null = null; let root: Root | null = null;
const payload: AIProfilesPayload = {
  defaultProfile: 'loc',
  surfaceMap: {},
  profiles: [
    { id: 'loc', label: 'Yerel gemma', provider: 'openai', baseUrl: 'http://vllm:8000/v1', model: 'gemma4-31b', hasKey: false, default: true, localOnly: true, effectiveToolset: 'full' },
    { id: 'ant', label: 'Anthropic', provider: 'anthropic', model: 'x', hasKey: true, effectiveToolset: 'full' },
  ],
};
function render(): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><ConfirmGate><AiProfilesPanel payload={payload} onChange={() => {}} /></ConfirmGate></MemoryRouter>); });
  return host;
}
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; document.body.innerHTML = ''; });

// R11: Radix Checkbox `<button role="checkbox">`, etiket kardeş `<label for>` (eskiden sarmalayan label + input).
const localBox = () => {
  const l = Array.from(document.querySelectorAll('label')).find(x => x.textContent?.includes('Yerel (self-hosted) model'));
  return l ? document.getElementById(l.htmlFor) : null;
};
const editBtn = (h: HTMLElement, label: string) => Array.from(h.querySelectorAll('tr')).find(r => r.textContent?.includes(label))!.querySelector('button')!;

describe('AiProfilesPanel localOnly', () => {
  it('rozet yalnız işaretli profilde', () => {
    const h = render();
    const rows = Array.from(h.querySelectorAll('tbody tr'));
    expect(rows.find(r => r.textContent?.includes('Yerel gemma'))!.textContent).toContain('yerel');
    expect(rows.find(r => r.textContent?.includes('Anthropic'))!.textContent).not.toContain('yerel');
  });
  it('kutu yalnız openai profilinde; işaret gövdeye gider', async () => {
    const h = render();
    act(() => { editBtn(h, 'Anthropic').click(); });
    expect(localBox()).toBeNull();
    act(() => { (Array.from(document.querySelectorAll('button')).find(b => b.textContent === 'Vazgeç') as HTMLButtonElement).click(); });
    act(() => { editBtn(h, 'Yerel gemma').click(); });
    const box = localBox();
    expect(box).not.toBeNull();
    expect(box!.getAttribute('aria-checked')).toBe('true');
    await act(async () => { (document.getElementById('ai-profile-form') as HTMLFormElement).requestSubmit(); });
    expect(api.putAIProfile).toHaveBeenCalledWith('loc', expect.objectContaining({ localOnly: true }));
  });
});
