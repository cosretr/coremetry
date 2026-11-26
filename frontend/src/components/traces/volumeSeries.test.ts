// volumeSeries.test.ts — /traces histogramının EKSEN SÖZLEŞMESİNİ pinler
// (v0.9.843, operatör isteği: "süre solda, sayı sağda" — Grafana düzeni).
//
// Neden test: eksen alanı üç seride tek kelimelik bir string ('left' /
// 'right'). Bir refactor sırasında sessizce geri dönerse hiçbir tip hatası
// vermez, hiçbir gate yakalamaz — yalnız grafik yanlış okunur. Buradaki
// tablo, takasın hangi seride olduğunu ve süre biçimlendiricisinin HER
// birim dalını (ms / s, v0.6.36 birim-karışımı disiplini) sabitler.

import { describe, expect, it } from 'vitest';
import type { SpanMetricSeries } from '@/lib/types';
import { weightedStatAvg, buildVolumeSeries, volumeBucketCaption, fmtStepShort, fmtVolumeDuration, volumeUnitLabel, stripBarStep, stripScope, isEntrySpanKey, volumeUnitFor, volumeHint, dataExtent, stripRootOnly, volumeEmptyNote, VOLUME_EMPTY_DEFAULT, errorStripUnit, errorStripHint, stripHeaderStats } from './volumeSeries';

const S = 1_000_000_000; // 1 saniye, ns
const T0 = 1_700_000_000 * S;

/** ns damgalı seri kurucu; step saniye cinsinden. */
function mk(values: (number)[], stepSec = 300): SpanMetricSeries[] {
  return [{ groupKey: [], points: values.map((v, i) => ({ time: T0 + i * stepSec * S, value: v })) }];
}

const byKey = (cfg: ReturnType<typeof buildVolumeSeries>, k: string) =>
  cfg.series.find(s => s.key === k)!;

describe('buildVolumeSeries — eksen eşlemesi (v0.10.656: süre SOL, sayı SAĞ — operatör)', () => {
  const cfg = buildVolumeSeries(mk([10, 20, 30]), mk([1, 2, 3]), mk([120, 130, 140]));

  // TABLO: seri → beklenen eksen. Takasın TEK kaynağı bu.
  it.each([
    ['total', 'left', 'bar'],   // span sayısı → SOL (v0.10.1180)
    ['error', 'left', 'bar'],   // span sayısı → SOL
    ['rt', 'right', 'line'],    // SÜRE → SAĞ
  ] as const)('%s serisi %s eksende ve %s tipinde', (key, axis, type) => {
    const s = byKey(cfg, key);
    expect(s.axis).toBe(axis);
    expect(s.type).toBe(type);
  });

  it('süre ekseni ile sayı ekseni AYRI taraflarda (çift eksen korunur)', () => {
    expect(byKey(cfg, 'rt').axis).not.toBe(byKey(cfg, 'total').axis);
  });

  it('çizim sırası korunur: tam bar → hata payı → p50 çizgisi en üstte', () => {
    expect(cfg.series.map(s => s.key)).toEqual(['total', 'error', 'rt']);
  });
});

