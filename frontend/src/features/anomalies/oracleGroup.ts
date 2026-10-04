// oracleGroup.ts — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün, hatta Exceptions altında da olabilir.")
//
// Oracle hata tablosu grupları exception_groups'ta `ora:` parmak iziyle yaşar
// (backend internal/oracle/exgroups.go): type = hata kodu, message = operasyon
// kodu, service = baskın servis (trace → servis çözümü) yoksa `oracle:<kaynak>`.
// Satır biçimi Exceptions'ınkiyle AYNI (TriageTitleCell, v0.10.1084):
//   kalın başlık  "<kod> · <operasyon>"
//   soluk satır   "Oracle · <kaynak> · N servis"
// Saf yardımcılar burada; bileşenler (AnomaliesPage, ProblemDetail) yalnız çizer.
import type { ExceptionGroup, OracleGroupInfo } from '@/lib/types';

export const ORACLE_GROUP_PREFIX = 'ora:';
export const ORACLE_SERVICE_PREFIX = 'oracle:';

export function isOracleGroup(g: Pick<ExceptionGroup, 'fingerprint'> | string): boolean {
  const fp = typeof g === 'string' ? g : g.fingerprint;
  return fp.startsWith(ORACLE_GROUP_PREFIX);
}

/** Sentetik servis (`oracle:<kaynak>`) — servis sayfasına link VERİLMEZ (yok). */
export function isSyntheticOracleService(service: string): boolean {
  return service === '' || service.startsWith(ORACLE_SERVICE_PREFIX);
}

/** Satır başlığı: "<kod> · <operasyon>" (operasyon yoksa yalnız kod). */
export function oracleRowTitle(g: Pick<ExceptionGroup, 'type' | 'message'>): string {
  const code = g.type.trim() || '—';
  const op = g.message.trim();
  return op ? `${code} · ${op}` : code;
}

/** Kaynak adı: bilgi yoksa sentetik servisten (`oracle:<ad>`), o da yoksa "?". */
export function oracleSourceName(g: Pick<ExceptionGroup, 'service' | 'oracle'>): string {
  if (g.oracle?.sourceName) return g.oracle.sourceName;
  if (g.service.startsWith(ORACLE_SERVICE_PREFIX)) return g.service.slice(ORACLE_SERVICE_PREFIX.length) || '?';
  return '?';
}

/** Soluk satır: "Oracle · <kaynak> · N servis" (servis bilinmiyorsa "servis bilinmiyor"). */
export function oracleRowDetail(g: Pick<ExceptionGroup, 'service' | 'oracle'>): string {
  const n = g.oracle?.serviceCount ?? 0;
  const svc = n > 0 ? `${n} servis` : 'servis bilinmiyor';
  return `Oracle · ${oracleSourceName(g)} · ${svc}`;
}

/** Kanal kırılımı kısa metni ("MOB %60 · WEB %40"), en çok n kanal. */
export function oracleChannelsText(info: Pick<OracleGroupInfo, 'channels'> | undefined, n = 3): string {
  const ch = info?.channels ?? [];
  const total = ch.reduce((s, c) => s + c.count, 0);
  if (total <= 0) return '';
  const parts = ch.slice(0, n).map(c => `${c.name} %${Math.round((c.count * 100) / total)}`);
  if (ch.length > n) parts.push(`+${ch.length - n}`);
  return parts.join(' · ');
}

/** Satır başlığının title metni: kırılım + gecikme notu. */
export function oracleRowTooltip(g: Pick<ExceptionGroup, 'type' | 'message' | 'service' | 'oracle'>): string {
  const lines = [oracleRowTitle(g), oracleRowDetail(g)];
  const ch = oracleChannelsText(g.oracle, 6);
  if (ch) lines.push(`Kanal: ${ch}`);
  const svcs = (g.oracle?.services ?? []).map(s => s.name);
  if (svcs.length) lines.push(`Servisler: ${svcs.join(', ')}`);
  if (g.oracle?.known) lines.push(`Son 1 sa: ${g.oracle.lastHour ?? 0} (önceki saat ${g.oracle.prevHour ?? 0})`);
  lines.push('Yalnız KAPANMIŞ dakikalar sayılır (özel SQL penceresinden çıkan dakika) — son görülme duvar saatinin birkaç dakika gerisindedir.');
  return lines.join('\n');
}

/**
 * AI paneli başlık satırı (v0.10.1100): "Oracle · <kaynak> · <kod> · <operasyon>".
 * Oracle grubunda stack yok — panel "Kodu da incele" yerine bu bağlamı gösterir.
 * Bilgi henüz yoksa (yükleniyor / 404) yalnız "Oracle hata grubu".
 */
export function oracleExplainLine(info: Pick<OracleGroupInfo, 'sourceName' | 'code' | 'operation'> | null | undefined): string {
  const parts = [info?.sourceName, info?.code, info?.operation].map(s => (s ?? '').trim()).filter(Boolean);
  return parts.length ? ['Oracle', ...parts].join(' · ') : 'Oracle hata grubu';
}

/** Exceptions "Oracle" çipi — URL ?oracle= değeri. Yok = Oracle grupları DAHİL. */
export type OracleFacet = 'all' | 'only' | 'exclude';

export function parseOracleFacet(v: string | null | undefined): OracleFacet {
  return v === 'only' || v === 'exclude' ? v : 'all';
}

/** İstek parametresi: 'all' → gönderilmez. */
export function oracleFacetParam(f: OracleFacet): 'only' | 'exclude' | undefined {
  return f === 'all' ? undefined : f;
}
