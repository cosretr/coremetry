// explainAnatomy.ts — v0.10.165: CoSRE cevap kartının SABİT anatomisi için
// saf çözümleyiciler (etüt seçenek A «Yapılandırılmış kart», dilim 1):
//   - verdictLine: «**Kök Neden ve Sonraki Adım**» (trace/exception,
//     prompts.go:84/249) ya da düz `Olası neden: …` (problem, prompts.go:172;
//     kalın DEĞİL) bölümünün ilk cümlesi = Karar satırı; bölüm o cümleden
//     ibaretse Karar ÇİZİLMEZ (yargıç must-fix: kopya). span/incident/
//     anomaly/service-health cevapları başlıksız ("no headers") → Karar yok,
//     bilinçli. v0.10.948 — beş başlıklı inceleme cevabında (**Bulgu** …)
//     Karar YOK: «Olası neden» hipotezdir, hüküm değil. v0.10.972 — o bölüm
//     «**Kök neden**» oldu, ilk satırı güven: YALNIZ "Güven: kesin"de Karar =
//     güven satırından sonraki ilk cümle; "olası" / güvensiz → Karar YOK.
//   - dropVerdictSentence: Karar çizilince AYNI cümle gövdeden düşer (madde
//     kalanını korur) — çift basım yok (inceleme #3).
//   - hoistCodeQuotes: gövdedeki OLUKLU kod alıntıları (N| önekli ya da
//     `// yol:a-b` başlıklı çitler) Kanıt'ın hemen altına taşınır; yerinde
//     «↑ kod alıntısı yukarıda» işareti kalır ki «aşağıdaki blok» gibi
//     göndermeler boşa düşmesin (#6); stack çitleri (oluksuz) «Stacktrace
//     Detayı»nda kalır.
// Sözleşme explainAnatomy.test.ts'te pinli. Akış SÜRERKEN çağrılmaz
// (yarım çit titremesin) — CopilotExplain yalnız bitmiş metne uygular.
import { hasGutter, parseFileHeader, stripMarker } from '@/lib/codeQuote';
import { dedent } from '@/components/Markdown';

// Kalın başlık (trace/exception) YA DA satır başı düz `Olası neden:` (problem).
const VERDICT_HDR = /(?:\*\*\s*(?:kök\s*neden[^*]*|olası\s*neden[^*]*|root\s*cause[^*]*)\*\*\s*:?\s*|^[ \t]*(?:kök\s*neden|olası\s*neden|root\s*cause)\s*:\s*)/im;
// v0.10.948 — "CoSRE'ye sor" inceleme cevabının imzası: satır başı kalın
// **Bulgu** başlığı (SystemPromptTraceInvestigation'ın beş başlığından ilki);
// model «**Bulgu:**» yazarsa da (SECTION_START gibi) iki nokta kabul.
const INVESTIGATION_SHAPE = /^[ \t]*\*\*[ \t]*bulgu[ \t]*:?[ \t]*\*\*/im;
// v0.10.972 — inceleme cevabının «Kök neden» başlığı (satır başı, kalın;
// «**Kök neden:**» de) ve ilk dolu satırındaki güven ("Güven: kesin" /
// "Güven: olası — <eksik halka>"; SystemPromptTraceInvestigation). Madde imi,
// kalın/italik işaret ve büyük harf toleranslı; güven başlıkla aynı satırda da olabilir.
// v0.10.972 — istemin kendi başlık biçimi «**X** — …» da kabul: «**Kök neden** — Güven: kesin»
// ve sarkan «**Kök neden** —» satırı (tire başlıkla birlikte tüketilir).
const INV_ROOT_HDR = /^[ \t]*\*\*[ \t]*kök[ \t]*neden[ \t]*:?[ \t]*\*\*[ \t]*(?:[:—–-][ \t]*)?/imu;
// v0.10.972 — «Güven: kesin değil / kesin olmayan» bir çekincedir, kesin DEĞİL:
// olumsuzlanan "kesin" güven sayılmaz (satır eşleşmez → Karar çizilmez).
const CONFIDENCE_LINE = /^[-–—*_>\s]*güven[*_\s]*:[*_\s]*(kesin(?![\s*_]*(?:değil|olma))|olası)(?!\p{L})/iu;
// Değeri tanınmasa da güven satırı («Güven: kesin değil», «Güven: orta»): Karar olmaz.
const CONFIDENCE_LABEL = /^[-–—*_>\s]*güven[*_\s]*:/iu;
// Güven taramasında boş sayılan satır: yalnız tire/boşluk (sarkan «—»).
const DASH_ONLY = /^[\s—–-]*$/;
// Bölüm sonu: satırın TAMAMI kalın başlık («**Eksik veri**»).
const HEADER_LINE = /^[ \t]*\*\*[^*\n]+\*\*[ \t]*:?[ \t]*$/m;

