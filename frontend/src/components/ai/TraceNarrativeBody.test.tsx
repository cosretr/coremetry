// TraceNarrativeBody.test.tsx — v0.10.2480; v0.10.2483 (operatör onaylı
// düzen): TÜR satırı → yaygınlık → iki paragraflık KARAR (log saatleri birebir
// eşleşince Loglar sekmesine bağlantı) + log notu → Sonraki adım → AÇIK hata
// zinciri (yoksa kritik yol) → KAPALI Diğer kanıt → dipnot. Akış / LOG satırı yok.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { TraceNarrativeBody } from './TraceNarrativeBody';
import { formatTraceClock, formatTraceWindow } from '@/lib/traceAiContext';
import type { TraceEvidenceView } from '@/lib/types';

const T = '4bf92f3577b34da6a3ce929d0e0e4736';
const TS = Date.UTC(2026, 10, 26, 17, 37, 2, 44) * 1e6; // 17:37:02.044 UTC
const C1 = formatTraceClock(TS)!.time; // test ortamının dilimi — UI ile sunucu aynı kuralı izler
const C2 = formatTraceClock(TS + 56e6)!.time;

const VIEW: TraceEvidenceView = {
  spans: 3, totalSpans: 3, logRows: 4, logState: 'rows', oracleRows: 1, rootMs: 82,
  fallback: 'Hata kaynağı: orders-bff · POST customer-api → /customers/start — HTTP 400.',
  chain: [
    { spanId: 'c1', service: 'orders-bff', kind: 'CLIENT', call: 'POST customer-api → /customers/start', status: 400, error: 'ValidationException: customerNo must be set', failed: true, durMs: 77, p50Ms: 30 },
    { spanId: 'r1', service: 'orders-bff', kind: 'SERVER', call: 'POST /start', status: 400, failed: true, durMs: 82 },
  ],
  critical: { spanId: 'c1', service: 'orders-bff', target: 'customer-api', durMs: 77, sharePct: 94, mode: 'dependent' },
  logIndex: [
    { tsNs: TS, service: 'orders-bff', level: 'INFO', key: 'calling customer-api customerNo=0' },
    { tsNs: TS + 56e6, service: 'customer-api', level: 'WARN', key: 'customerNo must be set' },
  ],
  logsReviewed: { total: 4, sent: 4 },
  prevalence: { kind: 'error', service: 'orders-bff', op: 'POST customer-api', fromNs: TS - 3600e9, toNs: TS, windowMin: 60, calls: 1840, errors: 214, wide: true, endsAtTrace: true },
  oracle: [{ tsNs: TS, operation: 'CUST_START', errorCode: 'E-100', message: 'validation' }],
  frame: { exType: 'java.lang.IllegalStateException', frame: 'com.example.orders.Start.run(Start.java:42)' },
};

const TEXT = `Business hata\nKARAR: orders-bff isteği 82 ms içinde HTTP 400 ile bitti. Bu bir arıza değil, business hata.\n\n${C1}'te orders-bff çağrıyı customerNo=0 ile yapıyor, ${C2}'de customer-api isteği reddediyor; 23:59:59.999'da başka bir şey.\nSonraki adım: Müşteri numarası alanına bakın.`;

const html = (text: string, view: TraceEvidenceView | null, busy = false) => renderToStaticMarkup(
  <MemoryRouter><TraceNarrativeBody text={text} busy={busy} view={view} traceId={T} /></MemoryRouter>,
);

