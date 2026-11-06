// logException — v0.10.1882 (Log Search Faz 3 dilim 3.3).
import { describe, it, expect } from 'vitest';
import { exceptionBadgeText, exceptionClassLabel, exceptionStackText, foldStack, shortExceptionType, stackLines } from './logException';

const JAVA = [
  'java.lang.OutOfMemoryError: Java heap space',
  '\tat com.acme.orders.cart.CartSerializer.write(CartSerializer.java:118)',
  '\tat org.jboss.resteasy.core.MethodInjectorImpl.invoke(MethodInjectorImpl.java:170)',
  '\tat io.undertow.servlet.handlers.ServletHandler.handleRequest(ServletHandler.java:74)',
  'Caused by: java.lang.OutOfMemoryError: Java heap space',
  '\tat com.acme.orders.cart.CartBuffer.grow(CartBuffer.java:41)',
].join('\n');

describe('logException', () => {
  it('rozet metni: tür + sınıf, kritikte ▲', () => {
    expect(exceptionBadgeText({ type: 'java.lang.OutOfMemoryError', class: 'resource', critical: true, frames: 4, appFrames: 2 }))
      .toBe('▲ OutOfMemoryError · kaynak tükenmesi');
    expect(exceptionBadgeText({ type: 'System.InvalidOperationException', frames: 3, appFrames: 1 })).toBe('InvalidOperationException');
    expect(exceptionClassLabel('bilinmeyen')).toBe('');
    expect(shortExceptionType('NoDots')).toBe('NoDots');
  });
  it('stack metni: öznitelik, yoksa gövde', () => {
    const e = { type: 'X', frames: 1, appFrames: 0, stackField: 'error.stack_trace' };
    expect(exceptionStackText({ body: 'b', attributes: { 'error.stack_trace': 'S' } }, e)).toBe('S');
    expect(exceptionStackText({ body: 'b', attributes: {} }, e)).toBe('b');
  });
  it('katlama: başlıklar + uygulama satırları görünür, framework sayılır', () => {
    const lines = stackLines(JAVA, [1, 5]);
    expect(lines.filter(l => l.head).map(l => l.text.slice(0, 18))).toEqual(['java.lang.OutOfMem', 'Caused by: java.la']);
    const f = foldStack(lines);
    expect(f.shown.map(l => l.text.trim().slice(0, 14))).toEqual(['java.lang.OutO', 'at com.acme.or', 'Caused by: jav', 'at com.acme.or']);
    expect(f.hidden).toBe(2);
  });
});
