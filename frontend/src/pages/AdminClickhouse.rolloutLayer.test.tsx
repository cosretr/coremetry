// @vitest-environment jsdom
// AdminClickhouse.rolloutLayer.test.tsx — v0.10.960 (Rollouts v2 P1.8,
// inceleme F2).
//
// Hata: P1.8 durum ucunun nesne listesine 0015'in sekiz state tablosunu
// ekledi (rollout_layer_admin.go RolloutLayerObjects = 0012'nin 17 nesnesi +
// 8 tablo). Kart listeyi TEK rozetle sayıyordu → 0012'si tam uygulanmış bir
// küme deploy'dan sonra EKSİK'e dönüyor, kartın tek "Uygula"sı 0012'yi
// bastığından UI'da bunu düzeltecek hiçbir şey yoktu (0015 yalnız API).
//
// Burada: 0012 rozeti YALNIZ 0012 nesnelerinden (hüküm öncekiyle aynı), 0015
// kendi rozeti + ön kontrol / uygula / geri al bloğu. fetch taklit edilir,
// api.ts GERÇEK — düğmeden çıkan istek (yol, yöntem, gövde) doğrulanır.
// Fikstürler Go kaynağından türetilir (0012 nesne listesi, çakışma mesajları)
// ki kart Go'nun gerçekte ürettiği metinle sınansın.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  ROLLOUT_V2_TABLES,
  type EntityLayerObjectStatus, type RolloutLayerStatusResult, type RolloutV2LayerPreflightResult,
  type RolloutV2LayerApplyResult, type RollupActionResult,
} from '@/lib/types';
import { ErrorBoundary } from '@/components/ErrorBoundary';
import { RolloutLayerWizardPanel } from './AdminClickhouse';

const GO = readFileSync(resolve(__dirname, '../../../internal/chstore/rollout_layer_admin.go'), 'utf8');
const goFunc = (name: string) => {
  const at = GO.search(new RegExp(`^func ${name}\\(`, 'm'));
  return GO.slice(at, GO.indexOf('\n}\n', at));
};

// 0012'nin 17 nesnesi, Go'daki sırayla (rolloutLayer0012Objects).
const OBJ_0012 = [...goFunc('rolloutLayer0012Objects').matchAll(/\{Name: "(\w+)", Kind: "(\w+)"(?:, Table: "(\w+)")?\}/g)]
  .map(m => ({ name: m[1], kind: m[2] as EntityLayerObjectStatus['kind'], table: m[3] }));

// Go rolloutV2LayerConflicts'in iki mesaj biçimi; %s sırayla doldurulur.
const CONFLICT_FMT = [...goFunc('rolloutV2LayerConflicts').matchAll(/fmt\.Sprintf\("([^"]+)"/g)].map(m => m[1]);
const fill = (fmt: string, ...args: string[]) => { let i = 0; return fmt.replace(/%s/g, () => args[i++]); };
const plainCopyConflict = (host: string, table: string) =>
  fill(CONFLICT_FMT.find(f => f.includes('motor %s'))!, host, table, 'ReplacingMergeTree');
const zkConflict = (host: string, table: string) =>
  fill(CONFLICT_FMT.find(f => f.includes('ZK yolu'))!, host, table, `/clickhouse/tables/{shard}/${table}`, `/clickhouse/tables/state/${table}`);

type State = EntityLayerObjectStatus['state'];
const HOSTS = 4;
const st = (o: { name: string; kind: EntityLayerObjectStatus['kind']; table?: string }, state: State): EntityLayerObjectStatus => ({
  ...o, hosts: HOSTS, haveHosts: state === 'ok' ? HOSTS : state === 'partial' ? 2 : 0, state,
});
const status = (s0012: State, s0015: State | ((t: string) => State)): RolloutLayerStatusResult => ({
  cluster: 'uptrace_all', activityRows: 0, generated: 1,
  objects: [
    ...OBJ_0012.map(o => st(o, s0012)),
    ...ROLLOUT_V2_TABLES.map(t => st({ name: t, kind: 'table' }, typeof s0015 === 'function' ? s0015(t) : s0015)),
  ],
});
const pre15 = (over: Partial<RolloutV2LayerPreflightResult> = {}): RolloutV2LayerPreflightResult => ({
  clusters: ['dev_all', 'uptrace_all'], suggestedCluster: 'uptrace_all', cluster: 'uptrace_all',
  spansLocal: true, bootManaged: false, conflicts: [], supported: true, installed: false, missing: [],
  detail: 'uygulanabilir: sekiz Rollouts v2 state tablosu', generated: 1, ...over,
});

// v0.10.975 — "kurulu" hükmünün metni Go sabitinden (rolloutV2LayerInstalledDetail).
const INSTALLED_DETAIL = GO.match(/const rolloutV2LayerInstalledDetail = "([^"]+)"/)?.[1] ?? '';
const preInstalled = (over: Partial<RolloutV2LayerPreflightResult> = {}) =>
  pre15({ installed: true, detail: INSTALLED_DETAIL, ...over });