describe('buildVolumeSeries — veri dönüşümü', () => {
  it('ns → unix saniye ve kova genişliği (sn) adımdan türer', () => {
    const cfg = buildVolumeSeries(mk([1, 2, 3], 300), null, null);
    expect(cfg.times).toEqual([1_700_000_000, 1_700_000_300, 1_700_000_600]);
    expect(cfg.barStepSec).toBe(300);
    expect(cfg.lineStepSec).toBe(300);
  });

  // v0.10.2480 (operator-reported) — eski bucketMin alt-dakika adımı 1'e
  // yuvarlıyordu: 10 sn'lik çubuklar "1m bucket" okunuyordu.
  it('alt-dakika adım saniye olarak kalır (1 dk\'ya yuvarlanmaz)', () => {
    expect(buildVolumeSeries(mk([1, 2], 30), null, null).barStepSec).toBe(30);
    expect(buildVolumeSeries(mk([1, 2], 10), null, null).barStepSec).toBe(10);
    expect(buildVolumeSeries(mk([1]), null, null).barStepSec).toBe(0);
  });

  it('boş count → boş konfig (grafik yerine Empty durumu)', () => {
    const cfg = buildVolumeSeries(null, mk([1]), mk([2]));
    expect(cfg.times).toEqual([]);
    expect(cfg.series).toEqual([]);
    expect(cfg.barStepSec).toBe(0);
  });

  it('hata payı toplamı aşamaz, eksik bucket 0 olur', () => {
    const errs: SpanMetricSeries[] = [{
      groupKey: [],
      points: [{ time: T0, value: 99 }], // toplam 10 — kırpılmalı
    }];
    const cfg = buildVolumeSeries(mk([10, 20]), errs, null);
    expect(byKey(cfg, 'error').data).toEqual([10, 0]);
  });

  it('p50 GAP korunur: örneği olmayan ya da 0 dönen bucket null (v0.9.73)', () => {
    const p50: SpanMetricSeries[] = [{
      groupKey: [],
      points: [{ time: T0, value: 0 }, { time: T0 + 300 * S, value: 42 }],
    }];
    const cfg = buildVolumeSeries(mk([1, 2, 3]), null, p50);
    expect(byKey(cfg, 'rt').data).toEqual([null, 42, null]);
  });
});

// Değer+birim şablonu → HER birim dalı (v0.6.36). Bu fonksiyon artık SOL
// ekseni biçimlendiriyor; eşiklerin ikisi de (1000ms → s, 10000ms → ondalıksız)
// ve <10ms ondalık dalı burada sabit.
describe('fmtVolumeDuration — her birim dalı', () => {
  it.each([
    [0, '0.0ms'],
    [0.9, '0.9ms'],
    [9.94, '9.9ms'],
    [10, '10ms'],
    [125.4, '125ms'],
    [999, '999ms'],
    [1000, '1.0s'],
    [3100, '3.1s'],
    [9999, '10.0s'],
    [10000, '10s'],
    [65000, '65s'],
  ])('%dms → %s', (v, want) => {
    expect(fmtVolumeDuration(v)).toBe(want);
  });

  it('ms dalı hiç "s" ile, s dalı hiç "ms" ile bitmez (birim karışımı)', () => {
    for (const v of [0, 1, 42, 999.4]) expect(fmtVolumeDuration(v).endsWith('ms')).toBe(true);
    for (const v of [1000, 5000, 120000]) expect(fmtVolumeDuration(v).endsWith('s')).toBe(true);
    for (const v of [1000, 5000, 120000]) expect(fmtVolumeDuration(v).endsWith('ms')).toBe(false);
  });
});

describe('volumeUnitLabel — v0.10.268 çubuk birimi', () => {
  it('servis seçiliyken traces, servissiz requests; etiket seriye iner', () => {
    expect(volumeUnitLabel(true)).toBe('traces');
    expect(volumeUnitLabel(false)).toBe('requests');
    const cfg = buildVolumeSeries(mk([1]), null, null, 'requests');
    expect(byKey(cfg, 'total').label).toBe('requests');
    expect(byKey(cfg, 'error').label).toBe('error requests');
  });
});


// v0.10.2477 (operator-reported: "süre çizgisi … bazen net göstermiyor") —
// v0.10.322'nin 5 kovalık yumuşatması kaldırıldı: çizgi HAM kova değeri,
// GAP'ler korunur, yoğun seride nokta yok.
describe('yanıt-süresi çizgisi yumuşatılmaz (v0.10.2477)', () => {
  it('her nokta kendi kovasının değeri; 0 → null GAP; yoğun seride nokta yok', () => {
    const n = 30;
    const mk = (f: (i: number) => number | null): SpanMetricSeries[] => [{ groupKey: [], points: Array.from({ length: n }, (_, i) => ({ time: (1_700_000_000 + i * 120) * 1e9, value: f(i) ?? 0 })) }];
    const cfg = buildVolumeSeries(mk(() => 100), mk(() => 1), mk(i => (i === 10 ? 0 : (i % 2 ? 10 : 2))));
    const rt = cfg.series.find(s0 => s0.key === 'rt')!;
    expect(rt.label).toContain('response time (');
    expect(rt.pointsShow).toBe(false);
    expect(rt.data[10]).toBeNull();
    expect(rt.data[15]).toBe(10);
    expect(rt.data[16]).toBe(2);
  });
});

