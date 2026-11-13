import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.676 — kiosk eylemleri (audit §10 F11): trace listesi (NAME hücresi)
// ve trace detayı (eylem şeridi) kiosk URL'sini ÜRETİCİDEN (traceHref,
// kiosk:true) alır ve `noopener,noreferrer` ile yeni pencerede açar. Ham `/trace?`
// yazımı traceLogsLinkGate'in konusu; burası kiosk bayrağının iki yüzeyde
// de üreticiden geçtiğini pinler.
const traces = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
const trace = readFileSync(resolve(__dirname, 'Trace.tsx'), 'utf8');

describe('kiosk eylemleri (v0.10.676)', () => {
  // v0.10.2116 (operatör kararı 2026-11-13) — iddia DEĞİŞTİ: trace LİSTESİNDEKİ
  // satır-içi kiosk düğmesi kaldırıldı; kiosk girişi yalnız trace detayında.
  it('Traces listesi: satır-içi kiosk girişi YOK', () => {
    expect(traces).not.toContain('kiosk: true');
    expect(traces).not.toContain('row-kiosk');
  });
  it('Trace detayı: traceHref(kiosk:true, span) + noopener', () => {
    expect(trace).toContain('traceHref(id, { kiosk: true, span: selectedId, pageRange: range })');
    expect(trace).toContain('⧉ Kiosk');
  });
});
