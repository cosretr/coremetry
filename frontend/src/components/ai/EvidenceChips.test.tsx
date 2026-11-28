// @vitest-environment jsdom
//
// v0.10.1272 — CoSRE adım 3: kanıt çipleri + Explore'da aç (+ operatör eki:
// sonuç sohbet içinde de). Pinler: çip alanları; "Explore'da aç" gerçek
// bağlantı ve tıklayınca href'e gider; kanıt yoksa hiçbir şey çizilmez; çip
// bloğunu açar/kapatır, varsayılan SON sonuç açık; eski olaylar (kanıtsız tur)
// aynen çizilir; arşiv kanıtı taşır; bağlı trace_list genel listede çizilmez.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter, useLocation } from 'react-router';
import { EvidenceChips } from './EvidenceChips';
import { ChatBubble } from './ChatBubble';
import { evidenceChipParts, evidenceDefaultOpen, evidenceChartGroups, evidenceHopLabel, evidenceHref, evidenceLevelTone, stepEvidenceItems } from './evidenceChip';
import { persistMessages, restoreTurns } from './chatPersist';
import { evidenceBlocksByRef, traceListBlocks } from '@/lib/chatBlocks';
import type { ChatQueryEvidence, ChatTypedBlock, ChatTurn } from '@/lib/types';

const W = { fromMs: Date.UTC(2026, 9, 16, 14, 0), toMs: Date.UTC(2026, 9, 16, 15, 0) };
const evLogs: ChatQueryEvidence = {
  tool: 'query_logs', source: 'Elasticsearch', query: 'logstore.Search WHERE service = \'payments\'', queryKind: 'summary',
  window: W, env: 'prod', cluster: 'ocp-a', rows: 2, durationMs: 123, href: '/logs?service=payments&range=custom%3A1-2', ref: 'ev1',
};
const evTraces: ChatQueryEvidence = {
  tool: 'query_traces', source: 'ClickHouse', query: 'SELECT 1', queryKind: 'sql', window: W, rows: 1, durationMs: 37,
  href: '/traces?service=checkout&sort=duration', ref: 'ev2',
};
const blocks: ChatTypedBlock[] = [
  { id: 'b1', type: 'table', seq: 1, final: true, payload: {
    title: 'payments · loglar', evidenceRef: 'ev1', deepLink: evLogs.href,
    columns: [{ key: 'time', label: 'Zaman', kind: 'time' }, { key: 'severity', label: 'Seviye', kind: 'severity' },
      { key: 'body', label: 'Mesaj', kind: 'text' }, { key: 'traceId', label: 'Trace', kind: 'trace' }],
    rows: [[W.fromMs, 'ERROR', 'gateway timeout', '4bf92f3577b34da6a3ce929d0e0e4736'], [W.fromMs + 1000, 'INFO', 'ok', '']],
  } },
  { id: 'b2', type: 'trace_list', seq: 2, final: true, payload: {
    query: 'checkout · trace', window: { fromNs: W.fromMs * 1e6, toNs: W.toMs * 1e6 }, deepLink: evTraces.href!, truncated: false, evidenceRef: 'ev2',
    traces: [{ traceId: 'a'.repeat(32), startTime: W.fromMs * 1e6, serviceName: 'checkout', rootName: 'POST /pay', durationMs: 812, spanCount: 9, hasError: true }],
  } },
];

let host: HTMLDivElement;
let root: Root;
let loc = '';
function Probe() { const l = useLocation(); loc = l.pathname + l.search; return null; }

