import { describe, expect, it } from 'vitest';
import { parseInline } from './chatInline';

// v0.9.419 pinleri — v0.10.1137'de mdLite (HTML dizesi) yerini saf düğüm
// çözücüsüne (chatInline.parseInline) bıraktı; aynı sözleşme düğüm düzeyinde:
// 32-hex trace id'ler trace düğümü (href SALT hex'ten, ChatBubble traceHref
// ile kurar), sınırlar aynı, HTML hiçbir zaman işaret olarak çözülmez, kod +
// kalın davranışı değişmedi.
describe('satır içi çözücü — mdLite sözleşmesi', () => {
  const tid = 'a1b2c3d4e5f60718293a4b5c6d7e8f90';

  it('32-hex trace id → trace düğümü', () => {
    expect(parseInline(`trace ${tid} yavaş`)).toEqual([
      { t: 'text', v: 'trace ' }, { t: 'trace', id: tid }, { t: 'text', v: ' yavaş' },
    ]);
  });

  it('31 hex ya da hex-olmayan token linklenmez', () => {
    const traces = (s: string) => parseInline(s).filter(n => n.t === 'trace');
    expect(traces(tid.slice(0, 31))).toEqual([]);
    expect(traces('g'.repeat(32))).toEqual([]);
    // 33-hex: sınır içinde 32'lik alt-parça yakalanmamalı
    expect(traces(tid + '0')).toEqual([]);
  });

  it('HTML işaret olarak çözülmez — metin kalır (React kaçışı)', () => {
    const nodes = parseInline(`<img src=x onerror=alert(1)> ${tid}`);
    expect(nodes[0]).toEqual({ t: 'text', v: '<img src=x onerror=alert(1)> ' });
    expect(nodes[1]).toEqual({ t: 'trace', id: tid });
  });

  it('kod + kalın davranışı korunur', () => {
    expect(parseInline('a `k` **b**')).toEqual([
      { t: 'text', v: 'a ' }, { t: 'code', v: 'k' }, { t: 'text', v: ' ' },
      { t: 'strong', c: [{ t: 'text', v: 'b' }] },
    ]);
  });
});
