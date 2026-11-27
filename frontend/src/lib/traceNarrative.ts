// traceNarrative — v0.10.2480 (operatör-bildirimli, prod v0.10.2478: "Trace
// açıklaması daha iyi olsun istiyorum ama çok da göz yormasın").
//
// Trace açıklamasının ANLATIM biçimi: model yalnız TÜR + KARAR + Sonraki adım
// yazar (Go copilot.SystemPromptTraceNarrative); hata zinciri / kritik yol /
// Diğer kanıt / yaygınlık satırlarını UI sunucunun hesapladığı `trace_evidence`
// bloğundan çizer (Go api/trace_explain_evidence.go). Bu modül saf: ayrıştırıcı
// + satır verisi (vitest node). TÜR kümesi Go copilot.TraceNarrativeLabels'ın aynası.
//
// v0.10.2483 (operatör onaylı): KARAR TEK açıklama, iki paragraf (ne oldu;
// loglar ne gösteriyor). Akış ve LOG satırları kalktı; loglar Loglar
// sekmesinde. KARAR'daki saat, sunucunun modele verdiği bir log satırının
// saatiyle (aynı biçim, izleyicinin dilimi) BİREBİR eşleşirse bağlantı olur.
import type { TraceEvidenceHop, TraceEvidencePrevalence, TraceEvidenceView } from './types';
import { formatTraceClock } from './traceAiContext';

/** Kapalı TÜR kümesi (Go copilot.TraceNarrativeLabels ile aynı sıra). */
export const TRACE_NARRATIVE_LABELS = [
  'Business hata', 'Bağımlılık hatası', 'Zaman aşımı', 'Yavaşlık',
  'Yetki hatası', 'Altyapı hatası', 'Başarılı', 'Belirsiz',
] as const;
export type TraceNarrativeLabel = typeof TRACE_NARRATIVE_LABELS[number];

/** Etiketin i18n anahtar parçası. */
export const TRACE_LABEL_KEY: Record<TraceNarrativeLabel, string> = {
  'Business hata': 'business', 'Bağımlılık hatası': 'dependency', 'Zaman aşımı': 'timeout', 'Yavaşlık': 'slow',
  'Yetki hatası': 'auth', 'Altyapı hatası': 'infra', 'Başarılı': 'ok', 'Belirsiz': 'unclear',
};

export interface TraceNarrative {
  /** Kapalı kümeden etiket; tanınmazsa null (UI etiket çizmez). */
  label: TraceNarrativeLabel | null;
  /** KARAR; v0.10.2483 — 1–2 paragraf, aralarında "\n\n". */
  verdict: string;
  /** KARAR paragrafları (0–2; tek paragraf geçerli, ikinci uydurulmaz). */
  paras: string[];
  /** v0.10.2482 — "Kod bulgusu" (yalnız "Kodu da incele" turu); yoksa ''. */
  code: string;
  next: string;
}

const lower = (s: string) => s.toLocaleLowerCase('tr');

