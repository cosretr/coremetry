// @vitest-environment jsdom
//
// AnomalyEventDetail.render — v0.10.1032.
//
// Operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor. Exception
// gibi detay gözükmüyor." + "Anlaşılır olsun. Çok detay verince daha
// anlaşılır olmuyor — alert ve anomaliler de."
//
// NE ÇİVİLİYOR (davranış, markup ayrıntısı değil):
//   • host: yükleniyor / 404 "kayıt yok" / hata + yeniden dene;
//   • KÜRESEL keepPreviousData altında (main.tsx ile aynı varsayılan) id
//     değişince ÖNCEKİ olayın gövdesi yeni adresin altında GÖRÜNMEZ;
//   • varsayılan görünüm yalın: özet cümlesi, türün TEK grafiği, kapalı
//     kök-neden şeridi (sayfa açılışında çekmez), "Ne yapabilirim", Triyaj;
//     "Teknik ayrıntı" kapalı ve kapalıyken MOUNT EDİLMEZ;
//   • Mute… çekmeceyle aynı gövdeyi yazar ve kuyruğa döner; viewer düğme görmez.
//   • v0.10.1060 — log_pattern'ın tek grafiği "Desen sayısı" barları (servis log
//     hacmi DEĞİL); diğer log türleri log hacmini korur, log dışı türlerde
//     desen grafiği yok; desen okuması düşerse sayfanın geri kalanı ayakta.
//   • v0.10.1062 — servissiz log_pattern'da "Ne yapabilirim" desene uyan
//     loglara gider (+ "En çok"); diğer servissiz türler / servisli olay aynen.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider, keepPreviousData } from '@tanstack/react-query';
import type { AnomalyEvent } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    events: {} as Record<string, unknown>,
    mode: 'ok' as 'ok' | 'missing' | 'fail',
    pendingIds: new Set<string>(),
    role: 'editor',
    silences: [] as unknown[],
    rcOpen: [] as boolean[],
    logSeries: [{ name: 'ERROR', total: 12 }] as { name: string; total: number }[] | null,
    patternMode: 'ok' as 'ok' | 'fail' | 'gone',
    patternCalls: 0,
    patternExtra: {} as Record<string, unknown>,
  };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    anomalyEvent: (id: unknown) => {
      if (m.pendingIds.has(String(id))) return new Promise(() => {});
      if (m.mode === 'missing') return Promise.reject(new Error('HTTP 404: anomaly not found'));
      if (m.mode === 'fail') return Promise.reject(new Error('HTTP 500: CH down'));
      return Promise.resolve(m.events[String(id)] ?? null);
    },
    problemVerdicts: async () => ({ verdicts: [], policy: { muteNotifications: false } }),
    createAnomalySilence: async (body: unknown) => { m.silences.push(body); return {}; },
    anomalyLogPatternSeries: async () => {
      m.patternCalls++;
      if (m.patternMode === 'fail') throw new Error('HTTP 502: log backend slow');
      if (m.patternMode === 'gone') return null; // 404: desenin tanımı artık yok
      return { pattern: 'p', bucketSec: 60, from: 0, to: 120e9, points: [{ t: 0, v: 3 }, { t: 60e9, v: 900 }], ...m.patternExtra };
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: m.role }, loading: false }),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: ({ title }: { title: string }) => <div data-topbar>{title}</div> }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/LogsHistogram', async () => {
  const React = await vi.importActual<typeof import('react')>('react');
  return {
    // Gerçek bileşen seçicisiz kullanımda boş/hatada HİÇBİR ŞEY çizmez; sayfa
    // durumu onSeries tri-state'inden okur — sahte de onu yayar.
    LogsHistogram: ({ onSeries }: { onSeries?: (s: { name: string; total: number }[] | null) => void }) => {
      React.useEffect(() => { onSeries?.(m.logSeries); }, [onSeries]);
      return <div data-loghist />;
    },
  };
});
vi.mock('@/components/CosreChart', () => ({
  CosreChart: ({ spec, presentation }: { spec: { agg: string; operation?: string }; presentation?: { title: string; emptyNote?: string } }) =>
    <div data-chart={spec.agg} data-op={spec.operation ?? ''} data-title={presentation?.title ?? ''} data-empty={presentation?.emptyNote ?? ''} />,
}));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: ({ viz, error }: { viz?: string; error?: string }) =>
    <div data-pattern-panel={viz ?? ''} data-error={error ?? ''} />,
}));
vi.mock('@/components/RootCauseRibbon', () => ({
  RootCauseRibbon: ({ defaultOpen }: { defaultOpen?: boolean }) => {
    m.rcOpen.push(!!defaultOpen);
    return <div data-rc />;
  },
}));