function stripInline(s: string): string {
  return s.replace(/\*\*([^*]+)\*\*/g, '$1').replace(/`([^`]+)`/g, '$1').replace(/^\s*[-*]\s+/, '').trim();
}

// Cümle sonu: [.!?] + boşluk + BÜYÜK harf / rakam / `kod` ya da metin sonu —
// «246. satırda» gibi sıra sayısı noktaları cümleyi bölmez; «2 span
// etkilendi.» ve «`orderId` boş.» gibi cevap-tipik cümle başları böler (#9).
function firstSentence(p: string): string {
  const m = p.match(/^(.+?[.!?])(?=\s+[A-ZÇĞİÖŞÜ0-9`]|\s*$)/);
  return (m ? m[1] : p).trim();
}

/**
 * v0.10.972 — başlıktan (`p`) sonraki ilk dolu satırın güveni: 'kesin' →
 * güven satırından SONRAKİ konum (Karar cümlesi oradan başlar), 'olası' →
 * null (Karar yok; değeri tanınmayan «Güven: …» satırı da), güven satırı
 * yok → undefined (çağıran eski davranışını sürdürür). Güven kuralı cevabın
 * ŞEKLİNE değil «Kök neden» bölümünün İÇERİĞİNE bağlıdır: **Bulgu** başlığı
 * farklı yazılsa da (## Bulgu, **Bulgular**, - **Bulgu**:) güven satırı
 * Karar olmaz. Yalnız tire olan satır (sarkan «—») boş sayılır.
 */
function applyConfidence(text: string, p: number): number | null | undefined {
  while (p < text.length) {
    let e = text.indexOf('\n', p); if (e < 0) e = text.length;
    const line = text.slice(p, e);
    if (!DASH_ONLY.test(line)) {
      const c = line.match(CONFIDENCE_LINE);
      if (c) return c[1].toLowerCase() === 'kesin' ? Math.min(e + 1, text.length) : null;
      return CONFIDENCE_LABEL.test(line) ? null : undefined;
    }
    p = e + 1;
  }
  return undefined;
}

/**
 * Karar cümlesinin başladığı konum (başlık/güven satırından sonrası); Karar
 * çizilmeyecekse null. v0.10.948 — beş başlıklı inceleme cevabında «Olası
 * neden» bir HİPOTEZDİR, hüküm değil: ilk cümlesini «Karar» şeridine çıkarmak
 * onu kesinleştirir, Bulgu → Kanıt → Olası neden sırasını bozar ve «ilişki,
 * neden değil» kaydını iddiasından koparırdı. v0.10.972 — o bölüm «Kök neden»
 * oldu: YALNIZ "Güven: kesin" Karar çizer; "olası" ya da güvensiz bölüm
 * bugünkü hipotez gibi (Karar yok → dropVerdictSentence de koşmaz, bölüm
 * bütün). `sources`suz önbellek isabeti de aynı metni taşıdığından denetim
 * prop'ta değil burada, metnin şeklinde.
 */
function verdictStart(text: string): number | null {
  if (INVESTIGATION_SHAPE.test(text)) {
    const m = text.match(INV_ROOT_HDR);
    if (!m || m.index === undefined) return null;
    const c = applyConfidence(text, m.index + m[0].length);
    return typeof c === 'number' ? c : null; // yalnız "kesin"; olası / güvensiz → Karar yok
  }
  const m = text.match(VERDICT_HDR);
  if (!m || m.index === undefined) return null;
  // v0.10.972 — şekil dışı cevapta da güven satırı Karar olmaz: olası → yok,
  // kesin → güven satırından sonrası; güven satırı yoksa eski davranış.
  const s = m.index + m[0].length;
  const c = applyConfidence(text, s);
  return c === undefined ? s : c;
}

