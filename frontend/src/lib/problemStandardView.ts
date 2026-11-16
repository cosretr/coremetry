// problemStandardView — v0.10.1529 (Problem standardı R2). ProblemStandard'ı
// (Go chstore.ProblemStandard) ekran metnine çeviren SAF işlevler: Inbox
// satırı (StdRowCells) ve detay başlığı (ProblemStandardHeader) AYNI
// metni buradan okur — satır "YÜKSEK" derken başlık "orta" demesin.
//
// Metin tamamen Türkçe (D9). Sunucu cümleleri (manşet, etki, kanıt, sonraki
// adım) olduğu gibi geçer; burada yalnız zaman, güven ve kaynak etiketleri
// kurulur. Hiçbir işlev ölçülmemiş bir şeyi iddia etmez: çağıran yoksa
// "çağıran" yazılmaz, dakikalık hata ölçülmediyse ikinci satır boş kalır.
import type { ProblemStandard, SignalGate, StdConfidenceLevel, StdMsg } from './types';
import { isSafeAppPath } from './safeAppPath';
import { t, tf, type Lang } from './i18n'; // v0.10.2283 — metinler arayüz dilinde (varsayılan 'tr')

const two = (n: number) => String(n).padStart(2, '0');

/** Güven etiketi (rozet metni). */
export const STD_CONFIDENCE_LABEL: Record<StdConfidenceLevel, string> = {
  high: 'YÜKSEK', medium: 'ORTA', low: 'DÜŞÜK',
};

/** Güven rozet tonu (Badge tone) — mockup: yüksek yeşil, orta sarı, düşük nötr. */
export function stdConfidenceTone(level: StdConfidenceLevel): 'success' | 'warning' | 'neutral' {
  return level === 'high' ? 'success' : level === 'medium' ? 'warning' : 'neutral';
}

/** Güven sıralama değeri (sunucu inboxConfidenceRank ile aynı; std yok = orta). */
export function stdConfidenceRank(std: ProblemStandard | undefined): number {
  const l = std?.confidence.level ?? 'medium';
  return l === 'high' ? 3 : l === 'medium' ? 2 : 1;
}

const SOURCE_LABEL: Record<string, string> = {
  vm: 'VictoriaMetrics', span: 'span', db_mv: 'DB özeti', probe: 'sentetik kontrol',
  exception: 'exception grubu', oracle: 'Oracle', self: 'Coremetry', external: 'dış kaynak', rule: 'kural',
  logs: 'log kalıpları', // v0.10.2038 — log anomalisi (span değil)
};

/** Kaynak adı ("VictoriaMetrics", "span" …). Bilinmeyen kaynak kendi adıyla. */
export function stdSourceName(source: string, lang: Lang = 'tr'): string {
  if (lang !== 'tr') return SOURCE_LABEL[source] ? t(`std.src.${source}`, lang) : (source || t('std.src.unknown', lang));
  return SOURCE_LABEL[source] ?? (source || 'bilinmiyor');
}

/** Satırın kaynak çipi: "VM · örneklenmemiş" / "span · örneklenmiş" / "exception grubu". */
export function stdSourceChip(std: ProblemStandard, lang: Lang = 'tr'): string {
  const src = std.evidence.source;
  if (src === 'vm') return t('std.chip.vm', lang);
  if (src === 'span') return t('std.chip.span', lang);
  return stdSourceName(src, lang);
}

/** Kaynağın örnekleme notu (başlık kartının ikinci satırı). */
export function stdSamplingNote(std: ProblemStandard, lang: Lang = 'tr'): string {
  return t(std.evidence.sampled ? 'std.sampled' : 'std.unsampled', lang);
}

/** Süre: "45 sn" · "35 dk" · "5 sa 40 dk" · "2 g 3 sa". */
export function fmtStdDuration(sec: number, lang: Lang = 'tr'): string {
  const s = Math.max(0, Math.floor(sec));
  if (s < 60) return tf('std.dur.s', lang, { s });
  const m = Math.floor(s / 60);
  if (m < 60) return tf('std.dur.m', lang, { m });
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? tf('std.dur.hm', lang, { h, m: m % 60 }) : tf('std.dur.h', lang, { h });
  const d = Math.floor(h / 24);
  return h % 24 ? tf('std.dur.dh', lang, { d, h: h % 24 }) : tf('std.dur.d', lang, { d });
}

