// sqlHighlight — v0.10.1859. Belirteçleyici KAYIPSIZ olmalı (ekrandaki ve
// kopyalanan metin = girdi) ve yanlış renk üretmemeli.
import { describe, it, expect } from 'vitest';
import { tokenizeSql, SQL_HIGHLIGHT_MAX, type SqlToken } from './sqlHighlight';

const kinds = (sql: string, kind: SqlToken['kind']) =>
  tokenizeSql(sql).filter(t => t.kind === kind).map(t => t.text);
const join = (sql: string) => tokenizeSql(sql).map(t => t.text).join('');

describe('tokenizeSql', () => {
  it('lossless on every dialect sample', () => {
    const samples = [
      'SELECT o.id, o.total FROM orders o WHERE o.customer_id = ? AND o.status IN (?, ?) ORDER BY o.created_at DESC',
      "select nvl(sum(t.amount),0) from acme.txn t where t.ccy = 'TRY' and t.dt >= to_date(:1,'YYYY-MM-DD') and rownum <= 10",
      'UPDATE "Order Items" SET qty = qty + 1 WHERE id = $1 RETURNING id',
      'INSERT INTO `cart` (`user`, total) VALUES (@uid, 12.50) -- trailing comment',
      "/* hint */ SELECT TOP 5 name FROM dbo.users WHERE note = 'it''s' AND score > 1e3",
      'SELECT a::text, b FROM t WHERE c = 0x1F',
      "'unterminated string",
      '/* unterminated comment',
      '',
      '   \n\t ',
      'çalışan_adı = ? AND şube IN (1,2)',
    ];
    for (const s of samples) expect(join(s)).toBe(s);
  });

  it('keywords are case-insensitive and text is not rewritten', () => {
    expect(kinds('select a from t Where b is NULL', 'kw')).toEqual(['select', 'from', 'Where', 'is', 'NULL']);
  });

  it('qualified names are never keywords', () => {
    // t.end / o.order / s.key: kolon adı, anahtar sözcük DEĞİL
    expect(kinds('SELECT t.end, o.order, s.key FROM t', 'kw')).toEqual(['SELECT', 'FROM']);
  });

  it('a word followed by "(" is a function; keywords stay keywords', () => {
    expect(kinds('SELECT COUNT(*), nvl (x, 0), pkg.calc(1) FROM t WHERE a IN (1)', 'fn')).toEqual(['COUNT', 'nvl', 'calc']);
    expect(kinds('SELECT 1 FROM t WHERE a IN (1)', 'kw')).toContain('IN');
  });

  it('strings keep doubled quotes and hide keywords inside', () => {
    expect(kinds("SELECT 'it''s FROM here' FROM t", 'str')).toEqual(["'it''s FROM here'"]);
    expect(kinds("SELECT 'it''s FROM here' FROM t", 'kw')).toEqual(['SELECT', 'FROM']);
  });

  it('quoted identifiers are plain, not keywords', () => {
    expect(kinds('SELECT "select", `from` FROM t', 'kw')).toEqual(['SELECT', 'FROM']);
  });

  it('comments: line and block', () => {
    expect(kinds('SELECT 1 -- select all\nFROM t /* where x */', 'cmt')).toEqual(['-- select all', '/* where x */']);
    expect(kinds('SELECT 1 -- select all\nFROM t', 'kw')).toEqual(['SELECT', 'FROM']);
  });

  it('bind placeholders', () => {
    expect(kinds('a = ? AND b = :name AND c = :1 AND d = $2 AND e = @p1', 'param')).toEqual(['?', ':name', ':1', '$2', '@p1']);
  });

  it('PostgreSQL cast is not a placeholder', () => {
    expect(kinds('SELECT a::text FROM t', 'param')).toEqual([]);
  });

  it('numbers, but not digits inside names', () => {
    expect(kinds('SELECT col1, t2.x, 42, 3.14, .5, 1e3, 0x1F FROM t', 'num')).toEqual(['42', '3.14', '.5', '1e3', '0x1F']);
  });

  it('oversized input is returned as one plain token', () => {
    const big = 'SELECT ' + 'x,'.repeat(SQL_HIGHLIGHT_MAX);
    expect(tokenizeSql(big)).toEqual([{ kind: 'plain', text: big }]);
  });

  it('adjacent plain text is merged (few spans)', () => {
    const t = tokenizeSql('a , b . c');
    expect(t).toEqual([{ kind: 'plain', text: 'a , b . c' }]);
  });
});
