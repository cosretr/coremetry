// @vitest-environment jsdom
//
// v0.9.1359 — CI kırmızıydı, lokal yeşildi. Bu dosya `sessionStorage`/
// `localStorage` KULLANIYOR ve vitest.config.ts ortamı bilinçli `node`
// (2000+ saf test jsdom bedelini ödemesin; dosya başına opt-in). Node 25
// bu global'leri yerleşik taşıyor, CI'ın Node 22.si TAŞIMIYOR — yani test
// lokalde ÇALIŞMA ZAMANI SÜRÜMÜ sayesinde geçiyordu, kendi hakkıyla değil.
// jsdom onları sürümden bağımsız sağlar.
import { describe, it, expect, beforeEach, beforeAll } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  sanitizeRedirect, savePostLoginRedirect, consumePostLoginRedirect,
  peekPostLoginRedirect, oidcStartHref, MAX_REDIRECT_LEN, firstAuthedRenderAction,
} from './postLoginRedirect';

// v0.8.390 — CI fix: the vitest environment is 'node' on purpose
// (vitest.config.ts), and Node < 23 has no Web Storage globals — CI
// (Node 22) failed with "sessionStorage is not defined" while newer
// local Node ships the global. Deterministic in-memory stub either
// way, so the suite never depends on the runtime's storage.
beforeAll(() => {
  const store = new Map<string, string>();
  const stub: Pick<Storage, 'getItem' | 'setItem' | 'removeItem' | 'clear'> = {
    getItem: k => store.get(k) ?? null,
    setItem: (k, v) => { store.set(k, String(v)); },
    removeItem: k => { store.delete(k); },
    clear: () => { store.clear(); },
  };
  Object.defineProperty(globalThis, 'sessionStorage', { value: stub, configurable: true });
});

// v0.8.367 — post-login deep-link restore. The sanitizer is the
// security boundary: only same-origin in-app paths may be restored.
describe('sanitizeRedirect', () => {
  it('accepts an in-app deep link with query + hash', () => {
    const url = '/traces?range=1h&sort=duration&filters=%5B%7B%22k%22%3A%22db.statement%22%7D%5D#top';
    expect(sanitizeRedirect(url)).toBe(url);
  });

  it('kiosk derin bağlantısını korur (v0.10.673 — /trace?id=…&kiosk=1)', () => {
    const url = '/trace?id=0af7651916cd43dd8448eb211c80319c&kiosk=1&span=b7ad6b7169203331&tab=logs';
    expect(sanitizeRedirect(url)).toBe(url);
  });

  it('rejects absolute and protocol-relative URLs (open redirect)', () => {
    expect(sanitizeRedirect('https://evil.example/phish')).toBeNull();
    expect(sanitizeRedirect('//evil.example/phish')).toBeNull();
    expect(sanitizeRedirect('javascript:alert(1)')).toBeNull();
  });

  it('rejects /login and public surfaces', () => {
    expect(sanitizeRedirect('/login')).toBeNull();
    expect(sanitizeRedirect('/login?error=x')).toBeNull();
    expect(sanitizeRedirect('/public/trace?token=abc')).toBeNull();
  });

  it('rejects empty / null', () => {
    expect(sanitizeRedirect(null)).toBeNull();
    expect(sanitizeRedirect('')).toBeNull();
    expect(sanitizeRedirect('traces')).toBeNull();
  });
});

describe('save + consume round-trip', () => {
  beforeEach(() => sessionStorage.clear());

  it('restores once, then clears', () => {
    savePostLoginRedirect('/service?name=archive-service&tab=details');
    expect(consumePostLoginRedirect()).toBe('/service?name=archive-service&tab=details');
    expect(consumePostLoginRedirect()).toBeNull();
  });

  it('never stores a rejected value', () => {
    savePostLoginRedirect('//evil.example');
    expect(consumePostLoginRedirect()).toBeNull();
  });

  it('sanitizes on read too (storage tampered between visits)', () => {
    sessionStorage.setItem('coremetry-post-login-redirect', 'https://evil.example');
    expect(consumePostLoginRedirect()).toBeNull();
  });
});

