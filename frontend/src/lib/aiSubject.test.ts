import { describe, it, expect, vi } from 'vitest';
import {
  formatAiParamForUrl, aiSelfParam,
  formatAiParam, parseAiParam, aiSubjectTitle, aiSubjectSubtitle,
  AI_KINDS, CHART_SCOPES, chartScopeLabel, type AISubject,
} from './aiSubject';

// v0.9.477 — AI-drawer'ın `?ai=` kodeği. Çekmece TEK yüzey olduğundan
// parse hatası = yanlış özne için LLM çağrısı (ya da boş çekmece). Pin'lenen
// sözleşme: her tür round-trip eder, ':' içeren id ayracı bozmaz, eksik/fazla
// segment ve bozuk yüzde-dizisi null döner (çekmece kapalı kalır).

const CASES: Array<{ name: string; subject: AISubject; param: string }> = [
  { name: 'trace',     subject: { kind: 'trace', id: '1b67a4f0c9de' },     param: 'trace:1b67a4f0c9de' },
  { name: 'problem',   subject: { kind: 'problem', id: 'p-42' },           param: 'problem:p-42' },
  { name: 'incident',  subject: { kind: 'incident', id: 'inc-7' },         param: 'incident:inc-7' },
  { name: 'anomaly',   subject: { kind: 'anomaly', id: 'an-9' },           param: 'anomaly:an-9' },
  { name: 'runbook',   subject: { kind: 'runbook', id: 'p-42' },           param: 'runbook:p-42' },
  { name: 'exception', subject: { kind: 'exception', id: 'fp0badc0ffee' }, param: 'exception:fp0badc0ffee' },
  { name: 'span',      subject: { kind: 'span', id: 'trace-1', spanId: 'span-2' }, param: 'span:trace-1:span-2' },
  {
    name: 'service-health',
    subject: { kind: 'service-health', id: 'checkout', fromNs: 1000, toNs: 2000 },
    param: 'service-health:checkout:1000:2000',
  },
  {
    name: 'charts',
    subject: { kind: 'charts', id: 'checkout', fromNs: 1000, toNs: 2000, scope: 'dur' },
    param: 'charts:checkout:1000:2000:dur',
  },
];

describe('formatAiParam / parseAiParam round-trip', () => {
  for (const c of CASES) {
    it(`${c.name} round-trips`, () => {
      expect(formatAiParam(c.subject)).toBe(c.param);
      expect(parseAiParam(c.param)).toEqual(c.subject);
    });
  }

  it('covers every declared kind (yeni tür eklenince bu test düşer)', () => {
    expect(new Set(CASES.map(c => c.subject.kind))).toEqual(new Set(AI_KINDS));
  });
});

describe('parseAiParam — id encoding', () => {
  it("id içindeki ':' ayracı bozmaz", () => {
    const s: AISubject = { kind: 'service-health', id: 'ns:checkout', fromNs: 1, toNs: 2 };
    const p = formatAiParam(s);
    expect(p).toBe('service-health:ns%3Acheckout:1:2');
    expect(parseAiParam(p)).toEqual(s);
  });
  it('boşluk / yüzde / eğik çizgi taşıyan id round-trip eder', () => {
    for (const id of ['a b', '100%', 'a/b', 'çökme', 'a+b', 'a&b=c']) {
      const p = formatAiParam({ kind: 'exception', id });
      expect(parseAiParam(p)).toEqual({ kind: 'exception', id });
    }
  });
  it('bozuk yüzde-dizisi atmak yerine null döner', () => {
    expect(parseAiParam('trace:%E0%A4%A')).toBeNull();
  });
});

