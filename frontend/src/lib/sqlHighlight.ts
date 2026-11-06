// sqlHighlight — v0.10.1859 (operatör: "SQL, Toad'daki gibi renkli görünsün").
//
// SAF ve KAYIPSIZ SQL belirteçleyici: tokens.map(t => t.text).join('') her
// zaman girdinin kendisidir (kopyalanan metin ve ekranda görünen metin aynı
// kalır). Ayrıştırıcı değil; lehçeden bağımsız (Oracle / PostgreSQL / MySQL /
// T-SQL) bir sözcük sınıflandırıcı. Yanlış sınıflandırma yalnız rengi
// etkiler, metni asla.
//
// Maliyet: tek geçiş, O(n). SQL_HIGHLIGHT_MAX üstündeki metin renklendirilmez
// (tek düz belirteç) — dev bir ifade çizimi yavaşlatmasın.

export type SqlTokenKind = 'plain' | 'kw' | 'fn' | 'str' | 'num' | 'param' | 'cmt';

export interface SqlToken {
  kind: SqlTokenKind;
  text: string;
}

export const SQL_HIGHLIGHT_MAX = 20_000;

const KEYWORDS = new Set([
  'SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'NOT', 'IN', 'IS', 'NULL', 'LIKE', 'ILIKE', 'BETWEEN', 'EXISTS',
  'INSERT', 'INTO', 'VALUES', 'UPDATE', 'SET', 'DELETE', 'MERGE', 'USING', 'MATCHED', 'RETURNING',
  'JOIN', 'INNER', 'LEFT', 'RIGHT', 'FULL', 'OUTER', 'CROSS', 'NATURAL', 'LATERAL', 'ON',
  'GROUP', 'BY', 'ORDER', 'HAVING', 'UNION', 'ALL', 'DISTINCT', 'INTERSECT', 'MINUS', 'EXCEPT',
  'AS', 'CASE', 'WHEN', 'THEN', 'ELSE', 'END', 'ASC', 'DESC', 'NULLS', 'FIRST', 'LAST',
  'LIMIT', 'OFFSET', 'FETCH', 'NEXT', 'ROWS', 'ROW', 'ONLY', 'TOP', 'ROWNUM', 'DUAL',
  'WITH', 'RECURSIVE', 'OVER', 'PARTITION', 'WINDOW', 'RANGE', 'UNBOUNDED', 'PRECEDING', 'FOLLOWING', 'CURRENT',
  'CONNECT', 'START', 'PRIOR', 'ESCAPE', 'ANY', 'SOME', 'TRUE', 'FALSE', 'DEFAULT',
  'CREATE', 'ALTER', 'DROP', 'TRUNCATE', 'TABLE', 'INDEX', 'VIEW', 'SEQUENCE', 'TRIGGER', 'SCHEMA', 'DATABASE',
  'PRIMARY', 'KEY', 'FOREIGN', 'REFERENCES', 'CONSTRAINT', 'UNIQUE', 'CHECK', 'TEMPORARY', 'TEMP', 'CASCADE',
  'BEGIN', 'COMMIT', 'ROLLBACK', 'SAVEPOINT', 'DECLARE', 'CALL', 'EXEC', 'EXECUTE', 'PROCEDURE', 'FUNCTION',
  'RETURN', 'IF', 'LOOP', 'WHILE', 'FOR', 'NOWAIT', 'WAIT', 'SKIP', 'LOCKED', 'LOCK',
  'GRANT', 'REVOKE', 'EXPLAIN', 'ANALYZE', 'SHOW', 'USE', 'REPLACE',
]);

