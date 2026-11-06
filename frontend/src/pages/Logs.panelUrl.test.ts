import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.448 (log arama denetimi C8) — ?panel= URL'den okunur/yazılır,
// süzgeç kimliğine girmez, sekme panele URL'den geçer.
// v0.10.1499 (Discover dilim 2) — panel bir GÖRÜNÜM: Logs | Desenler |
// Şablonlar (pages/logs/logsView.ts). URL tek kaynak; `logs.patterns.open`
// localStorage yedeği KALDIRILDI (param yok = Logs görünümü).
describe('Logs ?panel= (C8 → görünüm anahtarı)', () => {
  const src = readFileSync(resolve(__dirname, 'Logs.tsx'), 'utf8');
  const view = readFileSync(resolve(__dirname, 'logs/logsView.ts'), 'utf8');
  const url = readFileSync(resolve(__dirname, '../lib/logsUrl.ts'), 'utf8');
  it('reads the param with the narrowing parser; no localStorage fallback', () => {
    expect(src).toContain("const urlView = parseLogsView(searchParams.get('panel'));");
    expect(view).toContain('return parseLogsPanel(panel) ?? \'logs\';');
    expect(src).not.toContain('logs.patterns.open');
    expect(src).toContain('const view = effectiveLogsView(urlView, live);');
  });
  it('writes the param with replace:true (logs = deleted) and passes the view as the panel tab', () => {
    expect(view).toContain("if (view === 'logs') p.delete('panel'); else p.set('panel', view);");
    expect(src).toContain('setSearchParams(prev => writeLogsView(prev, v), { replace: true });');
    expect(src).toContain('<LogPatternsPanel embedded open={!scopeGate} onSearch={searchFromPattern} tab={view}'); // v0.10.1855
    // satır tıkı q yazar VE Logs'a geçer — tek URL yazımında
    expect(src).toContain("writeUrl(next, filters, undefined, mode === 'replace' ? 'logs' : undefined);");
  });
  it('panel never enters the filter identity', () => {
    const sig = url.slice(url.indexOf('export function logsUrlSig'), url.indexOf('export function logsUrlSig') + 600);
    expect(sig).not.toContain('panel');
    const reader = url.slice(url.indexOf('export function readLogsParams'), url.indexOf('export function readLogsParams') + 900);
    expect(reader).not.toContain("p.get('panel')");
  });
});