/** Başlangıç saati: bugünse "06:05", değilse "24.10 06:05". */
export function fmtStdClock(ns: number, nowMs: number = Date.now()): string {
  if (!ns) return '—';
  const d = new Date(ns / 1e6);
  const now = new Date(nowMs);
  const hm = `${two(d.getHours())}:${two(d.getMinutes())}`;
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  return sameDay ? hm : `${two(d.getDate())}.${two(d.getMonth() + 1)} ${hm}`;
}

// Sayının OKUNUŞUNUN son kelimesine göre ayrılma eki (bir'den, üç'ten,
// altı'dan, kırk'tan …). Birler basamağı sıfırsa onlar kelimesi okunur.
const ONES_SUFFIX = ['', "'den", "'den", "'ten", "'ten", "'ten", "'dan", "'den", "'den", "'dan"];
const TENS_SUFFIX = ['', "'dan", "'den", "'dan", "'tan", "'den"];
function numSuffix(n: number): string {
  if (n % 10) return ONES_SUFFIX[n % 10];
  if (n === 0) return "'dan"; // sıfır
  return TENS_SUFFIX[Math.floor(n / 10)] ?? "'den";
}

/** Türkçe "-'ten/-'den beri" eki: "06:05" beş'ten, "06:30" otuz'dan,
 *  "06:00" saat adıyla (altı'dan). */
export function sinceSuffix(clock: string): string {
  const m = /(\d{2}):(\d{2})$/.exec(clock.trim());
  if (!m) return "'den";
  const hh = Number(m[1]);
  const mm = Number(m[2]);
  return mm > 0 ? numSuffix(mm) : numSuffix(hh);
}

/** "06:05'ten beri" — satırın birinci zaman satırı. */
export function stdSinceLine(std: ProblemStandard, nowMs: number = Date.now(), lang: Lang = 'tr'): string {
  if (!std.since.startedAt) return '—';
  const c = fmtStdClock(std.since.startedAt, nowMs);
  return tf('std.since', lang, { c: lang === 'tr' ? `${c}${sinceSuffix(c)}` : c });
}

/** "5 sa 40 dk · sürüyor" / "35 dk · bitti" (+ "· 3. kez"). */
export function stdDurationLine(std: ProblemStandard, lang: Lang = 'tr'): string {
  const parts = [fmtStdDuration(std.since.durationSec, lang), t(std.since.ongoing ? 'std.ongoing' : 'std.ended', lang)];
  if ((std.since.episode ?? 0) > 1) parts.push(tf('std.episode', lang, { n: std.since.episode ?? 0 }));
  return parts.join(' · ');
}

/** Etkinin ikinci satırı: ölçülmüş dakikalık hata + doğrulanmış çağıranlar. Boş = çizme. */
export function stdImpactSecondary(std: ProblemStandard, lang: Lang = 'tr'): string {
  const parts: string[] = [];
  const fpm = std.impact.failedPerMin ?? 0;
  if (fpm > 0) parts.push(tf('pd.std.failed', lang, { n: lang === 'tr' ? fmtCount(fpm) : fmtCountEn(fpm) }));
  const n = std.impact.callers?.length ?? 0;
  if (n > 0) parts.push(tf('pd.std.callers', lang, { n }));
  if (!std.impact.known && parts.length === 0) parts.push(t('pd.std.impactUnknown', lang));
  return parts.join(' · ');
}

function fmtCountEn(v: number): string {
  if (v >= 100) return Math.round(v).toLocaleString('en-US');
  if (v >= 10) return String(Math.round(v));
  return v.toFixed(1);
}

function fmtCount(v: number): string {
  if (v >= 100) return Math.round(v).toLocaleString('tr-TR');
  if (v >= 10) return String(Math.round(v));
  return v.toFixed(1).replace('.', ',');
}

const GATE_MARK: Record<string, string> = { pass: '✓', fail: '✗', unknown: '?', flag: '!', context: '·' };

// v0.10.2298 (sunucu metinleri S1) — kapı adı `id`'den (Go signal_gates.go
// G1–G7). TR = sunucu `name` aynen; EN = katalog; bilinmeyen id → `name`.
export const STD_GATE_IDS = ['G1', 'G2', 'G3', 'G4', 'G5', 'G6', 'G7'] as const;

