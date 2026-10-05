// oracleGroup.ts — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün, hatta Exceptions altında da olabilir.")
//
// Oracle hata tablosu grupları exception_groups'ta `ora:` parmak iziyle yaşar
// (backend internal/oracle/exgroups.go): type = hata kodu, message = operasyon
// kodu, service = baskın servis (trace → servis çözümü) yoksa `oracle:<kaynak>`.
// Satır biçimi Exceptions'ınkiyle AYNI (TriageTitleCell, v0.10.1084):
//   kalın başlık  "<kod> · <operasyon>"
//   soluk satır   "<etiket> · <kaynak> · N servis"
// Saf yardımcılar burada; bileşenler (AnomaliesPage, ProblemDetail) yalnız çizer.
//
// v0.10.1108 (operatör: "exceptionsta Oracle yazıyor onun yerine … Teknik Hata
// gibi") — kullanıcıya görünen ad `label` PARAMETRESİ (gizli global yok):
// bileşen `useBranding().oracleGroupLabel` verir (varsayılan "Teknik hata",
// Settings › Branding). Kod adları, `?oracle=` ve `ora:` parmak izi değişmez;
// AI istemleri Oracle bilmeye devam eder (yalnız görünen metin).
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

/**
 * v0.10.1109 — Problems kuyruğu (/inbox) satırının soluk satırı: "<etiket> ·
 * <operasyon>". Satır listede zaten vardı ama span exception'ından ayırt
 * edilemiyordu (1108 etiketi yalnız Exceptions'a girmişti).
 */
export function oracleInboxDetail(operation: string, label: string): string {
  const op = operation.trim();
  return op ? `${label} · ${op}` : label;
}

/** Kaynak adı: bilgi yoksa sentetik servisten (`oracle:<ad>`), o da yoksa "?". */
export function oracleSourceName(g: Pick<ExceptionGroup, 'service' | 'oracle'>): string {
  if (g.oracle?.sourceName) return g.oracle.sourceName;
  if (g.service.startsWith(ORACLE_SERVICE_PREFIX)) return g.service.slice(ORACLE_SERVICE_PREFIX.length) || '?';
  return '?';
}

/** Soluk satır: "<etiket> · <kaynak> · N servis" (servis bilinmiyorsa "servis bilinmiyor"). */
export function oracleRowDetail(g: Pick<ExceptionGroup, 'service' | 'oracle'>, label: string): string {
  const n = g.oracle?.serviceCount ?? 0;
  const svc = n > 0 ? `${n} servis` : 'servis bilinmiyor';
  return `${label} · ${oracleSourceName(g)} · ${svc}`;
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
export function oracleRowTooltip(g: Pick<ExceptionGroup, 'type' | 'message' | 'service' | 'oracle'>, label: string): string {
  const lines = [oracleRowTitle(g), oracleRowDetail(g, label)];
  const ch = oracleChannelsText(g.oracle, 6);
  if (ch) lines.push(`Kanal: ${ch}`);
  const svcs = (g.oracle?.services ?? []).map(s => s.name);
  if (svcs.length) lines.push(`Servisler: ${svcs.join(', ')}`);
  if (g.oracle?.known) lines.push(`Son 1 sa: ${g.oracle.lastHour ?? 0} (önceki saat ${g.oracle.prevHour ?? 0})`);
  lines.push('Yalnız KAPANMIŞ dakikalar sayılır (özel SQL penceresinden çıkan dakika) — son görülme duvar saatinin birkaç dakika gerisindedir.');
  return lines.join('\n');
}

/**
 * AI paneli başlık satırı (v0.10.1100): "<etiket> · <kaynak> · <kod> · <operasyon>".
 * Oracle grubunda stack yok — panel "Kodu da incele" yerine bu bağlamı gösterir.
 * Bilgi henüz yoksa (yükleniyor / 404) yalnız "<etiket> grubu" (v0.10.1108).
 */
export function oracleExplainLine(info: Pick<OracleGroupInfo, 'sourceName' | 'code' | 'operation'> | null | undefined, label: string): string {
  const parts = [info?.sourceName, info?.code, info?.operation].map(s => (s ?? '').trim()).filter(Boolean);
  return parts.length ? [label, ...parts].join(' · ') : `${label} grubu`;
}

/** Exceptions çipinin görünen etiketleri (v0.10.1108): "<etiket> N" / "<etiket> hariç". */
export function oracleFacetLabels(label: string, count: number, fmt: (n: number) => string): { only: string; exclude: string } {
  return { only: count >= 0 ? `${label} ${fmt(count)}` : label, exclude: `${label} hariç` };
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
