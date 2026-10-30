import { describe, expect, it } from 'vitest';
import {
  EVAL_URL_PARAMS, RUN_STATUS_VIEW, activeRun, caseReason, compareCandidates, compareGroups, compareSummary,
  evalHref, expectLines, failingCases, fmtDurationMs, fmtRunTime, fmtSec1, fmtSigned2, fmtSignedInt,
  passDeltas, pickDefaultProfile, pickDefaultRunId, progressPct, progressText, routingHint, runElapsedMs,
  runStatusView, runToolsetView, selectionCaseCount, toggleSurface, toolsetLabel, withEvalParams,
  agentMetaLine, caseBadgeLabel, fmtAvg1, fmtTokensK, tierLabel, trailRows,
} from './aiEval';
import type { EvalAgentTrail, EvalCaseResult, EvalCompare, EvalRunSummary, EvalsetCatalogSurface } from '@/lib/types';

// aiEval.test.ts — v0.10.940 (Settings › CoSRE › Değerlendirme).
//
// KORUNAN SÖZLEŞMELER (hepsi sessiz yanlışa açık — ekranda hata görünmez):
//   • "Fark" yalnız kıyaslanabilir koşular arasında: yarıda kalan (cancelled)
//     ya da başka yüzey seçimli koşu SAHTE bir kırmızı gerileme basmaz.
//   • Başlıktaki "Profil" kataloğun ŞİMDİKİ varsayılanı (defaultProfileId),
//     yoksa yüzey çoğunluğu — en kalabalık yüzeyin (IntentClassify, küçük
//     modele eşlenebilen) profili ya da son koşunun bayat profili değil.
//   • "Fark" vaka sayısı farklı koşular arasında da yok (fikstür seti değişti).
//   • Çip seçimi: boş = tümü; "hepsi seçili" [] 'e döner (iki ayrı hâl yok).
//   • Durum görünümü: renk yalnız sapmada (T9) — done nötr, failed kırmızı.
// Zaman biçimleri TZ=UTC varsayar (repo kapısı `TZ=UTC npx vitest run`).

const surf = (surface: string, cases: number, profileId: string, model = `${profileId}-m`): EvalsetCatalogSurface =>
  ({ surface, cases, profileId, profileLabel: profileId.toUpperCase(), provider: 'openai', model, baseUrl: 'http://llm:11434/v1' });

const run = (id: string, over: Partial<EvalRunSummary> = {}): EvalRunSummary => ({
  id, status: 'done', startedAt: '2026-09-26T18:40:00Z', updatedAt: '2026-09-26T18:50:00Z', finishedAt: '2026-09-26T18:50:00Z',
  startedBy: 'admin@example.com', appVersion: 'v0.10.940', promptVersion: 'p1', model: 'qwen', profileId: 'yerel',
  surfaces: [], total: 51, done: 51, pass: 46, fail: 5, skipped: 0, rubricMean: 0.9, error: '', bySurface: [],
  ...over,
});

const kase = (id: string, over: Partial<EvalCaseResult> = {}): EvalCaseResult => ({
  id, surface: 'Problem', why: '', ok: true, skipped: false, skipReason: '', latencyMs: 1000, fails: [],
  unknownEntities: 0, rubricTotal: 1, profileId: 'yerel', model: 'qwen', input: '', inputTruncated: false,
  answer: '', answerTruncated: false, error: '', expect: {},
  ...over,
});

describe('toggleSurface — çip seçimi', () => {
  const order = ['IntentClassify', 'Problem', 'SLOBurn'];
  const cases: Array<{ name: string; sel: string[]; tap: string; want: string[] }> = [
    { name: 'tümü seçiliyken bir yüzey DARALTIR', sel: [], tap: 'Problem', want: ['Problem'] },
    { name: 'tek seçileni kaldırmak tümüne döner', sel: ['Problem'], tap: 'Problem', want: [] },
    { name: 'ekleme katalog sırasıyla', sel: ['SLOBurn'], tap: 'IntentClassify', want: ['IntentClassify', 'SLOBurn'] },
    { name: 'hepsi seçili olunca [] (Tümü ile tek hâl)', sel: ['IntentClassify', 'Problem'], tap: 'SLOBurn', want: [] },
  ];
  for (const c of cases) it(c.name, () => expect(toggleSurface(c.sel, c.tap, order)).toEqual(c.want));

  it('vaka sayısı: boş seçim = katalog toplamı', () => {
    const s = [surf('IntentClassify', 20, 'kucuk'), surf('Problem', 6, 'yerel'), surf('SLOBurn', 3, 'yerel')];
    expect(selectionCaseCount(s, [])).toBe(29);
    expect(selectionCaseCount(s, ['Problem', 'SLOBurn'])).toBe(9);
  });
});

