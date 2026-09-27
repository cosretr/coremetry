// trSuffix.ts — v0.10.968 — sayılara Türkçe ek (SAF).
//
// v0.10.968 — Onaylı kopya sayıya ek alıyor: "Kritik yolun %96'sı 9 pod'da",
// "3'ünde restart". Ek YAZILIŞA değil SÖYLENİŞE uyar: 96 "doksan altı" diye
// okunur, son sözcük "altı" → "'sı". Kural son sözcüğün son ünlüsü (ünlü
// uyumu) ve son harfi (ünlüyle bitene "s" kaynaştırması; bulunma ekinde sert
// ünsüzden sonra "t"). Servis adları ek ALMAZ (okunuşu bilinmez) — kopya o
// yüzden "Karşılaştırma servisi değişti: X." biçiminde.

const ONES = ['sıfır', 'bir', 'iki', 'üç', 'dört', 'beş', 'altı', 'yedi', 'sekiz', 'dokuz'];
const TENS = ['', 'on', 'yirmi', 'otuz', 'kırk', 'elli', 'altmış', 'yetmiş', 'seksen', 'doksan'];

/** v0.10.968 — sayının okunuşundaki SON sözcük (0 sıfır … 100 yüz, 1000 bin). */
export function trLastWord(n: number): string {
  if (!Number.isFinite(n)) return 'sıfır';
  let v = Math.abs(n);
  if (!Number.isInteger(v)) {
    // Kesirli sayı "iki virgül beş" diye okunur: son sözcük kesir hanelerinin
    // okunuşundan gelir (sondaki sıfırlar okunmaz).
    const frac = String(v).split('.')[1]?.replace(/0+$/, '') ?? '';
    v = frac ? Number(frac) : Math.trunc(v);
  }
  if (v === 0) return 'sıfır';
  if (v % 10 !== 0) return ONES[v % 10];
  if (v % 100 !== 0) return TENS[(v % 100) / 10];
  if (v % 1000 !== 0) return 'yüz';
  if (v % 1e6 !== 0) return 'bin';
  if (v % 1e9 !== 0) return 'milyon';
  return 'milyar';
}

const VOWELS = 'aeıioöuü';

function lastVowel(w: string): string {
  for (let i = w.length - 1; i >= 0; i--) if (VOWELS.includes(w[i])) return w[i];
  return 'e';
}

/** v0.10.968 — iyelik (3. tekil): "'sı" | "'si" | "'su" | "'sü" | "'ı" | "'i" | "'u" | "'ü". */
export function trPossessive(n: number): string {
  const w = trLastWord(n);
  const lv = lastVowel(w);
  const h = lv === 'a' || lv === 'ı' ? 'ı' : lv === 'e' || lv === 'i' ? 'i' : lv === 'o' || lv === 'u' ? 'u' : 'ü';
  return VOWELS.includes(w[w.length - 1]) ? `'s${h}` : `'${h}`;
}

/** v0.10.968 — bulunma: "'da" | "'de" | "'ta" | "'te". */
export function trLocative(n: number): string {
  const w = trLastWord(n);
  const lv = lastVowel(w);
  const back = lv === 'a' || lv === 'ı' || lv === 'o' || lv === 'u';
  const hard = 'fstkçşhp'.includes(w[w.length - 1]);
  return `'${hard ? 't' : 'd'}${back ? 'a' : 'e'}`;
}
