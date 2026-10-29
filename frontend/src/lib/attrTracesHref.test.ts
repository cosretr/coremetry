// v0.10.1555 — operatör: "Custom attribute'lara göre filtreleme yapabilmeli
// kullanıcılar". Span detayındaki attribute satırı → /traces süzgeci.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { attrTracesHref, ATTR_PIVOT_BEFORE_NS, ATTR_PIVOT_AFTER_NS } from './pivotHref';
import { decodeFilters } from './urlState';

const at = 1_793_009_399_876_000_000;
const params = (href: string) => new URLSearchParams(href.slice(href.indexOf('?') + 1));

describe('attrTracesHref', () => {
  it('custom span attribute → key = value süzgeci, span anı penceresi, rootOnly kapalı', () => {
    const h = attrTracesHref({ key: 'deviceUid', value: '202f99e50d12af49', atNs: at })!;
    const p = params(h);
    expect(h.startsWith('/traces?')).toBe(true);
    expect(decodeFilters(p.get('filters'))).toEqual([{ k: 'deviceUid', op: '=', v: ['202f99e50d12af49'] }]);
    expect(p.get('rootOnly')).toBe('false');
    expect(p.get('view')).toBe('list');
    expect(p.get('range')).toBe(`custom:${Math.round((at - ATTR_PIVOT_BEFORE_NS) / 1e6)}-${Math.round((at + ATTR_PIVOT_AFTER_NS) / 1e6)}`);
  });
  it('hariç tut → !=; resource attribute → resource. öneki (tekrarlamaz)', () => {
    expect(decodeFilters(params(attrTracesHref({ key: 'maskedMBB', value: '1859386206', atNs: at, exclude: true })!).get('filters')))
      .toEqual([{ k: 'maskedMBB', op: '!=', v: ['1859386206'] }]);
    expect(decodeFilters(params(attrTracesHref({ key: 'service.version', value: '1.2', atNs: at, resource: true })!).get('filters')))
      .toEqual([{ k: 'resource.service.version', op: '=', v: ['1.2'] }]);
    expect(decodeFilters(params(attrTracesHref({ key: 'resource.x', value: 'y', atNs: at, resource: true })!).get('filters')))
      .toEqual([{ k: 'resource.x', op: '=', v: ['y'] }]);
  });
  it('boş değer / dev değer / zamansız span → bağlantı yok', () => {
    expect(attrTracesHref({ key: 'k', value: '', atNs: at })).toBeNull();
    expect(attrTracesHref({ key: ' ', value: 'v', atNs: at })).toBeNull();
    expect(attrTracesHref({ key: 'k', value: 'x'.repeat(501), atNs: at })).toBeNull();
    expect(attrTracesHref({ key: 'k', value: 'v', atNs: 0 })).toBeNull();
  });
});

describe('SpanDetail kablosu', () => {
  const src = readFileSync(new URL('../components/SpanDetail.tsx', import.meta.url), 'utf8');
  it('span ve resource satırları kapsamıyla, eylemler = / ≠ bağlantısı', () => {
    expect(src).toContain('<Row key={k} k={k} v={v} copyable scope="span" />');
    expect(src).toContain('<Row key={k} k={k} v={v} copyable scope="resource" />');
    expect(src).toContain('<AttrPivotCtx.Provider value={span.startTime > 0 ? span.startTime : 0}>');
    expect(src.match(/className="ps-attr-act"/g)?.length).toBe(2);
  });
});
