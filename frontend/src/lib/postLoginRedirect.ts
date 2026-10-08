// Post-login deep-link restore — v0.8.367 (operator-reported: a
// shared /traces?…filters=… link bounced an expired session to
// /login, and after signing in the operator landed on the default
// page instead of the pasted URL; Dynatrace restores the link).
//
// The intended URL is captured when the auth guard bounces a
// protected path to /login and consumed on the first authed render.
// sessionStorage on purpose: it is tab-scoped (a stale redirect
// can't leak into tomorrow's session) yet survives the OIDC
// full-page round-trip to the IdP and back, which react-router
// navigation state would not.

const KEY = 'coremetry-post-login-redirect';

// sanitizeRedirect accepts only same-origin, in-app paths. Anything
// scheme-ful or protocol-relative ('//evil.example') is rejected so a
// crafted link can't turn the restore into an open redirect; /login
// itself and public (unauthenticated) surfaces are pointless to
// restore. Pure — vitest alongside.
//
// v0.10.1123 — rules are mirrored 1:1 by the server's sanitizeOIDCNext
// (internal/api/oidc_next.go), which guards the OIDC ?next= return path:
// single leading '/', no backslash, no control chars (CR/LF), no '.' /
// '..' segment (raw or %2e — /x/../api/foo), and the percent-decoded
// PATH part (/%2F%2Fevil, /%61pi/x) must pass the same checks and not
// sit under /login, /public or /api. Change one, change both (table
// tests on both sides). Only difference: the 2048 cap is server-side
// + oidcStartHref, not here — the sessionStorage fallback keeps a deep
// link of any length.
export const MAX_REDIRECT_LEN = 2048;

function hasDotSegment(p: string): boolean {
  return p.split('/').some(seg => seg === '.' || seg === '..');
}

function hasControlChar(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return true;
  }
  return false;
}

function unsafePath(p: string): boolean {
  return !p.startsWith('/') || p.startsWith('//') || p.includes('\\') || hasControlChar(p);
}

export function sanitizeRedirect(raw: string | null): string | null {
  if (!raw) return null;
  if (unsafePath(raw)) return null;
  const rawPath = raw.split(/[?#]/, 1)[0];
  let decoded: string;
  try {
    decoded = decodeURIComponent(rawPath);
  } catch {
    return null;
  }
  if (unsafePath(decoded)) return null;
  if (hasDotSegment(rawPath) || hasDotSegment(decoded)) return null;
  for (const blocked of ['/login', '/public', '/api']) {
    if (decoded === blocked || decoded.startsWith(blocked + '/')) return null;
  }
  return raw;
}

export function savePostLoginRedirect(path: string): void {
  const p = sanitizeRedirect(path);
  if (!p) return;
  try { sessionStorage.setItem(KEY, p); } catch { /* private mode etc. */ }
}

// peekPostLoginRedirect reads the saved deep link WITHOUT clearing it:
// the Login page's SSO button forwards it to the server as ?next=
// (v0.10.1123) while the entry stays as a fallback until the first
// authed render.
export function peekPostLoginRedirect(): string | null {
  try {
    return sanitizeRedirect(sessionStorage.getItem(KEY));
  } catch {
    return null;
  }
}

// oidcStartHref — SSO button target: server-side return path when a
// deep link is pending, plain start otherwise (v0.10.1123). Links over
// the server cap (2048) go without next — the sessionStorage entry
// still restores them on '/'.
export function oidcStartHref(): string {
  const next = peekPostLoginRedirect();
  return next && next.length <= MAX_REDIRECT_LEN
    ? `/api/auth/oidc/start?next=${encodeURIComponent(next)}`
    : '/api/auth/oidc/start';
}

export type PostLoginAction =
  | { kind: 'none' }
  | { kind: 'clear' }
  | { kind: 'navigate'; to: string };

// firstAuthedRenderAction — what AuthProvider does with a pending entry
// on the FIRST authed render (not /login, which keeps its own restore).
// Pure, vitest alongside.
//  · on the target already (server-side ?next= landed us) → clear
//  · on '/' with an entry → restore it (fallback: cookie expired, next
//    over the cap, old tab)
//  · anywhere else → clear: the user is already somewhere deliberate, a
//    later visit to '/' must not jump to a stale link
// Later authed renders never restore (only the first one decides).
export function firstAuthedRenderAction(pending: string | null, path: string, here: string): PostLoginAction {
  if (!pending) return { kind: 'none' };
  if (pending === here) return { kind: 'clear' };
  if (path === '/') return { kind: 'navigate', to: pending };
  return { kind: 'clear' };
}

export function consumePostLoginRedirect(): string | null {
  try {
    const p = sanitizeRedirect(sessionStorage.getItem(KEY));
    sessionStorage.removeItem(KEY);
    return p;
  } catch {
    return null;
  }
}
