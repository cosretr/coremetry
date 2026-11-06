// logsShareView.test.ts — v0.10.1899 (Log Search Faz 5 dilim 5.2): paylaşılan
// /logs linki alıcıda göndericinin tablosunu açar — alıcının yerel tercihi
// (kolonlar, satır yüksekliği, örnek boyu, yapışkan aralık) farklı olsa bile.
import { describe, it, expect } from 'vitest';
import { logsShareHref, logsShareViewParams, type LogsShareView } from './logsShareView';
import { readLogsDisplay, LOGS_DEFAULT_DISPLAY } from './logsDisplay';
import { readLogsSample } from './logsSample';
import type { LogsDisplay } from './types';

const NOW = 1_759_500_000_000;
const sender: LogsShareView = {
  display: { rowLines: 3, headerLines: 2, density: 'compact', pageSize: 250 },
  sample: 1200,
  cols: ['timestamp', 'severity', 'service', 'body', 'k8s.pod.name'],
  range: { preset: '1h' },
};

describe('logsShareViewParams', () => {
  it('etkin görünümün tamamı', () => {
    expect(logsShareViewParams(sender)).toEqual({
      rh: '3', hh: '2', den: 'compact', pp: '250', smp: '1200', cols: 'timestamp,severity,service,body,k8s.pod.name', range: '1h',
    });
  });
});

describe('logsShareHref', () => {
  it('URL\'de olmayan anahtarlar eklenir, olanların baytları değişmez, göreli aralık sabitlenir', () => {
    const href = 'https://apm.acme.test/logs?service=orders-api&q=level%3Aerror&rh=1#frag';
    const out = logsShareHref(href, NOW, sender);
    expect(out.startsWith('https://apm.acme.test/logs?service=orders-api&q=level%3Aerror&rh=1&')).toBe(true);
    expect(out.endsWith('#frag')).toBe(true);
    const p = new URL(out).searchParams;
    expect(p.get('rh')).toBe('1'); // URL kazanır (gönderici zaten bunu görüyor)
    expect(p.get('den')).toBe('compact');
    expect(p.get('smp')).toBe('1200');
    expect(p.get('range')).toBe(`custom:${NOW - 3600_000}-${NOW}`);
  });
  it('mutlak aralık ve tam URL olduğu gibi kalır', () => {
    const href = 'https://apm.acme.test/logs?range=custom%3A1-2&rh=3&hh=2&den=compact&pp=250&smp=1200&cols=a%2Cb';
    expect(logsShareHref(href, NOW, { ...sender, cols: ['a', 'b'] })).toBe(href);
  });
  it('sorgusuz href', () => {
    const out = logsShareHref('https://apm.acme.test/logs', NOW, { ...sender, range: undefined });
    expect(new URL(out).searchParams.get('pp')).toBe('250');
  });
  it('alıcıda aynı tablo: farklı yerel tercihle açan kullanıcı göndericinin görünümünü okur', () => {
    const out = new URL(logsShareHref('https://apm.acme.test/logs?service=orders-api', NOW, sender)).searchParams;
    // alıcının tercihleri tamamen farklı
    const receiverBase: LogsDisplay = { rowLines: 'auto', headerLines: 1, density: 'normal', pageSize: 50 };
    expect(readLogsDisplay(out, receiverBase).display).toEqual(sender.display);
    expect(readLogsDisplay(out, LOGS_DEFAULT_DISPLAY).display).toEqual(sender.display);
    expect(readLogsSample(out.get('smp'), 2000).sample).toBe(1200);
    expect(out.get('cols')!.split(',')).toEqual(sender.cols);
    expect(out.get('range')).toMatch(/^custom:\d+-\d+$/);
  });
});
