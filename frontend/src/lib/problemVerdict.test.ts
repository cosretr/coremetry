import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { inboxSignature, verdictIndex, verdictOf, filterByVerdictView, countNoise, parseVerdictView,
  verdictMutesNotifications, verdictCoversNotifications, verdictNotifyHint } from './problemVerdict';
import type { InboxItem, ProblemVerdict } from './types';

// v0.10.1015 — operatör: "Problems sekmesinde bütün hepsi gelsin, ben hangisi
// gerçek problem hangisi değil zamanla öğretelim." Karar İMZAYA bağlıdır:
// aynı kural + servis / aynı exception grubu yeniden gelince aynı sınıfa düşer.

const base = { id: 'x', source: 's', priority: 'P3', priorityReason: '', severity: 'warning', title: 't', description: '', startedAt: 0, lastSeen: 0, status: 'open' } as const;
const problem = (ruleId: string, service: string, id = 'p1'): InboxItem =>
  ({ ...base, kind: 'problem', service, problem: { id, ruleId, metric: 'm', value: 1, threshold: 1 } }) as InboxItem;
const exception = (fingerprint: string, service = 'svc', kind: 'exception' | 'httperror' = 'exception'): InboxItem =>
  ({ ...base, kind, service, exception: { fingerprint, type: 'T', message: 'm', occurrences: 1 } }) as InboxItem;
const anomaly = (kind: string, pattern: string, service: string, id = 'a1'): InboxItem =>
  ({ ...base, kind: 'anomaly', service, anomaly: { id, kind, pattern, peakRatio: 1, currentRatio: 1 } }) as InboxItem;
const incident = (): InboxItem => ({ ...base, kind: 'incident', service: 'svc', incident: { id: 'i1', severity: 's', status: 'open' } }) as InboxItem;

describe('inboxSignature', () => {
  it('kaynağa göre önek; olay kimliği imzaya GİRMEZ (aynı kural yeniden gelince aynı imza)', () => {
    expect(inboxSignature(problem('rule-err', 'payments-api', 'p1'))).toBe('p:rule-err|payments-api');
    expect(inboxSignature(problem('rule-err', 'payments-api', 'p2'))).toBe('p:rule-err|payments-api');
    expect(inboxSignature(problem('rule-err', 'orders-api'))).toBe('p:rule-err|orders-api');
    expect(inboxSignature(exception('9f8a7c'))).toBe('e:9f8a7c');
    expect(inboxSignature(exception('404', 'gw', 'httperror'))).toBe('e:404');
    expect(inboxSignature(anomaly('latency', 'p99 up', 'orders-api', 'a1'))).toBe('a:latency|orders-api|p99 up');
    expect(inboxSignature(anomaly('latency', 'p99 up', 'orders-api', 'a2'))).toBe('a:latency|orders-api|p99 up');
  });
  it('öğretilemeyen satır: olay ya da eksik kimlik → null', () => {
    expect(inboxSignature(incident())).toBeNull();
    expect(inboxSignature({ ...problem('', 'svc') })).toBeNull();
    expect(inboxSignature({ kind: 'exception', service: 'svc' })).toBeNull();
  });
  it('önekler sunucunun kabul ettikleriyle aynı (p: e: a:)', () => {
    const go = readFileSync(resolve(__dirname, '../../../internal/chstore/problem_verdict.go'), 'utf8');
    expect(go).toContain('case "p:", "e:", "a:":');
  });
});

describe('karar dizini ve görünüm', () => {
  const list: ProblemVerdict[] = [
    { signature: 'p:rule-err|payments-api', verdict: 'noise', at: 3 },
    { signature: 'e:9f8a7c', verdict: 'real', at: 2 },
    { signature: 'p:rule-err|payments-api', verdict: 'real', at: 1 }, // eski — ilk (en yeni) kazanır
  ];
  const idx = verdictIndex(list);
  const rows = [problem('rule-err', 'payments-api'), problem('rule-err', 'orders-api'), exception('9f8a7c'), exception('other'), incident()];

  it('aynı imza iki kez gelirse en yeni kazanır', () => {
    expect(idx.size).toBe(2);
    expect(verdictOf(rows[0], idx)).toBe('noise');
    expect(verdictOf(rows[2], idx)).toBe('real');
    expect(verdictOf(rows[1], idx)).toBeUndefined();
    expect(verdictOf(rows[4], idx)).toBeUndefined();
  });
  it('triage: "problem değil" HARİÇ her şey; noise: yalnız onlar', () => {
    expect(filterByVerdictView(rows, idx, 'triage')).toEqual([rows[1], rows[2], rows[3], rows[4]]);
    expect(filterByVerdictView(rows, idx, 'noise')).toEqual([rows[0]]);
    expect(countNoise(rows, idx)).toBe(1);
  });
  it('karar listesi yoksa hiçbir satır gizlenmez (güvenli taraf)', () => {
    const empty = verdictIndex(undefined);
    expect(filterByVerdictView(rows, empty, 'triage')).toEqual(rows);
    expect(filterByVerdictView(rows, empty, 'noise')).toEqual([]);
    expect(countNoise(null, empty)).toBe(0);
  });
  it('URL görünümü: yalnız "noise" tanınır, gerisi triage', () => {
    expect(parseVerdictView('noise')).toBe('noise');
    expect(parseVerdictView(null)).toBe('triage');
    expect(parseVerdictView('all')).toBe('triage');
  });
});