// v0.10.2477 — ince çizgi ızgarası: çizgi kendi adımında, çubuklar çubuk
// adımına toplanır (sayımlar kayıpsız), tek x ızgarası.
describe('stripBarStep / ince ızgara (v0.10.2477)', () => {
  it('çubuk adımı çizgi adımının en küçük katı (istenen adımı karşılar)', () => {
    expect(stripBarStep(60, 20)).toBe(60);
    expect(stripBarStep(30, 20)).toBe(40);
    expect(stripBarStep(900, 600)).toBe(1200);
    expect(stripBarStep(10, 30)).toBe(30); // çubuk çizgiden ince olamaz
    expect(stripBarStep(120, 0)).toBe(120); // ızgara yok → istenen
  });

  it('çizgi ince adımda, çubuklar toplanır; çubuk dışı indeksler undefined', () => {
    const t0 = 1_700_000_040; // 60 s'ye hizalı
    const fine = (vals: number[]): SpanMetricSeries[] => [{ groupKey: [], points: vals.map((v, i) => ({ time: (t0 + i * 20) * 1e9, value: v })) }];
    // 6 ince kova (20 s) → 2 çubuk (60 s)
    const cfg = buildVolumeSeries(fine([1, 2, 3, 4, 5, 6]), fine([0, 1, 0, 0, 9, 0]), fine([10, 0, 30, 40, 50, 60]), 'traces', 'avg',
      { lineStepSec: 20, barStepSec: 60 });
    expect(cfg.times).toEqual([t0, t0 + 20, t0 + 40, t0 + 60, t0 + 80, t0 + 100]);
    expect(byKey(cfg, 'total').data).toEqual([6, undefined, undefined, 15, undefined, undefined]);
    expect(byKey(cfg, 'error').data).toEqual([1, undefined, undefined, 9, undefined, undefined]);
    expect(byKey(cfg, 'rt').data).toEqual([10, null, 30, 40, 50, 60]); // 0 → GAP, interpolasyon yok
    expect(byKey(cfg, 'rt').label).toContain('(average)');
    expect(cfg.barStepSec).toBe(60);
    expect(cfg.lineStepSec).toBe(20);
  });

  // v0.10.2480 — prod 15 dk görünümü: çubuk = çizgi = 10 sn (eski başlık "1m").
  it('15 dk: çubuk ve çizgi 10 sn, her kova kendi değeri', () => {
    const t0 = 1_700_000_000;
    const fine = (vals: number[]): SpanMetricSeries[] => [{ groupKey: [], points: vals.map((v, i) => ({ time: (t0 + i * 10) * 1e9, value: v })) }];
    const cfg = buildVolumeSeries(fine([5, 6, 7, 8]), null, fine([100, 900, 120, 110]), 'requests', 'p99', { lineStepSec: 10, barStepSec: 10 });
    expect(cfg.barStepSec).toBe(10);
    expect(cfg.lineStepSec).toBe(10);
    expect(byKey(cfg, 'rt').data).toEqual([100, 900, 120, 110]);
    expect(byKey(cfg, 'total').data).toEqual([5, 6, 7, 8]);
  });

  it('trafiksiz ince kova ızgarada null kalır (sıfıra çekilmez)', () => {
    const t0 = 1_700_000_040;
    const pts = (vals: [number, number][]): SpanMetricSeries[] => [{ groupKey: [], points: vals.map(([i, v]) => ({ time: (t0 + i * 20) * 1e9, value: v })) }];
    const cfg = buildVolumeSeries(pts([[0, 5], [4, 5]]), null, pts([[0, 100], [4, 300]]), 'traces', 'avg', { lineStepSec: 20, barStepSec: 60 });
    expect(byKey(cfg, 'rt').data).toEqual([100, null, null, null, 300]);
    expect(byKey(cfg, 'total').data).toEqual([5, undefined, undefined, 5, undefined]);
  });

  // v0.10.2480 (operator-reported: boşluklar üçgenle birleşiyordu) — çizgi
  // serisi SIKI boşluk kipinde: TimeChart köprü (stepGapsRefiner) bağlamaz,
  // yalıtık örneği nokta basar; ileri doldurma yok (önceki değer taşınmaz).
  it('çizgi strictGaps: boş kova köprülenmez, önceki değer taşınmaz', () => {
    const t0 = 1_700_000_000;
    const pts = (vals: [number, number][]): SpanMetricSeries[] => [{ groupKey: [], points: vals.map(([i, v]) => ({ time: (t0 + i * 10) * 1e9, value: v })) }];
    const cfg = buildVolumeSeries(pts([[0, 5], [1, 5], [2, 5], [3, 5]]), null, pts([[0, 100], [2, 300]]), 'requests', 'p99', { lineStepSec: 10, barStepSec: 10 });
    const rt = byKey(cfg, 'rt');
    expect(rt.strictGaps).toBe(true);
    expect(rt.data).toEqual([100, null, 300, null]);
    expect(byKey(cfg, 'total').strictGaps).toBeUndefined();
  });
});

