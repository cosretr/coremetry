// @vitest-environment jsdom
// AutoWizard.test.tsx — v0.10.2410: kurulum sihirbazlarının tek kalıbı.
// Çiviler: iş yoksa kart YOK (boş kart değil); bekleyen işte otomatik ön
// kontrol + tek "Çalıştır" + katlanır SQL listesi; ağır iş kutusu varsayılan
// kapalı; engelleyici kontrol koşuyu kapatır; adaptörlerin saf parçaları ve
// sıralı koşu (ilk hatada durur, biten adım atlanır).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { AutoWizard, type AutoWizardProps, type WizardProbe } from './AutoWizard';
import { attrKVHProbe, backfillGapDays, objectsPlan, pickCluster, rollout0012State, rollupWizard, skipMatProbe } from './wizards';
import type { AttrKVHAliasStatus, EntityLayerObjectStatus, SkipMatJob } from '@/lib/types';

let host: HTMLElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; vi.unstubAllGlobals(); });
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

const base: WizardProbe = { applicable: true, pending: true, checks: [{ name: 'host erişimi', ok: true, detail: '2/2', blocking: true }], plan: [{ title: 'adım 1', lines: ['ALTER TABLE spans_local ON CLUSTER c1 ADD COLUMN IF NOT EXISTS x'] }], cluster: 'c1' };

type RunFn = AutoWizardProps['run'];
async function mount(probe: WizardProbe, run: ReturnType<typeof vi.fn<RunFn>> = vi.fn<RunFn>(async () => ({ ok: true, log: [] }))) {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(<AutoWizard title="T" intro="i" probe={async () => probe} run={run} confirmText="koşacak."
      heavy={{ label: 'eski veriye de indeks uygula — ağır I/O', hint: 'h' }} />);
  });
  await wait();
  return { el: host!, run };
}
const button = (el: HTMLElement, text: string) => [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === text);

describe('AutoWizard görünürlük', () => {
  it('iş yoksa kart hiç yok', async () => {
    const { el } = await mount({ ...base, pending: false });
    expect(el.innerHTML).toBe('');
  });
  it('kurulum şekli işi gerektirmiyorsa (boot yönetiyor) kart yok', async () => {
    const { el } = await mount({ ...base, applicable: false });
    expect(el.innerHTML).toBe('');
  });
  it('bekleyen iş → ön kontrol + katlanır SQL + tek Çalıştır; ağır kutu kapalı', async () => {
    const { el } = await mount(base);
    expect(el.textContent).toContain('host erişimi');
    expect(el.querySelector('details summary')?.textContent).toContain('Koşacak işler (1)');
    expect(el.querySelector('details li')?.textContent).toContain('ADD COLUMN IF NOT EXISTS');
    expect([...el.querySelectorAll('button')].filter(b => /Çalıştır|Uygula|Ön kontrol|Geri al/.test(b.textContent ?? '')).map(b => b.textContent?.trim())).toEqual(['Çalıştır']);
    const cb = el.querySelector<HTMLElement>('[role="checkbox"], input[type="checkbox"]');
    expect(cb?.getAttribute('aria-checked') ?? String((cb as HTMLInputElement | null)?.checked)).toBe('false');
  });
  it('engelleyici kontrol Çalıştır\'ı kapatır ve nedeni yazar', async () => {
    const { el } = await mount({ ...base, blocker: 'host erişimi: 1/2' });
    expect(button(el, 'Çalıştır')?.disabled).toBe(true);
    expect(el.textContent).toContain('host erişimi: 1/2');
  });
  it('Çalıştır → onay → run (ağır kapalı) → sonuç', async () => {
    const run = vi.fn<RunFn>(async () => ({ ok: false, failedStep: 'legacy', err: 'boom', log: [{ head: 'ALTER …', ok: false, err: 'boom' }] }));
    const { el } = await mount(base, run);
    await act(async () => { button(el, 'Çalıştır')!.click(); });
    expect(el.textContent).toContain('Emin misin?');
    await act(async () => { button(el, 'Evet')!.click(); });
    await wait();
    expect(run).toHaveBeenCalledTimes(1);
    expect(run.mock.calls[0][0]).toMatchObject({ cluster: 'c1', heavy: false });
    expect(el.textContent).toContain('DURDU');
    expect(el.textContent).toContain('legacy');
  });
});