describe('kablolama', () => {
  it('liste kararları süzer, çip sayıyı gösterir; çekmece düğmeleri taşır', () => {
    const inbox = readFileSync(resolve(__dirname, '../pages/Inbox.tsx'), 'utf8');
    expect(inbox).toContain('return filterByVerdictView(facet, verdicts, verdictView);');
    expect(inbox).toContain('Problem değil ({noiseCount})');
    expect(inbox).toContain("setParam('verdict', v === 'triage' ? null : v)");
    const drawer = readFileSync(resolve(__dirname, '../components/InboxTriageDrawer.tsx'), 'utf8');
    expect(drawer).toContain('<ProblemVerdictActions item={item}');
    const actions = readFileSync(resolve(__dirname, '../components/ProblemVerdictActions.tsx'), 'utf8');
    expect(actions).toContain("const isEditor = user?.role === 'admin' || user?.role === 'editor';");
    // v0.10.1016 — açıklama artık politikaya göre değişir (sabit "susturmaz" değil).
    expect(actions).toContain('{verdictNotifyHint(sig, q.data?.policy)}');
  });
  it('v0.10.1016 — "Problem değil" görünümü politika satırını taşır; anahtar yalnız admin, açmak onay ister', () => {
    const inbox = readFileSync(resolve(__dirname, '../pages/Inbox.tsx'), 'utf8');
    expect(inbox).toContain("{verdictView === 'noise' && <ProblemVerdictPolicyBar />}");
    const bar = readFileSync(resolve(__dirname, '../components/ProblemVerdictPolicyBar.tsx'), 'utf8');
    expect(bar).toContain("const isAdmin = user?.role === 'admin';");
    expect(bar).toContain('if (!on && !await confirm({');
    expect(bar).toContain('set.mutate(!on);');
  });
});

// v0.10.1016 — "problem değil" bildirimi de sustursun mu: varsayılan KAPALI;
// açıkken yalnız p: (alarm kuralı) ve e: (exception / HTTP hata) imzaları.
describe('bildirim politikası', () => {
  it('eksik / kapalı politika susturmaz (eski sunucu cevabı dahil)', () => {
    expect(verdictMutesNotifications(undefined)).toBe(false);
    expect(verdictMutesNotifications(null)).toBe(false);
    expect(verdictMutesNotifications({ muteNotifications: false })).toBe(false);
    expect(verdictMutesNotifications({ muteNotifications: true })).toBe(true);
  });
  it('kapsam: p: ve e: evet, a: ve imzasız hayır', () => {
    expect(verdictCoversNotifications('p:rule|svc')).toBe(true);
    expect(verdictCoversNotifications('e:9f8a')).toBe(true);
    expect(verdictCoversNotifications('a:latency|svc|p99')).toBe(false);
    expect(verdictCoversNotifications(null)).toBe(false);
  });
  it('açıklama durumu doğru söyler', () => {
    expect(verdictNotifyHint('p:r|s', undefined)).toContain('bildirimleri susturmaz');
    expect(verdictNotifyHint('p:r|s', { muteNotifications: false })).toContain('bildirimleri susturmaz');
    expect(verdictNotifyHint('p:r|s', { muteNotifications: true })).toContain('bildirimlerini de susturur');
    expect(verdictNotifyHint('e:fp', { muteNotifications: true })).toContain('bildirimlerini de susturur');
    expect(verdictNotifyHint('a:k|s|p', { muteNotifications: true })).toContain('etkilenmez');
  });
  it('imza kalıbı sunucudakiyle aynı (internal/notify/verdict_silence.go)', () => {
    const go = readFileSync(resolve(__dirname, '../../../internal/notify/verdict_silence.go'), 'utf8');
    // v0.10.1027 — kural imzasının sunucu tanımı chstore'a taşındı (tek tanım:
    // bildirim hunisi ve /api/databases/problems ikisi de çağırır); pin
    // bilinçli güncellendi: notify o fonksiyonu çağırıyor, kalıp chstore'da.
    expect(go).toContain('return chstore.RuleProblemVerdictSignature(p.RuleID, p.Service)');
    const store = readFileSync(resolve(__dirname, '../../../internal/chstore/problem_verdict.go'), 'utf8');
    expect(store).toContain('return "p:" + ruleID + "|" + subject');
    expect(go).toContain('return "e:" + fingerprint');
  });
});
