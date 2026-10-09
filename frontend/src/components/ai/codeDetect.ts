// codeDetect — v0.10.1145: yapıştırılan düz metin kod / log / stack trace /
// JSON / YAML / SQL / XML mi (SAF sezgiler). İki tüketici:
//   - composerPaste: ≥3 satırlık "kod gibi" metni çitli bloğa sarar;
//   - htmlToMarkdown: dili sınıftan okunamayan <pre> için etiket tahmini.
//
// Yanlış-pozitif, yanlış-negatiften PAHALI: operatörün düz cümlelerini kod
// bloğuna hapsetmek mesajı bozar (geri al bağlantısı olsa da). Bu yüzden
// her dalın bir YAPI şartı var — "Servis: x / Durum: y" gibi anahtar:değer
// düzyazısı YAML sayılmaz (girinti / liste / `---` yoksa), "Select … from"
// cümlesi SQL sayılmaz (ikinci bir yan tümce satırı + SQL noktalaması
// yoksa), virgülle biten satırlar kod sayılmaz.

export type CodeLang = 'json' | 'yaml' | 'sql' | 'xml' | 'log' | 'text';

const TS_LINE = /^\s*\[?(?:\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}|\d{2}:\d{2}:\d{2}[.,]\d+|[A-Z][a-z]{2} {1,2}\d{1,2} \d{2}:\d{2}:\d{2}|\d{1,2}\/[A-Z][a-z]{2}\/\d{4}:\d{2}:\d{2}|\d{10,13}\s)/;
const LEVEL_LINE = /^\s*\[?(?:TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|ERR|FATAL|CRITICAL|SEVERE)\b/;

// "at x.y(" — Java / JS / .NET çerçeveleri; Python, Go, istisna başlıkları.
const STACK: RegExp[] = [
  /^\s+at\s+[\w$.<>/[\]-]+\s*\(/,
  /^\s+at\s+[\w$<>-]+(?:\.[\w$<>-]+)+/,
  /^Traceback \(most recent call last\)/,
  /^\s*File ".+", line \d+/,
  /^goroutine \d+ \[/,
  /^panic: /,
  /^(?:Exception in thread "|Caused by: )/,
  /^\s+\.\.\. \d+ (?:more|common frames omitted)/,
  /^\s*(?:[a-z_$][\w$]*\.)+[A-Z][\w$]*(?:Exception|Error)\b/,
  /^\s+\S+\.go:\d+/,
];

const SQL_START = /^\s*(?:SELECT|WITH|INSERT\s+INTO|UPDATE|DELETE\s+FROM|CREATE\s+(?:OR\s+REPLACE\s+)?(?:TABLE|VIEW|INDEX|MATERIALIZED|DATABASE|FUNCTION)|ALTER\s+TABLE|DROP\s+(?:TABLE|VIEW|INDEX)|EXPLAIN|SHOW\s+\w+|DESCRIBE|TRUNCATE)\b/i;
const SQL_CLAUSE = /^\s*(?:FROM|WHERE|JOIN|LEFT|RIGHT|INNER|OUTER|FULL|CROSS|GROUP\s+BY|ORDER\s+BY|HAVING|LIMIT|OFFSET|UNION|VALUES|SET|AND|OR|ON|SETTINGS|FORMAT|PREWHERE|ARRAY\s+JOIN|SELECT|AS|INTO|RETURNING|WINDOW|QUALIFY)\b/i;

const YAML_KEY = /^\s*(?:-\s+)?["']?[\w.\-/@$]+["']?\s*:(?:\s|$)/;
const YAML_ITEM = /^\s*-\s+\S/;

const CODE_KEYWORD = /^\s*(?:func|def|class|import|package|return|const|let|var|public|private|protected|static|fn|impl|struct|interface|enum|#include|using|namespace|async\s+def|export\s+(?:default|const|function|class))\b/;
const CODE_CALL = /^\s*(?:if|for|while|switch|catch|elif|else\s+if)\s*\(/;
// Satır sonu: `{` `;` `(` `[` `);` `) {` — tek başına `)` DEĞİL ("(bkz. dün)").
const CODE_END = /(?:[{;([]|\)\s*[;{]|=>\s*\{?)\s*$/;
// Satır başı: kapanış ayracı, yorum, kabuk / REPL istemi. `@ad` DEĞİL (sohbet anması).
const CODE_START = /^\s*(?:[})\]]|\/\/|\/\*|\*\/|\$ |>>> )/;
// Güçlü işleçler; `->` DEĞİL (olay notlarında "svc-a -> svc-b" oku).
const CODE_OP = /(?:=>|:=|===|!==|&&|\|\||::|\+=|-=)/;
const ASSIGN = /^\s*[\w.$[\]]+\s*=\s*\S/;

function nonEmptyLines(text: string): string[] {
  return text.replace(/\r\n?/g, '\n').split('\n').filter(l => l.trim() !== '');
}

function isJson(t: string, lines: string[]): boolean {
  if (!/^[[{]/.test(t)) return false;
  if (/[\]}]$/.test(t)) {
    try { JSON.parse(t); return true; } catch { /* kırpık JSON — satır yapısına bak */ }
  }
  // NDJSON (JSON log satırları) ya da kırpık JSON gövdesi.
  if (lines.every(l => /^\s*\{.*\}\s*,?$/.test(l))) return true;
  const jsonish = lines.filter(l => /^\s*(?:"[^"\n]*"\s*:|[{}[\]],?$|"[^"\n]*",?$|-?\d[\d.eE+-]*,?$|(?:true|false|null),?$)/.test(l)).length;
  return jsonish / lines.length >= 0.8;
}

function isXml(t: string): boolean {
  return /^<(?:\?xml|!DOCTYPE|[A-Za-z][\w:.-]*)[\s>/]/.test(t) && />\s*$/.test(t) && /<\/[A-Za-z][\w:.-]*>|\/>/.test(t);
}

function isSql(t: string, lines: string[]): boolean {
  if (!SQL_START.test(lines[0])) return false;
  const clauses = lines.slice(1).filter(l => SQL_CLAUSE.test(l)).length;
  const sqlish = /\b(?:SELECT|FROM|WHERE|INSERT|UPDATE|JOIN)\b/.test(t) || /[=*;(]/.test(t);
  return (clauses >= 1 || lines.length === 1) && sqlish;
}

function isYaml(lines: string[]): boolean {
  const doc = lines.filter(l => !/^(?:---|\.\.\.)\s*$/.test(l)); // belge ayraçları oran dışı
  const keys = doc.filter(l => YAML_KEY.test(l)).length;
  const items = doc.filter(l => YAML_ITEM.test(l)).length;
  if (keys < 2 || (keys + items) / Math.max(1, doc.length) < 0.7) return false;
  // Yapı şartı: `---`, girintili satır ya da anahtar altında liste.
  if (lines[0].trim() === '---') return true;
  for (let i = 1; i < lines.length; i++) {
    const ind = /^\s*/.exec(lines[i])![0].length;
    const prevInd = /^\s*/.exec(lines[i - 1])![0].length;
    if (ind > prevInd && /:\s*$/.test(lines[i - 1])) return true;
  }
  return false;
}

function codeLineScore(lines: string[]): number {
  return lines.filter(l =>
    CODE_KEYWORD.test(l) || CODE_CALL.test(l) || CODE_END.test(l) || CODE_START.test(l) || CODE_OP.test(l) || ASSIGN.test(l),
  ).length;
}

/** Dil tahmini (satır sayısı şartı yok); kod gibi değilse ''. */
export function detectCodeLang(text: string): CodeLang | '' {
  const lines = nonEmptyLines(text);
  if (lines.length === 0) return '';
  const t = text.trim();
  if (isJson(t, lines)) return 'json';
  if (isXml(t)) return 'xml';
  const ts = lines.filter(l => TS_LINE.test(l) || LEVEL_LINE.test(l)).length;
  if (ts >= 2 && ts / lines.length >= 0.3) return 'log';
  const stack = lines.filter(l => STACK.some(re => re.test(l))).length;
  if (stack >= 2) return 'text';
  if (isSql(t, lines)) return 'sql';
  if (isYaml(lines)) return 'yaml';
  const code = codeLineScore(lines);
  const indented = lines.filter(l => /^(?: {2,}|\t)\S/.test(l)).length;
  if (code / lines.length >= 0.5) return 'text';
  if (code >= 1 && indented / lines.length >= 0.5 && /[{}();=]/.test(t)) return 'text';
  return '';
}

/**
 * Yapıştırılan çok satırlı düz metin kod bloğuna sarılmalı mı: ≥3 dolu
 * satır, henüz çit yok, ve bir dil tespit edildi. Dönüş: dil ya da null.
 */
export function detectCodePaste(text: string): CodeLang | null {
  if (!text || !text.includes('\n')) return null;
  const lines = nonEmptyLines(text);
  if (lines.length < 3) return null;
  if (/^\s{0,3}```/m.test(text)) return null;
  // Markdown tablosu / listesi zaten biçimli metin — dokunma.
  if (lines.every(l => /^\s*\|/.test(l))) return null;
  if (lines.every(l => /^\s*(?:[-*+]|\d{1,3}[.)])\s+\S/.test(l)) && !lines.some(l => YAML_KEY.test(l))) return null;
  const lang = detectCodeLang(text);
  return lang || null;
}