/** Karar satırı; yoksa ya da bölüm tek cümleyse null. ≤ 220 karakter. */
export function verdictLine(text: string | null | undefined): string | null {
  if (!text) return null;
  const start = verdictStart(text);
  if (start === null) return null;
  let after = text.slice(start);
  if (INVESTIGATION_SHAPE.test(text)) {
    // v0.10.972 — güven satırından sonra boş satır olabilir; bölüm bir
    // sonraki tam-kalın başlık satırında biter (yalnız güven satırı → Karar yok).
    after = after.replace(/^(?:[ \t]*\n)+/, '');
    const end = after.search(HEADER_LINE);
    if (end >= 0) after = after.slice(0, end);
  }
  // bölümün ilk paragrafı: boş satıra ya da bir sonraki **başlığa** kadar
  const para = after.split(/\n\s*\n|\n(?=\s*\*\*)/)[0] ?? '';
  const lines = para.split('\n').map(stripInline).filter(Boolean);
  if (lines.length === 0) return null;
  const sentence = firstSentence(lines[0]);
  if (!sentence) return null;
  const whole = lines.join(' ').trim();
  if (whole === sentence) return null; // bölüm = tek cümle → kopya olur
  // v0.10.358 — Operator-reported: "kod incelemesinde kök neden kesilmiş".
  // 220 karakterlik kesim cümleyi ortadan kırpıyordu ve dropVerdictSentence
  // cümleyi gövdeden de düşürdüğü için kalan yarısı HİÇBİR yerde
  // görünmüyordu. Karar cümlesi bütün gösterilir; yalnız patolojik (>1000)
  // çıktıya karşı tavan.
  return sentence.length > 1000 ? sentence.slice(0, 999) + '…' : sentence;
}

/**
 * Karar cümlesini gövdeden düşürür: başlıktan sonraki ilk dolu satırda,
 * inline işaretler soyulunca `sentence` ile başlayan en kısa ham önek kesilir
 * (kalın/kod işaretleri kalanında korunur); satır boşalırsa satır gider,
 * madde imi kalanın başına taşınır. Bulunamazsa metin aynen.
 */
export function dropVerdictSentence(text: string, sentence: string): string {
  const start = verdictStart(text); // v0.10.972 — inceleme cevabında güven satırından sonra
  if (start === null) return text;
  // başlığın bittiği satır (inline «Olası neden: …» aynı satırda sürer)
  let lineStart = text.lastIndexOf('\n', start - 1) + 1;
  let lineEnd = text.indexOf('\n', start); if (lineEnd < 0) lineEnd = text.length;
  let raw = text.slice(lineStart, lineEnd);
  let off = start - lineStart; // satır içinde cümlenin başladığı yer
  if (raw.slice(off).trim() === '') {
    // başlık kendi satırında → ilk dolu satır
    let p = lineEnd + 1;
    while (p < text.length) {
      let e = text.indexOf('\n', p); if (e < 0) e = text.length;
      if (text.slice(p, e).trim() !== '') { lineStart = p; lineEnd = e; raw = text.slice(p, e); off = 0; break; }
      p = e + 1;
    }
    if (off !== 0) return text;
  }
  const head = raw.slice(0, off);
  const tail = raw.slice(off);
  const bullet = tail.match(/^\s*[-*]\s+/)?.[0] ?? '';
  let cut = -1;
  for (let k = bullet.length + 1; k <= tail.length; k++) {
    if (stripInline(tail.slice(0, k)) === sentence) { cut = k; break; }
  }
  if (cut < 0) return text;
  const remainder = tail.slice(cut).trim();
  const keepHead = head.trim() !== '' && off > 0 && !/\*\*\s*$/.test(head) ? head : '';
  const line = remainder ? (keepHead || bullet.trimStart() || '') + remainder : keepHead.trim();
  const before = text.slice(0, lineStart);
  const after = text.slice(lineEnd);
  return line ? before + line + after : before + after.replace(/^\n/, '');
}

export interface CodeQuote { lang: string; lines: string[] }
const HOIST_MARK = '*↑ kod alıntısı yukarıda*';