async function mount(node: React.ReactNode) {
  await act(async () => {
    root.render(<MemoryRouter initialEntries={['/chat']}><Probe />{node}</MemoryRouter>);
  });
}

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  loc = '';
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe('EvidenceChips — çip alanları', () => {
  it('[Kaynak] · pencere · env/cluster · N satır · süre; ipucunda tam sorgu (monospace) + kopyala', async () => {
    await mount(<EvidenceChips items={[evLogs]} />);
    const txt = host.querySelector('.cm-evidence')?.textContent ?? '';
    expect(txt).toContain('Elasticsearch');
    expect(txt).toContain('1 h'); // v0.10.2484 — lib/format/duration
    expect(txt).toContain('prod/ocp-a');
    expect(txt).toContain('2 satır');
    expect(txt).toContain('123 ms');
    expect(evidenceChipParts(evLogs)).toEqual([expect.stringMatching(/^1 h \d\d:\d\d–\d\d:\d\d$/), 'prod/ocp-a', '2 satır', '123 ms']);
    expect(host.querySelector('[aria-label="Copy"]')).not.toBeNull();
    // ipucu odakta açılır (Tooltip) — sorgu monospace kutuda.
    const chip = host.querySelector<HTMLElement>('.cm-evidence__btn')!;
    await act(async () => { chip.focus(); chip.dispatchEvent(new FocusEvent('focus', { bubbles: true })); });
    const q = host.querySelector('.cm-evidence__q');
    expect(q?.className).toContain('mono');
    expect(q?.textContent).toBe(evLogs.query);
  });

  it("Explore'da aç gerçek bağlantı; tıklayınca href'e gider", async () => {
    await mount(<EvidenceChips items={[evLogs]} />);
    const a = host.querySelector<HTMLAnchorElement>('a.cm-evidence__go')!;
    expect(a).not.toBeNull();
    expect(a.getAttribute('href')).toBe(evLogs.href);
    expect(a.getAttribute('aria-label')).toContain("Explore'da aç");
    await act(async () => { a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })); });
    expect(loc).toBe(evLogs.href);
  });

  it('href yoksa bağlantı yok; dış / protokol-göreli href asla bağlantı olmaz', async () => {
    await mount(<EvidenceChips items={[{ ...evLogs, href: undefined }, { ...evTraces, href: '//evil.example/x' }]} />);
    expect(host.querySelector('a.cm-evidence__go')).toBeNull();
    expect(host.querySelectorAll('.cm-evidence__chip').length).toBe(2);
  });

  it('kanıt yoksa hiçbir şey çizilmez', async () => {
    await mount(<><EvidenceChips items={[]} /><EvidenceChips /></>);
    expect(host.querySelector('.cm-evidence')).toBeNull();
  });
});

describe('EvidenceChips — sohbet içi blok (operatör eki)', () => {
  const map = evidenceBlocksByRef(blocks);
  it('varsayılan: SON sonuç açık, önceki kapalı; çip aç/kapa yapar', async () => {
    await mount(<EvidenceChips items={[evLogs, evTraces]} blocks={map} defaultOpen="last" />);
    const btns = host.querySelectorAll<HTMLButtonElement>('button.cm-evidence__btn');
    expect(btns.length).toBe(2);
    expect(btns[0].getAttribute('aria-expanded')).toBe('false');
    expect(btns[1].getAttribute('aria-expanded')).toBe('true');
    expect(host.querySelector('.cm-trace-list')).not.toBeNull(); // son = trace_list açık
    expect(host.querySelector('.cm-evblk__table')).toBeNull();
    await act(async () => { btns[0].click(); });
    expect(btns[0].getAttribute('aria-expanded')).toBe('true');
    const table = host.querySelector('.cm-evblk__table');
    expect(table?.textContent).toContain('gateway timeout');
    expect(table?.querySelector('.badge.b-err')?.textContent).toBe('ERROR');
    expect(table?.querySelector('a')?.getAttribute('href')).toContain('4bf92f3577b34da6a3ce929d0e0e4736');
    await act(async () => { btns[1].click(); });
    expect(host.querySelector('.cm-trace-list')).toBeNull();
    // Explore'da aç bloklu çipte de duruyor.
    expect(host.querySelectorAll('a.cm-evidence__go').length).toBe(2);
  });
  it('adım paneli kipi (none): hepsi kapalı', async () => {
    await mount(<EvidenceChips items={[evLogs, evTraces]} blocks={map} defaultOpen="none" />);
    expect(host.querySelectorAll('button.cm-evidence__btn[aria-expanded="true"]').length).toBe(0);
    expect(host.querySelector('.cm-evblk')).toBeNull();
  });
  it('blok yoksa (arşivden gelen cevap) çip düz etiket, Explore bağlantısı yine var', async () => {
    await mount(<EvidenceChips items={[evLogs]} defaultOpen="last" />);
    expect(host.querySelector('button.cm-evidence__btn')).toBeNull();
    expect(host.querySelector('.cm-evidence__btn.is-static')).not.toBeNull();
    expect(host.querySelector('a.cm-evidence__go')).not.toBeNull();
  });
  it('saf yardımcılar: varsayılan açık ref, bağlı trace_list genel listeden düşer, chart birim grupları', () => {
    expect(evidenceDefaultOpen([evLogs, evTraces], map)).toBe('ev2');
    expect(evidenceDefaultOpen([evLogs, { ...evTraces, ref: undefined }], map)).toBe('ev1');
    expect(evidenceDefaultOpen([evLogs], undefined)).toBeNull();
    expect(traceListBlocks(blocks)).toEqual([]);
    const groups = evidenceChartGroups({ title: 't', evidenceRef: 'e', window: { fromNs: 0, toNs: 1 }, series: [
      { name: 'rate', unit: 'req/s', points: [[1000, 1]] }, { name: 'p99', unit: 'ms', points: [[1000, 2]] }, { name: 'p95', unit: 'ms', points: [[1000, 3]] },
    ] });
    expect(groups.map(g => [g.unit, g.items.length])).toEqual([['req/s', 1], ['ms', 2]]);
    expect(groups[0].items[0].series[0].points[0]).toEqual({ time: 1000 * 1e6, value: 1 });
  });
  // v0.10.1299 — operatör 2026-10-17: 5 dk p99 gürültüsü yanlış bulgu üretiyordu.
  // Operasyon grafiği: p50 birincil, p99 ikincil (soluk), önceki pencere p99'u kesikli.
  it('seri stili: secondary → muted rol, dashed → kesikli; stilsiz eski seri aynen', () => {
    const [ms] = evidenceChartGroups({ title: 't', evidenceRef: 'e', window: { fromNs: 0, toNs: 1 }, series: [
      { name: 'p50', unit: 'ms', points: [[1000, 4]] },
      { name: 'p99', unit: 'ms', style: 'secondary', points: [[1000, 9]] },
      { name: 'önceki pencere p99', unit: 'ms', style: 'dashed', points: [[1000, 7]] },
    ] }).map(g => g.items);
    expect(ms.map(i => [i.name, i.role, !!i.dashed])).toEqual([
      ['p50', 'data', false], ['p99', 'muted', false], ['önceki pencere p99', 'data', true],
    ]);
  });
});

