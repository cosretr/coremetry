// chatMarkdown — sohbet balonunun BLOK ÇÖZÜCÜSÜ (v0.9.1148, AI Faz 4.2).
//
// NEDEN VAR: Faz 3 tool'ları geldikten sonra model artık tool
// sonuçlarından TABLO ve KOD üretiyor ("hangi servisler yavaş" → 3
// kolonlu markdown tablosu, "sorguyu göster" → ```sql fence). Balon o
// güne dek mdLite'tı: `kod` + **kalın** + trace linki, gerisi düz metin.
// Sonuç ekranda ham `| a | b |` satırları ve çıplak ``` çitleriydi —
// tam olarak v0.9.641/696'nın "yıldızlar görünüyor" kusurunun tablo
// sürümü.
//
// NEDEN RenderedMarkdown DEĞİL: components/Markdown.tsx zaten var ve
// başlık/liste/fence biliyor, ama chat'e üç şeyi getiremiyor:
//   1. AKIŞ. Balon SSE delta'larıyla harf harf büyüyor. RenderedMarkdown
//      metni "bitmiş" varsayar: yarım bir fence'i kapanmış sayar ve
//      satırın kalanını yutar, yarım bir tablo satırını ham basar.
//      Burada çözücü `streaming` bayrağını alıyor ve BİTMEMİŞ son satırı
//      biliyor (aşağıdaki "kısmi satır" bölümü).
//   2. ```chart fence'i. Balonun kendine ait bir anlamı var (CosreChart,
//      v0.9.183) — Markdown.tsx onu kod bloğu sanar ve operatöre ham
//      JSON döker.
//   3. trace-id linkleri (mdLite, v0.9.419). Markdown.tsx'e eklemek
//      runbook/exception yüzeylerini de değiştirirdi.
// Yani paylaşım DOĞRU katmanda: çözücü chat'e özel, satır-içi kaçış
// disiplini (mdLite → escapeHTML) ORTAK ve değişmedi.
//
// SAFLIK: burada React yok, DOM yok. Çizim ChatBubble'da; bu dosya
// yalnız metin → blok listesi. Kısmi-akış vakalarının hepsi bu yüzden
// saf testle çivilenebiliyor (chatMarkdown.test.ts).

import { inlinePlain, parseInline } from './chatInline';

export type ChatBlockAlign = 'left' | 'center' | 'right';
export type CalloutVariant = 'summary' | 'note' | 'tip' | 'warning' | 'important' | 'caution';

// GFM uyarı türleri + Türkçe karşılıkları (büyük/küçük harf duyarsız).
const CALLOUT_MAP: Record<string, CalloutVariant> = {
  note: 'note', not: 'note', bilgi: 'note',
  tip: 'tip', 'ipucu': 'tip', 'i̇pucu': 'tip',
  warning: 'warning', uyari: 'warning', 'uyarı': 'warning',
  important: 'important', onemli: 'important', 'önemli': 'important',
  caution: 'caution', dikkat: 'caution',
  summary: 'summary', tldr: 'summary', 'tl;dr': 'summary', ozet: 'summary', 'özet': 'summary',
};
const CALLOUT_HEAD = /^\[!([^\]\s]{2,12})\]\s*(.*)$/;

/** SAF: `[!TÜR]` başlığını türe çevirir (tanınmayan → null). */
export function calloutVariant(tag: string): CalloutVariant | null {
  return CALLOUT_MAP[tag.toLocaleLowerCase('tr')] ?? CALLOUT_MAP[tag.toLowerCase()] ?? null;
}

// En üst "**Özet:** …" satırı (TL;DR) → özet kutusu.
const SUMMARY_LINE = /^\s{0,3}\*\*(?:Özet|ÖZET|Ozet|TL;DR|Kısaca)\s*:?\s*\*\*\s*:?\s*(.+)$/;

/**
 * SAF: fence bilgi dizesi → dil + dosya başlığı. Kabul edilen biçimler:
 * "yaml", "yaml title=deploy/app.yaml", 'yaml title="a b.yaml"', "yaml:deploy/app.yaml".
 * Dil [a-z0-9_+.#-] ile sınırlı (sınıf adına/etikete giden değer), başlık
 * 200 karakterde kesilir; ikisi de yalnız METİN olarak çizilir (React kaçışı).
 */
