// fleetSummary.test.ts — Clusters S4 (v0.10.2169; plan
// docs/frontend/clusters-shadcn-plan.md §0 D1). Sözleşme:
//   • filo cümlesi alarm şeridinin yerine geçer; sayılar kart durumlarından,
//     ek sayının okunuşundan; sıfır sayılı parça yazılmaz;
//   • çubuk yanı "kullanılan / kapasite · %": kapasite yoksa yalnız
//     kullanılan, kullanılan yoksa hiçbir şey (uydurma yok).
import { describe, it, expect } from 'vitest';
import {
  clusterState, fleetSentence, cpuUsageText, memUsageText, usageTone, type ClusterState,
} from './fleetSummary';

const rep = (s: ClusterState, n: number): ClusterState[] => Array.from({ length: n }, () => s);

describe('clusterState', () => {
  it('yanıt vermiyor > bekliyor > kritik > sağlıklı', () => {
    expect(clusterState({ isError: true, isPending: false })).toBe('unreachable');
    expect(clusterState({ isError: false, isPending: true })).toBe('pending');
    expect(clusterState({ isError: false, isPending: false, data: { cluster: 'prod-a', alertsCritical: 2 } })).toBe('critical');
    expect(clusterState({ isError: false, isPending: false, data: { cluster: 'prod-a', alertsWarning: 5 } })).toBe('healthy');
    expect(clusterState({ isError: false, isPending: false, data: { cluster: 'prod-a' } })).toBe('healthy');
  });
  it('sorgu nesnesi yoksa bekliyor', () => {
    expect(clusterState(undefined)).toBe('pending');
  });
});

describe('fleetSentence', () => {
  it('onaylı cümle: 8 cluster, 6 sağlıklı, 1 kritik, 1 yanıt vermiyor', () => {
    expect(fleetSentence([...rep('healthy', 6), 'critical', 'unreachable']))
      .toBe("8 cluster: 6 sağlıklı, 1'inde kritik alarm var, 1'i yanıt vermiyor.");
  });
  it('hepsi sağlıklı', () => {
    expect(fleetSentence(rep('healthy', 4))).toBe('4 cluster: hepsi sağlıklı.');
  });
  it('sıfır sayılı parça yazılmaz; ek sayıya uyar', () => {
    expect(fleetSentence([...rep('healthy', 3), ...rep('critical', 2)]))
      .toBe("5 cluster: 3 sağlıklı, 2'sinde kritik alarm var.");
    expect(fleetSentence([...rep('critical', 3), ...rep('unreachable', 6)]))
      .toBe("9 cluster: 3'ünde kritik alarm var, 6'sı yanıt vermiyor.");
  });
  it('yüklenenler ayrı sayılır (sağlıklı sanılmaz)', () => {
    expect(fleetSentence([...rep('healthy', 2), ...rep('pending', 3)]))
      .toBe("5 cluster: 2 sağlıklı, 3'ü yükleniyor.");
    expect(fleetSentence(rep('pending', 4))).toBe('4 cluster: yükleniyor.');
  });
  it('tek cluster: sayı tekrarlanmaz', () => {
    expect(fleetSentence(['healthy'])).toBe('1 cluster: sağlıklı.');
    expect(fleetSentence(['critical'])).toBe('1 cluster: kritik alarm var.');
    expect(fleetSentence(['unreachable'])).toBe('1 cluster: yanıt vermiyor.');
    expect(fleetSentence(['pending'])).toBe('1 cluster: yükleniyor.');
  });
  it('hepsi aynı sapmada', () => {
    expect(fleetSentence(rep('unreachable', 2))).toBe('2 cluster: hiçbiri yanıt vermiyor.');
    expect(fleetSentence(rep('critical', 2))).toBe('2 cluster: hepsinde kritik alarm var.');
  });
  it('liste boşsa cümle yok', () => {
    expect(fleetSentence([])).toBe('');
  });
});

describe('cpuUsageText', () => {
  it('kullanılan / kapasite çekirdek · %', () => {
    expect(cpuUsageText(148.3, 320)).toBe('148 / 320 çekirdek · %46');
    expect(cpuUsageText(22, 96)).toBe('22 / 96 çekirdek · %23');
  });
  it('v0.10.2186 — İngilizce arayüz: cores · 9%', () => {
    expect(cpuUsageText(85, 988, 'en')).toBe('85 / 988 cores · 9%');
    expect(cpuUsageText(148.3, undefined, 'en')).toBe('148 cores');
    expect(memUsageText(118 * 1024 ** 3, 384 * 1024 ** 3, 'en')).toBe('118 / 384 GiB · 31%');
  });
  it('10 çekirdeğin altında bir ondalık', () => {
    expect(cpuUsageText(2.44, 16)).toBe('2.4 / 16 çekirdek · %15');
    expect(cpuUsageText(0.04, 8)).toBe('0 / 8 çekirdek · %1');
    expect(cpuUsageText(3, 7.5)).toBe('3 / 7.5 çekirdek · %40');
  });
  it('kapasite yoksa yalnız kullanılan', () => {
    expect(cpuUsageText(148.3, undefined)).toBe('148 çekirdek');
    expect(cpuUsageText(148.3, 0)).toBe('148 çekirdek');
  });
  it('kullanılan yoksa metin yok', () => {
    expect(cpuUsageText(undefined, 320)).toBeNull();
  });
  it('kapasiteyi aşan kullanım %100 ile sınırlı', () => {
    expect(cpuUsageText(400, 320)).toBe('400 / 320 çekirdek · %100');
  });
});

describe('memUsageText', () => {
  const GiB = 1024 ** 3;
  it('birimler farklıysa ikisi de yazılır', () => {
    expect(memUsageText(922 * GiB, 1.28 * 1024 * GiB)).toBe('922 GiB / 1.28 TiB · %70');
  });
  it('birim aynıysa bir kez yazılır', () => {
    expect(memUsageText(118 * GiB, 384 * GiB)).toBe('118 / 384 GiB · %31');
  });
  it('kapasite yoksa yalnız kullanılan', () => {
    expect(memUsageText(118 * GiB, undefined)).toBe('118 GiB');
  });
  it('kullanılan yoksa metin yok', () => {
    expect(memUsageText(undefined, 384 * GiB)).toBeNull();
  });
});

describe('usageTone', () => {
  it('eşikler çubuk rengiyle aynı (pctColor): >85 err, >65 warn, altı nötr', () => {
    expect(usageTone(86)).toBe('err');
    expect(usageTone(85)).toBe('warn');
    expect(usageTone(66)).toBe('warn');
    expect(usageTone(65)).toBeNull();
    expect(usageTone(null)).toBeNull();
  });
});

// v0.10.2189 — İngilizce arayüz: cümle İngilizce, TR sayı eki yok.
describe('fleetSentence — EN', () => {
  it('karışık, hepsi, tek', () => {
    expect(fleetSentence([...rep('healthy', 15), ...rep('critical', 4), 'unreachable'], 'en'))
      .toBe('20 clusters: 15 healthy, 4 with a critical alert, 1 not responding.');
    expect(fleetSentence(rep('healthy', 3), 'en')).toBe('3 clusters: all healthy.');
    expect(fleetSentence(['unreachable'], 'en')).toBe('1 cluster: not responding.');
    expect(cpuUsageText(148, 320, 'en')).toBe('148 / 320 cores · 46%');
  });
});
