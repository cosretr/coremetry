// headKeyDown.test.ts — v0.10.925. Başlık İÇİNDEKİ bir düğmenin (LogTable
// kolon-kaldır ×) Enter/Boşluk'u kabarcıklanıp sıralamaya dönüyordu:
// klavye kolonu kaldıramıyordu. Sıralama yalnız başlığın KENDİSİ odaktayken;
// Shift+←/→ ile genişlik ayarı olduğu gibi kalır.
import { describe, it, expect, vi } from 'vitest';
import { headKeyDown, HEAD_RESIZE_STEP_PX, type DataTable } from './DataTable';

function fakeDt() {
  const toggleSort = vi.fn();
  const resizeBy = vi.fn();
  return { dt: { toggleSort, resizeBy } as unknown as DataTable<unknown>, toggleSort, resizeBy };
}
const th = {} as EventTarget;
const child = {} as EventTarget;
const ev = (key: string, target: EventTarget, shiftKey = false) =>
  ({ key, shiftKey, preventDefault: vi.fn(), target, currentTarget: th });

describe('headKeyDown', () => {
  it('başlık odaktayken Enter/Boşluk sıralar', () => {
    for (const k of ['Enter', ' ']) {
      const { dt, toggleSort } = fakeDt();
      const e = ev(k, th);
      headKeyDown(e, dt, 'c1');
      expect(toggleSort).toHaveBeenCalledWith('c1');
      expect(e.preventDefault).toHaveBeenCalled();
    }
  });
  it('başlık içindeki düğmeden gelen Enter/Boşluk sıralamaz, varsayılanı engellemez', () => {
    for (const k of ['Enter', ' ']) {
      const { dt, toggleSort } = fakeDt();
      const e = ev(k, child);
      headKeyDown(e, dt, 'c1');
      expect(toggleSort).not.toHaveBeenCalled();
      expect(e.preventDefault).not.toHaveBeenCalled();
    }
  });
  it('Shift+→ genişliği artırır', () => {
    const { dt, resizeBy } = fakeDt();
    headKeyDown(ev('ArrowRight', th, true), dt, 'c1');
    expect(resizeBy).toHaveBeenCalledWith('c1', HEAD_RESIZE_STEP_PX);
  });
});