export function parseFenceInfo(info: string): { lang: string; title?: string } {
  const s = info.trim();
  let lang = '';
  let title: string | undefined;
  const tm = /(?:^|\s)(?:title|file|filename)=(?:"([^"]*)"|'([^']*)'|(\S+))/i.exec(s);
  if (tm) title = (tm[1] ?? tm[2] ?? tm[3] ?? '').trim();
  const first = s.split(/\s+/)[0] ?? '';
  if (first && !/=/.test(first)) {
    const c = first.indexOf(':');
    if (c > 0) { lang = first.slice(0, c); if (!title) title = first.slice(c + 1); }
    else lang = first;
  }
  lang = lang.toLowerCase().replace(/[^a-z0-9_+.#-]/g, '').slice(0, 20);
  if (title !== undefined) {
    title = title.slice(0, 200);
    if (!title) title = undefined;
  }
  return title ? { lang, title } : { lang };
}

const EXT: Record<string, string> = {
  yaml: 'yaml', yml: 'yml', json: 'json', sh: 'sh', bash: 'sh', shell: 'sh', zsh: 'sh', sql: 'sql',
  go: 'go', ts: 'ts', typescript: 'ts', js: 'js', javascript: 'js', py: 'py', python: 'py', java: 'java',
  xml: 'xml', toml: 'toml', ini: 'ini', dockerfile: 'Dockerfile', diff: 'diff', md: 'md', markdown: 'md',
  groovy: 'groovy', properties: 'properties', html: 'html', css: 'css', hcl: 'tf', tf: 'tf',
};

// v0.10.1137 inceleme — indirme uzantısı İZİN LİSTESİ: dil uzantıları +
// düz metin türleri. Listede olmayan uzantıya ".txt" eklenir; çalıştırılabilir
// / tarayıcıda yorumlanan türler (EXEC_EXT) listede olsa bile HER ZAMAN ".txt"
// alır — model "deploy.bat" başlıklı bir blok yazsa da indirilen dosya çift
// tıkla çalışmaz.
const EXEC_EXT = new Set(['bat', 'cmd', 'ps1', 'exe', 'hta', 'js', 'vbs', 'scr', 'msi', 'jar', 'com', 'html', 'htm', 'svg']);
const SAFE_EXT = new Set([
  ...Object.values(EXT).map(e => e.toLowerCase()),
  'txt', 'md', 'json', 'yaml', 'yml', 'log', 'csv', 'sql', 'sh', 'xml', 'conf', 'ini', 'properties', 'tf', 'diff', 'patch',
].filter(e => !EXEC_EXT.has(e)));
// Uzantısız ama bilinen dosya adları (olduğu gibi kalır).
const BARE_NAMES = new Set(['dockerfile', 'makefile', 'jenkinsfile', 'vagrantfile', 'procfile']);

function withSafeExt(n: string): string {
  const dot = n.lastIndexOf('.');
  const ext = dot > 0 ? n.slice(dot + 1).toLowerCase() : '';
  if (!ext) return BARE_NAMES.has(n.toLowerCase()) ? n : `${n}.txt`;
  if (EXEC_EXT.has(ext) || !SAFE_EXT.has(ext)) return `${n}.txt`;
  return n;
}

/**
 * SAF: indirilecek dosya adı. Yalnız SON yol parçası; [A-Za-z0-9._-] dışı her
 * karakter "_"; baştaki noktalar/tireler atılır (gizli dosya / bayrak gibi
 * görünmesin); "..", boş ve rezerve adlar yedeğe düşer; 100 karakter tavanı;
 * uzantı izin listesinden (withSafeExt). Yedek: "snippet.<dil uzantısı>".
 */
export function sanitizeFileName(title: string | undefined, lang: string): string {
  const fallback = lang === 'dockerfile' ? 'Dockerfile' : withSafeExt(`snippet.${EXT[lang] && EXT[lang] !== 'Dockerfile' ? EXT[lang] : 'txt'}`);
  const raw = (title ?? '').split(/[\\/]/).pop() ?? '';
  let n = raw.normalize('NFKD').replace(/[\u0300-\u036f]/g, '').replace(/[^A-Za-z0-9._-]+/g, '_');
  n = n.replace(/^[._-]+/, '').replace(/_+/g, '_').slice(0, 100).replace(/[._-]+$/, '');
  if (!n || /^\.+$/.test(n) || /^(con|prn|aux|nul|com\d|lpt\d)(\..*)?$/i.test(n)) return fallback;
  return withSafeExt(n);
}

/** SAF: kod gövdesinin satır sayısı (son boş satır sayılmaz). */
export function codeLineCount(code: string): number {
  if (!code) return 0;
  return code.replace(/\n$/, '').split('\n').length;
}

/** Kod bloğu bu kadar satırı aşarsa katlanır ("Tümünü göster (N satır)"). */
export const CODE_COLLAPSE_LINES = 25;

/** SAF: diff satırının türü (+ ekleme / - silme / @@ bağlam başlığı). */
export function diffLineKind(line: string): 'add' | 'del' | 'hunk' | 'meta' | null {
  if (line.startsWith('+++') || line.startsWith('---')) return 'meta';
  if (line.startsWith('@@')) return 'hunk';
  if (line.startsWith('+')) return 'add';
  if (line.startsWith('-')) return 'del';
  return null;
}

/**
 * SAF: "**Anahtar:** değer" satırlarından oluşan paragraf → tanım satırları
 * (her satır bu biçimdeyse; değilse null). Çizim bunu iki kolonlu bir tanım
 * listesi olarak basar.
 */
export function definitionRows(text: string): { key: string; value: string }[] | null {
  const lines = text.split('\n').filter(l => l.trim() !== '');
  if (lines.length === 0) return null;
  const out: { key: string; value: string }[] = [];
  for (const l of lines) {
    const m = /^\s*\*\*([^*\n]{1,60}?)\s*:\s*\*\*\s*(.*)$/.exec(l) ?? /^\s*\*\*([^*\n]{1,60}?)\*\*\s*:\s*(.*)$/.exec(l);
    if (!m) return null;
    out.push({ key: m[1].trim(), value: m[2] });
  }
  return out;
}

const DETAILS_OPEN = /^\s{0,3}<details(\s+open)?\s*>\s*(?:<summary>(.*?)<\/summary>)?\s*$/i;
const SUMMARY_TAG = /^\s*<summary>(.*?)<\/summary>\s*$/i;
const DETAILS_CLOSE = /^\s*<\/details>\s*$/i;

export type ChatBlock =
  // Düz metin koşusu — mdLite ile satır içi işaretlenir, balonun
  // pre-wrap'ı satır sonlarını korur (bugünkü davranış birebir).
  | { kind: 'text'; text: string }
  // v0.10.1137 — dört kademe (####); ötesi 4'e kırpılır.
  | { kind: 'heading'; level: 1 | 2 | 3 | 4; text: string }
  // v0.10.1137 — `nested[i]` = i. maddenin altındaki girintili içerik
  // (alt liste, kod, düzyazı) — yalnız varsa alan yazılır; `start` sıralı
  // listede 1'den farklı ilk numara.
  | { kind: 'list'; ordered: boolean; items: string[]; nested?: (ChatBlock[] | null)[]; start?: number; tasks?: (boolean | null)[] }
  // v0.10.1137 — `> alıntı` (içerik yeniden bloklara çözülür) ve `---` çizgi.
  | { kind: 'quote'; blocks: ChatBlock[] }
  | { kind: 'hr' }
  // v0.10.1137 — GFM uyarı kutusu (`> [!NOTE]` …, TR karşılıkları) ve en
  // üstteki "**Özet:**" satırı / `> [!ÖZET]` bloğu (variant 'summary').
  | { kind: 'callout'; variant: CalloutVariant; title: string; blocks: ChatBlock[] }
  // v0.10.1137 — `<details><summary>…</summary>…</details>`: YALNIZ bu iki
  // etiket tanınır; özet metni ve gövde markdown olarak çözülür (React kaçışlı),
  // başka hiçbir HTML etiketi işlenmez. `closed:false` = kapanış etiketi yok.
  | { kind: 'details'; summary: string; blocks: ChatBlock[]; closed: boolean; open: boolean }
  // `open: true` = kapanış çiti HENÜZ gelmedi (akış sürüyor ya da
  // yanıt kesildi). Çizim tarafı kopyala butonunu buna göre saklıyor:
  // yarım kod kopyalanırsa operatör sessizce eksik komut çalıştırır.
  // v0.10.1137 — `title`: bilgi dizesindeki dosya yolu (```yaml title=a.yaml / ```yaml:a.yaml).
  | { kind: 'code'; lang: string; code: string; open: boolean; title?: string }
  | { kind: 'table'; head: string[]; align: ChatBlockAlign[]; rows: string[][] };

const FENCE = /^\s{0,3}```/;
const HEADING = /^(#{1,6})\s+(.*)$/;
const BULLET = /^\s{0,3}[-*+]\s+(.*)$/;
const ORDERED = /^\s{0,3}\d{1,3}[.)]\s+(.*)$/;
// Herhangi bir girintide madde işareti: [girinti, işaret, metin].
const ANY_ITEM = /^(\s*)([-*+]|\d{1,3}[.)])\s+(.*)$/;
const HR = /^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;

// Kısmi satırda "bitmemiş BLOK İŞARETİ" kalıpları. Bu üçü tutulur
// (ekrana basılmaz), gerisi basılır — gerekçe parseChatBlocks'ta.
const PARTIAL_FENCE = /^\s{0,3}`{1,3}[a-z0-9_+-]*$/i;
const PARTIAL_ROW = /^\s{0,3}\|/;
const PARTIAL_HEADING = /^\s{0,3}#{1,6}\s*$/;

/** Satır bir tablo satırı gibi mi duruyor: `|` ile BAŞLIYOR mu. */
function isRowLine(line: string): boolean {
  return line.trim().startsWith('|');
}

// Ayraç satırı: `|---|---:|`. Kural GFM'in kendisi — hücrelerin HEPSİ
// tire (istenirse iki yanında hizalama iki noktası) ve hücre SAYISI
// başlıkla aynı.
//
// Sayı eşitliği asıl kapı: onsuz `| önemli | not |` satırının altındaki
// bir `---` yatay çizgisi ayraç sanılır ve düzyazı boş bir tabloya
// dönüşür. Baştaki çubuğu ŞART koşmuyoruz (model bazen `---|---:`
// yazıyor) — yanlış-pozitife karşı asıl çapa BAŞLIK satırının çubukla
// başlaması (isRowLine), yani "hızlı | yavaş" gibi bir cümle hiçbir
// hâlde tablo başlatmıyor.
function isDelimRow(line: string, ncols: number): boolean {
  const s = line.trim();
  if (!/-/.test(s)) return false;
  if (!/^[|\s:-]+$/.test(s)) return false;
  const cells = splitRow(s);
  return cells.length === ncols && cells.every(c => /^:?-+:?$/.test(c));
}

// splitRow — bir tablo satırını hücrelere böler. İki tuzağı biliyor:
//   `\|`      → kaçırılmış çubuk, hücre İÇERİĞİ (ayırıcı değil).
//   `` `a|b` `` → kod aralığındaki çubuk bölmez; aksi hâlde model
//              `p95 | p99` gibi bir kod parçası yazdığında kolonlar
//              kayardı.
export function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith('|')) s = s.slice(1);
  if (s.endsWith('|') && !s.endsWith('\\|')) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = '';
  let inCode = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === '\\' && s[i + 1] === '|') { cur += '|'; i++; continue; }
    if (c === '`') { inCode = !inCode; cur += c; continue; }
    if (c === '|' && !inCode) { cells.push(cur.trim()); cur = ''; continue; }
    cur += c;
  }
  cells.push(cur.trim());
  return cells;
}

