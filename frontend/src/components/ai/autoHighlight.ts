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
//         (MCA-00029, ACM_020, ORA-01017)
//
// Desenler bilinçli DAR: Türkçe düzyazıda yanlış çip, hiç çip olmamasından
// kötü. Tarih (28.10.2026), sürüm (1.4.2), oran (%3) ve tek noktalı sıradan
// sözcükler eşleşmez. Kullanıcı mesajında KAPALI (MdCtx.hl).
//
// v0.10.2374 (operatör: "trace açıklamasında … servisler arası çağrı, mesaj
// highlight, çift tırnak gösterimleri daha güzel olabilir") — yeni türler:
//
//   code  gRPC/metot adı (BagService/callBagService, RelationService.getRelations),
//         HTTP 2xx/3xx, BÜYÜK_YILAN ad (db0p.REL_TABLE_GER; ACM_020 hata kodu kalır)
//   err   OTel durum sözü "status error"
//   ent   servis-benzeri ad: küçük harf kebab, ≥2 tire, isteğe bağlı DNS eki
//         (customer-rel-prod-grpc.customer.svc.cluster.local). YALNIZ stil —
//         link YOK (servis bağlantısı entity modelinden geçer, ad eşlemesinden değil)
//   kv    anahtar=değer (requestId=…, mbb=null); değer boşluksuz
//   dur   süre (196 ms, 9.1 ms, 12 µs, 3 sn) — çip değil, tabular rakam
//   msg   çift tırnaklı mesaj ("…" / “…”, 3–200 karakter, tek satır); içindeki
//         tanınan parçalar `c` altında çip kalır. Tırnağın içi TEK bir çipse
//         (“ACM_020”) mesaj sayılmaz, çip kazanır.
//
// ExplainBody (✨ Explain / açıklama turu) de aynı geçişi kullanır (HlText.tsx).

export type HlKind = 'code' | 'err' | 'ent' | 'kv' | 'dur' | 'msg';
export interface HlPart { v: string; kind?: HlKind; c?: HlPart[] }

// Türkçe harf \w / \b kapsamında değil: sözcük sınırı elle (TR) yazılır ki
// "değer-a-b-c" ya da "5 saat" ortadan eşleşmesin.
const TR = 'ÇĞİÖŞÜçğıöşü';
const ERR_CODE = String.raw`[A-Z]{2,6}[-_]\d{2,6}\b`;

