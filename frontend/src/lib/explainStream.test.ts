import { describe, it, expect, afterEach, vi } from 'vitest';
import { api, explainStepFrame } from './api';

// v0.9.1127 (AI Faz 1.5) — akan ✨ Explain'in İSTEMCİ sözleşmesi.
//
// Bu dosya `api.copilotExplain*` uçlarını GERÇEK çağrı yolundan test
// ediyor (fetch mock'lu), iç yardımcıyı değil: sözleşmenin kendisi
// "onDelta verirsen akar, vermezsen bugünkü gövdeyi alırsın".
//
// Üç geri düşüş yolu da burada pinli, çünkü üçü de SESSİZ olmak zorunda:
// operatör cevabını alır, taşımanın hangi yoldan geldiğini bilmez.
// Sessiz bir yol test edilmezse bozulduğunda da sessiz kalır.

interface FetchCall { url: string; init: RequestInit }
const calls: FetchCall[] = [];

function mockFetch(res: () => Response) {
  vi.stubGlobal('fetch', (url: string, init: RequestInit = {}) => {
    calls.push({ url, init });
    if (init.signal?.aborted) return Promise.reject(new DOMException('aborted', 'AbortError'));
    return Promise.resolve(res());
  });
}

function sseResponse(body: string): Response {
  const enc = new TextEncoder();
  return new Response(
    new ReadableStream({ start(c) { c.enqueue(enc.encode(body)); c.close(); } }),
    { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
  );
}

function jsonResponse(obj: unknown, status = 200): Response {
  return new Response(JSON.stringify(obj), {
    status, headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  calls.length = 0;
  vi.unstubAllGlobals();
});

describe('akan ✨ Explain — SSE yolu', () => {
  it('delta çerçeveleri SIRAYLA akar, cevap answer çerçevesinden gelir', async () => {
    mockFetch(() => sseResponse(
      'event: delta\ndata: {"text":"Kök "}\n\n' +
      'event: delta\ndata: {"text":"neden: "}\n\n' +
      'event: delta\ndata: {"text":"redis."}\n\n' +
      'event: answer\ndata: {"text":"Kök neden: redis.","exchangeId":"x9"}\n\n' +
      'event: done\ndata: {"ok":true}\n\n',
    ));
    const seen: string[] = [];
    const r = await api.copilotExplainProblem('p1', { onDelta: d => seen.push(d) });

    expect(seen).toEqual(['Kök ', 'neden: ', 'redis.']);
    expect(r.explanation).toBe('Kök neden: redis.');
    expect(r.exchangeId).toBe('x9');
    expect(calls[0].url).toContain('stream=1');
  });

  it('answer çerçevesi delta toplamından FARKLI olabilir — kazanan answer', async () => {
    // Model reasoning üretip cevabı sonda toparladığında sunucu düşünce
    // bloğunu akıtmaz, kurtarılmış cevabı tek parça yollar.
    mockFetch(() => sseResponse(
      'event: delta\ndata: {"text":"yarım"}\n\n' +
      'event: answer\ndata: {"text":"tam ve nihai cevap"}\n\n' +
      'event: done\ndata: {"ok":true}\n\n',
    ));
    const r = await api.copilotExplainProblem('p1', { onDelta: () => {} });
    expect(r.explanation).toBe('tam ve nihai cevap');
  });

  it('handler ekstra alanları answer çerçevesinden çözülür', async () => {
    mockFetch(() => sseResponse(
      'event: answer\ndata: {"text":"t","exchangeId":"x1","evidenceSpanIds":["s1","s2"]}\n\n' +
      'event: done\ndata: {"ok":true}\n\n',
    ));
    const r = await api.copilotExplainTrace('t1', false, { onDelta: () => {} });
    expect(r.evidenceSpanIds).toEqual(['s1', 's2']);
    expect(r.exchangeId).toBe('x1');
  });

  it('runbook similarCount akan kipte de gelir', async () => {
    mockFetch(() => sseResponse(
      'event: answer\ndata: {"text":"adımlar","exchangeId":"x2","similarCount":4}\n\n' +
      'event: done\ndata: {"ok":true}\n\n',
    ));
    const r = await api.copilotRunbook('p1', { onDelta: () => {} });
    expect(r.similarCount).toBe(4);
  });

  it('sorgu taşıyan path stream=1 bayrağını & ile ekler', async () => {
    mockFetch(() => sseResponse('event: answer\ndata: {"text":"x"}\n\nevent: done\ndata: {"ok":true}\n\n'));
    await api.copilotExplainSpan('trace1', 'span1', { onDelta: () => {} });
    expect(calls[0].url).toContain('?span=span1&stream=1');
  });
});

describe('akan ✨ Explain — sessiz geri düşüşler', () => {
  it('sunucu DÜZ JSON dönerse buffered kabul edilir (StreamText şeffaf geri düşüşü)', async () => {
    // `?stream=1` bir TALEP, garanti değil: akıyamayan uçta sunucu
    // buffered çağrıya düşer ve tam gövdeyi yollar.
    mockFetch(() => jsonResponse({ explanation: 'buffered cevap', exchangeId: 'x3' }));
    const seen: string[] = [];
    const r = await api.copilotExplainProblem('p1', { onDelta: d => seen.push(d) });

    expect(r.explanation).toBe('buffered cevap');
    expect(r.exchangeId).toBe('x3');
    expect(seen).toEqual([]); // sıfır delta — panel cevabı tek seferde çizer
  });

  it('SSE ama SIFIR delta: answer çerçevesi tek başına yeterli', async () => {
    mockFetch(() => sseResponse(
      'event: answer\ndata: {"text":"tek parça","exchangeId":"x4"}\n\n' +
      'event: done\ndata: {"ok":false}\n\n',
    ));
    const seen: string[] = [];
    const r = await api.copilotExplainProblem('p1', { onDelta: d => seen.push(d) });
    expect(r.explanation).toBe('tek parça');
    expect(seen).toEqual([]);
  });

  it('onDelta VERİLMEZSE akan yola hiç girilmez (bayt bayt eski davranış)', async () => {
    mockFetch(() => jsonResponse({ explanation: 'eski yol', exchangeId: 'x5' }));
    const r = await api.copilotExplainProblem('p1');
    expect(r.explanation).toBe('eski yol');
    expect(calls[0].url).not.toContain('stream=1');
  });
});

describe('akan ✨ Explain — hata ve iptal', () => {
  it('error çerçevesi Error olarak fırlar', async () => {
    mockFetch(() => sseResponse(
      'event: delta\ndata: {"text":"yarı"}\n\n' +
      'event: error\ndata: {"error":"model kotası doldu"}\n\n' +
      'event: done\ndata: {"ok":false}\n\n',
    ));
    await expect(api.copilotExplainProblem('p1', { onDelta: () => {} }))
      .rejects.toThrow('model kotası doldu');
  });

  it('cevapsız kapanan akış sessizce ÇÖZÜLMEZ', async () => {
    // Boş panel, hata mesajı olmadan: v0.9.1127 öncesi bu sınıfın en
    // sinsi hâli. Akış cevapsız kapanırsa çağıran bunu bilmeli.
    mockFetch(() => sseResponse('event: delta\ndata: {"text":"yarı"}\n\n'));
    await expect(api.copilotExplainProblem('p1', { onDelta: () => {} })).rejects.toThrow();
  });

  it('HTTP hatası akan kipte de Error (SSE içine gizlenmez)', async () => {
    mockFetch(() => jsonResponse({ error: 'yapılandırılmamış' }, 503));
    await expect(api.copilotExplainProblem('p1', { onDelta: () => {} })).rejects.toThrow(/503/);
  });

  it('iptal edilen signal isteği başlatmaz', async () => {
    mockFetch(() => sseResponse('event: answer\ndata: {"text":"x"}\n\n'));
    const ac = new AbortController();
    ac.abort();
    await expect(api.copilotExplainProblem('p1', { onDelta: () => {}, signal: ac.signal }))
      .rejects.toThrow();
  });

  it('signal fetch\'e GEÇER — aksi halde "Yeniden sor" eskisini kesemez', async () => {
    mockFetch(() => sseResponse('event: answer\ndata: {"text":"x"}\n\nevent: done\ndata: {"ok":true}\n\n'));
    const ac = new AbortController();
    await api.copilotExplainProblem('p1', { onDelta: () => {}, signal: ac.signal });
    expect(calls[0].init.signal).toBe(ac.signal);
  });
});

// ── v0.10.948 (CoSRE Faz B) — trace incelemesinin adım olayları ──────────
//
// Sunucu explain-trace SIRASINDA gerçek okumalar yürütür: `step` çağrıdan
// ÖNCE, `step-result` çağrı BİTİNCE (sohbetle aynı şekil). İstemci sözleşmesi:
// olaylar SIRAYLA onStep'e gider, şekil tek yerde daraltılır (explainStepFrame),
// dinleyen yoksa sessizce geçilir ve cevap yine answer çerçevesinden gelir.
describe('akan ✨ Explain — step / step-result (v0.10.948)', () => {
  const TRACE = '0af7651916cd43dd8448eb211c80319c';
  const body =
    `event: step\ndata: {"i":0,"tool":"get_trace","args":{"trace_id":"${TRACE}"}}\n\n` +
    'event: step-result\ndata: {"i":0,"tool":"get_trace","ok":true,"preview":"{}","truncated":false,"bytes":2,"durationMs":84.5,' +
      '"sources":[{"source":"traces","state":"ok"},{"bozuk":1},{"source":"traces","state":"truncated","flags":["truncated",7]}]}\n\n' +
    'event: step\ndata: {"i":1,"tool":"get_logs_for_trace","args":"{\\"trace_id\\":\\"x\\"}"}\n\n' +
    'event: step-result\ndata: {"i":1,"tool":"get_logs_for_trace","ok":true,"preview":"{}","truncated":false,"bytes":2,"durationMs":6001,' +
      '"sources":[{"source":"logs","state":"timeout","detail":"8s"}]}\n\n' +
    'event: step\ndata: {"tool":"etiket-i-yok"}\n\n' +
    'event: delta\ndata: {"text":"**Bulgu**"}\n\n' +
    'event: answer\ndata: {"text":"**Bulgu** …","exchangeId":"x7","evidenceSpanIds":["b7ad6b7169203331"],' +
      '"links":[{"label":"Trace","href":"/trace?id=' + TRACE + '"}],' +
      '"sources":[{"source":"traces","backend":"clickhouse","state":"ok","returned":12},{"source":"logs","backend":"elasticsearch","state":"timeout"}]}\n\n' +
    'event: done\ndata: {"ok":true}\n\n';

  it('adımlar SIRAYLA onStep\'e; nesne args JSON metnine; bozuk kaynak satırı atlanır; i\'siz çerçeve düşer', async () => {
    mockFetch(() => sseResponse(body));
    const seen: import('./types').ExplainStepEvent[] = [];
    const deltas: string[] = [];
    await api.copilotExplainTrace(TRACE, false, { onDelta: d => deltas.push(d), onStep: e => seen.push(e) });
    expect(seen.map(e => `${e.kind}:${e.i}`)).toEqual(['step:0', 'step-result:0', 'step:1', 'step-result:1']);
    const s0 = seen[0];
    expect(s0.kind === 'step' && s0.args).toBe(`{"trace_id":"${TRACE}"}`);
    const s2 = seen[2];
    expect(s2.kind === 'step' && s2.args).toBe('{"trace_id":"x"}');
    const r0 = seen[1];
    if (r0.kind !== 'step-result') throw new Error('step-result bekleniyordu');
    expect(r0).toMatchObject({ ok: true, durationMs: 84.5, bytes: 2, truncated: false });
    expect(r0.sources).toEqual([{ source: 'traces', state: 'ok' }, { source: 'traces', state: 'truncated', flags: ['truncated'] }]);
    const r1 = seen[3];
    expect(r1.kind === 'step-result' && r1.sources).toEqual([{ source: 'logs', state: 'timeout', detail: '8s' }]);
    expect(deltas).toEqual(['**Bulgu**']);
  });

  it('cevap çerçevesi sources + links + evidenceSpanIds taşır', async () => {
    mockFetch(() => sseResponse(body));
    const r = await api.copilotExplainTrace(TRACE, false, { onDelta: () => {}, onStep: () => {} });
    expect(r.explanation).toBe('**Bulgu** …');
    expect(r.sources?.map(s => `${s.source}:${s.state}`)).toEqual(['traces:ok', 'logs:timeout']);
    expect(r.sources?.[0].returned).toBe(12);
    expect(r.links).toEqual([{ label: 'Trace', href: `/trace?id=${TRACE}` }]);
    expect(r.evidenceSpanIds).toEqual(['b7ad6b7169203331']);
  });

  it('onStep verilmezse adım çerçeveleri sessizce geçilir (öteki Explain\'ler DEĞİŞMEDİ)', async () => {
    mockFetch(() => sseResponse(body));
    const r = await api.copilotExplainTrace(TRACE, false, { onDelta: () => {} });
    expect(r.exchangeId).toBe('x7');
  });

  it('önbellek isabeti: adım YOK, cached etiketi var', async () => {
    mockFetch(() => sseResponse(
      'event: answer\ndata: {"text":"eski cevap","cached":true,"cachedAtMs":1000,"exchangeId":"x1"}\n\n' +
      'event: done\ndata: {"ok":true}\n\n',
    ));
    const seen: unknown[] = [];
    const r = await api.copilotExplainTrace(TRACE, false, { onDelta: () => {}, onStep: e => seen.push(e) });
    expect(seen).toEqual([]);
    expect(r.cached).toBe(true);
  });

  // v0.10.948 — seçili span incelemenin odak servisini belirler (sunucu
  // `?span=<16hex>` okur). Geçersiz ya da verilmeyen span URL'e GİTMEZ (kök).
  it('seçili span ?span= ile gider (küçük harf, stream=1 & ile); geçersiz/boş span gitmez', async () => {
    mockFetch(() => sseResponse('event: answer\ndata: {"text":"t"}\n\nevent: done\ndata: {"ok":true}\n\n'));
    await api.copilotExplainTrace(TRACE, false, { onDelta: () => {} }, 'B7AD6B7169203331');
    expect(calls[0].url).toContain(`/explain-trace/${TRACE}?span=b7ad6b7169203331&stream=1`);
    await api.copilotExplainTrace(TRACE, false, { onDelta: () => {} }, 's1');
    await api.copilotExplainTrace(TRACE, false, { onDelta: () => {} });
    await api.copilotExplainTrace(TRACE, false, { onDelta: () => {}, fresh: true }, 'b7ad6b7169203331');
    expect(calls[1].url).not.toContain('span=');
    expect(calls[2].url).not.toContain('span=');
    expect(calls[2].url).toContain(`/explain-trace/${TRACE}?stream=1`);
    expect(calls[3].url).toContain(`/explain-trace/${TRACE}?span=b7ad6b7169203331&refresh=1&stream=1`);
  });
});

describe('explainStepFrame (v0.10.948) — güven sınırı', () => {
  it('step-result eksik alanlara güvenli varsayılan; skipped bayrağı taşınır', () => {
    expect(explainStepFrame({ kind: 'step-result', i: 2, skipped: true })).toEqual({
      kind: 'step-result', i: 2, tool: '', ok: false, preview: '', truncated: false, bytes: 0,
      href: undefined, durationMs: undefined, skipped: true, sources: undefined,
    });
  });
  it('negatif süre ölçüm değildir; sayı olmayan i ve bilinmeyen tür null', () => {
    expect(explainStepFrame({ kind: 'step-result', i: 1, durationMs: -5 })).toMatchObject({ durationMs: undefined });
    expect(explainStepFrame({ kind: 'step', i: 'x' })).toBeNull();
    expect(explainStepFrame({ kind: 'delta', i: 1 })).toBeNull();
  });
});
