import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  dbSpanRef, dbLinkWindow, dbAttrKind, dbSpanAttrHref, dbSpanDatabaseHref,
  dbSpanStatementHref, dbSpanTableHref, STMT_LIST_PATH, STMT_DETAIL_PATH, DB_LINK_PAD_NS,
} from './dbSpanLinks';
import { stmtVerdict } from './dbSpanLinks';
import { databaseDetailHref, parseDatabasePageRef } from '@/pages/databases/databaseParam';
import { decodeStmtParam } from '@/pages/slowqueries/stmtParam';
import { windowRangeParam } from '@/lib/urlState';

// dbSpanLinks.test.ts — v0.10.1525: Trace span detayı → Databases / ifade
// detayı. Pinlenen: (1) kimlik YALNIZ sunucu damgasından (damgasız span
// linksiz), (2) /database linki Databases listesinin satır linkiyle AYNI
// üretici + aynı üçlü (parse geri aynı ref'i verir), (3) ifade linki
// `?stmt=<hash>|<system>` kodekiyle çözülür, (4) iki rota App.tsx'te kayıtlı.

const START = 1_761_300_000_000_000_000; // 2025-10-24 ns
const span = {
  dbSystem: 'postgresql', dbInstance: 'db.example.test', dbName: 'orders',
  dbStmtHash: '18446744073709551557', startTime: START, endTime: START + 5_000_000,
};
const win = dbLinkWindow(span)!;

describe('dbSpanRef — yalnız sunucu damgası', () => {
  it('damgalı span → üçlü + hash', () => {
    expect(dbSpanRef(span)).toEqual({ system: 'postgresql', instance: 'db.example.test', dbName: 'orders', stmtHash: '18446744073709551557' });
  });
  it('damgasız (eski sunucu) ya da DB olmayan span → null', () => {
    expect(dbSpanRef({ dbSystem: 'postgresql' })).toBeNull();
    expect(dbSpanRef({ dbInstance: 'x' })).toBeNull();
    expect(dbSpanRef({})).toBeNull();
  });
  it('dbName boş → MV nöbetçisi "default" (Databases satırının kimliği)', () => {
    expect(dbSpanRef({ ...span, dbName: '' })!.dbName).toBe('default');
  });
});

describe('pencere', () => {
  it('span ±15 dk', () => {
    expect(win).toEqual({ fromNs: START - DB_LINK_PAD_NS, toNs: START + 5_000_000 + DB_LINK_PAD_NS });
    expect(dbLinkWindow({ startTime: 0, endTime: 0 })).toBeUndefined();
  });
});

describe('/database linki — Databases sayfasıyla aynı kimlik', () => {
  const ref = dbSpanRef(span)!;
  it('databaseDetailHref ile birebir, source=spans', () => {
    const href = dbSpanDatabaseHref(ref, win);
    expect(href).toBe(databaseDetailHref(
      { system: 'postgresql', instance: 'db.example.test', dbName: 'orders', source: 'spans' },
      { range: windowRangeParam(win) }));
    const back = parseDatabasePageRef(href.slice(href.indexOf('?')));
    expect(back).toEqual({ system: 'postgresql', instance: 'db.example.test', dbName: 'orders', source: 'spans' });
  });
  it('db.name / db.namespace / server.address → aynı detay sayfası', () => {
    for (const k of ['db.name', 'db.namespace', 'server.address', 'db.connection_string']) {
      expect(dbSpanAttrHref(k, 'v', ref, win)).toEqual({ href: dbSpanDatabaseHref(ref, win), kind: 'database' });
    }
  });
  it('db.system → motor süzgeçli Databases listesi', () => {
    const l = dbSpanAttrHref('db.system', 'postgresql', ref, win)!;
    expect(l.kind).toBe('engine');
    expect(l.href.startsWith('/databases?')).toBe(true);
    expect(new URLSearchParams(l.href.split('?')[1]).get('dbsys')).toBe('postgresql');
  });
});

describe('ifade linki — stmt kodeki', () => {
  const ref = dbSpanRef(span)!;
  it('db.statement / db.query.text → /databases/statement?stmt=<hash>|<system>', () => {
    for (const k of ['db.statement', 'db.query.text']) {
      const l = dbSpanAttrHref(k, 'SELECT 1', ref, win)!;
      expect(l.kind).toBe('statement');
      expect(l.href.startsWith(`${STMT_DETAIL_PATH}?`)).toBe(true);
      const p = new URLSearchParams(l.href.split('?')[1]);
      // uint64 > 2^53 kayıpsız (ondalık dizge sözleşmesi).
      expect(decodeStmtParam(p.get('stmt'))).toEqual({ hash: '18446744073709551557', system: 'postgresql' });
      expect(p.get('range')).toBe(windowRangeParam(win));
    }
  });
  it('hash yok → ifade linki yok (yanlış link linksizlikten kötü)', () => {
    expect(dbSpanStatementHref({ ...ref, stmtHash: '' }, win)).toBeNull();
    expect(dbSpanAttrHref('db.statement', 'SELECT 1', { ...ref, stmtHash: '' }, win)).toBeNull();
  });
});

describe('tablo linki — ifade listesi, SQL araması', () => {
  const ref = dbSpanRef(span)!;
  it('db.sql.table → liste ?dbsys&dbname&q', () => {
    const href = dbSpanTableHref(ref, 'orders_items', win)!;
    expect(href.startsWith(`${STMT_LIST_PATH}?`)).toBe(true);
    const p = new URLSearchParams(href.split('?')[1]);
    expect([p.get('dbsys'), p.get('dbname'), p.get('q')]).toEqual(['postgresql', 'orders', 'orders_items']);
  });
  it("'default' db adı süzgeç olarak yazılmaz", () => {
    const p = new URLSearchParams(dbSpanTableHref({ ...ref, dbName: 'default' }, 't')!.split('?')[1]);
    expect(p.has('dbname')).toBe(false);
  });
});

describe('ilgisiz anahtarlar linksiz', () => {
  it('db.user / db.operation / http.route → null', () => {
    for (const k of ['db.user', 'db.operation', 'http.route', 'service.name']) expect(dbAttrKind(k)).toBeNull();
    expect(dbSpanAttrHref('db.name', 'orders', null, win)).toBeNull();
  });
});

describe('rotalar App.tsx.te KAYITLI', () => {
  const app = readFileSync(join(__dirname, '..', 'App.tsx'), 'utf8');
  for (const path of [STMT_LIST_PATH, STMT_DETAIL_PATH, '/database', '/databases']) {
    it(path, () => { expect(app).toContain(`path="${path}"`); });
  }
});

// v0.10.1956 — span detayında sorgu bağlamı: bu çalışmanın p95'e göre yeri.
describe('stmtVerdict', () => {
  it('eşikler: ≥3× kat sayısı, ≥1,5× yavaş, altı olağan; p95 yoksa null', () => {
    expect(stmtVerdict(1590, 180)).toEqual({ kind: 'slowX', x: 9 });
    expect(stmtVerdict(300, 180)).toEqual({ kind: 'slow' });
    expect(stmtVerdict(100, 180)).toEqual({ kind: 'normal' });
    expect(stmtVerdict(100, 0)).toBeNull();
    expect(stmtVerdict(100, undefined)).toBeNull();
  });
});
