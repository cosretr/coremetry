import { describe, it, expect } from 'vitest';
import { prefToView, viewToPref, mergeBase, prefParamsMissingFromUrl } from './logsDisplayPrefs';
import { readLogsDisplay, LOGS_DEFAULT_DISPLAY } from './logsDisplay';
import { readLogsSample } from './logsSample';

// v0.10.1832 — öncelik tablosu: URL (link / kayıtlı arama) > kullanıcı tercihi > varsayılan.
const PREF = { v: 1 as const, rh: '3', hh: '2', den: 'expanded', pp: 250, smp: 1200 };

function effective(url: string, pref: typeof PREF | null) {
  const p = new URLSearchParams(url);
  // Sayfa akışı: önce eksik anahtarlar URL'ye yazılır, sonra URL okunur.
  for (const [k, v] of Object.entries(prefParamsMissingFromUrl(p, pref))) p.set(k, v);
  return {
    display: readLogsDisplay(p, mergeBase(LOGS_DEFAULT_DISPLAY, pref)).display,
    sample: readLogsSample(p.get('smp'), 2000).sample,
  };
}

describe('görünüm önceliği', () => {
  it.each([
    ['tercih yok, URL yok → varsayılan', '', null, { rowLines: 1, headerLines: 1, density: 'normal', pageSize: 100 }, 500],
    ['tercih var, URL yok → tercih', '', PREF, { rowLines: 3, headerLines: 2, density: 'expanded', pageSize: 250 }, 1200],
    ['URL tercihi ezer (kısmi)', 'rh=1&pp=50', PREF, { rowLines: 1, headerLines: 2, density: 'expanded', pageSize: 50 }, 1200],
    ['kayıtlı arama (URL) tümünü taşır', 'rh=2&hh=1&den=compact&pp=100&smp=300', PREF, { rowLines: 2, headerLines: 1, density: 'compact', pageSize: 100 }, 300],
    ['URL tercihsiz kullanıcıda da kazanır', 'den=compact', null, { rowLines: 1, headerLines: 1, density: 'compact', pageSize: 100 }, 500],
  ])('%s', (_n, url, pref, display, sample) => {
    expect(effective(url, pref)).toEqual({ display, sample });
  });

  it("tercihsiz kullanıcıda URL'ye hiçbir şey yazılmaz (eski linkler aynen)", () => {
    expect(prefParamsMissingFromUrl(new URLSearchParams('q=boom'), null)).toEqual({});
    expect(prefParamsMissingFromUrl(new URLSearchParams('rh=2&smp=40'), PREF)).toEqual({ hh: '2', den: 'expanded', pp: '250' });
  });

  it('bozuk tercih alanları düşer; gidiş-dönüş', () => {
    expect(prefToView({ v: 1, rh: '9', den: 'tight', pp: 7, smp: 3 })).toEqual({});
    const d = { rowLines: 'auto' as const, headerLines: 3 as const, density: 'compact' as const, pageSize: 500 as const };
    expect(prefToView(viewToPref(d, 900))).toEqual({ ...d, sample: 900 });
  });
});
