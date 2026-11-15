// @vitest-environment jsdom
//
// OracleOperationChip.render — v0.10.1329 (operatör-bildirimli): trace
// 6008dded… kökü POST /ACMWEB/application/expressOrderPreValidate, kendi Oracle
// satırı …APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST; çip fonksiyon kodu
// sözlüğünün en çok satırlı adayını (…APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST
// "(+15)") "Operasyon:" diye gösteriyordu.
//
// NE ÇİVİLİYOR:
//   • trace'in kendi Oracle satırı varsa çip O operasyonu gösterir, "+N" bu
//     trace'teki diğer operasyonlardır, sözlük HİÇ istenmez; ipucu/aria
//     kaynağı ("kendi Oracle hata satırları — kesin") söyler;
//   • kesin kaynak yoksa sözlük adayı ETİKETİYLE çizilir ("fonksiyon kodundan,
//     N aday"), ipucu "kesin DEĞİL" der ve adayları listeler;
//   • Oracle satırları yüklenirken (oracleRows undefined) çip bekler — tahmini
//     bir an bile göstermez;
//   • link hedefi değişmedi: fonksiyon kodunun diğer trace'leri.
//   • v0.10.1514 (trace başlığı A) — her biri TEK kimlik çipi (ui/IdentityChip):
//     OPERASYON etiketi · ad · ipucu hücresi (sözlükte amber "tahmin · N aday",
//     kesinde nötr "+N") · kopyala; FUNCTION / CHANNEL çipleri de aynı anatomi,
//     CHANNEL artık düz metin değil.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { LogRow, OracleFunctionCodesResponse, SpanRow } from '@/lib/types';

const TRX = 'LOAN_CONSUMER_APP_APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST';
const INQ = 'LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST';

const dict: OracleFunctionCodesResponse = {
  enabled: true, codes: { LCA01: { ops: [INQ, TRX, 'OTHER_REST'], total: 16 } },
};
const dictHook = vi.fn((enabled: boolean) => ({ data: enabled ? dict : undefined }));
vi.mock('@/lib/queries', () => ({ useOracleFunctionCodes: (enabled: boolean) => dictHook(enabled) }));

import { OracleOperationChip, TraceEntryCodes } from './OracleOperationChip';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const sp = (o: Partial<SpanRow> & { spanId: string }): SpanRow => ({
  traceId: '6008dded60bb6da25ec0d11b0045e24b', parentSpanId: '', name: 'POST /ACMWEB/application/expressOrderPreValidate',
  kind: 'server', serviceName: 'acmweb', hostName: '', startTime: 1, endTime: 2, durationMs: 1,
  statusCode: 'error', statusMessage: '', attributes: {}, resourceAttributes: {}, events: null, scopeName: '', ...o,
});
const spans: SpanRow[] = [
  sp({ spanId: 'r', attributes: { FUNCTION_CODE: 'LCA01' } }),
  sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'loan-inq', name: 'GET /inq', startTime: 2 }),
];
const oracleRow = (op: string, spanId: string, timestamp: number): LogRow => ({
  id: timestamp, timestamp, severity: 17, severityText: 'ERROR', body: 'err', serviceName: 'Log Merkezi',
  traceId: spans[0].traceId, spanId, attributes: { 'operation.code': op }, resourceAttributes: {}, origin: 'oracle',
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mountNode(node: React.ReactNode): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter>{node}</MemoryRouter>);
  });
  return host;
}
const mount = (oracleRows: LogRow[] | undefined, sps: SpanRow[] = spans) =>
  mountNode(<OracleOperationChip spans={sps} pageRange={{ preset: '1h' }} oracleRows={oracleRows} />);
const cell = (chip: Element, cls: string) => chip.querySelector(`.idchip__${cls}`) as HTMLElement | null;

beforeEach(() => { dictHook.mockClear(); });
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  host = null; root = null;
});

