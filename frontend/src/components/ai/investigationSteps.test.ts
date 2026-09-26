// investigationSteps.test.ts — v0.10.948 (CoSRE Faz B) sözleşmesi
// (investigationSteps.ts başlığı). "CoSRE'ye sor" ilerlemesi YALNIZ sunucu
// olaylarından; dipnot `ok` dahil her kaynağı; kanıt linki yalnız `id`siz ve
// göreli/http(s). Adlar sentetik (checkout, payments; env prod/uat).
import { describe, it, expect } from 'vitest';
import {
  applyExplainStep, explainStepRows, summarizeExplainSteps, sourceFooterItems,
  missingSources, evidenceLinks, isMissingState, isInternalHref,
} from './investigationSteps';
import type { ChatStepDetail, ExplainStepEvent } from '@/lib/types';

const step = (i: number, tool = `t${i}`, args?: string): ExplainStepEvent => ({ kind: 'step', i, tool, args });
const result = (i: number, over: Partial<Extract<ExplainStepEvent, { kind: 'step-result' }>> = {}): ExplainStepEvent =>
  ({ kind: 'step-result', i, tool: `t${i}`, ok: true, preview: '{"source":{"source":"traces","state":"ok"}}', truncated: false, bytes: 40, durationMs: 12, ...over });

const fold = (evs: ExplainStepEvent[]): ChatStepDetail[] => evs.reduce<ChatStepDetail[]>((acc, e) => applyExplainStep(acc, e), []);

describe('applyExplainStep', () => {
  it('step önce satır açar (sonuçsuz), step-result aynı i\'ye yazar', () => {
    let s = applyExplainStep([], step(0, 'get_trace', '{"trace_id":"0af7"}'));
    expect(s).toEqual([{ i: 0, tool: 'get_trace', args: '{"trace_id":"0af7"}', origin: undefined }]);
    expect(s[0].preview).toBeUndefined();
    s = applyExplainStep(s, result(0, { tool: 'get_trace', durationMs: 85 }));
    expect(s[0]).toMatchObject({ i: 0, tool: 'get_trace', ok: true, durationMs: 85 });
    expect(s[0].preview).toContain('traces');
  });
  it('etiket adımı (tool yok) ve aynı i\'nin ikinci step\'i satır açmaz', () => {
    const s = fold([{ kind: 'step', i: 3, label: 'bağlam: trace' }, step(1), step(1)]);
    expect(s.map(d => d.i)).toEqual([1]);
  });
  it('step\'siz step-result da satır olur (sonuç = koştu kanıtı); tool\'suz olan düşer', () => {
    expect(fold([result(4, { tool: 'list_deploys' })]).map(d => d.tool)).toEqual(['list_deploys']);
    expect(fold([result(5, { tool: '' })])).toEqual([]);
  });
  it('yürütülmeyen çağrının süresi ölçüm değildir, yazılmaz', () => {
    const s = fold([step(2), result(2, { skipped: true, ok: false, durationMs: 0 })]);
    expect(s[0].skipped).toBe(true);
    expect(s[0].durationMs).toBeUndefined();
  });
  it('sources aynen taşınır (rozet önizlemeden değil bundan)', () => {
    const s = fold([step(1), result(1, { sources: [{ source: 'logs', state: 'unreachable', detail: 'dial tcp: refused' }] })]);
    expect(s[0].sources).toEqual([{ source: 'logs', state: 'unreachable', detail: 'dial tcp: refused' }]);
  });
  it('girdiyi DEĞİŞTİRMEZ (saf)', () => {
    const prev: ChatStepDetail[] = [{ i: 1, tool: 't1' }];
    applyExplainStep(prev, result(1));
    expect(prev).toEqual([{ i: 1, tool: 't1' }]);
  });
});