describe('volumeBucketCaption / fmtStepShort (v0.10.2480)', () => {
  it.each([
    [10, 'tr', '10 sn'], [10, 'en', '10s'], [60, 'tr', '1 dk'], [60, 'en', '1m'],
    [90, 'en', '90s'], [1200, 'tr', '20 dk'], [3600, 'en', '1h'], [7200, 'tr', '2 sa'], [86400, 'en', '1d'],
  ] as const)('%i sn (%s) → %s', (sec, lang, want) => {
    expect(fmtStepShort(sec, lang)).toBe(want);
  });

  it('eşit adım: tek boy + "kova"', () => {
    expect(volumeBucketCaption('requests', 10, 10, 'tr')).toBe('istek / 10 sn kova · sürükle = zaman seç');
    expect(volumeBucketCaption('requests', 10, 10, 'en')).toBe('requests / 10s bucket · drag = select time');
  });

  it('farklı adım: iki boy da yazılır', () => {
    expect(volumeBucketCaption('requests', 60, 20, 'tr')).toBe('istek / 1 dk · süre / 20 sn · sürükle = zaman seç');
    expect(volumeBucketCaption('traces', 1200, 600, 'en')).toBe('traces / 20m · duration / 10m · drag = select time');
  });

  it('boş grafik: yalnız sürükle ipucu', () => {
    expect(volumeBucketCaption('spans', 0, 0, 'en')).toBe('drag = select time');
  });
});

