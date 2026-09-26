// toolSteps.test.ts — v0.10.161 (Copilot araç-çağrısı şeffaflık paneli, seçenek A).
// Saf çekirdek: özet satırı sayıları, ToolErrorJSON ayrıştırma, ilk satır,
// görünür satır kesimi, bütçe aşımı algısı. Sözleşme (yargıç must-fix'leri):
//   - toplam süre YALNIZ tüm yürütülen adımların durationMs'i varsa; yoksa null («—»)
//   - guided rozeti step.origin'den, delta'dan DEĞİL
//   - hata sayacı ok=false olan HER sonuç (timeout + tekrar koruması dâhil)
import { describe, it, expect } from 'vitest';
import {
  summarizeSteps, parseToolError, previewFirstLine, visibleRows, isDeadlineError, VISIBLE_ROWS,
  sourceStates, sourceStateTone, SOURCE_STATE_LABELS, stepRunning, isToolName, toolErrorLabel, stateUnknown,
  windowPrefix,
} from './toolSteps';
import type { ChatStepDetail } from '@/lib/types';

const d = (i: number, over: Partial<ChatStepDetail> = {}): ChatStepDetail => ({ i, tool: `t${i}`, ok: true, preview: 'x', ...over });

describe('summarizeSteps', () => {
  it('sayar: araç, hata, toplam süre (hepsi ölçülmüşse)', () => {
    const s = summarizeSteps([d(1, { durationMs: 210 }), d(2, { durationMs: 340, ok: false }), d(3, { durationMs: 20000, ok: false })]);
    expect(s).toMatchObject({ count: 3, errors: 2, totalMs: 20550, guided: false, pending: 0 });
  });
  it('bir adımın süresi yoksa toplam null («—»), bilinmeyen sayılır', () => {
    const s = summarizeSteps([d(1, { durationMs: 210 }), d(2)]);
    expect(s.totalMs).toBeNull();
    expect(s.unknownDuration).toBe(1);
  });
  it('sonucu gelmemiş adım pending sayılır ve hataya girmez', () => {
    const s = summarizeSteps([d(1, { durationMs: 5 }), { i: 2, tool: 'search_traces' }]);
    expect(s.pending).toBe(1);
    expect(s.errors).toBe(0);
    expect(s.totalMs).toBeNull();
  });
  it('tur bittiyse kanıtsız adım «sürüyor» DEĞİL «kanıt yok» (sunucu boş metinde step-result yayınlamaz)', () => {
    const s = summarizeSteps([d(1, { durationMs: 5 }), { i: 2, tool: 'search_traces' }], true);
    expect(s.pending).toBe(0);
    expect(s.noEvidence).toBe(1);
    expect(s.totalMs).toBeNull();
  });
  it('guided = TÜM adımlar origin=guided (karışık → false)', () => {
    expect(summarizeSteps([d(1, { origin: 'guided' }), d(2, { origin: 'guided' })]).guided).toBe(true);
    expect(summarizeSteps([d(1, { origin: 'guided' }), d(2)]).guided).toBe(false);
    expect(summarizeSteps([]).guided).toBe(false);
  });
});

describe('parseToolError', () => {
  it('ToolErrorJSON şeklini ayrıştırır', () => {
    const p = parseToolError('{"error":"timeout","retryable":true,"hint":"pencereyi daralt","detail":"code: 159"}');
    expect(p).toEqual({ cls: 'timeout', retryable: true, hint: 'pencereyi daralt', detail: 'code: 159' });
  });
  it('JSON değilse ya da error alanı yoksa null', () => {
    expect(parseToolError('error: upstream 502')).toBeNull();
    expect(parseToolError('{"ok":false}')).toBeNull();
    expect(parseToolError('')).toBeNull();
  });
});