/** Kapı adı arayüz dilinde (TR sunucu metni aynen). */
export function stdGateName(g: Pick<SignalGate, 'id' | 'name'>, lang: Lang = 'tr'): string {
  return lang !== 'tr' && (STD_GATE_IDS as readonly string[]).includes(g.id) ? t(`std.gate.${g.id}`, lang) : g.name;
}

/** Kapı özeti: "trafik ✓ sürdürme ✓ VM karar ? takvim !"; na kapılar yazılmaz. */
export function stdGateSummary(gates: SignalGate[], lang: Lang = 'tr'): string {
  return gates
    .filter(g => g.status !== 'na' && g.status !== 'context')
    .map(g => `${stdGateName(g, lang)} ${GATE_MARK[g.status] ?? '?'}`)
    .join(' ');
}

/** Güven rozetinin title'ı: kapı başına "G1 trafik: ✓ — not" satırları. */
/** v0.10.2284 — güven rozet metni arayüz dilinde (TR = STD_CONFIDENCE_LABEL). */
export function stdConfidenceLabel(level: StdConfidenceLevel, lang: Lang = 'tr'): string {
  return lang === 'tr' ? STD_CONFIDENCE_LABEL[level] : t(`pd.std.conf.${level}`, lang);
}

export function stdGateTitle(std: ProblemStandard, lang: Lang = 'tr'): string {
  const lines = std.confidence.gates
    .filter(g => g.status !== 'na')
    .map(g => `${g.id} ${stdGateName(g, lang)}: ${GATE_MARK[g.status] ?? g.status}${g.note ? ` — ${stdGateNote(g, lang)}` : ''}`);
  return [tf('pd.std.confTitle', lang, { l: stdConfidenceLabel(std.confidence.level, lang) }), ...lines].join('\n');
}

// ── v0.10.2299 (sunucu metinleri S2) — metin anahtarından EN çizim ──────────
// Sunucu her standart cümlesinin YANINA anahtar + ham argüman koyar (Go
// chstore std_msg.go). TR arayüz sunucu metnini AYNEN çizer (bayt bayt);
// EN arayüz anahtarı std.m.* kataloğundan çizer, çizemezse (anahtar yok /
// bilinmiyor / argüman eksik) sunucu metnine düşer ve `orig` işaretlenir
// (çağıran OrigLang ile sarar).

const STD_HEADLINE_MAX = 90; // Go StdHeadlineMax

// Go trNum ile aynı hassasiyet ve yuvarlama (strconv: tam eşitlikte çifte;
// toFixed eşitlikte yukarı yuvarlar — %91,25 TR "91,2", EN "91.2" olsun).
function enNum(v: number): string {
  const a = Math.abs(v);
  const prec = a >= 100 ? 0 : a >= 10 ? 1 : 2;
  const sc = v * 10 ** prec;
  const tie = Math.abs(sc - Math.trunc(sc)) === 0.5;
  const s = tie ? (2 * Math.round(sc / 2) / 10 ** prec).toFixed(prec) : v.toFixed(prec);
  return s.includes('.') ? s.replace(/0+$/, '').replace(/\.$/, '') : s;
}

function stdArg(name: string, v: string | number | boolean, lang: Lang): string | null {
  if (name.endsWith('K')) {
    const k = `std.m.${String(v)}`;
    const r = t(k, lang);
    return r === k ? null : r;
  }
  if (typeof v !== 'number') {
    if (v === 'inf' && name.endsWith('X')) return t('std.m.fullLoss', lang);
    return String(v);
  }
  if (name.endsWith('Pct')) return `${enNum(v)}%`;
  if (name.endsWith('Ms')) return v >= 1000 ? `${enNum(v / 1000)} s` : `${Math.round(v)} ms`;
  if (name.endsWith('X')) return `${enNum(v)}×`;
  return enNum(v);
}

