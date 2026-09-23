// fmtEta.ts — v0.10.901 (Dynatrace paritesi #6, dilim 2): "kaç gün kaldı"
// rozeti yardımcıları.
//
// fmtEtaDays: backend fmtDays'in (internal/evaluator/selfhealth.go) TR ikizi
// — aynı sayı rozet ve tooltip'te (Go'nun yazdığı diskReason cümlesi) iki
// farklı şekilde yazılmasın. Bir günün altında saat, bir saatin altında
// dakika: "0.4 gün" hiçbir şey söylemez, "9 saat" acil olduğunu söyler.
// Go %.Nf tam ikili "yarım"larda ÇİFTE yuvarlar (10.5 → 10), JS toFixed
// yukarı (→ 11); fixedHalfEven bu tek farkı kapatır (inceleme turu 901:
// 0.4375 gün rozet "11 saat", tooltip "10 saat" yazıyordu). Tam yarım
// ancak x·2^(d+1) tek tamsayıysa mümkündür (ikili kesir); o zaman
// x·10^d = (tek·5^d)/2 kesin yarımdır — çarpma ikinin kuvvetiyle olduğundan
// sınama kayıpsız. (v0.6.36 birim-karışımı dersi: değer+birim taşıyan her
// şablon HER birimiyle tablo-testli — fmtEta.test.ts.)
export function fixedHalfEven(x: number, d: number): string {
  const t = x * 2 ** (d + 1);
  if (Number.isInteger(t) && Math.abs(t) % 2 === 1) {
    const lower = (t * 5 ** d - 1) / 2; // x·10^d'nin altındaki tamsayı
    const n = lower % 2 === 0 ? lower : lower + 1;
    return (n / 10 ** d).toFixed(d);
  }
  return x.toFixed(d);
}

export function fmtEtaDays(days: number): string {
  if (!Number.isFinite(days) || days < 0) return '—';
  if (days < 1) {
    const h = days * 24;
    if (h < 1) return `${Math.floor(h * 60 + 0.5)} dakika`;
    return `${fixedHalfEven(h, 0)} saat`;
  }
  if (days < 10) return `${fixedHalfEven(days, 1)} gün`;
  return `${fixedHalfEven(days, 0)} gün`;
}

// etaChipLabel — rozet metni. 0 gün = regresyon doğrusu ZATEN tavanda
// (forecast.StatusAtLimit → diskETADays 0): "≈ 0 dakika kaldı" bitmiş bir
// geri sayım gibi okunur, oysa anlam "projeksiyon dolu" — SLO rozetinin
// 'breached' emsali.
export function etaChipLabel(days: number): string {
  if (Number.isFinite(days) && days <= 0) return 'dolu (projeksiyon tavanda)';
  return `≈ ${fmtEtaDays(days)} kaldı`;
}

// evaluatorQuietLabel — rozetin sayısı değerlendiricinin son tikinden
// gelir; değerlendirici susarsa satır (ve rozet) donar. Kalp atışı
// (/api/problems/evaluator, v0.9.550) ok değilse rozet soluklaşır ve bu
// ek yazılır. 'unknown' = ölçemedik; asla "iyi" sayılmaz.
export function evaluatorQuietLabel(h: { status: string; ageSec: number } | undefined): string {
  if (!h || h.status === 'ok') return '';
  if (h.status === 'stale') {
    const min = h.ageSec >= 0 ? Math.round(h.ageSec / 60) : 0;
    return min > 0 ? `değerlendirici ${min} dk sessiz` : 'değerlendirici sessiz';
  }
  if (h.status === 'failing') return 'değerlendirici hata veriyor';
  return 'değerlendirici durumu bilinmiyor';
}
