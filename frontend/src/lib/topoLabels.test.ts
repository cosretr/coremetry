import { describe, it, expect } from 'vitest';
import {
  depInstanceLabel, depPillLines, depDisambiguation, depPillLinesMap,
  type DepPillNode,
} from './topoLabels';

// v0.8.297 (operator-reported: db pill'inde "sadece oracle yazıyor") — the
// dependency pill's sub-line prefers the concrete identity over the generic
// kind label. Contract of depInstanceLabel:
//   1. db.name wins when present ("COREBANK");
//   2. else the @instance suffix of the node id ("db:oracle@oracle-prod" →
//      "oracle-prod") — UNLESS it just repeats the system name
//      ("db:redis@redis" adds nothing);
//   3. else null — caller falls back to the generic kind label.
describe('depInstanceLabel', () => {
  it('prefers db.name when present', () => {
    expect(depInstanceLabel({ service: 'db:oracle@oracle', subkind: 'oracle', dbName: 'COREBANK' })).toBe('COREBANK');
  });

  it('falls back to the @instance suffix', () => {
    expect(depInstanceLabel({ service: 'db:postgresql@pg-payments', subkind: 'postgresql' })).toBe('pg-payments');
  });

  it('suppresses an @instance that just repeats the system', () => {
    expect(depInstanceLabel({ service: 'db:redis@redis', subkind: 'redis' })).toBeNull();
  });

  it('bare node id without @ or db.name yields null', () => {
    expect(depInstanceLabel({ service: 'db:clickhouse', subkind: 'clickhouse' })).toBeNull();
  });

  it('empty db.name is treated as absent', () => {
    expect(depInstanceLabel({ service: 'db:mysql@my-1', subkind: 'mysql', dbName: '' })).toBe('my-1');
  });
});

// v0.10.517 (operatör: "oracle altta, db name üstte") — somut kimlik başlığa,
// sistem alt satıra; kimlik yoksa eski düzen. Bu gövde TEK düğüme bakar ve
// v0.10.837'de DEĞİŞMEDİ: küme-duyarlı karar depPillLinesMap'e eklendi.
describe('depPillLines', () => {
  it('db.name başlık, sistem alt satır', () => {
    expect(depPillLines({ service: 'db:oracle@orablog-prd', subkind: 'oracle', dbName: 'pgts02_prd' }, 'database'))
      .toEqual({ title: 'pgts02_prd', sub: 'oracle' });
  });
  it('@instance başlık, sistem alt satır', () => {
    expect(depPillLines({ service: 'db:postgresql@pg-payments', subkind: 'postgresql' }, 'database'))
      .toEqual({ title: 'pg-payments', sub: 'postgresql' });
  });
  it('kimlik yoksa sistem başlık, tür etiketi alt satır (eski düzen)', () => {
    expect(depPillLines({ service: 'db:redis@redis', subkind: 'redis' }, 'cache'))
      .toEqual({ title: 'redis', sub: 'cache' });
    expect(depPillLines({ service: 'queue:kafka' }, 'queue')).toEqual({ title: 'kafka', sub: 'queue' });
  });
});

// ── v0.10.837 (Operatör-bildirimli, prod) ────────────────────────────
// Bir servisin Topology sekmesinde ALTI ayrı Oracle düğümü aynı başlığı
// (aynı db.name) taşıyordu; yükleri tamamen farklıydı (103 çağrı %0 hata /
// 2.8K %1.3 / 67.5K %5.5), yani operatör yanlış düğüme bakabiliyordu. Kök
// neden: depPillLines tek düğüme bakar, db.name'i koşulsuz başlık yapar ve
// düğümü asıl ayıran `@<instance>` parçasını siler.
//
// Ayrım KÜME-DUYARLI bir karardır; aşağıdaki gövde onu render edilecek
// listeye bakarak verir.
const kindLabel = (n: DepPillNode) => (n.kind === 'queue' ? 'queue' : 'database');

