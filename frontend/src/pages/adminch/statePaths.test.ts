// statePaths.test.ts — v0.10.965 — "State tablolarının ZK yolu" bloğunun saf
// yardımcıları (operatör kararı 2026-09-27). Host adları sentetik.
import { describe, expect, it } from 'vitest';
import {
  ackFromPlan, actionLabel, classLabel, defaultSelection, fmtRows, groupHostsTitle, groupSummary,
  kindLabel, phaseLabel, rowsToDelete, stateLabel, stillLegacyAfterSelection,
} from './statePaths';
import type {
  CHStatePathAction, CHStatePathCheck, CHStatePathClass, CHStatePathPhase, CHStatePathRebuildPlan,
  CHStatePathRebuildTable, CHStatePathState, CHStatePathTable,
} from '@/lib/types';

const STATES: CHStatePathState[] = ['legacy', 'absent', 'unified', 'legacy_partial', 'unified_partial', 'mixed', 'unknown'];
const ACTIONS: CHStatePathAction[] = ['rebuild', 'create', 'skip', 'blocked'];
const PHASES: CHStatePathPhase[] = ['drop', 'drop_wait', 'create', 'verify', 'done'];
const CLASSES: CHStatePathClass[] = ['derived', 'operator_exception', 'empty_only'];

const legacy = (table: string, over: Partial<CHStatePathTable> = {}): CHStatePathTable => ({
  table, kind: 'legacy', rows: 0, rebuildable: true, class: 'empty_only',
  groups: [
    { path: `/clickhouse/tables/01/${table}`, hosts: ['host-1', 'host-2'], rows: 0, unified: false },
    { path: `/clickhouse/tables/02/${table}`, hosts: ['host-3', 'host-4'], rows: 0, unified: false },
  ],
  ...over,
});
const check = (legacyRows: CHStatePathTable[]): Pick<CHStatePathCheck, 'legacy'> => ({ legacy: legacyRows });

