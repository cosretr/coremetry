// @vitest-environment jsdom
//
// v0.10.2286 — pin: arayüz dili İngilizceyken Problem detayının alt panelleri
// (ProblemLogEvidence, ProblemNewLogTemplates, ProblemNotifyPanel,
// ProblemEventTimeline / ProblemEventEvidence ve problemEvents satır metni)
// bilinen Türkçe etiket ÇİZMEZ; Türkçe arayüzde metin aynen kalır.
// Sunucu metni (desen, şablon, gönderim hatası, kanal adı) kapsam dışı.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Problem } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', notify: [] as unknown[] }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));

const NOW_NS = Date.now() * 1e6;
vi.mock('@/lib/api', () => {
  const now = Date.now() * 1e6;
  const ev = (over: Record<string, unknown>) => ({
    detector: 'svc_error_rate', startedAt: now - 60e9, lastSeen: now, observed: 0.034, baseline: 0.01, direction: 'up', live: true, ...over,
  });
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logsPatterns: async () => ({
      groups: [{ hash: 'h1', template: 'conn reset <*>', sample: 'conn reset 1', query: 'conn reset', severity: 17, count: 12, serviceCount: 1 }],
      total: 12, totalIsLowerBound: false, sampled: 500, cap: 500, distinct: 1, partial: true,
      coveredFromNs: now - 120e9, coveredToNs: now,
    }),
    problemLogTemplates: async () => ({
      services: ['svc-a'],
      templates: [{ templateId: 't1', template: 'pool exhausted <*>', sample: 'pool exhausted 3', service: 'svc-a',
        firstSeen: now - 40e9, lastSeen: now, totalCount: 7, offsetSec: -90, query: 'pool exhausted' }],
    }),
    problemNotifications: async () => m.notify,
    problemEvents: async () => ({ items: [
      ev({ own: true }), ev({ detector: 'svc_slowdown', observed: 9000, baseline: 300, live: false }),
      ev({ detector: 'metric_anomaly', ruleId: 'anomaly:svc-a:p99_ms' }), ev({ detector: 'anomaly_promotion' }),
      ev({ detector: 'metric_anomaly', ruleId: 'anomaly:svc-a:x' }), ev({ detector: 'svc_breadth', baseline: 0 }),
    ] }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});

import { ProblemLogEvidence } from './ProblemLogEvidence';
import { ProblemNewLogTemplates } from './ProblemNewLogTemplates';
import { ProblemNotifyPanel } from './ProblemNotifyPanel';
import { ProblemEventTimeline, ProblemEventEvidence } from './ProblemEventTimeline';
import { problemEventLines } from './problemEvents';
import { startOffsetTitle } from './problemLogTemplates';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const prob = { id: 'p1', service: 'svc-a', startedAt: NOW_NS - 300e9, status: 'resolved', resolvedAt: NOW_NS } as unknown as Problem;
const WIN = { fromNs: NOW_NS - 600e9, toNs: NOW_NS };
const SENDS = [
  { id: 'u', channelKind: 'none', channelName: 'unmatched', ok: false, error: 'no channel matched', sentAt: NOW_NS, target: '' },
  { id: 'a', channelKind: 'email', channelName: 'ops-mail', ok: true, sentAt: NOW_NS - 1e9, target: 'ops@example.test' },
  { id: 'b', channelKind: 'webhook', channelName: 'hook-1', ok: false, error: 'x', sentAt: NOW_NS - 2e9, target: 'https://hook.example.test' },
];

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
const snap = () => ({ text: host!.textContent ?? '', html: host!.innerHTML });
async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
  await settle();
}
const close = () => { act(() => { root?.unmount(); }); host?.remove(); };
async function all() {
  const out = [];
  await mount(<ProblemLogEvidence service="svc-a" startedAt={prob.startedAt} resolvedAt={NOW_NS} linkWindow={{ from: 'now-1h', to: 'now' } as never} />);
  out.push(snap());
  act(() => { host!.querySelector<HTMLButtonElement>('button')!.click(); });
  await settle();
  out.push(snap()); close();
  await mount(<ProblemNewLogTemplates problem={prob} window={WIN} />); out.push(snap()); close();
  m.notify = SENDS;
  await mount(<ProblemNotifyPanel problemId="p1" />); out.push(snap()); close();
  m.notify = [];
  await mount(<ProblemNotifyPanel problemId="p2" />); out.push(snap()); close();
  await mount(<><ProblemEventTimeline problemId="p1" /><ProblemEventEvidence problemId="p1" /></>); out.push(snap());
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => { m.lang = 'en'; m.notify = []; });
afterEach(() => { close(); host = null; root = null; });

