// timeStack — v0.10.1871 (öğrenme döngüsü Faz 2 parti B, audit §7.2 "Log:
// yığılmış hacim + baseline gölge çubuk"). TimeChart'ın yığılı çubuk
// kanalının SAF matrisi: aynı `stack` anahtarlı çubuk serileri verildiği
// sırada (ilk = en alt) üst üste biner. Çizim matrisi kümülatif ÜST, taban
// ayrı (uPlot bars `disp.y0`/`disp.y1`): katmanlar birbirini ÖRTMEZ, y
// ölçeği kümülatif tepeyi görür. Tooltip / lejant HAM değeri okur
// (çağıranın `data`'sı). Yığında null = 0 (yığın deliksiz).

export interface StackInput { type: string; stack?: string; data: readonly (number | null)[] }

export interface StackMatrix {
  /** Seri başına çizim verisi: yığın üyesinde kümülatif üst, diğerlerinde ham. */
  draw: (number | null)[][];
  /** Seri başına yığın tabanı (kümülatif alt); yığın dışı seride null. */
  base: (number[] | null)[];
}

export function stackMatrix(series: readonly StackInput[]): StackMatrix {
  const acc = new Map<string, number[]>();
  const draw: (number | null)[][] = [];
  const base: (number[] | null)[] = [];
  for (const s of series) {
    if (s.type !== 'bar' || !s.stack) {
      draw.push([...s.data]);
      base.push(null);
      continue;
    }
    const cur = acc.get(s.stack) ?? new Array<number>(s.data.length).fill(0);
    const hi = s.data.map((v, i) => (cur[i] ?? 0) + (v != null && Number.isFinite(v) ? v : 0));
    acc.set(s.stack, hi);
    draw.push(hi);
    base.push(cur.slice());
  }
  return { draw, base };
}
