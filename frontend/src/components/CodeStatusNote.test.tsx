// @vitest-environment jsdom
//
// v0.10.1330 — stacktrace'siz trace'te "Kod incelemesi" ana operasyonun
// koduna düşer; durum satırı (codeStatusLine) tek bileşenden.
// v0.10.1407 — ✨ kartı (CopilotExplain) özne kipiyle silindi; satırı artık
// yalnız sohbetin kod turu (ChatBubble, v0.10.1406) çizer — render testleri
// bileşenin kendisini mount eder.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { CodeStatusNote } from './CodeStatusNote';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { api } from '@/lib/api';
import type { AICodeContext } from '@/lib/types';
import { codeStatusLead, codeStatusLine, codeStatusOperationTail } from './codeSourceRef';

const OP = 'LEND_PERSONAL_WEB_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST';
const opOK: AICodeContext = {
  repo: 'loan-service', branch: 'release', source: 'operation', operation: OP, operationSource: 'oracle',
  otherOperations: ['A_OTHER_REST', 'B_OTHER_REST'], operationLookup: 'ok',
  files: [
    { path: '/src/main/java/com/bank/rest/CreditProductInformationController.java', fromLine: 123, toLine: 140, kind: 'operation', url: 'https://tfs/x?line=123' },
    { path: '/src/main/java/com/bank/manager/CreditProductInformationManagerImpl.java', fromLine: 9, toLine: 17, kind: 'callee' },
  ],
};

const candOK: AICodeContext = {
  ...opOK, operationSource: 'function-code', otherOperations: ['A_CAND_REST'], operationCandidates: ['A_CAND_REST', OP],
};

describe('codeStatusLine', () => {
  it('operasyon kodundan okunduysa dosya#satır + operasyon + diğerleri', () => {
    const s = codeStatusLine(opOK);
    expect(s).toMatchObject({ kind: 'operation', operation: OP, others: 2,
      file: { label: 'CreditProductInformationController.java#L123', url: 'https://tfs/x?line=123' } });
    expect(codeStatusOperationTail(s as { operation: string; others: number }))
      .toBe(` (operasyon: ${OP}, +2 diğer operasyon okunmadı)`);
    expect(codeStatusOperationTail({ operation: OP, others: 0 })).toBe(` (operasyon: ${OP})`);
  });
  it('Code Search kapalı', () => {
    expect(codeStatusLine({ files: [], source: 'operation', operation: OP, operationLookup: 'off', reason: 'x' }))
      .toEqual({ kind: 'missing', detail: 'bu kayıtta stacktrace yok — operasyon kodu araması için Code Search kapalı' });
  });
  it('arama başarısız: operasyon + neden', () => {
    expect(codeStatusLine({ files: [], source: 'operation', operation: OP, operationLookup: 'failed', reason: 'isabet yok' }))
      .toEqual({ kind: 'missing', detail: `bu kayıtta stacktrace yok; operasyon ${OP} için depoda kod bulunamadı: isabet yok` });
  });
  it('operasyonsuz: eski dürüst not aynen; stack penceresinde satır yok', () => {
    expect(codeStatusLine({ files: [], reason: 'bu kayıtta stacktrace yok' }))
      .toEqual({ kind: 'missing', detail: 'bu kayıtta stacktrace yok' });
    expect(codeStatusLine({ source: 'convention', files: [{ path: 'a.java', fromLine: 1, toLine: 2, line: 2 }] })).toBeNull();
    expect(codeStatusLine(null)).toBeNull();
  });
  // v0.10.1349 (operatör-bildirimli) — hatasız trace: kesin operasyon yok.
  it('fonksiyon kodu adayı okunduysa "kesin değil" + aday + okunmayanlar', () => {
    const s = codeStatusLine(candOK);
    expect(s).toMatchObject({ kind: 'operation', operation: OP, others: 1, candidate: true,
      file: { label: 'CreditProductInformationController.java#L123' } });
    expect(codeStatusLead(s as { candidate?: boolean })).toBe('stacktrace yok → fonksiyon kodu ADAYININ kodu üzerinden incelendi (kesin değil):');
    expect(codeStatusOperationTail(s as { operation: string; others: number; candidate?: boolean }))
      .toBe(` (operasyon adayı: ${OP}, +1 aday okunmadı)`);
    expect(codeStatusOperationTail({ operation: OP, others: 0, candidate: true })).toBe(` (operasyon adayı: ${OP})`);
    // Kesin yol bayt bayt eskisi.
    expect(codeStatusLead({})).toBe('stacktrace yok → operasyon kodu üzerinden incelendi:');
    expect(codeStatusLine(opOK)).not.toHaveProperty('candidate');
  });
  it('adaylar depoda yok: adaylar + neden (sunucu cümlesi tekrarlanmaz)', () => {
    expect(codeStatusLine({ files: [], source: 'operation', operationSource: 'function-code', operationCandidates: ['A_CAND_REST', 'B_CAND_REST'],
      operationLookup: 'failed', reason: 'operasyon dosyası okunamadı: 500' }))
      .toEqual({ kind: 'missing', detail: 'bu kayıtta stacktrace yok; fonksiyon kodu adayları A_CAND_REST, B_CAND_REST için depoda kod bulunamadı: operasyon dosyası okunamadı: 500' });
    expect(codeStatusLine({ files: [], source: 'operation', operationSource: 'function-code', operationCandidates: ['A_CAND_REST'],
      operationLookup: 'failed', reason: 'bu kayıtta stacktrace yok; fonksiyon kodu adayları (A_CAND_REST) için depoda controller bulunamadı' }))
      .toEqual({ kind: 'missing', detail: 'bu kayıtta stacktrace yok; fonksiyon kodu adayları A_CAND_REST için depoda kod bulunamadı' });
  });
  it('ne kesin operasyon ne aday: "operasyon adı bulunamadı"', () => {
    expect(codeStatusLine({ files: [], source: 'operation', operationLookup: 'none', reason: 'bu kayıtta stacktrace yok; trace\'in operasyon adı bulunamadı' }))
      .toEqual({ kind: 'missing', detail: 'bu kayıtta stacktrace yok; trace\'in operasyon adı bulunamadı' });
  });
  it('v0.10.1407 — sohbetin kod turu aynı bileşenden çizer (✨ kartı silindi)', () => {
    const src = readFileSync(resolve(__dirname, 'ai/ChatBubble.tsx'), 'utf8');
    expect(src).toContain('<CodeStatusNote code={ex.code} />');
    expect(src).not.toContain('Kod okunamadı — <strong>');
  });
});

