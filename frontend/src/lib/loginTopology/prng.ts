// prng — v0.10.1928. Tohumlu PRNG + kimliklerden kararlı tohum (login arka planı).
// layout.ts'in kuvvet yerleşimi galaksiyle (galaxy.ts) değişti; yalnız bu iki
// yardımcı yaşıyor.

/** mulberry32 — küçük, hızlı, tohumlu PRNG ([0,1)). */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** FNV-1a: düğüm kimliklerinden kararlı tohum (kimlikler sunucuda tuzlu ve kararlı). */
export function seedOfIds(ids: readonly string[]): number {
  let h = 0x811c9dc5;
  for (const id of ids) {
    for (let i = 0; i < id.length; i++) {
      h ^= id.charCodeAt(i);
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    h ^= 0xff; h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}
