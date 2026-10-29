// autoHighlight — v0.10.1567 (operatör: "Bir trace'i qwen3.8 flash ile
// açıklattığımda düz metin olarak geliyor, okunması zorlaşıyor … bazı kritik
// yerler highlight edilebilir"). SAF.
//
// Çizici `kod` ve **kalın**ı zaten çiziyor; küçük modeller bu işaretleri
// kullanmıyor ve sınıf adı, dosya:satır, HTTP 500, hata kodu düz metne
// gömülüyor. Burada MODELDEN BAĞIMSIZ, deterministik bir geçiş var: asistan
// cevabının DÜZ METİN parçalarında (kod / bağlantı içine girmez — çağıran
// yalnız `text` düğümünü verir) tanınan teknik parçalar çipe döner:
//
//   code  dosya:satır (Foo.java:151), paket-nitelikli ad (com.x.y.Foo …),
//         metot çağrısı (Foo.bar()), HTTP metotlu yol (POST /api/…)
//   err   HTTP 4xx/5xx, …Exception / …Error türleri, hata kodları
//         (MCA-00029, BSA_020, ORA-01017)
//
// Desenler bilinçli DAR: Türkçe düzyazıda yanlış çip, hiç çip olmamasından
// kötü. Tarih (28.10.2026), sürüm (1.4.2), oran (%3) ve tek noktalı sıradan
// sözcükler eşleşmez. Kullanıcı mesajında KAPALI (MdCtx.hl).

export type HlKind = 'code' | 'err';
export interface HlPart { v: string; kind?: HlKind }

// Sıra önemli: aynı konumda ilk alternatif kazanır (dosya:satır, paket adının
// önünde; paket adı içindeki Exception paket adıyla birlikte tek çip).
const SRC = [
  // 1 dosya:satır
  String.raw`(?<file>\b[\w$]+\.(?:java|kt|scala|groovy|go|py|cs|ts|tsx|js|rb|php):\d+\b)`,
  // 2 paket-nitelikli ad: ≥2 küçük harfli paket + Büyük harfle başlayan tip (+ .üye, $İç, ())
  String.raw`(?<fqcn>\b(?:[a-z_][\w]*\.){2,}[A-Z][\w$]*(?:\.[\w$]+)*(?:\(\))?)`,
  // 3 Tip.metot() — tek nokta, parantezli çağrı
  String.raw`(?<call>\b[A-Z][\w$]*\.[a-z_][\w$]*\(\))`,
  // 4 HTTP metotlu yol
  String.raw`(?<route>\b(?:GET|POST|PUT|DELETE|PATCH)\s+\/[\w\-./{}:]+)`,
  // 5 HTTP 4xx/5xx
  String.raw`(?<http>\bHTTP\s?[45]\d\d\b)`,
  // 6 exception / error türü
  String.raw`(?<exc>\b[A-Z][\w$]*(?:Exception|Error)\b)`,
  // 7 hata kodu: 2-6 büyük harf + - ya da _ + 2-6 rakam
  String.raw`(?<code>\b[A-Z]{2,6}[-_]\d{2,6}\b)`,
].join('|');

const RE = new RegExp(SRC, 'g');

const KIND: Record<string, HlKind> = {
  file: 'code', fqcn: 'code', call: 'code', route: 'code',
  http: 'err', exc: 'err', code: 'err',
};

/** Metni düz ve vurgulu parçalara böler; eşleşme yoksa tek düz parça. */
export function highlightParts(text: string): HlPart[] {
  const out: HlPart[] = [];
  let last = 0;
  RE.lastIndex = 0;
  for (let m = RE.exec(text); m; m = RE.exec(text)) {
    if (m[0] === '') { RE.lastIndex++; continue; }
    const g = m.groups ?? {};
    const name = Object.keys(KIND).find(k => g[k] !== undefined);
    if (!name) continue;
    if (m.index > last) out.push({ v: text.slice(last, m.index) });
    out.push({ v: m[0], kind: KIND[name] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ v: text.slice(last) });
  return out.length ? out : [{ v: text }];
}
