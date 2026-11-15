// v0.10.2209 — Endpoints başlık hunisi: URL normalizasyonu + sekme başına sıra anahtarı;
// Service / Path üstteki çubukla TEK durum (aynı ?service / ?search), Method / Status sunucuya gider.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { endpointOrderKey, normMethod, normStatusClass } from './endpointColFilter';

describe('endpointColFilter', () => {
  it('metot / durum sınıfı normalize, bilinmeyen boş', () => {
    expect(normMethod(' post ')).toBe('POST');
    expect(normMethod('TRACE')).toBe('');
    expect(normStatusClass('5XX')).toBe('5xx');
    expect(normStatusClass('1xx')).toBe('');
    expect(normStatusClass(null)).toBe('');
  });
  it('sıra anahtarı sekme başına', () => {
    expect(endpointOrderKey('endpoints-v2', 'http')).not.toBe(endpointOrderKey('endpoints-v2', 'rpc'));
  });
  it('sayfa: huni ile çubuk aynı durumu yazar; metot/durum sorguya gider', () => {
    const src = readFileSync(resolve(__dirname, '../Endpoints.tsx'), 'utf8');
    expect(src).toContain("c.id === 'service' ? { v: service, set: setService }");
    expect(src).toContain("c.id === 'path' ? { v: search.trim(), set: (v: string) => { setSearch(v); setSearchParam(v); } }");
    expect(src).toContain("method: entry === 'http' && methodF ? methodF : undefined,");
    expect(src).toContain("status: entry === 'http' && statusF ? statusF : undefined,");
    expect(src).toContain('reorderable: true,');
  });
});
