// v0.10.1499 — /logs görünüm anahtarı (Logs | Desenler | Şablonlar) saf kuralları.
import { describe, it, expect } from 'vitest';
import { parseLogsView, effectiveLogsView, writeLogsView, logsListEnabled, writeTraceView, readTraceViewTarget } from './logsView';

// v0.10.1889 (Log Search 3.6) — birleşik görünüm `panel=trace&trace=&th=`.
describe('logsView trace', () => {
  const id = '0af7651916cd43dd8448eb211c80319c';
  it('panel=trace yalnız tanınan kimlikle', () => {
    expect(parseLogsView('trace', id)).toBe('trace');
    expect(parseLogsView('trace', `${id.slice(0, 8)}-${id.slice(8, 12)}-${id.slice(12, 16)}-${id.slice(16, 20)}-${id.slice(20)}`)).toBe('trace');
    expect(parseLogsView('trace', 'nope')).toBe('logs');
    expect(parseLogsView('trace', null)).toBe('logs');
    expect(logsListEnabled('trace', false, false)).toBe(false);
  });
  it('açma: kimlik normalize, ipucu saniye, çekmece kapanır, süzgeç dokunulmaz', () => {
    const prev = new URLSearchParams('q=boom&doc=1.2&docsvc=orders-api&range=1h');
    const p = writeTraceView(prev, `0x${id.toUpperCase()}`, 1_759_495_079_999_000_000)!;
    expect(p.get('panel')).toBe('trace');
    expect(p.get('trace')).toBe(id);
    expect(p.get('th')).toBe('1759495079');
    expect(p.has('doc') || p.has('docsvc')).toBe(false);
    expect(p.get('q')).toBe('boom');
    expect(p.has('traceId')).toBe(false);
    expect(readTraceViewTarget(p)).toEqual({ id, hintSec: 1759495079 });
    expect(writeTraceView(prev, 'not-a-trace')).toBeNull();
    expect(writeTraceView(prev, id)!.has('th')).toBe(false);
  });
  it('trace dışı görünüme yazım hedefi düşürür', () => {
    const p = new URLSearchParams(`panel=trace&trace=${id}&th=1&q=x`);
    expect(writeLogsView(p, 'logs').toString()).toBe('q=x');
    expect(readTraceViewTarget(new URLSearchParams(`trace=${id}`))).toBeNull();
  });
});

describe('logsView', () => {
  it('?panel= → görünüm; yok/bilinmeyen = logs', () => {
    expect(parseLogsView(null)).toBe('logs');
    expect(parseLogsView('')).toBe('logs');
    expect(parseLogsView('bogus')).toBe('logs');
    expect(parseLogsView('patterns')).toBe('patterns');
    expect(parseLogsView('templates')).toBe('templates');
  });
  it('canlı kuyruk Logs görünümünü zorlar', () => {
    expect(effectiveLogsView('patterns', true)).toBe('logs');
    expect(effectiveLogsView('templates', false)).toBe('templates');
  });
  it('yazma: logs paramı siler, diğerleri yazar, yabancı paramlar kalır', () => {
    const prev = new URLSearchParams('q=boom&panel=patterns&range=1h');
    expect(writeLogsView(prev, 'logs').toString()).toBe('q=boom&range=1h');
    expect(writeLogsView(prev, 'templates').toString()).toBe('q=boom&panel=templates&range=1h');
    expect(prev.get('panel')).toBe('patterns'); // girdi değişmez
    expect(parseLogsView(writeLogsView(prev, 'patterns').get('panel'))).toBe('patterns');
  });
  it('liste sorgusu yalnız Logs görünümünde, canlı değil, takım kapısı kapalıyken', () => {
    expect(logsListEnabled('logs', false, false)).toBe(true);
    expect(logsListEnabled('patterns', false, false)).toBe(false);
    expect(logsListEnabled('templates', false, false)).toBe(false);
    expect(logsListEnabled('logs', true, false)).toBe(false);
    expect(logsListEnabled('logs', false, true)).toBe(false);
  });
});
