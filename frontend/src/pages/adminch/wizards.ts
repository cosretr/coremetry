// wizards.ts — v0.10.2410: AutoWizard adaptörleri (probe + tek koşu). Her
// adaptör mevcut uçları kullanır (sunucu sözleşmesi değişmedi); yalnız 0017
// kendi uçlarıyla gelir. SAF parçalar (pickCluster, objectsPlan, …) testli.
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';
import type {
  AttrKVHAliasStatus, EntityLayerObjectStatus, RollupStmtResult, RollupTarget, SkipMatStatus, TraceBackfillRun,
} from '@/lib/types';
import { ROLLOUT_V2_TABLES } from '@/lib/types';
import type { WizardCheck, WizardProbe, WizardRunResult } from './AutoWizard';

/** Önerilen küme listede varsa o; tek küme varsa o; aksi '' (seçici görünür). */
export function pickCluster(suggested: string | undefined, clusters: string[]): string {
  if (suggested && clusters.includes(suggested)) return suggested;
  return clusters.length === 1 ? clusters[0] : '';
}

export function objectsPending(objs: EntityLayerObjectStatus[]): boolean {
  return objs.length === 0 || objs.some(o => o.state !== 'ok');
}

/** Eksik / kısmi nesneler → plan satırları. */
export function objectsPlan(objs: EntityLayerObjectStatus[]): string[] {
  return objs.filter(o => o.state !== 'ok')
    .map(o => `${o.kind} ${o.table ? `${o.table}.` : ''}${o.name} — ${o.state === 'unknown' ? 'okunamadı' : `${o.haveHosts}/${o.hosts} host`}`);
}

/** Ön kontrol yanıtının ortak alanları → kontrol satırları. */
export function preflightChecks(pre: { supported: boolean; detail: string; probeErrors?: string[] }, name = 'ön kontrol'): WizardCheck[] {
  const out: WizardCheck[] = [{ name, ok: pre.supported, detail: pre.detail, blocking: true }];
  for (const e of pre.probeErrors ?? []) out.push({ name: 'probe hatası', ok: false, detail: e, blocking: true });
  return out;
}

function blockerOf(checks: WizardCheck[]): string {
  const c = checks.find(x => x.blocking && !x.ok);
  return c ? `${c.name}: ${c.detail ?? ''}` : '';
}

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** Ardışık adımlar: her adımdan önce yeniden probe, ilk hatada dur. */
async function runSteps(steps: { id: string; pending: () => Promise<boolean>; exec: () => Promise<{ ok: boolean; statements: RollupStmtResult[] }> }[]): Promise<WizardRunResult> {
  const log: RollupStmtResult[] = [];
  for (const s of steps) {
    let pending: boolean;
    try { pending = await s.pending(); } catch (e) { return { ok: false, failedStep: s.id, err: `yeniden probe: ${msg(e)}`, log }; }
    if (!pending) { log.push({ head: `${s.id}: zaten tamam — atlandı`, ok: true }); continue; }
    try {
      const r = await s.exec();
      log.push(...r.statements);
      if (!r.ok) return { ok: false, failedStep: s.id, err: r.statements.find(x => !x.ok)?.err ?? 'ifade hatası', log };
    } catch (e) { return { ok: false, failedStep: s.id, err: msg(e), log }; }
  }
  return { ok: true, log };
}

// ── 0017 attr_kvh ALIAS ─────────────────────────────────────────────

