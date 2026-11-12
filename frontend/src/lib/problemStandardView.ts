// problemStandardView — v0.10.1529 (Problem standardı R2). ProblemStandard'ı
// (Go chstore.ProblemStandard) ekran metnine çeviren SAF işlevler: Inbox
// satırı (StdRowCells) ve detay başlığı (ProblemStandardHeader) AYNI
// metni buradan okur — satır "YÜKSEK" derken başlık "orta" demesin.
//
// Metin tamamen Türkçe (D9). Sunucu cümleleri (manşet, etki, kanıt, sonraki
// adım) olduğu gibi geçer; burada yalnız zaman, güven ve kaynak etiketleri
// kurulur. Hiçbir işlev ölçülmemiş bir şeyi iddia etmez: çağıran yoksa
// "çağıran" yazılmaz, dakikalık hata ölçülmediyse ikinci satır boş kalır.
import type { ProblemStandard, SignalGate, StdConfidenceLevel } from './types';
import { isSafeAppPath } from './safeAppPath';

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
export function stdSourceName(source: string): string {
  return SOURCE_LABEL[source] ?? (source || 'bilinmiyor');
}

/** Satırın kaynak çipi: "VM · örneklenmemiş" / "span · örneklenmiş" / "exception grubu". */
export function stdSourceChip(std: ProblemStandard): string {
  const src = std.evidence.source;
  if (src === 'vm') return 'VM · örneklenmemiş';
  if (src === 'span') return 'span · örneklenmiş';
  return stdSourceName(src);
}

/** Kaynağın örnekleme notu (başlık kartının ikinci satırı). */
export function stdSamplingNote(std: ProblemStandard): string {
  return std.evidence.sampled ? 'örneklenmiş' : 'örneklenmemiş';
}

/** Süre: "45 sn" · "35 dk" · "5 sa 40 dk" · "2 g 3 sa". */
export function fmtStdDuration(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  if (s < 60) return `${s} sn`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} dk`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h} sa ${m % 60} dk` : `${h} sa`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d} g ${h % 24} sa` : `${d} g`;
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
export function stdSinceLine(std: ProblemStandard, nowMs: number = Date.now()): string {
  if (!std.since.startedAt) return '—';
  const c = fmtStdClock(std.since.startedAt, nowMs);
  return `${c}${sinceSuffix(c)} beri`;
}

/** "5 sa 40 dk · sürüyor" / "35 dk · bitti" (+ "· 3. kez"). */
export function stdDurationLine(std: ProblemStandard): string {
  const parts = [fmtStdDuration(std.since.durationSec), std.since.ongoing ? 'sürüyor' : 'bitti'];
  if ((std.since.episode ?? 0) > 1) parts.push(`${std.since.episode}. kez`);
  return parts.join(' · ');
}

/** Etkinin ikinci satırı: ölçülmüş dakikalık hata + doğrulanmış çağıranlar. Boş = çizme. */
export function stdImpactSecondary(std: ProblemStandard): string {
  const parts: string[] = [];
  const fpm = std.impact.failedPerMin ?? 0;
  if (fpm > 0) parts.push(`~${fmtCount(fpm)} hatalı istek/dk`);
  const n = std.impact.callers?.length ?? 0;
  if (n > 0) parts.push(`${n} çağıran (doğrulanmış)`);
  if (!std.impact.known && parts.length === 0) parts.push('etki bilinmiyor');
  return parts.join(' · ');
}

function fmtCount(v: number): string {
  if (v >= 100) return Math.round(v).toLocaleString('tr-TR');
  if (v >= 10) return String(Math.round(v));
  return v.toFixed(1).replace('.', ',');
}

const GATE_MARK: Record<string, string> = { pass: '✓', fail: '✗', unknown: '?', flag: '!', context: '·' };

/** Kapı özeti: "trafik ✓ sürdürme ✓ VM karar ? takvim !"; na kapılar yazılmaz. */
export function stdGateSummary(gates: SignalGate[]): string {
  return gates
    .filter(g => g.status !== 'na' && g.status !== 'context')
    .map(g => `${g.name} ${GATE_MARK[g.status] ?? '?'}`)
    .join(' ');
}

/** Güven rozetinin title'ı: kapı başına "G1 trafik: ✓ — not" satırları. */
export function stdGateTitle(std: ProblemStandard): string {
  const lines = std.confidence.gates
    .filter(g => g.status !== 'na')
    .map(g => `${g.id} ${g.name}: ${GATE_MARK[g.status] ?? g.status}${g.note ? ` — ${g.note}` : ''}`);
  return [`Güven: ${STD_CONFIDENCE_LABEL[std.confidence.level]} (yalnız ölçülmüş kapılardan)`, ...lines].join('\n');
}

/** Manşeti özne + gövde olarak böler ("svc: X" → ["svc", "X"]); önek yoksa özne boş. */
export function splitStdHeadline(std: ProblemStandard): { subject: string; rest: string } {
  const p = std.subject ? `${std.subject}: ` : '';
  if (p && std.headline.startsWith(p)) return { subject: std.subject, rest: std.headline.slice(p.length) };
  return { subject: '', rest: std.headline };
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
