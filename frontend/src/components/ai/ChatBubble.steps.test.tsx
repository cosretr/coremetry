// ChatBubble.steps.test.tsx — v0.10.161: araç-çağrısı şeffaflık paneli
// (etüt seçenek A) çalışma zamanında. Kapalı hâl ChatBubble üzerinden
// (özet satırı, rozetler, panel yokluğu); tablo gövdesi ToolStepsPanel'in
// `initialOpen` test dikişiyle (statik render tıklayamaz): 12 çağrıda ilk 5
// satır + «7 daha», hatalı satırda ToolErrorJSON sınıfı/ipucu + tekrar rozeti,
// etiket adımı (tool yok) SAYILMAZ, süre bilinmiyorsa «—», kırpık rozeti.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { ChatBubble, ToolStepsPanel } from './ChatBubble';
import type { ChatTurn, ChatStepDetail } from '@/lib/types';

function html(turn: ChatTurn): string {
  return renderToStaticMarkup(<MemoryRouter><ChatBubble turn={turn} /></MemoryRouter>);
}
function panel(details: ChatStepDetail[], over: { error?: string; turnDone?: boolean } = {}): string {
  return renderToStaticMarkup(<ToolStepsPanel details={details} error={over.error} turnDone={over.turnDone ?? true} evId={null} setEvId={() => {}} initialOpen />);
}
const d = (i: number, over: Partial<ChatStepDetail> = {}): ChatStepDetail => ({ i, tool: `tool_${i}`, args: '{"service":"payments-orchestrator"}', ok: true, preview: 'ok-preview', durationMs: 100 * i, ...over });

describe('ToolStepsPanel (v0.10.161) — kapalı hâl (ChatBubble)', () => {
  it('özet satırı: araç · hata · toplam süre; çipler yerinde', () => {
    const details = [d(1), d(2, { ok: false, preview: '{"error":"timeout","retryable":true,"hint":"pencereyi daralt"}' }), d(3)];
    const h = html({ role: 'assistant', text: 'cevap', steps: details.map(x => x.tool), stepDetails: details });
    expect(h).toContain('3 araç');
    expect(h).toContain('1 hata');
    expect(h).toContain('600 ms');
    expect(h).toContain('⚙ tool_1');
  });
  it('etiket adımı (tool yok) araç sayılmaz; bir adımın süresi yoksa toplam «—»', () => {
    const details = [{ i: 1, tool: '', label: 'ekran bağlamı: api-gateway' }, d(2), d(3, { durationMs: undefined })];
    const h = html({ role: 'assistant', text: 'x', steps: ['ekran bağlamı: api-gateway', 'tool_2', 'tool_3'], stepDetails: details });
    expect(h).toMatch(/2 araç · —/);
  });
  it('guided → tek «ön-yükleme» rozeti; bütçe aşımı → rozet (satır değil)', () => {
    const details = [d(1, { origin: 'guided', durationMs: undefined }), d(2, { origin: 'guided', durationMs: undefined })];
    const h = html({ role: 'assistant', text: 'x', steps: ['a', 'b'], stepDetails: details });
    expect(h.match(/ön-yükleme/g)?.length).toBe(1);
    const h2 = html({ role: 'assistant', text: '', steps: ['a'], stepDetails: [d(1)], error: 'Bu alışveriş 3 dakika tavanına dayandı ve durduruldu.' });
    expect(h2.match(/bütçe aşıldı/g)?.length).toBe(1);
  });
  it('detaysız turda panel yok, çipler var; yalnız etiket adımı olan turda panel yok', () => {
    const h = html({ role: 'assistant', text: 'x', steps: ['get_topology'] });
    expect(h).toContain('⚙ get_topology');
    expect(h).not.toContain('cm-steps-sum');
    const h2 = html({ role: 'assistant', text: 'x', steps: ['pencere: 12:00'], stepDetails: [{ i: 1, tool: '', label: 'pencere: 12:00' }] });
    expect(h2).not.toContain('cm-steps-sum');
  });
});