let host: HTMLDivElement;
let root: Root;
const settle = async () => { await act(async () => { await Promise.resolve(); }); };

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  __resetCopilotEnabledCache();
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function mountTrace(code: AICodeContext) {
  await act(async () => { root.render(<CodeStatusNote code={code} />); });
  await settle();
}

describe('CodeStatusNote — operasyon kodu durum satırı (v0.10.1330)', () => {
  it('başarı: dosya linki + operasyon + okunmayanlar, "Kod okunamadı" YOK', async () => {
    await mountTrace(opOK);
    const t = host.textContent ?? '';
    expect(t).toContain('stacktrace yok → operasyon kodu üzerinden incelendi:');
    expect(t).toContain(`CreditProductInformationController.java#L123 ↗ (operasyon: ${OP}, +2 diğer operasyon okunmadı)`);
    expect(t).not.toContain('Kod okunamadı');
    expect(host.querySelector('a[href="https://tfs/x?line=123"]')).not.toBeNull();
  });
  it('v0.10.1349 — fonksiyon kodu adayı: "kesin değil" satırı, "Kod okunamadı" YOK', async () => {
    await mountTrace(candOK);
    const t = host.textContent ?? '';
    expect(t).toContain('stacktrace yok → fonksiyon kodu ADAYININ kodu üzerinden incelendi (kesin değil):');
    expect(t).toContain(`CreditProductInformationController.java#L123 ↗ (operasyon adayı: ${OP}, +1 aday okunmadı)`);
    expect(t).not.toContain('Kod okunamadı');
  });
  it('v0.10.1349 — operasyon adı yok: dürüst not', async () => {
    await mountTrace({ files: [], source: 'operation', operationLookup: 'none', reason: 'x' });
    const t = host.textContent ?? '';
    expect(t).toContain('Kod okunamadı');
    expect(t).toContain("(bu kayıtta stacktrace yok; trace'in operasyon adı bulunamadı)");
  });
  it('Code Search kapalı: dürüst not + neden', async () => {
    await mountTrace({ files: [], source: 'operation', operation: OP, operationLookup: 'off' });
    const t = host.textContent ?? '';
    expect(t).toContain('Kod okunamadı');
    expect(t).toContain('bu kayıtta stacktrace yok — operasyon kodu araması için Code Search kapalı');
  });
});