function alignOf(cell: string): ChatBlockAlign {
  const s = cell.trim();
  const l = s.startsWith(':');
  const r = s.endsWith(':');
  if (l && r) return 'center';
  if (r) return 'right';
  return 'left';
}

/** Metin koşusunun kenarındaki boş satırları atar (blok arası çift boşluk olmasın). */
function trimRun(lines: string[]): string {
  let a = 0;
  let b = lines.length;
  while (a < b && lines[a].trim() === '') a++;
  while (b > a && lines[b - 1].trim() === '') b--;
  return lines.slice(a, b).join('\n');
}

/**
 * parseChatBlocks — asistan metnini bloklara böler.
 *
 * `streaming` = tur hâlâ akıyor (turn.pending). AKIŞ KARARI, tek
 * cümleyle: son satır `\n` ile bitmediyse o satır YARIM'dır ve yalnız
 * "bitmemiş bir blok işareti" ise TUTULUR (bir sonraki delta'da tam
 * hâliyle gelir); değilse normal basılır.
 *
 * Neden bu ikili ayrım — iki başarısızlık kipi var ve ikisi de kötü:
 *   (a) her yarım satırı tutmak: düzyazı bir cevap dakikalarca TEK
 *       satır olabiliyor (model `\n` basmadan yazıyor). Tutarsak balon
 *       akış boyunca BOŞ durur — "AI takıldı" görünür. Kabul edilemez.
 *   (b) hiçbirini tutmamak: `| Servis | p9` satırı ham çubuklarla bir
 *       an görünür, sonra tabloya "sıçrar"; `` ``` `` çiti yalnız
 *       backtick olarak yanıp söner. Bugünkü kusurun ta kendisi.
 * Ayrım şu ölçüte oturuyor: karakterler ekranda BİLGİ mi taşıyor
 * (düzyazı → bas), yoksa henüz YAPI mı kuruyor (çubuk/çit/diyez →
 * tut)? Tutulan en fazla bir satırdır ve balonun imleci (.cm-ai-cursor)
 * zaten "yazıyor" sinyalini veriyor, yani duraklama gibi okunmuyor.
 *
 * `streaming: false` (akış bitti) hiçbir şeyi tutmaz: kapanmamış bir
 * fence bile `open: true` bir kod bloğu olarak çizilir. Kesilmiş bir
 * cevapta içeriği YUTMAK, ham basmaktan daha kötü (Markdown.tsx'in
 * "bilinmeyen işaret olduğu gibi geçer" ilkesi).
 */
