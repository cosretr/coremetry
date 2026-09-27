// clusterArgoFields.test.ts — v0.10.974 — Remote Cluster › "Argo CD eşlemesi"
// (onaylı mockup ClusterFields.dc.html, 2026-09-27).
//
// Sözleşme: istemci önizlemesi ve Kaydet öncesi denetim SUNUCUNUN kuralını
// taklit eder, onu aşmaz:
//   - normalizeAPIServerURL = internal/thanos NormalizeAPIServerURL'ün elle
//     ayrıştıran kopyası (new URL() DEĞİL — :443'ü atar, yolu yeniden
//     kodlar). Aşağıdaki tablolar apiserver_url_test.go'nun AYNI vakaları;
//   - isInClusterAPIServerURL = IsInClusterAPIServerURL (port / FQDN farkı yok);
//   - istemci sunucudan SIKI olamaz: sunucunun kabul ettiği bir adresi
//     reddetmek operatörü kaydedemez hâle getirirdi (Go 1.25 gevşek iki nokta
//     kuralı, :0443, boş port dahil);
//   - argoSuffix tekilliği büyük/küçük harf duyarsız; hata DÜZENLENEN satırda;
//   - pairGroup ipucu "Bu grupta: …" diğer kayıtları sayar;
//   - Argo hub sayımı settings.hubs[].clusterId + instances[].hubClusterId.
import { describe, it, expect } from 'vitest';
import {
  normalizeAPIServerURL, isInClusterAPIServerURL, splitUrlList, previewAPIServerUrls,
  checkArgoFields, clientCheckSummary, argoHubInstances, trDative, suggestSuffix,
  SUFFIX_HINT, PAIR_HINT, IN_CLUSTER_API_SERVER_URL, type ArgoFieldRow,
} from './clusterArgoFields';
import type { ArgoCDSettingsResponse } from '@/lib/types';