describe('pickDefaultProfile / routingHint — başlığın profili', () => {
  const surfaces = [surf('IntentClassify', 20, 'kucuk'), surf('Problem', 6, 'yerel'), surf('SLOBurn', 3, 'yerel'), surf('Runbook', 3, 'yerel')];
  const cat = (defaultProfileId: string, s: EvalsetCatalogSurface[] = surfaces) => ({ surfaces: s, defaultProfileId });

  it('kataloğun defaultProfileId\'si kazanır (çoğunluk başka olsa da)', () => {
    // v0.10.940 — operatör varsayılanı küçük modele çevirdi: çoğunluk hâlâ
    // "yerel" yazsa da sunucunun şimdiki varsayılanı başlıkta.
    expect(pickDefaultProfile(cat('kucuk'))?.profileId).toBe('kucuk');
  });
  it('bayat koşu varsayılanı kazanamaz: tek girdi katalog', () => {
    // v0.10.940 — eski imza (surfaces, preferId) son koşunun profileId'sini
    // öne alıyordu; koşu varsayılan değişmeden ÖNCE başladıysa başlık eski
    // profili gösterirdi. İkinci parametre geri gelirse burası kırmızı;
    // uçtan uca hâli AiEvalPanel.contract.test.tsx'te.
    expect(pickDefaultProfile.length).toBe(1);
    expect(pickDefaultProfile(cat('yerel'))?.profileId).toBe('yerel');
  });
  it('defaultProfileId hiçbir yüzeyde yoksa (ya da boşsa) yüzey ÇOĞUNLUĞU', () => {
    expect(pickDefaultProfile(cat('silinmis'))?.profileId).toBe('yerel');
    expect(pickDefaultProfile(cat(''))?.profileId).toBe('yerel');
  });
  it('çoğunluk: en kalabalık yüzey değil, eşitlikte vaka toplamı, sonra katalog sırası', () => {
    expect(pickDefaultProfile(cat(''))?.profileId).not.toBe('kucuk');
    expect(pickDefaultProfile(cat('', [surf('A', 2, 'x'), surf('B', 9, 'y')]))?.profileId).toBe('y');
    expect(pickDefaultProfile(cat('', [surf('A', 3, 'x'), surf('B', 3, 'y')]))?.profileId).toBe('x');
  });
  it('boş / yüklenmemiş katalog → null', () => {
    expect(pickDefaultProfile(cat('yerel', []))).toBeNull();
    expect(pickDefaultProfile(undefined)).toBeNull();
  });
  it('ayrılan yüzeyler tek satırda; ayrılan yoksa boş', () => {
    expect(routingHint(surfaces, 'yerel')).toBe('IntentClassify → KUCUK (kucuk-m)');
    expect(routingHint(surfaces.slice(1), 'yerel')).toBe('');
  });
});

describe('koşu seçimi', () => {
  it('varsayılan: en yeni sürmekte/bitmiş; failed/abandoned atlanır', () => {
    expect(pickDefaultRunId([run('f', { status: 'failed' }), run('a', { status: 'abandoned' }), run('d')])).toBe('d');
    expect(pickDefaultRunId([run('r', { status: 'running' }), run('d')])).toBe('r');
    expect(pickDefaultRunId([run('f', { status: 'failed' })])).toBe('f');
    expect(pickDefaultRunId([])).toBeNull();
  });
  it('activeRun / compareCandidates', () => {
    const runs = [run('r', { status: 'running' }), run('d1'), run('c', { status: 'cancelled' }), run('f', { status: 'failed' })];
    expect(activeRun(runs)?.id).toBe('r');
    expect(activeRun(runs.slice(1))).toBeNull();
    expect(compareCandidates(runs, 'd1').map(r => r.id)).toEqual(['c']);
  });
});

