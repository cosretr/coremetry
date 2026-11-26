// TraceNarrativeBody.test.tsx — v0.10.2480: anlatım biçiminin onaylı düzeni —
// TÜR satırı, KARAR, Akış, LOG (+ "Trace'in loglarını aç", dürüst boş
// durumlar), Sonraki adım, KAPALI Kanıt, dipnot; KARAR boşsa deterministik özet;
// saatler izleyicinin diliminde (Window çipiyle aynı kural).
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { TraceNarrativeBody } from './TraceNarrativeBody';
import { formatTraceClock, formatTraceWindow } from '@/lib/traceAiContext';
import type { TraceEvidenceView } from '@/lib/types';

const T = '4bf92f3577b34da6a3ce929d0e0e4736';
const TS = Date.UTC(2026, 10, 26, 17, 37, 2, 41) * 1e6; // 17:37:02.041 UTC

const VIEW: TraceEvidenceView = {
  spans: 3, totalSpans: 3, logRows: 4, logState: 'rows', oracleRows: 1, logsVisible: 1, rootMs: 82,
  fallback: 'Hata kaynağı: orders-bff · POST customer-api → /customers/start — HTTP 400.',
  chain: [
    { spanId: 'c1', service: 'orders-bff', kind: 'CLIENT', call: 'POST customer-api → /customers/start', status: 400, failed: true, durMs: 77, p50Ms: 30 },
    { spanId: 'r1', service: 'orders-bff', kind: 'SERVER', call: 'POST /start', status: 400, failed: true, durMs: 82 },
  ],
  logs: [
    { tsNs: TS, level: 'ERROR', service: 'orders-bff', message: 'customer rejected: birth date missing', count: 2, corrId: 'requestId=req-0001' },
    { tsNs: TS + 1e6, level: 'WARN', service: 'orders-bff', message: 'retry skipped' },
  ],
  oracle: [{ tsNs: TS, operation: 'CUST_START', errorCode: 'E-100', message: 'validation' }],
  frame: { exType: 'java.lang.IllegalStateException', frame: 'com.example.orders.Start.run(Start.java:42)' },
};

const TEXT = "Business hata\nKARAR: orders-bff isteği 82 ms içinde HTTP 400 ile bitti. Bu bir arıza değil, business hata.\nSonraki adım: Doğum tarihi alanına bakın.";

const html = (text: string, view: TraceEvidenceView | null, busy = false) => renderToStaticMarkup(
  <MemoryRouter><TraceNarrativeBody text={text} busy={busy} view={view} traceId={T} /></MemoryRouter>,
);

describe('TraceNarrativeBody', () => {
  it('onaylı düzen sırası; Kanıt kapalı; görünen log Kanıt özetinde yok', () => {
    const h = html(TEXT, VIEW);
    const order = ['data-tn-head', 'data-tn-verdict', 'data-tn-flow', 'data-tn-logrow', 'data-tn-next', 'data-tn-evidence', 'data-tn-footer'].map(a => h.indexOf(a));
    expect(order.every(i => i >= 0)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(h).toContain('HTTP 400');
    expect(h).toContain('orders-bff → POST customer-api → /customers/start · 400 · 77 ms → 400 to client'); // test ortamı EN kataloğu
    expect(h).toMatch(/<details class="cm-md-details" data-tn-evidence="">/); // open yok
    expect(h).toContain(`href="/trace?id=${T}&amp;tab=logs"`);
    // özet: 4 satır − görünen desenin 2 kaydı = 2 ek; ilk log Kanıt'ta tekrar listelenmez
    expect(h).toMatch(/3 spans · 2 hops · 2 more log lines · 1 Oracle rows · stack trace/);
    expect(h.split('customer rejected').length - 1).toBe(1);
    expect(h).toContain('retry skipped');
  });

  it("KARAR boşsa deterministik özet 'AI yazmadı' notuyla", () => {
    const h = html('Business hata\nKARAR:\n', VIEW);
    expect(h).toContain(VIEW.fallback);
    expect(h).toContain('data-tn-fallback');
  });

  it('kanıt gelmeden: KARAR akar, Akış/LOG yerine yükleniyor', () => {
    const h = html("Business hata\nKARAR: orders-bff", null, true);
    expect(h).toContain('orders-bff');
    expect(h).not.toContain('data-tn-flow');
    expect(h).not.toContain('data-tn-footer');
  });

  it.each([
    ['none', { logState: 'none' as const, logRows: 0 }],
    ['failed', { logState: 'failed' as const, logReason: 'timeout' as const, logRows: 0 }],
    ['nostore', { logState: 'failed' as const, logReason: 'nostore' as const, logRows: 0 }],
  ])('LOG boş durumu %s satırı gizlemez', (state, patch) => {
    const h = html(TEXT, { ...VIEW, ...patch, logs: [], logsVisible: 0 });
    expect(h).toContain(`data-tn-log-empty="${state}"`);
    expect(h).toContain('/trace?id=');
  });

  it('saat izleyicinin diliminde (Window çipiyle aynı)', () => {
    expect(formatTraceClock(TS, 'Europe/Istanbul')).toEqual({ time: '20:37:02.041', tzLabel: 'Europe/Istanbul' });
    expect(formatTraceWindow(TS, TS, 'Europe/Istanbul')?.text).toContain('20:37:02.041');
    expect(formatTraceClock(TS, 'UTC')?.time).toBe('17:37:02.041');
  });
});
