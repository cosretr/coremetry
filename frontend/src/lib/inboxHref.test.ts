// v0.10.784 — inboxItemHref + attentionRows (servis dikkat şeridi).
// v0.10.1032 (operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor.
// Exception gibi detay gözükmüyor.") — satır tıkının saf kararı (inboxRowOpen),
// ?problem= / ?anomaly= / ?item= dışlayıcılığı ve anomali "kaynağının" tam
// sayfa detaya (/inbox?anomaly=) taşınması.
import { describe, it, expect } from 'vitest';
import {
  anomalyDetailHref, attentionRows, inboxItemHref, inboxItemIdTarget, inboxRowLink, inboxRowOpen, incidentBackState,
  isSloProblem, readIncidentBackTo, readInboxDetail, withInboxDetail,
} from './inboxHref';
import type { InboxItem } from './types';

function item(over: Partial<InboxItem>): InboxItem {
  return {
    id: 'x', kind: 'problem', source: 'Alert rule', priority: 'P2', priorityReason: '',
    severity: 'warning', service: 'shop-payment', title: 't', description: '',
    startedAt: 1, lastSeen: 2, status: 'open', ...over,
  };
}

const prob = (id: string, ruleId = 'r1', priority: InboxItem['priority'] = 'P2') =>
  item({ id: `problem:${id}`, kind: 'problem', priority, problem: { id, ruleId, metric: 'error_rate', value: 9, threshold: 1 } });
const exc = (fp: string, kind: 'exception' | 'httperror' = 'exception') =>
  item({ id: `${kind}:${fp}`, kind, exception: { fingerprint: fp, type: 'T', message: 'm', occurrences: 3 } });
const anom = (id: string) =>
  item({ id: `anomaly:${id}`, kind: 'anomaly', anomaly: { id, kind: 'k', pattern: 'p', peakRatio: 2, currentRatio: 1 } });
const inc = (id: string) =>
  item({ id: `incident:${id}`, kind: 'incident', incident: { id, severity: 'sev2', status: 'open' } });

describe('inboxItemHref', () => {
  it('routes every kind to its detail surface', () => {
    expect(inboxItemHref(prob('a b'))).toBe('/problems?problem=a%20b');
    expect(inboxItemHref(exc('fp/1'))).toBe('/problems?exc=fp%2F1');
    expect(inboxItemHref(exc('fp2', 'httperror'))).toBe('/problems?exc=fp2');
    // v0.10.1032 — BİLİNÇLİ değişiklik: eskiden '/anomalies?event=e1'
    // (560px çekmece). Anomali olayının artık tam sayfa detayı var ve
    // Problems kuyruğunun içinde açılıyor; servis dikkat şeridi + "Open
    // source" oraya iner.
    expect(inboxItemHref(anom('e1'))).toBe('/anomalies/e1');
    expect(inboxItemHref(anom('e/1 x'))).toBe('/anomalies/e%2F1%20x');
    expect(inboxItemHref(inc('i1'))).toBe('/incident?id=i1');
  });
  it('returns null when the payload lacks the native id', () => {
    expect(inboxItemHref(item({ kind: 'problem' }))).toBeNull();
    expect(inboxItemHref(item({ kind: 'exception' }))).toBeNull();
    expect(inboxItemHref(item({ kind: 'anomaly' }))).toBeNull();
  });
  it('anomalyDetailHref — tek kurucu (çekmecenin "Tam detay →" linki de bunu kullanır)', () => {
    expect(anomalyDetailHref('abc')).toBe('/anomalies/abc');
    expect(anomalyDetailHref(anom('e1').anomaly!.id)).toBe(inboxItemHref(anom('e1')));
  });
});