describe('normalizeAPIServerURL — thanos.NormalizeAPIServerURL kopyası (apiserver_url_test.go)', () => {
  const ok: [string, string][] = [
    ['https://api.cluster-a.example.invalid:6443', 'https://api.cluster-a.example.invalid:6443'],
    ['  HTTPS://API.Cluster-A.Example.Invalid:6443/  ', 'https://api.cluster-a.example.invalid:6443'],
    ['https://api.cluster-a.example.invalid', 'https://api.cluster-a.example.invalid:6443'],
    ['https://api.cluster-a.example.invalid/', 'https://api.cluster-a.example.invalid:6443'],
    ['http://api.cluster-b.example.invalid:8443', 'http://api.cluster-b.example.invalid:8443'],
    ['https://api.cluster-b.example.invalid:443/', 'https://api.cluster-b.example.invalid:443'],
    ['https://kubernetes.default.svc', 'https://kubernetes.default.svc:6443'],
    ['https://10.0.0.1', 'https://10.0.0.1:6443'],
    ['https://[FD00::1]', 'https://[fd00::1]:6443'],
    ['https://[fd00::1]:443', 'https://[fd00::1]:443'],
  ];
  for (const [input, want] of ok) {
    it(`kabul: ${JSON.stringify(input)} → ${want} (idempotent)`, () => {
      const got = normalizeAPIServerURL(input);
      expect(got).toMatchObject({ ok: true, value: want });
      const again = got.ok ? normalizeAPIServerURL(got.value) : got;
      expect(again).toMatchObject({ ok: true, value: want });
    });
  }

  const bad: [string, string][] = [
    ['', 'boş'],
    ['   ', 'yalnız boşluk'],
    ['api.cluster-a.example.invalid:6443', 'şema yok'],
    ['ftp://api.cluster-a.example.invalid', 'şema http/https değil'],
    ['https://', 'host yok'],
    ['https:api.cluster-a.example.invalid', 'opak URL'],
    ['https://user:secret@api.cluster-a.example.invalid:6443', 'userinfo'],
    ['https://user@api.cluster-a.example.invalid', 'userinfo (parolasız)'],
    ['https://api.cluster-a.example.invalid:6443/k8s', 'yol'],
    ['https://api.cluster-a.example.invalid:6443//', 'çift eğik çizgi yolu'],
    ['https://api.cluster-a.example.invalid:6443?x=1', 'sorgu'],
    ['https://api.cluster-a.example.invalid:6443#frag', 'fragment'],
    ['https://api.cluster-a.example.invalid:0', 'port 0'],
    ['https://api.cluster-a.example.invalid:70000', 'port aralık dışı'],
    ['https://api.cluster-a.example.invalid:abc', 'port sayı değil'],
  ];
  for (const [input, why] of bad) {
    it(`ret (${why}): ${JSON.stringify(input)}`, () => {
      const got = normalizeAPIServerURL(input);
      expect(got.ok).toBe(false);
      // Hata metni ekrana ve role=alert özetine gider: userinfo parolası yankılanmaz.
      if (!got.ok) expect(got.error).not.toContain('secret');
    });
  }

  it('mockup hata metinleri (ClusterFields.dc.html normApi)', () => {
    const err = (s: string) => { const r = normalizeAPIServerURL(s); return r.ok ? '' : r.error; };
    expect(err('ftp://a.example.invalid')).toBe('şema http ya da https olmalı');
    expect(err('https://user@a.example.invalid')).toBe('kullanıcı bilgisi (user@) taşıyamaz');
    expect(err('https://a.example.invalid?x=1')).toBe('sorgu ya da fragment taşıyamaz');
    expect(err('https://a.example.invalid#f')).toBe('sorgu ya da fragment taşıyamaz');
    expect(err('https://a.example.invalid/k8s')).toBe('yol taşıyamaz (yalnız şema + host + port)');
    expect(err('https://')).toBe('host yok (biçim: https://<host>[:port])');
    expect(err('https://a b.example.invalid')).toBe('ayrıştırılamadı (biçim: https://<host>[:port])');
    expect(err('https://a.example.invalid:0')).toBe('port 1-65535 aralığında olmalı');
  });

  it('sunucudan sıkı değil: boş port, baştaki sıfır, sorgusuz ? değil ama "?" tek başına sorgu', () => {
    // Go url.Parse "host:" → Port() "" → varsayılan :6443.
    expect(normalizeAPIServerURL('https://api.cluster-a.example.invalid:')).toMatchObject({ ok: true, value: 'https://api.cluster-a.example.invalid:6443' });
    expect(normalizeAPIServerURL('https://api.cluster-a.example.invalid:06443')).toMatchObject({ ok: true, value: 'https://api.cluster-a.example.invalid:6443' });
    // ForceQuery ("…?") da sorgu sayılır (Go: u.ForceQuery).
    expect(normalizeAPIServerURL('https://api.cluster-a.example.invalid?').ok).toBe(false);
    // Go 1.25 gevşek iki nokta kuralı (go.mod go 1.25 → urlstrictcolons=0).
    expect(normalizeAPIServerURL('https://a:1:2')).toMatchObject({ ok: true, value: 'https://[a:1]:2' });
    // IPv4 köşeli parantezde geçersiz IP-literal; köşeli parantez ortada geçersiz.
    expect(normalizeAPIServerURL('https://[10.0.0.1]').ok).toBe(false);
    expect(normalizeAPIServerURL('https://a[b]').ok).toBe(false);
  });

  it('önizleme notları: küçük harf · sondaki / atıldı · :6443 eklendi | değişmedi', () => {
    const notes = (s: string) => { const r = normalizeAPIServerURL(s); return r.ok ? r.notes : ['ERR']; };
    expect(notes('HTTPS://API.Cluster-A.example.invalid/')).toEqual(['küçük harf', 'sondaki / atıldı', ':6443 eklendi']);
    expect(notes('https://api.cluster-a-internal.example.invalid:443')).toEqual([]);
    expect(notes('https://api.hub-1.example.invalid:6443')).toEqual([]);
    expect(notes('https://api.cluster-a.example.invalid')).toEqual([':6443 eklendi']);
    expect(notes('https://api.cluster-a.example.invalid:06443')).toEqual(['port sadeleşti']);
  });
});

