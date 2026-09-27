// @vitest-environment jsdom
// StatePathRebuild.test.tsx — v0.10.965 — "State tablolarının ZK yolu" bloğu
// (Replika tutarlılığı kartı). Operatör kararı 2026-09-27: on state tablosu
// birleşik yolda DROP + CREATE, veri taşınmaz. fetch taklit edilir, api.ts
// GERÇEK — düğmeden çıkan istek (yol, yöntem, gövde) doğrulanır
// (AdminClickhouse.rolloutLayer.test.tsx deseni). Host adları sentetik.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type {
  CHReplicaConsistencyResponse, CHStatePathCheck, CHStatePathRebuildPlan, CHStatePathRebuildResult, CHStatePathRebuildTable, CHStatePathTable,
} from '@/lib/types';
import { topEscLayer } from '@/lib/escLayer';
import { StatePathBlock } from './StatePathRebuild';
import { ReplicaConsistencyPanel } from '../AdminClickhouse';

const TEN = ['ingest_ledger', 'ai_eval_runs', 'rollout_events', 'rollout_workload_state', 'argocd_app_status',
  'argocd_sync_events', 'argocd_app_mapping', 'rollout_classification', 'ado_commit_enrichment', 'rollout_worker_runs'];
const HOSTS = ['host-1', 'host-2', 'host-3', 'host-4'];

const legacyRow = (table: string, rows = 0): CHStatePathTable => ({
  table, kind: 'legacy', rows: rows * 2, rebuildable: true,
  class: table === 'ingest_ledger' ? 'derived' : table === 'ai_eval_runs' ? 'operator_exception' : 'empty_only',
  classNote: `${table} notu`,
  groups: [
    { path: `/clickhouse/tables/01/${table}`, hosts: HOSTS.slice(0, 2), rows, unified: false },
    { path: `/clickhouse/tables/02/${table}`, hosts: HOSTS.slice(2), rows, unified: false },
  ],
});
const closedCheck = (over: Partial<CHStatePathCheck> = {}): CHStatePathCheck => ({
  zkPrefix: '/clickhouse/tables', lockOpen: false, lockReason: 'kurulum göç ÖNCESİ (10 state tablosu eski yolda)',
  complete: true, unreachable: 0, unified: 41,
  legacy: TEN.map(t => legacyRow(t, t === 'ingest_ledger' ? 1234 : t === 'ai_eval_runs' ? 5 : 0)),
  ...over,
});
const planRow = (table: string, rows: number): CHStatePathRebuildTable => ({
  table, state: 'legacy', action: 'rebuild', rows, groups: [], class: 'empty_only', classNote: `${table} notu`, verified: false,
});
const plan = (tables: string[], over: Partial<CHStatePathRebuildPlan> = {}): CHStatePathRebuildPlan => ({
  cluster: 'uptrace_all', database: 'coremetry', zkPrefix: '/clickhouse/tables', hosts: 4, measuredAt: 1_800_000_000_000,
  tables: tables.map(t => planRow(t, t === 'ingest_ledger' ? 2468 : t === 'ai_eval_runs' ? 10 : 0)),
  drops: tables.map(t => `DROP TABLE IF EXISTS ${t} ON CLUSTER \`uptrace_all\` SYNC`),
  creates: tables.map(t => `CREATE TABLE IF NOT EXISTS ${t} ON CLUSTER uptrace_all (…)`),
  lockOpensAfter: tables.length === TEN.length, stillLegacyAfter: tables.length === TEN.length ? [] : TEN.filter(t => !tables.includes(t)).sort(),
  blocked: [], checks: ['4/4 host erişilebilir', 'DDL kuyruğu sağlıklı'], warnings: [], // v0.10.965 — sunucu rollout/kilit uyarısını plana yazmaz (arayüz satırları)
  ...over,
});
const okResult = (over: Partial<CHStatePathRebuildResult> = {}): CHStatePathRebuildResult => ({
  ok: true, phase: 'done', tables: TEN.map(t => ({ ...planRow(t, 0), after: 'unified', verified: true })),
  statements: [{ head: 'DROP TABLE IF EXISTS ingest_ledger ON CLUSTER `uptrace_all` SYNC', ok: true }],
  lockOpen: true, lockReason: 'taze veya göç SONRASI kurulum', stillLegacy: [], resume: '',
  note: "Çalışan pod'lar boot anındaki yol gözlemini tutar: kilit her pod'un bir sonraki açılışında açılır (sonraki deploy ya da rolling restart).",
  ...over,
});

