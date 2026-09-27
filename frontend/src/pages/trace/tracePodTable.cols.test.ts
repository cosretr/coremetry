import { describe, it, expect } from 'vitest';
import { fitColumnWidths } from '@/lib/dataTable';
import { TRACE_POD_COLS } from './tracePodCols';

// v0.10.976 — CPU / Bellek hücreleri "%…"ya KIRPILMAZ (operatör, prod
// v0.10.968: 400 px yan panel tabloyu sıkıştırıyor, sığdırma kolonları
// DEFAULT_MIN'e (48 px) kadar küçültüyordu; `.tpm-mval` sondan "…" basıyordu).
// Yan panel gitti; bu çivi tabanı tutar: kap ne kadar dar olursa olsun
// (1280–1440 px pencerede de, ayrıntı açıkken de) sığdırma CPU/Bellek'i
// 116 px'in altına çekemez.
const DEFAULT_MIN = 48; // components/ui/DataTable/DataTable.tsx (dışa açık değil)
const fitInput = () => TRACE_POD_COLS.map(c => ({ id: c.id, px: c.flex ? null : (c.width ?? null), min: c.minWidth ?? DEFAULT_MIN }));

describe('TracePodTable kolonları — v0.10.976 CPU/Bellek tabanı', () => {
  it('cpu / mem: minWidth = beyan genişliği (116)', () => {
    for (const id of ['cpu', 'mem']) {
      const c = TRACE_POD_COLS.find(x => x.id === id)!;
      expect(c.width).toBe(116);
      expect(c.minWidth).toBe(116);
    }
  });

  it.each([1280, 1000, 900, 760, 700])('sığdırma %i px kapta CPU/Bellek ≥ 116', (containerPx) => {
    const out = fitColumnWidths(fitInput(), 0, containerPx);
    for (const id of ['cpu', 'mem']) {
      const w = out ? out[id] : TRACE_POD_COLS.find(x => x.id === id)!.width!;
      expect(w, `${id} @ ${containerPx}`).toBeGreaterThanOrEqual(116);
    }
  });

  it('tabanlar toplamı 1024 kabına sığar (tablet eşiğinde de kaydırma gerekmez)', () => {
    const sum = fitInput().reduce((a, c) => a + c.min, 0);
    expect(sum).toBeLessThanOrEqual(1024 - 2 * 16);
  });
});