// v0.10.1137 inceleme — iç içe blok derinliği tavanı (alıntı / details /
// liste / uyarı). Ötesi düz metin kalır: `'>'.repeat(3000)` ya da binlerce
// kademeli liste özyinelemeyi yığın taşmasına sürükleyemez.
export const MAX_BLOCK_DEPTH = 8;

// Üst düzey çözüm önbelleği (metin+akış → bloklar): akış sırasında yalnız
// son tur değişir; tamamlanmış turların yeniden çizimi çözücüyü koşturmaz.
const PARSE_CACHE = new Map<string, ChatBlock[]>();
const PARSE_CACHE_MAX = 64;

export function parseChatBlocks(text: string, streaming = false, depth: number | boolean = 0): ChatBlock[] {
  const d = depth === true ? 1 : depth === false ? 0 : depth;
  if (d >= MAX_BLOCK_DEPTH) {
    const t = trimRun((text ?? '').split('\n'));
    return t ? [{ kind: 'text', text: t }] : [];
  }
  if (d > 0) return parseBlocksInner(text, streaming, d);
  const key = (streaming ? '1' : '0') + text;
  const hit = PARSE_CACHE.get(key);
  if (hit) return hit;
  const res = parseTop(text, streaming);
  if (PARSE_CACHE.size >= PARSE_CACHE_MAX) PARSE_CACHE.delete(PARSE_CACHE.keys().next().value as string);
  PARSE_CACHE.set(key, res);
  return res;
}