// v0.10.1123 — server-side OIDC return path. sanitizeRedirect mirrors
// internal/api/oidc_next.go sanitizeOIDCNext (same table on both sides).
describe('sanitizeRedirect — rules shared with the server', () => {
  it.each([
    '//evil.example', '/\\evil', 'https://x', '/%2F%2Fevil', '/%5Cevil',
    '/login', '/login#x', '/api/x', '/api', '/%61pi/x', '/public/x',
    '/x\r\nSet-Cookie: a=b', '/x%0d%0aSet-Cookie:a', '/%zz',
    '/x/../api/foo', '/%2e%2e/api/foo', '/x/%2E./login', '/./services',
  ])('rejects %j', raw => {
    expect(sanitizeRedirect(raw)).toBeNull();
  });

  it('accepts a deep link with query + hash; dots inside a segment are fine', () => {
    expect(sanitizeRedirect('/services/svc-orders?x=1#tab')).toBe('/services/svc-orders?x=1#tab');
    expect(sanitizeRedirect('/services/svc.orders..v2')).toBe('/services/svc.orders..v2');
  });

  it('has no length cap (sessionStorage fallback keeps long links)', () => {
    const long = '/traces?q=' + 'a'.repeat(MAX_REDIRECT_LEN * 2);
    expect(sanitizeRedirect(long)).toBe(long);
  });
});

describe('firstAuthedRenderAction', () => {
  it('nothing pending → none', () => {
    expect(firstAuthedRenderAction(null, '/', '/')).toEqual({ kind: 'none' });
  });
  it('server landed us on the target → clear', () => {
    expect(firstAuthedRenderAction('/services/svc-orders?x=1#tab', '/services/svc-orders',
      '/services/svc-orders?x=1#tab')).toEqual({ kind: 'clear' });
  });
  it('on / with a pending entry → restore (fallback)', () => {
    expect(firstAuthedRenderAction('/services/svc-orders', '/', '/'))
      .toEqual({ kind: 'navigate', to: '/services/svc-orders' });
  });
  it('elsewhere with a different entry → clear (no stale jump on a later /)', () => {
    expect(firstAuthedRenderAction('/services/svc-orders', '/traces', '/traces?range=1h'))
      .toEqual({ kind: 'clear' });
  });
});

describe('peek + oidcStartHref', () => {
  beforeEach(() => sessionStorage.clear());

  it('peek does not consume', () => {
    savePostLoginRedirect('/services/svc-orders?x=1#tab');
    expect(peekPostLoginRedirect()).toBe('/services/svc-orders?x=1#tab');
    expect(peekPostLoginRedirect()).toBe('/services/svc-orders?x=1#tab');
    expect(consumePostLoginRedirect()).toBe('/services/svc-orders?x=1#tab');
    expect(peekPostLoginRedirect()).toBeNull();
  });

  it('peek sanitizes tampered storage', () => {
    sessionStorage.setItem('coremetry-post-login-redirect', '//evil.example');
    expect(peekPostLoginRedirect()).toBeNull();
  });

  it('SSO href carries the encoded deep link as ?next=', () => {
    savePostLoginRedirect('/services/svc-orders?x=1&y=2#tab');
    const u = new URL(oidcStartHref(), 'http://x');
    expect(u.pathname).toBe('/api/auth/oidc/start');
    expect(u.searchParams.get('next')).toBe('/services/svc-orders?x=1&y=2#tab');
    expect(u.hash).toBe('');
    // the entry stays as the fallback
    expect(peekPostLoginRedirect()).toBe('/services/svc-orders?x=1&y=2#tab');
  });

  it('SSO href omits next when nothing is pending', () => {
    expect(oidcStartHref()).toBe('/api/auth/oidc/start');
  });

  it('SSO href omits next over the 2048 cap but the entry is still stored', () => {
    const long = '/traces?q=' + 'a'.repeat(MAX_REDIRECT_LEN);
    savePostLoginRedirect(long);
    expect(oidcStartHref()).toBe('/api/auth/oidc/start');
    expect(peekPostLoginRedirect()).toBe(long);
    const edge = '/' + 'a'.repeat(MAX_REDIRECT_LEN - 1);
    savePostLoginRedirect(edge);
    expect(new URL(oidcStartHref(), 'http://x').searchParams.get('next')).toBe(edge);
  });

  it('Login SSO button navigates via oidcStartHref (no bare start URL)', () => {
    const src = readFileSync(resolve(__dirname, '../pages/Login.tsx'), 'utf8');
    expect(src).toContain('window.location.href = oidcStartHref()');
    expect(src).not.toContain("'/api/auth/oidc/start'");
  });
});
