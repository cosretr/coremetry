// argocdForm.test.ts — v0.10.974 — Ayarlar › Argo CD saf form çekirdeği:
// taslak kurulumu, PUT gövdesi (taslak sırası, token kuralları, pins aynen),
// NormalizeAPIURL aynası (Go settings_test.go TestNormalizeAPIURL vakaları),
// tüm istemci doğrulayıcıları (BE3 aynası dahil), diffPhrases,
// parseArgoHttpError, alan yolu → taslak satırı ve trLocative.
import { describe, it, expect } from 'vitest';
import type { ArgoCDBound, ArgoCDSettings } from '@/lib/types';
import {
  advError, advSummary, applyBuffer, blankBuffer, bufferDirty, bufferErrorsFromIssues, bufferFrom, diffPhrases, dirtyText,
  draftFromSettings, effectiveRef, fmtHourMinute, fmtTr, hasControl, instanceInput, issueFromServer, mergeDraft, normalizeApiUrl, numToInput,
  parseAdvInput, parseArgoHttpError, reloadText, remoteClustersFrom, savedAtText, targetForPath, toPutBody, tokenRank, tokenState,
  trLocative, trPossessive3, validTokenRef, validateBuffer, validateDraft, type Draft, type InstanceDraft, type RemoteCluster,
} from './argocdForm';

const H1 = 'c-4f7a21d9';
const H2 = 'c-9b03e6c2';
const CLUSTERS: RemoteCluster[] = [
  { id: H1, name: 'hub-1', enabled: true, label: 'cluster="hub-1"' },
  { id: H2, name: 'hub-2', enabled: true, label: 'cluster="hub-2"' },
  { id: 'c-1d2e3f40', name: 'cluster-a', enabled: true, label: '' },
  { id: 'c-9c0d1e20', name: 'cluster-c', enabled: false, label: '' },
];

const BOUNDS: Record<string, ArgoCDBound> = {
  'apiWorker.rps': { min: 0.1, max: 20, default: 1 },
  'apiWorker.burst': { min: 1, max: 50, default: 5 },
  'apiWorker.maxConcurrent': { min: 1, max: 10, default: 2 },
  'classification.windowMin': { min: 1, max: 60, default: 5 },
  'classification.outOfBandLookbackMin': { min: 5, max: 240, default: 30 },
  'reader.maxSeries': { min: 1000, max: 50000, default: 50000 },
  'reader.maxBodyMiB': { min: 1, max: 64, default: 64 },
  'reader.timeoutS': { min: 5, max: 45, default: 30 },
  'intervals.metricsS': { min: 30, max: 600, default: 60 },
  'intervals.inventoryMin': { min: 5, max: 120, default: 15 },
  'intervals.mapperMin': { min: 5, max: 240, default: 10 },
  'intervals.classifierReevalH': { min: 1, max: 168, default: 24 },
  'mapping.nameConfidence': { min: 1, max: 100, default: 70 },
  'mapping.namespaceConfidence': { min: 1, max: 100, default: 30 },
};

