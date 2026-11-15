import { describe, it, expect } from 'vitest';
import { podMatchesService, stripEnvSuffix, servicePodRegex } from './podWorkload';

// v0.9.130 — operatör raporu: "infrastructure tabında bazı cluster'ları
// buluyor bazılarını bulamıyor". Zincir eskiden depRow bulununca podSet'e
// KİLİTLENİP "<deploy>-" prefix yedeğine düşmüyordu; KSM'i kısmi/boş olan
// cluster'da pod'lar podSet'te olmadığından hiç eşleşmiyordu. Düzeltme:
// deploy varken podSet ADDİTİF (üyelik ⋃ prefix).
describe('podMatchesService', () => {
  const P = (pod: string, namespace = 'callcenter', service?: string) =>
    ({ pod, namespace, service });
  const opts = (o: Partial<{ service: string; deploy: string; ns: string; podNames: Set<string> | null }>) =>
    ({ service: 'shop-core-prep', deploy: '', ns: '', podNames: null, ...o });

  it('REGRESYON: depRow var ama podNames prefix-eşleşen pod\'u kaçırıyor → yine eşleşir', () => {
    // Eski kilitli zincirde bu FALSE dönerdi (podSet.has=false, prefix
    // yedeğine düşmezdi) — cluster boş görünürdü. Şimdi prefix yakalar.
    const pod = P('shop-core-prep-6f8744665f-k2tcj');
    const podNames = new Set(['shop-core-prep-aaaa111111-zzzzz']); // farklı pod
    expect(podMatchesService(pod, opts({ deploy: 'shop-core-prep', ns: 'callcenter', podNames })))
      .toBe(true);
  });

  it('REGRESYON: applyDeployKSM zero-serisi → podNames boş Set → prefix yine eşleşir', () => {
    const pod = P('shop-core-prep-6f8744665f-k2tcj');
    expect(podMatchesService(pod, opts({ deploy: 'shop-core-prep', ns: 'callcenter', podNames: new Set() })))
      .toBe(true);
  });

  it('podSet, prefix\'in kaçırdığı özel-adlı pod\'u yakalar (union geniş)', () => {
    const pod = P('legacy-worker-xyz'); // "<deploy>-" öneki taşımıyor
    const podNames = new Set(['legacy-worker-xyz']);
    expect(podMatchesService(pod, opts({ deploy: 'shop-core-prep', ns: 'callcenter', podNames })))
      .toBe(true);
  });

  it('ns süzgeci: farklı namespace\'in pod\'u dışlanır (ns türetildiyse)', () => {
    const pod = P('shop-core-prep-6f8744665f-k2tcj', 'other-ns');
    expect(podMatchesService(pod, opts({ deploy: 'shop-core-prep', ns: 'callcenter', podNames: new Set() })))
      .toBe(false);
  });

  it('deploy var, ne üyelik ne prefix → eşleşmez (daraltma korunur)', () => {
    const pod = P('unrelated-app-6f8744665f-k2tcj');
    expect(podMatchesService(pod, opts({ deploy: 'shop-core-prep', ns: 'callcenter', podNames: new Set() })))
      .toBe(false);
  });

  it('S2: ad eşitliği yedeği yok — sahiplik entity iş yükünün KSM pod\'larından', () => {
    const hit = P('shop-core-prep-6f8744665f-k2tcj');
    expect(podMatchesService(hit, opts({ service: 'shop-core-prep' }))).toBe(false);
    const owned = new Set(['shop-core-prep-6f8744665f-k2tcj']);
    expect(podMatchesService(hit, opts({ service: 'shop-core-prep', podNames: owned }))).toBe(true);
    const sibling = P('shop-core-prep-batch-7f5c96cd4b-gtg2f');
    expect(podMatchesService(sibling, opts({ service: 'shop-core-prep', podNames: owned }))).toBe(false);
  });

  it('yedek mod: enrichment service alanı eşleşir', () => {
    const pod = P('renamed-pod-abc', 'callcenter', 'shop-core-prep');
    expect(podMatchesService(pod, opts({ service: 'shop-core-prep' }))).toBe(true);
  });
});

// v0.9.535 — operatör direktifi: "mobile*bff-prod sonunda prod olmadan
// bul" + somut örnek: servis mobile-home-prod, pod
// mobile-home-bff-<hash>-<rand>. İki adlandırma boşluğu birden:
// (a) k8s deployment adı servis adındaki env ekini taşımıyor,
// (b) pod'da servis adında olmayan bir -bff kuyruğu olabiliyor.
describe('stripEnvSuffix', () => {
  it('bilinen env ekleri kuyruktayken soyulur', () => {
    expect(stripEnvSuffix('mobile-loans-bff-prod')).toBe('mobile-loans-bff');
    expect(stripEnvSuffix('mobile-home-prod')).toBe('mobile-home');
    expect(stripEnvSuffix('shop-login-int')).toBe('shop-login');
    expect(stripEnvSuffix('svc-uat')).toBe('svc');
    expect(stripEnvSuffix('svc-prep')).toBe('svc');
  });
  it('ad ortasındaki env sözcüğü DOKUNULMAZ', () => {
    expect(stripEnvSuffix('shop-digital-limitcore-prod-oneagent')).toBe('shop-digital-limitcore-prod-oneagent');
    expect(stripEnvSuffix('prod-gateway')).toBe('prod-gateway');
  });
  it('bilinmeyen ek / eksiz ad aynen kalır', () => {
    expect(stripEnvSuffix('mobile-loans-bff')).toBe('mobile-loans-bff');
    expect(stripEnvSuffix('svc-production')).toBe('svc-production');
  });
  it('yalnız ekten ibaret ad soyulmaz (boş ad üretme)', () => {
    expect(stripEnvSuffix('-prod')).toBe('-prod');
  });
});

// v0.9.536 — hedefli envanter seçicisi: sunucu bu regex'i pod=~ olarak
// PromQL'e gömer. Operator-reported kök sebep: cluster-geneli topk(500)
// düşük trafikli BFF pod'larını hiç döndürmüyordu — istemci gelmeyen
// pod'u eşleştiremez.
describe('servicePodRegex', () => {
  it('katalog deploy + servis + soyulmuş ad, sıralı ve tekilleşmiş', () => {
    expect(servicePodRegex('mobile-home-bff-prod', ''))
      .toBe('(mobile-home-bff-prod|mobile-home-bff)-.*');
  });
  it('deploy doluysa başa girer', () => {
    expect(servicePodRegex('mobile-home-prod', 'mobile-home-bff'))
      .toBe('(mobile-home-bff|mobile-home-prod|mobile-home)-.*');
  });
  it('eksiz ad tek aday üretir (çift üretme)', () => {
    expect(servicePodRegex('coremetry-monolithic', ''))
      .toBe('(coremetry-monolithic)-.*');
  });
  it('S2: entity iş yükü adları adaylara girer (tekil)', () => {
    expect(servicePodRegex('orders-prod', '', ['orders-api', 'orders-prod']))
      .toBe('(orders-api|orders-prod|orders)-.*');
  });
  it('regex metaları kaçışlanır — servis adı PromQL regex bozamaz', () => {
    expect(servicePodRegex('svc.v2', '')).toBe('(svc\\.v2)-.*');
  });
});
