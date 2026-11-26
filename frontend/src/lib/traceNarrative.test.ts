// traceNarrative.test.ts — v0.10.2480: anlatım biçiminin ayrıştırıcısı ve
// sunucu verisinden kurulan satırlar (Akış, LOG boş durumları, Kanıt özeti).
import { describe, it, expect } from 'vitest';
import {
  TRACE_NARRATIVE_LABELS, extraTraceLogs, matchTraceLabel, parseTraceNarrative, traceEvidenceSummary,
  traceFlowLine, traceHeadStatus, traceLogEmpty, visibleTraceLogs,
} from './traceNarrative';
import type { TraceEvidenceView } from './types';

const KARAR = "orders-api'nin POST /orders isteği HTTP 400 ile bitti. Bu bir arıza değil, business hata.";

describe('parseTraceNarrative', () => {
  it.each([
    ['temiz', `Business hata\nKARAR: ${KARAR}\nSonraki adım: Tutar alanına bakın.`, 'Business hata', KARAR, 'Tutar alanına bakın.'],
    ['etiket yok', `KARAR: ${KARAR}\nSonraki adım: Bakın.`, null, KARAR, 'Bakın.'],
    ['fazla markdown', `**Business hata**\n\n**KARAR:** ${KARAR}\n\n- **Sonraki adım:** Bakın.`, 'Business hata', KARAR, 'Bakın.'],
    ['TÜR öneki + parantez', `TÜR: Başarılı (yavaş değil)\nKARAR: Hata yok.`, 'Başarılı', 'Hata yok.', ''],
    ['sunucunun dış bekleme satırı atılır', `dış bağımlılık bekleme: rates.example.test 2.00 s\n\nYavaşlık\nKARAR: Dış çağrı yavaş.`, 'Yavaşlık', 'Dış çağrı yavaş.', ''],
    ['KARAR işaretsiz düz yazı', `Zaman aşımı\nshipping-api 5 s içinde yanıt vermedi.\nSonraki adım: Bakın.`, 'Zaman aşımı', 'shipping-api 5 s içinde yanıt vermedi.', 'Bakın.'],
    ['boş KARAR', `Business hata\nKARAR:\nSonraki adım: Bakın.`, 'Business hata', '', 'Bakın.'],
    ['boş metin', '', null, '', ''],
  ])('%s', (_n, text, label, verdict, next) => {
    const got = parseTraceNarrative(text);
    expect(got.label).toBe(label);
    expect(got.verdict).toBe(verdict);
    expect(got.next).toBe(next);
  });

  it('bilinmeyen etiket satırı etiket sayılmaz, KARAR yine bulunur', () => {
    const got = parseTraceNarrative(`Kesinti\nKARAR: ${KARAR}`);
    expect(got.label).toBeNull();
    expect(got.verdict).toBe(KARAR);
  });

  it('akarken: yarım ilk satır çizilmez; KARAR akmaya başlayınca görünür', () => {
    expect(parseTraceNarrative('Busi', true).verdict).toBe('');
    const p = parseTraceNarrative("Business hata\nKARAR: orders-api'nin POST", true);
    expect(p.label).toBe('Business hata');
    expect(p.verdict).toBe("orders-api'nin POST");
  });

  it('etiket kümesi Go ile aynı (8) ve eski taslak adı eşlenir', () => {
    expect(TRACE_NARRATIVE_LABELS).toHaveLength(8);
    expect(matchTraceLabel('İş kuralı reddi')).toBe('Business hata');
    expect(matchTraceLabel('### bağımlılık hatası.')).toBe('Bağımlılık hatası');
  });
});

const base: TraceEvidenceView = {
  spans: 3, totalSpans: 3, logRows: 4, logState: 'rows', oracleRows: 1, logsVisible: 1, fallback: 'Hata kaynağı: …',
  rootMs: 82,
  chain: [
    { spanId: 'c1', service: 'orders-bff', kind: 'CLIENT', call: 'POST customer-api → /customers/start', status: 400, failed: true, durMs: 77, p50Ms: 30 },
    { spanId: 'r1', service: 'orders-bff', kind: 'SERVER', call: 'POST /start', status: 400, failed: true, durMs: 82 },
  ],
  logs: [
    { tsNs: 1, level: 'ERROR', service: 'orders-bff', message: 'customer rejected', count: 2, corrId: 'requestId=req-0001' },
    { tsNs: 2, level: 'WARN', service: 'orders-bff', message: 'retry skipped' },
  ],
};

describe('Akış / LOG / Kanıt', () => {
  it('Akış: zincir tek satır, istemciye kök kodu', () => {
    expect(traceFlowLine(base)).toBe('orders-bff → POST customer-api → /customers/start · 400 · 77 ms → istemciye 400');
    expect(traceHeadStatus(base)).toBe(400);
  });

  it('Akış: yavaş başarılı trace kritik yolun en büyük payı', () => {
    const v: TraceEvidenceView = { ...base, chain: undefined, critical: { spanId: 'c1', service: 'orders-api', kind: 'CLIENT', target: 'https://rates.example.test/v1/fx', durMs: 2000, sharePct: 95, mode: 'external' } };
    expect(traceFlowLine(v)).toBe("orders-api · https://rates.example.test/v1/fx · 2.00 s · kök süresinin %95'i · dış bağımlılık beklemesi");
  });

  it('görünen log satırı Kanıt özetinde ikinci kez sayılmaz', () => {
    expect(visibleTraceLogs(base)).toHaveLength(1);
    expect(extraTraceLogs(base).map(l => l.message)).toEqual(['retry skipped']);
    // 4 satır − görünen desenin kapsadığı 2 kayıt = 2 ek satır
    expect(traceEvidenceSummary(base)).toEqual({ spans: 3, hops: 2, extraLogs: 2, oracle: 1, stack: false });
  });

  it('LOG boş durumları birbirinden ayrı', () => {
    const empty = { ...base, logs: [], logsVisible: 0 };
    expect(traceLogEmpty({ ...empty, logState: 'none', logRows: 0 })).toBe('none');
    expect(traceLogEmpty({ ...empty, logState: 'failed', logReason: 'timeout', logRows: 0 })).toBe('failed');
    expect(traceLogEmpty({ ...empty, logState: 'failed', logReason: 'nostore', logRows: 0 })).toBe('nostore');
    expect(traceLogEmpty({ ...empty, logState: 'rows', logRows: 5 })).toBe('quiet');
    expect(traceLogEmpty(base)).toBeNull();
  });
});