/** Markdown süsünü at: kalın/italik işaretleri, baştaki başlık/madde/alıntı işaretleri. */
function plain(line: string): string {
  return line.replace(/\*\*|__/g, '').replace(/^\s*(?:#{1,6}\s+|[-*•>]\s+)/, '').trim();
}

/** Satır tek başına bir TÜR etiketi mi ("TÜR: …", parantez, son nokta toleranslı). */
export function matchTraceLabel(line: string): TraceNarrativeLabel | null {
  let s = plain(line).replace(/^(?:tür|tur|type|etiket)\s*:\s*/i, '').replace(/[.:]+$/, '').trim();
  const bare = s.replace(/\s*\([^)]*\)\s*$/, '').trim();
  for (const cand of [s, bare]) {
    const l = lower(cand);
    for (const lab of TRACE_NARRATIVE_LABELS) if (lower(lab) === l) return lab;
  }
  s = lower(s);
  // "İş kuralı reddi" — v0.10.2480 taslağının adı; model eski örnekten alırsa.
  if (s === 'iş kuralı reddi') return 'Business hata';
  return null;
}

const WAIT_LINE = /^\s*[*_#>\s]*dış bağımlılık bekleme:/i;
const KARAR_RE = /(^|\n)[ \t]*(?:[*_#>-]+[ \t]*)*karar[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?/i;
const CODE_RE = /(^|\n)[ \t]*(?:[*_#>-]+[ \t]*)*kod[ \t]+bulgusu[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?/i;
const NEXT_RE =/(^|\n)[ \t]*(?:[*_#>-]+[ \t]*)*(?:önerilen[ \t]+)?sonraki[ \t]+adım[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?/i;

const flat = (s: string) => s.split('\n').map(plain).filter(Boolean).join(' ').replace(/\s+/g, ' ').trim();

/** KARAR gövdesi → en çok iki paragraf (Go tnParas aynası; fazlası ikinciye katılır). */
function paragraphs(s: string): string[] {
  const ps = s.split(/\n[ \t]*\n/).map(flat).filter(Boolean);
  return ps.length > 2 ? [ps[0], ps.slice(1).join(' ')] : ps;
}

/**
 * parseTraceNarrative — modelin çıktısı → {etiket, KARAR, Sonraki adım}.
 * Sunucunun mekanik "dış bağımlılık bekleme: …" satırı (Kanıt'ın kritik yolu
 * gösterir) atılır. KARAR işareti yoksa etiket sonrası, Sonraki adım öncesi
 * metin KARAR sayılır. `streaming`: ilk satır henüz bitmediyse KARAR boş
 * (yarım etiket metin gibi titremesin).
 */
export function parseTraceNarrative(text: string, streaming = false): TraceNarrative {
  const lines = (text ?? '').replace(/\r/g, '').split('\n').filter(l => !WAIT_LINE.test(l));
  let i = 0;
  while (i < lines.length && !lines[i].trim()) i++;
  if (streaming && i === lines.length - 1 && !KARAR_RE.test(lines[i])) return { label: null, verdict: '', paras: [], code: '', next: '' };
  let label: TraceNarrativeLabel | null = null;
  if (i < lines.length) {
    label = matchTraceLabel(lines[i]);
    if (label) i++;
  }
  const rest = lines.slice(i).join('\n');
  const n = NEXT_RE.exec(rest);
  let head = n ? rest.slice(0, n.index) : rest;
  const next = n ? flat(rest.slice(n.index + n[0].length)) : '';
  // v0.10.2482 — kod dalı: "Kod bulgusu" KARAR ile Sonraki adım arasında.
  const c = CODE_RE.exec(head);
  const code = c ? flat(head.slice(c.index + c[0].length)) : '';
  if (c) head = head.slice(0, c.index);
  const k = KARAR_RE.exec(head);
  const paras = paragraphs(k ? head.slice(k.index + k[0].length) : head);
  return { label, verdict: paras.join('\n\n'), paras, code, next };
}

/** "6.6 ms" / "50 ms" / "2.30 s" — Go tracechain.FmtMs aynası. */
export function fmtTraceMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms < 10) return `${ms.toFixed(1)} ms`;
  return `${Math.round(ms)} ms`;
}

/** Başlık satırının HTTP kodu: kökün kodu, yoksa kaynak halkanın. */
export function traceHeadStatus(v: TraceEvidenceView | null | undefined): number | undefined {
  const ch = v?.chain ?? [];
  if (!ch.length) return undefined;
  return ch[ch.length - 1].status || ch[0].status || undefined;
}

// ── KARAR'daki log saatleri ──

const CLOCK_RE = /\b\d{2}:\d{2}:\d{2}\.\d{3}\b/g;

/**
 * traceLogClockSet — modele giden log desenlerinin saatleri (ilk ve ×N ise
 * son), UI'ın saat kuralıyla (formatTraceClock: izleyicinin dilimi). Sunucu
 * modele AYNI dilim ve biçimde verdi; eşleşme tahmin değil, birebir. `tz` test dikişi.
 */
export function traceLogClockSet(v: TraceEvidenceView | null | undefined, tz?: string): Set<string> {
  const out = new Set<string>();
  for (const l of v?.logIndex ?? []) {
    for (const ns of [l.tsNs, l.lastTsNs]) {
      const c = ns ? formatTraceClock(ns, tz) : null;
      if (c) out.add(c.time);
    }
  }
  return out;
}

export interface ClockSeg { text: string; clock?: boolean }

/** Metni saat parçalarına böler: yalnız kümede BİREBİR olan saat `clock`; en yakına yuvarlama yok. */
export function splitLogClocks(text: string, clocks: Set<string>): ClockSeg[] {
  if (!clocks.size) return [{ text }];
  const out: ClockSeg[] = [];
  let last = 0;
  for (const m of text.matchAll(CLOCK_RE)) {
    if (!clocks.has(m[0])) continue;
    const at = m.index ?? 0;
    if (at > last) out.push({ text: text.slice(last, at) });
    out.push({ text: m[0], clock: true });
    last = at + m[0].length;
  }
  if (last < text.length) out.push({ text: text.slice(last) });
  return out.length ? out : [{ text }];
}

// ── KARAR altındaki log notu ──

export type TraceLogsNote =
  | { kind: 'all'; total: number }
  | { kind: 'some'; total: number; sent: number }
  | { kind: 'none' | 'failed' | 'nostore' };

/** "N log satırının hepsi incelendi" / "N satırın M'si" / yok / okunamadı / depo yok. */
export function traceLogsNote(v: TraceEvidenceView): TraceLogsNote | null {
  const r = v.logsReviewed;
  if (r && r.total > 0) return r.sent >= r.total ? { kind: 'all', total: r.total } : { kind: 'some', total: r.total, sent: r.sent };
  if (v.logState === 'failed') return { kind: v.logReason === 'nostore' ? 'nostore' : 'failed' };
  if (v.logState === 'none' || (v.logState === 'rows' && !v.logRows)) return { kind: 'none' };
  return null;
}

// ── Hata zinciri notları ──

export interface TraceHopNote {
  /** hop 1: exception tipi + mesajı (sunucu verisi, aynen) */
  source?: string;
  /** bilindiğinde 24 sa ortancası ve bu çağrının katı */
  p50Ms?: number;
  ratio?: number;
  /** son halka (kök istek) */
  root?: boolean;
}

/**
 * traceHopNote — zincir halkasının tek satırlık notu (sunucu verisinden):
 * kaynak (hop 1) exception tipi + mesajı; 24 sa ortancası biliniyorsa "×Y";
 * son halka "kök istek". Not yoksa null.
 */
export function traceHopNote(h: TraceEvidenceHop, i: number, n: number): TraceHopNote | null {
  const note: TraceHopNote = {};
  if (i === 0 && h.error) note.source = h.error;
  if (h.p50Ms && h.p50Ms > 0) {
    note.p50Ms = h.p50Ms;
    note.ratio = h.durMs / h.p50Ms;
  }
  if (n > 1 && i === n - 1) note.root = true;
  return note.source || note.p50Ms || note.root ? note : null;
}

// ── Yaygınlık satırı ──

/** "Yaygın" sunucuda karar verilir (Go tracePrevalenceWideMin); UI yalnız renklendirir. */
export type TracePrevalenceKey = 'errOne' | 'errMany' | 'opOver' | 'opPlain';

export function tracePrevalenceKey(p: TraceEvidencePrevalence): TracePrevalenceKey | null {
  if (p.kind === 'error') {
    if (!p.errors) return null; // yanlış sıfır gösterilmez
    return p.errors <= 1 ? 'errOne' : 'errMany';
  }
  if (!p.calls) return null;
  return p.overMs ? 'opOver' : 'opPlain';
}

// ── Diğer kanıt ──

export interface TraceOtherEvidence {
  stack: boolean;
  fields: number;
  /** kritik yol yalnız zincir gösterildiyse burada (zincir yoksa açık "Kritik yol") */
  critical: boolean;
  oracle: number;
  others: number;
}

export function traceOtherEvidence(v: TraceEvidenceView): TraceOtherEvidence {
  return {
    stack: !!v.frame,
    fields: v.fields?.length ?? 0,
    critical: !!v.critical && !!v.chain?.length,
    oracle: v.oracle?.length ?? 0,
    others: v.others?.length ?? 0,
  };
}

export function hasOtherEvidence(o: TraceOtherEvidence, codeFiles = 0): boolean {
  return o.stack || o.fields > 0 || o.critical || o.oracle > 0 || o.others > 0 || codeFiles > 0;
}
