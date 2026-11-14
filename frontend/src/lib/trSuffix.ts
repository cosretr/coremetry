// trSuffix — Türkçe sayıya iyelik / bulunma eki (Clusters S4, v0.10.2169).
//
// Düz durum cümleleri sayıyla kurulur ("8 cluster: 6 sağlıklı, 1'inde kritik
// alarm var, 1'i yanıt vermiyor"). Ek, rakamın OKUNUŞUNA göre değişir:
// 1'i · 2'si · 3'ü · 6'sı · 9'u · 10'u · 40'ı — sabit "'i" yazmak "3'i",
// "6'i" üretir. Kural: sayının son SÖYLENEN sözcüğü (son sıfır olmayan
// basamak; hepsi sıfırsa onluk / yüz / bin / milyon / milyar) ünlü uyumunu
// ve kaynaştırma "s"sini belirler. Yalnız tam sayılar (kesir kırpılır).

// bir, iki, üç, dört, beş, altı, yedi, sekiz, dokuz
const DIGIT = ['', 'i', 'si', 'ü', 'ü', 'i', 'sı', 'si', 'i', 'u'] as const;
// on, yirmi, otuz, kırk, elli, altmış, yetmiş, seksen, doksan
const TENS = ['', 'u', 'si', 'u', 'ı', 'si', 'ı', 'i', 'i', 'ı'] as const;

/** İyelik ekinin kendisi (kesme işareti hariç): 3 → "ü", 6 → "sı". */
function possSuffix(n: number): string {
  const v = Math.abs(Math.trunc(n));
  if (v === 0) return 'ı'; // sıfır
  if (v % 10) return DIGIT[v % 10];
  if (v % 100) return TENS[(v % 100) / 10];
  if (v % 1000) return 'ü'; // yüz
  if (v % 1_000_000) return 'i'; // bin
  if (v % 1_000_000_000) return 'u'; // milyon
  return 'ı'; // milyar
}

/** "3'ü", "6'sı", "10'u" — "N tanesi" anlamında iyelik. */
export function trPoss(n: number): string {
  return `${Math.trunc(n)}'${possSuffix(n)}`;
}

/** "3'ünde", "6'sında", "10'unda" — iyelik + bulunma. */
export function trPossLoc(n: number): string {
  const p = possSuffix(n);
  const back = p.endsWith('ı') || p.endsWith('u'); // kalın ünlü → -da
  return `${Math.trunc(n)}'${p}n${back ? 'da' : 'de'}`;
}
