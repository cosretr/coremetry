// seriesFill — v0.10.2464 (operatör-raporlu: Metrics sekmesi runtime
// grafiklerinde "altı taralı"). CorePanel'in seri dolgu opaklığı kararı SAF
// ve tablo-testli.
//
// Kök neden: `viz='line'` dolgusuz demek DEĞİLDİ — line dalı v0.9.756'dan
// beri Grafana imzası olan hafif degrade alan dolgusunu (fillOpacity 12)
// çizer. v0.10.2446 RuntimePodChart'a `viz={'line'}` verdi ama 53 pod × 3
// nesil = 159 çizginin her biri tabana kadar %12 dolgu bastı; üst üste
// binince grafik taralı göründü. `plain` = YALNIZ çizgi: line / area
// dalında dolgu yok (CorePanel ayrıca 9.+ seriye renk-dışı kesikli deseni
// basmaz). Verilmezse karar bayt-aynı (diğer sayfaların grafikleri
// değişmez). Çubuk / yığın aileleri dolgusuz anlamsız — `plain` onlara
// dokunmaz.
//
// Alt–üst bant (fillBetween, HeapBaselineCard'ın taban bandı) seri dolgusu
// değil, ayrı uPlot band'ıdır — `plain` ona da dokunmaz.
export type SeriesViz = 'line' | 'bars' | 'area' | 'stacked' | 'stacked-bars';

export function seriesFillOpacity(viz: SeriesViz, dashed: boolean, plain: boolean): number {
  if (viz === 'stacked-bars') return 100;
  if (viz === 'bars') return 35;
  if (viz === 'stacked') return 28;
  if (plain || dashed) return 0;
  return viz === 'area' ? 60 : 12;
}
