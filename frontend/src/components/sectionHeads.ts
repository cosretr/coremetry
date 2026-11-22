// sectionHeads — v0.10.1538 (operatör: "cosre'de modeli gemma4 / qwen3:8b
// çevirdiğimde dönen cevapta hata zinciri, metod gibi başlıklar bold olmuyor").
//
// Kök neden: istemler başlıkları **kalın** ister (systemTraceBody, trace
// takibi, exception eki) ama küçük yerel modeller başlığı çoğu kez düz satır
// olarak yazar ("Hata zinciri:", "1. Kök Neden ve Sonraki Adım"). Büyük model
// talimatı tutar, küçük model tutmaz — çözüm modele göre istem değil, ÇİZİMDE
// modelden bağımsız bir normalleştirme: aynı cevap her modelde aynı görünür.
//
// SAF ve muhafazakâr — yalnız şu satırlar başlığa yükselir (çit içi asla):
//   1. Bilinen CoSRE bölüm adı tek başına (baştaki #/numara/*, sondaki : atılır).
//   2. Kısa, tek başına ve ":" ile biten etiket satırı (":" korunur) (≤ 6 kelime, ≤ 48 harf,
//      cümle noktalaması yok) — ör. "Hata zinciri:", "Metod:".
//   3. Bilinen bölüm adıyla başlayan satır ("Kök Neden: …") → "**Kök Neden:** …".
// Akışta bitmemiş son satıra dokunulmaz (yazılırken kalınlaşıp sönmesin).

const KNOWN = [
  'İşlem Akışı ve Veri Özeti', 'İşlem Akışı', 'Veri Özeti', 'Stacktrace Detayı', 'Kök Neden ve Sonraki Adım',
  'Kök Neden', 'Kök neden adayı', 'Sonraki Adım', 'Sonraki Adımlar', 'Eksik veri', 'Hata Zinciri', 'Hata ve Anlamı',
  'Hata ne demek', 'Nerede yoğunlaşıyor', 'Patlama mı, sürekli akış mı', 'Ne kontrol edilmeli', 'Trace özeti',
  'Hipotez', 'Teknik hatalar', 'Durum özeti', 'Yayılım ve Bağlam', 'Şüpheli Değişiklik', 'Metod', 'Metot',
  'Etkilenen metod', 'Etkilenen metot', 'Öneri', 'Öneriler', 'Çözüm', 'Çözüm Önerisi', 'Neden', 'Etki',
  // v0.10.2456 — trace açıklamasının Qwen-first bölümleri.
  'Kritik Yol', 'Hata Kaynağı', 'Olası Neden', 'Önerilen Sonraki Adım',
];
const lc = (s: string) => s.toLocaleLowerCase('tr');
const KNOWN_LC = new Set(KNOWN.map(lc));
// En uzun önce: "Kök Neden ve Sonraki Adım", "Kök Neden"den önce denensin.
const KNOWN_BY_LEN = [...KNOWN].sort((a, b) => b.length - a.length);

const FENCE = /^\s{0,3}(```|~~~)/;
const DECOR = /^\s{0,3}(?:#{1,6}\s+|\d{1,2}[.)]\s+)?(?:\*\*|__)?\s*/;

function core(line: string): { head: string; colon: boolean } | null {
  const t = line.replace(DECOR, '').replace(/\s*(?:\*\*|__)\s*$/, '').trim();
  if (!t) return null;
  const colon = /[:：]$/.test(t);
  const head = t.replace(/\s*[:：]$/, '').replace(/\s*(?:\*\*|__)$/, '').trim();
  return head ? { head, colon } : null;
}

/** SAF: tek satırı başlığa yükseltir; dokunmazsa aynı satırı döndürür. */
export function promoteHeadLine(line: string): string {
  if (/^\s{0,3}[-*+]\s/.test(line) || /^\s{0,3}[>|]/.test(line)) return line; // liste / alıntı / tablo
  if (/^\s{0,3}#{1,6}\s/.test(line)) return line; // zaten markdown başlığı
  const c = core(line);
  if (c && !/\*\*|`/.test(c.head)) {
    if (KNOWN_LC.has(lc(c.head))) return `**${c.head}**`;
    const words = c.head.split(/\s+/).length;
    if (c.colon && words <= 6 && c.head.length <= 48 && !/[.!?;]/.test(c.head) && /^[\p{Lu}]/u.test(c.head)) {
      return `**${c.head}:**`; // genel etiket: iki nokta korunur (metin değişmez, yalnız kalınlaşır)
    }
  }
  // 3. "Kök Neden: …" → "**Kök Neden:** …" (yalnız bilinen ad, önek düz yazılmışsa).
  const body = line.replace(/^\s{0,3}(?:\d{1,2}[.)]\s+)?/, '');
  if (!body.startsWith('**')) {
    for (const k of KNOWN_BY_LEN) {
      if (lc(body.slice(0, k.length)) === lc(k) && /^\s*[:：]\s+\S/.test(body.slice(k.length))) {
        return `**${body.slice(0, k.length)}:** ${body.slice(k.length).replace(/^\s*[:：]\s+/, '')}`;
      }
    }
  }
  return line;
}

/** SAF: metnin tamamı — çit içi korunur; akışta bitmemiş son satır korunur. */
export function promoteSectionHeads(text: string, streaming = false): string {
  if (!text) return text;
  const lines = text.split('\n');
  const partial = streaming && !text.endsWith('\n') ? lines.length - 1 : -1;
  let inFence = false;
  let changed = false;
  for (let i = 0; i < lines.length; i++) {
    if (FENCE.test(lines[i])) { inFence = !inFence; continue; }
    if (inFence || i === partial) continue;
    const next = promoteHeadLine(lines[i]);
    if (next !== lines[i]) { lines[i] = next; changed = true; }
  }
  return changed ? lines.join('\n') : text;
}