const WORD_START = /[\p{L}_]/u;
const WORD_PART = /[\p{L}\p{N}_$#]/u;
const DIGIT = /[0-9]/;

function isWordStart(ch: string | undefined): boolean {
  return ch !== undefined && WORD_START.test(ch);
}
function isWordPart(ch: string | undefined): boolean {
  return ch !== undefined && WORD_PART.test(ch);
}
function isDigit(ch: string | undefined): boolean {
  return ch !== undefined && DIGIT.test(ch);
}

/** Tırnaklı bölgenin sonu (kapanış dahil); çift tırnak kaçışı (`''`, `""`). */
function quotedEnd(sql: string, from: number, quote: string): number {
  let i = from + 1;
  while (i < sql.length) {
    if (sql[i] === quote) {
      if (sql[i + 1] === quote) { i += 2; continue; }
      return i + 1;
    }
    i++;
  }
  return sql.length; // kapanmamış: sona kadar
}

export function tokenizeSql(sql: string): SqlToken[] {
  if (!sql) return [];
  if (sql.length > SQL_HIGHLIGHT_MAX) return [{ kind: 'plain', text: sql }];

  const out: SqlToken[] = [];
  let plainFrom = -1;
  const flushPlain = (to: number) => {
    if (plainFrom >= 0 && to > plainFrom) out.push({ kind: 'plain', text: sql.slice(plainFrom, to) });
    plainFrom = -1;
  };
  const push = (kind: SqlTokenKind, from: number, to: number) => {
    flushPlain(from);
    out.push({ kind, text: sql.slice(from, to) });
  };

  let i = 0;
  const n = sql.length;
  while (i < n) {
    const ch = sql[i];
    const next = sql[i + 1];

    // yorumlar
    if (ch === '-' && next === '-') {
      let j = i + 2;
      while (j < n && sql[j] !== '\n') j++;
      push('cmt', i, j); i = j; continue;
    }
    if (ch === '/' && next === '*') {
      const close = sql.indexOf('*/', i + 2);
      const j = close < 0 ? n : close + 2;
      push('cmt', i, j); i = j; continue;
    }
    // metin sabiti
    if (ch === "'") {
      const j = quotedEnd(sql, i, "'");
      push('str', i, j); i = j; continue;
    }
    // tırnaklı ad: düz metin (anahtar sözcük sanılmasın)
    if (ch === '"' || ch === '`') {
      const j = quotedEnd(sql, i, ch);
      if (plainFrom < 0) plainFrom = i;
      i = j; continue;
    }
    // bağlama yer tutucuları: ?  :ad  :1  $1  @ad
    if (ch === '?') { push('param', i, i + 1); i++; continue; }
    if (ch === ':' && sql[i - 1] !== ':' && next !== ':' && (isWordStart(next) || isDigit(next))) {
      let j = i + 1;
      while (j < n && isWordPart(sql[j])) j++;
      push('param', i, j); i = j; continue;
    }
    if (ch === '$' && isDigit(next) && !isWordPart(sql[i - 1])) {
      let j = i + 1;
      while (j < n && isDigit(sql[j])) j++;
      push('param', i, j); i = j; continue;
    }
    if (ch === '@' && isWordStart(next) && !isWordPart(sql[i - 1])) {
      let j = i + 1;
      while (j < n && isWordPart(sql[j])) j++;
      push('param', i, j); i = j; continue;
    }
    // sayı (bir adın parçası değilse)
    if ((isDigit(ch) || (ch === '.' && isDigit(next))) && !isWordPart(sql[i - 1])) {
      let j = i;
      if (ch === '0' && (next === 'x' || next === 'X')) {
        j = i + 2;
        while (j < n && /[0-9a-fA-F]/.test(sql[j])) j++;
      } else {
        while (j < n && isDigit(sql[j])) j++;
        if (sql[j] === '.' && isDigit(sql[j + 1])) { j++; while (j < n && isDigit(sql[j])) j++; }
        if ((sql[j] === 'e' || sql[j] === 'E') && (isDigit(sql[j + 1]) || ((sql[j + 1] === '+' || sql[j + 1] === '-') && isDigit(sql[j + 2])))) {
          j += 2;
          while (j < n && isDigit(sql[j])) j++;
        }
      }
      push('num', i, j); i = j; continue;
    }
    // sözcük
    if (isWordStart(ch)) {
      let j = i + 1;
      while (j < n && isWordPart(sql[j])) j++;
      const word = sql.slice(i, j);
      const qualified = sql[i - 1] === '.'; // şema.tablo / takma_ad.kolon: asla anahtar sözcük
      if (!qualified && KEYWORDS.has(word.toUpperCase())) {
        push('kw', i, j);
      } else {
        let k = j;
        while (k < n && (sql[k] === ' ' || sql[k] === '\t')) k++;
        if (sql[k] === '(') push('fn', i, j);
        else if (plainFrom < 0) plainFrom = i;
      }
      i = j; continue;
    }
    // boşluk, noktalama, diğer
    if (plainFrom < 0) plainFrom = i;
    i++;
  }
  flushPlain(n);
  return out;
}