// ── fetch yönlendirici: `YÖNTEM /yol` → cevap; api.ts gerçek ────────────
// v0.10.965 — 'hang' = hiç dönmeyen istek (apply sürüyor); fonksiyon = sıralı cevap; raw = JSON olmayan gövde (geçit HTML'i).
type Reply = { status?: number; body: unknown; raw?: boolean } | 'hang';
let routes: Record<string, Reply | (() => Reply)> = {};
let seen: { method: string; url: string; body: unknown }[] = [];
function stubFetch() {
  vi.stubGlobal('fetch', (url: unknown, init?: RequestInit) => {
    const u = String(url);
    const method = init?.method ?? 'GET';
    seen.push({ method, url: u, body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined });
    const r0 = routes[`${method} ${u.split('?')[0]}`];
    const r: Reply = typeof r0 === 'function' ? r0() : r0 ?? { status: 404, body: { error: `rota yok: ${u}` } };
    if (r === 'hang') return new Promise<Response>(() => {});
    const text = r.raw ? String(r.body) : JSON.stringify(r.body);
    return Promise.resolve(new Response(text, { status: r.status ?? 200, headers: { 'content-type': r.raw ? 'text/html' : 'application/json' } }));
  });
}
const PLAN = '/api/admin/clickhouse/replica-consistency/state-paths/plan';
const APPLY = '/api/admin/clickhouse/replica-consistency/state-paths/apply';
const calls = (path: string) => seen.filter(s => s.method === 'POST' && s.url.split('?')[0] === path);

const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
let host: HTMLElement | null = null;
let root: Root | null = null;
let onDone = vi.fn();
async function mount(check: CHStatePathCheck): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  onDone = vi.fn();
  act(() => {
    root = createRoot(host!);
    root.render(<StatePathBlock check={check} cluster="uptrace_all" onDone={onDone} />);
  });
  await wait();
  return host!;
}
const buttonNamed = (scope: ParentNode, label: string) => {
  const b = [...scope.querySelectorAll('button')].find(x => x.textContent?.trim() === label);
  expect(b, `"${label}" düğmesi yok`).toBeDefined();
  return b!;
};
const checkboxNamed = (scope: ParentNode, label: string) => {
  const l = [...scope.querySelectorAll('label')].find(x => x.textContent?.trim() === label);
  expect(l, `"${label}" onay kutusu yok`).toBeDefined();
  return l!.querySelector('input[type="checkbox"]') as HTMLInputElement;
};
const click = async (el: HTMLElement) => { await act(async () => { el.click(); }); await wait(); };
const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]');

beforeEach(() => {
  routes = {};
  seen = [];
  stubFetch();
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  vi.unstubAllGlobals();
});