describe('isInClusterAPIServerURL — thanos.IsInClusterAPIServerURL kopyası', () => {
  for (const s of [
    'https://kubernetes.default.svc',
    'https://kubernetes.default.svc/',
    'HTTPS://Kubernetes.Default.Svc',
    'https://kubernetes.default.svc:6443',
    'https://kubernetes.default.svc:443',
    'https://kubernetes.default.svc.cluster.local',
    'https://kubernetes.default.svc.cluster.local:443/',
  ]) it(`küme-içi: ${s}`, () => expect(isInClusterAPIServerURL(s)).toBe(true));
  for (const s of [
    'https://api.cluster-a.example.invalid:6443',
    'http://kubernetes.default.svc',
    'https://kubernetes.default.svc.example.invalid',
    'https://kubernetes.default',
    'not a url',
    '',
  ]) it(`küme-içi değil: ${JSON.stringify(s)}`, () => expect(isInClusterAPIServerURL(s)).toBe(false));
});

describe('previewAPIServerUrls — "Kaydedilecek biçim" satırları', () => {
  it('mockup örneği: normalize + not; küme-içi satır hata', () => {
    expect(splitUrlList(' a ,\n\n b, ')).toEqual(['a', 'b']);
    const lines = previewAPIServerUrls('HTTPS://API.Cluster-A.example.invalid/\nhttps://api.cluster-a-internal.example.invalid:443');
    expect(lines).toEqual([
      { text: 'https://api.cluster-a.example.invalid:6443', note: 'küçük harf · sondaki / atıldı · :6443 eklendi', bad: false },
      { text: 'https://api.cluster-a-internal.example.invalid:443', note: 'değişmedi', bad: false },
    ]);
    const hub = previewAPIServerUrls('https://api.hub-1.example.invalid:6443\nhttps://kubernetes.default.svc');
    expect(hub[1]).toEqual({ text: 'https://kubernetes.default.svc', note: 'yazılamaz: küme-içi hedef', bad: true, inCluster: true });
  });

  it('userinfo satırı önizlemede parolasız gösterilir; aynı kayıtta tekrar atılır', () => {
    const [u] = previewAPIServerUrls('https://user:secret@api.cluster-a.example.invalid');
    expect(u.bad).toBe(true);
    expect(u.text).not.toContain('secret');
    expect(u.note).toBe('kullanıcı bilgisi (user@) taşıyamaz');
    const dup = previewAPIServerUrls('https://api.cluster-a.example.invalid, HTTPS://api.cluster-a.example.invalid:6443/');
    expect(dup[1]).toEqual({ text: 'https://api.cluster-a.example.invalid:6443', note: 'tekrar · bir kez kaydedilir', bad: false });
  });
});

const row = (p: Partial<ArgoFieldRow> & { name: string }): ArgoFieldRow => ({
  apiServerUrls: '', savedApiServerUrls: '', argoSuffix: '', savedArgoSuffix: '', pairGroup: '', ...p,
});

