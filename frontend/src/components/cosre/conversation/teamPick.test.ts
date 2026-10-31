// v0.10.1725 — "Takımını seç" kartının görünürlüğü SAF: karar sunucuda
// (needsTeamScope → data-scope teamPick); istemci yalnız işareti okur.
import { describe, it, expect } from 'vitest';
import type { CosreScope } from '@/lib/types';
import { parseDataScope } from '@/lib/cosre/protocol';
import { showTeamPick, teamServicesLabel, withTeamPick } from './teamPick';

const sc = (data: Record<string, unknown>): CosreScope | null => parseDataScope({ type: 'data-scope', data });

describe('showTeamPick', () => {
  it.each([
    ['takım biçimli + no_team (teamPick)', { unscoped: true, reason: 'no_team', count: 0, teamPick: true }, true],
    ['çapalı soru: no_team ama teamPick yok', { unscoped: true, reason: 'no_team', count: 0 }, false],
    ['kapsamlı (team)', { unscoped: false, reason: 'team', count: 3, team: 'payments', teamPick: true }, false],
    ['no_services (takım biliniyor)', { unscoped: true, reason: 'no_services', count: 0, team: 'payments', teamPick: true }, false],
    ['kaçış', { unscoped: true, reason: 'escape', count: 0, teamPick: true }, false],
    ['teamPick true değil (dizgi)', { unscoped: true, reason: 'no_team', count: 0, teamPick: 'true' }, false],
  ])('%s', (_n, data, want) => {
    expect(showTeamPick(sc(data))).toBe(want);
  });
  it('kapsam yok → kart yok', () => {
    expect(showTeamPick(null)).toBe(false);
    expect(showTeamPick(undefined)).toBe(false);
  });
});

describe('withTeamPick', () => {
  it('seçilen takım userTeam olarak eklenir; base korunur', () => {
    expect(withTeamPick({ env: 'prod' }, { userTeam: ' payments ' })).toEqual({ env: 'prod', userTeam: 'payments' });
    expect(withTeamPick(undefined, { userTeam: 'payments' })).toEqual({ userTeam: 'payments' });
  });
  it('kapsamsız bayrağı', () => {
    expect(withTeamPick({ env: 'prod' }, { unscoped: true })).toEqual({ env: 'prod', unscoped: true });
  });
  it('boş seçim / seçim yok → base aynen', () => {
    const base = { env: 'prod' };
    expect(withTeamPick(base, { userTeam: '  ' })).toBe(base);
    expect(withTeamPick(base, undefined)).toBe(base);
    expect(withTeamPick(undefined, undefined)).toBeUndefined();
  });
  it('satır etiketi', () => {
    expect(teamServicesLabel(12)).toBe('12 servis');
    expect(teamServicesLabel(undefined)).toBe('');
  });
});
