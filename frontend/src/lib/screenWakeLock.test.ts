import { describe, it, expect } from 'vitest';
import { holdScreenAwake, type WakeDoc, type WakeNav, type WakeSentinel } from './screenWakeLock';

function fakes(opts: { hidden?: boolean; reject?: boolean } = {}) {
  const sentinels: { released: boolean; fire: () => void }[] = [];
  let requests = 0;
  const nav: WakeNav = {
    wakeLock: {
      request: () => {
        requests++;
        if (opts.reject) return Promise.reject(new Error('NotAllowedError'));
        let onRelease = () => {};
        const rec = { released: false, fire: () => { rec.released = true; onRelease(); } };
        sentinels.push(rec);
        const s: WakeSentinel = {
          release: () => { rec.fire(); return Promise.resolve(); },
          addEventListener: (_t, cb) => { onRelease = cb; },
        };
        return Promise.resolve(s);
      },
    },
  };
  const listeners = new Set<() => void>();
  const doc: WakeDoc & { show(): void; hide(): void } = {
    hidden: !!opts.hidden,
    addEventListener: (_t, cb) => { listeners.add(cb); },
    removeEventListener: (_t, cb) => { listeners.delete(cb); },
    show() { this.hidden = false; listeners.forEach(l => l()); },
    hide() { this.hidden = true; listeners.forEach(l => l()); },
  };
  return { nav, doc, sentinels, listeners, requests: () => requests };
}
const flush = () => new Promise<void>(r => setTimeout(r, 0));

describe('holdScreenAwake (v0.10.2151)', () => {
  it('görünür sayfada kilidi alır, bırakınca serbest kalır ve dinleyici kalkar', async () => {
    const f = fakes();
    const stop = holdScreenAwake(f.nav, f.doc);
    await flush();
    expect(f.requests()).toBe(1);
    expect(f.sentinels[0].released).toBe(false);
    stop();
    expect(f.sentinels[0].released).toBe(true);
    expect(f.listeners.size).toBe(0);
  });

  it('gizli sekmede istemez; görünür olunca alır', async () => {
    const f = fakes({ hidden: true });
    const stop = holdScreenAwake(f.nav, f.doc);
    await flush();
    expect(f.requests()).toBe(0);
    f.doc.show(); await flush();
    expect(f.requests()).toBe(1);
    stop();
  });

  it('tarayıcı kilidi bırakınca (sekme gizlendi) geri dönüşte yeniden alır', async () => {
    const f = fakes();
    const stop = holdScreenAwake(f.nav, f.doc);
    await flush();
    f.doc.hide(); f.sentinels[0].fire(); // tarayıcı gizlenince kendisi bırakır
    f.doc.show(); await flush();
    expect(f.requests()).toBe(2);
    expect(f.sentinels[1].released).toBe(false);
    stop();
    expect(f.sentinels[1].released).toBe(true);
  });

  it('kilit tutulurken tekrar görünürlük olayı ikinci istek açmaz', async () => {
    const f = fakes();
    const stop = holdScreenAwake(f.nav, f.doc);
    await flush();
    f.doc.show(); await flush();
    expect(f.requests()).toBe(1);
    stop();
  });

  it('istek sürerken bırakılırsa gelen kilit hemen serbest bırakılır', async () => {
    const f = fakes();
    const stop = holdScreenAwake(f.nav, f.doc);
    stop();
    await flush();
    expect(f.sentinels[0].released).toBe(true);
  });

  it('destek yoksa ve izin reddinde sessizce no-op', async () => {
    const f = fakes();
    expect(() => holdScreenAwake({}, f.doc)()).not.toThrow();
    const r = fakes({ reject: true });
    const stop = holdScreenAwake(r.nav, r.doc);
    await flush();
    expect(r.requests()).toBe(1);
    expect(() => stop()).not.toThrow();
  });
});