describe('ToolStepsPanel (v0.10.161) — açık tablo', () => {
  it('12 çağrıda ilk 5 satır + «7 daha»', () => {
    const details = Array.from({ length: 12 }, (_, i) => d(i + 1));
    const h = panel(details);
    expect(h.match(/<tr class="/g)?.length ?? 0).toBe(5);
    expect(h).toContain('7 daha');
  });
  it('hatalı satır: ToolErrorJSON sınıfı + ipucu, «tekrar» rozeti, is-err sınıfı; kırpık rozeti Durum hücresinde', () => {
    const details = [d(1, { ok: false, preview: '{"error":"timeout","retryable":true,"hint":"pencereyi daralt","detail":"code: 159"}' }), d(2, { truncated: true, bytes: 38200 })];
    const h = panel(details);
    expect(h).toContain('is-err');
    expect(h).toContain('timeout — pencereyi daralt');
    expect(h).toContain('hata · tekrar');
    expect(h).toContain('kırpık');
  });
  it('tur bitmiş, kanıtsız adım → «kanıt yok», süre «—»; sürüyorsa «…»', () => {
    const h = panel([d(1), { i: 2, tool: 'search_logs' }], { turnDone: true });
    expect(h).toContain('kanıt yok');
    const h2 = panel([d(1), { i: 2, tool: 'search_logs' }], { turnDone: false });
    expect(h2).toContain('sürüyor…');
  });
});

// v0.10.944 (CoSRE Faz A) — dürüst ilerleme ÇİZİMDE: sunucu çipi aracı
// çalıştırmadan önce yayınlar; çip sonucu gelene dek "çalışıyor…" der ve
// tur bitince bu iddia düşer. Kaynak durumu rozet olur; yürütülmeyen çağrı
// "yürütülmedi" der ve hata sayılmaz.
describe('dürüst ilerleme (v0.10.944)', () => {
  it('sonucu gelmemiş araç çipi tur sürerken "çalışıyor…"; tur bitince demez; bağlam etiketi hiç demez', () => {
    const running = html({ role: 'assistant', text: '', pending: true, steps: ['search_logs', 'bağlam: ekrandaki trace (abc)'], stepDetails: [{ i: 1, tool: 'search_logs' }, { i: 2, tool: 'bağlam: ekrandaki trace (abc)' }] });
    expect(running.match(/çalışıyor…/g)?.length).toBe(1);
    const done = html({ role: 'assistant', text: 'cevap', steps: ['search_logs'], stepDetails: [{ i: 1, tool: 'search_logs' }] });
    expect(done).not.toContain('çalışıyor…');
  });
  it('source.state rozeti çipte ve panelde; ok rozet almaz', () => {
    const preview = '{"logs":[],"source":{"source":"logs","backend":"elasticsearch","state":"unreachable","returned":0}}';
    const details = [d(1, { tool: 'search_logs', preview })];
    const h = html({ role: 'assistant', text: 'x', steps: ['search_logs'], stepDetails: details });
    expect(h).toContain('erişilemedi');
    expect(h).toContain('badge b-err');
    const p = panel(details);
    expect(p).toContain('erişilemedi');
    expect(p).not.toContain('>ok<');
    const okH = html({ role: 'assistant', text: 'x', steps: ['search_logs'], stepDetails: [d(1, { preview: '{"source":{"source":"logs","state":"ok","returned":4}}' })] });
    expect(okH).not.toContain('erişilemedi');
  });
  it('skipped:true → "yürütülmedi", hata sayılmaz, satır kırmızı değil', () => {
    const details = [d(1), d(2, { ok: false, skipped: true, durationMs: 0, preview: 'tekrar koruması: aynı argüman' })];
    const h = html({ role: 'assistant', text: 'x', steps: ['tool_1', 'tool_2'], stepDetails: details });
    expect(h).toContain('yürütülmedi');
    expect(h).not.toContain('1 hata');
    const p = panel(details);
    expect(p).toContain('yürütülmedi');
    expect(p).not.toContain('is-err');
  });
  it('unauthorized araç hatası panelde "yetki yok" der', () => {
    const p = panel([d(1, { ok: false, preview: '{"error":"unauthorized","retryable":false,"hint":"diğer kaynaklarla devam et"}' })]);
    expect(p).toContain('⚠ yetki yok');
  });
  // v0.10.944 — Go map sırasında `source`tan önce kırpılan önizleme: rozet
  // sunucunun yapısal `sources`undan; o da yoksa nötr «ok» DEĞİL «durum okunamadı».
  it('kırpık önizleme: yapısal sources rozeti çizer; yoksa «durum okunamadı» (nötr ok değil)', () => {
    const clipped = '{"count":50,"has_more":true,"logs":[{"body":"payment declined","service":"payments"},{"body":"pay';
    const withSrc = [d(1, { tool: 'search_logs', preview: clipped, truncated: true, bytes: 48_000, sources: [{ source: 'logs', state: 'partial', flags: ['partial', 'truncated'] }] })];
    const p = panel(withSrc);
    expect(p).toContain('kısmi');
    expect(p).toContain('limitli');
    expect(p).not.toContain('durum okunamadı');
    expect(p).not.toContain('logs · '); // tek kaynak: önek yok
    const h = html({ role: 'assistant', text: 'x', steps: ['search_logs'], stepDetails: withSrc });
    expect(h).toContain('kısmi');

    const unknown = panel([d(1, { tool: 'search_logs', preview: clipped, truncated: true, bytes: 48_000 })]);
    expect(unknown).toContain('durum okunamadı');
    expect(unknown).toContain('badge b-warn');
    expect(unknown).not.toContain('>ok<');
    // kırpılmamış ve durum taşımayan sonuç eskisi gibi nötr ok
    expect(panel([d(1)])).toContain('>ok<');
  });
});
