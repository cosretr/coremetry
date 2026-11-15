import { describe, it, expect, vi } from 'vitest';
import {
  formatAiParamForUrl, aiSelfParam,
  formatAiParam, parseAiParam, aiSubjectTitle, aiSubjectTopic,
  AI_KINDS, CHART_SCOPES, chartScopeLabel, type AISubject,
  dashboardPanelLoc, isMetricPanelLoc,
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
  {
    // v0.10.1284 — AI SRE Faz 1a: log artışı (servis ':' içerebilir, arama kodlanır).
    name: 'log-spike',
    subject: { kind: 'log-spike', id: 'ns:checkout', fromNs: 1000, toNs: 2000, mode: 'sel', env: 'prod', q: 'level:error AND "timeout"', level: 'error' },
    param: 'log-spike:ns%3Acheckout:1000:2000:sel:prod:level%3Aerror%20AND%20%22timeout%22:error',
  },
  {
    // v0.10.1291 — tek log satırı; sunucu kodeği mcptools.LogLineSubject ile bayt-aynı.
    name: 'log',
    subject: { kind: 'log', id: 'pay ments', tsNs: '1700000000123000000', pod: 'pod:1', traceId: '0af7651916cd43dd8448eb211c80319c', hash: 'ca29aa39' },
    param: 'log:1700000000123000000:pay%20ments:pod%3A1:0af7651916cd43dd8448eb211c80319c:ca29aa39',
  },
  {
    // v0.10.1367 — endpoint "CoSRE'ye sor": servis + rota kodlanır, fiil isteğe bağlı.
    name: 'endpoint',
    subject: { kind: 'endpoint', id: 'acm-web', route: '/ACMWEB/application/{id}', method: 'POST', fromNs: 1000, toNs: 2000 },
    param: 'endpoint:acm-web:%2FACMWEB%2Fapplication%2F%7Bid%7D:1000:2000:POST',
  },
  {
    name: 'endpoint (fiilsiz)',
    subject: { kind: 'endpoint', id: 'ns:cart', route: '/cart', fromNs: 1000, toNs: 2000 },
    param: 'endpoint:ns%3Acart:%2Fcart:1000:2000',
  },
  {
    // v0.10.1435 — panel: konum + pencere + brush + seçili seri (Go ai_subject_test.go aynası).
    name: 'metric_panel',
    subject: { kind: 'metric_panel', id: 'd.dash01.p_2', fromNs: 1000, toNs: 5000, brushFromNs: 2000, brushToNs: 3000, series: ['pod-a', 'ns:x y'] },
    param: 'metric_panel:d.dash01.p_2:1000:5000:2000:3000:pod-a,ns%3Ax%20y',
  },
  {
    name: 'metric_panel (Explore, kıyas)',
    subject: { kind: 'metric_panel', id: 'x.B', fromNs: 1000, toNs: 5000, brushFromNs: 0, brushToNs: 0, series: [], cmp: true },
    param: 'metric_panel:x.B:1000:5000:0:0::cmp',
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
    // v0.10.1367 — endpoint: 5 ya da 6 parça, artan pencere, geçerli yüzde dizisi.
    ['endpoint 4 parça', 'endpoint:svc:%2Fcart:1000'],
    ['endpoint 7 parça', 'endpoint:svc:%2Fcart:1000:2000:GET:x'],
    ['endpoint ters pencere', 'endpoint:svc:%2Fcart:2000:1000'],
    ['endpoint sıfır pencere', 'endpoint:svc:%2Fcart:1000:1000'],
    ['endpoint sıfır başlangıç', 'endpoint:svc:%2Fcart:0:1000'],
    ['endpoint bozuk yüzde (servis)', 'endpoint:%zz:%2Fcart:1000:2000'],
    ['endpoint bozuk yüzde (rota)', 'endpoint:svc:%E0%A4%A:1000:2000'],
    ['endpoint boş rota', 'endpoint:svc::1000:2000'],
  ] as const;
  for (const [name, raw] of bad) {
    it(`${name} → null`, () => expect(parseAiParam(raw)).toBeNull());
  }
});