// ── fetch yönlendirici: `YÖNTEM /yol` → cevap; api.ts gerçek ────────────
type Reply = { status?: number; body: unknown } | 'hang';
let routes: Record<string, Reply | ((url: string) => Reply)> = {};
let seen: { method: string; url: string; body: unknown }[] = [];
function stubFetch() {
  vi.stubGlobal('fetch', (url: unknown, init?: RequestInit) => {
    const u = String(url);
    const method = init?.method ?? 'GET';
    seen.push({ method, url: u, body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined });
    const r0 = routes[`${method} ${u.split('?')[0]}`];
    const r: Reply = typeof r0 === 'function' ? r0(u) : r0 ?? { status: 404, body: { error: `rota yok: ${u}` } };
    if (r === 'hang') return new Promise<Response>(() => {});
    const code = r.status ?? 200;
    return Promise.resolve(new Response(JSON.stringify(r.body), { status: code, headers: { 'content-type': 'application/json' } }));
  });
}
const calls = (method: string, path: string) => seen.filter(s => s.method === method && s.url.split('?')[0] === path);

const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
let host: HTMLElement | null = null;
let root: Root | null = null;
// v0.10.975 — boundary: AppShell'in rota başı ErrorBoundary'si gibi sarar;
// render hatası kartı söküp "Something went wrong" yedeğine düşürür.
async function mount({ boundary = false }: { boundary?: boolean } = {}): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const panel = <RolloutLayerWizardPanel />;
  act(() => {
    root = createRoot(host!);
    root.render(<QueryClientProvider client={qc}><MemoryRouter>{boundary ? <ErrorBoundary>{panel}</ErrorBoundary> : panel}</MemoryRouter></QueryClientProvider>);
  });
  await wait();
  return host!;
}

const group = (el: HTMLElement, id: '0012' | '0015') => {
  const g = el.querySelector<HTMLElement>(`[role="group"][aria-label^="${id}"]`);
  expect(g, `${id} grubu yok`).not.toBeNull();
  return g!;
};
/** Grubun durum satırındaki tamlık rozeti (tablo içindeki VAR/YOK hariç). */
const completeBadge = (g: HTMLElement) => [...g.querySelectorAll('.badge')]
  .filter(b => !b.closest('table'))
  .map(b => b.textContent)
  .filter(t => t === 'TAM' || t === 'EKSİK');
const tableNames = (g: HTMLElement) => [...g.querySelectorAll('tbody tr:not([data-dt-state]) td:first-child')].map(td => td.textContent);
const btn = (g: HTMLElement, label: string) => {
  const b = [...g.querySelectorAll('button')].find(x => x.textContent?.trim() === label);
  expect(b, `"${label}" düğmesi yok`).toBeDefined();
  return b!;
};
const click = async (b: HTMLElement) => { await act(async () => { b.click(); }); await wait(); };
const select = (g: HTMLElement) => g.querySelector('select')!;
/** KeyValue satırının değeri (`dl > div > dt + dd`). */
const kvValue = (g: HTMLElement, label: string) => {
  const row = [...g.querySelectorAll('.keyval__row')].find(r => r.querySelector('dt')?.textContent === label);
  expect(row, `"${label}" satırı yok`).toBeDefined();
  return row!.querySelector('dd')!.textContent ?? '';
};
const choose = async (s: HTMLSelectElement, v: string) => {
  await act(async () => { s.value = v; s.dispatchEvent(new Event('change', { bubbles: true })); });
  await wait();
};

beforeEach(() => {
  routes = {
    'GET /api/admin/rollout-layer/status': { body: status('ok', 'missing') },
  };
  seen = [];
  stubFetch();
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  vi.unstubAllGlobals();
});