/** Oluklu/başlıklı kod çitlerini gövdeden çıkarır; öteki çitler (stack) yerinde kalır. */
export function hoistCodeQuotes(text: string): { quotes: CodeQuote[]; rest: string } {
  const src = text.split('\n');
  const quotes: CodeQuote[] = [];
  const out: string[] = [];
  let i = 0;
  while (i < src.length) {
    const line = src[i];
    if (line.trimStart().startsWith('```')) {
      const lang = line.trimStart().slice(3).trim();
      const body: string[] = [];
      let j = i + 1;
      while (j < src.length && !src[j].trimStart().startsWith('```')) { body.push(src[j]); j++; }
      const closed = j < src.length;
      const dedented = dedent(body);
      const isQuote = closed && (hasGutter(dedented) || !!parseFileHeader(dedented[0]));
      if (isQuote) {
        quotes.push({ lang, lines: dedented });
        // çitin yerinde işaret: madde/prose göndermeleri boşa düşmesin (#6)
        out.push(line.slice(0, line.length - line.trimStart().length) + HOIST_MARK);
        i = j + 1;
        continue;
      }
      // çit olduğu gibi kalır
      for (let k = i; k <= Math.min(j, src.length - 1); k++) out.push(src[k]);
      i = j + 1;
      continue;
    }
    out.push(line);
    i++;
  }
  return { quotes, rest: out.join('\n').replace(/\n{3,}/g, '\n\n') };
}

export { stripMarker };

// ── v0.10.948 (CoSRE Faz B) — sunucunun "Kaynak durumu" dipnotu ──────────
//
// explain-trace cevabının SONUNA sunucu deterministik bir "Kaynak durumu"
// bloğu ekler (model atlasa bile her kaynak + durumu; ok olmayanlar eksik
// veri). Aynı bilgi cevap çerçevesinde yapısal `sources` olarak da gelir ve
// kart onu rozet dipnotu olarak çizer; metindeki blok o zaman İKİNCİ bir kopya
// olurdu. splitSourceFooter bloğu gövdeden ayırır — yalnız yapısal `sources`
// VARKEN çağrılır (eski sunucu / önbellekteki eski cevap metni aynen kalır).
// Sohbet bağlamına (onAnswer) giden HAM metin değişmez; bu yalnız çizim.
//
// Sınır: SON "Kaynak durumu" başlık satırı (model ortada kendi bölümünü
// yazdıysa sunucununki yine sondadır); blok bir sonraki cevap başlığında
// (beş kalın başlıktan biri ya da `#` başlığı) veya satır başı "⚠" uyarısında
// (sayı denetimi: "⚠ Kanıtta bulunamayan sayı(lar)") biter — ikisi de
// gövdede KALIR. Genel `**…**` satırı sınır DEĞİL: dipnot maddeleri
// "**logs/elasticsearch**: erişilemedi" biçiminde de gelebilir. Hemen
// üstteki `---` ayracı blokla birlikte gider.
const SOURCE_FOOTER_HDR = /^[ \t>]*(?:#{1,6}[ \t]*)?(?:[*_]{1,2}[ \t]*)?kaynak durumu\b/i;
// v0.10.972 — «Stacktrace detayı» ve «Kök neden» de cevap başlığı («Olası neden» eski önbellek metni için kalır).
// v0.10.986 — ilk cevap klasik üçlüde: «İşlem Akışı ve Veri Özeti», «Kök Neden ve Sonraki Adım» (eski beşli önbellek metni için kalır).
const SECTION_START = /^[ \t]*(?:#{1,6}[ \t]+\S|\*\*[ \t]*(?:bulgu|kanıt|stacktrace detayı|kök neden(?: ve sonraki adım)?|olası neden|eksik veri|sonraki kontrol|işlem akışı ve veri özeti)[ \t]*[*:])/i;
const WARN_LINE = /^[ \t>*_]*⚠/;
const RULE_LINE = /^[ \t]*([-*_])(?:[ \t]*\1){2,}[ \t]*$/;

export function splitSourceFooter(text: string): { body: string; footer: string | null } {
  const lines = text.split('\n');
  let start = -1;
  for (let k = lines.length - 1; k >= 0; k--) {
    if (SOURCE_FOOTER_HDR.test(lines[k])) { start = k; break; }
  }
  if (start < 0) return { body: text, footer: null };
  let end = lines.length;
  for (let k = start + 1; k < lines.length; k++) {
    if (WARN_LINE.test(lines[k]) || SECTION_START.test(lines[k])) { end = k; break; }
  }
  let cut = start;
  let p = start - 1;
  while (p >= 0 && lines[p].trim() === '') p--;
  if (p >= 0 && RULE_LINE.test(lines[p])) cut = p;
  const footer = lines.slice(start, end).join('\n').trim();
  const body = [...lines.slice(0, cut), ...lines.slice(end)].join('\n').replace(/\n{3,}/g, '\n\n').trim();
  return { body, footer };
}