describe('StatePathBlock — denetim listesi', () => {
  it('kapalı kilit: başlık, kırmızı rozet, kural 3 açıklaması, 10 etiketli kutu, host adsız özet', async () => {
    const el = await mount(closedCheck());
    expect(el.textContent).toContain('State tablolarının ZK yolu');
    const badge = [...el.querySelectorAll('.badge.b-err')].find(b => b.textContent?.startsWith('kilit KAPALI'));
    expect(badge?.textContent).toBe('kilit KAPALI · 10 state tablosu eski yolda');
    expect(el.textContent).toContain('(kural 3)');
    expect(el.textContent).toContain('kilit bir sonraki açılışta kendiliğinden açılır');
    for (const t of TEN) expect(checkboxNamed(el, t).checked).toBe(true);
    expect(el.querySelectorAll('li input[type="checkbox"]')).toHaveLength(10);
    expect(el.textContent).toContain('…/tables/01/ingest_ledger: 2 host, 1.234 satır');
    expect(el.textContent).not.toMatch(/host-\d/); // host adları yalnız title'da
    expect(el.querySelector('[title*="host-1, host-2"]')).not.toBeNull();
    expect(buttonNamed(el, 'Yeniden kurulumu planla (10 tablo)').disabled).toBe(false);
    expect(el.querySelector('[role="alert"]')).toBeNull();
    expect(calls(PLAN)).toHaveLength(0); // plan yalnız düğmeyle
  });

  it('açık kilit: yeşil rozet, açıklama yok; eksik satır sarı rozet', async () => {
    const absent: CHStatePathTable = { ...legacyRow('rollout_events'), kind: 'absent', groups: [] };
    const el = await mount(closedCheck({ lockOpen: true, lockReason: 'taze veya göç SONRASI kurulum', legacy: [absent] }));
    expect(el.querySelector('.badge.b-ok')?.textContent).toBe('kilit açık · yeni state tabloları birleşik yola kurulur');
    expect([...el.querySelectorAll('.badge.b-warn')].map(b => b.textContent)).toContain('1 tablo birleşik yolda eksik');
    expect(el.textContent).not.toContain('(kural 3)');
  });

  it('eksik roster: role=alert uyarısı', async () => {
    const el = await mount(closedCheck({ complete: false, unreachable: 1 }));
    const alert = el.querySelector('[role="alert"]');
    expect(alert?.textContent).toBe("Uyarı: küme tanımındaki 1 host cevap vermedi — liste eksik olabilir; yeniden kurulum tüm host'lar erişilebilir olmadan çalışmaz.");
  });

  it('izin listesi dışı eski tablo: kutu yok, neden yazılı', async () => {
    const foreign: CHStatePathTable = { ...legacyRow('alert_rules'), rebuildable: false, class: undefined, classNote: undefined };
    const el = await mount(closedCheck({ legacy: [legacyRow('ingest_ledger', 1), foreign] }));
    expect(el.querySelectorAll('li input[type="checkbox"]')).toHaveLength(1);
    expect(el.textContent).toContain('bu sihirbazın izin listesinde değil — kilidi kapalı tutar');
    // Seçilemeyen eski tablo kilidi tutar: kısmi onay istenir.
    expect(el.querySelector('[role="status"]')?.textContent).toContain('Seçim dışı kalan eski tablolar: alert_rules');
  });
});

describe('StatePathBlock — kısmi seçim', () => {
  it('ingest_ledger seçimden çıkınca kilit uyarısı; onaysız plan kapalı; istek partialOK:true taşır', async () => {
    const el = await mount(closedCheck());
    await click(checkboxNamed(el, 'ingest_ledger'));
    const status = el.querySelector('[role="status"]');
    expect(status?.textContent).toContain('Seçim dışı kalan eski tablolar: ingest_ledger — kilit KAPALI kalır');
    const planBtn = buttonNamed(el, 'Yeniden kurulumu planla (9 tablo)');
    expect(planBtn.disabled).toBe(true);
    await click(checkboxNamed(el, 'Kilidin kapalı kalacağını anlıyorum'));
    expect(planBtn.disabled).toBe(false);
    const nine = TEN.filter(t => t !== 'ingest_ledger');
    routes[`POST ${PLAN}`] = { body: plan(nine) };
    routes[`POST ${APPLY}`] = { body: okResult({ lockOpen: false, stillLegacy: ['ingest_ledger'] }) };
    await click(planBtn);
    expect(calls(PLAN)[0].body).toEqual({ tables: nine });
    const dlg = dialog()!;
    expect(dlg.textContent).toContain('Kilit bu çalıştırmadan sonra da KAPALI: ingest_ledger');
    await click(checkboxNamed(dlg, '9 tabloyu ve 10 satırı SİLMEYİ onaylıyorum; veri geri gelmez.'));
    await click(buttonNamed(dlg, 'Düşür ve yeniden kur'));
    expect(calls(APPLY)[0].body).toMatchObject({ partialOK: true, confirm: true, tables: nine });
    expect(el.textContent).toContain('Kilit: KAPALI — ingest_ledger');
  });
});