describe('previewFirstLine + visibleRows + deadline', () => {
  it('ilk satır, kırpılmış, boşsa «(boş)»', () => {
    expect(previewFirstLine('{"services":[1]}\nsecond', 8)).toBe('{"servic…');
    expect(previewFirstLine('', 20)).toBe('(boş)');
    expect(previewFirstLine(undefined, 20)).toBe('');
  });
  it('kapalıyken ilk 5, açıkken hepsi', () => {
    const rows = Array.from({ length: 12 }, (_, i) => d(i + 1));
    expect(visibleRows(rows, false).length).toBe(VISIBLE_ROWS);
    expect(visibleRows(rows, true).length).toBe(12);
    expect(visibleRows(rows.slice(0, 3), false).length).toBe(3);
  });
  it('bütçe aşımı metni (chatDeadlineMessageTR) algılanır, öteki hatalar değil', () => {
    expect(isDeadlineError('Bu alışveriş 3 dakika tavanına dayandı ve durduruldu.')).toBe(true);
    expect(isDeadlineError('openai-compat call: connection refused')).toBe(false);
    expect(isDeadlineError(undefined)).toBe(false);
  });
});

// v0.10.172 — «niyet» rozeti yalnız sınıflandırma dispatch ettiyse (intent + guided adımları); none/hata sonrası serbest döngü → rozet yok.
describe('summarizeSteps intent rozeti', () => {
  const st = (origin: string | undefined, preview = 'x', durationMs = 5) => ({ i: 1, tool: 't', args: '', preview, ok: true, truncated: false, bytes: 1, durationMs, origin });
  it('intent + guided → intent=true', () => {
    expect(summarizeSteps([st('intent'), st('guided'), st('guided')], true).intent).toBe(true);
  });
  it('intent + araç döngüsü adımı → intent=false; yalnız guided → false', () => {
    expect(summarizeSteps([st('intent'), st(undefined)], true).intent).toBe(false);
    expect(summarizeSteps([st('guided')], true).intent).toBe(false);
  });
});

// ── v0.10.944 (CoSRE Faz A) — dürüst ilerleme ──────────────────────────────
// Kaynak durumu rozetleri (internal/sourcestate sözlüğü), yürütülmeyen çağrı,
// "çalışıyor…" yalnız gerçek araç adında ve sonuç gelene dek, `unauthorized`
// hata sınıfı. Kırpılmış önizlemede yalnız source nesnesinin KENDİSİ okunur.
describe('sourceStates (v0.10.944)', () => {
  it('tek kaynak: ok rozet almaz, empty → «boş»', () => {
    expect(sourceStates('{"logs":[],"source":{"source":"logs","backend":"elasticsearch","state":"ok","returned":3}}')).toEqual([]);
    expect(sourceStates('{"logs":[],"source":{"source":"logs","state":"empty","returned":0}}'))
      .toEqual([{ source: 'logs', state: 'empty', label: 'boş' }]);
  });
  it('çok kaynak: her kaynağın durumu ayrı; ayrıntı taşınır', () => {
    const v = sourceStates(JSON.stringify({
      sources: [
        { source: 'traces', state: 'ok', returned: 10 },
        { source: 'metrics', state: 'unauthorized', returned: 0, detail: 'HTTP 403' },
        { source: 'logs', state: 'timeout', returned: 0 },
      ],
    }));
    expect(v.map(x => x.label)).toEqual(['yetki yok', 'zaman aşımı']);
    expect(v[0].detail).toBe('HTTP 403');
  });
  it('yedi durum etiketi (+ yapılandırılmamış/hata) sözlükte', () => {
    for (const [st, lbl] of [['unreachable', 'erişilemedi'], ['partial', 'kısmi'], ['delayed', 'gecikmeli'], ['truncated', 'limitli']]) {
      expect(sourceStates(`{"source":{"source":"logs","state":"${st}"}}`)[0]?.label).toBe(lbl);
    }
    expect(SOURCE_STATE_LABELS.not_configured).toBe('yapılandırılmamış');
    expect(sourceStateTone('unreachable')).toBe('err');
    expect(sourceStateTone('partial')).toBe('warn');
    expect(sourceStateTone('empty')).toBe('gray');
  });
  // v0.10.944 — fikstür Go map anahtar sırasında (alfabetik): eskisi `source`u
  // `series`ten ÖNCE koyuyordu, gerçek çıktıda hiç olmayan bir sıra.
  it('KIRPILMIŞ önizleme: source nesnesi okunur; başka alandaki "state" (pod) okunmaz', () => {
    const cut = '{"pods":[{"name":"checkout-0","state":"timeout"}],"source":{"source":"metrics","state":"partial","returned":2},"window":{"from_iso":"2026-09-2';
    expect(sourceStates(cut)).toEqual([{ source: 'metrics', state: 'partial', label: 'kısmi' }]);
    expect(sourceStates('{"pods":[{"name":"checkout-0","state":"timeout"}],"sou')).toEqual([]);
  });
  it('ikincil flags ayrı rozet: birincil tekrarlanmaz, ok/tanınmayan bayrak düşer', () => {
    const v = sourceStates('{"source":{"source":"logs","state":"partial","flags":["partial","truncated","ok","weird","truncated"],"returned":200}}');
    expect(v.map(x => x.label)).toEqual(['kısmi', 'limitli']);
    expect(v.every(x => x.source === 'logs')).toBe(true);
  });
  it('JSON değil / tanınmayan durum → boş liste (uydurma yok)', () => {
    expect(sourceStates('error: upstream 502')).toEqual([]);
    expect(sourceStates('{"source":{"source":"logs","state":"weird"}}')).toEqual([]);
    expect(sourceStates('{"source":"clickhouse"}')).toEqual([]);
    expect(sourceStates(undefined)).toEqual([]);
  });
});

