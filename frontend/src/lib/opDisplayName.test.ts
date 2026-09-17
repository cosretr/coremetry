// opDisplayName.test.ts — v0.10.756 çıplak fiil adı → "METHOD /route".
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { isBareHTTPMethod, opDisplayName } from './opDisplayName';

describe('opDisplayName', () => {
  it('çıplak fiil + route → birleşik; aksi hâlde ad aynen', () => {
    expect(opDisplayName('POST', '/shop/orders/:id')).toBe('POST /shop/orders/:id');
    expect(opDisplayName('get', '/health')).toBe('get /health'); // fiil harf-duyarsız tanınır, ad olduğu gibi
    expect(opDisplayName('POST', '')).toBe('POST');
    expect(opDisplayName('POST', undefined)).toBe('POST');
    expect(opDisplayName('SELECT shop.orders', '/x')).toBe('SELECT shop.orders'); // fiil değil → route eklenmez
    expect(opDisplayName('POST /already/here', '/x')).toBe('POST /already/here');
    expect(opDisplayName('', '/x')).toBe('');
    expect(opDisplayName(undefined, '/x')).toBe('');
  });
  it('isBareHTTPMethod', () => {
    for (const m of ['GET', 'post', ' Put ', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS', 'TRACE', 'CONNECT']) expect(isBareHTTPMethod(m)).toBe(true);
    for (const m of ['', 'GETX', 'POST /a', 'SELECT', 'grpc.Call']) expect(isBareHTTPMethod(m)).toBe(false);
  });
  it('Traces Name hücresi gösterim adını kullanır; tip rootRoute taşır', () => {
    const traces = readFileSync(resolve(__dirname, '../pages/Traces.tsx'), 'utf8');
    expect(traces).toContain('opDisplayName(t.rootName, t.rootRoute)');
    const types = readFileSync(resolve(__dirname, 'types.ts'), 'utf8');
    expect(types).toContain('rootRoute?: string;');
  });
});