describe('passDeltas — "Fark" sütunu', () => {
  const cases: Array<{ name: string; runs: EvalRunSummary[]; want: Record<string, number | null> }> = [
    { name: 'aynı model + aynı seçim: fark',
      runs: [run('n', { pass: 44 }), run('o', { pass: 46 })],
      want: { n: -2, o: null } },
    { name: 'farklı model atlanır, aynı modelli bir sonraki eskiye bakılır',
      runs: [run('n', { pass: 48 }), run('x', { model: 'gemma', pass: 10 }), run('o', { pass: 46 })],
      want: { n: 2, x: null, o: null } },
    { name: 'cancelled ne kıyaslanır ne taban olur (kısmi sayı)',
      runs: [run('n', { pass: 46 }), run('c', { status: 'cancelled', pass: 9 }), run('o', { pass: 45 })],
      want: { n: 1, c: null, o: null } },
    { name: 'running satırı kıyaslanmaz',
      runs: [run('r', { status: 'running', pass: 3 }), run('o', { pass: 45 })],
      want: { r: null, o: null } },
    { name: 'yüzey seçimi farklıysa kıyas yok (20 vakalık koşu tam koşuya karşı)',
      runs: [run('n', { surfaces: ['IntentClassify'], pass: 19 }), run('o', { pass: 46 })],
      want: { n: null, o: null } },
    // v0.10.940 — aynı seçim, farklı fikstür seti: 48'de 45 ile 51'de 46
    // arasındaki "−1" başka vakaları sayar; daha eskiye de bakılmaz.
    { name: 'vaka sayısı farklıysa (fixture seti değişti) kıyas yok',
      runs: [run('n', { pass: 45, total: 48, done: 48 }), run('o', { pass: 46, total: 51 })],
      want: { n: null, o: null } },
    { name: 'yüzey kümesi sırası önemsiz',
      runs: [run('n', { surfaces: ['B', 'A'], pass: 7 }), run('o', { surfaces: ['A', 'B'], pass: 7 })],
      want: { n: 0, o: null } },
  ];
  for (const c of cases) it(c.name, () => expect(passDeltas(c.runs)).toEqual(c.want));
});

describe('durum görünümü (T9: renk yalnız sapmada)', () => {
  it('done / running / cancelled nötr; failed kırmızı; abandoned amber', () => {
    expect(RUN_STATUS_VIEW.done.tone).toBeUndefined();
    expect(RUN_STATUS_VIEW.running.tone).toBeUndefined();
    expect(RUN_STATUS_VIEW.cancelled.tone).toBeUndefined();
    expect(RUN_STATUS_VIEW.failed.tone).toBe('err');
    expect(RUN_STATUS_VIEW.abandoned.tone).toBe('warn');
    expect(RUN_STATUS_VIEW.done.dot).toContain('status-dot-operational'); // soluk halka, yeşil değil
    expect(RUN_STATUS_VIEW.failed.dot).toContain('status-dot-outage');
    expect(RUN_STATUS_VIEW.abandoned.dot).toContain('status-dot-degraded');
  });
  it('bilinmeyen durum çökertmez: nötr, ham ad', () => {
    expect(runStatusView('queued')).toEqual({ label: 'queued', dot: 'status-dot mr-1.5 inline-block align-middle status-dot-operational' });
    expect(runStatusView('toString').label).toBe('toString'); // prototip anahtarı durum sayılmaz
    expect(runStatusView('done').label).toBe('Bitti');
  });
});

describe('biçimler', () => {
  it.each([
    [0, '0 sn'], [45_000, '45 sn'], [250_000, '4 dk 10 sn'], [3_720_000, '1 sa 2 dk'], [Number.NaN, '0 sn'], [-5, '0 sn'],
  ])('fmtDurationMs(%s) = %s', (ms, want) => expect(fmtDurationMs(ms)).toBe(want));
  it.each([[1400, '1,4'], [0, '0,0'], [12_345, '12,3'], [1_234_567, '1.234,6']])('fmtSec1(%s) = %s', (ms, want) => {
    expect(fmtSec1(ms)).toBe(want);
  });
  it.each([[2, '+2'], [-3, '−3'], [0, '0']])('fmtSignedInt(%s) = %s', (n, want) => expect(fmtSignedInt(n)).toBe(want));
  it.each([[0.02, '+0,02'], [-0.1, '−0,10'], [0, '0,00'], [0.001, '0,00'], [-0.001, '0,00']])('fmtSigned2(%s) = %s', (x, want) => {
    expect(fmtSigned2(x)).toBe(want);
  });
  it('fmtRunTime: dd.mm.yyyy HH:mm; boş/bozuk → —', () => {
    expect(fmtRunTime('2026-09-26T18:40:05Z')).toBe('26.09.2026 18:40');
    expect(fmtRunTime('')).toBe('—');
    expect(fmtRunTime('dün')).toBe('—');
  });
});