describe('ChatBubble — kanıt çipleri ve eski olaylar', () => {
  const html = (turn: ChatTurn) => renderToStaticMarkup(<MemoryRouter><ChatBubble turn={turn} /></MemoryRouter>);
  it('answer.evidence → cevap altında çipler; son sonucun bloğu açık', () => {
    const h = html({ role: 'assistant', text: 'cevap', evidence: [evLogs, evTraces], blocks });
    expect(h).toContain('cm-evidence');
    expect(h).toContain("Explore&#x27;da aç");
    expect(h).toContain('cm-trace-list');
    expect(h).not.toContain('cm-evblk__table');
  });
  it('kanıtsız eski tur aynen: çip yok', () => {
    const h = html({ role: 'assistant', text: 'eski cevap', links: [{ label: 'Trace\'ler', href: '/traces' }] });
    expect(h).not.toContain('cm-evidence');
    expect(h).toContain('eski cevap');
    expect(h).toContain('ai-link');
  });
  it('answer.evidence yoksa adımların kanıtı çip olur (hata ile biten tur)', () => {
    const h = html({ role: 'assistant', text: 'yarım', steps: ['query_logs'], stepDetails: [{ i: 1, tool: 'query_logs', ok: true, preview: '{}', evidence: evLogs }] });
    expect(h).toContain('cm-evidence__chip');
  });
});

describe('arşiv (chatPersist) — kanıt çipleri geri yüklenir', () => {
  it('asistan kanıtı persist + restore; kanıtsız mesaj {role,text}', () => {
    const msgs = persistMessages([
      { role: 'user', text: 'soru' },
      { role: 'assistant', text: 'cevap', evidence: [evLogs] },
      { role: 'assistant', text: 'düz' },
    ]);
    expect(msgs).toEqual([{ role: 'user', text: 'soru' }, { role: 'assistant', text: 'cevap', evidence: [evLogs] }, { role: 'assistant', text: 'düz' }]);
    expect(restoreTurns(msgs)[1].evidence).toEqual([evLogs]);
  });
});

