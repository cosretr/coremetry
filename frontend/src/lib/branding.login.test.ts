import { describe, it, expect } from 'vitest';
import { resolveBranding, DEFAULT_BRANDING } from './branding';

// v0.10.1907/1909 — Login alanlarının varsayılanları (operatör kararları):
// özet açık, söz KAPALI (yalnız "on" açar), topoloji isimsiz; orgName kırpılır.
describe('resolveBranding — Login alanları', () => {
  it('boş blob: özet on, söz off, topoloji anonymous, orgName boş', () => {
    for (const b of [null, undefined, {}]) {
      const r = resolveBranding(b);
      expect(r.loginSummary).toBe('on');
      expect(r.loginQuote).toBe('off');
      expect(r.loginTopology).toBe('anonymous');
      expect(r.orgName).toBe('');
    }
    expect(DEFAULT_BRANDING.loginQuote).toBe('off');
  });
  it('söz: "" ve "off" kapalı, yalnız "on" açık', () => {
    expect(resolveBranding({ loginQuote: '' }).loginQuote).toBe('off');
    expect(resolveBranding({ loginQuote: 'off' }).loginQuote).toBe('off');
    expect(resolveBranding({ loginQuote: 'on' }).loginQuote).toBe('on');
  });
  it('özet: "" ve "on" açık, "off" kapalı', () => {
    expect(resolveBranding({ loginSummary: '' }).loginSummary).toBe('on');
    expect(resolveBranding({ loginSummary: 'on' }).loginSummary).toBe('on');
    expect(resolveBranding({ loginSummary: 'off' }).loginSummary).toBe('off');
  });
  it('topoloji: "" isimsiz; off / named / anonymous olduğu gibi', () => {
    expect(resolveBranding({ loginTopology: '' }).loginTopology).toBe('anonymous');
    expect(resolveBranding({ loginTopology: 'anonymous' }).loginTopology).toBe('anonymous');
    expect(resolveBranding({ loginTopology: 'off' }).loginTopology).toBe('off');
    expect(resolveBranding({ loginTopology: 'named' }).loginTopology).toBe('named');
  });
  it('orgName kırpılır', () => {
    expect(resolveBranding({ orgName: '  acme  ' }).orgName).toBe('acme');
  });
});