describe('checkArgoFields — argoSuffix tekilliği (büyük/küçük harf duyarsız, düzenlenen satırda)', () => {
  it('mockup: cluster-a "CB" yazdı, cluster-b\'de "cb" kayıtlı → hata cluster-a\'da, cluster-b temiz', () => {
    const rows = [
      row({ name: 'hub-1', argoSuffix: 'h1', savedArgoSuffix: 'h1' }),
      row({ name: 'cluster-a', argoSuffix: 'CB', savedArgoSuffix: 'ca' }),
      row({ name: 'cluster-b', argoSuffix: 'cb', savedArgoSuffix: 'cb' }),
    ];
    const c = checkArgoFields(rows);
    expect(c[1].suffixError).toBe('“CB” eki cluster-b kaydında da var (büyük/küçük harf duyarsız). Ek, uygulama adının son jetonundan kümeyi seçer; her kümede tekil olmalı. cluster-a uygulamalarındaki son jetonu girin, ör. ca.');
    expect(c[1].issues).toEqual([{ field: 'suffix', text: '“CB” cluster-b kaydında da var (büyük/küçük harf duyarsız). cluster-a\'ya kendi ekini verin, ör. ca.' }]);
    expect(c[2].suffixError).toBeUndefined();
    expect(c[0].suffixError).toBeUndefined();
    expect(c[0].issues).toEqual([]);
  });

  it('hiçbiri düzenlenmediyse (içe aktarılmış blob) liste sırasında SONRAKİ kayıt hatalı — sunucu gibi', () => {
    const c = checkArgoFields([
      row({ name: 'cluster-a', argoSuffix: 'ca', savedArgoSuffix: 'ca' }),
      row({ name: 'cluster-b', argoSuffix: 'CA', savedArgoSuffix: 'CA' }),
    ]);
    expect(c[0].suffixError).toBeUndefined();
    expect(c[1].suffixError).toMatch(/^“CA” eki cluster-a kaydında da var/);
  });

  it('örnek ek çakışıyorsa ya da geçersizse "ör." cümlesi düşer; biçim hatası sunucu kuralıyla', () => {
    const c = checkArgoFields([
      row({ name: 'cluster-a', argoSuffix: 'ca', savedArgoSuffix: 'ca' }),
      row({ name: 'c-a', argoSuffix: 'CA' }), // yeni satır: önerilen "ca" dolu
    ]);
    expect(c[1].suffixError).toBe('“CA” eki cluster-a kaydında da var (büyük/küçük harf duyarsız). Ek, uygulama adının son jetonundan kümeyi seçer; her kümede tekil olmalı. c-a uygulamalarındaki son jetonu girin.');
    expect(c[1].issues[0].text).toBe('“CA” cluster-a kaydında da var (büyük/küçük harf duyarsız). c-a\'ya kendi ekini verin.');
    const f = checkArgoFields([row({ name: 'cluster-a', argoSuffix: '-ca' })]);
    expect(f[0].suffixError).toBe('“-ca” geçersiz: harf/rakamla başlayıp biten, en çok 63 karakter (arada . _ - olabilir).');
    expect(f[0].issues).toEqual([{ field: 'suffix', text: '“-ca” geçersiz: harf/rakamla başlayıp biten, en çok 63 karakter (arada . _ - olabilir).' }]);
    expect(checkArgoFields([row({ name: 'cluster-a', argoSuffix: 'a'.repeat(64) })])[0].suffixError).toMatch(/geçersiz/);
    expect(checkArgoFields([row({ name: 'cluster-a', argoSuffix: '  ca.1_x-y  ' })])[0].suffixError).toBeUndefined();
  });

  it('aynı adlı iki kayıt sunucuda çakışmaz (o != c.Name) — istemci de işaretlemez', () => {
    const c = checkArgoFields([row({ name: 'cluster-a', argoSuffix: 'ca' }), row({ name: 'cluster-a', argoSuffix: 'CA' })]);
    expect(c.map(x => x.suffixError)).toEqual([undefined, undefined]);
  });
});