describe('yürütülmeyen çağrı + unauthorized (v0.10.944)', () => {
  it('skipped hata sayılmaz, süresi ölçüm sayılmaz; Σ yalnız yürütülenler', () => {
    const s = summarizeSteps([d(1, { durationMs: 40 }), d(2, { ok: false, skipped: true, durationMs: 0, preview: 'tekrar koruması' })], true);
    expect(s).toMatchObject({ count: 2, errors: 0, skipped: 1, totalMs: 40 });
    expect(summarizeSteps([d(1, { skipped: true, ok: false, durationMs: 0 })], true).totalMs).toBeNull();
  });
  it('unauthorized sınıfı ayrıştırılır ve Türkçe etiketi var; tanınmayan sınıf ham kalır', () => {
    expect(parseToolError('{"error":"unauthorized","retryable":false,"hint":"diğer kaynaklarla devam et"}')?.cls).toBe('unauthorized');
    expect(toolErrorLabel('unauthorized')).toBe('yetki yok');
    expect(toolErrorLabel('backend_unavailable')).toBe('kaynak erişilemez');
    expect(toolErrorLabel('yeni_sinif')).toBe('yeni_sinif');
  });
});

describe('stepRunning — "çalışıyor…" (v0.10.944)', () => {
  it('gerçek araç, sonuç yok, tur sürüyor → true; sonuç gelince / tur bitince false', () => {
    expect(stepRunning({ i: 1, tool: 'search_logs' }, false)).toBe(true);
    expect(stepRunning({ i: 1, tool: 'search_logs', preview: '{}' }, false)).toBe(false);
    expect(stepRunning({ i: 1, tool: 'search_logs' }, true)).toBe(false);
  });
  it('bağlam etiketi (araç adı değil) hiç "çalışıyor" demez', () => {
    expect(isToolName('bağlam: ekrandaki trace (abc)')).toBe(false);
    expect(isToolName('get_trace')).toBe(true);
    expect(isToolName('')).toBe(false);
    expect(stepRunning({ i: 1, tool: 'bağlam: ekrandaki trace (abc)' }, false)).toBe(false);
    expect(stepRunning(undefined, false)).toBe(false);
  });
});

