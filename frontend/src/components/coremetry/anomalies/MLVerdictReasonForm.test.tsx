// @vitest-environment jsdom
//
// v0.10.2256 — "Sorun değil" nedeni: neden seçmeden kayıt yok, "Diğer" için
// not zorunlu, seçilen neden + not kaydedilir; EN arayüz İngilizce.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';

const m = vi.hoisted(() => ({ lang: 'en' }));
vi.mock('@/lib/branding', async (orig) => {
  const real = await orig<typeof import('@/lib/branding')>();
  return { ...real, useBranding: () => ({ ...real.useBranding(), language: m.lang }) };
});

import { MLVerdictReasonForm } from './MLVerdictReasonForm';
import { VERDICT_REASONS, isVerdictReason, reasonFormError } from './mlVerdictReason';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

function mount(onSave: (r: string, n: string) => void) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(<MLVerdictReasonForm busy={false} onSave={onSave} onCancel={() => {}} />));
  return host;
}
const submit = (el: HTMLElement) => act(() => { el.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
const pick = (el: HTMLElement, v: string) => act(() => { (el.querySelector(`input[value="${v}"]`) as HTMLInputElement).click(); });

describe('mlVerdictReason (SAF)', () => {
  it('kapalı küme sunucuyla aynı', () => {
    expect([...VERDICT_REASONS]).toEqual(['no_deviation', 'no_impact', 'expected', 'other']);
    expect(isVerdictReason('free text')).toBe(false);
  });
  it('neden zorunlu; Diğer için not zorunlu', () => {
    expect(reasonFormError('', '')).toBe('mlRecord.reason.pick');
    expect(reasonFormError('other', '  ')).toBe('mlRecord.reason.otherNote');
    expect(reasonFormError('other', 'gece işi')).toBeNull();
    expect(reasonFormError('expected', '')).toBeNull();
  });
});

describe('MLVerdictReasonForm', () => {
  it('seçimsiz kayıt yok, Diğer notsuz yok, seçim + not kaydedilir (EN)', () => {
    m.lang = 'en';
    const onSave = vi.fn();
    const el = mount(onSave);
    expect(el.textContent).toContain('Why is this not an issue?');
    expect(el.textContent).not.toContain('Neden');
    submit(el);
    expect(onSave).not.toHaveBeenCalled();
    expect(el.textContent).toContain('Pick a reason.');
    pick(el, 'other');
    submit(el);
    expect(onSave).not.toHaveBeenCalled();
    expect(el.textContent).toContain('Note (required)');
    pick(el, 'no_impact');
    submit(el);
    expect(onSave).toHaveBeenCalledWith('no_impact', '');
  });
  it('TR metin', () => {
    m.lang = 'tr';
    const el = mount(() => {});
    expect(el.textContent).toContain('Neden sorun değil?');
    expect(el.textContent).toContain('Servis etkisi yok');
  });
});