describe('adaptörlerin saf parçaları', () => {
  it('pickCluster', () => {
    expect(pickCluster('a', ['a', 'b'])).toBe('a');
    expect(pickCluster('x', ['b'])).toBe('b');
    expect(pickCluster(undefined, ['a', 'b'])).toBe('');
  });
  const o = (name: string, kind: EntityLayerObjectStatus['kind'], state: EntityLayerObjectStatus['state']): EntityLayerObjectStatus => ({ name, kind, state, hosts: 2, haveHosts: state === 'ok' ? 2 : state === 'partial' ? 1 : 0, table: 'spans_local' });
  it('objectsPlan yalnız eksikleri yazar', () => {
    expect(objectsPlan([o('a', 'column', 'ok'), o('b', 'index', 'partial')])).toEqual(['index spans_local.b — 1/2 host']);
  });
  it('0012: MV kapısı kapalıyken yalnız MV eksikse koşulamaz', () => {
    expect(rollout0012State([o('a', 'column', 'ok'), o('m', 'mv', 'missing')], false)).toEqual({ pending: true, gatedOnly: true });
    expect(rollout0012State([o('a', 'column', 'missing'), o('m', 'mv', 'missing')], false).gatedOnly).toBe(false);
    expect(rollout0012State([o('a', 'column', 'ok')], true)).toEqual({ pending: false, gatedOnly: false });
  });
  it('geri doldurma: bugün hariç boş günler', () => {
    expect(backfillGapDays([{ day: '2026-11-21', gap: true }, { day: '2026-11-19', gap: true }, { day: '2026-11-20', gap: false }], '2026-11-21')).toEqual(['2026-11-19']);
  });
  it('0017 durumu → probe (boot yönetiyor = kart yok; SQL bekleyen adımlardan)', () => {
    const st: AttrKVHAliasStatus = {
      cluster: 'c1', hosts: 2, bootManaged: false, spansLocal: true, thisPodMode: 'legacy', bloomUsed: 0,
      checks: [{ name: 'host erişimi', ok: true, blocking: true, detail: '2/2' }], hostInfo: [],
      steps: [
        { id: 'columns', title: '1', state: 'done', objects: [], sql: ['A'] },
        { id: 'indexes', title: '2', state: 'pending', objects: [], sql: ['B'] },
        { id: 'switch', title: '3', state: 'pending', objects: [], sql: [] },
        { id: 'legacy', title: '4', state: 'pending', objects: [], sql: ['C'] },
        { id: 'materialize', title: '5', state: 'optional', objects: [], sql: ['M1', 'M2'] },
      ],
      pods: [{ id: 'p', hostname: 'p', version: 'v', mode: 'legacy', isThisPod: true }],
      partitions: [{ id: '20261120', partition: '2026-11-20', parts: 1, rows: 1, bytes: 1, materialized: false }],
      pendingMutations: 0, pending: true, job: { running: false, materialize: false, log: [] }, generated: 1,
    };
    const p = attrKVHProbe(st);
    expect(p.applicable).toBe(true);
    expect(p.plan.map(g => g.lines[0])).toEqual(['B', 'C', expect.stringContaining('(SQL yok)'), "M1 IN PARTITION ID '<20261120 … 20261120>'"]);
    expect(p.lastRun).toBeNull();
    expect(attrKVHProbe({ ...st, bootManaged: true }).applicable).toBe(false);
  });
});

describe('rollup: sıralı koşu', () => {
  it('eksik aileler sırayla; ilk hatada durur; kurulu aile atlanır', async () => {
    const applied: string[] = [];
    vi.stubGlobal('fetch', (url: unknown, init?: RequestInit) => {
      const u = String(url);
      let body: unknown = { error: 'yok' };
      if (u.includes('/rollup/status')) body = { cluster: 'c1', generated: 1, tables: [
        { table: 'n', family: 'narrow', exists: false, rows: 0, minTsMs: 0 },
        { table: 'm', family: 'metrics', exists: true, rows: 0, minTsMs: 0 },
        { table: 'r', family: 'route', exists: false, rows: 0, minTsMs: 0 },
      ] };
      if (u.includes('/rollup/apply')) {
        const t = JSON.parse(String(init?.body)).target;
        applied.push(t);
        body = t === 'narrow' ? { ok: false, statements: [{ head: 'CREATE …', ok: false, err: 'kuyruk' }] } : { ok: true, statements: [] };
      }
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } }));
    });
    const r = await rollupWizard.run({ cluster: 'c1' });
    expect(applied).toEqual(['narrow']);
    expect(r).toMatchObject({ ok: false, failedStep: 'narrow', err: 'kuyruk' });
  });
});

describe('skipMatProbe (v0.10.2424)', () => {
  const job = (over: Partial<SkipMatJob>): SkipMatJob => ({
    id: 'spans:index:idx_x', table: 'spans', local: 'spans_local', cluster: 'c1', kind: 'index', name: 'idx_x',
    state: 'running', createdAt: 1, updatedAt: 1, partitions: [],
    counts: { total: 3, done: 1, pending: 1, running: 1, failed: 0, dropped: 0 }, ...over,
  });
  it('no live job → card hidden; declined/done/cancelled are not work', () => {
    const p = skipMatProbe({ jobs: [job({ state: 'declined' }), job({ state: 'done' }), job({ state: 'cancelled' })], active: false, failing: false, generated: 0 });
    expect(p.pending).toBe(false);
  });
  it('running job → visible, polling, retry blocked', () => {
    const p = skipMatProbe({ jobs: [job({ sql: "ALTER TABLE spans_local ON CLUSTER c1 MATERIALIZE INDEX idx_x IN PARTITION ID '20261120'" })], active: true, failing: false, generated: 0 });
    expect(p).toMatchObject({ pending: true, running: true });
    expect(p.blocker).toMatch(/başarısız partition yok/);
    expect(p.plan[0].lines[0]).toMatch(/IN PARTITION ID/);
  });
  it('failed partitions → retry allowed + self-health check shown', () => {
    const p = skipMatProbe({ jobs: [job({ state: 'failed', counts: { total: 2, done: 1, pending: 0, running: 0, failed: 1, dropped: 0 } })], active: false, failing: true, health: 'x', generated: 0 });
    expect(p.blocker).toBe('');
    expect(p.steps?.[0].state).toBe('failed');
    expect(p.checks[0]).toMatchObject({ name: 'self-health', ok: false });
  });
});