function settings(over: Partial<ArgoCDSettings> = {}): ArgoCDSettings {
  return {
    enabled: true,
    hubs: [{ clusterId: H1, injectClusterLabel: true }, { clusterId: H2 }],
    envList: ['prod', 'int'],
    instances: [
      { id: 'team-a-prod', hubClusterId: H1, name: 'Team A prod', hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics', apiUrl: 'https://argocd.team-a-prod.example.invalid', tokenRef: 'env:ARGOCD_TEAM_A_TOKEN', enabled: true, discovered: true },
      { id: 'team-b-int', hubClusterId: H2, hubNamespace: 'team-b-int', enabled: true },
    ],
    apiWorker: { rps: 1.5 }, classification: {}, reader: {}, intervals: {}, mapping: {},
    pins: [{ clusterId: 'c-1d2e3f40', namespace: 'team-a', workloadKind: 'Deployment', workload: 'api', instanceId: 'team-a-prod', appNamespace: 'team-a-prod', appName: 'team-a-api-prod-ca' }],
    ...over,
  };
}

function inst(over: Partial<InstanceDraft> = {}): InstanceDraft {
  return {
    key: 'n:x', origin: 'new', savedId: '', id: 'team-c-prod', hubClusterId: H1, name: '', hubNamespace: 'team-c-prod',
    metricsJob: '', apiUrl: '', storedRef: '', tokenInput: '', clearToken: false, insecureSkipVerify: false,
    enabled: true, discovered: false, appsAnyNamespace: false, ...over,
  };
}

describe('remoteClustersFrom', () => {
  it('id\'siz satırı atar, devre dışını tutar, etiketi (boş değer = ad) kurar', () => {
    const rc = remoteClustersFrom([
      { id: H1, name: 'hub-1', url: 'u', hasToken: false, enabled: true, thanosLabelName: 'cluster', thanosLabelValue: '' },
      { name: 'eski', url: 'u', hasToken: false, enabled: true },
      { id: 'c-9c0d1e20', name: 'cluster-c', url: 'u', hasToken: false, enabled: false },
    ]);
    expect(rc).toEqual([
      { id: H1, name: 'hub-1', enabled: true, label: 'cluster="hub-1"' },
      { id: 'c-9c0d1e20', name: 'cluster-c', enabled: false, label: '' },
    ]);
  });
});

describe('draftFromSettings', () => {
  it('SAKLANAN blobdan: eksik diziler [], inject yoksa true, token girdisi boş', () => {
    const d = draftFromSettings({ enabled: false, apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {} });
    expect(d.hubs).toEqual([]);
    expect(d.instances).toEqual([]);
    expect(d.envList).toEqual([]);
    expect(d.adv['apiWorker.rps']).toBe('');
    expect(d.metricsOnlyMode).toBe('');
    const s = draftFromSettings(settings());
    expect(s.hubs.map(h => h.inject)).toEqual([true, true]);
    expect(s.instances[0]).toMatchObject({ origin: 'saved', savedId: 'team-a-prod', storedRef: 'env:ARGOCD_TEAM_A_TOKEN', tokenInput: '', clearToken: false });
    expect(s.adv['apiWorker.rps']).toBe('1,5');
    expect(new Set(s.instances.map(i => i.key)).size).toBe(2);
  });
});

describe('toPutBody — sıra, bölümler, pins', () => {
  it('her bölüm gider; pins yüklendiği gibi; boş gelişmiş = 0; ondalık virgül', () => {
    const s = settings();
    const d = draftFromSettings(s);
    d.adv['reader.timeoutS'] = '40';
    const body = toPutBody(d, s.pins ?? [], 1_790_000_000_000_000_000);
    expect(Object.keys(body).sort()).toEqual(['apiWorker', 'classification', 'enabled', 'envList', 'expectedUpdatedAt', 'hubs', 'instances', 'intervals', 'mapping', 'pins', 'reader']);
    expect(body.pins).toEqual(s.pins);
    expect(body.hubs).toEqual([{ clusterId: H1, injectClusterLabel: true }, { clusterId: H2, injectClusterLabel: true }]);
    expect(body.apiWorker).toEqual({ rps: 1.5, burst: 0, maxConcurrent: 0 });
    expect(body.reader).toEqual({ maxSeries: 0, maxBodyMiB: 0, timeoutS: 40 });
    expect(body.classification.metricsOnlyMode).toBe('');
    expect(body).not.toHaveProperty('updatedAt');
  });
  it('instances TASLAK sırasıyla (yeni satır sonda)', () => {
    const d = draftFromSettings(settings());
    d.instances.push(inst({ key: 'n:1', id: 'aaa-first-alpha' }));
    expect(toPutBody(d, [], 0).instances.map(i => i.id)).toEqual(['team-a-prod', 'team-b-int', 'aaa-first-alpha']);
  });
  // v0.10.978 — iyimser ön koşul: GET'te görülen updatedAt HER PUT'ta geri gider
  // (hiç kaydedilmemiş blob = 0; sunucu 0'ı da damgayla karşılaştırır — arada
  // biri kaydettiyse 409). Sunucu sahipli `updatedAt` gövdede YOK.
  it('expectedUpdatedAt = GET updatedAt (yoksa 0) gövdede; updatedAt gitmez', () => {
    const d = draftFromSettings(settings());
    expect(toPutBody(d, [], 1_790_000_000_000_000_000).expectedUpdatedAt).toBe(1_790_000_000_000_000_000);
    const zero = toPutBody(d, [], 0);
    expect(zero.expectedUpdatedAt).toBe(0);
    expect('expectedUpdatedAt' in zero).toBe(true);
    expect(zero).not.toHaveProperty('updatedAt');
  });
});

describe('token kuralları (BE1)', () => {
  const saved = inst({ key: 's:a', origin: 'saved', savedId: 'a', id: 'a', storedRef: 'env:A' });
  it.each([
    ['kayıtlı, dokunulmamış → "" ve bayrak YOK', saved, { tokenRef: '' }, false],
    ['kayıtlı, yazılan ref → tokenRef', { ...saved, tokenInput: ' env:B ' }, { tokenRef: 'env:B' }, false],
    ['kayıtlı, "Referansı kaldır" → "" + clearTokenRef', { ...saved, clearToken: true }, { tokenRef: '', clearTokenRef: true }, true],
    ['kaldır + yazılan → yazılan kazanır, bayrak yok', { ...saved, clearToken: true, tokenInput: 'env:C' }, { tokenRef: 'env:C' }, false],
    ['yeni satır boş ref → clearTokenRef', inst(), { tokenRef: '', clearTokenRef: true }, true],
    ['yeni satır yazılan ref → tokenRef, bayrak yok', inst({ tokenInput: 'file:/etc/x' }), { tokenRef: 'file:/etc/x' }, false],
  ] as const)('%s', (_n, row, want, hasFlag) => {
    const out = instanceInput(row);
    expect(out).toMatchObject(want);
    expect('clearTokenRef' in out).toBe(hasFlag);
  });
  it('effectiveRef + tokenState + sıra (çözülemedi < kayıtlı < yok)', () => {
    const tokens = { a: { tokenRef: 'env:A', resolved: false, error: 'tokenRef env:A: ortam değişkeni boş ya da tanımsız' } };
    expect(effectiveRef(saved)).toBe('env:A');
    expect(effectiveRef({ ...saved, clearToken: true })).toBe('');
    expect(tokenState(saved, tokens)).toBe('unresolved');
    expect(tokenState(saved, { a: { tokenRef: 'env:A', resolved: true } })).toBe('resolved');
    expect(tokenState({ ...saved, tokenInput: 'env:Z' }, tokens)).toBe('pending');
    expect(tokenState({ ...saved, clearToken: true }, tokens)).toBe('none');
    expect(tokenState(inst({ tokenInput: 'env:N' }), {})).toBe('pending');
    expect(tokenState(saved, {})).toBe('stored');
    expect(tokenRank('unresolved')).toBeLessThan(tokenRank('resolved'));
    expect(tokenRank('resolved')).toBeLessThan(tokenRank('none'));
  });
});

describe('normalizeApiUrl — Go NormalizeAPIURL aynası', () => {
  it.each([
    ['https://argocd.example.invalid', 'https://argocd.example.invalid'],
    [' HTTPS://ArgoCD.Example.Invalid/ ', 'https://argocd.example.invalid'],
    ['https://argocd.example.invalid:8443/', 'https://argocd.example.invalid:8443'],
    ['https://apps.example.invalid/ArgoCD/', 'https://apps.example.invalid/ArgoCD'],
    ['http://argocd-server.team-a.svc', 'http://argocd-server.team-a.svc'],
    ['https://argocd.example.invalid:443', 'https://argocd.example.invalid:443'],
  ])('%s → %s', (inp, want) => {
    expect(normalizeApiUrl(inp)).toEqual({ value: want, error: '' });
  });
  it.each([
    ['argocd.example.invalid', 'bir http(s) adresi değil'],
    ['ftp://argocd.example.invalid', 'http:// ya da https:// ile başlamalı (girilen: ftp://).'],
    ['https://', 'Host zorunlu.'],
    ['https://:8443', 'Host zorunlu.'],
    ['https://u:p@argocd.example.invalid', 'Kullanıcı bilgisi (user:pass@) içeremez; token tokenRef ile verilir.'],
    ['https://argocd.example.invalid/?refresh=hard', 'Sorgu (?) ya da parça (#) içeremez.'],
    ['https://argocd.example.invalid/#x', 'Sorgu (?) ya da parça (#) içeremez.'],
    ['https://argo cd.example.invalid', 'Adres boşluk içeremez.'],
    ['https://host.example.invalid:abc', 'port yalnız rakam'],
    ['https://host.example.invalid/%zz', 'geçersiz % kodlaması'],
  ])('%s reddedilir', (inp, msg) => {
    const r = normalizeApiUrl(inp);
    expect(r.value).toBe('');
    expect(r.error).toContain(msg);
  });
  // v0.10.974 — Go url.EscapedPath + Go 1.25 host:port kuralı. Beklenenler
  // Go NormalizeAPIURL'ün GERÇEK çıktısı (go test -overlay yoklaması): istemci
  // sunucunun kaydedeceği biçimi gösterir ve sunucudan SIKI olamaz.
  it.each([
    ['/ü', '/%C3%BC'],
    ['/a"b', '/a%22b'],
    ['/x\u00a0y', '/x%C2%A0y'],
    ['/a{b}', '/a%7Bb%7D'],
    ['/a|b', '/a%7Cb'],
    ['/a%41b', '/a%41b'],
    ['/a%41ü', '/aA%C3%BC'],
    ['/a!ü', '/a%21%C3%BC'],
    ['/a%2Fü', '/a/%C3%BC'],
    ['/a[b]', '/a[b]'],
    ['/a[b]ü', '/a%5Bb%5D%C3%BC'],
    ["/a'b", "/a'b"],
    ['/ü/', '/%C3%BC'],
    ['/a%2F/', '/a%2F'],
    ['/a%2fb', '/a%2fb'],
    ['/a\\b', '/a%5Cb'],
    ['/a^b', '/a%5Eb'],
    ['/a`b', '/a%60b'],
    ['/a<b>', '/a%3Cb%3E'],
    ['/%C3%BC', '/%C3%BC'],
    ['/#', ''],
  ])('yol %s → Go EscapedPath %s', (path, want) => {
    expect(normalizeApiUrl(`https://argocd.example.invalid${path}`)).toEqual({ value: `https://argocd.example.invalid${want}`, error: '' });
  });
  it.each([
    ['https://a:b:443', 'https://a:b:443'],
    ['https://a::443', 'https://a::443'],
    ['https://a:', 'https://a:'],
    ['https://[::1]:8443/x', 'https://[::1]:8443/x'],
    ['https://Argo.Example.Invalid:443/Api/', 'https://argo.example.invalid:443/Api'],
  ])('host:port (Go 1.25, son ":"tan bölünür) %s → %s', (inp, want) => {
    expect(normalizeApiUrl(inp)).toEqual({ value: want, error: '' });
  });
  it('yalnız " " ve "\\t" boşluk sayılır; son ":"tan sonra rakam dışı port reddedilir', () => {
    expect(normalizeApiUrl('https://argocd.example.invalid/a\tb').error).toBe('Adres boşluk içeremez.');
    expect(normalizeApiUrl('https://a:b').error).toBe('URL ayrıştırılamadı: port yalnız rakam olabilir.');
    expect(normalizeApiUrl('https://:443').error).toBe('Host zorunlu.');
  });
  it('CLI --server biçimi (host:port) mockup iletisi', () => {
    expect(normalizeApiUrl('argocd.team-b-int.example.invalid:443').error).toBe(
      "“argocd.team-b-int.example.invalid:443” bir http(s) adresi değil. Argo CD CLI'ın --server biçimi (host:port) burada geçmez; tarayıcıda açtığınız adresi girin, ör. https://argocd.team-b-int.example.invalid");
  });
  it('boş = ayarlanmamış (hata yok); kontrol karakteri reddedilir', () => {
    expect(normalizeApiUrl('  ')).toEqual({ value: '', error: '' });
    expect(hasControl('a\u0007b')).toBe(true);
    expect(hasControl('ab')).toBe(false);
  });
});

describe('validTokenRef — secretref.Valid aynası', () => {
  it.each([
    ['env:ARGOCD_TEAM_A_TOKEN', true], ['env:_X1', true], ['env:1X', false], ['env:', false],
    ['file:/etc/coremetry/secrets/argocd-team-a-int', true], ['file:relative', false], ['file:/a b', false],
    ['ghp_plaintexttoken', false],
  ])('%s → %s', (ref, ok) => { expect(validTokenRef(ref)).toBe(ok); });
});

describe('validateDraft — tüm kurallar, sunucu tarzı yollar', () => {
  const ctx = (d: Draft) => ({ clusters: CLUSTERS, bounds: BOUNDS, saved: draftFromSettings(settings()).instances, _d: d });
  const paths = (d: Draft) => validateDraft(d, ctx(d)).map(i => i.path);

  it('geçerli taslakta sorun yok', () => {
    expect(paths(draftFromSettings(settings()))).toEqual([]);
  });
  it('açıkken hub yok; silinmiş ve (açıkken) devre dışı hub', () => {
    const d = draftFromSettings(settings({ hubs: [], instances: [] }));
    expect(paths(d)).toEqual(['hubs']);
    const d2 = draftFromSettings(settings({ hubs: [{ clusterId: H1 }, { clusterId: H2 }, { clusterId: 'c-77d0aa15' }, { clusterId: 'c-9c0d1e20' }] }));
    const is = validateDraft(d2, ctx(d2));
    expect(is.map(i => i.path)).toEqual(['hubs[2].clusterId', 'hubs[3].clusterId']);
    expect(is[0].short).toBe('Remote Cluster kaydı yok');
    expect(is[0].target).toEqual({ kind: 'hub', key: d2.hubs[2].key });
    d2.enabled = false;
    expect(paths(d2)).toEqual(['hubs[2].clusterId']); // kapalıyken devre dışı hub serbest
  });
  it('kimlik: biçim, hub\'lar arası tekrar (mockup metni), namespace hub başına tekil', () => {
    const d = draftFromSettings(settings());
    d.instances.push(inst({ key: 'n:1', id: 'Bad_ID', hubNamespace: 'x1' }));
    d.instances.push(inst({ key: 'n:2', id: 'team-a-prod', hubClusterId: H2, hubNamespace: 'team-a-prod' }));
    d.instances.push(inst({ key: 'n:3', id: 'dup-ns', hubClusterId: H2, hubNamespace: 'team-b-int' }));
    const is = validateDraft(d, ctx(d));
    expect(is.map(i => i.path)).toEqual(['instances[2].id', 'instances[3].id', 'instances[4].hubNamespace']);
    expect(is[1].message).toBe("“team-a-prod” hub-1'deki instance'ta da var. Kimlik hub'lar arasında tekil olmalı (Faz 3'te ClickHouse instance_id). Bu satıra ayrı bir kimlik verin, ör. team-a-prod-hub-2. Aynı namespace iki hub'da serbesttir.");
    expect(is[2].message).toBe("“team-b-int” bu hub'da zaten “team-b-int” instance'ına ait (bir hub'da namespace başına tek Argo CD).");
    // aynı namespace İKİ hub'da serbest
    const ok = draftFromSettings(settings());
    ok.instances.push(inst({ key: 'n:4', id: 'team-a-prod-hub-2', hubClusterId: H2, hubNamespace: 'team-a-prod' }));
    expect(paths(ok)).toEqual([]);
  });
  it('hub listede değil, ad/iş uzunluğu, apiUrl, tokenRef', () => {
    const d = draftFromSettings(settings());
    d.instances.push(inst({ key: 'n:1', id: 'x-1', hubClusterId: 'c-1d2e3f40', hubNamespace: 'x1', name: 'a'.repeat(129), metricsJob: 'j\u0007x', apiUrl: 'argocd.x.example.invalid:443', tokenInput: 'plaintext' }));
    const is = validateDraft(d, ctx(d));
    expect(is.map(i => i.path)).toEqual(['instances[2].hubClusterId', 'instances[2].name', 'instances[2].metricsJob', 'instances[2].apiUrl', 'instances[2].tokenRef']);
    expect(is[3].short).toBe('http(s) adresi değil');
    expect(is[4].message).toBe('tokenRef `env:NAME` ya da `file:/path` biçiminde olmalı (düz token saklanmaz).');
  });
  it('envList: jeton biçimi ve en çok 50', () => {
    const d = draftFromSettings(settings({ envList: ['prod', 'pre-prod'] }));
    expect(paths(d)).toEqual(['envList[1]']);
    const many = draftFromSettings(settings({ envList: Array.from({ length: 51 }, (_, i) => `e${i}`) }));
    expect(paths(many)).toEqual(['envList']);
  });
  it('BE3 aynası: yeni kimlik, kayıtlı kimliği bu taslakta kaldırılmış yuvayı alırsa ret', () => {
    const d = draftFromSettings(settings());
    d.instances = d.instances.filter(i => i.id !== 'team-a-prod');
    d.instances.push(inst({ key: 'n:9', id: 'team-a-gitops', hubClusterId: H1, hubNamespace: 'team-a-prod' }));
    const is = validateDraft(d, ctx(d));
    expect(is.map(i => i.path)).toEqual(['instances[1].id']);
    expect(is[0].message).toBe("“team-a-gitops” kayıtlı “team-a-prod” instance'ının yerini alıyor (hub-1/team-a-prod): kayıtlı kimlik değiştirilemez (Faz 3'te ClickHouse instance_id). Önce “team-a-prod” satırını kaldırıp kaydedin, sonra “team-a-gitops” ile ekleyin.");
  });
  it('BE3 aynası: izinli durumlar', () => {
    // Aynı kimliği sil+ekle (kimlik kayıtlı) → serbest.
    const same = draftFromSettings(settings());
    same.instances = same.instances.filter(i => i.id !== 'team-a-prod');
    same.instances.push(inst({ key: 'n:1', id: 'team-a-prod', hubClusterId: H1, hubNamespace: 'team-a-prod' }));
    expect(paths(same)).toEqual([]);
    // Kayıtlı kimlik başka hub'a taşındı, boşalan yuvaya yeni kimlik → serbest.
    const moved = draftFromSettings(settings());
    moved.instances[0] = { ...moved.instances[0], hubClusterId: H2 };
    moved.instances.push(inst({ key: 'n:2', id: 'team-a-new', hubClusterId: H1, hubNamespace: 'team-a-prod' }));
    expect(paths(moved)).toEqual([]);
    // Silme + ilgisiz ekleme → serbest.
    const del = draftFromSettings(settings());
    del.instances = del.instances.filter(i => i.id !== 'team-a-prod');
    del.instances.push(inst({ key: 'n:3', id: 'team-z', hubClusterId: H1, hubNamespace: 'team-z' }));
    expect(paths(del)).toEqual([]);
  });
});

describe('gelişmiş alanlar', () => {
  const adv = (over: Partial<Record<string, string>>) => ({ ...draftFromSettings(settings({ apiWorker: {} })).adv, ...over }) as Draft['adv'];
  it.each([
    ['apiWorker.rps', '0,5', ''],
    ['apiWorker.rps', 'x', 'Sayı olmalı: 0,1–20 (boş = 1).'],
    ['apiWorker.burst', '2.5', 'Tam sayı olmalı: 1–50 (boş = 5).'],
    ['apiWorker.burst', '99', '1–50 olmalı (boş = 5); girilen 99.'],
    ['reader.maxSeries', '60000', '1.000–50.000 olmalı (boş = 50.000); girilen 60.000.'],
  ] as const)('%s = %s', (k, v, msg) => {
    expect(advError(adv({ [k]: v }), k, BOUNDS)).toBe(msg);
  });
  it('çapraz: out_of_band < pencere (boş ve dolu), namespace ≥ ad güveni', () => {
    expect(advError(adv({ 'classification.windowMin': '45' }), 'classification.outOfBandLookbackMin', BOUNDS))
      .toBe('Boş = 30 dk; eşleşme penceresi 45 dk olduğu için geçersiz — en az 45 girin ya da pencereyi küçültün.');
    expect(advError(adv({ 'classification.windowMin': '10', 'classification.outOfBandLookbackMin': '8' }), 'classification.outOfBandLookbackMin', BOUNDS))
      .toBe('Eşleşme penceresinden (10 dk) kısa olamaz — en az 10 girin ya da pencereyi küçültün.');
    expect(advError(adv({ 'mapping.nameConfidence': '20' }), 'mapping.namespaceConfidence', BOUNDS))
      .toBe('Boş = 30; ad eşleşmesi güveni 20 olduğu için geçersiz — en çok 19 girin ya da ad eşleşmesi güvenini artırın.');
    expect(advError(adv({ 'mapping.namespaceConfidence': '80' }), 'mapping.namespaceConfidence', BOUNDS))
      .toBe('Ad eşleşmesi güveninden (70) küçük olmalı — en çok 69 girin.');
    expect(advError(adv({ 'mapping.nameConfidence': '1' }), 'mapping.namespaceConfidence', BOUNDS))
      .toBe('Ad eşleşmesi güveni 1 iken geçerli değer yok — ad eşleşmesi güvenini artırın.');
  });
  it('kutu ↔ sayı ve özet satırı', () => {
    expect(parseAdvInput('')).toBeNull();
    expect(parseAdvInput('1,5')).toBe(1.5);
    expect(Number.isNaN(parseAdvInput('1e3'))).toBe(true);
    expect(numToInput(0)).toBe('');
    expect(numToInput(0.5)).toBe('0,5');
    const d = draftFromSettings(settings({ apiWorker: {} }));
    expect(advSummary(d, BOUNDS)).toBe('Varsayılanlar · API 1 istek/sn, burst 5 · okuyucu ≤ 50.000 seri, 64 MiB, 30 sn · metrik turu 60 sn');
    d.adv['intervals.metricsS'] = '120';
    expect(advSummary(d, BOUNDS)).toMatch(/^1 değer değişti · .*metrik turu 120 sn$/);
  });
  // v0.10.974 — '' ile 'estimate' aynı ETKİN kip (sunucu '' → estimate):
  // özet ve sayaç ham dizgeyi değil etkin kipi karşılaştırır (mockup def: estimate).
  it('metricsOnlyMode: estimate varsayılandır — özet ve sayaç değişiklik saymaz', () => {
    const d = draftFromSettings(settings({ apiWorker: {}, classification: { metricsOnlyMode: 'estimate' } }));
    expect(d.metricsOnlyMode).toBe('estimate');
    expect(advSummary(d, BOUNDS)).toMatch(/^Varsayılanlar · /);
    const blank = draftFromSettings(settings({ apiWorker: {} }));
    expect(blank.metricsOnlyMode).toBe('');
    expect(diffPhrases(d, blank, CLUSTERS)).toEqual([]);
    expect(diffPhrases(blank, d, CLUSTERS)).toEqual([]);
    const unk = { ...d, metricsOnlyMode: 'unknown' as const };
    expect(advSummary(unk, BOUNDS)).toMatch(/^1 değer değişti · /);
    expect(diffPhrases(unk, blank, CLUSTERS)).toEqual(['classification.metricsOnlyMode değişti']);
  });
});

describe('satır içi form', () => {
  it('validateBuffer mockup metinleri; tekrar eden kimlik öneri taşır', () => {
    const d = draftFromSettings(settings());
    const b = blankBuffer(H2);
    b.id = 'team-a-prod'; b.hubNamespace = 'team-b-int'; b.apiUrl = 'ftp://x'; b.tokenInput = 'abc';
    const e = validateBuffer(b, d.instances, d.hubs, CLUSTERS);
    expect(e.id).toBe("“team-a-prod” hub-1'deki bir instance'ta zaten var. Kimlik hub'lar arasında da tekil olmalı; ör. team-a-prod-hub-2.");
    expect(e.ns).toBe("“team-b-int” bu hub'da zaten “team-b-int” instance'ına ait (bir hub'da namespace başına tek Argo CD).");
    expect(e.url).toBe('http:// ya da https:// ile başlamalı (girilen: ftp://).');
    expect(e.ref).toBe('tokenRef `env:NAME` ya da `file:/path` biçiminde olmalı (düz token saklanmaz).');
    const empty = validateBuffer(blankBuffer(H1), d.instances, d.hubs, CLUSTERS);
    expect(empty.id).toBe('Kimlik zorunlu: küçük harf, rakam ve tire.');
    expect(empty.ns).toBe("Namespace zorunlu: Argo CD'nin çalıştığı namespace.");
    const off = blankBuffer('c-1d2e3f40');
    expect(validateBuffer(off, d.instances, d.hubs, CLUSTERS).hub).toBe('Hub “cluster-a” listede değil — listeden bir hub seçin.');
  });
  it('bufferDirty: boş yeni satır sayılmaz; kayıtlı satırda her alan', () => {
    const d = draftFromSettings(settings());
    expect(bufferDirty(blankBuffer(H1), d.instances)).toBe(false);
    const nb = blankBuffer(H1); nb.name = 'x';
    expect(bufferDirty(nb, d.instances)).toBe(true);
    const b = bufferFrom(d.instances[0]);
    expect(bufferDirty(b, d.instances)).toBe(false);
    expect(bufferDirty({ ...b, clearToken: true }, d.instances)).toBe(true);
    expect(bufferDirty(null, d.instances)).toBe(false);
  });
  it('applyBuffer: URL normalleşir, yazılan ref kaldırmayı ezer', () => {
    const d = draftFromSettings(settings());
    const b = { ...bufferFrom(d.instances[0]), apiUrl: ' HTTPS://Argo.Example.Invalid/ ', clearToken: true, tokenInput: ' env:NEW ' };
    const out = applyBuffer(b);
    expect(out).toMatchObject({ key: d.instances[0].key, origin: 'saved', apiUrl: 'https://argo.example.invalid', tokenInput: 'env:NEW', clearToken: false });
  });
});

describe('diffPhrases / dirtyText', () => {
  it('mockup cümleleri', () => {
    const base = draftFromSettings(settings());
    const d = draftFromSettings(settings());
    expect(diffPhrases(d, base, CLUSTERS)).toEqual([]);
    d.enabled = false;
    d.hubs[1] = { ...d.hubs[1], inject: false };
    d.hubs.push({ key: 'h:c-1d2e3f40', clusterId: 'c-1d2e3f40', inject: true });
    d.instances[0] = { ...d.instances[0], tokenInput: 'env:NEW' };
    d.instances = d.instances.filter(i => i.id !== 'team-b-int');
    d.instances.push(inst({ key: 'n:7', id: 'team-c' }));
    d.envList = ['prod', 'uat'];
    d.adv['reader.timeoutS'] = '40';
    d.metricsOnlyMode = 'unknown';
    expect(diffPhrases(d, base, CLUSTERS)).toEqual([
      'Argo CD kapatıldı', 'hub-2: küme etiketi eklenmeyecek', 'cluster-a hub olarak eklendi',
      'team-a-prod değişti', 'team-c eklendi', 'team-b-int kaldırıldı', 'ortam uat eklendi', 'ortam int çıkarıldı',
      'reader.timeoutS değişti', 'classification.metricsOnlyMode değişti',
    ]);
    const hubGone = draftFromSettings(settings());
    hubGone.hubs = hubGone.hubs.slice(0, 1);
    expect(diffPhrases(hubGone, base, CLUSTERS)).toEqual(['hub-2 hub listesinden çıkarıldı']);
  });
  it('sayaç satırı', () => {
    expect(dirtyText([], false)).toBe('Kayıtlı ayarlarla aynı.');
    expect(dirtyText(['a', 'b', 'c', 'd'], true)).toBe('4 kaydedilmemiş değişiklik: a, b, c … Açık formda tabloya uygulanmamış değişiklik var.');
  });
});

describe('parseArgoHttpError / alan yolu eşlemesi', () => {
  it('400 {error, field}: "<field>: " öneki atılır', () => {
    const e = new Error('HTTP 400: {"error":"instances[1].apiUrl: http:// ya da https:// ile başlamalı","field":"instances[1].apiUrl"}\n');
    expect(parseArgoHttpError(e)).toEqual({ status: 400, error: 'http:// ya da https:// ile başlamalı', field: 'instances[1].apiUrl' });
  });
  it('keşif hata gövdesi, 429 düz, JSON olmayan gövde ve HTTP olmayan hata', () => {
    expect(parseArgoHttpError(new Error('HTTP 502: {"error":"thanos rejected the cluster credentials (HTTP 403)","errorType":"unavailable"}')))
      .toEqual({ status: 502, error: 'thanos rejected the cluster credentials (HTTP 403)', errorType: 'unavailable' });
    expect(parseArgoHttpError(new Error('HTTP 429: {"error":"bir Argo CD keşfi zaten koşuyor"}'))).toEqual({ status: 429, error: 'bir Argo CD keşfi zaten koşuyor' });
    expect(parseArgoHttpError(new Error('HTTP 503: argocd settings not wired'))).toEqual({ status: 503, error: 'argocd settings not wired' });
    expect(parseArgoHttpError(new Error('Request timed out after 75s'))).toEqual({ status: 0, error: 'Request timed out after 75s' });
  });
  // v0.10.978 — 409 stale: `updatedAt` (kayıtlı damga) sayı olarak taşınır;
  // sayı olmayan / eksik updatedAt alan olarak gelmez (kutu "kayıt zamanı bilinmiyor" der).
  it('409 {errorType: stale, updatedAt} → updatedAt sayı; JSON dışı updatedAt atılır', () => {
    expect(parseArgoHttpError(new Error('HTTP 409: {"error":"ayarlar bu sayfa yüklendikten sonra başka biri tarafından değiştirildi — yeniden yükleyin","errorType":"stale","updatedAt":1790000000000001000}')))
      .toEqual({ status: 409, error: 'ayarlar bu sayfa yüklendikten sonra başka biri tarafından değiştirildi — yeniden yükleyin', errorType: 'stale', updatedAt: 1790000000000001000 });
    expect(parseArgoHttpError(new Error('HTTP 409: {"error":"x","errorType":"stale","updatedAt":"1"}'))).toEqual({ status: 409, error: 'x', errorType: 'stale' });
    expect(parseArgoHttpError(new Error('HTTP 409: {"error":"x","errorType":"stale"}'))).toEqual({ status: 409, error: 'x', errorType: 'stale' });
  });
  it('instances[N] / hubs[N] TASLAK dizisine eşlenir', () => {
    const d = draftFromSettings(settings());
    expect(targetForPath('instances[1].apiUrl', d)).toEqual({ kind: 'instance', key: d.instances[1].key, field: 'apiUrl' });
    expect(targetForPath('instances[9].id', d)).toEqual({ kind: 'other' });
    expect(targetForPath('hubs[0].clusterId', d)).toEqual({ kind: 'hub', key: d.hubs[0].key });
    expect(targetForPath('hubs', d)).toEqual({ kind: 'hubs' });
    expect(targetForPath('envList[0]', d)).toEqual({ kind: 'env' });
    expect(targetForPath('reader.timeoutS', d)).toEqual({ kind: 'adv', key: 'reader.timeoutS' });
    // v0.10.974 — pins ve kip seçiminin gidilecek yeri var; kalanlar düz metin.
    expect(targetForPath('pins[0].instanceId', d)).toEqual({ kind: 'pins' });
    expect(targetForPath('pins[3].clusterId', d)).toEqual({ kind: 'pins' });
    expect(targetForPath('pins', d)).toEqual({ kind: 'pins' });
    expect(targetForPath('pinsX', d)).toEqual({ kind: 'other' });
    expect(targetForPath('classification.metricsOnlyMode', d)).toEqual({ kind: 'advMode' });
    expect(targetForPath('instances', d)).toEqual({ kind: 'other' });
    const is = issueFromServer({ status: 400, error: 'm', field: 'instances[0].clearTokenRef' }, d);
    expect(bufferErrorsFromIssues([is], d.instances[0].key)).toEqual({ ref: 'm' });
  });
});

// v0.10.978 — 409 sonrası "Yeniden yükle": üç yönlü birleştirme. Alan/satır
// başına: kullanıcı değiştirdi (base≠draft) VE sunucu değiştirmedi (base=fresh)
// → kullanıcının değeri korunur; sunucu da değiştirdiyse sunucunun değeri gelir
// ve düzenleme "atıldı" sayılır. Satırlar hub'da clusterId, instance'ta id ile
// eşlenir; sıra sunucununki + kullanıcının yeni satırları sonda.
describe('mergeDraft / reloadText — 409 sonrası üç yönlü birleştirme', () => {
  const base = () => draftFromSettings(settings());
  it('düzenleme yokken sunucunun taslağı aynen; sayaçlar sıfır', () => {
    const fresh = draftFromSettings(settings({ envList: ['prod', 'uat'] }));
    const m = mergeDraft(base(), base(), fresh);
    expect(m.draft).toEqual(fresh);
    expect([m.kept, m.dropped]).toEqual([0, 0]);
    expect(reloadText(m)).toBe('Yeniden yüklendi — kayıtlı ayar güncel; kaydedilmemiş düzenleme yoktu.');
  });
  it('skalarlar: sunucuda değişmeyen alandaki düzenleme korunur, ikisi de değişince sunucunun', () => {
    const d = base();
    d.enabled = false;                       // kullanıcı; sunucu dokunmadı → korunur
    d.adv['reader.timeoutS'] = '40';         // kullanıcı 40; sunucu 35 → sunucunun
    d.envList = ['prod', 'int', 'dev'];      // kullanıcı ekledi; sunucu dokunmadı → korunur
    d.metricsOnlyMode = 'unknown';           // kullanıcı; sunucu da 'unknown' yaptı → sunucunun (aynı değer, yine sayılır)
    const fresh = draftFromSettings(settings({ reader: { timeoutS: 35 }, classification: { metricsOnlyMode: 'unknown' }, apiWorker: { rps: 2 } }));
    const m = mergeDraft(base(), d, fresh);
    expect(m.draft.enabled).toBe(false);
    expect(m.draft.adv['reader.timeoutS']).toBe('35');
    expect(m.draft.adv['apiWorker.rps']).toBe('2');
    expect(m.draft.envList).toEqual(['prod', 'int', 'dev']);
    expect(m.draft.metricsOnlyMode).toBe('unknown');
    expect([m.kept, m.dropped]).toEqual([2, 2]);
    expect(reloadText(m)).toBe('Yeniden yüklendi — 2 düzenlemeniz korundu; 2 düzenlemeniz sunucuda da değişen alana ya da satıra dokunduğu için atıldı. Korunanları kaydetmek için Kaydet.');
  });
  it('hub satırları: kullanıcının eklediği/kaldırdığı/değiştirdiği hub; sunucunun eklediği hub; çakışan bayrak', () => {
    const d = base();
    d.hubs = [
      { ...d.hubs[0], inject: false },       // kullanıcı bayrağı kapattı; sunucu dokunmadı → korunur
      // H2 kullanıcı tarafından kaldırıldı; sunucu H2'nin bayrağını değiştirdi → kaldırma atılır, sunucunun H2'si gelir
      { key: 'h:c-1d2e3f40', clusterId: 'c-1d2e3f40', inject: true }, // kullanıcı ekledi → sonda korunur
    ];
    const fresh = draftFromSettings(settings({ hubs: [{ clusterId: H1, injectClusterLabel: true }, { clusterId: H2, injectClusterLabel: false }, { clusterId: 'c-9c0d1e20' }] }));
    const m = mergeDraft(base(), d, fresh);
    expect(m.draft.hubs.map(h => [h.clusterId, h.inject])).toEqual([[H1, false], [H2, false], ['c-9c0d1e20', true], ['c-1d2e3f40', true]]);
    expect([m.kept, m.dropped]).toEqual([2, 1]);
  });
  it('instance satırları: id ile eşleme; sunucunun sildiği satırdaki düzenleme atılır; aynı id\'li yeni satırda sunucu kazanır', () => {
    const d = base();
    d.instances = [
      { ...d.instances[0], name: 'Team A prod (ops)' },  // kullanıcı düzenledi; sunucu team-a-prod'u sildi → atılır
      // team-b-int kullanıcı tarafından kaldırıldı; sunucu dokunmadı → kaldırma korunur
      inst({ key: 'n:7', id: 'team-c-prod' }),           // kullanıcı ekledi; sunucu da team-c-prod ekledi → sunucunun
      inst({ key: 'n:8', id: 'team-d-prod', hubClusterId: H2, hubNamespace: 'team-d-prod' }), // kullanıcı ekledi → korunur
    ];
    const fresh = draftFromSettings(settings({ instances: [
      { id: 'team-b-int', hubClusterId: H2, hubNamespace: 'team-b-int', enabled: true },
      { id: 'team-c-prod', hubClusterId: H1, name: 'Sunucunun C', hubNamespace: 'team-c-prod', enabled: false },
    ] }));
    const m = mergeDraft(base(), d, fresh);
    expect(m.draft.instances.map(i => [i.id, i.key, i.origin, i.name, i.enabled])).toEqual([
      ['team-c-prod', 's:team-c-prod', 'saved', 'Sunucunun C', false],
      ['team-d-prod', 'n:8', 'new', '', true],
    ]);
    expect([m.kept, m.dropped]).toEqual([2, 2]);
  });
  it('kullanıcının düzenlediği kayıtlı satır sunucuda değişmediyse düzenleme (token girdisi dahil) kalır; sunucu değiştirdiyse sunucunun satırı', () => {
    const d = base();
    d.instances = d.instances.map(i => (i.id === 'team-a-prod' ? { ...i, tokenInput: 'env:NEW', enabled: false } : { ...i, name: 'B' }));
    const fresh = draftFromSettings(settings({ instances: [
      { id: 'team-a-prod', hubClusterId: H1, name: 'Team A prod', hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics', apiUrl: 'https://argocd.team-a-prod.example.invalid', tokenRef: 'env:ARGOCD_TEAM_A_TOKEN', enabled: true, discovered: true },
      { id: 'team-b-int', hubClusterId: H2, hubNamespace: 'team-b-int', metricsJob: 'b-metrics', enabled: true },
    ] }));
    const m = mergeDraft(base(), d, fresh);
    expect(m.draft.instances.map(i => [i.id, i.tokenInput, i.enabled, i.name, i.metricsJob])).toEqual([
      ['team-a-prod', 'env:NEW', false, 'Team A prod', 'team-a-prod-metrics'],
      ['team-b-int', '', true, '', 'b-metrics'],
    ]);
    expect([m.kept, m.dropped]).toEqual([1, 1]);
    expect(reloadText(m)).toBe('Yeniden yüklendi — 1 düzenlemeniz korundu; 1 düzenlemeniz sunucuda da değişen alana ya da satıra dokunduğu için atıldı. Korunanları kaydetmek için Kaydet.');
    expect(reloadText({ draft: m.draft, kept: 0, dropped: 1 })).toBe('Yeniden yüklendi — 1 düzenlemeniz sunucuda da değişen alana ya da satıra dokunduğu için atıldı.');
    expect(reloadText({ draft: m.draft, kept: 3, dropped: 0 })).toBe('Yeniden yüklendi — 3 düzenlemeniz korundu. Korunanları kaydetmek için Kaydet.');
  });
  // v0.10.978 — satır bütünlüğü: kullanıcı yalnız enabled'ı, sunucu yalnız name'i
  // değiştirdi → satır BÜTÜNÜYLE sunucunun (token üçlüsü tek mantıksal alan; alan
  // başına birleştirme sunucunun token değişikliğini sessizce ezerdi) ve metin
  // "alana ya da satıra" der. Eski metin ("alana dokunduğu için") yanlıştı:
  // kullanıcının dokunduğu alan sunucuda değişmemişti.
  it('instance satırı BÜTÜN olarak birleşir: kullanıcı yalnız enabled\'ı, sunucu yalnız name\'i değiştirdi → satır sunucunun, metin "alana ya da satıra" der', () => {
    const d = base();
    d.instances = d.instances.map(i => (i.id === 'team-a-prod' ? { ...i, enabled: false } : i));
    const fresh = draftFromSettings(settings({ instances: [
      { id: 'team-a-prod', hubClusterId: H1, name: 'Team A prod (ops)', hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics', apiUrl: 'https://argocd.team-a-prod.example.invalid', tokenRef: 'env:ARGOCD_TEAM_A_TOKEN', enabled: true, discovered: true },
      { id: 'team-b-int', hubClusterId: H2, hubNamespace: 'team-b-int', enabled: true },
    ] }));
    const m = mergeDraft(base(), d, fresh);
    const row = m.draft.instances.find(i => i.id === 'team-a-prod')!;
    expect([row.name, row.enabled]).toEqual(['Team A prod (ops)', true]);
    expect([m.kept, m.dropped]).toEqual([0, 1]);
    expect(reloadText(m)).toBe('Yeniden yüklendi — 1 düzenlemeniz sunucuda da değişen alana ya da satıra dokunduğu için atıldı.');
  });
});

describe('trLocative / fmtTr / savedAtText', () => {
  it.each([
    ['hub-1', "'de"], ['hub-2', "'de"], ['hub-3', "'te"], ['hub-6', "'da"], ['hub-9', "'da"], ['hub-0', "'da"],
    ['hub-10', "'da"], ['hub-40', "'ta"], ['hub-70', "'te"], ['hub-100', "'de"],
    ['cluster-a', "'da"], ['team-a-prod', "'da"], ['platform-gitops', "'ta"], ['hub', "'da"], ['cluster-e', "'de"], ['edge', "'de"],
    ['kit', "'te"],
  ])('%s → %s', (w, suf) => { expect(trLocative(w)).toBe(suf); });
  // v0.10.974 — States (c) "4 instance · 2'si hatalı": sayının okunuşuna göre iyelik.
  it.each([
    [1, "'i"], [2, "'si"], [3, "'ü"], [4, "'ü"], [5, "'i"], [6, "'sı"], [7, "'si"], [8, "'i"], [9, "'u"],
    [10, "'u"], [12, "'si"], [20, "'si"], [30, "'u"], [40, "'ı"], [50, "'si"], [60, "'ı"], [70, "'i"], [80, "'i"], [90, "'ı"],
    [100, "'ü"], [300, "'ü"], [1000, "'i"], [2000, "'i"], [1100, "'ü"],
  ])('trPossessive3(%i) → %s', (n, suf) => { expect(trPossessive3(n)).toBe(suf); });
  it('binlik nokta, ondalık virgül', () => {
    expect(fmtTr(1184)).toBe('1.184');
    expect(fmtTr(50000)).toBe('50.000');
    expect(fmtTr(0.1)).toBe('0,1');
    expect(fmtTr(12)).toBe('12');
  });
  it('son kayıt metni (ns)', () => {
    expect(savedAtText(undefined)).toBe('');
    expect(savedAtText(Date.UTC(2026, 8, 26, 20, 41) * 1e6)).toMatch(/^son kayıt 26 Eyl \d\d:41$/);
    expect(fmtHourMinute(new Date(2026, 8, 26, 7, 5))).toBe('07:05');
  });
});
