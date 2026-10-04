// @vitest-environment jsdom
//
// KafkaClientsTab.render — v0.10.1097 (operatör: "9'u yap").
//
// NE ÇİVİLİYOR (davranış):
//   • `topic` etiketi VARSA topic seçicisi çizilir; YOKSA hiç çizilmez ve
//     sekmede hakkında tek kelime yok (ölü denetim yok);
//   • client_id seçicisi + "Toplam / Pod bazlı" yalnız etiketleri varken;
//   • "Pod bazlı" URL'e ?kview=pod yazar (yabancı param korunur), istek
//     view=pod ile tekrar gider, panel "12 pod + diğer N" der;
//   • kaynak notu TEK satır, uzun metin title'da;
//   • "Bağlantılar": aktif pod sayısı; seri yoksa "metrik yok".
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { MessagingClients, SpanMetricSeries, TimeRange } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    labels: 'all' as 'all' | 'noTopic',
    connEmpty: false,
    calls: [] as Array<{ set?: string; tab?: { topicFilter?: string; clientFilter?: string; view?: string } }>,
    // v0.10.1102 — seçici istekleri (etiket + kapsam).
    labelCalls: [] as Array<{ label: string; scope: { system: string; cluster: string; destination: string; topic?: string; clientId?: string } }>,
  };
});

function series(n: number, svc = 'orders-consumer'): SpanMetricSeries[] {
  return Array.from({ length: n }, (_, i) => ({
    groupKey: [svc, `${svc}-6b7c-${i}`],
    points: [{ time: 1e18, value: 1 }, { time: 1e18 + 15e9, value: i + 1 }],
  }));
}

function fixture(tab?: { topicFilter?: string; clientFilter?: string; view?: string }): MessagingClients {
  const podView = tab?.view === 'pod';
  const block = (label: string, metric: string) => ({
    metric, label, unit: '{connection}', kind: 'gauge', agg: 'sum',
    groupBy: podView ? ['service.name', 'k8s_pod_name'] : ['service.name', 'client_id'],
    series: podView ? [...series(12), { groupKey: ['diğer 3'], points: [{ time: 1e18, value: 3 }] }] : series(2),
    folded: podView ? 3 : undefined,
  });
  return {
    system: 'kafka', cluster: 'c1', destination: 'orders', scope: 'services', source: 'vm', available: true,
    note: "Kaynak: METRİK (vm) — Kafka client metrikleri (OTel Java agent kafka-clients-metrics). Lag = bu istemcinin gördüğü partition lag'i; consumer group lag'i DEĞİL.",
    producers: ['orders-api'], consumers: ['orders-consumer'],
    blocks: {
      consumer_connection_count: block('Açık bağlantı (tüketici) — istemci', 'kafka.consumer.connection_count'),
      consumer_rebalance_rate: block('Rebalance/saat — istemci', 'kafka.consumer.rebalance_rate_per_hour'),
    },
    labels: m.labels === 'all'
      ? { detected: true, topic: true, clientId: true, pod: 'k8s_pod_name' }
      : { detected: true, topic: false, clientId: true, pod: 'k8s_pod_name' },
    // Sunucu UYGULANAN süzgeci yansıtır (etiket var → istenen = uygulanan).
    filter: { topic: tab?.topicFilter || undefined, clientId: tab?.clientFilter || undefined },
    view: podView ? 'pod' : '', stepSeconds: 15,
    connections: m.connEmpty
      ? { series: [], total: 0, podLabel: 'k8s_pod_name', activePods: 0 }
      : { series: series(4), total: 4, podLabel: 'k8s_pod_name', activePods: 3 },
  };
}

