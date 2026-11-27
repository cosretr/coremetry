// traceNarrative.test.ts — v0.10.2480: anlatım biçiminin ayrıştırıcısı ve
// sunucu verisinden kurulan satırlar. v0.10.2483: iki paragraflık KARAR, log
// saati eşleşmesi, log notu, zincir notları, yaygınlık, Diğer kanıt.
import { describe, it, expect } from 'vitest';
import {
  TRACE_NARRATIVE_LABELS, hasOtherEvidence, matchTraceLabel, parseTraceNarrative, splitLogClocks, traceHeadStatus,
  traceHopNote, traceLogClockSet, traceLogsNote, traceOtherEvidence, tracePrevalenceKey,
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

  it('v0.10.2482 — Kod bulgusu KARAR ile Sonraki adım arasından ayrılır (Go ParseTraceNarrative aynası)', () => {
    const got = parseTraceNarrative(`Business hata\nKARAR: ${KARAR}\n**Kod bulgusu:** OrderMapper.java:42 — tutar boş geçiyor.\nSonraki adım: Bakın.`);
    expect(got.verdict).toBe(KARAR);
    expect(got.code).toBe('OrderMapper.java:42 — tutar boş geçiyor.');
    expect(got.next).toBe('Bakın.');
    expect(parseTraceNarrative(`Business hata\nKARAR: ${KARAR}`).code).toBe('');
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

describe('v0.10.2483 — iki paragraflık KARAR', () => {
  it('iki paragraf korunur; tek paragraf geçerli; fazlası ikinciye katılır (Go aynası)', () => {
    const two = parseTraceNarrative("Business hata\nKARAR: Bir.\nİki.\n\n20:37:02.044'te orders-bff reddediyor.\nSonraki adım: x");
    expect(two.paras).toEqual(['Bir. İki.', "20:37:02.044'te orders-bff reddediyor."]);
    expect(two.verdict).toBe("Bir. İki.\n\n20:37:02.044'te orders-bff reddediyor.");
    expect(parseTraceNarrative('Başarılı\nKARAR: Tek.\nSonraki adım: y').paras).toEqual(['Tek.']);
    expect(parseTraceNarrative('Belirsiz\nKARAR: A.\n\nB.\n \nC.').paras).toEqual(['A.', 'B. C.']);
  });
});

const TS = Date.UTC(2026, 10, 26, 17, 37, 2, 44) * 1e6; // 20:37:02.044 Europe/Istanbul

const base: TraceEvidenceView = {
  spans: 3, totalSpans: 3, logRows: 4, logState: 'rows', oracleRows: 1, fallback: 'Hata kaynağı: …',
  rootMs: 82,
  chain: [
    { spanId: 'c1', service: 'orders-bff', kind: 'CLIENT', call: 'POST customer-api → /customers/start', status: 400, error: 'ValidationException: customerNo must be set', failed: true, durMs: 77, p50Ms: 30 },
    { spanId: 'm1', service: 'orders-bff', kind: 'INTERNAL', call: 'StartFlow.run', failed: true, durMs: 79 },
    { spanId: 'r1', service: 'orders-bff', kind: 'SERVER', call: 'POST /start', status: 400, failed: true, durMs: 82 },
  ],
  logIndex: [
    { tsNs: TS, service: 'orders-bff', level: 'INFO', key: 'calling customer-api customerNo=0' },
    { tsNs: TS + 56e6, lastTsNs: TS + 60e6, service: 'customer-api', level: 'WARN', key: 'customerNo must be set' },
  ],
  logsReviewed: { total: 4, sent: 4 },
};

describe('KARAR log saatleri', () => {
  const clocks = traceLogClockSet(base, 'Europe/Istanbul');
  it('küme: ilk ve son saat, izleyicinin diliminde (Window çipiyle aynı biçim)', () => {
    expect([...clocks].sort()).toEqual(['20:37:02.044', '20:37:02.100', '20:37:02.104']);
    expect(traceLogClockSet(base, 'UTC').has('17:37:02.044')).toBe(true);
  });

  it('yalnız BİREBİR eşleşen saat bağlantı olur; en yakın tahmin yok', () => {
    const segs = splitLogClocks("20:37:02.044'te BFF çağırıyor, 20:37:02.045'te değil; 20:37:02.100'de servis reddediyor.", clocks);
    expect(segs.filter(s => s.clock).map(s => s.text)).toEqual(['20:37:02.044', '20:37:02.100']);
    expect(segs.map(s => s.text).join('')).toBe("20:37:02.044'te BFF çağırıyor, 20:37:02.045'te değil; 20:37:02.100'de servis reddediyor.");
    expect(splitLogClocks('17:37:02.044 UTC', clocks)).toEqual([{ text: '17:37:02.044 UTC' }]);
    expect(splitLogClocks('x', new Set())).toEqual([{ text: 'x' }]);
  });
});

describe('log notu', () => {
  it('hepsi / bir kısmı / yok / okunamadı / depo yok', () => {
    expect(traceLogsNote(base)).toEqual({ kind: 'all', total: 4 });
    expect(traceLogsNote({ ...base, logsReviewed: { total: 250, sent: 120 } })).toEqual({ kind: 'some', total: 250, sent: 120 });
    expect(traceLogsNote({ ...base, logsReviewed: undefined, logState: 'none', logRows: 0 })).toEqual({ kind: 'none' });
    expect(traceLogsNote({ ...base, logsReviewed: undefined, logState: 'failed', logReason: 'timeout', logRows: 0 })).toEqual({ kind: 'failed' });
    expect(traceLogsNote({ ...base, logsReviewed: undefined, logState: 'failed', logReason: 'nostore', logRows: 0 })).toEqual({ kind: 'nostore' });
  });
});

describe('hata zinciri notları', () => {
  it('kaynak: exception + 24 sa ortancası; ara halka notsuz; son halka kök istek', () => {
    const ch = base.chain!;
    expect(traceHopNote(ch[0], 0, 3)).toEqual({ source: 'ValidationException: customerNo must be set', p50Ms: 30, ratio: 77 / 30 });
    expect(traceHopNote(ch[1], 1, 3)).toBeNull();
    expect(traceHopNote(ch[2], 2, 3)).toEqual({ root: true });
    expect(traceHopNote({ ...ch[0], error: undefined, p50Ms: undefined }, 0, 1)).toBeNull();
    expect(traceHeadStatus(base)).toBe(400);
  });
});

describe('yaygınlık', () => {
  const p = { kind: 'error' as const, service: 'customer-api', op: 'POST /customers/start', fromNs: 1, toNs: 2, windowMin: 60, calls: 1840, wide: false, endsAtTrace: true };
  it('tür ve sayıya göre anahtar; sıfır gösterilmez', () => {
    expect(tracePrevalenceKey({ ...p, errors: 1 })).toBe('errOne');
    expect(tracePrevalenceKey({ ...p, errors: 214, wide: true })).toBe('errMany');
    expect(tracePrevalenceKey({ ...p, errors: 0 })).toBeNull();
    expect(tracePrevalenceKey({ ...p, kind: 'op', over: 62, overMs: 800 })).toBe('opOver');
    expect(tracePrevalenceKey({ ...p, kind: 'op', p95Ms: 640 })).toBe('opPlain');
    expect(tracePrevalenceKey({ ...p, kind: 'op', calls: 0 })).toBeNull();
  });
});

describe('Diğer kanıt', () => {
  it('kritik yol yalnız zincir varken Diğer kanıt\'ta; kod dosyası da sayılır', () => {
    const crit = { spanId: 'c1', service: 'orders-bff', target: 'customer-api', durMs: 77, sharePct: 94, mode: 'dependent' as const };
    expect(traceOtherEvidence({ ...base, critical: crit }).critical).toBe(true);
    expect(traceOtherEvidence({ ...base, chain: undefined, critical: crit }).critical).toBe(false);
    const none = traceOtherEvidence({ ...base, oracle: undefined });
    expect(hasOtherEvidence(none)).toBe(false);
    expect(hasOtherEvidence(none, 1)).toBe(true);
  });
});