describe('StatePathBlock — plan ve çalıştırma', () => {
  it('plan diyaloğu tabloları, satırları ve toplamı listeler; onay kutusu olmadan düğme kapalı; gövde sözleşmesi', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN) };
    routes[`POST ${APPLY}`] = { body: okResult() };
    const el = await mount(closedCheck());
    await click(buttonNamed(el, 'Yeniden kurulumu planla (10 tablo)'));
    expect(calls(PLAN)[0].body).toEqual({ tables: TEN });
    const dlg = dialog();
    expect(dlg).not.toBeNull();
    expect(dlg!.textContent).toContain('State tablolarını birleşik yola yeniden kur — 10 tablo');
    expect(dlg!.textContent).toContain('ingest_ledger · eski yol · düşür + birleşik yolda kur · silinecek 2.468 satır');
    expect(dlg!.textContent).toContain('Toplam silinecek: 2.478 satır (aktif parçalar; birleşmeden önceki fiziksel sayım).');
    expect(dlg!.textContent).toContain('/clickhouse/tables/state/<ad>, replika {shard}-{replica}');
    expect(dlg!.textContent).toContain('Veri TAŞINMAZ');
    expect(dlg!.textContent).toContain('✓ 4/4 host erişilebilir');
    expect(dlg!.textContent).toContain('Koşacak ifadeler (20)');
    const ack = checkboxNamed(dlg!, '10 tabloyu ve 2.478 satırı SİLMEYİ onaylıyorum; veri geri gelmez.');
    const total = dlg!.querySelector(`[id="${ack.getAttribute('aria-describedby')}"]`);
    expect(total?.textContent).toContain('Toplam silinecek');
    const go = buttonNamed(dlg!, 'Düşür ve yeniden kur');
    expect(go.disabled).toBe(true);
    expect(go.className).toMatch(/danger/);
    await click(ack);
    expect(go.disabled).toBe(false);
    await click(go);
    const body = calls(APPLY)[0].body as Record<string, unknown>;
    expect(body).toEqual({
      cluster: 'uptrace_all', tables: TEN, partialOK: false, confirm: true,
      ack: {
        measuredAt: 1_800_000_000_000,
        tables: TEN.map(t => ({ table: t, state: 'legacy', rows: t === 'ingest_ledger' ? 2468 : t === 'ai_eval_runs' ? 10 : 0 })),
      },
    });
    expect(dialog()).toBeNull();
    expect(el.textContent).toContain('Tamam: 10 tablo birleşik yolda doğrulandı (4/4 host). Kilit: açık.');
    expect(el.textContent).toContain('sonraki deploy ya da rolling restart');
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it('engelli plan: "Engel:" role=alert, onay kutusu ve düğme kapalı', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN, { blocked: ['erişilemeyen host: küme tanımında 4 host, 3 cevap verdi'] }) };
    const el = await mount(closedCheck());
    await click(buttonNamed(el, 'Yeniden kurulumu planla (10 tablo)'));
    const dlg = dialog()!;
    const alert = [...dlg.querySelectorAll('[role="alert"]')].map(a => a.textContent);
    expect(alert).toContain('Engel: erişilemeyen host: küme tanımında 4 host, 3 cevap verdi');
    const ack = checkboxNamed(dlg, '10 tabloyu ve 2.478 satırı SİLMEYİ onaylıyorum; veri geri gelmez.');
    expect(ack.disabled).toBe(true);
    expect(buttonNamed(dlg, 'Düşür ve yeniden kur').disabled).toBe(true);
    await click(buttonNamed(dlg, 'Vazgeç'));
    expect(dialog()).toBeNull();
    expect(calls(APPLY)).toHaveLength(0);
  });

  it('409: sunucunun metni role=alert ile, onDone çağrılmaz', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN) };
    routes[`POST ${APPLY}`] = { status: 409, body: { error: 'ön kontrol geçmedi — ingest_ledger: durum değişti (onay: eski yol, şimdi: birleşik) — yeniden planla' } };
    const el = await mount(closedCheck());
    await click(buttonNamed(el, 'Yeniden kurulumu planla (10 tablo)'));
    const dlg = dialog()!;
    await click(checkboxNamed(dlg, '10 tabloyu ve 2.478 satırı SİLMEYİ onaylıyorum; veri geri gelmez.'));
    await click(buttonNamed(dlg, 'Düşür ve yeniden kur'));
    const alerts = [...el.querySelectorAll('[role="alert"]')].map(a => a.textContent);
    expect(alerts).toContain('ön kontrol geçmedi — ingest_ledger: durum değişti (onay: eski yol, şimdi: birleşik) — yeniden planla');
    expect(onDone).not.toHaveBeenCalled();
  });

  it('verify aşamasında kalan sonuç: devam metni, ifadeler title\'da', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN) };
    routes[`POST ${APPLY}`] = {
      body: okResult({
        ok: false, phase: 'verify', lockOpen: false, stillLegacy: ['ingest_ledger'], note: '',
        resume: 'ingest_ledger doğrulanamadı (şimdi: eski yol). Eski yola yeniden kurulduysa bu arada bir pod açılmıştır — rollout/restart olmadığından emin ol, yeniden ölç ve çalıştır.',
      }),
    };
    const el = await mount(closedCheck());
    await click(buttonNamed(el, 'Yeniden kurulumu planla (10 tablo)'));
    const dlg = dialog()!;
    await click(checkboxNamed(dlg, '10 tabloyu ve 2.478 satırı SİLMEYİ onaylıyorum; veri geri gelmez.'));
    await click(buttonNamed(dlg, 'Düşür ve yeniden kur'));
    const line = [...el.querySelectorAll('[role="alert"]')].find(a => a.textContent?.startsWith('Yarıda kaldı'));
    expect(line?.textContent).toBe('Yarıda kaldı (doğrulama): ingest_ledger doğrulanamadı (şimdi: eski yol). Eski yola yeniden kurulduysa bu arada bir pod açılmıştır — rollout/restart olmadığından emin ol, yeniden ölç ve çalıştır.');
    expect(line?.getAttribute('title')).toContain('✓ DROP TABLE IF EXISTS ingest_ledger');
    expect(onDone).toHaveBeenCalledTimes(1); // DDL koştu: kart yeniden ölçülür
  });
});

