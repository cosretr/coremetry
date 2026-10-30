// @vitest-environment jsdom
// coremetry/Field sözleşmesi — Faz 2 dilim 1 R2 (plan G1 + G10). Etiket ↔
// denetim kimliği, ipucu/hata aria-describedby (çağıranın id'si korunur),
// hata ipucunu yener + aria-invalid, bg-field zemini, `small` genişliği,
// `labelAside` etiket satırında, checkbox etiketi sağda; secret geçişi
// (value/placeholder/type olduğu gibi, gizli input yok).
import { describe, it, expect, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { Field, SelectField, TextareaField, CheckboxField } from './index';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  // jsdom eksiği: Radix Checkbox form içinde kabarcık inputunu useSize ile ölçer.
  (globalThis as unknown as Record<string, unknown>).ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
});
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; });
function render(node: React.ReactNode) {
  host = document.createElement('div'); document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(node));
  return host;
}
const noop = () => {};

describe('Field', () => {
  it('etiket htmlFor = denetim id; ipucu aria-describedby; aria-invalid yok', () => {
    const el = render(<Field label="Thanos URL" hint="https://…" value="" onChange={noop} />);
    const input = el.querySelector('input')!;
    const label = el.querySelector('label')!;
    expect(label.htmlFor).toBe(input.id);
    expect(label.textContent).toBe('Thanos URL');
    const hint = el.ownerDocument.getElementById(input.getAttribute('aria-describedby')!)!;
    expect(hint.textContent).toBe('https://…');
    expect(input.hasAttribute('aria-invalid')).toBe(false);
    expect(input.className).toContain('bg-field');
  });
  it('hata ipucunu yener, aria-invalid=true, aynı describedby hedefi', () => {
    const el = render(<Field label="L" hint="ipucu" error="bozuk" value="" onChange={noop} />);
    const input = el.querySelector('input')!;
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(el.textContent).not.toContain('ipucu');
    expect(el.ownerDocument.getElementById(input.getAttribute('aria-describedby')!)!.textContent).toBe('bozuk');
  });
  it('çağıranın aria-describedby id\'si korunur, ipucu eklenir', () => {
    const el = render(<><div id="ext">dış not</div><Field label="L" hint="h" aria-describedby="ext" value="" onChange={noop} /></>);
    const ids = el.querySelector('input')!.getAttribute('aria-describedby')!.split(' ');
    expect(ids[0]).toBe('ext');
    expect(ids).toHaveLength(2);
  });
  it('ipucu/hata yoksa describedby yok; verilen id kullanılır', () => {
    const el = render(<Field id="f1" label="L" value="" onChange={noop} />);
    const input = el.querySelector('input')!;
    expect(input.id).toBe('f1');
    expect(input.hasAttribute('aria-describedby')).toBe(false);
  });
  it('small = 180px kap; labelAside etiket satırında', () => {
    const el = render(<Field small label="Token" labelAside={<span data-x="">· stored</span>} value="" onChange={noop} />);
    expect(el.querySelector('[data-cm-field]')!.className).toContain('w-[180px]');
    const aside = el.querySelector('[data-x]')!;
    expect(aside.previousElementSibling?.tagName).toBe('LABEL');
    expect(aside.textContent).toBe('· stored');
  });
  it('secret geçişi: type/placeholder/value olduğu gibi, gizli input yok', () => {
    const el = render(<form><Field label="Token" type="password" value="" onChange={noop} placeholder="(leave empty to keep stored value)" /></form>);
    const inputs = el.querySelectorAll('input');
    expect(inputs).toHaveLength(1);
    expect(inputs[0].type).toBe('password');
    expect(inputs[0].value).toBe('');
    expect(inputs[0].placeholder).toBe('(leave empty to keep stored value)');
  });
});

describe('SelectField / TextareaField', () => {
  it('native select: etiket bağı, seçenekler çağırandan, hata', () => {
    const el = render(<SelectField label="Auth" error="seç" value="b" onChange={noop}><option value="a">A</option><option value="b">B</option></SelectField>);
    const sel = el.querySelector('select')!;
    expect(el.querySelector('label')!.htmlFor).toBe(sel.id);
    expect(sel.options).toHaveLength(2);
    expect(sel.value).toBe('b');
    expect(sel.getAttribute('aria-invalid')).toBe('true');
  });
  it('textarea: rows varsayılanı 4, verilen rows geçer, ipucu bağlı', () => {
    const el = render(<><TextareaField label="A" value="" onChange={noop} /><TextareaField label="B" rows={2} hint="h" value="" onChange={noop} /></>);
    const [a, b] = [...el.querySelectorAll('textarea')];
    expect(a.rows).toBe(4);
    expect(b.rows).toBe(2);
    expect(el.ownerDocument.getElementById(b.getAttribute('aria-describedby')!)!.textContent).toBe('h');
  });
});

describe('CheckboxField', () => {
  it('role=checkbox, etiket sağda ve bağlı, aria-checked, tıklama değeri çevirir; form dışında gizli input yok', () => {
    const seen: boolean[] = [];
    const el = render(<CheckboxField label="Enabled" checked={false} onCheckedChange={v => seen.push(v)} />);
    const box = el.querySelector<HTMLButtonElement>('[role="checkbox"]')!;
    const label = el.querySelector('label')!;
    expect(label.htmlFor).toBe(box.id);
    expect(box.compareDocumentPosition(label) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(box.getAttribute('aria-checked')).toBe('false');
    act(() => box.click());
    expect(seen).toEqual([true]);
    expect(el.querySelectorAll('input')).toHaveLength(0);
  });
  it('form içinde Radix kabarcık inputu adsız (form gövdesine değer taşımaz)', () => {
    const el = render(<form><CheckboxField label="E" checked onCheckedChange={noop} /></form>);
    for (const i of el.querySelectorAll('input')) expect(i.hasAttribute('name')).toBe(false);
    expect([...new FormData(el.querySelector('form')!).keys()]).toEqual([]);
  });
  it('disabled geçer; ipucu describedby', () => {
    const el = render(<CheckboxField label="X" hint="not" disabled checked onCheckedChange={noop} />);
    const box = el.querySelector<HTMLButtonElement>('[role="checkbox"]')!;
    expect(box.disabled).toBe(true);
    expect(box.getAttribute('aria-checked')).toBe('true');
    expect(el.ownerDocument.getElementById(box.getAttribute('aria-describedby')!)!.textContent).toBe('not');
  });
});