describe('explainStepRows', () => {
  it('canlıyken sonucu gelmeyen «running», bittikten sonra «no-result»', () => {
    const s = fold([step(1)]);
    expect(explainStepRows(s, true)[0].status).toBe('running');
    expect(explainStepRows(s, false)[0].status).toBe('no-result');
  });
  it('ok + kaynak durumu rozetleri (ok olan kaynak rozet almaz)', () => {
    const s = fold([step(1, 'compare_periods'), result(1, { sources: [
      { source: 'traces', state: 'ok' },
      { source: 'traces', state: 'partial', detail: 'referans penceresi' },
    ] })]);
    const [r] = explainStepRows(s, false);
    expect(r.status).toBe('ok');
    expect(r.states.map(x => x.label)).toEqual(['kısmi']);
    expect(r.durationMs).toBe(12);
  });
  it('hata sınıfı Türkçe etiketle (unauthorized → yetki yok)', () => {
    const s = fold([step(1, 'query_metric'), result(1, { ok: false, preview: '{"error":"unauthorized","retryable":false}' })]);
    expect(explainStepRows(s, false)[0]).toMatchObject({ status: 'error', errorLabel: 'yetki yok' });
  });
  it('JSON olmayan hata metni: genel «hata»', () => {
    const s = fold([step(1), result(1, { ok: false, preview: 'boom' })]);
    expect(explainStepRows(s, false)[0].errorLabel).toBe('hata');
  });
  it('yürütülmeyen: skipped, süre yok', () => {
    const s = fold([step(1), result(1, { skipped: true, ok: false })]);
    const [r] = explainStepRows(s, false);
    expect(r.status).toBe('skipped');
    expect(r.durationMs).toBeUndefined();
  });
  it('kırpık JSON, yapısal durum yok → durum okunamadı (nötr ok yalan olurdu)', () => {
    const s = fold([step(1), result(1, { preview: '{"logs":[{"body":"x"', truncated: true, sources: undefined })]);
    expect(explainStepRows(s, false)[0]).toMatchObject({ status: 'ok', unknownState: true });
  });
});

describe('summarizeExplainSteps', () => {
  it('sayar; en uzun yalnız yürütülen HER adımın süresi biliniyorsa', () => {
    const rows = explainStepRows(fold([
      step(1), result(1, { durationMs: 100 }),
      step(2), result(2, { durationMs: 250, sources: [{ source: 'logs', state: 'empty' }] }),
      step(3), result(3, { skipped: true, ok: false }),
      step(4), result(4, { ok: false, preview: '{"error":"timeout"}', durationMs: 8000 }),
    ]), false);
    expect(summarizeExplainSteps(rows)).toEqual({ count: 4, running: 0, errors: 1, skipped: 1, degraded: 1, longestMs: 8000 });
  });
  it('süresi olmayan ya da sürmekte olan adım varsa en uzun null', () => {
    expect(summarizeExplainSteps(explainStepRows(fold([step(1), result(1, { durationMs: undefined })]), false)).longestMs).toBeNull();
    const live = summarizeExplainSteps(explainStepRows(fold([step(1), result(1), step(2)]), true));
    expect(live).toMatchObject({ running: 1, longestMs: null });
  });
  // v0.10.948 — get_trace'ten sonraki dört okuma PARALEL koşar
  // (trace_investigate.go): süreleri toplamak duvar saatini ~4× şişirirdi.
  it('paralel okumaların süresi TOPLANMAZ (v0.10.948)', () => {
    const s = summarizeExplainSteps(explainStepRows(fold([
      step(0, 'get_trace'), result(0, { tool: 'get_trace', durationMs: 120 }),
      step(1, 'get_logs_for_trace'), step(2, 'compare_periods'), step(3, 'get_pod_health'), step(4, 'list_deploys'),
      result(1, { durationMs: 2000 }), result(2, { durationMs: 2100 }),
      result(3, { durationMs: 1900 }), result(4, { durationMs: 2200 }),
    ]), false));
    expect(s.longestMs).toBe(2200);
    expect(s).not.toHaveProperty('totalMs');
  });
});

