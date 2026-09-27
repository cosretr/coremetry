// @vitest-environment jsdom
//
// ExternalPaths.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR: dış bağımlılığın yol kırılımı tablosu (statik, dt yok) boş
// ve HATA hâlini tablonun İÇİNDE, başlık dururken basar. Hata eskiden
// erken dönüşle tabloyu hiç çizmiyordu; artık tek `<tr data-dt-state=
// "error">`. Sıra korunur: hata, sunucudan gelen (kısmi/bayat) yolları da
// gizler — "okunamadı" satırının yanında yollar "geçerli" gibi okunmasın.
// Dipnot (toplam çağrı) yalnız satır varken.
//
// NEDEN GERÇEK MOUNT: kapı tip-doğru biçimde yanlış yazılabilir
// (`rows.length === 0 ? state : rows`) — hata + satır hâlinde hata kaybolur.
import { describe, it, expect, afterEach } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ExternalPathRow } from '@/lib/types';
import { ExternalPaths } from './ExternalPaths';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const PATHS: ExternalPathRow[] = [
  { path: '/orders/{id}', calls: 1200, errors: 6, errorRate: 0.5, p99Ms: 90 },
  { path: '/nvi/kps', calls: 300, errors: 18, errorRate: 6, p99Ms: 1400 },
];

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const stateRows = (el: HTMLElement) => el.querySelectorAll('tbody tr[data-dt-state]');
const stateRow = (el: HTMLElement, kind: string) => el.querySelector<HTMLTableRowElement>(`tbody tr[data-dt-state="${kind}"]`);
const bodyRows = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));
const headCount = (el: HTMLElement) => el.querySelectorAll('thead th').length;

describe('ExternalPaths — durumlar tablonun içinde (P-2)', () => {
  it('yollar varken: satırlar + dipnot, durum satırı yok', () => {
    const el = mount(<ExternalPaths paths={PATHS} windowS={3600} />);
    expect(bodyRows(el)).toHaveLength(2);
    expect(stateRows(el)).toHaveLength(0);
    expect(el.textContent).toContain('1.5K çağrı');
  });

  it('boş: başlık durur, tek boş satır colSpan = 4 <th>, dipnot yok', () => {
    const el = mount(<ExternalPaths paths={[]} />);
    expect(headCount(el)).toBe(4);
    const row = stateRow(el, 'empty');
    expect(row).not.toBeNull();
    expect(stateRows(el)).toHaveLength(1);
    expect(row!.querySelector('td')!.colSpan).toBe(4);
    expect(row!.textContent).toContain("URL taşıyan istemci span'i yok");
    expect(el.textContent).not.toMatch(/gruplandı/);
  });

  it('hata (geniş): tablonun İÇİNDE hata satırı, sunucu metni + "çağıran listesi geçerli" korunur', () => {
    const el = mount(<ExternalPaths paths={undefined} error="ch timeout 30s" />);
    expect(headCount(el), 'hata hâlinde başlık durmalı (S6)').toBe(4);
    const row = stateRow(el, 'error');
    expect(row).not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(4);
    expect(row!.textContent).toContain('Yol kırılımı okunamadı');
    expect(row!.textContent).toContain('ch timeout 30s');
    expect(row!.textContent).toContain('çağıran listesi ve trend geçerli');
    expect(stateRow(el, 'empty')).toBeNull();
  });

  it('hata + gelen yollar: hata satırı kazanır, bayat yol ve dipnot yok', () => {
    const el = mount(<ExternalPaths paths={PATHS} error="partial read" windowS={3600} />);
    expect(stateRow(el, 'error'), 'hata satırı yollar yüzünden gizlendi').not.toBeNull();
    expect(bodyRows(el)).toHaveLength(0);
    expect(el.textContent).not.toContain('/orders/{id}');
    expect(el.textContent).not.toMatch(/gruplandı/);
  });

  it('dense (kart): colSpan = 3 <th>, ham hata metni yok (240px kart)', () => {
    const el = mount(<ExternalPaths dense limit={5} paths={undefined} error="very long server error" />);
    expect(headCount(el)).toBe(3);
    const row = stateRow(el, 'error');
    expect(row!.querySelector('td')!.colSpan).toBe(3);
    expect(row!.textContent).toContain('Yol kırılımı okunamadı');
    expect(row!.textContent).not.toContain('very long server error');
  });

  it('durum satırı tıklanmaz: role / data-row-action yok', () => {
    const el = mount(<ExternalPaths paths={[]} />);
    const row = stateRow(el, 'empty')!;
    expect(row.getAttribute('role')).toBeNull();
    expect(row.hasAttribute('data-row-action')).toBe(false);
  });
});