import { AnomalyEventHost } from './AnomalyEventDetail';
import { anomalyChartWindow } from './anomalyDetail';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ev = (over: Partial<AnomalyEvent> = {}): AnomalyEvent => ({
  id: 'a1', kind: 'trace_op', pattern: 'POST /pay', service: 'checkout',
  startedAt: 1_700_000_000e9, lastSeen: 1_700_000_600e9, peakRatio: 4.2, currentRatio: 3.1, currentCount: 12,
  sample: '4bf92f3577b34da6a3ce929d0e0e4736', status: 'active', ...over,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const onBack = vi.fn();
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

function tree(id: string) {
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/inbox?anomaly=${id}`]}>
        <AnomalyEventHost id={id} isAdmin={m.role !== 'viewer'} onBack={onBack} />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

async function mount(id = 'a1', seedList?: AnomalyEvent[]): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  // main.tsx ile AYNI küresel varsayılan: tuzak tam olarak buydu.
  // retryDelay 0: kancanın kendi retry'ı (404 dışı hatada 2 deneme) testi bekletmesin.
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0, placeholderData: keepPreviousData } } });
  // /anomalies olay listesinin önbelleği (ilk boyama basamağı).
  if (seedList) qc.setQueryData(['anomalies', 'events'], { items: seedList, activeTotal: 0, clearedTotal: 0, truncated: false });
  await act(async () => { root!.render(tree(id)); });
  await settle();
  return host;
}

const byText = (el: HTMLElement, sel: string, text: string) =>
  [...el.querySelectorAll<HTMLElement>(sel)].find(n => n.textContent?.includes(text));

beforeEach(() => {
  m.events = { a1: ev(), b2: ev({ id: 'b2', kind: 'log_pattern', pattern: 'ORA-00060', service: 'billing', peakRatio: 6, sample: 'deadlock' }) };
  m.mode = 'ok'; m.pendingIds = new Set(); m.role = 'editor'; m.silences = []; m.rcOpen = [];
  m.logSeries = [{ name: 'ERROR', total: 12 }];
  m.patternMode = 'ok'; m.patternCalls = 0; m.patternExtra = {};
  onBack.mockReset();
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('AnomalyEventHost — durumlar', () => {
  it('yükleniyor: spinner, gövde yok', async () => {
    m.pendingIds.add('a1');
    const el = await mount();
    expect(el.querySelector('[data-topbar]')?.textContent).toBe('Anomali');
    expect(el.textContent).not.toContain('normalin');
  });
  it('404 → "Anomali kaydı yok" + geri', async () => {
    m.mode = 'missing';
    const el = await mount();
    expect(el.textContent).toContain('Anomali kaydı yok');
    act(() => { byText(el, 'button', '← Problems')!.click(); });
    expect(onBack).toHaveBeenCalled();
  });
  it('sunucu hatası 404 DEĞİL: "yüklenemedi" + Tekrar dene', async () => {
    m.mode = 'fail';
    const el = await mount();
    expect(el.textContent).toContain('Anomali yüklenemedi');
    expect(el.textContent).not.toContain('kaydı yok');
    expect(byText(el, 'button', 'Tekrar dene')).toBeTruthy();
  });
  it('id değişince ÖNCEKİ olayın gövdesi yeni adresin altında görünmez (keepPreviousData tuzağı)', async () => {
    const el = await mount('a1');
    expect(el.textContent).toContain('POST /pay için hata normalin 4.2 katına çıktı');
    m.pendingIds.add('b2');
    await act(async () => { root!.render(tree('b2')); });
    await settle();
    expect(el.textContent).not.toContain('POST /pay');
    expect(el.textContent).not.toContain('normalin');
  });
});

describe('AnomalyEventDetail — yalın varsayılan görünüm', () => {
  it('trace_op: özet, TEK grafik (operasyonun hata SAYISI), kapalı kök neden, pivotlar', async () => {
    const el = await mount('a1');
    expect(el.querySelector('.pd-summary__what')?.textContent)
      .toBe('checkout servisinde POST /pay için hata normalin 4.2 katına çıktı.');
    expect(el.querySelector('.pd-summary__when')?.textContent).toMatch(/başladı · 10m · sürüyor$/);
    const charts = el.querySelectorAll('[data-chart]');
    expect(charts.length).toBe(1);
    // v0.10.1032 (inceleme) — dedektör hata SAYISINI kıyaslar; oran (%) değil.
    expect(charts[0].getAttribute('data-chart')).toBe('errors');
    expect(charts[0].getAttribute('data-op')).toBe('POST /pay');
    expect(charts[0].getAttribute('data-title')).toBe('POST /pay · hata sayısı');
    expect(charts[0].getAttribute('data-empty')).toContain('Bu pencere için seri yok');
    // Tür rozeti düz Türkçe.
    expect(el.querySelector('.rb-bar')?.textContent).toContain('Operasyon hatası');
    expect(el.querySelector('.rb-bar')?.textContent).not.toContain('TRACE OP');
    expect(el.querySelector('[data-loghist]')).toBeNull();
    // v0.10.1060 — log dışı türde desen grafiği yok, okuması da yok.
    expect(el.querySelector('[data-pattern-panel]')).toBeNull();
    expect(byText(el, '.pb-sect', 'Desen sayısı')).toBeUndefined();
    expect(m.patternCalls).toBe(0);
    expect(m.rcOpen.length).toBeGreaterThan(0);
    expect(m.rcOpen.every(o => o === false), 'kök neden sayfa açılışında açılmamalı').toBe(true);
    const actions = byText(el, '.pb-sect', 'Ne yapabilirim')!;
    expect(actions.textContent).toContain('Logları aç');
    expect(actions.textContent).toContain("Operasyonun hatalı trace'leri");
    expect(actions.textContent).toContain('Örnek trace');
    expect(actions.textContent).toContain('Servis sayfası');
  });

  it('"Teknik ayrıntı" kapalı gelir, kapalıyken mount edilmez; açınca olgu ızgarası gelir', async () => {
    const el = await mount('a1');
    expect(el.textContent).not.toContain('En yüksek artış');
    expect(el.textContent).not.toContain('Penceredeki sayım');
    const toggle = byText(el, 'button', 'Teknik ayrıntı')!;
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    act(() => { toggle.click(); });
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(el.textContent).toContain('En yüksek artış');
    expect(el.textContent).toContain('Penceredeki sayım');
  });

  // v0.10.1060 (operatör: "artışın ne zaman başladığını göstermiyor.
  // Elastic'e gidip bakınca barlardan net görüyorum") — tek grafik desenin
  // KENDİ sayısı (bar), "Teknik ayrıntı"nın ÜSTÜNDE ve açık; servis log hacmi yok.
  it('log_pattern: TEK grafik "Desen sayısı" barları, log hacmi / Seyir yok, desen özetin altında', async () => {
    const el = await mount('b2');
    expect(el.querySelector('.pd-summary__what')?.textContent).toBe('billing loglarında bu desen normalin 6 katına çıktı.');
    expect(el.querySelector('.pd-summary__detail')?.textContent).toBe('ORA-00060');
    expect(el.querySelector('[data-loghist]')).toBeNull();
    expect(el.querySelectorAll('[data-chart]').length).toBe(0);
    const sect = byText(el, '.pb-sect', 'Desen sayısı')!;
    expect(sect.querySelector('[data-pattern-panel]')?.getAttribute('data-pattern-panel')).toBe('bars');
    expect(sect.querySelector('.pb-sect-toggle'), 'kapalı bölüm değil, her zaman görünür').toBeNull();
    const sects = [...el.querySelectorAll('.pb-sect')];
    expect(sects.indexOf(sect)).toBeLessThan(sects.indexOf(byText(el, '.pb-sect', 'Teknik ayrıntı')!));
    expect(m.patternCalls).toBe(1);
    expect(byText(el, '.pb-sect', 'Ne yapabilirim')!.textContent).toContain("Hatalı trace'ler");
    // Bölüm başlığı yeterli: bileşenin İngilizce alt yazısı tam sayfada YOK.
    expect(el.textContent).not.toContain('log volume around the spike');
    expect(el.querySelector('.rb-bar')?.textContent).toContain('Log deseni');
  });

  it('desen okuması düşerse: panel içinde hata, sayfanın geri kalanı ayakta', async () => {
    m.patternMode = 'fail';
    const el = await mount('b2');
    await settle(); // kancanın tek yeniden denemesi
    expect(el.querySelector('[data-pattern-panel]')?.getAttribute('data-error')).toBe('Desen sayısı okunamadı.');
    expect(el.querySelector('.pd-summary__what')?.textContent).toContain('normalin 6 katına');
    expect(byText(el, '.pb-sect', 'Ne yapabilirim')!.textContent).toContain('Logları aç');
    expect(el.querySelector('.pd-triage')).not.toBeNull();
  });

  it('log_template_new: log hacmi grafiği kalır, desen grafiği yok', async () => {
    m.events.c3 = ev({ id: 'c3', kind: 'log_template_new', pattern: 'user <*> logged in', service: 'billing', peakRatio: 0, currentRatio: 0, sample: 'user 7 logged in' });
    const el = await mount('c3');
    expect(el.querySelector('[data-loghist]')).not.toBeNull();
    expect(el.querySelector('[data-pattern-panel]')).toBeNull();
    expect(m.patternCalls).toBe(0);
  });

  it('log hacmi boş / hatalı: sayfanın kısa notu (sessiz boşluk yok)', async () => {
    m.events.c3 = ev({ id: 'c3', kind: 'log_template_new', pattern: 'user <*> logged in', service: 'billing', peakRatio: 0, currentRatio: 0, sample: 'user 7 logged in' });
    m.logSeries = [];
    const el = await mount('c3');
    expect(byText(el, '.pb-sect', 'Log hacmi')!.textContent).toContain('Bu pencere için seri yok — olay saklama süresinin dışında olabilir.');
    act(() => { root!.unmount(); });
    host?.remove();
    m.logSeries = null;
    const el2 = await mount('c3');
    expect(byText(el2, '.pb-sect', 'Log hacmi')!.textContent).toContain('Log hacmi okunamadı');
  });
});

// v0.10.1062 (operatör, prod ES: servissiz log deseni anomalisinde "Ne
// yapabilirim" yalnız "Bu olay bir servis adı taşımıyor…" diyordu; operatör
// Kibana'ya elle gidiyordu). Servissiz log_pattern → desene uyan satırlarla
// "Logları aç" (pencere olayınki) + "En çok: …"; diğer servissiz türler AYNEN.
// v0.10.1071 (operatör, prod ES: "Logları aç" grafiğin saydığından başka
// satırlar açtı) — bağlantı `pattern=<desen adı>` taşır, `q=` YAZMAZ: /logs
// sunucusu dedektörün yüklemini uygular. Servisli log_pattern'da da.
describe('AnomalyEventDetail — "Ne yapabilirim" servissiz log deseni', () => {
  const svcless = (over: Partial<AnomalyEvent> = {}) => ev({
    id: 'd4', kind: 'log_pattern', pattern: 'Service quota', service: '', peakRatio: 781,
    sample: 'Service quota warning: ORDER_QUEUE_LISTENER', ...over,
  });
  const card = (el: HTMLElement) => byText(el, '.pb-sect', 'Ne yapabilirim')!;
  const logsLink = (el: HTMLElement) =>
    [...card(el).querySelectorAll('a')].find(a => a.textContent?.includes('Logları aç'));
  // Desen okuması olay okuması ÇİZİLDİKTEN sonra başlar (ilk settle'ın
  // sonunda); cevabın çizimi için ikinci bir settle.
  const mountRead = async (id: string) => { const el = await mount(id); await settle(); return el; };

  it('servissiz log_pattern: "Logları aç" pattern= ile (q yok), olay penceresinde; "En çok"; tek okuma', async () => {
    m.events.d4 = svcless();
    m.patternExtra = {
      topServices: [{ service: 'orders-svc', count: 700 }, { service: 'billing-svc', count: 81 }],
    };
    const el = await mountRead('d4');
    expect(el.querySelector('.pd-summary__what')?.textContent).toBe('Loglarda bu desen normalin 781 katına çıktı.');
    const a = logsLink(el)!;
    expect(a).toBeTruthy();
    const url = new URL(a.getAttribute('href')!, 'http://x');
    const win = anomalyChartWindow(svcless());
    expect(url.pathname).toBe('/logs');
    expect(url.searchParams.get('pattern')).toBe('Service quota');
    expect(url.searchParams.has('q')).toBe(false);
    expect(url.searchParams.get('range')).toBe(`custom:${win.fromNs / 1e6}-${win.toNs / 1e6}`);
    expect(url.searchParams.has('service')).toBe(false);
    expect(a.textContent).toContain('desene uyan satırlar, olay penceresi');
    expect(card(el).textContent).toContain('En çok: orders-svc, billing-svc');
    expect(card(el).textContent).not.toContain('servis adı taşımıyor');
    // Servis gerektiren pivotlar yine YOK.
    expect(card(el).textContent).not.toContain("Hatalı trace'ler");
    expect(card(el).textContent).not.toContain('Servis sayfası');
    // Grafik bölümü ile kart AYNI okumayı paylaşır.
    expect(m.patternCalls).toBe(1);
  });

  it('servis atfı yoksa (CH) yalnız bağlantı, "En çok" satırı yok', async () => {
    m.events.d4 = svcless();
    m.patternExtra = {};
    const el = await mountRead('d4');
    expect(logsLink(el)).toBeTruthy();
    expect(card(el).textContent).not.toContain('En çok');
  });

  it('grafik okuması düşse de bağlantı durur (ad olaydan); desen tanımı kalkmışsa (404) sahte bağlantı yok', async () => {
    m.events.d4 = svcless();
    m.patternMode = 'fail';
    const el = await mountRead('d4');
    await settle(); // kancanın tek yeniden denemesi
    const a = logsLink(el);
    expect(a).toBeTruthy();
    expect(new URL(a!.getAttribute('href')!, 'http://x').searchParams.get('pattern')).toBe('Service quota');
    expect(card(el).textContent).not.toContain('En çok');
    act(() => { root!.unmount(); });
    host?.remove();
    m.patternMode = 'gone'; // sunucu bu adı tanımıyor → /logs?pattern= de 400 olurdu
    const el2 = await mountRead('d4');
    expect(logsLink(el2)).toBeUndefined();
    expect(card(el2).textContent).toContain('Bu olay bir servis adı taşımıyor');
  });

  it('diğer servissiz türler değişmez: cümle aynen, desen okuması yok', async () => {
    m.events.e5 = ev({ id: 'e5', service: '' });
    m.events.f6 = ev({ id: 'f6', kind: 'log_template_new', pattern: 'user <*> logged in', service: '', peakRatio: 0, currentRatio: 0, sample: 'user 7 logged in' });
    for (const id of ['e5', 'f6']) {
      const el = await mountRead(id);
      expect(card(el).textContent).toBe('Ne yapabilirimBu olay bir servis adı taşımıyor — log / trace / servis pivotları bir servis adı gerektiriyor.');
      act(() => { root!.unmount(); });
      host?.remove();
    }
    expect(m.patternCalls).toBe(0);
  });

  it('servisli log_pattern: servis kapsamı + pattern= (q yok), "En çok" yok', async () => {
    m.patternExtra = { topServices: [{ service: 'orders-svc', count: 3 }] };
    const el = await mountRead('b2');
    const url = new URL(logsLink(el)!.getAttribute('href')!, 'http://x');
    expect(url.searchParams.get('service')).toBe('billing');
    expect(url.searchParams.get('pattern')).toBe('ORA-00060');
    expect(url.searchParams.has('q')).toBe(false);
    expect(logsLink(el)!.textContent).toContain('servis + desen, olay penceresi');
    expect(card(el).textContent).toContain("Hatalı trace'ler");
    expect(card(el).textContent).toContain('Servis sayfası');
    expect(card(el).textContent).not.toContain('En çok');
  });
});

// v0.10.1032 (inceleme) — önbellek yalnız İLK BOYAMA; tekil okuma her zaman
// koşar ve kazanır. Eskiden önbellek bulunursa okuma hiç koşmuyordu: bitmiş
// bir olay dakikalarca "ACTIVE · sürüyor" diye donuk kalıyordu.
describe('AnomalyEventHost — önbellek ilk boyama, okuma gerçek', () => {
  it('önbellekte ACTIVE, okuma CLEARED → sayfa CLEARED ve "bitti" der', async () => {
    m.events = { a1: ev({ status: 'cleared' }) };
    const el = await mount('a1', [ev({ status: 'active' })]);
    expect(el.querySelector('.rb-bar')?.textContent).toContain('CLEARED');
    expect(el.querySelector('.rb-bar')?.textContent).not.toContain('ACTIVE');
    expect(el.querySelector('.pd-summary__when')?.textContent).toMatch(/bitti$/);
  });
  it('okuma beklerken önbellekteki satır anında çizilir', async () => {
    m.pendingIds.add('a1');
    const el = await mount('a1', [ev({ status: 'active' })]);
    expect(el.querySelector('.pd-summary__what')?.textContent).toContain('POST /pay için hata');
  });
  it('sunucu 404 derse önbellekteki satıra rağmen "kayıt yok"', async () => {
    m.mode = 'missing';
    const el = await mount('a1', [ev()]);
    expect(el.textContent).toContain('Anomali kaydı yok');
  });
});

describe('AnomalyEventDetail — Triyaj', () => {
  it('Mute… çekmeceyle aynı gövdeyi yazar (parmak izi = olay kimliği) ve kuyruğa döner', async () => {
    const el = await mount('a1');
    await act(async () => { byText(el, 'button', 'Mute…')!.click(); });
    await settle();
    expect(m.silences).toEqual([{
      fingerprint: 'a1', kind: 'trace_op', pattern: 'POST /pay', service: 'checkout', durationSec: 3600,
    }]);
    expect(onBack).toHaveBeenCalled();
  });
  it('TEK satır, özetin hemen altında, iki kolonun ÜSTÜNDE: [süre] [Mute…] · Gerçek problem mi? …', async () => {
    const el = await mount('a1');
    const row = el.querySelector('.pd-triage')!;
    expect(el.querySelector('.pd-summary')!.nextElementSibling).toBe(row);
    expect(el.querySelector('.pd-cols')!.contains(row)).toBe(false);
    expect(row.querySelector('select[aria-label="Susturma süresi"]')).not.toBeNull();
    expect(row.textContent).toContain('Gerçek problem mi?');
    expect(byText(row as HTMLElement, 'button', 'Problem değil')).toBeTruthy();
    expect(byText(row as HTMLElement, 'button', 'Gerçek problem')).toBeTruthy();
    // Uzun üç satırlık açıklama yok; tek kısa cümle var.
    expect(row.textContent).toContain('Aynısı yeniden gelirse aynı sınıfa düşer; geri alınabilir.');
    expect(row.textContent).not.toContain('Karar bu satırın imzasına yazılır');
    expect(el.querySelector('.pb-sect .h')?.textContent).not.toBe('Triyaj');
  });
  it('viewer: Mute ve karar düğmeleri YOK, durum salt okunur görünür', async () => {
    m.role = 'viewer';
    const el = await mount('a1');
    expect(byText(el, 'button', 'Mute…')).toBeUndefined();
    expect(byText(el, 'button', 'Problem değil')).toBeUndefined();
    expect(el.querySelector('.pd-triage')?.textContent).toContain('işaretlenmedi');
  });
});