// v0.10.1275 — CoSRE adım 6: sinyaller arası pivot (trace → pod → JVM → log) + playbook.
describe('EvidenceChips — bileşik kanıt listesi (pivot_from_trace)', () => {
  const hopEv = (hop: string, source: string, ref: string): ChatQueryEvidence => ({
    tool: 'pivot_from_trace', source, query: 'q', queryKind: 'summary', window: W, durationMs: 5, hop, ref,
    href: hop === 'trace' ? '/trace?id=' + 'a'.repeat(32) : '/pod?pod=co-1',
  });
  const list = [hopEv('trace', 'ClickHouse', 'ev7'), hopEv('pods', 'Thanos', 'ev7.1'), hopEv('jvm', 'VictoriaMetrics', 'ev7.2')];

  it('hop etiketi: Trace, Trace › Pod; atomik kanıtta yok', () => {
    expect(evidenceHopLabel('trace')).toBe('Trace');
    expect(evidenceHopLabel('pods')).toBe('Trace › Pod');
    expect(evidenceHopLabel('op_baseline')).toBe('Trace › Operasyon');
    expect(evidenceHopLabel(undefined)).toBe('');
    expect(evidenceChipParts(list[1])[0]).toBe('Trace › Pod');
    expect(evidenceChipParts(evLogs)[0]).not.toContain('Trace');
  });

  it('adım: evidenceList varsa liste, eski olay yalnız evidence', () => {
    expect(stepEvidenceItems({ evidence: list[0], evidenceList: list })).toHaveLength(3);
    expect(stepEvidenceItems({ evidence: evLogs })).toEqual([evLogs]);
    expect(stepEvidenceItems({})).toEqual([]);
  });

  it('liste çipleri çizilir, hop etiketleriyle', async () => {
    await mount(<EvidenceChips items={list} />);
    const chips = host.querySelectorAll('.cm-evidence__chip');
    expect(chips.length).toBe(3);
    expect(chips[1].textContent).toContain('Trace › Pod');
    expect(chips[2].textContent).toContain('Trace › JVM');
  });

  it('ChatBubble: adımın evidenceList listesi cevap çipleri olur (answer.evidence yoksa)', () => {
    const h = renderToStaticMarkup(<MemoryRouter><ChatBubble turn={{
      role: 'assistant', text: 'yarım', steps: ['pivot_from_trace'],
      stepDetails: [{ i: 7, tool: 'pivot_from_trace', ok: true, preview: '{}', evidence: list[0], evidenceList: list }],
    }} /></MemoryRouter>);
    expect((h.match(/cm-evidence__chip/g) ?? []).length).toBeGreaterThanOrEqual(3);
    expect(h).toContain('Trace › Pod');
  });

  it('level sütunu: crit/warn rozet, ok düz metin', async () => {
    expect(evidenceLevelTone('crit')).toBe('danger');
    expect(evidenceLevelTone('warn')).toBe('warning');
    expect(evidenceLevelTone('ok')).toBeNull();
    const jvmBlocks: ChatTypedBlock[] = [{ id: 'j', type: 'table', seq: 1, final: true, payload: {
      title: 'JVM', evidenceRef: 'ev7.2', columns: [{ key: 'pod', label: 'Pod', kind: 'text' }, { key: 'level', label: 'Seviye', kind: 'level' }],
      rows: [['co-1', 'crit'], ['co-2', 'warn'], ['co-3', 'ok']],
    } }];
    await mount(<EvidenceChips items={[list[2]]} blocks={evidenceBlocksByRef(jvmBlocks)} defaultOpen="last" />);
    const table = host.querySelector('.cm-evblk__table')!;
    expect(table.querySelector('.badge.b-err')?.textContent).toBe('kritik');
    expect(table.querySelector('.badge.b-warn')?.textContent).toBe('uyarı');
    expect(table.textContent).toContain('normal');
    expect(table.querySelectorAll('.badge').length).toBe(2);
  });
});

// v0.10.1314 — evidenceHref tek kapıdan (lib/safeAppPath): react-router v6
// ters bölü açık yönlendirmesi ve kodlanmış biçimleri tıklanmaz.
describe('evidenceHref (v0.10.1314)', () => {
  const CASES: Array<[string | undefined, string | null]> = [
    ['/\\evil.com', null],
    ['//evil.com', null],
    ['/%5Cevil.com', null],
    ['https://evil.com', null],
    [undefined, null],
    ['/logs?service=a', '/logs?service=a'],
  ];
  for (const [href, want] of CASES) {
    it(`${JSON.stringify(href)} → ${JSON.stringify(want)}`, () => {
      expect(evidenceHref({ ...evLogs, href })).toBe(want);
    });
  }
});
