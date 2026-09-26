// spread.test.ts — v0.10.949 — yayılım işareti + ipucu + taban alanı okuyucu.
import { describe, it, expect } from 'vitest';
import { spreadOf, spreadTitle, defaultFloorTitle, spreadOffFloorTitle, readFloorMeta } from './spread';

describe('spreadOf', () => {
  it('spread ≥ 2 → işaret; ortaklar yalnız string', () => {
    expect(spreadOf({ fingerprint: 'a', spread: 3, spreadServices: ['b', 'c'] })).toEqual({ n: 3, partners: ['b', 'c'] });
    expect(spreadOf({ spread: 2, spreadServices: ['b', 7] })).toEqual({ n: 2, partners: ['b'] });
    expect(spreadOf({ spread: 2 })).toEqual({ n: 2, partners: [] });
  });
  it('alan yok / < 2 / yanlış tip / boş → null', () => {
    expect(spreadOf({ fingerprint: 'a' })).toBeNull();
    expect(spreadOf({ spread: 1 })).toBeNull();
    expect(spreadOf({ spread: '3' })).toBeNull();
    expect(spreadOf(undefined)).toBeNull();
    expect(spreadOf(null)).toBeNull();
  });
});

describe('spreadTitle', () => {
  it('ortaklar + adı yazılmayanlar (+k) + pencere', () => {
    expect(spreadTitle(8, ['a', 'b', 'c', 'd', 'e'], 10))
      .toBe('Aynı exception aynı anda 8 serviste: a, b, c, d, e (+2) · pencere ±10 dk');
  });
  it('tam liste → +k yok; pencere yoksa yazılmaz', () => {
    expect(spreadTitle(3, ['a', 'b'])).toBe('Aynı exception aynı anda 3 serviste: a, b');
  });
  it('ortak adı hiç yoksa yalnız sayı', () => {
    expect(spreadTitle(2, [], 0)).toBe('Aynı exception aynı anda 2 serviste');
  });
});

describe('defaultFloorTitle', () => {
  it('kuralı etkin taban ve pencereyle söyler', () => {
    const t = defaultFloorTitle(5, 10);
    expect(t).toContain('5+ oluşumlu gruplar');
    expect(t).toContain('(±10 dk)');
    expect(t).toContain("5'in altında, tek serviste");
  });
  it('v0.10.949 — kural cümlesi regressed istisnasını söyler (gösterilen + gizlenmeyen)', () => {
    const t = defaultFloorTitle(5, 10);
    expect(t).toContain('regressed gruplar (çözülmüş, sonra yeniden görülmüş)');
    expect(t).toContain("5'in altında, tek serviste kalan ve regressed olmayan gruplar gizli");
    // Etkin taban P1 eşiğine indiyse cümle o sayıyla konuşur.
    expect(defaultFloorTitle(3)).toContain("3'in altında, tek serviste kalan ve regressed olmayan");
  });
});

describe('spreadOffFloorTitle', () => {
  it('istisnanın kapalı olduğunu ve düz tabanı söyler; çoklu-servis iddiası yok', () => {
    const t = spreadOffFloorTitle(5);
    expect(t).toContain('geçici olarak kullanılamıyor');
    expect(t).toContain("5'in altındaki gruplar gizli, aynı anda birden çok serviste görülenler dahil");
    expect(t).not.toContain('tek serviste');
  });
  it('v0.10.949 — yayılım kapalıyken de regressed gruplar gösterilir ("TÜM gruplar gizli" yalanı yok)', () => {
    const t = spreadOffFloorTitle(5);
    expect(t).toContain('yalnız regressed gruplar');
    expect(t).not.toContain('TÜM gruplar gizli');
  });
});

describe('readFloorMeta', () => {
  it('sayısal/boolean alanları okur, yanlış tipi atar', () => {
    expect(readFloorMeta({ items: [], minOcc: 5, hiddenByMinOcc: 12, floorDefault: true, spreadWindowMin: 10 }))
      .toEqual({ minOcc: 5, hiddenByMinOcc: 12, floorDefault: true, spreadWindowMin: 10 });
    expect(readFloorMeta({ minOccDefault: false, keptBySpread: 3 })).toEqual({ floorDefault: false, keptBySpread: 3 });
    // v0.10.949 — regressed istisnasıyla tutulanlar ayrı sayaç.
    expect(readFloorMeta({ keptRegressed: 2 })).toEqual({ keptRegressed: 2 });
    expect(readFloorMeta({ keptRegressed: '2' })).toEqual({ keptRegressed: undefined });
    expect(readFloorMeta({ minOcc: '5' })).toEqual({ minOcc: undefined });
    expect(readFloorMeta(null)).toEqual({});
  });
  it('v0.10.949 — spreadAvailable: boolean okunur; yoksa/yanlış tipse undefined (geri uyum = var)', () => {
    expect(readFloorMeta({ floorDefault: true, spreadAvailable: false })).toEqual({ floorDefault: true, spreadAvailable: false });
    expect(readFloorMeta({ minOccDefault: true, spreadAvailable: true })).toEqual({ floorDefault: true, spreadAvailable: true });
    expect(readFloorMeta({ spreadAvailable: 'no' })).toEqual({});
    expect(readFloorMeta({ floorDefault: true }).spreadAvailable).toBeUndefined();
  });
});
