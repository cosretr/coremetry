// argocdDiscovery.test.ts — v0.10.974 — keşif panelinin saf yarısı: taslağa
// karşı yeniden işaretleme (Go matchConfigured/suggestID/uniqueID aynası),
// namespaceCase notları, Türkçe hata önekleri, sayı biçimi, hub durumu
// önceliği (hata > kısmi > limitli > boş > ok), hata yanıtı eşlemesi (401/403
// "yetki yok" — v0.10.978, 429, 504, 400 guardrail) ve özet/ayrıntı satırları.
import { describe, it, expect } from 'vitest';
import type { ArgoCDCandidate, ArgoCDDiscoverResult } from '@/lib/types';
import type { InstanceDraft, RemoteCluster } from './argocdForm';
import {
  candidateErrorNote, completionText, failureState, fmtCount, fmtSec, hubBadge, hubState, hubView, markCandidates,
  matchInstance, panelMeta, parseDiscoverError, preflight, progressText, shownResult, suggestId, uniqueId, type HubRun,
} from './argocdDiscovery';

const H1 = 'c-4f7a21d9';
const H2 = 'c-9b03e6c2';
const CLUSTERS: RemoteCluster[] = [
  { id: H1, name: 'hub-1', enabled: true, label: 'cluster="hub-1"' },
  { id: H2, name: 'hub-2', enabled: true, label: 'cluster="hub-2"' },
  { id: 'c-9c0d1e20', name: 'cluster-c', enabled: false, label: '' },
];

const cand = (over: Partial<ArgoCDCandidate>): ArgoCDCandidate => ({
  id: 'x', hubClusterId: H1, name: 'x', hubNamespace: 'x', metricsJob: 'x-metrics', appsAnyNamespace: false, discovered: true,
  namespaceCase: 'A', ...over,
});
const inst = (over: Partial<InstanceDraft>): InstanceDraft => ({
  key: 'k', origin: 'saved', savedId: 'a', id: 'a', hubClusterId: H1, name: '', hubNamespace: 'a', metricsJob: '', apiUrl: '',
  storedRef: '', tokenInput: '', clearToken: false, insecureSkipVerify: false, enabled: true, discovered: false, appsAnyNamespace: false, ...over,
});
const result = (over: Partial<ArgoCDDiscoverResult>): ArgoCDDiscoverResult => ({
  hubClusterId: H1, hubName: 'hub-1', injectClusterLabel: true, window: { start: 0, end: 1 }, candidates: [], jobsTruncated: false,
  calls: 3, saved: false, ...over,
});

describe('suggestId / uniqueId (Go aynası)', () => {
  it.each([
    ['team-a-prod', 'team-a-prod'], ['OpenShift_GitOps', 'openshift-gitops'], ['--x--', 'x'], ['', 'instance'], ['a'.repeat(70), 'a'.repeat(63)],
  ])('%s → %s', (inp, want) => { expect(suggestId(inp)).toBe(want); });
  it('-2, -3 ve 63 sınırı', () => {
    expect(uniqueId('a', new Set(['a']))).toBe('a-2');
    expect(uniqueId('a', new Set(['a', 'a-2']))).toBe('a-3');
    expect(uniqueId('b'.repeat(63), new Set(['b'.repeat(63)]))).toBe('b'.repeat(61) + '-2');
  });
});

