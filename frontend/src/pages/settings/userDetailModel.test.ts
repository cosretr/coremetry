import { describe, expect, it } from 'vitest';
import { userDetailTabFromParam, userSourceLabel, userStatus } from './userDetailModel';

// v0.10.1731 — kullanıcı detay sayfası saf yardımcıları.
describe('userDetailModel', () => {
  it('sekme URL değeri', () => {
    expect(userDetailTabFromParam(null)).toBe('overview');
    expect(userDetailTabFromParam('teams')).toBe('teams');
    expect(userDetailTabFromParam('changes')).toBe('changes');
    expect(userDetailTabFromParam('bogus')).toBe('overview');
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
