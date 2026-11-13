// Traces.characterization.pin.test.ts — v0.10.2084 (Traces shadcn geçişi,
// dilim S0; plan docs/frontend/traces-shadcn-plan.md §3).
//
// Render testinin (Traces.characterization.test.tsx) ölçemediği ya da
// kaynakta tek satırla sabitlenen sözleşmeler. Üretim kodu değişmedi.
// Yorumlar ayıklanarak okunur: bir yorumdaki "Compare" kelimesi pini
// kırmaz, koda geri gelen bir düğme kırar.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { STRIP_STAT_DEFAULT } from '@/components/traces/stripStat';
import { ROW_H } from '@/components/ui/DataTable';

const SRC = resolve(__dirname, '..');
const raw = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
// Satır ve blok yorumları + JSX yorumları. (Dize içi `//` Traces.tsx'te
// yalnız URL'lerde; yalnız varlık/yokluk aradığımız için zararsız.)
const code = raw
  .replace(/\{\/\*[\s\S]*?\*\/\}/g, '')
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .replace(/^\s*\/\/.*$/gm, '')
  .replace(/\s\/\/ .*$/gm, '');

describe('S0 pin — şerit varsayılanı', () => {
  // DECISIONS üç ayrı değer yazıyor (:503 p95, :11634 p99, :11637 p50);
  // KOD p99 (v0.10.1524, operatör "Default p99 olsun"). Bu programda değişmez.
  it('STRIP_STAT_DEFAULT === p99', () => {
    expect(STRIP_STAT_DEFAULT).toBe('p99');
  });
});

describe('S0 pin — kaldırılmış kontroller kaynakta yok', () => {
  it('Correlate ve alt Compare (v0.10.347) yok', () => {
    expect(code).not.toMatch(/Correlate/i);
    expect(code).not.toMatch(/\bCompare\b/);
    expect(code).not.toContain('/compare');
  });
  it('"veriye sığdır" (v0.10.813) ve şerit expand/shrink (v0.10.725) yok', () => {
    expect(code).not.toMatch(/veriye sığdır|fitExtent|fit to data/i);
    expect(code).not.toMatch(/chartTall|⌄ expand|⌃ shrink/);
  });
});

describe('S0 pin — arama deneyimi önce: listede poll yok', () => {
  it('setInterval / useAutoRefresh / refetchInterval / LIVE_META / kendi EventSource yok', () => {
    for (const p of [/setInterval\(/, /useAutoRefresh/, /refetchInterval/, /LIVE_META/, /new EventSource/, /useEventStream/]) {
      expect(code, String(p)).not.toMatch(p);
    }
  });
  it('`/` liste tablosuna bağlanmaz (searchRef verilmez, v0.9.1003)', () => {
    const i = code.indexOf("storageKey: 'traces-list'");
    const block = code.slice(i, code.indexOf('});', i));
    expect(block).toContain('onOpen:');
    expect(block).not.toContain('searchRef');
  });
});

describe('S0 pin — URL sahip parametre kümesi (v0.10.1218)', () => {
  it('TRACES_OWNED_PARAMS aynen', () => {
    const m = code.match(/const TRACES_OWNED_PARAMS = \[([\s\S]*?)\] as const;/);
    expect(m).not.toBeNull();
    const list = [...m![1].matchAll(/'([^']+)'/g)].map(x => x[1]);
    expect(list).toEqual([
      'view', 'viz', 'rt', 'sort', 'order', 'page', 'groupBy', 'groupAttr', 'aggSort', 'aggOrder',
      'having', 'service', 'search', 'op', 'minMs', 'maxMs', 'hasError', 'rootOnly',
      'services', 'filters', 'filterGroup', 'cols',
    ]);
  });
  it('varsayılanlar URL\'ye yazılmaz (rt / sort / order / page)', () => {
    expect(code).toContain("['rt',       stripStat !== STRIP_STAT_DEFAULT ? stripStat : '']");
    expect(code).toContain("['sort',     sort !== 'time' ? sort : '']");
    expect(code).toContain("['order',    order !== 'desc' ? order : '']");
    expect(code).toContain("['page',     page > 0 ? page : '']");
  });
});

describe('S0 pin — satır yüksekliği ve sayfa boyu', () => {
  it('ROW_H 36 ve liste VirtualTable\'a ROW_H verir', () => {
    expect(ROW_H).toBe(36);
    expect(code).toContain('rowHeight={ROW_H}');
  });
  it('50 satır/sayfa üç yerde aynı: istek, useDataTable server demeti, Pager', () => {
    expect(code).toContain('limit: 50, offset: page * 50, from, to, sort, order,');
    expect(code).toContain('server: { page, pageSize: 50, hasMore, onPage: setPage },');
    expect(code).toMatch(/<Pager mode="offset" count="skip"\s+page=\{page\} pageSize=\{50\}/);
  });
});

describe('S0 pin — ColumnFilterShell paylaşımı (Traces + Problems/Exceptions)', () => {
  function walk(dir: string, out: string[] = []): string[] {
    for (const e of readdirSync(dir)) {
      const p = join(dir, e);
      if (statSync(p).isDirectory()) walk(p, out);
      else if (/\.tsx?$/.test(p) && !/\.test\.tsx?$/.test(p)) out.push(p);
    }
    return out;
  }
  it('kabuğu yalnız pages/traces/ColumnFilter.tsx ve features/anomalies/AnomaliesPage.tsx kullanır', () => {
    const users = walk(SRC)
      .filter(p => /<ColumnFilterShell\b/.test(readFileSync(p, 'utf8')))
      .map(p => relative(SRC, p).split(sep).join('/'))
      .sort();
    expect(users).toEqual(['features/anomalies/AnomaliesPage.tsx', 'pages/traces/ColumnFilter.tsx']);
  });
  it('AnomaliesPage kabuğu Traces modülünden alır (çatallanmış ikinci kabuk yok)', () => {
    const src = readFileSync(resolve(SRC, 'features/anomalies/AnomaliesPage.tsx'), 'utf8');
    expect(src).toContain("import { ColumnFilterShell } from '@/pages/traces/ColumnFilter';");
  });
});