describe('TraceNarrativeBody', () => {
  it('onaylı düzen sırası; zincir açık; Diğer kanıt kapalı; Akış / LOG satırı yok', () => {
    const h = html(TEXT, VIEW);
    const order = ['data-tn-head', 'data-tn-prevalence', 'data-tn-verdict', 'data-tn-logsnote', 'data-tn-next', 'data-tn-chain', 'data-tn-evidence', 'data-tn-footer'].map(a => h.indexOf(a));
    expect(order.every(i => i >= 0)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(h).not.toContain('data-tn-flow');
    expect(h).not.toContain('data-tn-logrow');
    expect(h).toMatch(/<details class="cm-md-details" data-tn-evidence="">/); // open yok
    expect(h).not.toMatch(/<details[^>]*data-tn-chain/); // zincir katlanır değil
    expect(h).toContain('Verdict is AI-generated · error chain is server data'); // test ortamı EN kataloğu
  });

  it('KARAR iki paragraf; birebir eşleşen log saati Loglar sekmesine bağlı, eşleşmeyen düz metin', () => {
    const h = html(TEXT, VIEW);
    expect(h).toContain('data-tn-para="1"');
    expect(h).toContain('data-tn-para="2"');
    const logs = `href="/trace?id=${T}&amp;tab=logs" data-discover="true"`;
    expect(h).toContain(`${logs}>${C1}</a>`);
    expect(h).toContain(`${logs}>${C2}</a>`);
    expect(h.split('data-tn-clock').length - 1).toBe(2);
    expect(h).not.toMatch(/data-tn-clock[^>]*>23:59:59\.999/);
  });

  it('log notu: hepsi / bir kısmı incelendi + "Loglar sekmesinde aç"', () => {
    expect(html(TEXT, VIEW)).toMatch(/data-tn-logsnote="all"[^]*All 4 log lines reviewed[^]*Open in the Logs tab/);
    expect(html(TEXT, { ...VIEW, logsReviewed: { total: 250, sent: 120 } })).toContain('120 of 250 log lines reviewed');
    expect(html(TEXT, { ...VIEW, logsReviewed: undefined, logIndex: undefined, logState: 'none', logRows: 0 })).toContain('No logs found for this trace');
    expect(html(TEXT, { ...VIEW, logsReviewed: undefined, logIndex: undefined, logState: 'failed', logReason: 'timeout', logRows: 0 })).toContain('Logs could not be read');
  });

  it('hata zinciri: numaralı, kaynak işaretli, not satırları sunucu verisinden', () => {
    const h = html(TEXT, VIEW);
    expect(h).toContain('data-tn-hop="1"');
    expect(h).toContain('data-tn-source=""');
    expect(h).toContain('ValidationException: customerNo must be set');
    expect(h).toContain('24 h median 30 ms, this call ×2.6');
    expect(h).toContain('root request');
  });

  it('zincir yoksa kritik yol aynı biçimde açık; Diğer kanıt kritik yolu tekrar etmez', () => {
    const v: TraceEvidenceView = {
      ...VIEW, chain: undefined, prevalence: undefined,
      critical: { spanId: 'c1', service: 'orders-api', target: 'https://rates.example.test/v1/fx', durMs: 2000, sharePct: 95, mode: 'external', p50Ms: 180 },
      critPath: [
        { spanId: 'c1', service: 'orders-api', kind: 'CLIENT', call: 'https://rates.example.test/v1/fx', durMs: 2000, sharePct: 95, p50Ms: 180 },
        { spanId: 'r1', service: 'orders-api', kind: 'SERVER', call: 'GET /orders', status: 200, durMs: 2100, root: true },
      ],
    };
    const h = html(TEXT, v);
    expect(h).toContain('data-tn-critpath');
    expect(h).toContain('95% of root duration · 24 h median 180 ms');
    expect(h).not.toContain('data-tn-chain');
    expect(h).not.toMatch(/data-tn-evidence[^]*Critical path ·/);
  });

  it('yaygınlık: sayı süzülmüş görünüme bağlı, yaygınsa renkli; tekil düz; yoksa satır yok', () => {
    const h = html(TEXT, VIEW);
    expect(h).toMatch(/data-tn-prevalence="wide"/);
    expect(h).toContain('This error was seen <a');
    expect(h).toContain('>214</a> times in the hour before the trace (in 1,840 calls)');
    expect(h).toMatch(/href="\/traces\?service=orders-bff[^"]*hasError=true/);
    const one = html(TEXT, { ...VIEW, prevalence: { ...VIEW.prevalence!, errors: 1, wide: false, endsAtTrace: false } });
    expect(one).toContain('data-tn-prevalence="narrow"');
    expect(one).toContain('This error was seen only in this request in the last hour');
    expect(html(TEXT, { ...VIEW, prevalence: undefined })).not.toContain('data-tn-prevalence');
    expect(html(TEXT, { ...VIEW, prevalence: { ...VIEW.prevalence!, errors: 0 } })).not.toContain('data-tn-prevalence');
  });

  it("KARAR boşsa deterministik özet 'AI yazmadı' notuyla", () => {
    const h = html('Business hata\nKARAR:\n', VIEW);
    expect(h).toContain(VIEW.fallback);
    expect(h).toContain('data-tn-fallback');
  });

  it('kanıt gelmeden: KARAR akar, zincir yerine yükleniyor', () => {
    const h = html("Business hata\nKARAR: orders-bff", null, true);
    expect(h).toContain('orders-bff');
    expect(h).not.toContain('data-tn-chain');
    expect(h).not.toContain('data-tn-footer');
  });

  it('v0.10.2482 — kod turu: Kod bulgusu KARAR ile Sonraki adım arasında; kod künyesi (içeriksiz) Diğer kanıt\'ta', () => {
    const code = { repo: 'orders', branch: 'main', files: [{ path: 'src/Start.java', fromLine: 37, toLine: 47, line: 42, url: 'https://code.example/s' }] };
    const h = renderToStaticMarkup(
      <MemoryRouter><TraceNarrativeBody text={TEXT.replace('\nSonraki adım', '\nKod bulgusu: Start.java:42 — numara boş geçiyor.\nSonraki adım')}
        busy={false} view={VIEW} traceId={T} code={code} /></MemoryRouter>,
    );
    const order = ['data-tn-verdict', 'data-tn-code=', 'data-tn-next', 'data-tn-evidence', 'data-tn-codesource'].map(a => h.indexOf(a));
    expect(order.every(i => i >= 0)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(h).toContain('src/Start.java:37-47');
    expect(h).toContain('hata satırı 42');
    expect(h).not.toContain('<pre');
    expect(h).toContain('Code finding');
    expect(html(TEXT, VIEW)).not.toContain('data-tn-code');
  });

  it('saat izleyicinin diliminde (Window çipiyle aynı)', () => {
    expect(formatTraceClock(TS, 'Europe/Istanbul')).toEqual({ time: '20:37:02.044', tzLabel: 'Europe/Istanbul' });
    expect(formatTraceWindow(TS, TS, 'Europe/Istanbul')?.text).toContain('20:37:02.044');
    expect(formatTraceClock(TS, 'UTC')?.time).toBe('17:37:02.044');
  });
});
