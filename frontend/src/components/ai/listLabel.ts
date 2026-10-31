// listLabel — v0.10.1720 (operatör: CoSRE Explain cevabındaki "İşlem Akışı
// ve Veri Özeti" listesinde "Operasyon: …", "Kritik hata noktası: …" gibi
// maddelerde etiket ile değer aynı ağırlık + renkte; ayırt edilemiyor).
// SAF. Şablon (copilot/prompts.go) düz `- Etiket: değer` istiyor, model de
// öyle yazıyor; düzeltme ÇİZİCİDE, modelden bağımsız ve deterministik.
//
// Etiket = maddenin başında, ilk ": " öncesi; harfle başlar, ≤4 sözcük,
// ≤32 karakter, yalnız harf / boşluk / kısa çizgi / kesme. Rakam, / . ( ) =
// → " _ ` * içermez: "Window 28.10…", URL, key=value, class.method, saat
// etiket sayılmaz. **kalın** ile başlayan madde zaten vurgulu: dokunulmaz.

export interface ListLabel { label: string; rest: string }

const MAX_CHARS = 32;
const MAX_WORDS = 4;
const RE = /^(\p{L}[\p{L} '’-]*):(?= \S)/u;

/** Etiket bulunursa {label:"Etiket:", rest:" değer"} (label+rest === girdi); yoksa null. */
export function splitListLabel(item: string): ListLabel | null {
  const m = RE.exec(item);
  if (!m) return null;
  const name = m[1];
  if (name.length > MAX_CHARS || name !== name.trimEnd()) return null;
  if (name.split(' ').filter(Boolean).length > MAX_WORDS) return null;
  return { label: m[0], rest: item.slice(m[0].length) };
}