// v0.10.323 (operatör, prod): db.statement ~ … filtresinde şerit "No traces
// in view to bucket" derken tablo doluydu. Kapsam kararı saf ve pinli.
describe('stripScope', () => {
  it('giriş span\'ı anahtarları → entry; db./messaging./bilinmeyen → spans; arama → spans', () => {
    expect(stripScope([{ k: 'service.name' }, { k: 'http.route' }, { k: 'status_code' }], '')).toBe('entry');
    expect(stripScope([], '')).toBe('entry');
    expect(stripScope([{ k: 'service.name' }, { k: 'db.statement' }], '')).toBe('spans');
    expect(stripScope([{ k: 'messaging.destination' }], '')).toBe('spans');
    expect(stripScope([{ k: 'custom.attr' }], '')).toBe('spans');
    expect(stripScope([{ k: 'service.name' }], 'timeout')).toBe('spans');
    expect(isEntrySpanKey('HTTP.METHOD')).toBe(true);
    expect(isEntrySpanKey('db.system')).toBe(false);
  });
  // v0.10.730 (operator-reported: name = "INSERT …" → şerit boş) — span ADI
  // her kind'da yaşar (DB istemci span'i "INSERT <tablo>"); giriş span'ine
  // ait olduğunu VARSAYMAK kind kısıtını AND'leyip grafiği boşaltıyordu.
  it('name / span.name giriş anahtarı DEĞİL — şerit eşleşen span\'leri sayar', () => {
    expect(isEntrySpanKey('name')).toBe(false);
    expect(isEntrySpanKey('span.name')).toBe(false);
    expect(stripScope([{ k: 'name' }], '')).toBe('spans');
    expect(stripScope([{ k: 'service.name' }, { k: 'name' }], '')).toBe('spans');
    // service.name (servis kimliği) etkilenmedi.
    expect(isEntrySpanKey('service.name')).toBe(true);
  });
  // v0.10.1115 — op_group (Operations › Normalized pivotu) span ADININ şekli:
  // adla aynı sınıf, giriş span'ine ait değil → kind kısıtı yok, şerit
  // eşleşen span'leri sayar (Operations satırının saydığı küme).
  it('op_group giriş anahtarı DEĞİL — şerit eşleşen span\'leri sayar', () => {
    expect(isEntrySpanKey('op_group')).toBe(false);
    expect(stripScope([{ k: 'op_group' }], '')).toBe('spans');
    expect(stripRootOnly(stripScope([{ k: 'op_group' }], ''), true)).toBe(false);
  });
  // v0.10.1117 — /endpoints şekil pivotu: http.route_shape, http.route ile AYNI
  // sınıf (giriş span'inin rotası; satır inbound span'lerden kuruldu) →
  // şerit giriş kapsamında kalır. name_shape (RPC sekmesi) `name` sınıfı
  // (v0.10.730): kind kısıtı yok, eşleşen span'leri sayar.
  it('http.route_shape giriş anahtarı (http.route gibi); name_shape değil', () => {
    expect(isEntrySpanKey('http.route_shape')).toBe(isEntrySpanKey('http.route'));
    expect(stripScope([{ k: 'service.name' }, { k: 'http.route_shape' }], '')).toBe('entry');
    expect(isEntrySpanKey('name_shape')).toBe(false);
    expect(stripScope([{ k: 'name_shape' }], '')).toBe('spans');
    expect(stripRootOnly(stripScope([{ k: 'name_shape' }], ''), true)).toBe(false);
  });
  // v0.10.1006 (operator-reported, prod: function_code çipinde tablo dolu,
  // şerit boş) — iş kimliklerini giriş span'i taşımıyor (prod'da yalnız
  // log-yayın MQ span'leri); giriş kapsamı kind kısıtını AND'leyip grafiği
  // boşaltıyordu.
  it('channel_code / function_code / function_id giriş anahtarı DEĞİL — şerit eşleşen span\'leri sayar', () => {
    for (const k of ['function_code', 'FUNCTION_CODE', 'channel_code', 'function_id']) {
      expect(isEntrySpanKey(k)).toBe(false);
      expect(stripScope([{ k }], '')).toBe('spans');
    }
    expect(stripScope([{ k: 'service.name' }, { k: 'function_code' }], '')).toBe('spans');
    expect(volumeUnitFor(true, stripScope([{ k: 'function_code' }], ''))).toBe('spans');
    // Giriş span'inde yaşayanlar etkilenmedi.
    expect(stripScope([{ k: 'service.name' }, { k: 'http.route' }, { k: 'deployment.environment' }], '')).toBe('entry');
  });
  // v0.10.1008 (operator-reported, prod: function_code çipi + Root → şerit boş)
  // — Root bayrağı kök yüklemini çiple aynı span'de AND'ler; çip giriş
  // span'inde yaşamıyorsa şerit bayrağı göndermez.
  it('Root bayrağı yalnız giriş kapsamında şeride gider', () => {
    expect(stripRootOnly('entry', true)).toBe(true);
    expect(stripRootOnly('entry', false)).toBe(false);
    expect(stripRootOnly('spans', true)).toBe(false);
    expect(stripRootOnly(stripScope([{ k: 'function_code' }], ''), true)).toBe(false);
    expect(stripRootOnly(stripScope([{ k: 'http.route' }], ''), true)).toBe(true);
  });
  // v0.10.1011 (operator-reported, prod: function_code çipi + Errors → liste
  // dolu, şerit "No traces in view") — boş şerit nedenini söyler.
  // v0.10.1082 (operator-reported: "Error seçildiğinde histogram gelmiyor") —
  // cümle yalnız DOĞRU olduğunda: metric-batch yolu (span düzeyi) + liste dolu.
  // Errors şeridi (error-histogram) listeyle aynı kümeyi sayar; orada boş şerit
  // boş liste demektir ve "hata başka span'de" demek yalan olurdu.
  it.each([
    // [birim, Errors, liste dolu, Errors şeridi, cümle?]
    ['spans', true, true, false, true],    // tek meşru hâl (ör. arama + Errors)
    ['spans', true, false, false, false],  // liste de boş → "başka span" iddiası yok
    ['spans', true, true, true, false],    // Errors şeridi: listeyle aynı küme
    ['traces', true, true, true, false],   // trace kipi şeridi
    ['spans', false, true, false, false],  // Errors kapalı
    ['traces', true, true, false, false],  // giriş kapsamı
    ['requests', true, true, false, false],
  ] as const)('birim %s, Errors %s, liste dolu %s, Errors şeridi %s → cümle %s', (unit, hasError, listNonEmpty, errorStrip, want) => {
    const note = volumeEmptyNote(unit, hasError, listNonEmpty, errorStrip);
    if (want) {
      expect(note).toContain("hata aynı trace'in başka bir span'inde");
      expect(note).toContain("Errors'u kaldırın");
    } else {
      expect(note).toBe(VOLUME_EMPTY_DEFAULT);
    }
  });
  it('Errors şeridi: birim ve ipucu sunucu kipinden', () => {
    expect(errorStripUnit('span')).toBe('spans');
    expect(errorStripUnit('trace')).toBe('traces');
    // v0.10.1801 — ④ çip-trace: listenin trace'leri.
    expect(errorStripUnit('chip')).toBe('traces');
    expect(errorStripHint('chip', false)).toContain('trace düzeyinde');
    expect(errorStripHint('chip', true)).toContain('tavana');
    expect(errorStripHint('trace', false)).toContain('başlangıç');
    expect(errorStripHint('span', false)).toContain("hatalı span'ler");
    expect(errorStripHint('trace', true)).toContain('tavana');
    expect(errorStripHint('span', false)).not.toContain('tavana');
    // v0.10.1101 — çipsiz Errors: tüm hatalı span'ler (giriş + istemci + iç).
    expect(errorStripHint('span', false, true)).toContain('istemci');
    expect(errorStripHint('span', false, true)).not.toContain('çip');
  });
  // v0.10.1101 — çipsiz Errors (yalnız / servis / ortam / küme): başlık sayıları
  // (SPANS · ERROR SPANS · ERR RATE · <stat> AVG) şeridin KENDİ cevabından —
  // error-histogram'ın count / errors / rt'si; eskiden metric-batch giriş
  // span'lerini sayıyordu ve sayılar satırlarla tutmuyordu.
  it('stripHeaderStats — error-histogram cevabından: toplam = hatalı span, oran %100, rt ağırlıklı', () => {
    const resp = { count: mk([3, 0, 1]), errors: mk([3, 0, 1]), rt: mk([10, 0, 50]) };
    expect(stripHeaderStats(resp)).toEqual({ total: 4, err: 4, errRate: 100, rtAvg: 20 });
  });
  it('stripHeaderStats — metric-batch cevabı (Errors kapalı): oran hatalı / toplam', () => {
    expect(stripHeaderStats({ count: mk([90, 10]), errors: mk([1, 1]), rt: mk([5, 5]) }))
      .toEqual({ total: 100, err: 2, errRate: 2, rtAvg: 5 });
  });
  it('stripHeaderStats — cevap yok / boş seri → sıfırlar (NaN yok)', () => {
    expect(stripHeaderStats(null)).toEqual({ total: 0, err: 0, errRate: 0, rtAvg: 0 });
    expect(stripHeaderStats({ count: [], errors: [], rt: [] })).toEqual({ total: 0, err: 0, errRate: 0, rtAvg: 0 });
  });
  it('birim ve ipucu kapsamı söyler', () => {
    expect(volumeUnitFor(true, 'entry')).toBe('traces');
    expect(volumeUnitFor(false, 'entry')).toBe('requests');
    expect(volumeUnitFor(true, 'spans')).toBe('spans');
    expect(volumeHint('spans')).toContain('SPAN');
    expect(volumeHint('traces')).toContain('Giriş span');
  });
});