describe('ilerleme', () => {
  const start = Date.parse('2026-09-26T18:40:00Z');
  const r = run('r', { status: 'running', done: 32, total: 51, finishedAt: '' });
  it('metin: sayaç · süre · sunucuda sürer', () => {
    expect(progressText(r, start + 250_000))
      .toBe('Koşuyor · 32 / 51 vaka · 4 dk 10 sn · sayfadan ayrılabilirsin, koşu sunucuda sürer');
  });
  it('süre: bitmişse finishedAt, saat kayması eksiye düşmez', () => {
    expect(runElapsedMs(run('d'), 0)).toBe(600_000);
    expect(runElapsedMs(r, start - 5_000)).toBe(0);
  });
  it('yüzde: kelepçeli, total 0 → 0', () => {
    expect(progressPct(r)).toBe(63);
    expect(progressPct({ done: 0, total: 0 })).toBe(0);
    expect(progressPct({ done: 60, total: 51 })).toBe(100);
  });
});

describe('vakalar', () => {
  it('kalan = ne geçen ne atlanan; neden = ilk kalma gerekçesi, yoksa hata', () => {
    const cs = [kase('ok'), kase('skip', { ok: false, skipped: true }), kase('bad', { ok: false, fails: ['forbidden: eskale edin', 'missing: slo'] }),
      kase('err', { ok: false, error: 'dial tcp: refused' })];
    expect(failingCases(cs).map(c => c.id)).toEqual(['bad', 'err']);
    expect(caseReason(cs[2])).toBe('forbidden: eskale edin');
    expect(caseReason(cs[3])).toBe('dial tcp: refused');
  });

  it('beklenti satırları: kaldıranlar önce, ölçü (süre) en sonda', () => {
    expect(expectLines({
      maxLatencyMs: 30_000, knownEntities: ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j'], maxUnknownEntities: 0,
      mustContain: ['SLO'], mustNotContain: ['eskale edin'], intent: 'find_entity', intentService: 'checkout',
      verdicts: ['probable_cause'], minEvidenceCitationRate: 0.8, knownTeams: ['core'],
    })).toEqual([
      'İçermeli: «SLO»',
      'İçermemeli: «eskale edin»',
      'Niyet: find_entity · servis: checkout',
      'Kabul edilen hüküm: probable_cause',
      'Asgari kanıt atıf oranı: 0,80',
      'Uydurma ad üst sınırı: 0',
      'Bilinen varlıklar (10): a, b, c, d, e, f, g, h … (+2)',
      'Bilinen takımlar: core',
      'Süre hedefi: 30,0 sn (ölçü — aşılması vakayı kaldırmaz)',
    ]);
    expect(expectLines({})).toEqual([]);
    expect(expectLines(undefined)).toEqual([]);
  });
});

describe('karşılaştırma', () => {
  const side = (id: string, pass: number) => ({ id, startedAt: '2026-09-26T18:40:00Z', model: 'qwen', promptVersion: 'p', appVersion: `v-${id}`, pass, fail: 51 - pass, total: 51 });
  const cmp: EvalCompare = {
    comparable: true, note: '', base: side('b', 46), head: side('h', 48), rubricMeanDelta: 0.02,
    newlyFailing: [{ id: 'x', surface: 'Problem' }], newlyPassing: [{ id: 'y', surface: 'SLOBurn' }, { id: 'z', surface: 'SLOBurn' }],
    regressed: [], improved: [{ id: 'w', surface: 'Runbook', before: 0.5, after: 0.9 }],
    onlyInBase: [], onlyInHead: ['new-case'],
  };
  it('gruplar: önce bozulan, boşlar düşer, rubrik farkı ayrıntıda', () => {
    const g = compareGroups(cmp);
    expect(g.map(x => x.key)).toEqual(['newlyFailing', 'newlyPassing', 'improved']);
    expect(g[0].tone).toBe('err');
    expect(g[1].tone).toBeUndefined();
    expect(g[2].items[0]).toEqual({ id: 'w', surface: 'Runbook', detail: '0,50 → 0,90' });
  });
  it('null listeler (eski sunucu) çökertmez', () => {
    const bare = { ...cmp, newlyFailing: null, newlyPassing: null, regressed: null, improved: null } as unknown as EvalCompare;
    expect(compareGroups(bare)).toEqual([]);
  });
  it('özet satırı', () => {
    expect(compareSummary(cmp)).toBe(
      '26.09.2026 18:40 · v-b · 46 / 51 → 26.09.2026 18:40 · v-h · 48 / 51 · rubrik ortalaması +0,02 · yalnız seçilide 1 vaka');
  });
  // v0.10.1311 — full ↔ simple kıyası: araç seti tarafın künyesinde.
  it('özet satırı araç setini taşır (biliniyorsa)', () => {
    const ts: EvalCompare = { ...cmp, base: { ...cmp.base, toolset: 'full' }, head: { ...cmp.head, toolset: 'simple' }, onlyInHead: [] };
    expect(compareSummary(ts)).toBe(
      '26.09.2026 18:40 · v-b · tam · 46 / 51 → 26.09.2026 18:40 · v-h · basit · 48 / 51 · rubrik ortalaması +0,02');
  });
});