describe('Rollouts katmanı kartı — durum 0012 / 0015 diye bölünür (F2)', () => {
  it('fikstür Go\'dan okundu', () => {
    expect(OBJ_0012).toHaveLength(17);
    expect(CONFLICT_FMT).toHaveLength(2);
  });

  it('0012 tam + v2 tabloları yok → 0012 TAM (eski hüküm), 0015 EKSİK', async () => {
    const el = await mount();
    expect(el.textContent).toContain('0012 + 0015');
    expect(completeBadge(group(el, '0012'))).toEqual(['TAM']);
    expect(completeBadge(group(el, '0015'))).toEqual(['EKSİK']);
  });

  it('her grup kendi nesnelerini listeler', async () => {
    const el = await mount();
    const n12 = tableNames(group(el, '0012'));
    const n15 = tableNames(group(el, '0015'));
    expect(n12).toHaveLength(17);
    expect([...n15].sort()).toEqual([...ROLLOUT_V2_TABLES].sort());
    for (const t of ROLLOUT_V2_TABLES) expect(n12).not.toContain(t);
  });

  it('0015 rozeti yalnız v2 tablolarından: hepsi VAR → TAM', async () => {
    routes['GET /api/admin/rollout-layer/status'] = { body: status('ok', 'ok') };
    const el = await mount();
    expect(completeBadge(group(el, '0012'))).toEqual(['TAM']);
    expect(completeBadge(group(el, '0015'))).toEqual(['TAM']);
  });

  it('bir v2 tablosu KISMİ → 0015 EKSİK, 0012 etkilenmez', async () => {
    routes['GET /api/admin/rollout-layer/status'] = { body: status('ok', t => (t === 'argocd_sync_events' ? 'partial' : 'ok')) };
    const el = await mount();
    expect(completeBadge(group(el, '0012'))).toEqual(['TAM']);
    expect(completeBadge(group(el, '0015'))).toEqual(['EKSİK']);
  });

  it('0012 eksik, v2 tam → 0012 EKSİK, 0015 TAM (ters yön)', async () => {
    routes['GET /api/admin/rollout-layer/status'] = { body: status('missing', 'ok') };
    const el = await mount();
    expect(completeBadge(group(el, '0012'))).toEqual(['EKSİK']);
    expect(completeBadge(group(el, '0015'))).toEqual(['TAM']);
  });
});

