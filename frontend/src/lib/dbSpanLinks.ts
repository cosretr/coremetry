import type { SpanRow } from '@/lib/types';
import { databaseDetailHref, databasesFilterHref } from '@/pages/databases/databaseParam';
import { stmtDetailHref, STMT_DETAIL_PATH } from '@/pages/slowqueries/stmtParam';
import { windowRangeParam } from '@/lib/urlState';

// dbSpanLinks — v0.10.1525 (operatör, prod 2026-10-24): Trace span detayının
// DATABASE grubu `service.name` gibi linkli olsun.
//
// ── KİMLİK SUNUCUDAN ────────────────────────────────────────────────────
//
// Databases detayının kimliği ÜÇLÜ (system, instance, dbName —
// databaseParam.ts) ve `instance` altı basamaklı bir coalesce zinciri
// (peer.service → server.address → net.peer.name → db.host → db.name →
// service.name). İfade detayının kimliği stmt_hash = xxHash64(normalize(
// db.statement)). İkisini de burada yeniden türetmek iki ikinci kaynak
// açardı; sunucu trace yükünde span'i MV'lerin kendi kurallarıyla
// damgalıyor (chstore/db_span_identity.go: dbInstance / dbName /
// dbStmtHash). Damga yoksa (eski sunucu, DB olmayan span) link YOK —
// yanlış bir link linksizlikten kötüdür (spanEntityLinks.ts).
//
// ── PENCERE ─────────────────────────────────────────────────────────────
//
// Span bir OLAY; varış sayfası span anını göstermeli (K1). Ama DB yüzeyleri
// 1 / 5 dk kovalarla okur, ±1 dk'lık log penceresi tek kova olurdu. Link
// span'in etrafında ±15 dk açılır (logs linkinin LOGS_LINK_EXTRA_NS payı).

export const DB_LINK_PAD_NS = 15 * 60_000_000_000;

export interface DbSpanRef {
  system: string;
  instance: string;
  dbName: string;
  /** stmt_hash ondalık dizge; ifadesiz span'de ''. */
  stmtHash: string;
}

/** Sunucu damgasından kimlik; damgasız / DB olmayan span → null. */
export function dbSpanRef(span: Pick<SpanRow, 'dbSystem' | 'dbInstance' | 'dbName' | 'dbStmtHash'>): DbSpanRef | null {
  const system = (span.dbSystem ?? '').trim();
  const instance = (span.dbInstance ?? '').trim();
  if (!system || !instance) return null;
  return { system, instance, dbName: (span.dbName ?? '').trim() || 'default', stmtHash: (span.dbStmtHash ?? '').trim() };
}

/** Span'in etrafındaki link penceresi (ns). */
export function dbLinkWindow(span: Pick<SpanRow, 'startTime' | 'endTime'>): { fromNs: number; toNs: number } | undefined {
  if (!(span.startTime > 0)) return undefined;
  const end = span.endTime > span.startTime ? span.endTime : span.startTime;
  return { fromNs: span.startTime - DB_LINK_PAD_NS, toNs: end + DB_LINK_PAD_NS };
}

export type DbLinkKind = 'engine' | 'database' | 'statement' | 'table';

/** Attribute anahtarı → hedef türü (null = link yok). */
export function dbAttrKind(k: string): DbLinkKind | null {
  switch (k) {
    case 'db.system':
    case 'db.system.name':
      return 'engine';
    case 'db.name':
    case 'db.namespace':
    case 'server.address':
    case 'db.connection_string':
      return 'database';
    case 'db.statement':
    case 'db.query.text':
      return 'statement';
    case 'db.sql.table':
    case 'db.collection.name':
      return 'table';
  }
  return null;
}

/** /database detayı — Databases listesinin satır linkiyle AYNI üretici. */
export function dbSpanDatabaseHref(ref: DbSpanRef, window?: { fromNs: number; toNs: number }): string {
  const range = window ? windowRangeParam(window) || undefined : undefined;
  return databaseDetailHref(
    { system: ref.system, instance: ref.instance, dbName: ref.dbName, source: 'spans' },
    { range },
  );
}

/** İfade detayı (/databases/statement?stmt=<hash>|<system>); kimliksiz → null. */
export function dbSpanStatementHref(ref: DbSpanRef, window?: { fromNs: number; toNs: number }): string | null {
  if (!ref.stmtHash || !window) return null;
  return stmtDetailHref({ hash: ref.stmtHash, system: ref.system }, window);
}

/** İfade listesi, bu motor + veritabanı, SQL içinde tablo adı araması (sunucu `q`). */
export function dbSpanTableHref(ref: DbSpanRef, table: string, window?: { fromNs: number; toNs: number }): string | null {
  const t = table.trim();
  if (!t) return null;
  const p = new URLSearchParams();
  p.set('dbsys', ref.system);
  if (ref.dbName && ref.dbName !== 'default') p.set('dbname', ref.dbName);
  p.set('q', t.slice(0, 200)); // sunucu arama tavanı (dbStatementsSearchMax)
  const range = window ? windowRangeParam(window) : '';
  if (range) p.set('range', range);
  return `${STMT_LIST_PATH}?${p.toString()}`;
}

/** İfade listesinin rotası (App.tsx ile birebir — dbSpanLinks.test.ts pinler). */
export const STMT_LIST_PATH = '/databases/slow-queries';
// STMT_DETAIL_PATH yeniden ihraç: iki rota tek yerden okunsun.
export { STMT_DETAIL_PATH };

/** Bir DATABASE attribute satırının linki; ilgisiz anahtar / kimliksiz → null. */
export function dbSpanAttrHref(
  k: string, v: string, ref: DbSpanRef | null, window?: { fromNs: number; toNs: number },
): { href: string; kind: DbLinkKind } | null {
  if (!ref || !v) return null;
  const kind = dbAttrKind(k);
  if (!kind) return null;
  const range = window ? windowRangeParam(window) || undefined : undefined;
  let href: string | null = null;
  switch (kind) {
    case 'engine': href = databasesFilterHref({ dbSystem: ref.system }, { range }); break;
    case 'database': href = dbSpanDatabaseHref(ref, window); break;
    case 'statement': href = dbSpanStatementHref(ref, window); break;
    case 'table': href = dbSpanTableHref(ref, v, window); break;
  }
  return href ? { href, kind } : null;
}

/**
 * v0.10.1956 (operatör: span detayında sorgu bağlamı) — bu çalışmanın süresi, sorgunun
 * önceki 1 saatlik p95'ine göre: ≥ 3× → kat sayısıyla yavaş, ≥ 1,5× → yavaş, altı → olağan.
 * p95 yoksa (veri yok) null.
 */
export type StmtVerdict = { kind: 'slowX'; x: number } | { kind: 'slow' } | { kind: 'normal' };
export function stmtVerdict(durationMs: number, p95Ms: number | undefined | null): StmtVerdict | null {
  if (!p95Ms || !(p95Ms > 0) || !Number.isFinite(durationMs)) return null;
  const x = durationMs / p95Ms;
  if (x >= 3) return { kind: 'slowX', x: Math.round(x) };
  if (x >= 1.5) return { kind: 'slow' };
  return { kind: 'normal' };
}

