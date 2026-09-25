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

// diskHistoryChip — v0.10.911 (parite #6 dilim 4): problem YOKKEN 7 günlük
// kalıcı tarihçe tahmininin rozeti. Ton sayıdan: <2 gün kırmızı, <7 gün sarı
// (açık problem eşiğiyle aynı sınır), üstü gri; geniş bandda "N+"; tahmin
// yoksa rozet değil soluk "⏳ yok · sebep".
export interface DiskHistoryFc {
  days: number; critical: boolean; status?: string; reason?: string; r2?: number;
  loDays?: number; hiDays?: number; hiOpen?: boolean; wide?: boolean; points?: number; windowDays?: number;
}
export function diskHistoryChip(f: DiskHistoryFc, source = 'kalıcı disk serisi', horizonDays = 30): { text: string; tone: 'b-err' | 'b-warn' | 'b-gray'; badge: boolean; title: string } {
  const base = `Son ${f.windowDays || 7} günün doğrusal eğilimi (${f.points ?? 0} nokta) · ${source}.`;
  if (f.status === 'at_limit') return { text: '⏳ dolu (eğilim tavanda)', tone: 'b-err', badge: true, title: base };
  if (f.status !== 'ok') {
    return { text: `⏳ yok · ${f.reason || 'tahmin kurulamadı'}`, tone: 'b-gray', badge: false,
      title: `${base} Tahmin yalnız eğim pozitif, uyum R² ≥ 0.6 ve ufuk ≤ ${horizonDays} günse gösterilir.` };
  }
  const tone = f.days < 2 ? 'b-err' : f.days < 7 ? 'b-warn' : 'b-gray';
  const r2 = f.r2 !== undefined ? ` · R² ${f.r2.toFixed(2)}` : '';
  const range = f.loDays !== undefined && f.loDays > 0
    ? ` Aralık ${fmtEtaDays(f.loDays)} – ${f.hiOpen ? 'üst sınır yok' : fmtEtaDays(f.hiDays ?? f.days)}.` : '';
  const text = f.wide ? `⏳ ${fmtEtaDays(f.loDays ?? f.days)}+` : `⏳ ≈ ${fmtEtaDays(f.days)}`;
  return { text: text + r2, tone, badge: true, title: base + range };
}

// clusterCapacityChip — v0.10.912: Clusters KPI kartı; disk rozetiyle aynı dil.
export function clusterCapacityChip(f: {
  status: string; days?: number; loDays?: number; hiDays?: number; hiOpen?: boolean; wide?: boolean;
  r2?: number; points: number; stepMin?: number; windowDays: number; reason?: string;
} | undefined) {
  if (!f) return null;
  const step = f.stepMin ? `${f.stepMin} dk adım, ` : '';
  return diskHistoryChip({ ...f, days: f.days ?? 0, critical: (f.days ?? 0) < 2 }, `${step}Thanos küme toplamı; kapasite = allocatable`, 30);
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