describe('statePaths — saf', () => {
  it('etiket haritaları birlik tiplerinin TAMAMINI kapsar', () => {
    for (const s of STATES) expect(stateLabel(s)).toBeTruthy();
    for (const a of ACTIONS) expect(actionLabel(a)).toBeTruthy();
    for (const p of PHASES) expect(phaseLabel(p)).toBeTruthy();
    for (const c of CLASSES) expect(classLabel(c)).toBeTruthy();
    for (const k of ['legacy', 'mixed', 'absent'] as const) expect(kindLabel(k)).toBeTruthy();
    expect(stateLabel('legacy_partial')).toBe("eski (bazı host'larda yok)");
    expect(stateLabel('unified_partial')).toBe("birleşik (bazı host'larda yok)");
    expect(stateLabel('mixed')).toBe('karışık (birleşik + eski)');
    expect(stateLabel('unknown')).toBe('tanınmayan');
    expect(actionLabel('rebuild')).toBe('düşür + birleşik yolda kur');
    expect(actionLabel('skip')).toBe('atla (zaten birleşik)');
    expect(phaseLabel('drop_wait')).toBe('düşürme doğrulaması');
    expect(classLabel('operator_exception')).toBe('operatör istisnası (2026-09-27)');
    expect(classLabel('empty_only')).toBe('yalnız boşken');
  });

  it('defaultSelection: yalnız izinli (rebuildable) tablolar', () => {
    const c = check([legacy('ingest_ledger'), legacy('alert_rules', { rebuildable: false, class: undefined }), legacy('rollout_events', { kind: 'absent', groups: [] })]);
    expect(defaultSelection(c)).toEqual(['ingest_ledger', 'rollout_events']);
  });

  // v0.10.971 — kilit kalktı (boot'un kural 3'ü yok): seçim dışı eski tablolar
  // yalnız BİLGİ olarak listelenir — bölünmüş kalırlar, yeni tabloları eski
  // yola çekmezler. Sunucunun statePathStillLegacyAfter aynası.
  it('stillLegacyAfterSelection: hepsi seçili boş; kısmi seçim ve izin dışı eski tablo listelenir', () => {
    const ten = ['ingest_ledger', 'ai_eval_runs', 'rollout_events', 'rollout_workload_state', 'argocd_app_status',
      'argocd_sync_events', 'argocd_app_mapping', 'rollout_classification', 'ado_commit_enrichment', 'rollout_worker_runs'];
    const c = check(ten.map(t => legacy(t)));
    expect(stillLegacyAfterSelection(c, ten)).toEqual([]);
    expect(stillLegacyAfterSelection(c, ten.slice(2))).toEqual(['ai_eval_runs', 'ingest_ledger']);
    // Gözlemde olmayan (absent) satır eski yolda değildir, seçilmese bile.
    const withAbsent = check([legacy('ingest_ledger'), legacy('rollout_events', { kind: 'absent', groups: [] })]);
    expect(stillLegacyAfterSelection(withAbsent, ['ingest_ledger'])).toEqual([]);
    // İzin dışı eski tablo seçilemez: adı seçimde olsa da eski yolda kalır.
    const foreign = check([legacy('ingest_ledger'), legacy('alert_rules', { rebuildable: false })]);
    expect(stillLegacyAfterSelection(foreign, ['ingest_ledger', 'alert_rules'])).toEqual(['alert_rules']);
    // Karışık satır da bölünmüş → seçilmezse kalır.
    expect(stillLegacyAfterSelection(check([legacy('ai_eval_runs', { kind: 'mixed' })]), [])).toEqual(['ai_eval_runs']);
  });

  it('groupSummary: yol sayısı, kısa yol, host SAYISI, tam satır; host adları yalnız title', () => {
    const t = legacy('ingest_ledger', {
      groups: [
        { path: '/clickhouse/tables/01/ingest_ledger', hosts: ['host-1', 'host-2'], rows: 1234, unified: false },
        { path: '/clickhouse/tables/02/ingest_ledger', hosts: ['host-3', 'host-4'], rows: 1100, unified: false },
      ],
    });
    expect(groupSummary(t)).toBe('2 yol · …/tables/01/ingest_ledger: 2 host, 1.234 satır · …/tables/02/ingest_ledger: 2 host, 1.100 satır');
    expect(groupSummary(t)).not.toMatch(/host-\d/);
    expect(groupHostsTitle(t)).toContain('host-1, host-2');
    expect(groupSummary({ groups: [] })).toBe("hiçbir host'ta yok");
    expect(groupSummary({ groups: [{ path: '/clickhouse/tables/state/x', hosts: ['host-1'], rows: 0, unified: true }] })).toContain('(birleşik)');
  });

  it('fmtRows: binlik nokta, yuvarlama yok', () => {
    expect(fmtRows(0)).toBe('0');
    expect(fmtRows(999)).toBe('999');
    expect(fmtRows(1234)).toBe('1.234');
    expect(fmtRows(1234567)).toBe('1.234.567');
  });

  const row = (table: string, action: CHStatePathAction, rows: number, state: CHStatePathState = 'legacy'): CHStatePathRebuildTable => ({
    table, state, action, rows, groups: [], class: 'empty_only', classNote: '', verified: false,
  });
  const plan: Pick<CHStatePathRebuildPlan, 'measuredAt' | 'tables'> = {
    measuredAt: 1_800_000_000_000,
    tables: [row('ingest_ledger', 'rebuild', 2468), row('ai_eval_runs', 'rebuild', 5), row('rollout_events', 'create', 0, 'absent'),
      row('argocd_app_status', 'skip', 7, 'unified'), row('rollout_worker_runs', 'create', 3, 'unified_partial')],
  };

  it('ackFromPlan: ölçüm anı + ATLA olmayan her tablonun durumu ve satırı', () => {
    expect(ackFromPlan(plan)).toEqual({
      measuredAt: 1_800_000_000_000,
      tables: [
        { table: 'ingest_ledger', state: 'legacy', rows: 2468 },
        { table: 'ai_eval_runs', state: 'legacy', rows: 5 },
        { table: 'rollout_events', state: 'absent', rows: 0 },
        { table: 'rollout_worker_runs', state: 'unified_partial', rows: 3 },
      ],
    });
  });

  it('rowsToDelete: yalnız düşürülecek (rebuild) satırlar', () => {
    expect(rowsToDelete(plan)).toBe(2473);
    expect(rowsToDelete({ tables: [] })).toBe(0);
  });
});