describe('matchInstance (matchConfigured aynası)', () => {
  it('ns + iş; kayıtta iş boşsa yalnız ns; aday ns\'i yoksa yalnız iş; hatalı aday eşlenmez', () => {
    const a = inst({ key: 'a', hubNamespace: 'team-a', metricsJob: 'team-a-metrics' });
    const b = inst({ key: 'b', id: 'b', hubNamespace: 'team-b', metricsJob: '' });
    const c = inst({ key: 'c', id: 'c', hubNamespace: 'zzz', metricsJob: 'job-c' });
    expect(matchInstance(cand({ hubNamespace: 'team-a', metricsJob: 'team-a-metrics' }), [a, b, c])?.key).toBe('a');
    expect(matchInstance(cand({ hubNamespace: 'team-a', metricsJob: 'other' }), [a, b, c])).toBeUndefined();
    expect(matchInstance(cand({ hubNamespace: 'team-b', metricsJob: 'anything' }), [a, b, c])?.key).toBe('b');
    expect(matchInstance(cand({ hubNamespace: '', metricsJob: 'job-c' }), [a, b, c])?.key).toBe('c');
    expect(matchInstance(cand({ hubNamespace: 'team-a', metricsJob: 'team-a-metrics', error: 'timeout: x' }), [a])).toBeUndefined();
  });
});