describe('sourceFooterItems / missingSources', () => {
  const items = sourceFooterItems([
    { source: 'traces', backend: 'clickhouse', state: 'ok', returned: 42 },
    { source: 'logs', backend: 'elasticsearch', state: 'unreachable', detail: 'dial tcp: connection refused' },
    { source: 'metrics', backend: 'victoriametrics', state: 'partial', flags: ['partial', 'truncated'], notes: ['env filtresi uygulanamadı'] },
    { source: 'traces', state: 'ok', detail: 'referans penceresi' },
    { source: 'deploys', state: 'empty', returned: 0 },
  ]);
  it('ok DAHİL her kaynak; ok nötr, erişilemedi kırmızı, kısmi sarı, ek bayrak ayrı rozet', () => {
    expect(items.map(i => `${i.name}|${i.label}|${i.tone}`)).toEqual([
      'traces/clickhouse|ok|gray',
      'logs/elasticsearch|erişilemedi|err',
      'metrics/victoriametrics|kısmi|warn',
      'metrics/victoriametrics|limitli|warn',
      'referans · traces|ok|gray',
      'deploys|boş|gray',
    ]);
  });
  it('ipucu: detay, notlar, sayım (erişilemeyende sayım YOK)', () => {
    expect(items[0].title).toContain('42 kayıt');
    expect(items[1].title).toContain('connection refused');
    expect(items[1].title).not.toContain('kayıt');
    expect(items[2].title).toContain('env filtresi uygulanamadı');
  });
  // v0.10.948 — sunucu künyesi (worst != OK) ve prompt'un "Eksik veri" kuralıyla
  // aynı tanım: boş da eksik veri (adım özeti de boşu "eksik kapsam" sayıyor).
  it('eksik veri: ok DIŞINDAKİLER (boş dahil), tekil', () => {
    expect(missingSources(items)).toEqual(['logs/elasticsearch', 'metrics/victoriametrics', 'deploys']);
    expect(isMissingState('empty')).toBe(true);
    expect(isMissingState('not_configured')).toBe(true);
    expect(isMissingState('ok')).toBe(false);
  });
  // v0.10.948 — arızada sunucu `returned: 0` yollar (omitempty yok): «0 kayıt»
  // boş sonuç gibi okunurdu. Sayım yalnız sorgusu tamamlanan durumlarda.
  it('arıza durumunda (zaman aşımı · hata) ipucunda sayım YOK', () => {
    const failed = sourceFooterItems([
      { source: 'logs', backend: 'elasticsearch', state: 'timeout', detail: 'context deadline exceeded', returned: 0 },
      { source: 'metrics', state: 'error', returned: 0 },
    ]);
    expect(failed).toHaveLength(2);
    for (const it of failed) expect(it.title).not.toContain('kayıt');
    expect(items[5].title).toContain('0 kayıt'); // boş: sorgu tamamlandı, sayım anlamlı
  });
  // v0.10.948 — ad inceleme bölümünün etiketinden: traces/clickhouse hem trace
  // okuması hem dönem kıyası olabilir; eksik veri listesi HANGİ kanıtın eksik
  // olduğunu söyler. source/backend ipucunda kalır. Etiketsizler eskisi gibi.
  it('bölüm etiketi (label + tool) adı belirler; eksik veri kıyası söyler, trace\'i değil', () => {
    const inv = sourceFooterItems([
      { section: 'T', label: 'Trace', tool: 'get_trace', source: 'traces', backend: 'clickhouse', state: 'ok' },
      { section: 'K', label: 'Karşılaştırma', tool: 'compare_periods', source: 'traces', backend: 'clickhouse', state: 'timeout' },
      { section: 'K', label: 'Karşılaştırma', source: 'traces', backend: 'clickhouse', state: 'not_configured' },
    ]);
    expect(inv.map(i => i.name)).toEqual(['Trace (get_trace)', 'Karşılaştırma (compare_periods)', 'Karşılaştırma']);
    expect(new Set(inv.map(i => i.name)).size).toBe(3);
    expect(missingSources(inv)).toEqual(['Karşılaştırma (compare_periods)', 'Karşılaştırma']);
    expect(missingSources(inv)).not.toContain('Trace (get_trace)');
    expect(inv[1].title.startsWith('traces/clickhouse (Karşılaştırma): zaman aşımı')).toBe(true);
    // etiketsiz satır (eski sunucu) source/backend'e düşer
    expect(items[0].name).toBe('traces/clickhouse');
  });
  it('bilinmeyen durum ham adıyla; bozuk satır atlanır; boş girdi boş liste', () => {
    const odd = sourceFooterItems([{ source: 'pods', state: 'weird' }, { source: 'x', state: '' }]);
    expect(odd.map(i => i.label)).toEqual(['weird']);
    expect(sourceFooterItems(undefined)).toEqual([]);
  });
});

describe('evidenceLinks', () => {
  it('yalnız id\'siz, göreli ya da http(s); javascript:/protokolsüz // düşer; href tekil', () => {
    const out = evidenceLinks([
      { label: 'Trace', href: '/trace?id=0af7651916cd43dd8448eb211c80319c&span=b7ad6b7169203331' },
      { label: 'request_id', href: 'https://kibana.example/app?q=r1', id: 'r1' },
      { label: 'Loglar', href: '/logs?traceId=0af7651916cd43dd8448eb211c80319c&range=custom:1000-2000' },
      { label: 'Kötü', href: 'javascript:alert(1)' },
      { label: 'Protokolsüz', href: '//evil.example/x' },
      { label: 'Tekrar', href: '/trace?id=0af7651916cd43dd8448eb211c80319c&span=b7ad6b7169203331' },
      { label: 'Dış', href: 'https://grafana.example/d/x' },
      { label: '  ', href: '/service?name=checkout&env=prod' },
    ]);
    expect(out.map(l => l.label)).toEqual(['Trace', 'Loglar', 'Dış']);
  });
  it('isInternalHref', () => {
    expect(isInternalHref('/service?name=checkout&env=prod')).toBe(true);
    expect(isInternalHref('//x')).toBe(false);
    expect(isInternalHref('https://x')).toBe(false);
  });
});