export function attrKVHProbe(st: AttrKVHAliasStatus): WizardProbe {
  const pods = st.pods.map(p => `${p.hostname}${p.isThisPod ? ' (bu pod)' : ''}=${p.mode || '?'}`).join(', ');
  const plan = st.steps.filter(s => s.state !== 'done' && s.id !== 'materialize' && s.sql.length > 0)
    .map(s => ({ title: s.title, lines: s.sql }));
  const sw = st.steps.find(s => s.id === 'switch');
  if (sw && sw.state !== 'done') plan.push({ title: sw.title, lines: ["(SQL yok) pod'lar kolonları ≤ 30 sn'de kendileri görür; koşu görünen her pod v2 diyene kadar bekler, demezse eski kolonlara dokunmadan durur"] });
  const mat = st.steps.find(s => s.id === 'materialize');
  const left = st.partitions.filter(p => !p.materialized);
  if (mat && left.length > 0) {
    plan.push({ title: `${mat.title} — yalnız onay kutusu işaretliyse, partition partition (${left.length})`, lines: mat.sql.map(q => `${q} IN PARTITION ID '<${left[0].id} … ${left[left.length - 1].id}>'`) });
  }
  return {
    applicable: !st.bootManaged,
    pending: st.pending,
    checks: st.checks.map(c => ({ name: c.name, ok: c.ok, detail: c.detail, blocking: c.blocking })),
    steps: st.steps.map(s => ({ id: s.id, title: s.title, state: s.state, detail: s.detail })),
    blocker: st.blocker,
    cluster: st.cluster,
    plan,
    summary: `küme ${st.cluster || '—'} · ${st.hosts} host · pod kipleri: ${pods || '—'} · bloom yüklemi (bu pod) ${fmtNum(st.bloomUsed)}${st.pendingMutations > 0 ? ` · ${st.pendingMutations} bekleyen kvh mutasyonu` : ''}${st.job.running ? ` · koşuyor: ${st.job.step ?? ''}` : ''}`,
    running: st.job.running,
    lastRun: st.job.startedAt ? { ok: !st.job.err && !st.job.running, failedStep: st.job.failedStep, err: st.job.err, log: st.job.log } : null,
  };
}

export const attrKVHWizard = {
  probe: async () => attrKVHProbe(await api.attrKVHAliasStatus()),
  run: async ({ heavy }: { heavy: boolean }) => { await api.attrKVHAliasRun(heavy); return null; },
};

// ── arka plan MATERIALIZE işleri (v0.10.2424) ───────────────────────

/** Kart yalnız sürmekte olan, başarısız ya da self-health notu açık iş varken görünür. */
export function skipMatProbe(st: SkipMatStatus): WizardProbe {
  const live = st.jobs.filter(j => j.state !== 'declined' && j.state !== 'cancelled' && j.state !== 'done');
  const failed = live.filter(j => j.counts.failed > 0);
  const checks: WizardCheck[] = [];
  if (st.failing && st.health) checks.push({ name: 'self-health', ok: false, detail: st.health });
  return {
    applicable: true,
    pending: live.length > 0,
    checks,
    steps: live.map(j => ({
      id: j.id, title: `${j.kind} ${j.name} · ${j.local}${j.cluster ? ` ON CLUSTER ${j.cluster}` : ''}`,
      state: j.counts.failed > 0 ? 'failed' : j.state,
      detail: `${j.counts.done}/${j.counts.total} partition bitti · ${j.counts.running} yazılıyor · ${j.counts.failed} başarısız · ${j.counts.dropped} retention'la düştü${j.note ? ` — ${j.note}` : ''}`,
    })),
    blocker: failed.length === 0 ? 'yeniden denenecek başarısız partition yok — işler arka planda ilerliyor' : '',
    plan: live.filter(j => j.sql).map(j => ({ title: `${j.id} — sıradaki partition`, lines: [j.sql as string] })),
    summary: 'Worker lideri her 30 sn\'de bir adım atar: tabloda tek MATERIALIZE mutasyonu, en yeni partition önce; başka mutasyon / merge birikimi / disk < %10 iken bekler.',
    running: st.active && failed.length === 0,
  };
}

export const skipMatWizard = {
  probe: async () => skipMatProbe(await api.skipMatStatus()),
  run: async ({ probe }: { probe: WizardProbe }): Promise<WizardRunResult> => {
    const log: RollupStmtResult[] = [];
    for (const s of probe.steps ?? []) {
      if (s.state !== 'failed') continue;
      try { await api.skipMatAction('retry', s.id); log.push({ head: `${s.id}: başarısız partition'lar yeniden sırada`, ok: true }); }
      catch (e) { return { ok: false, failedStep: s.id, err: msg(e), log }; }
    }
    return { ok: true, log };
  },
};

// ── 0001/0003/0008 rollup zinciri ───────────────────────────────────

const ROLLUP_FAMILIES: Exclude<RollupTarget, 'both'>[] = ['narrow', 'metrics', 'route'];

async function rollupMissing(): Promise<Map<string, string[]>> {
  const st = await api.rollupStatus();
  const m = new Map<string, string[]>();
  for (const t of st.tables) if (!t.exists) m.set(t.family, [...(m.get(t.family) ?? []), t.table]);
  return m;
}