describe('markCandidates — taslağa karşı', () => {
  const instances = [
    inst({ key: 's:team-a-prod', id: 'team-a-prod', hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics' }),
    inst({ key: 'n:1', origin: 'new', savedId: '', id: 'team-c-uat', hubNamespace: 'team-c-uat', metricsJob: 'team-c-uat-metrics', discovered: true }),
    inst({ key: 's:team-b-int', id: 'team-b-int', hubClusterId: H2, hubNamespace: 'team-b-int', metricsJob: 'team-b-int-metrics' }),
  ];
  const r1 = [
    cand({ hubNamespace: 'openshift-gitops', metricsJob: 'openshift-gitops-metrics', namespaceCase: 'C', appCount: 1184, shardCount: 3 }),
    cand({ hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics', appCount: 412, shardCount: 2 }),
    cand({ hubNamespace: 'team-c-uat', metricsJob: 'team-c-uat-metrics', namespaceCase: 'B', note: 'exported_namespace yok (honorLabels: true): namespace Application ns\'idir; hubNamespace tahmini — doğrulayın' }),
    cand({ hubNamespace: '', metricsJob: 'team-d-uat-metrics', error: 'timeout: query timed out', namespaceCase: undefined }),
    cand({ hubNamespace: 'team-b-int', metricsJob: 'team-b-int-metrics', shardCount: 100, shardCountTruncated: true, countNote: 'sayım atlandı: keşif bütçesi doldu' }),
  ];
  const r2 = [
    cand({ hubClusterId: H2, hubNamespace: 'openshift-gitops', metricsJob: 'openshift-gitops-metrics' }),
    cand({ hubClusterId: H2, hubNamespace: 'team-b-int', metricsJob: 'team-b-int-metrics' }),
  ];
  const m = markCandidates([{ clusterId: H1, candidates: r1 }, { clusterId: H2, candidates: r2 }], instances, CLUSTERS);

  it('sıra yeni → okunamayan → kayıtlı; kayıtlı YALNIZ aynı hub\'daki kayıtlı satır', () => {
    const rows = m.get(H1)!;
    expect(rows.map(r => r.status)).toEqual(['new', 'added', 'new', 'error', 'saved']);
    expect(rows.map(r => r.cand.hubNamespace)).toEqual(['openshift-gitops', 'team-c-uat', 'team-b-int', '', 'team-a-prod']);
    // hub-1'deki team-b-int adayı, hub-2'deki KAYITLI team-b-int ile eşlenmez → yeni, kimlik çakışmadan
    expect(rows[2]).toMatchObject({ status: 'new', id: 'team-b-int-2' });
    expect(rows[1]).toMatchObject({ status: 'added', id: 'team-c-uat', matchKey: 'n:1' });
    expect(rows[4]).toMatchObject({ status: 'saved', id: 'team-a-prod', matchKey: 's:team-a-prod' });
  });
  it('öteki hub\'ın önerisiyle çakışmaz; aynı ns iki hub\'da ayrı instance notu', () => {
    const h2 = m.get(H2)!;
    expect(h2.map(r => r.status)).toEqual(['new', 'saved']);
    expect(h2[0].id).toBe('openshift-gitops-2');
    expect(h2[0].note).toBe("aynı namespace hub-1'de de var — ayrı instance");
    expect(m.get(H1)![0].note).toBe('apps-in-any-namespace: exported_namespace ≠ namespace');
  });
  it('notlar namespaceCase\'ten, B uyarı tonu, hata Türkçe', () => {
    const rows = m.get(H1)!;
    expect(rows[1].noteTone).toBe('warn');
    expect(rows[3]).toMatchObject({ note: 'okunamadı: zaman aşımı (15 sn)', noteTone: 'err', noteTitle: 'timeout: query timed out', apps: '—', shards: '—' });
  });
  it('sayılar: 1.184 · eksik — · kesik ≥100; title = countNote', () => {
    const rows = m.get(H1)!;
    expect([rows[0].apps, rows[0].shards]).toEqual(['1.184', '3']);
    expect([rows[1].apps, rows[1].shards]).toEqual(['—', '—']);
    expect(rows[2]).toMatchObject({ shards: '≥100', countTitle: 'sayım atlandı: keşif bütçesi doldu' });
  });
});

describe('Türkçe hata önekleri ve sayı biçimi', () => {
  it.each([
    ['timeout: x', 'okunamadı: zaman aşımı (15 sn)'], ['unavailable: x', 'okunamadı: erişilemedi'], ['internal: x', 'okunamadı: iç hata'],
    ['bad_data: x', 'okunamadı: geçersiz sorgu'], ['execution: x', 'okunamadı: değerlendirme hatası'],
    ['response_too_large: x', 'okunamadı: yanıt çok büyük'], ['skipped: discovery budget exhausted', 'atlandı: keşif bütçesi doldu'],
    ['unauthorized: thanos rejected the cluster credentials (HTTP 403)', 'okunamadı: yetki yok'], // v0.10.978
    ['hub query failed', 'okunamadı'],
  ])('%s → %s', (e, want) => { expect(candidateErrorNote(e)).toBe(want); });
  it('fmtCount / fmtSec', () => {
    expect(fmtCount(1184)).toBe('1.184');
    expect(fmtCount(undefined)).toBe('—');
    expect(fmtCount(100, true)).toBe('≥100');
    expect(fmtCount(0)).toBe('0');
    expect(fmtSec(3100)).toBe('3,1');
  });
});

describe('hub durumu ve hata eşlemesi', () => {
  const done = (r: Partial<ArgoCDDiscoverResult>): HubRun => ({ kind: 'done', result: result(r), ms: 1200, oneShot: false });
  it('öncelik: hata > kısmi > limitli > boş > ok', () => {
    expect(hubState(done({ candidates: [cand({})] }))).toBe('ok');
    expect(hubState(done({ candidates: [] }))).toBe('empty');
    expect(hubState(done({ candidates: [], jobsTruncated: true }))).toBe('truncated');
    expect(hubState(done({ candidates: [cand({ note: 'değer listesi probe limitinde kesildi (truncated)' })] }))).toBe('truncated');
    expect(hubState(done({ jobsTruncated: true, incomplete: true }))).toBe('partial');
    expect(hubState(done({ jobsTruncated: true, candidates: [cand({ error: 'timeout: x' })] }))).toBe('partial');
    expect(hubState({ kind: 'failed', err: { status: 502, error: 'x', errorType: 'unavailable' }, ms: 1, oneShot: false })).toBe('unreachable');
  });
  it('countsIncomplete rozeti değiştirmez', () => {
    expect(hubState(done({ candidates: [cand({})], countsIncomplete: true }))).toBe('ok');
    expect(hubBadge('ok')).toBeNull();
  });
  it.each([
    [{ status: 400, error: 'hub Remote Cluster bilinmiyor ya da devre dışı', errorType: 'guardrail' }, 'not_configured', 'yapılandırılmamış'],
    [{ status: 504, error: 'x', errorType: 'timeout' }, 'timeout', 'zaman aşımı'],
    [{ status: 502, error: 'thanos rejected the cluster credentials (HTTP 403)', errorType: 'unauthorized', upstreamStatus: 403 }, 'unauthorized', 'yetki yok'], // v0.10.978
    [{ status: 502, error: 'thanos is unavailable (HTTP 503)', errorType: 'unavailable', upstreamStatus: 503 }, 'unreachable', 'erişilemedi'],
    [{ status: 502, error: 'hub query failed', errorType: 'internal' }, 'unreachable', 'erişilemedi'],
    [{ status: 429, error: 'bir Argo CD keşfi zaten koşuyor' }, 'busy', null],
    // v0.10.978 — bizim oturum/rol kapımız (auth.writeForbidden `{"error":"…"}`, errorType yok)
    // "yetki yok" kutusunu TETİKLEMEZ: durum yalnız errorType'tan okunur. 403 gerçek yol
    // (rol yetersiz → api.request "HTTP 403: …" → parseArgoHttpError status 403); 401 saf
    // fonksiyon pini — pratikte api.request onu UnauthorizedError'a çevirir (status 0 + çıkış).
    [{ status: 403, error: 'forbidden' }, 'unreachable', 'erişilemedi'],
    [{ status: 401, error: 'unauthorized' }, 'unreachable', 'erişilemedi'],
    [{ status: 0, error: 'Request timed out after 75s — try a narrower time range or fewer filters' }, 'timeout', 'zaman aşımı'],
  ] as const)('%o → %s', (err, st, badge) => {
    expect(failureState(err)).toBe(st);
    expect(hubBadge(failureState(err))?.text ?? null).toBe(badge);
  });
  // v0.10.978 — sunucu 401/403'ü errorType unauthorized + upstreamStatus ile
  // döner (HTTP 502 kalır): States (b) hub-1 — "yetki yok" rozeti, özet
  // upstream kodunu söyler, ayrıntı satırı yok, alert kutusu adımları taşır.
  it('"yetki yok": errorType unauthorized → rozet + HTTP <upstream> özeti + unauthorized kutusu', () => {
    const at = (upstreamStatus?: number): HubRun => ({ kind: 'failed', oneShot: false, ms: 300,
      err: { status: 502, error: `thanos rejected the cluster credentials (HTTP ${upstreamStatus ?? 403})`, errorType: 'unauthorized', ...(upstreamStatus ? { upstreamStatus } : {}) } });
    const H = { name: 'hub-1', label: 'cluster="hub-1"', clusterId: H1 };
    const v = hubView(at(403), [], H);
    expect(v.state).toBe('unauthorized');
    expect(v.badge).toEqual({ text: 'yetki yok', tone: 'danger' });
    expect(v.summary).toBe('0 aday · 0,3 sn · HTTP 403'); // hata özetinde "N çağrı" yok; kod upstream'in
    expect(v.detail).toEqual([]);
    expect(v.notice).toEqual({ kind: 'unauthorized' });
    expect(JSON.stringify(v)).not.toContain('erişilemedi');
    expect(hubView(at(401), [], H).summary).toBe('0 aday · 0,3 sn · HTTP 401');
    expect(hubView(at(undefined), [], H).summary).toBe('0 aday · 0,3 sn · HTTP 502'); // upstream kodu yoksa bizimki
  });
  it('parseDiscoverError: upstreamStatus gövdeden; JSON olmayan / alanı olmayan gövdede yok', () => {
    expect(parseDiscoverError(new Error(`HTTP 502: {"error":"thanos rejected the cluster credentials (HTTP 403)","errorType":"unauthorized","upstreamStatus":403,"hubClusterId":"${H1}"}`)))
      .toEqual({ status: 502, error: 'thanos rejected the cluster credentials (HTTP 403)', errorType: 'unauthorized', upstreamStatus: 403 });
    expect(parseDiscoverError(new Error('HTTP 429: {"error":"bir Argo CD keşfi zaten koşuyor"}'))).toEqual({ status: 429, error: 'bir Argo CD keşfi zaten koşuyor' });
    expect(parseDiscoverError(new Error('HTTP 502: <html>Bad Gateway</html>'))).toEqual({ status: 502, error: '<html>Bad Gateway</html>' });
    expect(parseDiscoverError(new Error('HTTP 502: {"error":"x","errorType":"unavailable","upstreamStatus":"503"}'))).toEqual({ status: 502, error: 'x', errorType: 'unavailable' });
    expect(parseDiscoverError(new Error('Request timed out after 75s'))).toEqual({ status: 0, error: 'Request timed out after 75s' });
  });
  it('429 metni', () => {
    const v = hubView({ kind: 'failed', err: { status: 429, error: 'bir Argo CD keşfi zaten koşuyor' }, ms: 10, oneShot: false }, [], { name: 'hub-1', label: '', clusterId: H1 });
    expect(v.detail).toEqual(['Bir Argo CD keşfi zaten koşuyor — bitince yeniden arayın.']);
    expect(v.badge).toBeNull();
  });
  it('preflight: kayıt yok / devre dışı → istek yok', () => {
    expect(preflight(H1, CLUSTERS)).toBeNull();
    expect(preflight('c-77d0aa15', CLUSTERS)).toBe('missing');
    expect(preflight('c-9c0d1e20', CLUSTERS)).toBe('disabled');
    const v = hubView({ kind: 'skipped', reason: 'disabled' }, [], { name: 'cluster-c', label: '', clusterId: 'c-9c0d1e20' });
    expect(v.badge?.text).toBe('yapılandırılmamış');
    expect(v.summary).toBe('istek gönderilmedi · Remote Cluster kaydı devre dışı');
  });
});

describe('özet / ayrıntı satırları', () => {
  const H = { name: 'hub-2', label: 'cluster="hub-2"', clusterId: H2 };
  it('ok özeti (mockup biçimi) ve countsIncomplete satırı', () => {
    const r = result({ hubClusterId: H2, calls: 21, countsIncomplete: true, candidates: [
      cand({ hubNamespace: 'a' }), cand({ hubNamespace: 'b', countNote: 'sayım atlandı: keşif bütçesi doldu' }),
    ] });
    const rows = markCandidates([{ clusterId: H2, candidates: r.candidates }], [], CLUSTERS).get(H2)!;
    const v = hubView({ kind: 'done', result: r, ms: 3100, oneShot: false }, rows, H);
    expect(v.summary).toBe('2 aday · 2 yeni · 21 çağrı · 3,1 sn · küme etiketi eklendi (cluster="hub-2")');
    expect(v.badge).toBeNull();
    expect(v.detail).toEqual(['Uygulama/shard sayısı 1 adayda eksik: keşif bütçesi doldu.']);
  });
  it('kısmi: okunamayan iş satırı; bütçe; limitli', () => {
    const r = result({ hubClusterId: H2, injectClusterLabel: false, jobsTruncated: true, candidates: [
      cand({ hubNamespace: 'a' }), cand({ hubNamespace: '', metricsJob: 'team-d-uat-metrics', error: 'timeout: t' }),
      cand({ hubNamespace: '', metricsJob: 'team-e-metrics', error: 'skipped: discovery budget exhausted' }),
    ] });
    const rows = markCandidates([{ clusterId: H2, candidates: r.candidates }], [], CLUSTERS).get(H2)!;
    const v = hubView({ kind: 'done', result: r, ms: 15800, oneShot: false }, rows, H);
    expect(v.badge).toEqual({ text: 'kısmi', tone: 'warning' });
    expect(v.summary).toBe('1 aday · 1 yeni · 2 iş okunamadı · 3 çağrı · 15,8 sn · küme etiketi eklenmedi');
    expect(v.detail).toEqual([
      "team-d-uat-metrics işinin namespace'i 15 sn'de okunamadı; diğer adaylar tam.",
      'Keşif bütçesi doldu (hub başına ≤150 çağrı / 60 sn): kalan işler atlandı; listelenen adaylar doğru → yeniden arayın.',
      "≤50 iş ya da iş başına ≤100 değer sınırı doldu; liste eksik → eksik namespace'i elle ekleyin.",
    ]);
  });
  it('boş + etiket → etiketsiz yeniden ara kutusu; tek seferlikten sonra etiketsiz kutu', () => {
    const empty = result({ hubClusterId: H2, calls: 1 });
    expect(hubView({ kind: 'done', result: empty, ms: 400, oneShot: false }, [], H).notice).toEqual({ kind: 'emptyLabel', label: 'cluster="hub-2"' });
    expect(hubView({ kind: 'done', result: empty, ms: 400, oneShot: false }, [], H).summary)
      .toBe('0 aday · 1 çağrı · 0,4 sn · küme etiketi eklendi (cluster="hub-2")');
    const oneShot = result({ hubClusterId: H2, calls: 1, injectClusterLabel: false });
    expect(hubView({ kind: 'done', result: oneShot, ms: 400, oneShot: true }, [], H).notice).toEqual({ kind: 'emptyNoLabel', afterOneShot: true });
    const found = result({ hubClusterId: H2, injectClusterLabel: false, candidates: [cand({})] });
    const rows = markCandidates([{ clusterId: H2, candidates: found.candidates }], [], CLUSTERS).get(H2)!;
    expect(hubView({ kind: 'done', result: found, ms: 400, oneShot: true }, rows, H).notice).toEqual({ kind: 'oneShotFound' });
  });
  it('panel meta, ilerleme, tamamlama', () => {
    const v = hubView({ kind: 'done', result: result({ calls: 2 }), ms: 1, oneShot: false }, [], H);
    expect(panelMeta('20:52', [v, v])).toBe("20:52 · son 1 saat · 2 hub · 4 çağrı · hiçbir hub'da aday yok");
    expect(progressText(['hub-1'], 'hub-2')).toBe('hub-1 bitti · hub-2 aranıyor…');
    expect(completionText([{ name: 'hub-1', view: { ...v, candidates: 5, errors: 0 } }, { name: 'hub-2', view: { ...v, candidates: 3, errors: 1 } }]))
      .toBe('Keşif tamamlandı: 2 hub, 8 aday, 1 iş okunamadı.');
    expect(completionText([{ name: 'hub-2', view: { ...v, candidates: 4, errors: 0 } }])).toBe('hub-2 keşfi tamamlandı: 4 aday.');
  });
});

// v0.10.974 — yeniden arama sürerken önceki aday tablosu kalır (mockup
// buildDisc): `shownResult` koşan hub'ın ÖNCEKİ bitmiş sonucunu verir; özet
// "aranıyor…", rozet/ayrıntı/ek kutu gizli.
describe('yeniden arama: önceki sonuç görünür kalır', () => {
  it('shownResult: bitmiş → kendisi; koşan → prev; hata/atlandı → yok', () => {
    const done: HubRun = { kind: 'done', result: result({ candidates: [cand({})], jobsTruncated: true }), ms: 900, oneShot: false };
    expect(shownResult(done)).toBe(done);
    const running: HubRun = { kind: 'running', oneShot: false, prev: done.kind === 'done' ? done : undefined };
    expect(shownResult(running)).toBe(done);
    expect(shownResult({ kind: 'running', oneShot: false })).toBeUndefined();
    expect(shownResult({ kind: 'skipped', reason: 'missing' })).toBeUndefined();
    expect(shownResult({ kind: 'failed', err: { status: 504, error: 'x' }, ms: 1, oneShot: false })).toBeUndefined();
    expect(shownResult(undefined)).toBeUndefined();
    const v = hubView(running, [], { name: 'hub-1', label: 'cluster="hub-1"', clusterId: H1 });
    expect(v).toMatchObject({ summary: 'aranıyor…', badge: null, detail: [], notice: null });
  });
});