// v0.10.965 — inceleme bulguları (UI-1…UI-7, C-F5): çalışan apply, cevapsız istek,
// görünür ifade listesi, taze plandan kısmi onay, kopya düzeltmeleri, yeniden ölçüm.
describe('StatePathBlock — çalıştırma sırasında ve sonrasında', () => {
  const openAndAck = async (el: HTMLElement, planLabel: string, ackLabel: string) => {
    await click(buttonNamed(el, planLabel));
    const dlg = dialog()!;
    await click(checkboxNamed(dlg, ackLabel));
    return dlg;
  };
  const ACK10 = '10 tabloyu ve 2.478 satırı SİLMEYİ onaylıyorum; veri geri gelmez.';

  it('apply sürerken diyalog kapanmaz: Vazgeç kapalı, Esc ve × diyaloğu bırakır, "Çalışıyor" satırı görünür', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN) };
    routes[`POST ${APPLY}`] = 'hang';
    const el = await mount(closedCheck());
    const dlg = await openAndAck(el, 'Yeniden kurulumu planla (10 tablo)', ACK10);
    await click(buttonNamed(dlg, 'Düşür ve yeniden kur'));
    expect(buttonNamed(dialog()!, 'Vazgeç').disabled).toBe(true);
    await act(async () => { topEscLayer()?.(); });
    await wait();
    expect(dialog()).not.toBeNull();
    await click(dialog()!.querySelector<HTMLElement>('[aria-label="Close dialog"]')!);
    expect(dialog()).not.toBeNull();
    const status = [...dialog()!.querySelectorAll('[role="status"]')].map(x => x.textContent);
    expect(status.some(t => t?.startsWith('Çalışıyor: seçilen tablolar düşürülüp birleşik yolda kuruluyor (en çok 12 dk)'))).toBe(true);
  });

  it('504 (geçit zaman aşımı): "sürüyor olabilir" uyarısı role=alert, ham HTML yok, kart yeniden ölçülür', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN) };
    routes[`POST ${APPLY}`] = { status: 504, raw: true, body: '<html><body><h1>504 Gateway Time-out</h1></body></html>' };
    const el = await mount(closedCheck());
    const dlg = await openAndAck(el, 'Yeniden kurulumu planla (10 tablo)', ACK10);
    await click(buttonNamed(dlg, 'Düşür ve yeniden kur'));
    const a = [...el.querySelectorAll('[role="alert"]')].find(x => x.textContent?.startsWith('Sunucudan cevap alınamadı'));
    expect(a?.textContent).toContain('(HTTP 504)');
    expect(a?.textContent).toContain('sürüyor olabilir');
    expect(a?.textContent).not.toContain('<html>');
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it('düşürme fazında kalan sonuç: hata metni görünür listede (yalnız title değil)', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN) };
    routes[`POST ${APPLY}`] = {
      body: okResult({
        ok: false, phase: 'drop', lockOpen: false, stillLegacy: ['ai_eval_runs'], note: '',
        resume: 'DROP yarıda kaldı. Yeniden ölç → planla → çalıştır: düşürülmüş tablolar kurulur, kalan eski tablolar düşürülür.',
        statements: [
          { head: 'DROP TABLE IF EXISTS ingest_ledger ON CLUSTER `uptrace_all` SYNC', ok: true },
          { head: 'DROP TABLE IF EXISTS ai_eval_runs ON CLUSTER `uptrace_all` SYNC', ok: false, err: 'Code: 999. Keeper exception' },
        ],
      }),
    };
    const el = await mount(closedCheck());
    const dlg = await openAndAck(el, 'Yeniden kurulumu planla (10 tablo)', ACK10);
    await click(buttonNamed(dlg, 'Düşür ve yeniden kur'));
    const list = el.querySelector('ul[aria-label="Koşan ifadeler"]');
    expect(list).not.toBeNull();
    expect(list!.closest('details')?.open).toBe(true);
    const bad = [...list!.querySelectorAll('li')].find(li => li.textContent?.startsWith('✗'));
    expect(bad?.textContent).toBe('✗ DROP TABLE IF EXISTS ai_eval_runs ON CLUSTER `uptrace_all` SYNC — Code: 999. Keeper exception');
    expect(el.textContent).toContain('Yarıda kaldı (düşürme): DROP yarıda kaldı.');
  });

  it('kart kilidin açılacağını sanıyor, TAZE plan kapalı diyor: diyalogdaki onay olmadan düğme kapalı; istek partialOK:true', async () => {
    routes[`POST ${PLAN}`] = { body: plan(TEN, { lockOpensAfter: false, stillLegacyAfter: ['alert_rules'] }) };
    routes[`POST ${APPLY}`] = { body: okResult({ lockOpen: false, stillLegacy: ['alert_rules'] }) };
    const el = await mount(closedCheck());
    expect(el.querySelector('[role="status"]')).toBeNull(); // kart kısmi onay istemiyor
    const dlg = await openAndAck(el, 'Yeniden kurulumu planla (10 tablo)', ACK10);
    expect(dlg.textContent).toContain('Kilit bu çalıştırmadan sonra da KAPALI: alert_rules');
    const go = buttonNamed(dlg, 'Düşür ve yeniden kur');
    expect(go.disabled).toBe(true);
    await click(checkboxNamed(dlg, 'Kilidin kapalı kalacağını anlıyorum'));
    expect(go.disabled).toBe(false);
    await click(go);
    expect(calls(APPLY)[0].body).toMatchObject({ partialOK: true, confirm: true });
  });

  it('tabloya özgü veri uyarıları yalnız o tablo düşecekse; plan uyarıları yinelenmez', async () => {
    const eight = TEN.slice(2);
    routes[`POST ${PLAN}`] = { body: plan(eight) };
    const el = await mount(closedCheck({ legacy: eight.map(t => legacyRow(t)) }));
    await click(buttonNamed(el, 'Yeniden kurulumu planla (8 tablo)'));
    const dlg = dialog()!;
    expect(dlg.textContent).not.toContain('silinen skorlar');
    expect(dlg.textContent).not.toContain('defter yazımları düşer');
    expect(dlg.textContent).not.toContain('Uyarı:');
    expect(dlg.textContent?.match(/Çalışırken Coremetry rollout/g)?.length).toBe(1);
  });

  it('düşürmesiz plan (yalnız eksik tablolar): "SİLMEYİ" ve "Düşür" yok, başlık ATLA satırını saymaz', async () => {
    const two = ['rollout_events', 'rollout_worker_runs'];
    const rows: CHStatePathRebuildTable[] = [
      ...two.map(t => ({ ...planRow(t, 0), state: 'absent' as const, action: 'create' as const })),
      { ...planRow('argocd_app_status', 0), state: 'unified' as const, action: 'skip' as const },
    ];
    routes[`POST ${PLAN}`] = { body: plan(two, { tables: rows, drops: [], lockOpensAfter: true, stillLegacyAfter: [] }) };
    routes[`POST ${APPLY}`] = { body: okResult() };
    const el = await mount(closedCheck({ lockOpen: true, legacy: two.map(t => ({ ...legacyRow(t), kind: 'absent', groups: [] })) }));
    await click(buttonNamed(el, 'Yeniden kurulumu planla (2 tablo)'));
    const dlg = dialog()!;
    expect(dlg.textContent).toContain('State tablolarını birleşik yola yeniden kur — 2 tablo');
    expect(dlg.textContent).not.toContain('SİLMEYİ');
    expect(dlg.textContent).toContain('Düşürülecek tablo yok');
    const go = buttonNamed(dlg, 'Birleşik yolda kur');
    expect(go.disabled).toBe(true);
    await click(checkboxNamed(dlg, '2 tabloyu birleşik yolda kurmayı onaylıyorum (silinecek veri yok).'));
    expect(go.disabled).toBe(false);
  });
});

