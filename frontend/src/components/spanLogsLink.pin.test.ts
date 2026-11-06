// v0.10.1890 (Log Search 3.8, audit §4 "Ters yön") — span detayındaki log
// bağlantısı trace_id'yi serbest aramaya (q=) değil traceId + spanId
// süzgeçlerine taşır: "bu span'in logları". Üretici logsHref (link kapısı).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { logsHref } from '@/lib/logsUrl';

describe('SpanDetail → logs bağlantısı', () => {
  const src = readFileSync(resolve(__dirname, 'SpanDetail.tsx'), 'utf8');
  it('traceId + spanId süzgeci; q=traceId yok', () => {
    expect(src).toContain('logsHref({ window: logsLinkRange || null, traceId: span.traceId, spanId: span.spanId })');
    expect(src).not.toContain('q: span.traceId');
  });
  it('üretici iki paramı yazar', () => {
    const p = new URLSearchParams(logsHref({ window: null, traceId: 'a'.repeat(32), spanId: 'b'.repeat(16) }).split('?')[1]);
    expect(p.get('traceId')).toBe('a'.repeat(32));
    expect(p.get('spanId')).toBe('b'.repeat(16));
    expect(p.has('q')).toBe(false);
  });
});
