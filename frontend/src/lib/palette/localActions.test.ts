// @vitest-environment jsdom
// lib/palette/localActions — v0.10.1665 (etkileşim P3, plan §1.2/§4 P3 kabul):
// her yerel eylem saf testli — copy-link göreli pencereyi mutlak ms'ye dondurur
// (URL'nin geri kalanı bayt bayt aynı), tema/yoğunluk saklama anahtarları
// değişmedi (index.html açılış betiği aynı anahtarları okur), bağlam süzgeçleri
// (copilot kapalı → Ask CoSRE yok; servis yok → pin yok; geçerli tema/yoğunluk
// listede yok), sunucu durumu yazan eylem YOK, "changes state" etiketi yalnız
// ACTIONS altısında.
import { describe, it, expect, vi } from 'vitest';
import {
  localActionCatalog, filterLocalActions, serviceFromLocation, shareLinkFor,
  askCosreQuestion, runLocalAction, type LocalActionCtx, type LocalActionEffects,
} from './localActions';
import { buildPaletteResults, CHANGES_STATE } from './results';
import { localizePages } from './pages';
import { decodeRange } from '@/lib/urlState';
import { applyDensity, currentDensity, nextDensity, DENSITY_STORAGE_KEY } from '@/lib/density';
import { applyTheme } from '@/lib/theme';
import { getRaw } from '@/lib/storage';

// Bu ortamda localStorage yok — depo bellek haritası (anahtar ADI testin konusu).
const mem = vi.hoisted(() => new Map<string, string>());
vi.mock('@/lib/storage', () => ({
  getRaw: (k: string) => mem.get(k) ?? null,
  setRaw: (k: string, v: string) => { mem.set(k, v); },
}));

const ctx = (o: Partial<LocalActionCtx> = {}): LocalActionCtx => ({
  href: 'https://cm.example/traces?service=a&range=1h#x', nowMs: 1_700_000_000_000,
  service: null, servicePinned: false, copilotEnabled: true, theme: 'dark', density: 'comfortable', ...o,
});
const ids = (c: LocalActionCtx) => localActionCatalog(c).map(a => a.id);

describe('localActionCatalog — bağlam', () => {
  it.each([
    ['copilot açık, servis yok', ctx(), ['copy-link', 'ask-cosre', 'theme-light', 'theme-redhat', 'density-compact', 'density-dense', 'shortcuts-help']],
    ['copilot kapalı', ctx({ copilotEnabled: false }), ['copy-link', 'theme-light', 'theme-redhat', 'density-compact', 'density-dense', 'shortcuts-help']],
    ['servis sabitli değil', ctx({ service: 'checkout', theme: 'redhat', density: 'dense' }), ['copy-link', 'ask-cosre', 'theme-dark', 'theme-light', 'density-comfortable', 'density-compact', 'shortcuts-help', 'pin-service']],
    ['servis sabitli', ctx({ service: 'checkout', servicePinned: true }), ['copy-link', 'ask-cosre', 'theme-light', 'theme-redhat', 'density-compact', 'density-dense', 'shortcuts-help', 'unpin-service']],
  ])('%s', (_n, c, want) => { expect(ids(c)).toEqual(want); });

  it('yenileme aralığı / silme / ayar yazımı eylemi yok (Q2)', () => {
    const all = ids(ctx({ service: 'x' }));
    expect(all.some(i => /refresh|interval|delete|setting|ack|mute|silence/.test(i))).toBe(false);
  });
});

describe('filterLocalActions — sorgu', () => {
  it.each([
    ['', []],
    ['copy', ['copy-link']],
    ['theme', ['theme-light', 'theme-redhat']],
    ['kurumsal', ['theme-redhat']],
    ['dense', ['density-dense']],
    ['ask', ['ask-cosre']],
    ['kısayol', ['shortcuts-help']],
    ['zzz', []],
  ])('%j', (q, want) => { expect(filterLocalActions(ctx(), q).map(a => a.id)).toEqual(want); });

  it('pin yalnız servis sayfasında', () => {
    expect(filterLocalActions(ctx(), 'pin').map(a => a.id)).toEqual([]);
    expect(filterLocalActions(ctx({ service: 's' }), 'pin').map(a => a.id)).toEqual(['pin-service']);
  });
});

