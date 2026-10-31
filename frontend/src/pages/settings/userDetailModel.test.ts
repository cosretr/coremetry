import { describe, expect, it } from 'vitest';
import { directoryAttributeRows, orgRows, userDetailTabFromParam, userSourceLabel, userStatus } from './userDetailModel';

// v0.10.1731 — kullanıcı detay sayfası saf yardımcıları.
describe('userDetailModel', () => {
  it('sekme URL değeri', () => {
    expect(userDetailTabFromParam(null)).toBe('overview');
    expect(userDetailTabFromParam('teams')).toBe('teams');
    expect(userDetailTabFromParam('changes')).toBe('changes');
    expect(userDetailTabFromParam('bogus')).toBe('overview');
  });
  it('dizin sekmesi ve organizasyon satırları (v0.10.1733)', () => {
    expect(userDetailTabFromParam('directory')).toBe('directory');
    expect(userDetailTabFromParam('permissions')).toBe('permissions');
    expect(userDetailTabFromParam('notifications')).toBe('notifications');
    expect(directoryAttributeRows({ title: ['Müdür'], Department: ['Teknoloji'], memberOf: ['a', 'b'] }))
      .toEqual([{ k: 'Department', v: 'Teknoloji' }, { k: 'memberOf', v: 'a\nb' }, { k: 'title', v: 'Müdür' }]);
    const rows = orgRows({ self: 'me', managers: ['m1', 'm2'], reports: ['r1'] });
    expect(rows.map(r => [r.p, r.depth, r.role])).toEqual([
      ['m2', 0, 'manager'], ['m1', 1, 'manager'], ['me', 2, 'self'], ['r1', 3, 'report'],
    ]);
  });
  it('hesap kaynağı', () => {
    expect(userSourceLabel('ldap')).toBe('AD (LDAP)');
    expect(userSourceLabel('oidc')).toBe('SSO (OIDC)');
    expect(userSourceLabel('local')).toBe('Yerel');
    expect(userSourceLabel(undefined)).toBe('Yerel');
  });
  it('durum: engelli disabled\'ı da taşır ama Engelli okunur', () => {
    expect(userStatus({ disabled: true, blocked: true }).label).toBe('Engelli');
    expect(userStatus({ disabled: true }).label).toBe('Pasif');
    expect(userStatus({ disabled: false }).status).toBe('ok');
  });
});