describe('0015 bloğu — ön kontrol / uygula / geri al (v0.10.960)', () => {
  it('ön kontrolden önce: boş durum ipucu, Uygula ve Geri al kapalı', async () => {
    const g = group(await mount(), '0015');
    expect(g.textContent).toContain('Ön kontrol henüz koşmadı');
    expect(btn(g, 'Uygula (0015)').disabled).toBe(true);
    expect(btn(g, 'Tabloları geri al (0015)').disabled).toBe(true);
    expect(calls('GET', '/api/admin/rollout-layer/preflight-0015')).toHaveLength(0); // pahalı probe: yalnız düğmeyle
  });

  it('küme seçimi suggestedCluster\'dan tohumlanır; ilk ön kontrol parametresiz', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(calls('GET', '/api/admin/rollout-layer/preflight-0015').map(c => c.url)).toEqual(['/api/admin/rollout-layer/preflight-0015']);
    expect(select(g).value).toBe('uptrace_all');
    expect([...select(g).options].map(o => o.value)).toEqual(['', 'dev_all', 'uptrace_all']);
    // Yeniden koşunca seçili küme için sorar.
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(calls('GET', '/api/admin/rollout-layer/preflight-0015').map(c => c.url).at(-1))
      .toBe('/api/admin/rollout-layer/preflight-0015?cluster=uptrace_all');
  });

  it('suggestedCluster tanımsız + tek küme → o küme', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({ clusters: ['only_one'], suggestedCluster: undefined, cluster: '', supported: false, detail: 'küme seçilmedi' }) };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(select(g).value).toBe('only_one');
  });

  it('Uygula (0015) yalnız UYGULANABİLİR ön kontrolde ve o kümede açık', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({ supported: false, detail: 'spans_local yok — bu kurulum tek düğüm', spansLocal: false }) };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(g.textContent).toContain('UYGULANAMAZ');
    expect(btn(g, 'Uygula (0015)').disabled).toBe(true);

    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(g.textContent).toContain('UYGULANABİLİR');
    expect(btn(g, 'Uygula (0015)').disabled).toBe(false);

    // Ön kontrolün koşmadığı küme seçilince kapanır (çakışma probe'u kümeye özgü).
    await choose(select(g), 'dev_all');
    expect(btn(g, 'Uygula (0015)').disabled).toBe(true);
    expect(g.textContent).toContain("seçili küme için Ön kontrol (0015)'ü yeniden koş");
  });

  it('uygula: onay → POST apply-0015 {cluster}; sonuç + not; durum ve ön kontrol tazelenir', async () => {
    const res: RolloutV2LayerApplyResult = { ok: true, note: 'tablolar boş doğar', statements: [{ head: 'CREATE TABLE IF NOT EXISTS rollout_events', ok: true }] };
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
    routes['POST /api/admin/rollout-layer/apply-0015'] = { body: res };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    const statusBefore = calls('GET', '/api/admin/rollout-layer/status').length;
    await click(btn(g, 'Uygula (0015)'));
    expect(calls('POST', '/api/admin/rollout-layer/apply-0015')).toHaveLength(0); // onaysız basılmaz
    expect(g.textContent).toContain('ReplicatedReplacingMergeTree');
    await click(btn(g, 'Evet'));
    expect(calls('POST', '/api/admin/rollout-layer/apply-0015').map(c => c.body)).toEqual([{ cluster: 'uptrace_all' }]);
    expect(calls('POST', '/api/admin/rollout-layer/apply')).toHaveLength(0); // 0012 ucu değil
    expect(g.textContent).toContain('TAMAM');
    expect(g.textContent).toContain('tablolar boş doğar');
    expect(g.textContent).toContain('CREATE TABLE IF NOT EXISTS rollout_events');
    expect(calls('GET', '/api/admin/rollout-layer/status').length).toBeGreaterThan(statusBefore);
    expect(calls('GET', '/api/admin/rollout-layer/preflight-0015').at(-1)?.url).toBe('/api/admin/rollout-layer/preflight-0015?cluster=uptrace_all');
  });

  it('uygula 409 (sunucu ön kontrolü reddetti): gövdedeki hata metni, ham "HTTP 409" değil', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
    routes['POST /api/admin/rollout-layer/apply-0015'] = { status: 409, body: { error: 'ön kontrol geçmedi — 1 çakışma' } };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    await click(btn(g, 'Uygula (0015)'));
    await click(btn(g, 'Evet'));
    expect(g.textContent).toContain('ön kontrol geçmedi — 1 çakışma');
    expect(g.textContent).not.toContain('HTTP 409');
  });

  it('geri al: ghost-danger; onay argocd_sync_events kaybını söyler; Vazgeç basmaz; Evet confirm:true gönderir', async () => {
    const res: RollupActionResult = { ok: true, statements: [{ head: 'DROP TABLE IF EXISTS rollout_worker_runs', ok: true }] };
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({ supported: false, detail: '1 çakışma' }) };
    routes['POST /api/admin/rollout-layer/rollback-0015'] = { body: res };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    const rb = btn(g, 'Tabloları geri al (0015)');
    expect(rb.className).toContain('ghost-danger');
    expect(rb.disabled).toBe(false); // geri alma UYGULANAMAZ ön kontrolde de açık (0012 gibi)

    await click(rb);
    // İnceleme: geri alınamaz kayıp uyarısı ekran okuyucuya duyurulur
    // (role=alert) ve kapatılacak yazıcıları adıyla söyler (0015 rollback başlığı).
    const alert = g.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain('argocd_sync_events tarihçesi geri gelmez');
    expect(alert?.textContent).toContain('source=v1');
    expect(alert?.textContent).toContain('Argo CD / zenginleştirme kapalı');
    expect(alert?.textContent).toContain('UNKNOWN_TABLE');
    expect(btn(g, 'Evet').className).toContain('danger');
    // Açan düğme söküldü → odak body'ye düşmez, Vazgeç'e gider (ConfirmDialog sözleşmesi 4).
    expect(document.activeElement).toBe(btn(g, 'Vazgeç'));
    await click(btn(g, 'Vazgeç'));
    expect(g.textContent).not.toContain('argocd_sync_events tarihçesi geri gelmez');
    expect(calls('POST', '/api/admin/rollout-layer/rollback-0015')).toHaveLength(0);
    // Vazgeç → odak açan düğmeye döner (Modal'ın odak iadesi gibi).
    expect(document.activeElement).toBe(btn(g, 'Tabloları geri al (0015)'));

    await click(btn(g, 'Tabloları geri al (0015)'));
    await click(btn(g, 'Evet'));
    expect(calls('POST', '/api/admin/rollout-layer/rollback-0015').map(c => c.body)).toEqual([{ cluster: 'uptrace_all', confirm: true }]);
    expect(calls('POST', '/api/admin/rollout-layer/rollback')).toHaveLength(0); // 0012'nin MV geri alması değil
    expect(g.textContent).toContain('DROP TABLE IF EXISTS rollout_worker_runs');
  });

  it('düz boot kopyası çakışması → ne yapılacağı ipucu; yalnız ZK çakışmasında ipucu yok', async () => {
    const zk = zkConflict('ch-2', 'argocd_app_status');
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({ supported: false, detail: '1 çakışma', conflicts: [zk] }) };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(g.textContent).toContain(zk);
    expect(g.textContent).not.toContain('ON CLUSTER olmadan');

    const plain = plainCopyConflict('ch-1', 'rollout_events');
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({ supported: false, detail: '2 çakışma', conflicts: [plain, zk] }) };
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(g.textContent).toContain(plain);
    expect(g.textContent).toContain('ON CLUSTER olmadan');
    expect(g.textContent).toContain('0015_rollouts_v2_rollback.sql');
    // İnceleme: Uygula ön kontrol UYGULANABİLİR olmadan açılmaz — "DROP'tan
    // hemen sonra Uygula" talimatı arada Ön kontrol adımını söylemeli.
    expect(g.textContent).toContain('Ön kontrol (0015) → Uygula (0015)');
  });

  it('boot yönetimi ve probe hataları görünür', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({ bootManaged: true, supported: false, detail: 'probe hatası', probeErrors: ['ZK yolları: timeout'] }) };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(g.textContent).toContain('BOOT YÖNETİYOR');
    expect(g.textContent).toContain('ZK yolları: timeout');
  });

  // İnceleme: motor / ZK probe'u hata verince Go o dilimi nil geçer →
  // conflicts [] ama çakışma kontrolü HİÇ koşmadı. Satır "✓" dememeli.
  it('probe hatası + çakışma listesi boş → "Çakışma yok" satırı ✓ değil, doğrulanamadı', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({
      supported: false, detail: 'probe hatası — emin olamadığımız kümeye DDL basmıyoruz', conflicts: [],
      probeErrors: ['tablo motorları: code: 279, ALL_CONNECTION_TRIES_FAILED', 'ZK yolları: code: 279, ALL_CONNECTION_TRIES_FAILED'],
    }) };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    const v = kvValue(g, 'Çakışma yok (host başına motor + ZK yolu)');
    expect(v).not.toContain('✓');
    expect(v).toContain('probe hatası — doğrulanamadı');
    // Probe temiz + çakışma yok → ✓ (kontrol koştu ve geçti).
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(kvValue(g, 'Çakışma yok (host başına motor + ZK yolu)')).toContain('✓');
  });

  // İnceleme: onay açıkken küme seçici / Ön kontrol açık kalıyordu; Evet
  // o anki kümeye — ön kontrolü hiç koşmamış olsa da — POST atıyordu.
  it('onay açıkken küme seçici ve Ön kontrol kilitli; Evet ön kontrolsüz kümeye gitmez', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
    routes['POST /api/admin/rollout-layer/apply-0015'] = { body: { ok: true, note: '', statements: [] } };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    await click(btn(g, 'Uygula (0015)'));
    expect(document.activeElement).toBe(btn(g, 'Vazgeç'));
    expect(select(g).disabled).toBe(true);
    expect(btn(g, 'Ön kontrol (0015)').disabled).toBe(true);
    expect(btn(g, 'Evet').disabled).toBe(false);
    // İkinci kapı: jsdom'da kilitli <select>'e programatik change yine
    // onChange'e ulaşır → seçim ön kontrolsüz dev_all olur; Evet Uygula'nın
    // kapısına (UYGULANABİLİR + aynı küme + tazelenmiyor) bağlı → kapanır.
    await choose(select(g), 'dev_all');
    expect(select(g).value).toBe('dev_all');
    expect(btn(g, 'Evet').disabled).toBe(true);
    await click(btn(g, 'Evet'));
    expect(calls('POST', '/api/admin/rollout-layer/apply-0015')).toHaveLength(0);
    expect(calls('GET', '/api/admin/rollout-layer/preflight-0015')).toHaveLength(1);
  });

  // İnceleme: yeniden koşunca seçimi suggestedCluster'a geri çeken bir
  // mutant (Uygula önerilen küme dışında HİÇ açılmazdı) testlerden geçiyordu:
  // rota ?cluster='ı yok sayıyordu. Burada rota istenen kümeyi yankılar.
  it('seçilen küme için yeniden koşan ön kontrol seçimi korur → Uygula o kümeye açılır ve gider', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = (url: string) => ({
      body: pre15({ cluster: new URL(url, 'http://x').searchParams.get('cluster') ?? 'uptrace_all' }),
    });
    routes['POST /api/admin/rollout-layer/apply-0015'] = { body: { ok: true, note: '', statements: [] } };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    await choose(select(g), 'dev_all');
    expect(btn(g, 'Uygula (0015)').disabled).toBe(true);
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(calls('GET', '/api/admin/rollout-layer/preflight-0015').at(-1)?.url).toBe('/api/admin/rollout-layer/preflight-0015?cluster=dev_all');
    expect(select(g).value).toBe('dev_all');
    expect(g.textContent).not.toContain('yeniden koş');
    expect(btn(g, 'Uygula (0015)').disabled).toBe(false);
    await click(btn(g, 'Uygula (0015)'));
    await click(btn(g, 'Evet'));
    expect(calls('POST', '/api/admin/rollout-layer/apply-0015').map(c => c.body)).toEqual([{ cluster: 'dev_all' }]);
  });

  // İnceleme: pre.cluster boşken ipucu "ön kontrol — için koştu" diyordu.
  it('ön kontrol kümesiz koştuysa bayat ipucu bunu söyler ("— için" değil)', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({
      suggestedCluster: undefined, cluster: '', supported: false, detail: 'küme seçilmedi — DDL `ON CLUSTER` yazıyor',
    }) };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(select(g).value).toBe('');
    await choose(select(g), 'dev_all');
    expect(g.textContent).toContain("ön kontrol küme seçilmeden koştu — seçili küme için Ön kontrol (0015)'ü yeniden koş");
    expect(g.textContent).not.toContain('— için koştu');
  });

  // İnceleme: elle <label>+<select> yerine SelectField — etiket bağı ve
  // Uygula'nın neden kapalı olduğunu söyleyen metin seçiciye bağlı.
  it('küme seçici etiketli; bayat gerekçe seçiciye ve Uygula\'ya aria-describedby ile bağlı', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    const s = select(g);
    expect(s.id).not.toBe('');
    expect(g.querySelector(`label[for="${s.id}"]`)?.textContent).toBe('Küme');
    expect(s.getAttribute('aria-describedby')).toBeNull();
    await choose(s, 'dev_all');
    const hintId = select(g).getAttribute('aria-describedby');
    expect(hintId).toBeTruthy();
    expect(document.getElementById(hintId!)?.textContent).toContain('uptrace_all için koştu');
    expect(btn(g, 'Uygula (0015)').getAttribute('aria-describedby')).toBe(hintId);
  });

  // v0.10.975 — onaylı kalan iş: sekiz tablo her host'ta Replicated + birleşik
  // yoldayken (v0.10.965 / v0.10.971'den beri NORMAL durum) kart yeşil
  // UYGULANABİLİR diyor, Uygula açık kalıyordu (basmak IF NOT EXISTS no-op).
  // Artık: nötr KURULU hükmü, Uygula kapalı + gerekçe aria-describedby ile
  // bağlı; geri alma değişmedi. Sunucu kapısı değişmedi (Go testi pinler).
  describe('kurulu / kısmi kurulum (v0.10.975)', () => {
    it('hüküm metni Go sabitinden okundu', () => {
      expect(INSTALLED_DETAIL).toBe("Kurulu: sekiz tablo her host'ta birleşik yolda — uygulama gerekmiyor");
    });

    it('kurulu → nötr KURULU (yeşil yok), Uygula kapalı + gerekçe bağlı, geri al açık', async () => {
      routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: preInstalled() };
      const g = group(await mount(), '0015');
      await click(btn(g, 'Ön kontrol (0015)'));
      expect(g.textContent).toContain(INSTALLED_DETAIL);
      const verdict = [...g.querySelectorAll('.badge')].find(b => b.textContent === 'KURULU');
      expect(verdict, 'KURULU rozeti yok').toBeDefined();
      expect(verdict!.className).toContain('b-gray');
      expect(g.textContent).not.toContain('UYGULANABİLİR');
      // Palet kuralı: normal durum renk taşımaz — çerçevede --ok / --ok-bg, rozette b-ok yok.
      expect(g.querySelector('.badge.b-ok')).toBeNull();
      expect(g.querySelector('[style*="var(--ok"]')).toBeNull();

      const apply = btn(g, 'Uygula (0015)');
      expect(apply.disabled).toBe(true);
      const reasonId = apply.getAttribute('aria-describedby');
      expect(reasonId).toBeTruthy();
      expect(document.getElementById(reasonId!)?.textContent).toBe(INSTALLED_DETAIL);
      // Geri alma kurulu kümede de açık (davranış değişmedi).
      expect(btn(g, 'Tabloları geri al (0015)').disabled).toBe(false);
      await click(apply);
      expect(g.querySelector('[role="alert"]')).toBeNull(); // kapalı düğme onay açmaz
      expect(calls('POST', '/api/admin/rollout-layer/apply-0015')).toHaveLength(0);
    });

    it('kısmi kurulum → UYGULANABİLİR, Uygula açık (gerekçe yok), eksik listesi görünür', async () => {
      const missing = ['host-3: argocd_app_mapping', 'host-4: sekizi de yok'];
      routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({
        detail: "uygulanabilir — kısmi kurulum: 2 host'ta eksik tablo; Uygula (0015) eksikleri birleşik yola kurar", missing,
      }) };
      routes['POST /api/admin/rollout-layer/apply-0015'] = { body: { ok: true, note: '', statements: [] } };
      const g = group(await mount(), '0015');
      await click(btn(g, 'Ön kontrol (0015)'));
      expect(g.textContent).toContain('UYGULANABİLİR');
      expect(g.textContent).not.toContain('KURULU');
      const items = [...g.querySelectorAll('li')].map(li => li.textContent);
      for (const m of missing) expect(items).toContain(m);
      const apply = btn(g, 'Uygula (0015)');
      expect(apply.disabled).toBe(false);
      expect(apply.getAttribute('aria-describedby')).toBeNull();
      await click(apply);
      await click(btn(g, 'Evet'));
      expect(calls('POST', '/api/admin/rollout-layer/apply-0015').map(c => c.body)).toEqual([{ cluster: 'uptrace_all' }]);
    });

    it('taze küme (eksik listesi boş) → liste yok, Uygula açık', async () => {
      routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15() };
      const g = group(await mount(), '0015');
      await click(btn(g, 'Ön kontrol (0015)'));
      expect(g.textContent).not.toContain('Eksik');
      expect(btn(g, 'Uygula (0015)').disabled).toBe(false);
    });

    it('kurulu ama başka küme seçildi → gerekçe bayat ipucu (hüküm o kümenin değil)', async () => {
      routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: preInstalled() };
      const g = group(await mount(), '0015');
      await click(btn(g, 'Ön kontrol (0015)'));
      await choose(select(g), 'dev_all');
      const apply = btn(g, 'Uygula (0015)');
      expect(apply.disabled).toBe(true);
      expect(document.getElementById(apply.getAttribute('aria-describedby')!)?.textContent).toContain('uptrace_all için koştu');
    });

    // v0.10.975 (inceleme F1) — rolling deploy: yeni paketin isteği henüz eski
    // (v0.10.960–976) pod'a düşer; gövdede `missing` / `installed` anahtarı yok.
    // Kart `pre.missing.length`'te TypeError atıp sayfayı ErrorBoundary
    // yedeğine düşürüyordu. Eski gövde = eski hüküm: UYGULANABİLİR, Uygula açık.
    it('eski sunucu gövdesi (missing / installed yok) → çökmez, UYGULANABİLİR, Uygula açık', async () => {
      const { missing: _m, installed: _i, ...old } = pre15();
      expect(Object.keys(old)).not.toContain('missing');
      routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: old };
      const el = await mount({ boundary: true });
      await click(btn(group(el, '0015'), 'Ön kontrol (0015)'));
      expect(el.textContent).not.toContain('Something went wrong');
      const g = group(el, '0015');
      expect(g.textContent).toContain('UYGULANABİLİR');
      expect(g.textContent).not.toContain('KURULU');
      expect(g.textContent).not.toContain('Eksik —');
      expect(select(g).value).toBe('uptrace_all');
      const apply = btn(g, 'Uygula (0015)');
      expect(apply.disabled).toBe(false);
      expect(apply.getAttribute('aria-describedby')).toBeNull();
    });

    it('kurulu → yeniden ön kontrol kısmi dönerse Uygula açılır', async () => {
      routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: preInstalled() };
      const g = group(await mount(), '0015');
      await click(btn(g, 'Ön kontrol (0015)'));
      expect(btn(g, 'Uygula (0015)').disabled).toBe(true);
      routes['GET /api/admin/rollout-layer/preflight-0015'] = { body: pre15({ missing: ['host-4: sekizi de yok'] }) };
      await click(btn(g, 'Ön kontrol (0015)'));
      expect(btn(g, 'Uygula (0015)').disabled).toBe(false);
      expect(g.textContent).not.toContain(INSTALLED_DETAIL);
    });
  });

  it('ön kontrol yükleniyor → düğme meşgul; hata → mesaj, kutu yok', async () => {
    routes['GET /api/admin/rollout-layer/preflight-0015'] = 'hang';
    const g = group(await mount(), '0015');
    await click(btn(g, 'Ön kontrol (0015)'));
    expect(btn(g, 'Ön kontrol (0015)').getAttribute('aria-busy')).toBe('true');
    expect(g.textContent).not.toContain('Ön kontrol henüz koşmadı');
    act(() => root!.unmount()); host?.remove(); root = null;

    routes['GET /api/admin/rollout-layer/preflight-0015'] = { status: 500, body: { error: 'system.clusters: boom' } };
    const g2 = group(await mount(), '0015');
    await click(btn(g2, 'Ön kontrol (0015)'));
    expect(g2.textContent).toContain('system.clusters: boom');
    expect(g2.textContent).not.toContain('UYGULANABİLİR');
    expect(g2.textContent).not.toContain('UYGULANAMAZ');
  });
});

