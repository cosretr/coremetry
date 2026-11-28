// @vitest-environment jsdom
//
// v0.10.1337 — ServiceReportCard render sözleşmesi: hüküm rozeti, iki statik
// operasyon tablosu, teknik hata satırı + /problems?exc= linki, trace linki
// (traceHref) + mono log satırı, log durum cümleleri (depo yok / okunamadı /
// log yok ayrı), okunamayan bölümler "yok" demez, link çipleri.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
import { ServiceReportCard } from './ServiceReportCard';
import { drillLogsNote, verdictBadgeClass } from './serviceReportCardView';
import type { ChatServiceReportPayload } from '@/lib/types';

const reportFixture = (): ChatServiceReportPayload => ({
  service: 'payment-service',
  rangeS: 3600,
  verdict: 'Sorunlu',
  reasons: ['hata oranı %7.20 (baseline %0.40)'],
  red: { spans: 5000, errorRate: 7.2, p99Ms: 1850, baseErrorRate: 0.4, baseP99Ms: 210 },
  ops: {
    slow: [{ name: 'POST /api/pay', calls: 4120, errors: 890, errorRate: 21.6, p99Ms: 2400, priorP99Ms: 300, ratio: 8, hasPrior: true }],
    erroring: [{ name: 'POST /api/pay', calls: 4120, errors: 890, errorRate: 21.6, p99Ms: 2400, hasPrior: true }],
  },
  exceptions: [{
    fingerprint: 'ora:1', type: 'COR-30518', message: 'PAYMENT_AUTH_REST', state: 'new', priority: 'P1', oracle: true, code: 'COR-30518',
    operation: 'PAYMENT_AUTH_REST', hourKnown: true, lastHour: 900, prevHour: 200, occurrences: 12345, firstSeen: 1, lastSeen: 2,
    href: '/problems?exc=ora%3A1', explainHref: '/problems?exc=ora%3A1&ai=exception',
  }],
  drills: [{
    traceId: '0af7651916cd43dd8448eb211c80319c', label: 'en yavaş', ok: true, text: "50.02 s'nin 50.0 s'i (%100) CLIENT çağrısında: POST auth-gateway",
    logsState: 'ok', logs: [{ ts: 1_790_000_000_000_000_000, severity: 'ERROR', service: 'payment-service', body: 'auth call failed: Read timed out' }],
  }],
  logBackend: 'elasticsearch',
  links: [{ label: 'payment-service · Overview', href: '/service?name=payment-service' }],
});

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

function render(rep: ChatServiceReportPayload) {
  act(() => {
    root.render(<MemoryRouter><ServiceReportCard rep={rep} /></MemoryRouter>);
  });
}

const hrefs = () => Array.from(host.querySelectorAll('a')).map(a => a.getAttribute('href'));

describe('ServiceReportCard', () => {
  it('hüküm, operasyon tabloları, teknik hata, trace + log, link çipleri', () => {
    render(reportFixture());
    const text = host.textContent ?? '';
    expect(host.querySelector('.ev-verdict .badge.b-err')?.textContent).toBe('Sorunlu');
    expect(text).toContain('hata oranı %7.20 (baseline %0.40)');
    expect(host.querySelectorAll('table.ev-table')).toHaveLength(2);
    expect(text).toContain('POST /api/pay');
    expect(text).toContain('×8.0');
    expect(text).toContain('COR-30518');
    expect(text).toContain('son 1 sa 900 · önce 200');
    expect(hrefs()).toContain('/problems?exc=ora%3A1');
    expect(hrefs()).toContain('/trace?id=0af7651916cd43dd8448eb211c80319c');
    expect(host.querySelector('[aria-label="trace logları"]')?.classList.contains('mono')).toBe(true);
    expect(text).toContain('auth call failed: Read timed out');
    expect(hrefs()).toContain('/service?name=payment-service');
  });
  it('okunamayan bölümler "yok" demez; boş bölümler kendi cümlesiyle', () => {
    const p = reportFixture();
    p.ops = { slow: null, erroring: null, err: 'timeout' };
    p.exceptions = [];
    p.exceptionsError = 'timeout';
    p.red = { spans: 0, errorRate: 0, p99Ms: 0, baseErrorRate: 0, baseP99Ms: 0, unavailable: 'timeout' };
    p.drills = [];
    p.links = [];
    render(p);
    const text = host.textContent ?? '';
    expect(text).toContain('Operasyonlar okunamadı (timeout)');
    expect(text).toContain('Hatalar okunamadı');
    expect(text).toContain('Servis RED okunamadı');
    expect(text).toContain('kırılan trace yok');
    expect(host.querySelectorAll('table')).toHaveLength(0);
  });
  // v0.10.1415 — operatör: "… tam rapor çekerken CoSRE VictoriaMetrics'ten HTTP grafiklerini getirsin. Response time ve throughput."
  it('VM HTTP grafikleri özetin hemen altında (operasyonlardan önce), Metrics linki; yoksa dürüst not', () => {
    const p = reportFixture();
    const chart = (kind: string) => ({
      title: `orders-api · ${kind} · VictoriaMetrics (örneklenmemiş)`, evidenceRef: '', deepLink: `/metrics?k=${kind}`,
      window: { fromNs: 1, toNs: 2 }, kind, unit: 's', query: `q_${kind}`, series: [{ name: 'p99', unit: 's', points: [[1, 0.2]] as [number, number][] }],
    });
    p.httpCharts = [chart('rate'), chart('latency')];
    render(p);
    const charts = Array.from(host.querySelectorAll('[data-http-chart]')).map(e => e.getAttribute('data-http-chart'));
    expect(charts).toEqual(['rate', 'latency']);
    const html = host.innerHTML;
    expect(html.indexOf('data-http-chart="rate"')).toBeGreaterThan(html.indexOf('ev-verdict'));
    expect(html.indexOf('data-http-chart="latency"')).toBeLessThan(html.indexOf('En yavaş operasyonlar'));
    expect(hrefs()).toContain('/metrics?k=rate');
    expect(host.textContent).toContain('VictoriaMetrics (örneklenmemiş)');

    const q = reportFixture();
    q.httpChartsNote = 'VictoriaMetrics yapılandırılmamış';
    render(q);
    expect(host.querySelectorAll('[data-http-chart]')).toHaveLength(0);
    expect(host.textContent).toContain('HTTP grafikleri yok — VictoriaMetrics yapılandırılmamış');
    render(reportFixture());
    expect(host.textContent).not.toContain('HTTP grafikleri yok');
  });
  it('log durumu cümleleri ayrı; hüküm rengi yalnız sapan hükümde', () => {
    const d = reportFixture().drills![0];
    expect(drillLogsNote({ ...d, logsState: 'no_store' })).toContain('depo yok');
    expect(drillLogsNote({ ...d, logsState: 'error' })).toContain('okunamadı');
    expect(drillLogsNote({ ...d, logsState: 'empty' })).toContain('log kaydı yok');
    expect(drillLogsNote({ ...d, logsState: 'ok' })).toBeNull();
    expect(verdictBadgeClass('Dikkat')).toBe('badge b-warn');
    expect(verdictBadgeClass('Sağlıklı')).toBe('badge b-gray');
  });
});