// v0.10.1316 — ajan kipi: iz satırları, künye, yeni beklenti anahtarları,
// kıyasın araç çağrısı ortalaması. null ortalama "ölçülmedi" (— ), 0 değil.
describe('ajan kipi (v0.10.1316)', () => {
  const trail: EvalAgentTrail = {
    tier: 'loop', rounds: 3, calls: 4, executed: 3, skipped: 1, modelCalls: 3,
    inputTokens: 5200, outputTokens: 900, cachedTokens: 0, capped: false,
    steps: [
      { i: 1, tool: 'list_problems', origin: 'guided', args: '{}', ok: true, skipped: false, ms: 1200, bytes: 2048, preview: '{"problems":[]}' },
      { i: 2, tool: 'compare_periods', args: '{"service":"checkout"}', ok: false, skipped: false, ms: 20000, bytes: 64, preview: 'timeout' },
      { i: 3, tool: 'compare_periods', args: '{"service":"checkout"}', ok: false, skipped: true, ms: 0, bytes: 0, preview: 'skipped' },
    ],
  };
  it('agentMetaLine: kademe · tur · çağrı (atlanan) · token', () => {
    expect(agentMetaLine(trail)).toBe('döngü · 3 tur · 4 çağrı (1 atlandı) · 6,1k token');
    expect(agentMetaLine({ ...trail, tier: 'intent', rounds: 0, calls: 0, skipped: 0, inputTokens: 300, outputTokens: 40, capped: true, deep: true }))
      .toBe('niyet · 0 tur · 0 çağrı · 340 token · bütçe doldu · derin');
  });
  it('v0.10.1476 — bağlanan servis künyede; altyapı hatası rozeti; niyet kabul kümesi ve uzunluk tavanı', () => {
    expect(agentMetaLine({ ...trail, bound: 'checkout → orders-api' })).toBe('döngü · 3 tur · 4 çağrı (1 atlandı) · 6,1k token · servis checkout → orders-api');
    expect(caseBadgeLabel({ ok: false, skipped: true, error: 'openai-compat 500: x', infraError: 'http_5xx' })).toBe('altyapı hatası');
    expect(caseBadgeLabel({ ok: false, skipped: true, error: '' })).toBe('atlandı');
    expect(caseBadgeLabel({ ok: false, skipped: false, error: 'x' })).toBe('hata');
    expect(caseBadgeLabel({ ok: false, skipped: false, error: '' })).toBe('kaldı');
    expect(caseBadgeLabel({ ok: true, skipped: false, error: '' })).toBe('geçti');
    expect(expectLines({ intent: 'none', intentAnyOf: ['off_topic'], maxAnswerRunes: 600 })).toEqual([
      'Niyet: none / off_topic',
      'Cevap uzunluğu üst sınırı: 600 karakter',
    ]);
  });
  it('trailRows: sıra korunur; durum metni; atlanan adımda süre yok; ön-yükleme kökeni', () => {
    const rows = trailRows(trail);
    expect(rows.map(r => [r.n, r.tool, r.status, r.statusLabel, r.origin])).toEqual([
      [1, 'list_problems', 'ok', 'tamam', 'ön-yükleme'],
      [2, 'compare_periods', 'err', 'hata', ''],
      [3, 'compare_periods', 'skipped', 'atlandı', ''],
    ]);
    expect(rows[0].meta).toBe('1.200 ms · 2.048 B');
    expect(rows[2].meta).toBe('');
    expect(new Set(rows.map(r => r.key)).size).toBe(3);
    expect(trailRows(undefined)).toEqual([]);
  });
  it('sayı biçimleri: null → null (soluk —), tr-TR', () => {
    expect(fmtTokensK(6100)).toBe('6,1k');
    expect(fmtTokensK(640)).toBe('640');
    expect(fmtTokensK(null)).toBeNull();
    expect(fmtAvg1(2.25)).toBe('2,3');
    expect(fmtAvg1(undefined)).toBeNull();
    expect(tierLabel('loop')).toBe('döngü');
    expect(tierLabel('yeni-kademe')).toBe('yeni-kademe');
  });
  it('expectLines: ajan anahtarları kaldıran koşullarla, token hedefi ölçü olarak en sonda', () => {
    expect(expectLines({
      mustContain: ['checkout'], anyOfTools: ['query_logs', 'search_logs'], expectedTools: ['list_problems'],
      forbiddenTools: ['read_source_code'], maxToolCalls: 6, expectTier: 'loop', maxTokens: 60000, maxLatencyMs: 180000,
    })).toEqual([
      'İçermeli: «checkout»',
      'Koşması gereken araçlar: list_problems',
      'Araçlardan en az biri: query_logs / search_logs',
      'Denenmemesi gereken araçlar: read_source_code',
      'Araç çağrısı üst sınırı: 6',
      'Beklenen kademe: döngü (loop)',
      'Süre hedefi: 180,0 sn (ölçü — aşılması vakayı kaldırmaz)',
      'Token hedefi: 60.000 (ölçü — aşılması vakayı kaldırmaz)',
    ]);
  });
  it('compareSummary: iki tarafta da ajan ortalaması varsa araç çağrısı değişimi', () => {
    const side = (id: string, mean: number | null) => ({ id, startedAt: '2026-09-26T18:40:00Z', model: 'qwen', promptVersion: 'p', appVersion: `v-${id}`, pass: 5, fail: 1, total: 6, toolCallsMean: mean, tokensMean: null });
    const base: EvalCompare = { comparable: true, note: '', base: side('b', 2), head: side('h', 3.5), rubricMeanDelta: 0,
      newlyFailing: [], newlyPassing: [], regressed: [], improved: [], onlyInBase: [], onlyInHead: [] };
    expect(compareSummary(base)).toBe('26.09.2026 18:40 · v-b · 5 / 6 → 26.09.2026 18:40 · v-h · 5 / 6 · rubrik ortalaması 0,00 · araç çağrısı ort. 2,0 → 3,5');
    expect(compareSummary({ ...base, base: side('b', null) })).not.toContain('araç çağrısı');
  });
});