// v0.10.513 (operatör: "Median yerine p95 olsun default") — üçüncü seri
// seçilebilir istatistik; etiket seçimi söyler. Varsayılan avg (v0.10.2477).
describe('şerit istatistiği (v0.10.513)', () => {
  const mk = (v: number): SpanMetricSeries[] => [{ groupKey: [], points: [1, 2, 3].map(i => ({ time: (1_700_000_000 + i * 120) * 1e9, value: v })) }];
  it('varsayılan etiket average (v0.10.2477; önce p99 v0.10.1524, median v0.10.725); p50 median, p95 adıyla', () => {
    expect(buildVolumeSeries(mk(10), mk(1), mk(42)).series.find(s0 => s0.key === 'rt')!.label).toContain('(average)');
    expect(buildVolumeSeries(mk(10), mk(1), mk(42), 'traces', 'p95').series.find(s0 => s0.key === 'rt')!.label).toContain('(p95)');
    expect(buildVolumeSeries(mk(10), mk(1), mk(42), 'traces', 'p50').series.find(s0 => s0.key === 'rt')!.label).toContain('(median)');
    expect(buildVolumeSeries(mk(10), mk(1), mk(42), 'traces', 'p99').series.find(s0 => s0.key === 'rt')!.label).toContain('(p99)');
  });
});