describe('OracleOperationChip (v0.10.1329)', () => {
  it('kesin: trace\'in kendi Oracle satırları — TRX ana, +1 = bu trace\'teki INQ; sözlük istenmez', async () => {
    const h = await mount([oracleRow(INQ, 'c', 5), oracleRow(TRX, 'r', 9)]);
    const chip = h.querySelector('[data-op-source]') as HTMLElement;
    expect(chip.dataset.opSource).toBe('oracle');
    // v0.10.1418 — operasyon adının yanında kopyala düğmesi (⧉), değeri tam ad.
    // v0.10.1514 — kimlik çipi: OPERASYON · ad · "+1" (nötr) · ⧉.
    expect(chip.getAttribute('role')).toBe('listitem');
    expect(chip.textContent).toBe(`OPERASYON${TRX}+1⧉`);
    expect(cell(chip, 'label')!.textContent).toBe('OPERASYON');
    expect(cell(chip, 'val')!.textContent).toBe(TRX);
    expect(cell(chip, 'hint')!.textContent).toBe('+1');
    expect(cell(chip, 'hint')!.classList.contains('is-warn')).toBe(false);
    expect(cell(chip, 'hint')!.title).toBe(`Bu trace'teki diğer operasyonlar: ${INQ}`);
    const copy = chip.querySelector('button[title="Operasyon adını kopyala"]');
    expect(copy).not.toBeNull();
    expect(copy!.getAttribute('aria-label')).toBe('Operasyon adını kopyala');
    expect(chip.textContent).not.toContain('(+15)');
    expect(chip.title).toContain("Kaynak: bu trace'in kendi Oracle hata satırları (operation.code, 2 satır) — kesin.");
    expect(chip.title).toContain(`Bu trace'teki diğer operasyonlar: ${INQ}.`);
    const a = chip.querySelector('a')!;
    expect(a.getAttribute('aria-label')).toContain('kendi Oracle hata satırları');
    expect(a.getAttribute('href')).toContain('LCA01');
    expect(dictHook).toHaveBeenLastCalledWith(false);
  });

  it('yedek: kesin kaynak yoksa sözlük adayı etiketli ("fonksiyon kodundan, 16 aday"), ipucu kesin değil der', async () => {
    const h = await mount([]);
    const chip = h.querySelector('[data-op-source]') as HTMLElement;
    expect(chip.dataset.opSource).toBe('dictionary');
    // v0.10.1514 — uzun "Operasyon (fonksiyon kodundan, 16 aday):" etiketi amber
    // ipucu hücresine indi; kopyala (v0.10.1418) çipin son hücresi.
    expect(chip.textContent).toBe(`OPERASYON${INQ}tahmin · 16 aday⧉`);
    expect(chip.textContent).not.toContain('fonksiyon kodundan,');
    const hint = cell(chip, 'hint')!;
    expect(hint.textContent).toBe('tahmin · 16 aday');
    expect(hint.classList.contains('is-warn')).toBe(true);
    expect(hint.title).toContain('kesin DEĞİL');
    expect(chip.querySelector('button[title="Operasyon adını kopyala"]')).not.toBeNull();
    expect(chip.title).toContain('kesin DEĞİL');
    expect(chip.title).toContain(`Bu kod 16 operasyonda görülüyor: ${INQ}, ${TRX}, OTHER_REST (+13 daha).`);
    const a = chip.querySelector('a')!;
    expect(a.getAttribute('aria-label')).toContain('fonksiyon kodu sözlüğü');
    expect(a.getAttribute('href')).toContain('LCA01');
    expect(dictHook).toHaveBeenLastCalledWith(true);
  });

  it('Oracle satırları yüklenirken hiçbir şey çizilmez (tahmin bir an bile görünmez)', async () => {
    const h = await mount(undefined);
    expect(h.querySelector('[data-op-source]')).toBeNull();
    expect(h.textContent).toBe('');
  });

  it("v0.10.1514 — kesin kaynak, fonksiyon kodu yok: ad düz metin (bağlantı yok), kaynak sr-only notta, +N yoksa ipucu hücresi yok", async () => {
    const plain = [sp({ spanId: 'r', attributes: { 'operation.code': TRX } })];
    const h = await mount([], plain);
    const chip = h.querySelector('[data-op-source]') as HTMLElement;
    expect(chip.dataset.opSource).toBe('span');
    expect(chip.querySelector('a')).toBeNull();
    expect(cell(chip, 'hint')).toBeNull();
    expect(cell(chip, 'val')!.querySelector('.sr-only')!.textContent).toContain("attribute'u operation.code — kesin.");
    expect(chip.title).toBe("Kaynak: kök span attribute'u operation.code — kesin.");
  });

  it('v0.10.1514 — sözlük: her aday kendi çipi, hepsi listitem', async () => {
    const two = [sp({ spanId: 'r', attributes: { FUNCTION_CODE: 'LCA01', function_code: 'LCA02' } })];
    dict.codes.LCA02 = { ops: ['SECOND_REST'], total: 1 };
    try {
      const h = await mount([], two);
      const chips = [...h.querySelectorAll('[data-op-source="dictionary"]')];
      expect(chips.map(c => c.querySelector('.idchip__val')!.textContent)).toEqual([INQ, 'SECOND_REST']);
      expect(chips.every(c => c.getAttribute('role') === 'listitem')).toBe(true);
      expect(chips[1].querySelector('.idchip__hint')!.textContent).toBe('tahmin · 1 aday');
    } finally { delete dict.codes.LCA02; }
  });
});

describe('TraceEntryCodes (v0.10.1497; v0.10.1514 kimlik çipi)', () => {
  const coded = [sp({ spanId: 'r', attributes: { FUNCTION_CODE: 'SPE0112', channel_code: '060203' } })];
  it('FUNCTION bağlantılı (aynı kodun trace\'leri), CHANNEL çip — ikisi de kopyalanır, anahtar yazımı ipucunda', async () => {
    const h = await mountNode(<TraceEntryCodes spans={coded} pageRange={{ preset: '1h' }} />);
    const fn = h.querySelector('[data-trace-codes="function"]') as HTMLElement;
    const ch = h.querySelector('[data-trace-codes="channel"]') as HTMLElement;
    expect(fn.textContent).toBe('FUNCTIONSPE0112⧉');
    expect(fn.title).toBe('FUNCTION_CODE: SPE0112');
    const a = fn.querySelector('a.idchip__val')!;
    expect(a.getAttribute('href')).toContain('SPE0112');
    expect(a.getAttribute('title')).toBe("Bu fonksiyon kodunun diğer trace'leri");
    expect(fn.querySelector('button[aria-label="Fonksiyon kodunu kopyala"]')).not.toBeNull();
    expect(ch.textContent).toBe('CHANNEL060203⧉');
    expect(ch.title).toBe('channel_code: 060203');
    expect(ch.querySelector('a')).toBeNull();
    expect(ch.querySelector('button[aria-label="Kanal kodunu kopyala"]')).not.toBeNull();
    expect([fn, ch].every(c => c.getAttribute('role') === 'listitem')).toBe(true);
  });
  it('kod yoksa hiçbir şey çizilmez', async () => {
    const h = await mountNode(<TraceEntryCodes spans={[sp({ spanId: 'x' })]} pageRange={{ preset: '1h' }} />);
    expect(h.textContent).toBe('');
  });
});
