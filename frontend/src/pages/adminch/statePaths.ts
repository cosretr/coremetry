/**
 * statePaths — v0.10.965 — "State tablolarının ZK yolu" bloğunun saf yarısı
 * (Replika tutarlılığı kartı; operatör kararı 2026-09-27). Kararlar
 * sunucuda (chstore.statePathCheckFor / PlanStatePathRebuild): burada
 * varsayılan seçim, kilit uyarısı (yalnız UYARI — kararı sunucu verir),
 * grup özeti, etiketler ve onay gövdesi.
 */
import type {
  CHStatePathAckTable, CHStatePathAction, CHStatePathCheck, CHStatePathClass, CHStatePathPhase,
  CHStatePathRebuildPlan, CHStatePathState, CHStatePathTable,
} from '@/lib/types';
import { shortZk } from './replicaConsistency';

/** v0.10.965 — varsayılan seçim: sihirbazın izin listesindeki her eski/karışık/eksik tablo. */
export function defaultSelection(check: Pick<CHStatePathCheck, 'legacy'>): string[] {
  return check.legacy.filter(t => t.rebuildable).map(t => t.table);
}

/**
 * v0.10.965 — seçim kilidi açar mı? Sunucu kuralının (statePathLockAfter)
 * aynası, YALNIZ uyarı için: seçilen her tablo birleşik yola geçer; geriye
 * gözlenen (eski ya da karışık) bir state tablosu kalırsa boot kural 3 ile
 * yeni tabloları yine eski yola kurar. "absent" satırı gözlemde değildir,
 * kilidi tutmaz. İzin listesi dışı eski tablo seçilemez → kilidi tutar.
 */
export function lockAfterSelection(check: Pick<CHStatePathCheck, 'legacy'>, selected: readonly string[]): { opens: boolean; stillLegacy: string[] } {
  const sel = new Set(selected);
  const stillLegacy = check.legacy
    .filter(t => t.kind !== 'absent' && !(t.rebuildable && sel.has(t.table)))
    .map(t => t.table)
    .sort();
  return { opens: stillLegacy.length === 0, stillLegacy };
}

/** Satır sayısı: binlik ayraç nokta (tr), yuvarlama YOK — operatör tam sayıyı onaylar. */
export function fmtRows(n: number): string {
  return String(Math.trunc(n)).replace(/\B(?=(\d{3})+(?!\d))/g, '.');
}

/**
 * v0.10.965 — grup özeti: "2 yol · …/tables/01/ingest_ledger: 2 host, 1.234 satır · …".
 * Host ADLARI metinde yok (yalnız sayı); adlar çağıranın title'ında.
 */
export function groupSummary(t: Pick<CHStatePathTable, 'groups'>): string {
  const groups = t.groups ?? [];
  if (groups.length === 0) return 'hiçbir host\'ta yok';
  const parts = groups.map(g => `${shortZk(g.path)}${g.unified ? ' (birleşik)' : ''}: ${g.hosts.length} host, ${fmtRows(g.rows)} satır`);
  return [`${groups.length} yol`, ...parts].join(' · ');
}

/** Host adları — yalnız title için. */
export function groupHostsTitle(t: Pick<CHStatePathTable, 'groups'>): string {
  return (t.groups ?? []).map(g => `${g.path}\n  ${g.hosts.join(', ')}`).join('\n');
}

const STATE_LABEL: Record<CHStatePathState, string> = {
  legacy: 'eski yol', absent: 'yok', unified: 'birleşik',
  legacy_partial: 'eski (bazı host\'larda yok)', unified_partial: 'birleşik (bazı host\'larda yok)',
  mixed: 'karışık (birleşik + eski)', unknown: 'tanınmayan',
};
export const stateLabel = (s: CHStatePathState): string => STATE_LABEL[s];

const ACTION_LABEL: Record<CHStatePathAction, string> = {
  rebuild: 'düşür + birleşik yolda kur', create: 'birleşik yolda kur',
  skip: 'atla (zaten birleşik)', blocked: 'engelli',
};
export const actionLabel = (a: CHStatePathAction): string => ACTION_LABEL[a];

const PHASE_LABEL: Record<CHStatePathPhase, string> = {
  drop: 'düşürme', drop_wait: 'düşürme doğrulaması', create: 'kurma', verify: 'doğrulama', done: 'tamam',
};
export const phaseLabel = (p: CHStatePathPhase): string => PHASE_LABEL[p];

const CLASS_LABEL: Record<CHStatePathClass, string> = {
  derived: 'türev', operator_exception: 'operatör istisnası (2026-09-27)', empty_only: 'yalnız boşken',
};
export const classLabel = (c: CHStatePathClass): string => CLASS_LABEL[c];

const KIND_LABEL: Record<CHStatePathTable['kind'], string> = {
  legacy: 'eski yol', mixed: 'karışık', absent: 'birleşik yolda eksik',
};
export const kindLabel = (k: CHStatePathTable['kind']): string => KIND_LABEL[k];

/**
 * v0.10.965 — onay gövdesi: planın ölçüm anı + ATLA olmayan her tablonun
 * durumu ve satırı. Sunucu apply'da TAZE ölçer ve bunlarla karşılaştırır.
 */
export function ackFromPlan(plan: Pick<CHStatePathRebuildPlan, 'measuredAt' | 'tables'>): { measuredAt: number; tables: CHStatePathAckTable[] } {
  return {
    measuredAt: plan.measuredAt,
    tables: plan.tables.filter(t => t.action !== 'skip').map(t => ({ table: t.table, state: t.state, rows: t.rows })),
  };
}

/** v0.10.965 — silinecek satır: yalnız düşürülecek (rebuild) tablolar. */
export function rowsToDelete(plan: Pick<CHStatePathRebuildPlan, 'tables'>): number {
  return plan.tables.filter(t => t.action === 'rebuild').reduce((a, t) => a + t.rows, 0);
}
