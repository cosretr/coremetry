// @vitest-environment jsdom
//
// AlertProblemDetail.layout — v0.10.1032.
//
// Operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor. Exception
// gibi detay gözükmüyor." + "Anlaşılır olsun. Çok detay verince daha
// anlaşılır olmuyor — alert ve anomaliler de."
//
// NE ÇİVİLİYOR:
//   • şeridin altındaki ilk şey tek cümlelik özet + "ne zaman" satırı, hemen
//     altında Triyaj (Assign… + öğretme — çekmece atlanınca kaybolmasınlar);
//   • kök neden / metrik / blast radius / correlated signals GÖRÜNÜR;
//   • zaman çizelgesi, Bildirim, Runbook, Description KAPALI gelir ve kapalıyken
//     MOUNT EDİLMEZ — bildirim geçmişi ve runbook koşuları sayfa açılışında
//     çekilmez, bölüm açılınca çekilir; hiçbir bölüm silinmedi;
//   • Assign… çekmeceyle aynı (prompt, boş = atamayı kaldır); viewer salt okunur.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Problem } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { role: 'editor', notifyMounts: 0, runbookMounts: 0, assigned: [] as unknown[][] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    serviceOperations: async () => [],
    problemVerdicts: async () => ({ verdicts: [] }),
    servicesMetadata: async () => ({}),
    setProblemAssignee: async (...a: unknown[]) => { m.assigned.push(a); return {}; },
    acknowledgeProblems: async () => ({}),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: m.role }, loading: false }),
}));
vi.mock('./ProblemInsightStrip', () => ({ ProblemInsightStrip: () => null }));
vi.mock('@/components/RootCausePanel', () => ({ RootCausePanel: () => <div data-rcpanel /> }));
vi.mock('./ExternalEvidencePanel', () => ({ ExternalEvidencePanel: () => null }));
vi.mock('@/components/AffectedEntitiesList', () => ({ AffectedEntitiesList: () => null }));
vi.mock('./ProblemLogEvidence', () => ({ ProblemLogEvidence: () => <div data-logev /> }));
vi.mock('./ProblemNotifyPanel', () => ({
  ProblemNotifyPanel: () => { m.notifyMounts++; return <div data-notify />; },
}));
vi.mock('@/components/ProblemRunbookPanel', () => ({
  ProblemRunbookPanel: () => { m.runbookMounts++; return <div data-runbooks />; },
}));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('./AlertMetricChartSection', () => ({
  AlertMetricChartSection: ({ problem }: { problem: Problem }) => <div data-alert-chart={problem.metric} />,
}));

