// logDetail — v0.10.1878 (Log Search Faz 3 dilim 3.1; audit §6 "Satır detayı",
// mockup görünüm 2). Satır detayı çekmecesinin SAF çekirdeği: düz cümle,
// seviye → durum, alan listesi (süz), teknik kanıt ayrımı. logDetail.test.ts.
//
// Düz cümle YALNIZ satırdaki veriden kurulur (ek istek yok, uydurma yok):
// kim (servis · pod), ne zaman, hangi seviyede, ne dedi (ilk satır, ≤160
// karakter), trace'e bağlı mı. Mockup'taki "son 5 dk'da 14 kez" gibi sayımlar
// ayrı istek ister — burada yok (dürüst eksik).
import type { LogRow } from '@/lib/types';
import type { Status } from '@/lib/statusTone';
import { podOfLog } from '@/lib/logPod';

export const DETAIL_SENTENCE_MAX = 160;

export function severityStatus(sev: number): Status {
  if (sev >= 17) return 'critical';
  if (sev >= 13) return 'warn';
  if (sev >= 9) return 'info';
  return 'unknown';
}

export function severityLabel(l: Pick<LogRow, 'severity' | 'severityText'>): string {
  const t = (l.severityText ?? '').trim();
  if (t) return t.toUpperCase();
  if (l.severity >= 21) return 'FATAL';
  if (l.severity >= 17) return 'ERROR';
  if (l.severity >= 13) return 'WARN';
  if (l.severity >= 9) return 'INFO';
  if (l.severity >= 5) return 'DEBUG';
  return l.severity > 0 ? 'TRACE' : 'OTHER';
}

/** Gövdenin ilk boş olmayan satırı, kırpılmış. */
export function firstLine(body: string, max = DETAIL_SENTENCE_MAX): string {
  const line = (body ?? '').split(/\r?\n/).map(s => s.trim()).find(s => s !== '') ?? '';
  return line.length > max ? line.slice(0, max - 1) + '…' : line;
}

/** Düz cümle — mockup "önce düz cümle" kuralı. clock: HH:MM:SS.mmm metni. */
export function plainLogSentence(l: LogRow, clock: string): string {
  const svc = (l.serviceName ?? '').trim() || 'Bilinmeyen servis';
  const pod = podOfLog(l);
  const who = pod ? `${svc} (pod ${pod})` : svc;
  const msg = firstLine(l.body);
  const said = msg ? `: “${msg}”` : '.';
  const trace = l.traceId ? ' Kayıt bir trace\'e bağlı.' : '';
  return `${who} ${clock}'de ${severityLabel(l)} seviyesinde bir kayıt yazdı${said}${trace}`;
}

export interface DetailField {
  key: string;
  value: string;
  /** Kimlik / kod değeri mi (mono). */
  mono: boolean;
  /** Süzgeç / kolon eylemleri anlamlı mı (zaman değil). */
  actionable: boolean;
}

/**
 * "Alanlar" bölümü: bilinen çekirdek (servis, seviye, trace, span) önce, sonra
 * öznitelikler (anahtar sırası). Kaynak öznitelikleri "Teknik kanıt"a gider
 * (technicalFields). needle: anahtar VEYA değerde, harf duyarsız.
 */
export function detailFields(l: LogRow, needle = ''): DetailField[] {
  const out: DetailField[] = [];
  const push = (key: string, value: string | undefined, mono: boolean, actionable = true) => {
    if (value === undefined || value === null || value === '') return;
    out.push({ key, value: String(value), mono, actionable });
  };
  push('service.name', l.serviceName, false);
  push('level', severityLabel(l), false);
  push('trace.id', l.traceId, true);
  push('span.id', l.spanId, true);
  const attrs = Object.entries(l.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b));
  for (const [k, v] of attrs) {
    if (out.some(f => f.key === k)) continue;
    push(k, v, true);
  }
  const n = needle.trim().toLowerCase();
  return n ? out.filter(f => f.key.toLowerCase().includes(n) || f.value.toLowerCase().includes(n)) : out;
}

/** Teknik kanıt (kapalı başlar): kaynak öznitelikleri + belge kimliği. */
export function technicalFields(l: LogRow): { key: string; value: string }[] {
  const out: { key: string; value: string }[] = [];
  if (l.docRef) {
    const slash = l.docRef.indexOf('/');
    if (slash > 0) {
      out.push({ key: '_index', value: l.docRef.slice(0, slash) });
      out.push({ key: '_id', value: l.docRef.slice(slash + 1) });
    } else out.push({ key: 'docRef', value: l.docRef });
  }
  for (const [k, v] of Object.entries(l.resourceAttributes ?? {}).sort(([a], [b]) => a.localeCompare(b))) {
    if (v !== '') out.push({ key: k, value: String(v) });
  }
  return out;
}

/** Görünen satırlar içinde komşu (↑ / ↓); yoksa null. */
export function neighborRow(rows: readonly LogRow[], id: number, dir: -1 | 1): LogRow | null {
  const i = rows.findIndex(r => r.id === id);
  if (i < 0) return null;
  return rows[i + dir] ?? null;
}