export const rollupWizard = {
  probe: async (): Promise<WizardProbe> => {
    const [missing, pre] = await Promise.all([rollupMissing(), api.rollupPreflight()]);
    const checks = preflightChecks(pre);
    if (pre.missingColumns.length > 0) checks.push({ name: 'kaynak kolonlar', ok: false, detail: pre.missingColumns.join(', '), blocking: true });
    return {
      applicable: pre.spansLocal, pending: missing.size > 0, checks, blocker: blockerOf(checks),
      cluster: pickCluster(pre.suggestedCluster, pre.clusters), clusters: pre.clusters,
      plan: ROLLUP_FAMILIES.filter(f => missing.has(f)).map(f => ({ title: `${f} zinciri (eksik tablolar)`, lines: missing.get(f) ?? [] })),
    };
  },
  run: ({ cluster }: { cluster: string }) => runSteps(ROLLUP_FAMILIES.map(f => ({
    id: f,
    pending: async () => (await rollupMissing()).has(f),
    exec: () => api.rollupApply(cluster, f),
  }))),
};

// ── 0011 entity katmanı ─────────────────────────────────────────────

export const entityLayerWizard = {
  probe: async (): Promise<WizardProbe> => {
    const [st, pre] = await Promise.all([api.entityLayerStatus(), api.entityLayerPreflight()]);
    const checks = preflightChecks(pre);
    return {
      applicable: pre.spansLocal, pending: objectsPending(st.objects), checks, blocker: blockerOf(checks),
      cluster: pickCluster(pre.suggestedCluster, pre.clusters), clusters: pre.clusters,
      plan: [{ title: 'eksik nesneler', lines: objectsPlan(st.objects) }],
      summary: `pod attribute kapsaması %${(pre.podAttrCoverage * 100).toFixed(0)} · son 1 sa ${fmtNum(pre.uniqPods1h)} pod`,
    };
  },
  run: ({ cluster }: { cluster: string }) => runSteps([{
    id: '0011', pending: async () => objectsPending((await api.entityLayerStatus()).objects), exec: () => api.entityLayerApply(cluster),
  }]),
};

// ── 0012 + 0015 rollouts katmanı ────────────────────────────────────

export function splitRolloutObjects(objs: EntityLayerObjectStatus[]) {
  const v2 = new Set<string>(ROLLOUT_V2_TABLES);
  return {
    layer0012: objs.filter(o => !(o.kind === 'table' && v2.has(o.name))),
    layer0015: objs.filter(o => o.kind === 'table' && v2.has(o.name)),
  };
}

/** 0012 bekliyor mu — MV kapısı kapalıyken yalnız MV eksikse iş yok sayılmaz ama koşulamaz. */
export function rollout0012State(objs: EntityLayerObjectStatus[], mvGate: boolean): { pending: boolean; gatedOnly: boolean } {
  const missing = objs.filter(o => o.state !== 'ok');
  return { pending: objs.length === 0 || missing.length > 0, gatedOnly: !mvGate && missing.length > 0 && missing.every(o => o.kind === 'mv') };
}

export const rolloutLayerWizard = {
  probe: async (): Promise<WizardProbe> => {
    const [st, pre] = await Promise.all([api.rolloutLayerStatus(), api.rolloutLayerPreflight()]);
    const cluster = pickCluster(pre.suggestedCluster, pre.clusters);
    const pre15 = await api.rolloutV2LayerPreflight(cluster || undefined);
    const g = splitRolloutObjects(st.objects);
    const s12 = rollout0012State(g.layer0012, pre.mvGate);
    const pending15 = !pre15.bootManaged && !pre15.installed;
    const checks = [...preflightChecks(pre, 'ön kontrol 0012')];
    checks.push({ name: 'MV kapısı (replicaset kapsaması ≥ %95)', ok: pre.mvGate, detail: pre.mvGate ? 'açık — MV de kurulur' : 'kapalı — kolon/indeks/tablo kurulur, MV atlanır' });
    if (pending15) checks.push(...preflightChecks(pre15, 'ön kontrol 0015'));
    if (pre15.conflicts.length > 0) checks.push({ name: '0015 çakışma', ok: false, detail: pre15.conflicts.join(', '), blocking: true });
    let blocker = blockerOf(s12.pending ? checks : checks.filter(c => !c.name.endsWith('0012')));
    if (!blocker && s12.gatedOnly && !pending15) blocker = 'yalnız MV eksik ve MV kapısı kapalı — kapsama ≥ %95 olunca kurulur';
    return {
      applicable: pre.spansLocal, pending: s12.pending || pending15, checks, blocker,
      cluster, clusters: pre.clusters,
      plan: [
        { title: '0012 eksik nesneler', lines: s12.pending ? objectsPlan(g.layer0012) : [] },
        { title: '0015 state tabloları', lines: pending15 ? pre15.missing : [] },
      ],
      summary: `son 1 sa ${fmtNum(pre.uniqRs1h)} replicaset · ${fmtNum(pre.uniqImage1h)} image`,
    };
  },
  run: ({ cluster }: { cluster: string }) => runSteps([
    {
      id: '0012',
      pending: async () => {
        const [st, pre] = await Promise.all([api.rolloutLayerStatus(), api.rolloutLayerPreflight()]);
        const s = rollout0012State(splitRolloutObjects(st.objects).layer0012, pre.mvGate);
        return s.pending && !s.gatedOnly;
      },
      exec: async () => {
        const pre = await api.rolloutLayerPreflight();
        return api.rolloutLayerApply(cluster, pre.mvGate);
      },
    },
    {
      id: '0015',
      pending: async () => { const p = await api.rolloutV2LayerPreflight(cluster); return !p.bootManaged && !p.installed; },
      exec: () => api.rolloutV2LayerApply(cluster),
    },
  ]),
};