// v0.10.944 — Go map çıktısında veri anahtarları `source`tan önce gelir
// (logs < source, series < source, analysis < source, problem/reference <
// sources): veri dolu her sonuç 4 KB önizlemede durumundan ÖNCE kırpılır.
// Rozet önce sunucunun TAM çıktıdan okuduğu `sources`tan; önizleme yedeği
// çok kaynaklı dizide Status'un kendi `notes`/`flags` dizisinde durmaz.
describe('kırpık önizleme + yapısal kaynak durumu (v0.10.944)', () => {
  const logRow = '{"body":"payment declined","service":"payments","severity":"ERROR","span_id":"b7ad6b7169203331","trace_id":"0af7651916cd43dd8448eb211c80319c","ts_iso":"2026-09-21T14:13:20Z"}';
  // search_logs: count < has_more < logs < mapping < match < source — 4 KB logs içinde biter.
  const clippedLogs = ('{"count":50,"has_more":true,"logs":[' + Array.from({ length: 40 }, () => logRow).join(',')).slice(0, 4096);

  it('source\'tan önce kırpılmış gerçekçi sonuç: durum YOK, stateUnknown true', () => {
    expect(clippedLogs.length).toBe(4096);
    expect(clippedLogs).not.toContain('"source"');
    expect(sourceStates(clippedLogs)).toEqual([]);
    expect(stateUnknown({ truncated: true, preview: clippedLogs })).toBe(true);
    // kırpılmamış / yapısal durum gelmiş / düz metin → bilinmiyor DEĞİL
    expect(stateUnknown({ truncated: false, preview: clippedLogs })).toBe(false);
    expect(stateUnknown({ truncated: true, preview: clippedLogs, sources: [{ source: 'logs', state: 'ok' }] })).toBe(false);
    // v0.10.944 — sunucu çıktının tamamını okudu, durum yok: `sources: []` "bilinmiyor" DEĞİL.
    expect(stateUnknown({ truncated: true, preview: clippedLogs, sources: [] })).toBe(false);
    expect(stateUnknown({ truncated: true, preview: 'ts=… level=error msg=' + 'x'.repeat(5000) })).toBe(false);
    // kırpık ama durum önizlemede okunabiliyor → bilinmiyor değil
    expect(stateUnknown({ truncated: true, preview: '{"source":{"source":"logs","state":"partial"},"window":{"fr' })).toBe(false);
  });

  it('çok kaynaklı kırpık dizi: notes/flags dizisindeki "]" taramayı durdurmaz', () => {
    const cut = '{"notes":["Sayılar span\'lerden hesaplanır."],"problem":{"p95_ms":812.5},"reference":{"p95_ms":240.1},'
      + '"sources":[{"source":"traces","backend":"clickhouse","state":"partial","returned":1200,"notes":["env filtresi uygulanamadı"]},'
      + '{"source":"logs","backend":"elasticsearch","state":"unreachable","returned":0,"detail":"dial tcp: connection refused"},'
      + '{"source":"metrics","backend":"victoriametrics","state":"timeo';
    expect(sourceStates(cut).map(x => x.label)).toEqual(['kısmi', 'erişilemedi']);
    expect(sourceStates(cut)[1].detail).toBe('dial tcp: connection refused');
  });

  it('çok kaynaklı dizi kapanınca tarama durur; iç içe nesne atlanmaz, tarama biter', () => {
    const closed = '{"sources":[{"source":"logs","state":"empty"}],"x":{"source":"y","state":"timeout"},"cu';
    expect(sourceStates(closed).map(x => x.label)).toEqual(['boş']);
    const nested = '{"sources":[{"source":"logs","state":"timeout"},{"source":"metrics","extra":{"state":"partial"}},{"source":"traces","state":"delayed"}],"cu';
    expect(sourceStates(nested).map(x => x.label)).toEqual(['zaman aşımı']);
  });

  it('yapısal `sources` önizlemeye ÜSTÜN: önizleme hiç okunmaz', () => {
    const okPreview = '{"source":{"source":"logs","state":"ok","returned":3}}';
    expect(sourceStates(okPreview, [{ source: 'logs', state: 'timeout', detail: 'ES 8 s' }]))
      .toEqual([{ source: 'logs', state: 'timeout', label: 'zaman aşımı', detail: 'ES 8 s' }]);
    const partialPreview = '{"source":{"source":"logs","state":"partial"}}';
    expect(sourceStates(partialPreview, [])).toEqual([]);
    expect(sourceStates(clippedLogs, [{ source: 'logs', state: 'truncated', flags: ['truncated', 'delayed'] }]).map(x => x.label))
      .toEqual(['limitli', 'gecikmeli']);
  });
});

// v0.10.944 — compare_periods iki traces rozeti: pencere detail'den öneke.
describe('windowPrefix', () => {
  it('sorun/referans penceresi (başarı ve fail yolu) önek alır, diğerleri almaz', () => {
    expect(windowPrefix('sorun penceresi')).toBe('sorun · ');
    expect(windowPrefix('referans penceresi: code: 159 timeout')).toBe('referans · ');
    expect(windowPrefix('dial tcp: connection refused')).toBe('');
    expect(windowPrefix(undefined)).toBe('');
  });
});