import { AlertProblemDetail } from './ProblemDetail';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const prob = (over: Partial<Problem> = {}): Problem => ({
  id: 'p1', ruleId: 'builtin:error_rate', ruleName: 'High error rate', severity: 'critical',
  service: 'checkout', metric: 'error_rate', value: 12.4, threshold: 5, status: 'open',
  description: 'Error rate above 5% for 5m', startedAt: Date.now() * 1e6 - 15 * 60e9,
  assignee: 'payments-team', ...over,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const onBack = vi.fn();
const onChanged = vi.fn();
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(p: Problem = prob()): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/inbox?problem=p1']}>
          <AlertProblemDetail problem={p} isAdmin={m.role !== 'viewer'} onBack={onBack} onChanged={onChanged} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const sects = (el: HTMLElement) => [...el.querySelectorAll<HTMLElement>('.pb-sect')];
const sect = (el: HTMLElement, title: string) =>
  sects(el).find(s => (s.querySelector(':scope > .h')?.textContent ?? '').replace(/^[▸▾]\s*/, '').startsWith(title));
const toggleOf = (s: HTMLElement) => s.querySelector<HTMLButtonElement>(':scope > .h button[aria-expanded]');
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent?.includes(text));

beforeEach(() => {
  m.role = 'editor'; m.notifyMounts = 0; m.runbookMounts = 0; m.assigned = [];
  onBack.mockReset(); onChanged.mockReset();
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  vi.restoreAllMocks();
});

describe('AlertProblemDetail — özet önce, ikincil bölümler kapalı (v0.10.1032)', () => {
  it('özet + ne zaman satırı şeridin hemen altında; TEK satırlık triyaj onun altında, iki kolonun ÜSTÜNDE', async () => {
    const el = await mount();
    const summary = el.querySelector('.pd-summary')!;
    expect(summary.querySelector('.pd-summary__what')?.textContent)
      .toBe('High error rate — checkout: error_rate değeri 12.40, eşik 5.00.');
    expect(summary.querySelector('.pd-summary__when')?.textContent).toMatch(/başladı · 15m · sürüyor$/);
    const bar = el.querySelector('.rb-bar')!;
    expect(bar.nextElementSibling).toBe(summary);
    const triage = el.querySelector<HTMLElement>('.pd-triage')!;
    expect(summary.nextElementSibling).toBe(triage);
    expect(el.querySelector('.pd-cols')!.contains(triage)).toBe(false);
    // Kutulu bir "Triyaj" bölümü ve uzun paragraf YOK (inceleme: "çok büyük").
    expect(sect(el, 'Triyaj')).toBeUndefined();
    expect(triage.textContent).not.toContain('Karar bu satırın imzasına yazılır');
    expect(triage.textContent).toContain('Aynısı yeniden gelirse aynı sınıfa düşer; geri alınabilir.');
    // "Started … · ongoing" şeritten kalktı (aynı bilgi iki kez basılmaz).
    expect(bar.textContent).not.toContain('Started');
  });

  it('çözülmüş problemde ne zaman satırı BİTİŞİ söyler', async () => {
    const started = Date.UTC(2026, 9, 2, 6, 0) * 1e6;
    const el = await mount(prob({ status: 'resolved', startedAt: started, resolvedAt: started + 90 * 60e9 }));
    expect(el.querySelector('.pd-summary__when')?.textContent).toMatch(/başladı · 1\.5h · \d{2}:\d{2} bitti$/);
  });

  it('kural adı ekranın üstünde İKİ KEZ basılmaz (özet zaten onunla başlıyor)', async () => {
    const el = await mount();
    expect(sect(el, 'Root cause analysis')!.textContent).not.toContain('High error rate');
    const anomaly = await (async () => { act(() => { root!.unmount(); }); host?.remove(); return mount(prob({ ruleId: 'anomaly:error_rate' })); })();
    expect(sect(anomaly, 'Root cause analysis')!.textContent).toContain('ANOMALY');
    expect(anomaly.querySelector('.pd-summary__what')?.textContent).toContain('olağan değer 5.00');
  });

  it('görüntü kimliği (P-xxxxx) şeritte, tıkla-kopyala', async () => {
    const el = await mount(prob({ displayId: 'P-4F2A1' }));
    const chip = [...el.querySelectorAll<HTMLElement>('.rb-bar .badge')].find(b => b.textContent === 'P-4F2A1');
    expect(chip).toBeTruthy();
    expect(chip!.getAttribute('title')).toContain('kopyalamak için tıkla');
  });

  it('kök neden, blast radius, correlated signals GÖRÜNÜR; Metric kapalı ama yerinde', async () => {
    const el = await mount();
    for (const t of ['Root cause analysis', 'Blast radius', 'Correlated signals']) {
      const s = sect(el, t);
      expect(s, t).toBeTruthy();
      expect(toggleOf(s!), `${t} kapanır olmamalı`).toBeNull();
      expect(s!.querySelector(':scope > .b'), `${t} gövdesi görünür`).not.toBeNull();
    }
    expect(el.querySelector('[data-rcpanel]')).not.toBeNull();
    const metric = sect(el, 'Metric')!;
    expect(toggleOf(metric)?.getAttribute('aria-expanded')).toBe('false');
    act(() => { toggleOf(metric)!.click(); });
    expect(sect(el, 'Metric')!.textContent).toContain('12.40');
  });

  it('zaman çizelgesi / Bildirim / Runbook / Description kapalı gelir ve kapalıyken mount edilmez', async () => {
    const el = await mount();
    for (const t of ['Problem timeline', 'Bildirim', 'Runbook', 'Description']) {
      const s = sect(el, t);
      expect(s, `${t} silinmemeli`).toBeTruthy();
      expect(toggleOf(s!)?.getAttribute('aria-expanded'), t).toBe('false');
      expect(s!.querySelector(':scope > .b'), `${t} kapalıyken gövde yok`).toBeNull();
    }
    expect(m.notifyMounts, 'bildirim geçmişi sayfa açılışında çekilmemeli').toBe(0);
    expect(m.runbookMounts, 'runbook koşuları sayfa açılışında çekilmemeli').toBe(0);

    act(() => { toggleOf(sect(el, 'Bildirim')!)!.click(); });
    expect(m.notifyMounts).toBeGreaterThan(0);
    expect(el.querySelector('[data-notify]')).not.toBeNull();
    act(() => { toggleOf(sect(el, 'Runbook')!)!.click(); });
    expect(el.querySelector('[data-runbooks]')).not.toBeNull();
    act(() => { toggleOf(sect(el, 'Description')!)!.click(); });
    expect(sect(el, 'Description')!.textContent).toContain('Error rate above 5% for 5m');
  });

  it('Log kanıtı bölümü kendi kapalı açıcısıyla yerinde (ikinci kapak yok)', async () => {
    const el = await mount();
    const s = sect(el, 'Log kanıtı')!;
    expect(toggleOf(s)).toBeNull();
    expect(s.querySelector('[data-logev]')).not.toBeNull();
  });
});

// v0.10.1054 — anomaliden terfi eden Problem. Operatör: "Anomaliden terfi eden
// problem de 'yinelenen' kuralına uysun; bugün deploy'a hâlâ eski kurala göre
// bağlanıyor." Kural tutunca sunucu recentDeploy göndermez (deploy kutusu yok)
// ve bölüm alanlarını gönderir: "ne zaman" satırında anomaliyle aynı tek ek,
// YENİ BÖLÜM YOK. Kural satırında alan gelse bile hiçbir şey değişmez.
describe('AlertProblemDetail — terfi Problem\'i yinelenen (v0.10.1054)', () => {
  it('gece işi 3. gece: deploy kutusu yok, ne zaman satırında "yinelenen · bu 3. kez · ilk kez …"', async () => {
    const started = Date.UTC(2026, 9, 2, 2, 0) * 1e6;
    const el = await mount(prob({
      ruleId: 'anomaly-auto:0123456789abcdef', ruleName: 'Anomaly · nightly-job', metric: 'anomaly_ratio',
      startedAt: started, status: 'resolved', resolvedAt: started + 20 * 60e9,
      episodeCount: 3, firstStartedAt: started - 2 * 86_400e9,
    }));
    const when = el.querySelector('.pd-summary__when')?.textContent ?? '';
    expect(when).toMatch(/başladı · 20m · \d{2}:\d{2} bitti · yinelenen · bu 3\. kez · ilk kez \d{2}\.\d{2}\.\d{4} \d{2}:\d{2}$/);
    expect(sect(el, 'Root cause analysis')!.textContent).not.toContain('Deploy correlation');
    // Yeni bölüm yok: başlıklar bugünkülerle aynı küme.
    expect(sects(el).some(s => /yinelen/i.test(s.querySelector(':scope > .h')?.textContent ?? ''))).toBe(false);
  });
  // İnceleme senaryosu (gece işi 7 gece, deploy, 8. bölüm).
  const PRIOR = { version: 'v2.0.0', timeUnixNs: 1, ageSeconds: 600 };
  const ep8 = (over: Partial<Problem> = {}) => prob({
    ruleId: 'anomaly-auto:0123456789abcdef', ruleName: 'Anomaly · nightly-job', metric: 'anomaly_ratio',
    episodeCount: 8, firstStartedAt: Date.now() * 1e6 - 7 * 86_400e9, ...over,
  });
  const timelineLis = async (el: HTMLElement) => {
    const s = sect(el, 'Problem timeline')!;
    act(() => { toggleOf(s)!.click(); });
    await settle();
    return [...sect(el, 'Problem timeline')!.querySelectorAll('li')].filter(li => li.textContent?.startsWith('Deploy'));
  };
  it('(b) warning, ölçüm yok: deploy kutusu yok; ne zaman ekinde ve zaman çizelgesinde NÖTR deploy', async () => {
    const el = await mount(ep8({ severity: 'warning', priorDeploy: PRIOR }));
    expect(el.querySelector('.pd-summary__when')?.textContent)
      .toMatch(/sürüyor · yinelenen · bu 8\. kez · ilk kez \S+ \S+ · deploy v2\.0\.0 10 dk önce \(öncesinde de görülüyordu\)$/);
    expect(sect(el, 'Root cause analysis')!.textContent).not.toContain('Deploy correlation');
    const lis = await timelineLis(el);
    expect(lis).toHaveLength(1);
    expect(lis[0].className).toBe(''); // warn DEĞİL — nötr nokta
    expect(lis[0].hasAttribute('data-prior-deploy')).toBe(true);
    expect(lis[0].textContent).toContain('v2.0.0');
  });
  it('(a) critical, ölçülen gerileme: sunucu deploy\'u yeniden olası neden yapar → deploy kutusu + warn satırı, nötr satır yok', async () => {
    const el = await mount(ep8({ recentDeploy: { version: 'v2.0.0', timeUnixNs: 1, ageSeconds: 600 } }));
    expect(sect(el, 'Root cause analysis')!.textContent).toContain('Deploy correlation');
    expect(el.querySelector('.pd-summary__when')?.textContent).not.toContain('deploy v2.0.0');
    const lis = await timelineLis(el);
    expect(lis).toHaveLength(1);
    expect(lis[0].className).toBe('warn');
    expect(lis[0].hasAttribute('data-prior-deploy')).toBe(false);
  });

  it('vaka A (2. bölüm, deploy KORUNUR): deploy kutusu VE ek birlikte', async () => {
    const el = await mount(prob({
      ruleId: 'anomaly-auto:0123456789abcdef', episodeCount: 2, firstStartedAt: Date.now() * 1e6 - 75 * 60e9,
      recentDeploy: { version: 'v2.0.0', timeUnixNs: Date.now() * 1e6 - 25 * 60e9, ageSeconds: 600 },
    }));
    expect(el.querySelector('.pd-summary__when')?.textContent).toMatch(/sürüyor · yinelenen · bu 2\. kez · ilk kez /);
    expect(sect(el, 'Root cause analysis')!.textContent).toContain('Deploy correlation');
  });
  it('alarm kuralı: alan gelse bile ne zaman satırı bugünkü gibi, deploy kutusu yerinde', async () => {
    const el = await mount(prob({
      episodeCount: 3, firstStartedAt: 1,
      recentDeploy: { version: 'v2.0.0', timeUnixNs: Date.now() * 1e6 - 25 * 60e9, ageSeconds: 600 },
    }));
    const when = el.querySelector('.pd-summary__when')?.textContent ?? '';
    expect(when).toMatch(/başladı · 15m · sürüyor$/);
    expect(when).not.toContain('yinelenen');
    expect(sect(el, 'Root cause analysis')!.textContent).toContain('Deploy correlation');
  });
});

describe('AlertProblemDetail — Triyaj (tek satır)', () => {
  const row = (el: HTMLElement) => el.querySelector<HTMLElement>('.pd-triage')!;
  it('atanan görünür; Assign… prompt\'u ile atar (boş = kaldır), kuyruğu tazeler', async () => {
    const el = await mount();
    expect(row(el).textContent).toContain('payments-team');
    const prompt = vi.spyOn(window, 'prompt').mockReturnValue('  ops@corp.example  ');
    await act(async () => { button(row(el), 'Assign…')!.click(); });
    await settle();
    expect(prompt).toHaveBeenCalled();
    expect(m.assigned).toEqual([['p1', 'ops@corp.example']]);
    expect(onChanged).toHaveBeenCalled();
    prompt.mockReturnValue('');
    await act(async () => { button(row(el), 'Assign…')!.click(); });
    await settle();
    expect(m.assigned[1]).toEqual(['p1', '']);
  });
  it('iptal (null) hiçbir şey yazmaz', async () => {
    const el = await mount();
    vi.spyOn(window, 'prompt').mockReturnValue(null);
    await act(async () => { button(row(el), 'Assign…')!.click(); });
    expect(m.assigned).toEqual([]);
  });
  it('öğretme düğmeleri aynı satırda', async () => {
    const el = await mount();
    expect(row(el).textContent).toContain('Gerçek problem mi?');
    expect(button(row(el), 'Gerçek problem')).toBeTruthy();
    expect(button(row(el), 'Problem değil')).toBeTruthy();
  });
  it('Acknowledge kuyruğu da tazeler (ana onay yolu artık burası)', async () => {
    const el = await mount();
    const inv = vi.spyOn(qc, 'invalidateQueries');
    await act(async () => { button(el, 'Acknowledge')!.click(); });
    await settle();
    expect(onChanged).toHaveBeenCalled();
    expect(inv.mock.calls.some(c => JSON.stringify(c[0]?.queryKey) === JSON.stringify(['inbox']))).toBe(true);
  });
  it('viewer: atananı ve kararı görür, düğme görmez', async () => {
    m.role = 'viewer';
    const el = await mount();
    expect(row(el).textContent).toContain('payments-team');
    expect(row(el).textContent).toContain('işaretlenmedi');
    expect(button(row(el), 'Assign…')).toBeUndefined();
    expect(button(row(el), 'Problem değil')).toBeUndefined();
    expect(button(el, 'Acknowledge')).toBeUndefined();
  });
});

// v0.10.1055 (operatör: "Okdir") — anomaliden terfi etmiş Problem'de ANOMALY
// rozeti yoktu ("anomaly-auto:" `startsWith('anomaly:')`i ıskalıyordu). Rozet
// gelir; Description bölümü KALIR (terfi açıklaması — tür / desen / tepe oranı /
// sayı — sayfadaki tek insan-okur metin), özet "eşik" der (threshold gerçek
// kapı). "anomaly:" dedektör ailesinde Description bugünkü gibi gizli.
describe('AlertProblemDetail — terfi / küme Problem\'inde ANOMALY rozeti (v0.10.1055)', () => {
  it.each([
    ['terfi', 'anomaly-auto:0123456789abcdef', 'Auto-promoted from anomaly: trace_op / nightly-job (peak ratio 5.0×, count 42)'],
    ['küme', 'anomaly-cluster:checkout', 'Correlated incident rooted at checkout — 3 services degraded together'],
  ])('%s: rozet var, Description yerinde ve açılınca metni taşır', async (_n, ruleId, desc) => {
    const el = await mount(prob({ ruleId, ruleName: 'Anomaly · nightly-job', metric: 'anomaly_ratio', value: 5, threshold: 3, description: desc }));
    expect(sect(el, 'Root cause analysis')!.textContent).toContain('ANOMALY');
    expect(el.querySelector('.pd-summary__what')?.textContent).toContain('eşik 3.00');
    const d = sect(el, 'Description');
    expect(d, 'Description silinmemeli').toBeTruthy();
    act(() => { toggleOf(d!)!.click(); });
    expect(sect(el, 'Description')!.textContent).toContain(desc);
  });
  it('"anomaly:" dedektörü: rozet var, Description bugünkü gibi gizli', async () => {
    const el = await mount(prob({ ruleId: 'anomaly:checkout:p99_ms', description: 'p99 on checkout — current 900 vs baseline 300' }));
    expect(sect(el, 'Root cause analysis')!.textContent).toContain('ANOMALY');
    expect(sect(el, 'Description')).toBeUndefined();
  });
  it('alarm kuralı: rozet yok', async () => {
    const el = await mount();
    expect(sect(el, 'Root cause analysis')!.textContent).not.toContain('ANOMALY');
  });
});

// v0.10.1064 (operatör: "grafik olmadığı için de anlamak çok zor artışları") —
// tetiklenen metriğin grafiği sol kolonun İLK bölümü, yalnız span-metrik alarm
// kuralında. Grafik bölümü sahte (kendi testi AlertMetricChartSection.render).
describe('AlertProblemDetail — tetiklenen metriğin grafiği (v0.10.1064)', () => {
  const leftCol = (el: HTMLElement) => el.querySelector<HTMLElement>('.pd-cols > div')!;
  it('alarm kuralı: sol kolonun ilk bölümü, kök nedenin ÜSTÜNDE', async () => {
    const el = await mount(prob({ ruleId: 'builtin-warn-http-p99-3s', metric: 'http_p99_ms', value: 3872.62, threshold: 3000 }));
    const first = leftCol(el).firstElementChild as HTMLElement;
    expect(first.dataset.alertChart).toBe('http_p99_ms');
    expect(el.querySelectorAll('[data-alert-chart]')).toHaveLength(1);
  });
  it.each([
    ['anomali', { ruleId: 'anomaly:checkout:p99_ms', metric: 'p99_ms' }],
    ['terfi anomalisi', { ruleId: 'anomaly-auto:0123456789abcdef', metric: 'anomaly_ratio' }],
    ['SLO', { ruleId: 'slo:checkout:critical', metric: 'error_rate' }],
    ['log sorgusu kuralı', { ruleId: 'builtin-x', metric: 'log_query' }],
    ['db öznesi', { ruleId: 'builtin-x', kind: 'db', service: 'db:oracle@core-db-01' }],
  ] as const)('%s: grafik yok, sayfa aynı', async (_n, over) => {
    const el = await mount(prob(over as Partial<Problem>));
    expect(el.querySelector('[data-alert-chart]')).toBeNull();
    expect(sect(el, 'Root cause analysis')).toBeTruthy();
  });
});