describe('parseAiParam — reddedilenler', () => {
  const bad = [
    ['null', null],
    ['undefined', undefined],
    ['boş', ''],
    ['bilinmeyen tür', 'metric:foo'],
    ['id yok', 'trace'],
    ['boş id', 'trace:'],
    ['basit türde fazla segment', 'trace:abc:def'],
    ['span spanId eksik', 'span:trace-1'],
    ['span boş spanId', 'span:trace-1:'],
    ['span fazla segment', 'span:trace-1:span-2:x'],
    ['service-health penceresiz', 'service-health:checkout'],
    ['service-health yarım pencere', 'service-health:checkout:1000'],
    ['service-health sayı değil', 'service-health:checkout:a:b'],
    ['service-health ters pencere', 'service-health:checkout:2000:1000'],
    ['service-health sıfır pencere', 'service-health:checkout:1000:1000'],
    ['service-health negatif başlangıç', 'service-health:checkout:-5:1000'],
  ] as const;
  for (const [name, raw] of bad) {
    it(`${name} → null`, () => expect(parseAiParam(raw)).toBeNull());
  }
});

// v0.10.948 (CoSRE Faz B) — trace çekmecesinin başlığı düğmenin adıyla aynı,
// i18n'li: TR "CoSRE’ye sor · trace <kısa>", EN "Ask CoSRE · trace <kısa>".
// Öteki türlerin başlığı DEĞİŞMEDİ. Dil verilmezse etkin dil (kullanıcı seçimi).
describe('trace başlığı i18n (v0.10.948)', () => {
  const tr = { kind: 'trace', id: '0af7651916cd43dd8448eb211c80319c' } as const;
  it('açık dil', () => {
    expect(`${aiSubjectTitle(tr, 'tr')} · ${aiSubjectSubtitle(tr, 'tr')}`).toBe('CoSRE’ye sor · trace 0af7651916cd43dd…');
    expect(`${aiSubjectTitle(tr, 'en')} · ${aiSubjectSubtitle(tr, 'en')}`).toBe('Ask CoSRE · trace 0af7651916cd43dd…');
  });
  it('öteki türler aynı', () => {
    expect(aiSubjectTitle({ kind: 'span', id: 't', spanId: 's' }, 'tr')).toBe('Explain span');
    expect(aiSubjectTitle({ kind: 'exception', id: 'fp' }, 'tr')).toBe('Explain root cause');
  });
  it('varsayılan dil: kullanıcı seçimi (localStorage) → yoksa İngilizce', () => {
    const mem = new Map<string, string>();
    const ls = { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, v); }, removeItem: (k: string) => { mem.delete(k); } };
    vi.stubGlobal('localStorage', ls);
    vi.stubGlobal('window', { localStorage: ls });
    try {
      expect(aiSubjectTitle(tr)).toBe('Ask CoSRE');
      mem.set('coremetry.lang', 'tr');
      expect(aiSubjectTitle(tr)).toBe('CoSRE’ye sor');
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe('başlık yardımcıları', () => {
  it('her tür için başlık üretir', () => {
    for (const c of CASES) {
      expect(aiSubjectTitle(c.subject).length).toBeGreaterThan(0);
    }
  });
  it('uzun id kısaltılır, servis adı olduğu gibi kalır', () => {
    // v0.10.948 — trace alt satırı "trace <kısa kimlik>" (başlık "CoSRE’ye sor · trace …").
    expect(aiSubjectSubtitle({ kind: 'trace', id: 'a'.repeat(32) })).toBe(`trace ${'a'.repeat(16)}…`);
    expect(aiSubjectSubtitle({ kind: 'problem', id: 'b'.repeat(32) })).toBe(`${'b'.repeat(16)}…`);
    expect(aiSubjectSubtitle({ kind: 'service-health', id: 'checkout-service-long', fromNs: 1, toNs: 2 }))
      .toBe('checkout-service-long');
    expect(aiSubjectSubtitle({ kind: 'span', id: 'trace-1', spanId: 'span-2' }))
      .toBe('trace-1 · span span-2');
  });
});


// v0.9.1033 — charts öznesi (ServiceCharts AI çekmecesi, Ⓐ+Ⓑ).
// service-health ile AYNI pencere doğrulamasını paylaşır ama bir segment
// daha taşır; arity karışırsa iki özne birbirinin linkini açardı.
describe('parseAiParam — charts kapsamı', () => {
  it('her kapsam round-trip eder', () => {
    for (const scope of CHART_SCOPES) {
      const s: AISubject = { kind: 'charts', id: 'svc', fromNs: 10, toNs: 20, scope };
      expect(parseAiParam(formatAiParam(s))).toEqual(s);
    }
  });

  it('bilinmeyen kapsam reddedilmez, en genişe düşer (backend ile aynı)', () => {
    expect(parseAiParam('charts:svc:10:20:garbage')).toEqual(
      { kind: 'charts', id: 'svc', fromNs: 10, toNs: 20, scope: 'all' });
  });

  it('eksik kapsam segmenti = service-health arity → reddedilir', () => {
    // Aksi halde 4 segmentli bir charts linki sessizce parse edilir ve
    // kapsam UYDURULUR.
    expect(parseAiParam('charts:svc:10:20')).toBeNull();
  });

  it('fazladan segment reddedilir', () => {
    expect(parseAiParam('charts:svc:10:20:dur:extra')).toBeNull();
  });

  it('ters/sıfır pencere reddedilir (service-health ile aynı kural)', () => {
    expect(parseAiParam('charts:svc:20:10:dur')).toBeNull();
    expect(parseAiParam('charts:svc:0:10:dur')).toBeNull();
  });

  it("':' içeren servis adı ayracı bozmaz", () => {
    const s: AISubject = { kind: 'charts', id: 'a:b', fromNs: 10, toNs: 20, scope: 'rps' };
    expect(parseAiParam(formatAiParam(s))).toEqual(s);
  });

  it('her kapsamın etiketi var ve benzersiz', () => {
    const labels = CHART_SCOPES.map(chartScopeLabel);
    expect(labels.every(l => l.length > 0)).toBe(true);
    expect(new Set(labels).size).toBe(labels.length);
  });
});

// v0.10.731 (operator-reported: "explain trace diyince sonuna tekrar trace
// id ekliyor") — SELF biçimi: özne sayfanın kendi kimliğiyse `?ai=trace`.
describe('SELF biçimi — ?ai=<kind> sayfa kimliğinden çözülür (v0.10.731)', () => {
  const page = new URLSearchParams('id=e4de1be1b3227254cfb1c86c208b4a0a&range=6h');
  it('trace öznesi sayfanın ?id= ile aynıysa adrese YALNIZ kind yazılır', () => {
    expect(formatAiParamForUrl({ kind: 'trace', id: 'e4de1be1b3227254cfb1c86c208b4a0a' }, page)).toBe('trace');
  });
  it('id farklıysa / sayfa parametresi yoksa kanonik uzun biçim', () => {
    expect(formatAiParamForUrl({ kind: 'trace', id: 'ffff' }, page)).toBe('trace:ffff');
    expect(formatAiParamForUrl({ kind: 'trace', id: 'ffff' }, null)).toBe('trace:ffff');
    expect(formatAiParamForUrl({ kind: 'trace', id: 'ffff' }, new URLSearchParams(''))).toBe('trace:ffff');
  });
  it('span KISMİ kısalır (v0.10.743: trace id sayfadan, spanId açık); charts/service-health kısalmaz', () => {
    expect(formatAiParamForUrl({ kind: 'span', id: 'e4de1be1b3227254cfb1c86c208b4a0a', spanId: 'ab' }, page))
      .toBe('span:ab');
    expect(aiSelfParam('span')).toBe('id');
    expect(aiSelfParam('trace')).toBe('id');
    expect(aiSelfParam('charts')).toBeNull();
    expect(aiSelfParam('service-health')).toBeNull();
  });
  it('kısa biçim parse: sayfa id\'si → özne; id yoksa null (yanlış özne açmaz)', () => {
    expect(parseAiParam('trace', page)).toEqual({ kind: 'trace', id: 'e4de1be1b3227254cfb1c86c208b4a0a' });
    expect(parseAiParam('trace', new URLSearchParams('range=6h'))).toBeNull();
    expect(parseAiParam('trace')).toBeNull();
    // v0.10.743 — problem artık kısa biçim taşır (?problem=); bu sayfada o param yok → null.
    expect(parseAiParam('problem', page)).toBeNull();
    // Kısa biçimi olmayan kind çıplak gelirse reddedilir.
    expect(parseAiParam('charts', page)).toBeNull();
  });
  it('uzun biçim AYNEN çalışır (eski linkler); gidiş-dönüş kanonik', () => {
    const s = { kind: 'trace' as const, id: 'e4de1be1b3227254cfb1c86c208b4a0a' };
    expect(parseAiParam('trace:e4de1be1b3227254cfb1c86c208b4a0a', page)).toEqual(s);
    expect(parseAiParam(formatAiParamForUrl(s, page), page)).toEqual(s);
    // Karşılaştırma/sunucu tarafı kanonik biçimi kullanmaya devam eder.
    expect(formatAiParam(s)).toBe('trace:e4de1be1b3227254cfb1c86c208b4a0a');
  });
});

// v0.10.731/743 — SELF biçimi: özne sayfanın kendi kimliğiyse adres id'yi
// tekrarlamaz (`?ai=trace`, `?ai=problem`, `?ai=exception`, `?ai=span:<spanId>`).
// Kısa biçim yalnız sayfa parametresi verilince çözülür; eşleşmeyen id
// uzun biçimde kalır; uzun biçim her zaman parse edilir (geriye dönük).
describe('SELF biçimi (v0.10.731/743)', () => {
  const page = (o: Record<string, string>) => new URLSearchParams(o);
  it("trace: ?id eşleşince 'trace', çözüm sayfadan, round-trip", () => {
    const s = { kind: 'trace', id: 'abc123' } as const;
    expect(formatAiParamForUrl(s, page({ id: 'abc123' }))).toBe('trace');
    expect(parseAiParam('trace', page({ id: 'abc123' }))).toEqual(s);
    expect(formatAiParamForUrl(s, page({ id: 'other' }))).toBe('trace:abc123'); // eşleşmiyor → uzun
    expect(parseAiParam('trace', page({}))).toBeNull(); // sayfa kimliği yok → null
    expect(parseAiParam('trace')).toBeNull();
  });
  it('problem (?problem=) ve exception (?exc=)', () => {
    const pr = { kind: 'problem', id: 'p-9' } as const;
    expect(formatAiParamForUrl(pr, page({ problem: 'p-9' }))).toBe('problem');
    expect(parseAiParam('problem', page({ problem: 'p-9' }))).toEqual(pr);
    const ex = { kind: 'exception', id: 'a6a7f0ebb720fc5c' } as const;
    expect(formatAiParamForUrl(ex, page({ exc: 'a6a7f0ebb720fc5c' }))).toBe('exception');
    expect(parseAiParam('exception', page({ exc: 'a6a7f0ebb720fc5c' }))).toEqual(ex);
    // Uzun biçim aynen çalışır (eski/paylaşılan linkler).
    expect(parseAiParam('exception:a6a7f0ebb720fc5c', page({}))).toEqual(ex);
  });
  it("span kısmi: 'span:<spanId>' — trace id sayfadan, spanId açık; 3 parçalı biçim korunur", () => {
    const sp = { kind: 'span', id: 'abc123', spanId: 's:1' } as const;
    expect(formatAiParamForUrl(sp, page({ id: 'abc123' }))).toBe('span:s%3A1');
    expect(parseAiParam('span:s%3A1', page({ id: 'abc123' }))).toEqual(sp);
    expect(parseAiParam('span:s%3A1', page({}))).toBeNull(); // sayfa kimliği yok
    expect(parseAiParam('span', page({ id: 'abc123' }))).toBeNull(); // spanId şart
    expect(formatAiParamForUrl(sp, page({ id: 'other' }))).toBe('span:abc123:s%3A1');
    expect(parseAiParam('span:abc123:s%3A1')).toEqual(sp);
  });
  it('service-health / charts kısaltılmaz (pencere/kapsam taşır)', () => {
    const sh = { kind: 'service-health', id: 'svc', fromNs: 1000, toNs: 2000 } as const;
    expect(formatAiParamForUrl(sh, page({ name: 'svc' }))).toBe(formatAiParam(sh));
  });
});
