// signalDetail — v0.10.1659 (CoSRE v2 Faz 2b B4, plan §3 / §5.6). Sinyal
// ayrıntısı (`/api/ai/signal` detail) en çok 512 BAYT UTF-8: karakter değil
// bayt — Türkçe harf 2 bayt, emoji 4. Kesim kod noktası sınırında (yarım
// çok-baytlı dizi gönderilmez); vekil çift bölünmez.
export const SIGNAL_DETAIL_MAX_BYTES = 512;

export function truncateUtf8(s: string, max = SIGNAL_DETAIL_MAX_BYTES): string {
  const enc = new TextEncoder();
  if (enc.encode(s).length <= max) return s;
  let out = '';
  let used = 0;
  for (const ch of s) { // for…of kod noktası gezer (vekil çift tek adım)
    const n = enc.encode(ch).length;
    if (used + n > max) break;
    out += ch;
    used += n;
  }
  return out;
}

/** Not metni → gönderilecek ayrıntı (kırpılmış, baştaki/sondaki boşluksuz); boşsa null. */
export function noteDetail(text: string): string | null {
  const t = text.trim();
  return t ? truncateUtf8(t) : null;
}