describe('serviceFromLocation', () => {
  it.each([
    ['/service', '?name=checkout&range=1h', 'checkout'],
    ['/service/', '?name=a%20b', 'a b'],
    ['/service', '?name=', null],
    ['/services', '?name=x', null],
    ['/service/backtrace', '?name=x', null],
  ])('%s%s', (p, s, want) => { expect(serviceFromLocation(p, s)).toBe(want); });
});

describe('copy-link — pencere dondurulur', () => {
  it('göreli range → custom mutlak ms; diğer parametreler + hash aynen', () => {
    const out = shareLinkFor(ctx());
    expect(out.startsWith('https://cm.example/traces?service=a&range=')).toBe(true);
    expect(out.endsWith('#x')).toBe(true);
    const tok = new URL(out).searchParams.get('range');
    expect(decodeRange(tok, { preset: '15m' })).toEqual({ preset: 'custom', fromMs: 1_700_000_000_000 - 3_600_000, toMs: 1_700_000_000_000 });
  });
  it('range yok → URL değişmez', () => {
    expect(shareLinkFor(ctx({ href: 'https://cm.example/inbox?x=1' }))).toBe('https://cm.example/inbox?x=1');
  });
});

describe('askCosreQuestion', () => {
  it.each([[null, ''], ['checkout', 'checkout: ']])('%j', (s, want) => { expect(askCosreQuestion(s)).toBe(want); });
});

describe('runLocalAction — yan etkiler', () => {
  const fx = (): LocalActionEffects => ({
    copy: vi.fn(async () => true), prefill: vi.fn(), theme: vi.fn(), density: vi.fn(),
    shortcutsHelp: vi.fn(), togglePin: vi.fn(() => true), toast: vi.fn(),
  });
  it('her kimlik tek etkisine gider', async () => {
    const f = fx();
    await runLocalAction('copy-link', ctx(), f);
    expect(f.copy).toHaveBeenCalledWith(shareLinkFor(ctx()));
    expect(f.toast).toHaveBeenCalledWith('success', 'Link copied');
    await runLocalAction('ask-cosre', ctx({ service: 's' }), f);
    expect(f.prefill).toHaveBeenCalledWith('s: ');
    await runLocalAction('theme-light', ctx(), f);
    expect(f.theme).toHaveBeenCalledWith('light');
    await runLocalAction('density-dense', ctx(), f);
    expect(f.density).toHaveBeenCalledWith('dense');
    await runLocalAction('shortcuts-help', ctx(), f);
    expect(f.shortcutsHelp).toHaveBeenCalledOnce();
    await runLocalAction('pin-service', ctx({ service: 's' }), f);
    expect(f.togglePin).toHaveBeenCalledWith('s');
  });
  it('kopyalama başarısız → hata toast\'ı (sessiz "Copied" yok)', async () => {
    const f = { ...fx(), copy: vi.fn(async () => false) };
    await runLocalAction('copy-link', ctx(), f);
    expect(f.toast).toHaveBeenCalledWith('error', 'Copy failed');
  });
});

describe('tema / yoğunluk saklama anahtarları değişmedi', () => {
  it('applyDensity → coremetry-density + data-density + döngü DOM\'dan', () => {
    expect(DENSITY_STORAGE_KEY).toBe('coremetry-density');
    applyDensity('compact');
    expect(getRaw('coremetry-density')).toBe('compact');
    expect(document.documentElement.getAttribute('data-density')).toBe('compact');
    expect(currentDensity()).toBe('compact');
    expect(nextDensity('dense')).toBe('comfortable');
  });
  it('applyTheme → coremetry-theme', () => {
    applyTheme('light');
    expect(getRaw('coremetry-theme')).toBe('light');
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
  });
});

describe('sonuç listesi — etiket + sıra', () => {
  it('ACTIONS "changes state" taşır, yerel eylemler taşımaz ve onların ardından', () => {
    const q = 'disable';
    const rs = buildPaletteResults({
      query: q, pages: localizePages(k => k, false), services: [], endpoints: [], oracleOps: [], pivotSvcs: [],
      role: 'editor', seeAllLabel: '{q}', localActions: filterLocalActions(ctx(), 'copy'),
    });
    const acts = rs.filter(r => r.kind === 'action');
    expect(acts[0].action?.id).toBe('disable-rule');
    expect(acts[0].hint?.endsWith(`· ${CHANGES_STATE}`)).toBe(true);
    expect(acts[acts.length - 1]).toMatchObject({ local: 'copy-link', hint: localActionCatalog(ctx())[0].hint });
  });
});