// İnceleme: 0015 bloğu 0012'nin satır içi stil bloklarını kopyalıyordu
// (merdiven dışı 12.5 / 11.5 punto, elle color-mix zemin). İskelet artık
// tek kopya (Wiz* alt bileşenleri) — kart bölümünde ön kontrol çerçevesi
// BİR kez yazılır. Kaynak-okuma pini; kart dosyanın sonundaki bölüm.
describe('Rollouts kartı iskeleti — 0012 / 0015 ortak, token merdiveninde', () => {
  const RAW = readFileSync(resolve(__dirname, './AdminClickhouse.tsx'), 'utf8');
  const at = RAW.indexOf('// ── Rollouts katmanı kartı: 0012 + 0015');
  // Yorumlar soyulur (k5PagesAM deseni): gerekçe metnindeki "color-mix" pini yanıltmasın.
  const card = RAW.slice(at)
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '');

  // v0.10.975 — çerçevenin üçüncü tonu: `settled` (0015 KURULU) nötr —
  // b-gray rozet, --border / --bg2 zemin; yeşil yalnız "yapılacak iş var" hükmünde.
  it('ön kontrol çerçevesi tek kopya; zemin --ok-bg / --warn-bg, yerleşik hüküm nötr', () => {
    expect(at).toBeGreaterThan(0);
    expect(card.match(/pre\.supported \? 'b-ok' : 'b-warn'/g)).toHaveLength(1);
    expect(card.match(/border: `1px solid \$\{pre\.supported/g)).toHaveLength(1);
    expect(card).toContain("background: pre.supported ? 'var(--ok-bg)' : 'var(--warn-bg)'");
    expect(card).not.toContain('color-mix');
    // Yerleşik (settled) dal yeşile hiç girmez: nötr çerçeve + gri rozet.
    expect(card).toContain("const tone = settled ? { border: '1px solid var(--border)', background: 'var(--bg2)' } : {");
    expect(card.match(/settled \? 'b-gray' : pre\.supported \? 'b-ok' : 'b-warn'/g)).toHaveLength(1);
  });

  it('merdiven dışı punto yok', () => {
    expect(card).not.toMatch(/fontSize: 1\d\.5/);
  });
});
