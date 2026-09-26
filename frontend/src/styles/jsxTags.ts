// jsxTags — v0.10.933 (tablo standardı T2) — paylaşılan TEST yardımcısı.
//
// Bir JSX açılış etiketini (`<tr … >`) süslü parantez derinliğiyle keser.
// Düz regex (`<tr\b[^>]*`) ilk `>`'de durur: `{...rowActivation(() => …)}`
// ya da `onClick={() => …}` içindeki ok fonksiyonu etiketi erken bitirir ve
// sonraki öznitelikler (ör. `style={{ cursor: … }}`) görünmez olur. Burada
// `>` yalnız derinlik 0'da etiketi bitirir; dizgi/şablon içi ve yorumlar
// atlanır (satır içi yorumlarda `<tr>'si` gibi kesme işaretleri geçiyor).
// Tüketiciler: ui/DataTable/rowAction.contract.test.tsx,
// styles/tableUnityRatchet.test.ts. Üretim kodu bunu içe aktarmaz.

export interface JsxTag { tag: string; line: number }

export function jsxOpenTags(src: string, name: string): JsxTag[] {
  const out: JsxTag[] = [];
  const re = new RegExp(`<${name}(?=[\\s>/])`, 'g');
  let m: RegExpExecArray | null;
  while ((m = re.exec(src)) !== null) {
    let i = m.index + 1 + name.length; let depth = 0; let q: string | null = null;
    for (; i < src.length; i++) {
      const c = src[i];
      if (q) { if (c === '\\') { i++; continue; } if (c === q) q = null; continue; }
      if (c === '/' && src[i + 1] === '/') { const nl = src.indexOf('\n', i); i = nl < 0 ? src.length : nl; continue; }
      if (c === '/' && src[i + 1] === '*') { const e = src.indexOf('*/', i + 2); i = e < 0 ? src.length : e + 1; continue; }
      if (c === '"' || c === "'" || c === '`') { q = c; continue; }
      if (c === '{') depth++;
      else if (c === '}') depth--;
      else if (c === '>' && depth === 0) break;
    }
    out.push({ tag: src.slice(m.index, i + 1), line: src.slice(0, m.index).split('\n').length });
  }
  return out;
}