// v0.10.965 — UI-7: yıkıcı çalıştırmadan sonraki otomatik yeniden ölçüm
// başarısız olursa blok (ve yarıda kaldı satırı) ekranda KALIR.
describe('ReplicaConsistencyPanel — yeniden ölçüm hatası sonucu silmez', () => {
  it('apply 200 (faz drop) → yeniden ölçüm 500: "Yarıda kaldı" satırı ve "ölçülemedi" rozeti birlikte', async () => {
    const report: CHReplicaConsistencyResponse = {
      cluster: 'uptrace_all', database: 'coremetry', loadBalancing: 'random', hosts: [], tables: [], generatedAt: 1,
      statePaths: closedCheck(),
    };
    let n = 0;
    routes['GET /api/admin/clickhouse/replica-consistency'] = () => (n++ === 0 ? { body: report } : { status: 500, body: { error: 'system.parts: timeout' } });
    routes[`POST ${PLAN}`] = { body: plan(TEN) };
    routes[`POST ${APPLY}`] = { body: okResult({ ok: false, phase: 'drop', note: '', resume: 'DROP yarıda kaldı. Yeniden ölç → planla → çalıştır.' }) };
    host = document.createElement('div');
    document.body.appendChild(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    act(() => {
      root = createRoot(host!);
      root.render(<QueryClientProvider client={qc}><MemoryRouter><ReplicaConsistencyPanel /></MemoryRouter></QueryClientProvider>);
    });
    await wait();
    const el = host!;
    await click(buttonNamed(el, 'Ölç'));
    expect([...el.querySelectorAll('.badge.b-err')].map(b => b.textContent)).toContain('kilit KAPALI · 10 state tablosu eski ZK yolunda');
    await click(buttonNamed(el, 'Yeniden kurulumu planla (10 tablo)'));
    await click(checkboxNamed(dialog()!, '10 tabloyu ve 2.478 satırı SİLMEYİ onaylıyorum; veri geri gelmez.'));
    await click(buttonNamed(dialog()!, 'Düşür ve yeniden kur'));
    await wait();
    expect(n).toBe(2); // yeniden ölçüm gerçekten koştu ve düştü
    const line = [...el.querySelectorAll('[role="alert"]')].find(a => a.textContent?.startsWith('Yarıda kaldı'));
    expect(line?.textContent).toBe('Yarıda kaldı (düşürme): DROP yarıda kaldı. Yeniden ölç → planla → çalıştır.');
    expect([...el.querySelectorAll('.badge.b-err')].map(b => b.textContent)).toContain('ölçülemedi');
  });
});

describe('StatePathBlock — kaynak kapıları', () => {
  const src = readFileSync(resolve(__dirname, 'StatePathRebuild.tsx'), 'utf8');
  it('ham <button>, yeni <table>, hex renk ve `as any` yok; yalnız atomlar', () => {
    expect(src).not.toMatch(/<button/);
    expect(src).not.toMatch(/<table/);
    expect(src).not.toMatch(/#[0-9a-fA-F]{3,6}\b/);
    expect(src).not.toMatch(/as any/);
    expect(src).toContain("from '@/components/ui'");
    expect(src).toContain('<Modal open size="lg"');
    expect(src).toContain('variant="danger"');
  });

  it('AdminClickhouse: blok ReplicaConsistencyPanel içinde, uyarı/notlardan sonra ve ana tablodan önce; satır etiketi', () => {
    const page = readFileSync(resolve(__dirname, '../AdminClickhouse.tsx'), 'utf8');
    expect(page).toContain("import { StatePathBlock } from './adminch/StatePathRebuild';");
    const panelAt = page.indexOf('function ReplicaConsistencyPanel(');
    const panelEnd = page.indexOf('\nfunction ', panelAt + 1);
    const panel = page.slice(panelAt, panelEnd);
    // v0.10.965 — yeniden ölçüm keepOnError ile: başarısız ölçüm yarıda kaldı satırını silmez.
    const mountSrc = '{data?.cluster && data.statePaths && <StatePathBlock check={data.statePaths} cluster={data.cluster} onDone={() => void scan(true)} />}';
    expect(panel).toContain(mountSrc);
    const notesAt = panel.indexOf('{data?.notes?.map(');
    const mountAt = panel.indexOf(mountSrc);
    const tableAt = panel.indexOf('<table>');
    expect(notesAt).toBeGreaterThan(0);
    expect(mountAt).toBeGreaterThan(notesAt);
    expect(tableAt).toBeGreaterThan(mountAt);
    expect(panel).toContain('{t.statePath && <div className="cell-hint">{statePathLabel(t.statePath)}</div>}');
    expect(panel).toContain('if (!keepOnError) setData(null)');
    expect(page.split('<StatePathBlock').length - 1).toBe(1);
  });

  it('api.ts: iki uç, confirm:true ve zaman aşımları; types: optional alanlar', () => {
    const apiSrc = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
    expect(apiSrc).toContain(`'${PLAN}'`);
    expect(apiSrc).toContain(`'${APPLY}'`);
    expect(apiSrc).toContain('JSON.stringify({ ...req, confirm: true }), timeoutMs: 780_000');
    expect(apiSrc).toContain('JSON.stringify({ tables }), timeoutMs: 120_000');
    const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(types).toContain('statePaths?: CHStatePathCheck');
    expect(types).toContain("statePath?: 'legacy' | 'mixed'");
  });
});
