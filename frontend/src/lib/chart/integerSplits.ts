// v0.10.1066 — TimeChart.leftInteger için tam sayım ekseni bölmeleri (SAF): 0 / orta / üst, tam sayıya
// yuvarlı, yinelenen ve negatif atılmış. max 1.1 → [0, 1]; 3.3 → [0, 2, 3].
export function integerSplits(max: number): number[] {
  const out: number[] = [];
  for (const v of [0, Math.round(max / 2), Math.round(max)]) {
    if (v >= 0 && !out.includes(v)) out.push(v);
  }
  return out;
}
