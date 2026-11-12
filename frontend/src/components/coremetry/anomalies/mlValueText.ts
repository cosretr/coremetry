// mlValueText — v0.10.2044 (operatör: cümle 1,34 bin derken grafik 2,8 bin
// gösteriyordu). Kural: detay cümlesi panelde okunabilen ölçüyü söyler. Panel
// verisi geldiyse sunucu cümlesindeki değer parçası (card.value.text) panelin
// olay aralığındaki tepesiyle (aynı toplama) yeniden yazılır; dedektörün
// ölçtüğü değer "Teknik ayrıntı"da kalır. SAF; sunucu biçimlemesinin
// (internal/ml explain_text.go trInt/trDec + trNumWithSfx, explain_card.go
// valueFmt) aynası.
import type { ExplainValue } from '@/lib/types';

const ONES = ['sıfır', 'bir', 'iki', 'üç', 'dört', 'beş', 'altı', 'yedi', 'sekiz', 'dokuz'];
const TENS = ['', 'on', 'yirmi', 'otuz', 'kırk', 'elli', 'altmış', 'yetmiş', 'seksen', 'doksan'];

/** SAF: tam sayının Türkçe okunuşunun son sözcüğü (1.150 → "elli", 40.000 → "bin"). */
export function spokenLastWord(n: number): string {
  n = Math.abs(Math.trunc(n));
  if (n === 0) return 'sıfır';
  if (n % 10 !== 0) return ONES[n % 10];
  if (n % 100 !== 0) return TENS[(n % 100) / 10];
  if (n % 1000 !== 0) return 'yüz';
  if (n % 1_000_000 !== 0) return 'bin';
  if (n % 1_000_000_000 !== 0) return 'milyon';
  return 'milyar';
}

const VOWELS = 'aeıioöuü';

/** SAF: yönelme eki (kesmesiz): "-a/-e", ünlüden sonra "-ya/-ye". */
export function dativeFor(word: string): string {
  const w = word.toLocaleLowerCase('tr-TR');
  let lastV = 'e';
  for (let i = w.length - 1; i >= 0; i--) if (VOWELS.includes(w[i])) { lastV = w[i]; break; }
  const a = 'aıou'.includes(lastV) ? 'a' : 'e';
  return VOWELS.includes(w[w.length - 1] ?? '') ? `y${a}` : a;
}

/** SAF: trInt — binlik nokta, yuvarlanmış tam sayı ("2.800"). */
export function trInt(v: number): string {
  const n = Math.round(v);
  const s = String(Math.abs(n)).replace(/\B(?=(\d{3})+(?!\d))/g, '.');
  return n < 0 ? `-${s}` : s;
}

/** SAF: trDec — en çok `dec` ondalık, sondaki sıfırlar atılır ("1,34", "162"). */
export function trDec(v: number, dec: number): string {
  if (dec <= 0) return trInt(v);
  const p = 10 ** dec;
  const r = Math.round(v * p) / p;
  const whole = Math.trunc(r);
  const frac = Math.abs(r - whole).toFixed(dec).slice(2).replace(/0+$/, '');
  const head = r < 0 && whole === 0 ? '-0' : trInt(whole);
  return frac ? `${head},${frac}` : head;
}

/** SAF: sayı metni + kesme + yönelme eki (trNumWithSfx aynası; ondalıkta son ondalık parça okunur). */
export function numDative(text: string): string {
  const i = text.lastIndexOf(',');
  const digits = (i >= 0 ? text.slice(i + 1) : text).replace(/^[%-]+/, '').replace(/\./g, '');
  const n = Number(digits);
  const word = Number.isFinite(n) && digits !== '' ? spokenLastWord(n) : text;
  return `${text}'${dativeFor(word)}`;
}

const BYTE_UNITS: [number, string][] = [[2 ** 30, 'GB'], [2 ** 20, 'MB'], [2 ** 10, 'KB']];

/** SAF: dedektör birimine göre değer metni (valueFmt.one aynası). */
export function valueText(v: number, unit: string | undefined): { text: string; hasUnit: boolean } {
  switch (unit) {
    case 'bytes': {
      const [sc, u] = BYTE_UNITS.find(([s]) => v >= s) ?? [1, 'B'];
      return { text: `${trDec(v / sc, 1)} ${u}`, hasUnit: true };
    }
    case 's': {
      const ms = v * 1000;
      return ms >= 1000 ? { text: `${ms / 1000 < 10 ? trDec(ms / 1000, 1) : trInt(ms / 1000)} sn`, hasUnit: true } : { text: `${trInt(ms)} ms`, hasUnit: true };
    }
    case 'ms':
      return v >= 1000 ? { text: `${v / 1000 < 10 ? trDec(v / 1000, 1) : trInt(v / 1000)} sn`, hasUnit: true } : { text: `${trInt(v)} ms`, hasUnit: true };
    case 's/s': return { text: `${trDec(v, 2)} sn/sn`, hasUnit: true };
    case 'core': return { text: `${trDec(v, 2)} çekirdek`, hasUnit: true };
  }
  return { text: trDec(v, 2), hasUnit: false };
}

/** SAF: değer parçası — "2.800'e çıktı (grafikteki tepe)" / "1,2 GB seviyesine çıktı (grafikteki tepe)". */
export function peakSpan(peak: number, value: Pick<ExplainValue, 'unit' | 'direction'>): string {
  const { text, hasUnit } = valueText(peak, value.unit);
  const verb = value.direction === 'down' ? 'düştü' : 'çıktı';
  const what = value.direction === 'down' ? 'grafikteki en düşük değer' : 'grafikteki tepe';
  return `${hasUnit ? `${text} seviyesine` : numDative(text)} ${verb} (${what})`;
}

/** SAF: sunucu cümlesinde dedektör değeri parçasını panel tepesiyle değiştirir.
 *  Parça bulunamazsa ya da tepe sonlu değilse cümle aynen döner. */
export function withPanelPeak(summary: string, value: ExplainValue | undefined, peak: number | null): string {
  if (!value || peak == null || !Number.isFinite(peak) || !value.text || !summary.includes(value.text)) return summary;
  return summary.replace(value.text, peakSpan(peak, value));
}
