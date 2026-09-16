// occTick — v0.10.734 (operatör: "histogramda da zaman olsun, zaman çizelgesi
// yok gibi"). Exception detayı Occurrences grafiğinin x tiki HER ZAMAN
// tarih + saat ("13.09 11:35"): grup günler sürebilir; yalnız gün sınırında
// tarih basan ev formatı (fmtXTicks) burada "zaman yok" gibi okunuyordu. SAF.
export function fmtOccTick(tsSec: number): string {
  const d = new Date(tsSec * 1000);
  const p2 = (n: number) => String(n).padStart(2, '0');
  return `${p2(d.getDate())}.${p2(d.getMonth() + 1)} ${p2(d.getHours())}:${p2(d.getMinutes())}`;
}