describe('depDisambiguation', () => {
  const cases: Array<{
    name: string;
    nodes: DepPillNode[];
    want: Record<string, string>;
  }> = [
    {
      name: 'aynı db.name FARKLI instance → ayrım EKLENİR (operatör hâli)',
      nodes: [
        { service: 'db:oracle@db-core-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
        { service: 'db:oracle@db-core-b', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      ],
      want: { 'db:oracle@db-core-a': 'db-core-a', 'db:oracle@db-core-b': 'db-core-b' },
    },
    {
      name: 'aynı db.name AYNI instance, farklı motor → ikili zaten benzersiz, ayrım YOK',
      nodes: [
        { service: 'db:oracle@db-core-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
        { service: 'db:mysql@db-core-a', kind: 'db', subkind: 'mysql', dbName: 'shop_prd' },
      ],
      want: {},
    },
    {
      name: 'farklı db.name → ayrım YOK',
      nodes: [
        { service: 'db:oracle@db-core-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
        { service: 'db:oracle@db-core-b', kind: 'db', subkind: 'oracle', dbName: 'shop_stg' },
      ],
      want: {},
    },
    {
      name: 'db.name yok → bugünkü yol (@instance zaten ayırıyor)',
      nodes: [
        { service: 'db:postgresql@pg-payments', kind: 'db', subkind: 'postgresql' },
        { service: 'db:postgresql@pg-orders', kind: 'db', subkind: 'postgresql' },
      ],
      want: {},
    },
    {
      name: 'kuyruk düğümleri db.name taşımaz → dokunulmaz',
      nodes: [
        { service: 'queue:kafka', kind: 'queue', subkind: 'kafka' },
        { service: 'queue:rabbitmq', kind: 'queue', subkind: 'rabbitmq' },
      ],
      want: {},
    },
    {
      name: 'servis düğümleri (kind yok) hiç değerlendirilmez',
      nodes: [
        { service: 'shop-payment' },
        { service: 'shop-orders' },
      ],
      want: {},
    },
    {
      name: 'kısa ana makine adı FQDN\'lerde aynıysa merdiven tam instance\'a çıkar',
      nodes: [
        { service: 'db:oracle@db-core-a.eu.example.com', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
        { service: 'db:oracle@db-core-a.us.example.com', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      ],
      want: {
        'db:oracle@db-core-a.eu.example.com': 'db-core-a.eu.example.com',
        'db:oracle@db-core-a.us.example.com': 'db-core-a.us.example.com',
      },
    },
    {
      name: 'uzun ana makine adı tek noktaya kadar kısalır (tam alan adı basılmaz)',
      nodes: [
        { service: 'db:oracle@db-core-a.internal.example.com', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
        { service: 'db:oracle@db-log-a.internal.example.com', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      ],
      want: {
        'db:oracle@db-core-a.internal.example.com': 'db-core-a',
        'db:oracle@db-log-a.internal.example.com': 'db-log-a',
      },
    },
    {
      name: 'instance\'sız düğüm çakışırsa merdiven düğüm kimliğine iner',
      nodes: [
        { service: 'db:oracle@db-core-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
        { service: 'db:oracle', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      ],
      want: { 'db:oracle@db-core-a': 'oracle@db-core-a', 'db:oracle': 'oracle' },
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(Object.fromEntries(depDisambiguation(c.nodes, kindLabel))).toEqual(c.want);
    });
  }
});

describe('depPillLinesMap', () => {
  it('çakışmayan düğüm v0.10.517 etiketini BAYT BAYT korur', () => {
    const nodes: DepPillNode[] = [
      { service: 'db:oracle@db-core-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      { service: 'db:postgresql@pg-payments', kind: 'db', subkind: 'postgresql' },
      { service: 'queue:kafka', kind: 'queue', subkind: 'kafka' },
      { service: 'shop-payment' },
    ];
    const m = depPillLinesMap(nodes, kindLabel);
    expect(m.get('db:oracle@db-core-a')).toEqual({ title: 'shop_prd', sub: 'oracle' });
    expect(m.get('db:postgresql@pg-payments')).toEqual({ title: 'pg-payments', sub: 'postgresql' });
    expect(m.get('queue:kafka')).toEqual({ title: 'kafka', sub: 'queue' });
    // Servis düğümü bağımlılık pili değildir — eşleme girmez.
    expect(m.has('shop-payment')).toBe(false);
  });

  it('çakışan düğümde ana makine BAŞLIK, db.name motor adının yanında alt satırda', () => {
    const nodes: DepPillNode[] = [
      { service: 'db:oracle@db-core-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      { service: 'db:oracle@db-log-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
    ];
    const m = depPillLinesMap(nodes, kindLabel);
    expect(m.get('db:oracle@db-core-a')).toEqual({ title: 'db-core-a', sub: 'oracle · shop_prd' });
    expect(m.get('db:oracle@db-log-a')).toEqual({ title: 'db-log-a', sub: 'oracle · shop_prd' });
  });

  // ── SÖZLEŞME ────────────────────────────────────────────────────────
  // Render edilen düğüm kümesi içinde FARKLI kimliğe sahip iki düğüm AYNI
  // (başlık, alt satır) ikilisini ÜRETEMEZ. Operatörün ekranının sentetik
  // karşılığı: altı Oracle düğümü, hepsinde aynı db.name, instance'ları
  // farklı. Yamasız hâlde bu test KIRMIZI (altı ikili de 'shop_prd|oracle').
  it('SÖZLEŞME: altı düğüm, aynı db.name, farklı instance → altı BENZERSİZ ikili', () => {
    const hosts = ['db-core-a', 'db-core-b', 'db-core-c', 'db-log-a', 'db-log-b', 'db-rep-a'];
    const nodes: DepPillNode[] = hosts.map(h => ({
      service: `db:oracle@${h}`, kind: 'db', subkind: 'oracle', dbName: 'shop_prd',
    }));
    const m = depPillLinesMap(nodes, kindLabel);
    const pairs = nodes.map(n => {
      const p = m.get(n.service)!;
      return JSON.stringify([p.title, p.sub]);
    });
    expect(new Set(pairs).size, `çakışan ikililer: ${pairs.join(' | ')}`).toBe(nodes.length);
    // Ayrım BAŞLIKTA görünür — hover gerekmez.
    expect(new Set(nodes.map(n => m.get(n.service)!.title)).size).toBe(nodes.length);
  });

  it('SÖZLEŞME: karışık küme (db + queue + servis) de benzersiz ikililer üretir', () => {
    const nodes: DepPillNode[] = [
      { service: 'db:oracle@db-core-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      { service: 'db:oracle@db-core-b', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      { service: 'db:oracle@db-log-a', kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
      { service: 'db:postgresql@pg-payments', kind: 'db', subkind: 'postgresql', dbName: 'shop_prd' },
      { service: 'db:redis@redis', kind: 'db', subkind: 'redis' },
      { service: 'queue:kafka', kind: 'queue', subkind: 'kafka' },
      { service: 'shop-payment' },
    ];
    const m = depPillLinesMap(nodes, kindLabel);
    const deps = nodes.filter(n => n.kind);
    const pairs = deps.map(n => {
      const p = m.get(n.service)!;
      return JSON.stringify([p.title, p.sub]);
    });
    expect(new Set(pairs).size, `çakışan ikililer: ${pairs.join(' | ')}`).toBe(deps.length);
    // postgresql aynı db.name'i taşıyor ama alt satırı zaten farklı (motor
    // adı) — ikili benzersiz olduğu için etiketi DEĞİŞMEZ (v0.10.517).
    expect(m.get('db:postgresql@pg-payments')).toEqual({ title: 'shop_prd', sub: 'postgresql' });
  });
});