// v0.10.1032 — satır tıkı (ve klavye Enter/o: aynı openRow) — tür → hedef.
describe('inboxRowOpen — Problems kuyruğunda satır nereye açılır', () => {
  it.each<[string, InboxItem, ReturnType<typeof inboxRowOpen>]>([
    // exception ailesi DEĞİŞMEDİ: tam sayfa exception detayına gezinir (v0.9.341).
    ['exception', exc('fp/1'), { to: 'page', href: '/problems?exc=fp%2F1' }],
    ['httperror', exc('404', 'httperror'), { to: 'page', href: '/problems?exc=404' }],
    // alarm kuralı: çekmece DEĞİL, yerinde tam sayfa (?problem=, AlertProblemHost).
    ['problem', prob('p 1'), { to: 'detail', param: 'problem', id: 'p 1' }],
    // anomali: çekmece DEĞİL, yerinde tam sayfa (?anomaly=, AnomalyEventHost).
    ['anomaly', anom('e1'), { to: 'page', href: '/anomalies/e1' }],
    // v0.10.1081 (operatör: "Drawer çıkmasın …") — incident: çekmece DEĞİL,
    // tam sayfa (/incident?id=).
    ['incident', inc('i 1'), { to: 'page', href: '/incident?id=i%201' }],
    // yükü / doğal kimliği eksik satır: çekmece yok — kimlikten aynı hedef.
    ['problem, payload yok', item({ id: 'problem:x', kind: 'problem' }), { to: 'detail', param: 'problem', id: 'x' }],
    ['problem, boş id', item({ id: 'problem:', kind: 'problem', problem: { id: '', ruleId: 'r', metric: 'm', value: 1, threshold: 1 } }), { to: 'none' }],
    ['anomaly, payload yok', item({ id: 'anomaly:x', kind: 'anomaly' }), { to: 'page', href: '/anomalies/x' }],
    ['exception, payload yok', item({ id: 'exception:x', kind: 'exception' }), { to: 'page', href: '/problems?exc=x' }],
    ['incident, payload yok', item({ id: 'incident:i9', kind: 'incident' }), { to: 'page', href: '/incident?id=i9' }],
    ['tanınmayan kimlik', item({ id: 'garbage', kind: 'problem' }), { to: 'none' }],
  ])('%s', (_name, it_, want) => {
    expect(inboxRowOpen(it_)).toEqual(want);
  });
  it('hiçbir tür çekmeceye açılmaz', () => {
    for (const it_ of [prob('a'), exc('b'), exc('c', 'httperror'), anom('d'), inc('e')]) {
      expect(['page', 'detail']).toContain(inboxRowOpen(it_).to);
    }
  });
});

// v0.10.1081 — eski ?item=<tür>:<kimlik> linkleri tam sayfaya yönlendirilir.
describe('inboxItemIdTarget — eski ?item= linkinin hedefi', () => {
  it.each<[string | null, ReturnType<typeof inboxItemIdTarget>]>([
    ['problem:p1', { to: 'detail', param: 'problem', id: 'p1' }],
    ['problem:runtime:jvm-gc:svc-a:pod-1', { to: 'detail', param: 'problem', id: 'runtime:jvm-gc:svc-a:pod-1' }],
    ['anomaly:e1', { to: 'page', href: '/anomalies/e1' }],
    ['incident:i 1', { to: 'page', href: '/incident?id=i%201' }],
    ['exception:fp/1', { to: 'page', href: '/problems?exc=fp%2F1' }],
    ['httperror:404', { to: 'page', href: '/problems?exc=404' }],
    ['problem:', { to: 'none' }],
    [':x', { to: 'none' }],
    ['other:x', { to: 'none' }],
    ['', { to: 'none' }],
    [null, { to: 'none' }],
  ])('%s', (raw, want) => {
    expect(inboxItemIdTarget(raw)).toEqual(want);
  });
});