// v0.10.1311 — araç seti etiketi (profil paneliyle aynı sözcükler).
describe('araç seti', () => {
  it('toolsetLabel: simple/full okunur, boş/yok null', () => {
    expect(toolsetLabel('simple')).toBe('basit');
    expect(toolsetLabel('full')).toBe('tam');
    expect(toolsetLabel('')).toBeNull();
    expect(toolsetLabel(undefined)).toBeNull();
  });
  it('runToolsetView: açık seçim eki, otomatik eksiz, uygulanmamış null', () => {
    expect(runToolsetView({ toolset: 'simple', toolsetPinned: true }).text).toBe('basit (açık)');
    expect(runToolsetView({ toolset: 'full', toolsetPinned: false }).text).toBe('tam');
    expect(runToolsetView({ toolset: '' }).text).toBeNull();
    expect(runToolsetView({}).text).toBeNull();
  });
});

describe('URL yardımcıları', () => {
  it('yabancı parametreler korunur, null siler', () => {
    const prev = new URLSearchParams('tab=eval&s_settings-ai-eval-runs=pass.desc&case=old');
    expect(withEvalParams(prev, { run: 'ev-1', case: null }).toString())
      .toBe('tab=eval&s_settings-ai-eval-runs=pass.desc&run=ev-1');
    expect(prev.get('case')).toBe('old'); // girdi kopyalanır, değiştirilmez
    expect(evalHref('/settings/ai', prev, { case: 'c1' })).toBe('/settings/ai?tab=eval&s_settings-ai-eval-runs=pass.desc&case=c1');
    expect(evalHref('/settings/ai', new URLSearchParams(), { case: null })).toBe('/settings/ai');
  });
  it('sekmenin sahip olduğu parametreler (AiTab çıkışta siler)', () => {
    expect([...EVAL_URL_PARAMS]).toEqual(['run', 'case', 'cmp']);
  });
});
