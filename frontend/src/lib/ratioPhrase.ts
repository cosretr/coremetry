// ratioPhrase — v0.10.1896 (Log Search Faz 4 dilim 4.2). Oran ifadesi kuralı
// (docs/anomaly/learning-loop-audit.md "Oran ifadesi"): Go `ml.RatioPhrase`
// kısa biçiminin istemci eşi — ekranda asla "×0,0" ya da "×12.4" yazılmaz.
//   artış: < 1,12 normal aralıkta · < 1,3 biraz yüksek · < 1,8 yaklaşık 1,5 kat ·
//          > 20 20 kattan fazla · aksi yaklaşık N kat (tam sayıya yuvarlı)
//   azalış (dir 'down'): aynı merdiven, "kat az" diliyle; gözlenen 0 → durdu
//   taban 0, gözlenen > 0 → normalde hiç görülmez
// SAF; tablo testi lib/ratioPhrase.test.ts. Go merdiveniyle aynı eşikler.

export function ratioPhrase(observed: number, base: number): string {
  if (!(base > 0)) return observed > 0 ? 'normalde hiç görülmez' : 'normal aralıkta';
  if (!(observed > 0)) return 'durdu';
  const r = observed / base;
  if (r >= 1) {
    if (r < 1.12) return 'normal aralıkta';
    if (r < 1.3) return 'biraz yüksek';
    if (r < 1.8) return 'yaklaşık 1,5 kat';
    if (r > 20) return '20 kattan fazla';
    return `yaklaşık ${Math.round(r)} kat`;
  }
  const d = base / observed;
  if (d < 1.12) return 'normal aralıkta';
  if (d < 1.3) return 'biraz düşük';
  if (d < 1.8) return 'yaklaşık 1,5 kat az';
  if (d > 20) return 'neredeyse durdu';
  return `yaklaşık ${Math.round(d)} kat az`;
}