const TR_LABELS = [
  'Log desenleri', 'problem penceresi', 'Şiddet', 'Desen', 'Kayıt', 'Servis', 'kısmi sonuç', 'örnek satır', 'tavan', 'desen',
  'kapsanan', 'en yeni', 'yaklaşıktır', 'Desenler paneli', 'Başlangıçta doğan', 'başlangıç', 'dk', 'sn', 'örnek', 'Örnek satır',
  'Şablonun bağlandığı', 'Logları aç', 'Ara:', 'doğdu', 'KİMSEYE GİTMEDİ', 'eşleşme kuralını', 'gönderim', 'gitti', 'hata',
  'bildirim kaydı yok', 'Ayarlar', 'Yaygın yavaşlama', 'Anomali terfisi', 'Metrik anomalisi', 'Gecikme', 'Servis hata oranı',
  '(açan)', '(bitti)', 'gözlenen', 'olağan', 'olay daha', 'Teknik kanıt', 'Birleşen olaylar', 'birleşen',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Problem detayı alt panelleri (2) — İngilizce arayüz (v0.10.2286)', () => {
  it('log kanıtı, doğan şablonlar, bildirim, birleşen olaylar: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'Log patterns (ERROR+, problem window)', 'Severity', 'Pattern (sample line in the tooltip)', 'Records', 'Services',
      'partial result', '12 ERROR+ log · 500 sampled lines (cap 500) · 1 pattern', 'covered: ', '(2 min, newest end)',
      ' · based on the newest sample lines; the pattern query is approximate',
      'Log templates that appeared at the start', 'svc-a · start −10 min … +5 min', '−1 min 30 s', '7 samples', 'Open logs',
      'REACHED NO ONE', 'no channel matched', 'Widen the channel', '1 send succeeded', '1 send failed', 'sent', 'error',
      'No notification record for this problem', 'Settings → Notifications',
      'Service error rate (ML) (opener) 3.4×', 'Widespread slowdown 30× (ended)', 'Latency (p99) anomaly', 'Anomaly promotion',
      'Metric anomaly', 'Widespread slowdown (ML)', 'and 1 more event (Technical evidence)', 'Merged events', '6 events',
      'observed 0.034 · normal 0.01', 'observed 0.034 · no normal value',
    ]) expect(r.text, en).toContain(en);
    for (const en of ['Partial result: timeout or shard failure', "This pattern's lines", 'appeared 1 min 30 s before the start',
      'The service the template is linked to', 'Search: pool exhausted', 'aria-label="Events merged into this problem"'])
      expect(r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', () => {
    expect(startOffsetTitle(-90)).toBe('başlangıçtan 1 dk 30 sn önce doğdu');
    expect(startOffsetTitle(-90, 'en')).toBe('appeared 1 min 30 s before the start');
    const e = { detector: 'svc_slowdown', startedAt: 1, lastSeen: 2, observed: 9000, baseline: 300, direction: 'up', live: false, own: true } as const;
    expect(problemEventLines([e], () => '14:02')[0].text).toBe('14:02 Yaygın yavaşlama (açan) 30× (bitti)');
    expect(problemEventLines([e], () => '14:02', 'en')[0].text).toBe('14:02 Widespread slowdown (opener) 30× (ended)');
  });
});

describe('Problem detayı alt panelleri (2) — Türkçe arayüzde metin aynen (v0.10.2286)', () => {
  it('paneller', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of [
      'Log desenleri (ERROR+, problem penceresi)', 'Şiddet', 'Desen (örnek satır başlıkta)', 'Kayıt', 'Servis', 'kısmi sonuç',
      '12 ERROR+ log · 500 örnek satır (tavan 500) · 1 desen', 'kapsanan: ', '(2 dk, en yeni uç)',
      ' · en yeni örnek satırlara göre; desen sorgusu yaklaşıktır',
      'Başlangıçta doğan log şablonları', 'svc-a · başlangıç −10 dk … +5 dk', '−1 dk 30 sn', '7 örnek', 'Logları aç',
      'KİMSEYE GİTMEDİ', 'Kanalın eşleşme kuralını', '1 gönderim başarılı', '1 gönderim başarısız', 'gitti', 'hata',
      'Bu problem için bildirim kaydı yok', 'Ayarlar → Bildirimler',
      'Servis hata oranı (ML) (açan) 3,4×', 'Yaygın yavaşlama 30× (bitti)', 'Gecikme (p99) anomalisi', 'Anomali terfisi',
      'Metrik anomalisi', 'Yaygın yavaşlama (ML)', 've 1 olay daha (Teknik kanıt)', 'Birleşen olaylar', '6 olay',
      'gözlenen 0,034 · olağan 0,01', 'gözlenen 0,034 · olağan değer yok',
    ]) expect(r.text, tr).toContain(tr);
    for (const tr of ['Kısmi sonuç: zaman aşımı ya da shard hatası', '— Bu desenin satırları', 'başlangıçtan 1 dk 30 sn önce doğdu',
      'Şablonun bağlandığı servis', 'Ara: pool exhausted', 'aria-label="Bu probleme birleşen olaylar"'])
      expect(r.html, tr).toContain(tr);
  });
});