function parseTop(text: string, streaming: boolean): ChatBlock[] {
  const bs = parseBlocksInner(text, streaming, 0);
  // v0.10.1137 — en üstteki "**Özet:** …" satırı özet kutusuna (TL;DR).
  const f = bs[0];
  if (f && f.kind === 'text') {
    const [line0, ...rest] = f.text.split('\n');
    const m = line0.match(SUMMARY_LINE);
    if (m) {
      const out: ChatBlock[] = [{ kind: 'callout', variant: 'summary', title: '', blocks: [{ kind: 'text', text: m[1].trim() }] }];
      const r = trimRun(rest);
      if (r) out.push({ kind: 'text', text: r });
      return [...out, ...bs.slice(1)];
    }
  }
  return bs;
}

function parseBlocksInner(text: string, streaming: boolean, depth: number): ChatBlock[] {
  const blocks: ChatBlock[] = [];
  if (!text) return blocks;
  const lines = text.split('\n');
  // Yarım satırın indeksi (yoksa -1). `\n` ile biten metinde son öğe
  // boş dizedir, yani yarım satır YOKTUR.
  const partial = streaming && !text.endsWith('\n') ? lines.length - 1 : -1;
  const full = (i: number) => i < lines.length && i !== partial;

  let run: string[] = [];
  const flushRun = () => {
    if (run.length === 0) return;
    const joined = trimRun(run);
    run = [];
    if (joined !== '') blocks.push({ kind: 'text', text: joined });
  };

  let i = 0;
  while (i < lines.length) {
    const line = lines[i];

    // ── Yarım satır: işaret kalıplarından biriyse TUT, değilse aşağıya düş.
    if (i === partial) {
      if (PARTIAL_FENCE.test(line) || PARTIAL_ROW.test(line) || PARTIAL_HEADING.test(line)) break;
    }

    // ── Fence (```lang … ```)
    if (FENCE.test(line)) {
      flushRun();
      // v0.10.1137 — bilgi dizesi: dil + isteğe bağlı dosya başlığı.
      const info = parseFenceInfo(line.trim().replace(/^`{3,}/, ''));
      const lang = info.lang;
      const code: string[] = [];
      let open = true;
      let j = i + 1;
      while (j < lines.length) {
        if (j === partial) {
          // Kapanış çiti yazılıyor olabilir → tut. Değilse yazılmakta
          // olan KOD satırıdır: kod içinde harf harf büyüme doğal, bas.
          if (!/^\s{0,3}`/.test(lines[j])) code.push(lines[j]);
          // j'yi SONA taşımak ŞART: yarım satır zaten son satırdır ve
          // `i = j` ile dış döngüye dönerse aynı satır İKİNCİ kez, bu
          // sefer düz metin olarak basılır (kod bloğunun altında ham
          // JSON/SQL olarak görünüyordu — bu satır olmadan test kızarır).
          j = lines.length;
          break;
        }
        if (FENCE.test(lines[j])) { open = false; j++; break; }
        code.push(lines[j]);
        j++;
      }
      // KAPANMAMIŞ blokta sondaki boş satırlar atılır: metin `\n` ile
      // bittiğinde split son öğe olarak boş dize verir ve `<pre>` onu
      // gerçek bir boş satır olarak çizer — kod bloğu her yeni satırda
      // bir satır boyu ZIPLAR. Kapanmış blokta gövde AYNEN korunuyor
      // (çitler arasındaki boşluk modelin/üreticinin kararı).
      while (open && code.length > 0 && code[code.length - 1].trim() === '') code.pop();
      const cb: Extract<ChatBlock, { kind: 'code' }> = { kind: 'code', lang, code: code.join('\n'), open };
      if (info.title) cb.title = info.title;
      blocks.push(cb);
      i = j;
      continue;
    }

    // ── Tablo: `|…` başlığı + hemen ardından TAM bir ayraç satırı.
    const head = isRowLine(line) ? splitRow(line) : null;
    if (head && full(i + 1) && isDelimRow(lines[i + 1], head.length)) {
      flushRun();
      const align = splitRow(lines[i + 1]).map(alignOf);
      const rows: string[][] = [];
      let j = i + 2;
      // Yarım satır burada da TUTULUYOR: yarım bir satırı satır olarak
      // basmak kolon genişliklerini her delta'da zıplatır.
      while (full(j) && isRowLine(lines[j])) {
        const cells = splitRow(lines[j]);
        // Kısa satır doldurulur; FAZLA hücre KORUNUR (içerik yutmayız —
        // tablo bir kolon geniş çizilir, o kolonun başlığı boş kalır).
        while (cells.length < head.length) cells.push('');
        rows.push(cells);
        j++;
      }
      blocks.push({ kind: 'table', head, align, rows });
      i = j;
      continue;
    }

    // ── v0.10.1137 — <details><summary>…</summary> … </details> (yalnız bu iki etiket).
    const dm = full(i) ? line.match(DETAILS_OPEN) : null;
    if (dm) {
      flushRun();
      let summary = dm[2] ?? '';
      let j = i + 1;
      if (dm[2] === undefined && j < lines.length && j !== partial) {
        const sm = lines[j].match(SUMMARY_TAG);
        if (sm) { summary = sm[1]; j++; }
      }
      const inner: string[] = [];
      let depth = 1;
      let closed = false;
      while (j < lines.length) {
        const l = lines[j];
        if (j !== partial && DETAILS_OPEN.test(l)) depth++;
        if (j !== partial && DETAILS_CLOSE.test(l)) {
          depth--;
          if (depth === 0) { closed = true; j++; break; }
        }
        inner.push(l);
        j++;
      }
      const toEnd = !closed;
      blocks.push({
        kind: 'details', summary: summary.trim() || 'Ayrıntılar', closed, open: !!dm[1],
        blocks: parseChatBlocks(inner.join('\n') + (toEnd && partial < 0 ? '\n' : ''), streaming && toEnd, depth + 1),
      });
      i = j;
      continue;
    }

    // ── Başlık (#, ##, ###). #### ve ötesi 3'e KIRPILIR: model derin
    // başlık üretiyor ve balonun içinde 4. kademe görsel olarak yok.
    const h = line.match(HEADING);
    if (h) {
      flushRun();
      const level = Math.min(h[1].length, 4) as 1 | 2 | 3 | 4;
      blocks.push({ kind: 'heading', level, text: h[2].trim() });
      i++;
      continue;
    }

    // ── v0.10.1137 — yatay çizgi (`---`, `***`, `___`). Tablo ayracından
    // SONRA bakılıyor: başlık satırının altındaki `|---|` ayraçtır.
    if (HR.test(line) && full(i)) {
      flushRun();
      blocks.push({ kind: 'hr' });
      i++;
      continue;
    }

    // ── v0.10.1137 — alıntı: ardışık `>` satırları, içerik yeniden çözülür.
    if (QUOTE.test(line) && !(i === partial && /^\s{0,3}>\s*$/.test(line))) {
      flushRun();
      const inner: string[] = [];
      let j = i;
      while (j < lines.length && QUOTE.test(lines[j])) {
        inner.push((lines[j].match(QUOTE) as RegExpMatchArray)[1]);
        j++;
      }
      const toEnd = j >= lines.length;
      const tail = toEnd && partial < 0 ? '\n' : '';
      // v0.10.1137 — `> [!NOTE] başlık` → uyarı kutusu (tanınmayan tür: alıntı).
      const ch = inner[0].trim().match(CALLOUT_HEAD);
      const variant = ch ? calloutVariant(ch[1]) : null;
      if (ch && variant) {
        blocks.push({ kind: 'callout', variant, title: ch[2].trim(), blocks: parseChatBlocks(inner.slice(1).join('\n') + tail, streaming && toEnd, depth + 1) });
      } else {
        blocks.push({ kind: 'quote', blocks: parseChatBlocks(inner.join('\n') + tail, streaming && toEnd, depth + 1) });
      }
      i = j;
      continue;
    }

    // ── Liste (- / * / + ve 1. / 1) ). Tür değişimi listeyi kapatır.
    // v0.10.1137 — İÇ İÇE: maddenin işaretinden daha girintili satırlar
    // (alt liste, kod çiti, devam düzyazısı) o maddenin `nested` içeriği
    // olur ve yeniden bloklara çözülür. Tek boş satır listeyi kesmez
    // (gevşek liste) — ardından aynı tür madde ya da girintili içerik
    // geliyorsa liste sürer.
    const b = line.match(BULLET);
    const o = b ? null : line.match(ORDERED);
    if (b || o) {
      flushRun();
      const ordered = !!o;
      const first = line.match(ANY_ITEM) as RegExpMatchArray;
      const base = first[1].length;
      const items: string[] = [first[3]];
      const nested: (ChatBlock[] | null)[] = [null];
      let sub: string[] = [];
      const closeSub = (toEnd: boolean) => {
        if (sub.length === 0) return;
        // Ortak girintiyi at (en az girintili boş olmayan satır kadar).
        const ind = Math.min(...sub.filter(l => l.trim() !== '').map(l => l.match(/^\s*/)![0].length));
        const txt = sub.map(l => l.slice(Math.min(ind, l.match(/^\s*/)![0].length))).join('\n');
        const bs = parseChatBlocks(txt + (toEnd && partial < 0 ? '\n' : ''), streaming && toEnd, depth + 1);
        if (bs.length) nested[nested.length - 1] = [...(nested[nested.length - 1] ?? []), ...bs];
        sub = [];
      };
      const sameKind = (m: RegExpMatchArray) => (/\d/.test(m[2]) === ordered);
      let j = i + 1;
      while (j < lines.length) {
        const l = lines[j];
        if (j === partial && (PARTIAL_FENCE.test(l) || PARTIAL_ROW.test(l) || PARTIAL_HEADING.test(l)) && l.match(/^\s*/)![0].length <= base) break;
        if (l.trim() === '') {
          // Boş satır: sonraki dolu satır listeye ait mi?
          let k = j + 1;
          while (k < lines.length && lines[k].trim() === '') k++;
          if (k >= lines.length) break;
          const nm = lines[k].match(ANY_ITEM);
          const nind = lines[k].match(/^\s*/)![0].length;
          if (nind >= base + 2) { sub.push(''); j++; continue; }
          if (nm && nm[1].length <= base + 1 && sameKind(nm)) { j = k; continue; }
          break;
        }
        const ind = l.match(/^\s*/)![0].length;
        if (ind >= base + 2) { sub.push(l); j++; continue; }
        const m = l.match(ANY_ITEM);
        if (!m || !sameKind(m) || HR.test(l)) break;
        closeSub(false);
        items.push(m[3]);
        nested.push(null);
        j++;
      }
      closeSub(j >= lines.length);
      // v0.10.1137 — GFM görev listesi: "[ ] iş" / "[x] iş" (salt-okunur kutu).
      const tasks = items.map(it => { const t = /^\[([ xX])\]\s+/.exec(it); return t ? t[1] !== ' ' : null; });
      const blk: Extract<ChatBlock, { kind: 'list' }> = {
        kind: 'list', ordered, items: items.map((it, k) => (tasks[k] === null ? it : it.replace(/^\[[ xX]\]\s+/, ''))),
      };
      if (tasks.some(t => t !== null)) blk.tasks = tasks;
      if (nested.some(n => n !== null)) blk.nested = nested;
      if (ordered) {
        const n = parseInt(first[2], 10);
        if (n !== 1 && Number.isFinite(n)) blk.start = n;
      }
      blocks.push(blk);
      i = j;
      continue;
    }

    run.push(line);
    i++;
  }
  flushRun();
  return blocks;
}