describe('checkArgoFields — API server URL alanı', () => {
  it('küme-içi satır: alan geçersiz; özet maddesi mockup metni (dış adres listede / değil)', () => {
    const c = checkArgoFields([
      row({ name: 'hub-1', apiServerUrls: 'https://api.hub-1.example.invalid:6443\nhttps://kubernetes.default.svc' }),
      row({ name: 'hub-2', apiServerUrls: 'https://kubernetes.default.svc.cluster.local:443' }),
    ]);
    expect(c[0].apiInvalid).toBe(true);
    expect(c[0].issues).toEqual([{ field: 'api', text: 'https://kubernetes.default.svc yazılamaz — Argo\'nun küme-içi hedefi instance\'ın hub\'ına çözülür. Satırı silin; hub\'ın dış API adresi zaten listede.' }]);
    expect(c[1].issues).toEqual([{ field: 'api', text: 'https://kubernetes.default.svc yazılamaz — Argo\'nun küme-içi hedefi instance\'ın hub\'ına çözülür. Satırı silin; hub için dış API adresini girin.' }]);
  });

  it('diğer hatalar sıra numarasıyla, ham değer yankılanmadan', () => {
    const c = checkArgoFields([row({ name: 'cluster-a', apiServerUrls: 'https://api.cluster-a.example.invalid, https://user:secret@x.example.invalid/k8s' })]);
    expect(c[0].apiInvalid).toBe(true);
    expect(c[0].issues).toEqual([{ field: 'api', text: '2. adres: kullanıcı bilgisi (user@) taşıyamaz' }]);
    expect(JSON.stringify(c[0])).not.toContain('secret');
  });

  it('kayıtlar arası tekillik: düzenlenen satır hatalı, önizleme notu diğer kaydı söyler', () => {
    const c = checkArgoFields([
      row({ name: 'cluster-a', apiServerUrls: 'https://api.cluster-a.example.invalid:6443', savedApiServerUrls: 'https://api.cluster-a.example.invalid:6443' }),
      row({ name: 'cluster-b', apiServerUrls: 'HTTPS://api.cluster-a.example.invalid/' }),
    ]);
    expect(c[0].apiInvalid).toBe(false);
    expect(c[1].apiInvalid).toBe(true);
    expect(c[1].preview[0]).toEqual({ text: 'https://api.cluster-a.example.invalid:6443', note: 'cluster-a kaydında da var', bad: true });
    expect(c[1].issues).toEqual([{ field: 'api', text: 'https://api.cluster-a.example.invalid:6443 cluster-a kaydında da var; bir API server adresi aynı anda tek kayda bağlanabilir.' }]);
  });

  it('en çok 16 tekil adres (sunucu apiServerURLsMax)', () => {
    const urls = Array.from({ length: 17 }, (_, i) => `https://api-${i}.cluster-a.example.invalid`).join('\n');
    const c = checkArgoFields([row({ name: 'cluster-a', apiServerUrls: urls })]);
    expect(c[0].apiInvalid).toBe(true);
    expect(c[0].issues).toEqual([{ field: 'api', text: 'en çok 16 adres taşıyabilir (17 verildi)' }]);
    const ok16 = Array.from({ length: 16 }, (_, i) => `https://api-${i}.cluster-a.example.invalid`).join('\n');
    expect(checkArgoFields([row({ name: 'cluster-a', apiServerUrls: `${ok16}\nhttps://api-0.cluster-a.example.invalid` })])[0].apiInvalid).toBe(false);
  });

  it('sunucunun kabul ettiği mevcut test girdisi engellenmez', () => {
    const c = checkArgoFields([row({ name: 'cluster-b', apiServerUrls: ' https://api.cluster-b.example.invalid:6443 ,\n\nHTTPS://api-2.cluster-b.example.invalid/, ' })]);
    expect(c[0].apiInvalid).toBe(false);
    expect(c[0].issues).toEqual([]);
  });
});