// v0.10.1081 — incident sayfasının geri bağlantısı yalnız /inbox adresini kabul eder.
describe('readIncidentBackTo', () => {
  it('kuyruk adresini taşır, başka her şeyi reddeder', () => {
    expect(readIncidentBackTo(incidentBackState('/inbox?prio=P1&s_inbox=firstSeen.desc'))).toBe('/inbox?prio=P1&s_inbox=firstSeen.desc');
    expect(readIncidentBackTo(incidentBackState('/inbox'))).toBe('/inbox');
    expect(readIncidentBackTo({ backTo: 'https://evil.example/' })).toBeNull();
    expect(readIncidentBackTo({ backTo: '/inboxes' })).toBeNull();
    expect(readIncidentBackTo({ backTo: 42 })).toBeNull();
    expect(readIncidentBackTo(null)).toBeNull();
    expect(readIncidentBackTo('/inbox')).toBeNull();
  });
});

describe('withInboxDetail / readInboxDetail — ?problem= ve ?anomaly= karşılıklı dışlayıcı', () => {
  const sp = (q: string) => new URLSearchParams(q);
  it('açmak öteki detayı ve çekmeceyi (?item=) siler, yabancı paramları korur', () => {
    const a = withInboxDetail(sp('prio=P1&problem=p1&item=problem:p1&env=prod'), 'anomaly', 'e1');
    expect(a.get('anomaly')).toBe('e1');
    expect(a.has('problem')).toBe(false);
    expect(a.has('item')).toBe(false);
    expect(a.get('prio')).toBe('P1');
    expect(a.get('env')).toBe('prod');
    const p = withInboxDetail(sp('anomaly=e1&q=timeout'), 'problem', 'p1');
    expect(p.get('problem')).toBe('p1');
    expect(p.has('anomaly')).toBe(false);
    expect(p.get('q')).toBe('timeout');
  });
  // v0.10.1032 (inceleme) — kapatmak TÜM detay paramlarını (problem, anomaly,
  // item) siler: elle yazılmış bir link "← Problems"ten sonra öteki detaya ya
  // da çekmeceye sıçramaz; yabancı paramlar kalır.
  it('kapatmak tüm detay paramlarını siler, süzgeçleri korur', () => {
    const c = withInboxDetail(sp('anomaly=e1&problem=p1&item=incident:i1&kind=anomaly'), 'anomaly', null);
    expect(c.has('anomaly')).toBe(false);
    expect(c.has('problem')).toBe(false);
    expect(c.has('item')).toBe(false);
    expect(c.get('kind')).toBe('anomaly');
    expect(readInboxDetail(c)).toBeNull();
    expect(withInboxDetail(sp('problem=p1'), 'problem', '').has('problem')).toBe(false);
  });
  it('girdiyi yerinde DEĞİŞTİRMEZ (setSearchParams prev kopyası)', () => {
    const prev = sp('problem=p1');
    withInboxDetail(prev, 'anomaly', 'e1');
    expect(prev.get('problem')).toBe('p1');
  });
  it('okuma: tek detay; elle ikisi birden yazılmışsa problem kazanır; boş değer yok sayılır', () => {
    expect(readInboxDetail(sp(''))).toBeNull();
    expect(readInboxDetail(sp('item=incident:i1'))).toBeNull();
    expect(readInboxDetail(sp('anomaly=e1'))).toEqual({ param: 'anomaly', id: 'e1' });
    expect(readInboxDetail(sp('problem=p1'))).toEqual({ param: 'problem', id: 'p1' });
    expect(readInboxDetail(sp('anomaly=e1&problem=p1'))).toEqual({ param: 'problem', id: 'p1' });
    expect(readInboxDetail(sp('problem=&anomaly=e1'))).toEqual({ param: 'anomaly', id: 'e1' });
  });
  it('satır tıkının yazdığı URL tek bir detay okur (uçtan uca)', () => {
    const o = inboxRowOpen(prob('p9', 'r'));
    if (o.to !== 'detail') throw new Error('detail bekleniyordu');
    const next = withInboxDetail(sp('anomaly=e1&item=x'), o.param, o.id);
    expect(readInboxDetail(next)).toEqual({ param: 'problem', id: 'p9' });
  });
});

