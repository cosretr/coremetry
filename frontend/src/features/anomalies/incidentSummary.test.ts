// incidentSummary — v0.10.1081 (operatör: "ekteki hata mesela hiç
// anlaşılmıyor"). Incident tam sayfasının manşeti, birincil problemi ve
// "Ne yapabilirim" pivotları; tablo testli, sentetik adlarla.
import { describe, it, expect } from 'vitest';
import type { Problem } from '@/lib/types';
import {
  attachedProblemLabel, incidentHeadline, incidentPivotLinks, incidentPrimaryProblem, problemSentence,
} from './incidentSummary';

const NOW = 1_790_000_000e9;
const prob = (over: Partial<Problem>): Problem => ({
  id: 'p1', ruleId: 'r1', ruleName: 'Rule', severity: 'critical', service: 'svc-checkout',
  metric: 'error_rate', value: 9, threshold: 1, status: 'open', description: '',
  startedAt: NOW - 600e9, ...over,
} as Problem);

const DB_RULE = 'db-health:couchbase@db-host-1/orders';
const dbProblem = prob({
  id: 'db1', ruleId: DB_RULE, ruleName: 'DB health · couchbase', service: 'db:couchbase@orders', kind: 'db',
  description: 'couchbase orders veritabanında hata oranı %100.0 (eşik %5), 3 çağıran servis etkilendi (svc-a, svc-b, svc-c) — 1200 çağrı / 5 dk.',
});

describe('incidentPrimaryProblem', () => {
  it('en erken başlayan; eşitlikte kimlik sırası; boşta null', () => {
    const a = prob({ id: 'b', startedAt: 5 });
    const b = prob({ id: 'a', startedAt: 5 });
    const c = prob({ id: 'c', startedAt: 9 });
    expect(incidentPrimaryProblem([c, a, b])?.id).toBe('a');
    expect(incidentPrimaryProblem([])).toBeNull();
  });
});

describe('problemSentence', () => {
  it('açıklama düz cümle olarak; kapanış eki düşer', () => {
    expect(problemSentence(dbProblem)).toContain('couchbase orders veritabanında hata oranı %100.0 (eşik %5), 3 çağıran servis etkilendi');
    expect(problemSentence(prob({ description: 'x oldu · auto-resolved: source silent' }))).toBe('x oldu');
  });
  it('açıklama boşsa alarm özeti cümlesi', () => {
    expect(problemSentence(prob({ ruleName: 'High error rate', description: '  ' })))
      .toBe('High error rate — svc-checkout: error_rate değeri 9.00, eşik 1.00.');
  });
});

describe('incidentHeadline', () => {
  const inc = { title: 'db:couchbase@orders — DB health · couchbase', summary: 'kayıtlı özet' };
  it('birincil problemin cümlesi (en erken başlayan)', () => {
    const later = prob({ id: 'z', startedAt: NOW, description: 'sonraki problem' });
    expect(incidentHeadline(inc, [later, dbProblem])).toBe(problemSentence(dbProblem));
  });
  it('problem yoksa kayıtlı özet, o da yoksa başlık — gerekçe metni ASLA', () => {
    expect(incidentHeadline(inc, [])).toBe('kayıtlı özet');
    expect(incidentHeadline({ title: 'T', summary: '' }, [])).toBe('T');
    expect(incidentHeadline(inc, [dbProblem])).not.toMatch(/Declared incident|kaynak önceliği/);
  });
});

describe('incidentPivotLinks', () => {
  const incDb = { service: 'db:couchbase@orders', startedAt: NOW - 600e9 };
  it('tek db-health problemi → veritabanı sayfası + trace pivotları ondan', () => {
    const p = incidentPivotLinks(incDb, [dbProblem], NOW);
    expect(p.links.map(l => l.label)).toEqual(['Veritabanı sayfası', "Trace'ler", "Hatalı trace'ler"]);
    expect(p.links[0].to).toContain('/database?system=couchbase&instance=db-host-1&name=orders');
    expect(p.links[2].to).toContain('hasError=true');
  });
  it('tek servis problemi → servisin log / trace / servis sayfası pivotları, problem penceresiyle', () => {
    const p = incidentPivotLinks({ service: 'svc-checkout', startedAt: NOW - 600e9 }, [prob({})], NOW);
    expect(p.links.map(l => l.label)).toEqual(['Hata logları', 'Loglar', "Hatalı trace'ler", 'Servis sayfası']);
    expect(p.links[0].to).toContain('service=svc-checkout');
    expect(p.links[0].to).toContain('severity=17');
    expect(p.links[2].to).toContain('hasError=true');
    expect(p.links.every(l => l.sub.includes('problem penceresi'))).toBe(true);
  });
  it('çoklu problem → incident servisi ve incident penceresi', () => {
    const p = incidentPivotLinks({ service: 'svc-checkout', startedAt: NOW - 600e9 }, [], NOW);
    expect(p.links.every(l => l.sub.includes('incident penceresi'))).toBe(true);
  });
  it('servis olmayan özne / servissiz incident → link yok, nedenini söyler', () => {
    const db = incidentPivotLinks(incDb, [], NOW);
    expect(db.links).toEqual([]);
    expect(db.note).toContain('veritabanı');
    const none = incidentPivotLinks({ service: '', startedAt: NOW }, [], NOW);
    expect(none.links).toEqual([]);
    expect(none.note).toContain('servis adı');
  });
});

describe('attachedProblemLabel', () => {
  it('kural adı — okunur özne', () => {
    expect(attachedProblemLabel(dbProblem)).toBe('DB health · couchbase — couchbase · orders');
    expect(attachedProblemLabel(prob({ ruleName: '', metric: 'p99_ms', service: '' }))).toBe('p99_ms');
  });
});