// Sıra önemli: aynı konumda ilk alternatif kazanır (dosya:satır, paket adının
// önünde; paket adı içindeki Exception paket adıyla birlikte tek çip).
const SRC = [
  // 1 dosya:satır
  String.raw`(?<file>\b[\w$]+\.(?:java|kt|scala|groovy|go|py|cs|ts|tsx|js|rb|php):\d+\b)`,
  // 2 paket-nitelikli ad: ≥2 küçük harfli paket + Büyük harfle başlayan tip (+ .üye, $İç, (), gRPC /metot)
  String.raw`(?<fqcn>\b(?:[a-z_][\w]*\.){2,}[A-Z][\w$]*(?:\.[\w$]+)*(?:\(\)|\/[A-Za-z_][\w$]*)?)`,
  // 3 Tip.metot() — tek nokta, parantezli çağrı
  String.raw`(?<call>\b[A-Z][\w$]*\.[a-z_][\w$]*\(\))`,
  // 4 HTTP metotlu yol
  String.raw`(?<route>\b(?:GET|POST|PUT|DELETE|PATCH)\s+\/[\w\-./{}:]+)`,
  // 5 HTTP 4xx/5xx
  String.raw`(?<http>\bHTTP\s?[45]\d\d\b)`,
  // 5b HTTP 2xx/3xx — nötr kod çipi
  String.raw`(?<httpok>\bHTTP\s?[23]\d\d\b)`,
  // 6 exception / error türü
  String.raw`(?<exc>\b[A-Z][\w$]*(?:Exception|Error)\b)`,
  // 6b OTel durum sözü
  String.raw`(?<st>\b[Ss]tatus error\b|\bSTATUS_CODE_ERROR\b)`,
  // 7 hata kodu: 2-6 büyük harf + - ya da _ + 2-6 rakam
  String.raw`(?<code>\b${ERR_CODE})`,
  // 8 DeveTip/metot ya da DeveTip.metot (parantezsiz): tip ≥2 hörgüç, metot camelCase
  String.raw`(?<rpc>\b[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]*)+[./][a-z]+[A-Z][A-Za-z0-9]*\b)`,
  // 9 anahtar=değer: anahtar tanımlayıcı, değer boşluksuz; değeri hata kodu olan
  //   çift (error_code=MCA-00029) kv DEĞİL — kırmızı hata çipi kazanır
  String.raw`(?<kv>(?<![\w${TR}.$?&/=-])[A-Za-z_][\w.]*=(?!${ERR_CODE})[^\s=,;·"“”'()<>][^\s,;·"“”'()<>]*(?<![.:!?]))`,
  // 10 BÜYÜK_YILAN ad (≥1 alt çizgi), isteğe bağlı şema öneki
  String.raw`(?<snake>(?<![\w.$-])(?:[A-Za-z][\w$]*\.)?[A-Z][A-Z0-9]+(?:_[A-Z0-9]+)+(?![\w-]))`,
  // 11 servis-benzeri ad: kebab, ≥2 tire (+ DNS eki). UUID ve "end-to-end"
  //    türü İngilizce deyimler (ortada to/of/in…) dışarıda.
  String.raw`(?<ent>(?<![\w${TR}./@-])(?![0-9a-f]{8}-[0-9a-f]{4}-)(?![a-z0-9-]*-(?:to|of|in|the|and|or|by|on|at|for|as|a)-)[a-z][a-z0-9]*(?:-[a-z0-9]+){2,}(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*(?![\w${TR}-]))`,
  // 12 süre: sayı + birim. Tarih / sürüm / saat / yüzde / çıplak sayı değil.
  String.raw`(?<dur>(?<![\w${TR}.,:%/-])\d+(?:[.,]\d+)?\s?(?:ms|µs|μs|ns|sn|dk|sa|s)(?![\w${TR}]))`,
  // 13 çift tırnaklı mesaj
  String.raw`(?<msg>(?<![\w${TR}])"(?=\S)[^"\n]{3,200}(?<=\S)"(?![\w${TR}])|“(?=\S)[^”\n]{3,200}(?<=\S)”)`,
].join('|');

const RE = new RegExp(SRC, 'g');

const KIND: Record<string, HlKind> = {
  file: 'code', fqcn: 'code', call: 'code', route: 'code', httpok: 'code', rpc: 'code', snake: 'code',
  http: 'err', exc: 'err', st: 'err', code: 'err',
  kv: 'kv', ent: 'ent', dur: 'dur', msg: 'msg',
};

/** Metni düz ve vurgulu parçalara böler; eşleşme yoksa tek düz parça. */
export function highlightParts(text: string): HlPart[] {
  const out: HlPart[] = [];
  let last = 0;
  // matchAll deseni kopyalar: iç içe çağrı (tırnak içi) lastIndex'i bozmaz.
  for (const m of text.matchAll(RE)) {
    const g = m.groups ?? {};
    const name = Object.keys(KIND).find(k => g[k] !== undefined);
    if (!name || m[0] === '') continue;
    const at = m.index ?? 0;
    if (at > last) out.push({ v: text.slice(last, at) });
    const kind = KIND[name];
    if (kind === 'msg') {
      const open = m[0][0];
      const close = m[0][m[0].length - 1];
      const inner = highlightParts(m[0].slice(1, -1));
      if (inner.length === 1 && inner[0].kind) {
        out.push({ v: open }, inner[0], { v: close });
      } else {
        out.push({ v: m[0], kind, c: [{ v: open }, ...inner, { v: close }] });
      }
    } else {
      out.push({ v: m[0], kind });
    }
    last = at + m[0].length;
  }
  if (last < text.length) out.push({ v: text.slice(last) });
  return out.length ? out : [{ v: text }];
}

/** Parçalarda (tırnak içi dahil) hata türü var mı. */
export function hasErrPart(parts: readonly HlPart[]): boolean {
  return parts.some(p => p.kind === 'err' || (p.c ? hasErrPart(p.c) : false));
}