describe('isSloProblem', () => {
  it('matches the slo: rule-id prefix only on problems', () => {
    expect(isSloProblem(prob('p', 'slo:abc:critical'))).toBe(true);
    expect(isSloProblem(prob('p', 'error_rate_high'))).toBe(false);
    expect(isSloProblem(exc('f'))).toBe(false);
  });
});

describe('attentionRows', () => {
  it('drops SLO problems into the footnote count and keeps server order', () => {
    const r = attentionRows([prob('p1', 'r', 'P1'), prob('s1', 'slo:a:critical'), exc('f1'), prob('s2', 'slo:a:warning')]);
    expect(r.rows.map(x => x.id)).toEqual(['problem:p1', 'exception:f1']);
    expect(r.sloCount).toBe(2);
    expect(r.hidden).toBe(0);
  });
  it('excludes anomalies unless asked', () => {
    expect(attentionRows([anom('a'), exc('f')]).rows.map(x => x.id)).toEqual(['exception:f']);
    expect(attentionRows([anom('a'), exc('f')], { includeAnomalies: true }).rows.map(x => x.id))
      .toEqual(['anomaly:a', 'exception:f']);
  });
  it('caps and reports the remainder', () => {
    const many = Array.from({ length: 8 }, (_, i) => exc(`f${i}`));
    const r = attentionRows(many);
    expect(r.rows).toHaveLength(5);
    expect(r.hidden).toBe(3);
  });
  it('skips rows without a destination', () => {
    expect(attentionRows([item({ kind: 'problem' })]).rows).toHaveLength(0);
  });
  // v0.10.1827 — Incidents emekliyken şerit incident satırı çizmez; açıkken aynen.
  it('excludeIncidents drops incident rows only', () => {
    const inc = item({ id: 'incident:i1', kind: 'incident', incident: { id: 'i1', severity: 'critical', status: 'open' } });
    expect(attentionRows([inc, exc('f')]).rows.map(x => x.id)).toEqual(['incident:i1', 'exception:f']);
    expect(attentionRows([inc, exc('f')], { excludeIncidents: true }).rows.map(x => x.id)).toEqual(['exception:f']);
  });
});

// v0.10.1414 (tablo satırları gerçek link) — satır kararının bağlantı hâli.
// Adresler eski openRow / applyOpen ile AYNI: tam sayfa push (incident'te geri
// dönüş durumu), yerinde detay withInboxDetail'in kuyruk adresi (replace).
describe('inboxRowLink — v0.10.1414 satırın gerçek link hedefi', () => {
  const Q = '?prio=P1%2CP2&item=problem%3Aold&env=prod';
  it('tam sayfa (exception): o.href, push, durum yok', () => {
    expect(inboxRowLink({ to: 'page', href: '/problems?exc=fp1' }, '/inbox', Q))
      .toEqual({ to: '/problems?exc=fp1', replace: false, state: undefined });
  });
  it('tam sayfa (incident): kuyruk adresi geri dönüş durumunda', () => {
    expect(inboxRowLink({ to: 'page', href: '/incident?id=i1' }, '/inbox', Q))
      .toEqual({ to: '/incident?id=i1', replace: false, state: incidentBackState('/inbox' + Q) });
  });
  it('yerinde detay: withInboxDetail adresi (yabancı param kalır, ?item= ve öteki detay silinir), replace', () => {
    const l = inboxRowLink({ to: 'detail', param: 'anomaly', id: 'e1' }, '/inbox', Q + '&problem=p0');
    expect(l).toEqual({ to: '/inbox?prio=P1%2CP2&env=prod&anomaly=e1', replace: true });
    const sp = new URLSearchParams(l!.to.split('?')[1]);
    expect(sp.get('prio')).toBe('P1,P2');
    expect(sp.has('problem')).toBe(false);
    expect(sp.has('item')).toBe(false);
  });
  it('çözülemeyen satır: null (link / işaret yok)', () => {
    expect(inboxRowLink({ to: 'none' }, '/inbox', Q)).toBeNull();
  });
});