/** Anahtardan EN metin; çizilemezse null. TR'de çağrılmaz (sunucu metni aynen). */
export function stdMsgText(msg: StdMsg | undefined | null, lang: Lang = 'en'): string | null {
  if (!msg?.k) return null;
  const args = msg.a ?? {};
  const base = `std.m.${msg.k}`;
  const one = args.cNum === 1 && t(`${base}_one`, lang) !== `${base}_one` ? `${base}_one` : base;
  const tpl = t(one, lang);
  if (tpl === one) return null;
  let ok = true;
  const out = tpl.replace(/\{(\w+)\}/g, (_m, ph: string) => {
    const hit = ph in args ? ph : `${ph}K` in args ? `${ph}K` : '';
    const r = hit ? stdArg(hit, args[hit], lang) : null;
    if (r == null) { ok = false; return ''; }
    return r;
  });
  return ok ? out : null;
}

/** Görünüm: TR = sunucu metni; EN = anahtardan, yoksa sunucu metni + orig. */
export function stdMsgView(msg: StdMsg | undefined | null, fallback: string, lang: Lang = 'tr'): { text: string; orig: boolean } {
  if (lang === 'tr') return { text: fallback, orig: false };
  const r = stdMsgText(msg, lang);
  return r != null ? { text: r, orig: false } : { text: fallback, orig: !!fallback };
}

function clipStd(s: string): string {
  const r = [...s];
  return r.length <= STD_HEADLINE_MAX ? s : `${r.slice(0, STD_HEADLINE_MAX - 1).join('').trim()}…`;
}

/** Manşet (EN'de Go ile aynı tavan). */
export function stdHeadlineView(std: ProblemStandard, lang: Lang = 'tr'): { text: string; orig: boolean } {
  const v = stdMsgView(std.headlineMsg, std.headline, lang);
  return v.orig || lang === 'tr' ? v : { text: clipStd(v.text), orig: false };
}

export const stdImpactView = (std: ProblemStandard, lang: Lang = 'tr') => stdMsgView(std.impact.primary.msg, std.impact.primary.text, lang);
export const stdEvidenceView = (std: ProblemStandard, lang: Lang = 'tr') => stdMsgView(std.evidence.msg, std.evidence.sentence, lang);
export const stdNextView = (std: ProblemStandard, lang: Lang = 'tr') => stdMsgView(std.nextStep.msg, std.nextStep.text, lang);

/** Bağlam satırları (" · " ile): anahtar dizisi eşleşmezse sunucu metni. */
export function stdContextView(std: ProblemStandard, lang: Lang = 'tr'): { text: string; orig: boolean } {
  const ctx = std.context ?? [];
  const msgs = std.contextMsgs ?? [];
  if (lang === 'tr' || msgs.length !== ctx.length) return stdMsgView(null, ctx.join(' · '), lang);
  const parts = ctx.map((c, i) => stdMsgView(msgs[i], c, lang));
  return { text: parts.map(p => p.text).join(' · '), orig: parts.some(p => p.orig) };
}

/** Kapı notu arayüz dilinde (TR sunucu notu aynen). */
export function stdGateNote(g: SignalGate, lang: Lang = 'tr'): string {
  return g.note ? stdMsgView(g.noteMsg, g.note, lang).text : '';
}

/** Manşeti özne + gövde olarak böler ("svc: X" → ["svc", "X"]); önek yoksa özne boş. */
export function splitStdHeadline(std: ProblemStandard, lang: Lang = 'tr'): { subject: string; rest: string } {
  const h = stdHeadlineView(std, lang).text; // v0.10.2299 — EN'de anahtardan (özne öneki aynı "svc: "); orig → stdHeadlineView
  const p = std.subject ? `${std.subject}: ` : '';
  if (p && h.startsWith(p)) return { subject: std.subject, rest: h.slice(p.length) };
  return { subject: '', rest: h };
}

// Detay sayfasının KENDİSİNE dönen sonraki-adım adresleri (exception grubu →
// /exceptions, anomali → kendi ?anomaly=, kural → kendi ?problem=): başlıkta
// link olarak çizilmez, cümle düz metin kalır. Dış/mutlak adres de çizilmez.
const SELF_NEXT = [/^\/exceptions(\?|$)/, /^\/inbox\?anomaly=/, /^\/problems\?problem=/];

/** Başlıkta link olarak çizilecek sonraki-adım adresi; yoksa undefined. */
export function stdNextHref(std: ProblemStandard): string | undefined {
  const h = std.nextStep.href;
  if (!h || !isSafeAppPath(h)) return undefined;
  return SELF_NEXT.some(re => re.test(h)) ? undefined : h;
}