// ── Düz metin (kopyala) ─────────────────────────────────────────────
// v0.10.1137 — cevabı kopyala: markdown işaretleri düşer, linkler
// "etiket (adres)" olarak, tablolar sekmeyle ayrık satırlar, kod gövdesi
// aynen. Operatör yapıştırdığı yerde yıldız/çubuk görmesin.
/** Uyarı kutusu başlıkları (TR). */
export const CALLOUT_LABEL: Record<CalloutVariant, string> = {
  summary: 'Özet', note: 'Not', tip: 'İpucu', warning: 'Uyarı', important: 'Önemli', caution: 'Dikkat',
};

function plainBlocks(bs: readonly ChatBlock[], indent: string): string[] {
  const out: string[] = [];
  const il = (t: string) => inlinePlain(parseInline(t));
  for (const b of bs) {
    switch (b.kind) {
      case 'text': out.push(b.text.split('\n').map(l => indent + il(l)).join('\n')); break;
      case 'heading': out.push(indent + il(b.text)); break;
      case 'hr': out.push(indent + '—'); break;
      case 'code': out.push(b.code.split('\n').map(l => indent + l).join('\n')); break;
      case 'table':
        out.push([b.head, ...b.rows].map(r => indent + r.map(il).join('\t')).join('\n'));
        break;
      case 'quote': out.push(plainBlocks(b.blocks, indent + '> ').join('\n\n')); break;
      case 'callout': {
        const head = b.variant === 'summary' ? 'Özet' : CALLOUT_LABEL[b.variant];
        out.push(`${indent}${head}${b.title ? `: ${il(b.title)}` : ':'}\n${plainBlocks(b.blocks, indent).join('\n\n')}`);
        break;
      }
      case 'details': out.push(`${indent}${il(b.summary)}\n${plainBlocks(b.blocks, indent).join('\n\n')}`); break;
      case 'list': {
        const lines: string[] = [];
        b.items.forEach((it, k) => {
          const task = b.tasks?.[k];
          const mark = task != null ? (task ? '[x]' : '[ ]') : b.ordered ? `${(b.start ?? 1) + k}.` : '•';
          lines.push(`${indent}${mark} ${il(it)}`);
          const sub = b.nested?.[k];
          if (sub) lines.push(plainBlocks(sub, indent + '   ').join('\n'));
        });
        out.push(lines.join('\n'));
        break;
      }
    }
  }
  return out;
}

export function chatPlainText(md: string): string {
  return plainBlocks(parseChatBlocks(md ?? ''), '').join('\n\n').trim();
}