// v0.10.948 (CoSRE Faz B) — trace çekmecesinin başlığı düğmenin adıyla aynı,
// i18n'li: TR "CoSRE’ye sor · trace <kısa>", EN "Ask CoSRE · trace <kısa>".
// Dil verilmezse etkin dil (kullanıcı seçimi).
describe('trace başlığı i18n (v0.10.948)', () => {
  const tr = { kind: 'trace', id: '0af7651916cd43dd8448eb211c80319c' } as const;
  it('açık dil', () => {
    expect(aiSubjectTitle(tr, 'tr')).toBe('CoSRE’ye sor');
    expect(aiSubjectTitle(tr, 'en')).toBe('Ask CoSRE');
  });
  it('öteki türler', () => {
    // v0.10.1338 — span de artık "CoSRE’ye sor" (aşağıdaki describe).
    expect(aiSubjectTitle({ kind: 'runbook', id: 'p' }, 'tr')).toBe('Runbook AI');
    expect(aiSubjectTitle({ kind: 'service-health', id: 'svc', fromNs: 1, toNs: 2 }, 'tr')).toBe('AI triage');
    // v0.10.1326 (operatör) — exception çekmecesi düğmeyle aynı ad: "Ask CoSRE".
    expect(aiSubjectTitle({ kind: 'exception', id: 'fp' }, 'tr')).toBe('CoSRE’ye sor');
    expect(aiSubjectTitle({ kind: 'exception', id: 'fp' }, 'en')).toBe('Ask CoSRE');
    // Model bağlamı kök-neden konusunu korur.
    expect(aiSubjectTopic({ kind: 'exception', id: 'fp' })).toBe('Explain root cause');
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

// v0.10.1338 (operatör) — her AI açıklama affordance'ı "Ask CoSRE" /
// "CoSRE’ye sor" adını taşır: span/problem/incident/anomaly çekmece başlıkları
// da düğmeyle aynı. MODEL bağlamı (drawerChat KONU satırı) semantik kalır.
describe('Ask CoSRE başlıkları (v0.10.1338)', () => {
  const subjects = [
    { s: { kind: 'span', id: 't', spanId: 's' } as const, topic: 'Explain span' },
    { s: { kind: 'problem', id: 'p1' } as const, topic: 'Explain problem' },
    { s: { kind: 'incident', id: 'i1' } as const, topic: 'Explain incident' },
    { s: { kind: 'anomaly', id: 'a1' } as const, topic: 'Explain anomaly' },
  ];
  for (const { s, topic } of subjects) {
    it(`${s.kind} başlığı tr/en`, () => {
      expect(aiSubjectTitle(s, 'tr')).toBe('CoSRE’ye sor');
      expect(aiSubjectTitle(s, 'en')).toBe('Ask CoSRE');
    });
    it(`${s.kind} model konusu "${topic}" kalır`, () => {
      expect(aiSubjectTopic(s)).toBe(topic);
    });
  }
  it('runbook / service-health / charts / log konuları başlıklarıyla aynı', () => {
    expect(aiSubjectTopic({ kind: 'runbook', id: 'p' })).toBe('Runbook AI');
    expect(aiSubjectTopic({ kind: 'service-health', id: 'svc', fromNs: 1, toNs: 2 })).toBe('AI triage');
    expect(aiSubjectTopic({ kind: 'charts', id: 'svc', fromNs: 1, toNs: 2, scope: 'all' })).toBe('AI grafik özeti');
  });
});

describe('başlık yardımcıları', () => {
  it('her tür için başlık üretir', () => {
    for (const c of CASES) {
      expect(aiSubjectTitle(c.subject).length).toBeGreaterThan(0);
    }
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

// v0.10.1284 — AI SRE Faz 1a: explain_log_spike (log artışını ne açıklıyor).
// Filo geneli özne '*', eski (seviyesiz 7 parça) biçim all'a düşer, arama
// 200 karakterde kesilir, bilinmeyen kip/ters pencere reddedilir.
describe('log-spike öznesi', () => {
  it('filo geneli * ↔ boş id', () => {
    const s: AISubject = { kind: 'log-spike', id: '', fromNs: 5, toNs: 9, mode: 'auto', env: '', q: '', level: 'all' };
    expect(formatAiParam(s)).toBe('log-spike:*:5:9:auto:::all');
    expect(parseAiParam('log-spike:*:5:9:auto:::all')).toEqual(s);
  });
  it('seviyesiz 7 parça → all', () => {
    expect(parseAiParam('log-spike:svc:5:9:sel::')).toEqual(
      { kind: 'log-spike', id: 'svc', fromNs: 5, toNs: 9, mode: 'sel', env: '', q: '', level: 'all' });
  });
  it('arama 200 karakterde kesilir', () => {
    const s: AISubject = { kind: 'log-spike', id: 'svc', fromNs: 5, toNs: 9, mode: 'auto', env: '', q: 'x'.repeat(500), level: 'all' };
    const back = parseAiParam(formatAiParam(s));
    expect(back && back.kind === 'log-spike' ? back.q.length : -1).toBe(200);
  });
  it.each([
    ['bilinmeyen kip', 'log-spike:svc:5:9:zoom:::all'],
    ['ters pencere', 'log-spike:svc:9:5:auto:::all'],
    ['eksik segment', 'log-spike:svc:5:9:auto'],
    ['fazla segment', 'log-spike:svc:5:9:auto:::all:x'],
  ])('%s → null', (_n, raw) => expect(parseAiParam(raw)).toBeNull());
  it('başlık + alt satır', () => {
    const s: AISubject = { kind: 'log-spike', id: '', fromNs: 5, toNs: 9, mode: 'sel', env: '', q: '', level: 'error' };
    expect(aiSubjectTitle(s, 'tr')).toBe('CoSRE’ye sor'); // v0.10.1338 — konu: 'Log artışını açıkla'
  });
});

// v0.10.1291 — `log` öznesi (tek log satırı ✨). Zaman METİN kalır: 1.7e18
// JS sayısına sığmaz; bozuk parça → null (yanlış satırı açıklamaktansa hiç).
describe('log öznesi', () => {
  it('pod ve trace boş olabilir', () => {
    const s: AISubject = { kind: 'log', id: 'checkout', tsNs: '1700000000123456789', pod: '', traceId: '', hash: '0000abcd' };
    expect(formatAiParam(s)).toBe('log:1700000000123456789:checkout:::0000abcd');
    expect(parseAiParam('log:1700000000123456789:checkout:::0000abcd')).toEqual(s);
  });
  it('zaman basamakları korunur (2^53 üstü)', () => {
    const back = parseAiParam('log:1700000000123456789:a:::0000abcd');
    expect(back && back.kind === 'log' ? back.tsNs : '').toBe('1700000000123456789');
  });
  it.each([
    ['servissiz', 'log:1700000000123456789::p::0000abcd'],
    ['zaman sayı değil', 'log:17e17:a:::0000abcd'],
    ['sıfır zaman', 'log:0:a:::0000abcd'],
    ['kötü trace', 'log:1:a::xyz:0000abcd'],
    ['kötü özet', 'log:1:a:::abc'],
    ['eksik segment', 'log:1:a::'],
    ['fazla segment', 'log:1:a:::0000abcd:x'],
  ])('%s → null', (_n, raw) => expect(parseAiParam(raw)).toBeNull());
  it('başlık + alt satır', () => {
    const s: AISubject = { kind: 'log', id: 'checkout', tsNs: '1', pod: 'checkout-7f9', traceId: '', hash: '0000abcd' };
    expect(aiSubjectTitle(s, 'tr')).toBe('CoSRE’ye sor'); // v0.10.1338 — konu: 'Log satırını açıkla'
  });
});

// v0.10.1367 — endpoint öznesi: bilinmeyen fiil REDDETTİRMEZ, düşer; fiil
// büyük harfe çevrilir; başlık "CoSRE’ye sor", konu "Endpoint analizi".
describe('endpoint öznesi (v0.10.1367)', () => {
  it('bilinmeyen fiil düşer, özne açılır', () => {
    expect(parseAiParam('endpoint:svc:%2Fcart:1000:2000:BREW')).toEqual({ kind: 'endpoint', id: 'svc', route: '/cart', fromNs: 1000, toNs: 2000 });
  });
  it('küçük harf fiil büyütülür', () => {
    expect(parseAiParam('endpoint:svc:%2Fcart:1000:2000:get')).toMatchObject({ method: 'GET' });
  });
  it('format kanonik: beyaz listede olmayan fiil adrese yazılmaz', () => {
    expect(formatAiParam({ kind: 'endpoint', id: 'svc', route: '/cart', method: 'BREW', fromNs: 1, toNs: 2 })).toBe('endpoint:svc:%2Fcart:1:2');
  });
  it('kısa (SELF) biçim yok', () => {
    expect(parseAiParam('endpoint', new URLSearchParams('service=svc&path=/cart'))).toBeNull();
    expect(aiSelfParam('endpoint')).toBeNull();
  });
  it('başlık + konu + alt satır', () => {
    const s: AISubject = { kind: 'endpoint', id: 'shop-api', route: '/cart', method: 'GET', fromNs: 1, toNs: 2 };
    expect(aiSubjectTitle(s, 'tr')).toBe('CoSRE’ye sor');
    expect(aiSubjectTitle(s, 'en')).toBe('Ask CoSRE');
    expect(aiSubjectTopic(s)).toBe('Endpoint analizi');
  });
});

// v0.10.1435 — metric_panel kodeği: konum biçimi, brush pencere içinde, ≤3 seri,
// 8. parça yalnız cmp; seri adı 80'e kırpılır, 400'ü aşan kodekte sondaki seriler
// düşer (Go TestAiSubjectMetricPanelFit aynası).
describe('metric_panel kodeği (v0.10.1435)', () => {
  it('geçersiz biçimleri reddeder', () => {
    for (const raw of [
      'metric_panel:m:1000:5000:0:0', 'metric_panel:q.A:1000:5000:0:0:', 'metric_panel:x.E:1000:5000:0:0:',
      'metric_panel:d.a.b.c:1000:5000:0:0:', 'metric_panel:m:5000:1000:0:0:', 'metric_panel:m:1000:5000:500:3000:',
      'metric_panel:m:1000:5000:2000:6000:', 'metric_panel:m:1000:5000:0:3000:', 'metric_panel:m:1000:5000:0:0:a,b,c,d',
      'metric_panel:m:1000:5000:0:0:a,,b', 'metric_panel:m:1000:5000:0:0::x', 'metric_panel:m:1000:5000:0:0:%0A',
    ]) {
      expect(parseAiParam(raw), raw).toBeNull();
    }
  });
  it('400 tavanına sığdırır, idempotent', () => {
    const long = 's'.repeat(100) + 'ş';
    const s: AISubject = {
      kind: 'metric_panel', id: `d.${'a'.repeat(64)}.${'b'.repeat(64)}`,
      fromNs: 1790000000000000000, toNs: 1790003600000000000, brushFromNs: 1790000600000000000, brushToNs: 1790001200000000000,
      series: [long, long, long, 'x'], cmp: true,
    };
    const p = formatAiParam(s);
    expect(p.length).toBeLessThanOrEqual(400);
    const back = parseAiParam(p);
    if (!back || back.kind !== 'metric_panel') throw new Error('ayrıştırılamadı');
    expect(back.series.length).toBeGreaterThan(0);
    for (const v of back.series) expect([...v].length).toBe(80);
    expect(formatAiParam(back)).toBe(p);
  });
  it('dashboardPanelLoc: biçimsiz kimlik → null (✨ yok)', () => {
    expect(dashboardPanelLoc('dash01', 'p1')).toBe('d.dash01.p1');
    expect(dashboardPanelLoc('a.b', 'p1')).toBeNull();
    expect(isMetricPanelLoc('x.D')).toBe(true);
  });
});