// ── 0013 attr_function_id terfi kolonu ──────────────────────────────

export const functionIdWizard = {
  probe: async (): Promise<WizardProbe> => {
    const st = await api.functionIdColumnStatus();
    if (st.bootManaged) return { applicable: false, pending: false, checks: [], plan: [] };
    const pre = await api.functionIdColumnPreflight();
    const checks = preflightChecks(pre);
    return {
      applicable: pre.spansLocal && !pre.bootManaged, pending: objectsPending(st.objects), checks, blocker: blockerOf(checks),
      cluster: pickCluster(pre.suggestedCluster, pre.clusters), clusters: pre.clusters,
      plan: [{ title: 'eksik nesneler', lines: objectsPlan(st.objects) }],
      summary: `doluluk (son 10 dk) ${fmtNum(st.filled)} / ${fmtNum(st.total)}`,
    };
  },
  run: ({ cluster, heavy }: { cluster: string; heavy: boolean }) => runSteps([
    { id: '0013', pending: async () => objectsPending((await api.functionIdColumnStatus()).objects), exec: () => api.functionIdColumnApply(cluster) },
    ...(heavy ? [{ id: 'materialize', pending: async () => true, exec: () => api.functionIdColumnMaterialize(cluster) }] : []),
  ]),
};

// ── /traces tarihçe geri doldurma (trace_summary_5m boşlukları) ─────

export function backfillGapDays(days: { day: string; gap: boolean }[], todayUtc: string): string[] {
  return days.filter(d => d.gap && d.day < todayUtc).map(d => d.day).sort();
}

function backfillLastRun(run: TraceBackfillRun): WizardRunResult | null {
  if (!run.startedAt) return null;
  const errs = run.errors ?? [];
  return {
    ok: errs.length === 0 && !run.running, err: errs.join(' · ') || undefined,
    log: run.days.map((d, i) => ({ head: `gün ${d}`, ok: i < run.done })),
  };
}

export const traceBackfillWizard = {
  probe: async (): Promise<WizardProbe> => {
    const [pre, run] = await Promise.all([api.traceBackfillPreflight(), api.traceBackfillStatus()]);
    const gaps = backfillGapDays(pre.days, new Date().toISOString().slice(0, 10));
    return {
      applicable: true, pending: gaps.length > 0 || run.running,
      checks: [{ name: 'trace_summary_5m boş günler (bugün hariç)', ok: gaps.length === 0, detail: gaps.length > 0 ? `${gaps.length} gün` : 'yok' }],
      plan: [{ title: 'yeniden kurulacak günler (MV partition düş + ham spans\'ten aggregate; span\'lere dokunulmaz)', lines: gaps }],
      summary: run.running ? `koşuyor: ${run.done}/${run.days.length} gün · şu an ${run.current ?? '—'}` : undefined,
      running: run.running,
      lastRun: backfillLastRun(run),
    };
  },
  run: async () => {
    const pre = await api.traceBackfillPreflight();
    const gaps = backfillGapDays(pre.days, new Date().toISOString().slice(0, 10));
    if (gaps.length === 0) return { ok: true, log: [{ head: 'boş gün kalmadı — atlandı', ok: true }] };
    await api.traceBackfillApply(gaps, 1);
    return null;
  },
};