describe('checkArgoFields — pairGroup "Bu grupta" ipucu; özet cümlesi', () => {
  it('aynı değeri taşıyan diğer adlı kayıtlar listelenir', () => {
    const c = checkArgoFields([
      row({ name: 'cluster-a', pairGroup: 'prod-pair-1' }),
      row({ name: 'cluster-b', pairGroup: ' prod-pair-1 ' }),
      row({ name: 'cluster-c', pairGroup: 'prod-pair-1' }),
      row({ name: 'hub-1' }),
    ]);
    expect(c[0].pairHint).toBe(`${PAIR_HINT} Bu grupta: cluster-b, cluster-c.`);
    expect(c[1].pairHint).toBe(`${PAIR_HINT} Bu grupta: cluster-a, cluster-c.`);
    expect(c[3].pairHint).toBe(PAIR_HINT);
    expect(SUFFIX_HINT).toBe('Argo uygulama adının son jetonu (…-env-ek). Tekil, büyük/küçük harf duyarsız.');
  });
  it('özet', () => {
    expect(clientCheckSummary(2)).toBe('Kaydedilmedi — istemci denetimi 2 sorun buldu; istek gönderilmedi, hiçbir kayıt değişmedi.');
    expect(IN_CLUSTER_API_SERVER_URL).toBe('https://kubernetes.default.svc');
  });
});

describe('argoHubInstances — "Argo hub" rozeti + instance sayısı', () => {
  it('hubs[].clusterId → instances[].hubClusterId sayısı; alanlar yoksa boş', () => {
    const res = {
      settings: {
        enabled: true,
        hubs: [{ clusterId: 'c-aaaa0001' }, { clusterId: 'c-bbbb0002', injectClusterLabel: false }],
        instances: [
          { id: 'team-a-prod', hubClusterId: 'c-aaaa0001', hubNamespace: 'team-a-prod', enabled: true },
          { id: 'team-a-int', hubClusterId: 'c-aaaa0001', hubNamespace: 'team-a-int', enabled: false },
          { id: 'orphan', hubClusterId: 'c-zzzz9999', hubNamespace: 'orphan', enabled: true },
        ],
        apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {},
      },
    } satisfies Pick<ArgoCDSettingsResponse, 'settings'>;
    const m = argoHubInstances(res);
    expect([...m.entries()]).toEqual([['c-aaaa0001', 2], ['c-bbbb0002', 0]]);
    expect(argoHubInstances(null).size).toBe(0);
    expect(argoHubInstances({ settings: { enabled: false, apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {} } }).size).toBe(0);
  });
});

describe('trDative / suggestSuffix — Türkçe yönelme eki ve örnek ek', () => {
  it('ünlü uyumu, kaynaştırma, harf ve sayı okunuşu', () => {
    expect(trDative('cluster-a')).toBe("cluster-a'ya");
    expect(trDative('cluster-b')).toBe("cluster-b'ye");
    expect(trDative('hub-1')).toBe("hub-1'e");
    expect(trDative('hub-2')).toBe("hub-2'ye");
    expect(trDative('hub-6')).toBe("hub-6'ya");
    expect(trDative('hub-9')).toBe("hub-9'a");
    expect(trDative('hub-10')).toBe("hub-10'a");
    expect(trDative('hub-20')).toBe("hub-20'ye");
    expect(trDative('hub-100')).toBe("hub-100'e");
    expect(trDative('hub-3000')).toBe("hub-3000'e");
    expect(trDative('team-a-prod')).toBe("team-a-prod'a");
    expect(trDative('prod-ist')).toBe("prod-ist'e");
    expect(trDative('c-aaaa0001')).toBe("c-aaaa0001'e");
    expect(trDative('')).toBe('');
  });
  it('ad parçalarının baş harfleri; dolu ya da geçersizse yok', () => {
    expect(suggestSuffix('cluster-a', ['cb'])).toBe('ca');
    expect(suggestSuffix('hub-1', [])).toBe('h1');
    expect(suggestSuffix('team-a-prod', [])).toBe('tap');
    expect(suggestSuffix('cluster-a', ['CA'])).toBeUndefined();
    expect(suggestSuffix('', [])).toBeUndefined();
  });
});
