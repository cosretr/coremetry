// errRateDelta — /database "Err rate" karosunun önceki-pencere farkı
// (v0.10.1025, Databases dilim 3).
//
// NEDEN YÜZDE PUAN, ORAN DEĞİL: hata oranı zaten bir yüzde. %0,10'dan
// %0,20'ye çıkış göreli olarak "+%100" okunur ve karonun en bağıran rozeti
// olur — oysa 1.000 çağrıda bir hata daha demek. Tersi de yanıltır: %40'tan
// %44'e çıkış "+%10" diye sakin görünür ama her yüz çağrıdan dördü daha
// patlıyor. Operatörün sorduğu "kaç puan kötüleşti" — o yüzden fark
// mutlak, birimi `pp`. Diğer karolar (çağrı, gecikme) göreli kalır
// (TrendDelta); bu tek karo bilinçli olarak farklı.
//
// Renk kuralı (K5/T9): yalnız KÖTÜLEŞEN değer renk alır ve o da ≥ 0,05 pp
// iken — daha küçük artış gürültü sayılır. İyileşme nötr kalır. İki
// ondalıkta "0.00" basacak fark (|Δ| < 0,005 pp) hiç çizilmez.
//
// v0.10.1025 (inceleme R6a) — HER KARAR GÖSTERİLEN değer üzerinden: önce
// fark iki ondalığa yuvarlanır, eşik ve gizleme o yuvarlanmış sayıya
// uygulanır. Ham farkla karar verilseydi +0,0499 "+0.05 pp" diye basılıp
// NÖTR, +0,05 ise aynı metinle KIRMIZI çizilirdi — aynı yazı iki renk.

export const ERR_PP_HIDE_BELOW = 0.005;
export const ERR_PP_WORSE_AT = 0.05;

export type ErrRatePpDelta = {
  /** "+0.42 pp" / "-0.10 pp" — iki ondalık, işaret her zaman yazılı. */
  text: string;
  /** true → GÖSTERİLEN kötüleşme ≥ ERR_PP_WORSE_AT; yalnız o renk alır. */
  worse: boolean;
  /** Ham fark (pp, 1e-6'ya yuvarlı) — ipucu için. */
  delta: number;
};

/**
 * errRatePpDelta — cur ve prior 0..100 aralığında hata yüzdeleri.
 * Fark çizilmeyecek kadar küçükse ya da girdi sayı değilse null.
 */
export function errRatePpDelta(cur: number, prior: number): ErrRatePpDelta | null {
  // Tamsayı aritmetiği: fark önce 1e-6 pp'ye (kayan nokta gürültüsü —
  // 0,15 − 0,10 = 0,04999…), sonra gösterilen yüzdeliğe yuvarlanır.
  const micro = Math.round((cur - prior) * 1e6);
  if (!Number.isFinite(micro)) return null;
  const hundredths = Math.round(Math.abs(micro) / 1e4); // gösterilen değer × 100
  if (hundredths === 0) return null;
  const sign = micro > 0 ? '+' : '-';
  return {
    text: `${sign}${(hundredths / 100).toFixed(2)} pp`,
    worse: micro > 0 && hundredths >= Math.round(ERR_PP_WORSE_AT * 100),
    delta: micro / 1e6,
  };
}