describe('weightedStatAvg (v0.10.660 — başlık: aralığın istek-ağırlıklı ortalaması)', () => {
  const ser = (vals: (number | null)[]) => [{ groupKey: [] as string[], points: vals.map((v, i) => ({ time: i * 60, value: v as number })) }];
  it('istek sayısıyla ağırlıklandırır: 3 isteklik kova 3000 isteklik kovayı eğmez', () => {
    // kova0: 3 istek × 1000ms, kova1: 3000 istek × 100ms → ≈100.9ms (düz ortalama 550 olurdu)
    expect(weightedStatAvg(ser([3, 3000]), ser([1000, 100]))).toBeCloseTo((3 * 1000 + 3000 * 100) / 3003, 3);
  });
  it('istatistiği olmayan kova ve 0 istekli kova ağırlıksız', () => {
    expect(weightedStatAvg(ser([10, 0, 10]), ser([100, 900, null]))).toBe(100);
  });
  it('istek yoksa 0 (bölme yok); null seriler → 0', () => {
    expect(weightedStatAvg(ser([0, 0]), ser([5, 5]))).toBe(0);
    expect(weightedStatAvg(null, null)).toBe(0);
  });
});

// v0.10.738 — veri pencerenin küçük bir kısmına sıkışmışsa "veriye sığdır".
describe('dataExtent', () => {
  const win = { from: T0 / S, to: T0 / S + 24 * 3600 }; // 24 saat
  const series = (vals: number[], stepSec = 600) => [{ groupKey: [] as string[], points: vals.map((v, i) => ({ time: T0 + i * stepSec * S, value: v })) }];
  it('35 dk patlama / 24 sa pencere → aralık + %10 pay, en az bir kova', () => {
    // 144 kova (10 dk); yalnız 60-63. kovalar dolu (10:00-10:40 gibi)
    const vals = Array.from({ length: 144 }, (_, i) => (i >= 60 && i <= 63 ? 5 : 0));
    const e = dataExtent(series(vals), win.from, win.to)!;
    expect(e).not.toBeNull();
    const first = T0 / S + 60 * 600, last = T0 / S + 63 * 600 + 600;
    expect(e.fraction).toBeCloseTo((last - first) / (24 * 3600), 6);
    expect(e.fromSec).toBeCloseTo(first - 600, 6); // pay = max(step, %10) = 600
    expect(e.toSec).toBeCloseTo(last + 600, 6);
  });
  it('veri pencerenin ≥ %25\'ini kaplıyorsa null (sığdıracak şey yok)', () => {
    const vals = Array.from({ length: 144 }, (_, i) => (i >= 20 && i <= 120 ? 1 : 0));
    expect(dataExtent(series(vals), win.from, win.to)).toBeNull();
  });
  it('hiç sıfır olmayan kova yoksa / boşsa null; pay pencereyi aşmaz', () => {
    expect(dataExtent(series([0, 0, 0]), win.from, win.to)).toBeNull();
    expect(dataExtent(null, win.from, win.to)).toBeNull();
    const e = dataExtent(series([3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]), win.from, win.to)!;
    expect(e.fromSec).toBe(win.from); // sol pay pencere başına kelepçeli
  });
});