vi.mock('@/lib/api', () => ({
  api: {
    messagingClients: (_s: string, _c: string, _d: string, _f: number, _t: number, _sig: AbortSignal,
      set?: string, tab?: { topicFilter?: string; clientFilter?: string; view?: string }) => {
      m.calls.push({ set, tab });
      return Promise.resolve(fixture(tab));
    },
    kafkaLabelValues: async (label: string, _q: string, _f: number, _t: number,
      scope: { system: string; cluster: string; destination: string; topic?: string; clientId?: string }) => {
      m.labelCalls.push({ label, scope });
      return { label, source: 'vm', values: ['payments'] };
    },
  },
  isCanceled: () => false,
}));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: ({ title, note, items }: { title: string; note?: string; items: Array<{ name: string }> }) => (
    <div data-panel={title}>
      <span data-note>{note}</span>
      {items.map(i => <span key={i.name} data-item>{i.name}</span>)}
    </div>
  ),
}));
vi.mock('@/components/LazyMount', () => ({ LazyMount: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock('@/pages/alerts/KafkaAlertModal', () => ({ KafkaAlertModal: () => null }));

import { KafkaClientsTab } from './KafkaClientsTab';

// Kararlı kimlik: render başına yeni nesne memo'yu bozar (v0.5.184 sınıfı).
const RANGE: TimeRange = { preset: '15m' };
let root: Root | null = null;
let host: HTMLDivElement;
let loc = '';

function Probe() {
  const l = useLocation();
  loc = l.search;
  return null;
}

async function mount(search = '?system=kafka&cluster=c1&destination=orders&tab=clients') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/messaging/topic${search}`]}>
          <Probe />
          <KafkaClientsTab system="kafka" cluster="c1" destination="orders" range={RANGE}
            xRange={{ from: 0, to: 1 }} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await flush();
}

async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

beforeEach(() => { m.labels = 'all'; m.connEmpty = false; m.calls = []; m.labelCalls = []; loc = ''; });
afterEach(() => { act(() => root?.unmount()); root = null; host?.remove(); });

describe('KafkaClientsTab', () => {
  it('topic etiketi VAR: topic + client_id seçicileri ve görünüm geçişi çizilir', async () => {
    await mount();
    expect(host.querySelector('[aria-label="topic süzgeci"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="client_id süzgeci"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="Seri görünümü"]')).not.toBeNull();
    // "süzülemez" beyanı seçici ekrandayken yazılmaz.
    expect(host.textContent).not.toContain('süzülemez');
  });

  it('topic etiketi YOK: seçici hiç yok ve hakkında hiçbir şey yazmıyor', async () => {
    m.labels = 'noTopic';
    await mount();
    expect(host.querySelector('[aria-label="topic süzgeci"]')).toBeNull();
    expect(host.querySelector('[aria-label="client_id süzgeci"]')).not.toBeNull();
    expect(host.textContent).not.toMatch(/topic etiketi/i);
    // Var olan kapsam beyanı (metrik topic'e göre süzülemez) yerinde.
    expect(host.textContent).toContain('süzülemez');
  });

  it('kaynak notu tek satır, uzun metin title\'da', async () => {
    await mount();
    const src = host.querySelector('.kc-src');
    expect(src?.textContent).toBe('Kaynak: VictoriaMetrics · kafka client metrikleri · 15 sn adım');
    expect(src?.getAttribute('title')).toContain('consumer group lag');
    expect(host.querySelector('.kc-note')).toBeNull();
  });

  it('"Pod bazlı" URL\'e yazılır, istek view=pod ile gider, panel 12 + diğer N', async () => {
    await mount();
    expect(m.calls.at(-1)?.tab?.view).toBe('');
    const podBtn = [...host.querySelectorAll('[role="radio"]')].find(b => b.textContent === 'Pod bazlı') as HTMLElement;
    expect(podBtn).toBeTruthy();
    await act(async () => { podBtn.click(); });
    await flush();
    const sp = new URLSearchParams(loc);
    expect(sp.get('kview')).toBe('pod');
    expect(sp.get('tab')).toBe('clients');      // yabancı param korunur
    expect(sp.get('destination')).toBe('orders');
    expect(m.calls.at(-1)?.tab?.view).toBe('pod');
    const panel = host.querySelector('[data-panel="Rebalance/saat — istemci"]');
    expect(panel?.querySelector('[data-note]')?.textContent).toBe('12 pod + diğer 3');
    expect([...(panel?.querySelectorAll('[data-item]') ?? [])].at(-1)?.textContent).toBe('diğer 3');
  });

  it('URL\'deki görünüm/süzgeç isteğe gider (paylaşılan link aynı görünümü kurar)', async () => {
    await mount('?system=kafka&cluster=c1&destination=orders&tab=clients&kview=pod&ktopic=payments&kclient=consumer-orders-1');
    expect(m.calls.at(-1)?.tab).toEqual({ topicFilter: 'payments', clientFilter: 'consumer-orders-1', view: 'pod' });
  });

  it('Bağlantılar: aktif pod; bağlantı blokları ızgarada ikinci kez çizilmez', async () => {
    await mount();
    const conn = host.querySelector('[aria-label="Bağlantılar"]');
    expect(conn?.textContent).toContain('aktif pod 3 / 4');
    expect(host.querySelector('[data-panel="Açık bağlantı (tüketici) — istemci"]')).toBeNull();
    expect(host.querySelector('[data-panel="Rebalance/saat — istemci"]')).not.toBeNull();
  });

  // v0.10.1102 (operatör-onaylı) — öneriler SAYFA kapsamında: seçici isteği
  // sayfa anahtarlarını + uygulanmış karşı süzgeci taşır (servis listesi değil).
  it('seçici isteği sayfanın kapsamını ve karşı süzgeci taşır', async () => {
    await mount('?system=kafka&cluster=c1&destination=orders&tab=clients&ktopic=payments&kclient=consumer-orders-1');
    // usePickerSearch 180 ms debounce — gerçek zamanlayıcı.
    await act(async () => { await new Promise(r => setTimeout(r, 250)); });
    const client = m.labelCalls.filter(c => c.label === 'client_id').at(-1);
    const topic = m.labelCalls.filter(c => c.label === 'topic').at(-1);
    // Sayfa anahtarları (sunucu servis kümelerini panellerle aynı yoldan türetir).
    expect(client?.scope).toMatchObject({ system: 'kafka', cluster: 'c1', destination: 'orders', topic: 'payments' });
    expect(client?.scope).not.toHaveProperty('producers');
    expect(topic?.scope).toMatchObject({ destination: 'orders', clientId: 'consumer-orders-1' });
  });

  it('Bağlantılar: seri yoksa "metrik yok"', async () => {
    m.connEmpty = true;
    await mount();
    const conn = host.querySelector('[aria-label="Bağlantılar"]');
    expect(conn?.textContent).toContain('metrik yok');
    expect(conn?.textContent).not.toContain('aktif pod');
  });
});
