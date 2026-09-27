import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { jsxOpenTags } from '@/styles/jsxTags';

// v0.10.973 — tablo standardı dilim 6 (docs/DECISIONS.md "Tablo standardı").
// Üç sayfa hücresi satır içi stilden standardın sınıfına geçti; bu çiviler
// satır içi stilin (ve Endpoints'in hata satırı zemininin) geri dönmesini
// yakalar. tableUnityRatchet yalnız TOPLAMI sınırlar: burada başka bir
// dosyanın düşüşü bu hücrelerin geri gelişini örtemez.
const read = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');
const tdWith = (src: string, needle: string) => {
  const hits = jsxOpenTags(src, 'td').filter(t => t.tag.includes(needle));
  expect(hits, `<td … ${needle} …> bulunamadı — çivi hedefini kaybetti`).toHaveLength(1);
  return hits[0].tag;
};

describe('Metrics — Services hücresi `num` (T4)', () => {
  const src = read('./Metrics.tsx');
  it('sayı hücresi num sınıfıyla, satır içi tabular-nums yok', () => {
    const td = tdWith(src, 'm.serviceCount');
    expect(td).toContain('className="num"');
    expect(td).not.toMatch(/\sstyle=\{/);
  });
  it('kolon numeric kalır (başlık ve hücre aynı hizada)', () => {
    expect(src).toMatch(/id: 'svcs', label: 'Services',[^\n]*numeric: true/);
  });
});

describe('SlowQueries — genişletilmiş örnek hücresi `row-detail` (T6)', () => {
  const src = read('./SlowQueries.tsx');
  it('colSpan 12 detay hücresi row-detail, satır içi zemin/dolgu yok', () => {
    const td = tdWith(src, 'colSpan={12}');
    expect(td).toContain('className="row-detail"');
    expect(td).not.toMatch(/\sstyle=\{/);
  });
});

describe('Endpoints — hata satırında dolgu yok (T9)', () => {
  const src = read('./Endpoints.tsx');
  it('satır ve sabit Traces hücresi kırmızı zemin taşımaz', () => {
    expect(src).not.toMatch(/color-mix\(in srgb, var\(--err\)/);
    const row = jsxOpenTags(src, 'tr').filter(t => t.tag.includes('data-row-action'));
    expect(row).toHaveLength(1);
    expect(row[0].tag).not.toMatch(/\sstyle=\{/);
    expect(tdWith(src, 'className="sticky-right"')).not.toMatch(/\sstyle=\{/);
  });
  it('sapmayı Error % rozeti işaretlemeye devam eder', () => {
    expect(src).toContain("const errCls = r.errorRate >= 5 ? 'b-err' : r.errorRate >= 1 ? 'b-warn' : 'b-gray';");
    expect(src).toContain('<span className={`badge ${errCls}`}>{r.errorRate.toFixed(2)}%</span>');
  });
});
