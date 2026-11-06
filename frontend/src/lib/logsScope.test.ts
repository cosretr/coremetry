// logsScope — v0.10.1855 (Log Search Faz 2 dilim 2.4).
import { describe, it, expect } from 'vitest';
import { decideLogsScope, parseLogsScope, logsScopeRequestParam, logsScopeGate, scopeHealthLabel, type LogsScopeDecision } from './logsScope';
import type { LogsScope } from '@/lib/types';

const MINE: LogsScope = { available: true, scoped: true, reason: 'team', team: 'orders-team', services: [{ name: 'orders-api' }] };
const NOTEAM: LogsScope = { available: true, scoped: false, reason: 'no_team', services: [] };
const CH: LogsScope = { available: false, scoped: false, reason: 'unavailable', services: [] };

describe('decideLogsScope', () => {
  const cases: [string, Parameters<typeof decideLogsScope>[0], LogsScopeDecision][] = [
    ['açık servis kazanır', { urlScope: 'mine', urlHadFilters: false, service: 'orders-api', scope: MINE }, { kind: 'off' }],
    ['scope=all', { urlScope: 'all', urlHadFilters: false, service: '', scope: MINE }, { kind: 'off' }],
    ['süzgeçli derin link', { urlScope: '', urlHadFilters: true, service: '', scope: MINE }, { kind: 'off' }],
    ['yükleniyor → bekle', { urlScope: '', urlHadFilters: false, service: '', scope: undefined }, { kind: 'pending' }],
    ['uç hatası → bugünkü', { urlScope: '', urlHadFilters: false, service: '', scope: null }, { kind: 'off' }],
    ['CH log yolu → kapalı', { urlScope: 'mine', urlHadFilters: false, service: '', scope: CH }, { kind: 'off' }],
    ['temiz açılış → yaz', { urlScope: '', urlHadFilters: false, service: '', scope: MINE }, { kind: 'apply' }],
    ['URL mine → etkin', { urlScope: 'mine', urlHadFilters: true, service: '', scope: MINE }, { kind: 'active' }],
    ['takımsız → tüm servisler', { urlScope: '', urlHadFilters: false, service: '', scope: NOTEAM }, { kind: 'fallback-all', reason: 'no_team' }],
    ['URL mine + takımsız → tüm servisler', { urlScope: 'mine', urlHadFilters: false, service: '', scope: NOTEAM }, { kind: 'fallback-all', reason: 'no_team' }],
    ['servissiz takım → tüm servisler', { urlScope: '', urlHadFilters: false, service: '', scope: { ...MINE, services: [] } }, { kind: 'fallback-all', reason: 'team' }],
  ];
  it.each(cases)('%s', (_n, input, want) => { expect(decideLogsScope(input)).toEqual(want); });

  it('istek parametresi ve kapı', () => {
    expect(logsScopeRequestParam({ kind: 'apply' })).toBe('mine');
    expect(logsScopeRequestParam({ kind: 'active' })).toBe('mine');
    expect(logsScopeRequestParam({ kind: 'off' })).toBeUndefined();
    expect(logsScopeGate({ kind: 'pending' })).toBe(true);
    // v0.10.1868 — kapsam çözülemezse okuma BEKLETİLMEZ ve kapsam parametresi gitmez.
    expect(logsScopeGate({ kind: 'fallback-all', reason: 'no_team' })).toBe(false);
    expect(logsScopeRequestParam({ kind: 'fallback-all', reason: 'no_team' })).toBeUndefined();
    expect(logsScopeGate({ kind: 'apply' })).toBe(false);
  });

  it('parseLogsScope', () => {
    expect(parseLogsScope('mine')).toBe('mine');
    expect(parseLogsScope('all')).toBe('all');
    expect(parseLogsScope('team')).toBe('');
    expect(parseLogsScope(null)).toBe('');
  });
});

describe('scopeHealthLabel', () => {
  it('her sağlık durumu', () => {
    expect(scopeHealthLabel({ name: 'a', health: 'green', errorRate: 0.2 })).toBe('sağlıklı · %0,2 hata');
    expect(scopeHealthLabel({ name: 'a', health: 'green' })).toBe('sağlıklı · %0 hata');
    expect(scopeHealthLabel({ name: 'a', health: 'yellow', errorRate: 3.14 })).toBe('bozulmuş · %3,1 hata');
    expect(scopeHealthLabel({ name: 'a', health: 'red', errorRate: 12, openProblems: 1 })).toBe('sorunlu · %12 hata · 1 açık Problem');
    expect(scopeHealthLabel({ name: 'a' })).toBe('son 5 dk span yok');
  });
});
