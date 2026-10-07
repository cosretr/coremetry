// @vitest-environment jsdom
//
// v0.10.1090 — şeridin "Ranked candidates" canlı yolu GERÇEK mount'ta.
//
// v0.10.1063 manşeti düzeltti ama şerit bağlantısız worse/lost satırları hâlâ
// "Ranked candidates" altında listeliyordu (operatör: "<svc-B> ile ilgili
// olduğunu düşünüyor ama alakasız"). Artık yalnız sunucunun causeEligible
// işaretlediği satır aday; hiçbiri yoksa panelin dürüst hükmü basılır.
// Saf yardımcı (ribbonCandidates) ayrıca testli — bu dosya bileşenin onu
// gerçekten kullandığını ve boş dalı doğru çizdiğini çiviler.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { RootCauseRibbon } from './RootCauseRibbon';
import { api } from '@/lib/api';
import type { ChangedService, RootCause } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const row = (service: string, score: number, o: Partial<ChangedService> = {}): ChangedService => ({
  service, baselineRate: 1, currentRate: 1, rateDeltaPct: 0, baselineErrorRate: 0,
  currentErrorRate: 0, errDeltaPct: 0, baselineP99Ms: 10, currentP99Ms: 10, p99DeltaPct: 0,
  score, reasons: [`r-${service}`], ...o,
});

const rcOf = (correlations: ChangedService[], topologyKnown: boolean): RootCause => ({
  problemId: 'p1', service: 'svc-a', metric: 'http_p99_ms', startedAt: 0, fromNs: 0, toNs: 6e11,
  correlations, topologyKnown,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(rc: RootCause): Promise<string> {
  vi.spyOn(api, 'problemRootCauseCore').mockResolvedValue(rc);
  vi.spyOn(api, 'rootCauseVerdictPersisted').mockResolvedValue({ found: false });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter><RootCauseRibbon anchor="problem" id="p1" defaultOpen /></MemoryRouter>);
  });
  await act(async () => { await Promise.resolve(); });
  return host.textContent ?? '';
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  vi.restoreAllMocks();
});

describe('RootCauseRibbon — canlı adaylar yalnız causeEligible (v0.10.1090)', () => {
  it('karışık satırlar: yalnız bağlı + kötüleşen svc-x listelenir', async () => {
    const txt = await mount(rcOf([
      row('svc-b', 404, { direction: 'lost' }),                 // bağlantısız, trafiği kesilmiş
      row('svc-u', 220, { direction: 'worse' }),                // bağlantısız, kötüleşen
      row('svc-c', 213, { direction: 'better', relation: 'downstream' }),
      row('svc-x', 62, { direction: 'worse', relation: 'downstream', causeEligible: true }),
    ], true));
    expect(txt).toContain('Ranked candidates');
    expect(txt).toContain('svc-x');
    for (const bad of ['svc-b', 'svc-u', 'svc-c']) expect(txt).not.toContain(bad);
    expect(txt).not.toContain('none is a connected, worsening dependency');
  });

  it('uygun satır yok, topoloji okundu → dürüst "bağlı ve kötüleşen yok" satırı', async () => {
    const txt = await mount(rcOf([
      row('svc-b', 404, { direction: 'lost' }),
      row('svc-u', 220, { direction: 'worse' }),
    ], true));
    expect(txt).toContain('Ranked candidates');
    expect(txt).toContain('Other services moved in the same window but none is a connected, worsening dependency of svc-a.');
    expect(txt).not.toContain('svc-b');
    expect(txt).not.toContain('svc-u');
    expect(txt).not.toContain('No correlating signals');
  });

  it('topoloji okunamadı → "bağlantı doğrulanamadı"', async () => {
    const txt = await mount(rcOf([row('svc-u', 220, { direction: 'worse' })], false));
    expect(txt).toContain('bağlantı doğrulanamadı (topology unavailable).');
    expect(txt).not.toContain('svc-u');
  });

  it('kıpırdayan servis yok → eski "No correlating signals" boş hâli', async () => {
    const txt = await mount(rcOf([], true));
    expect(txt).toContain('No correlating signals');
    expect(txt).not.toContain('Ranked candidates');
  });
});
