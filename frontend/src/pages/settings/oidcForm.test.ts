// oidcForm.test — v0.10.1067 (OIDC Settings'ten yönetilir) saf yardımcılar.
import { describe, it, expect } from 'vitest';
import {
  formToInput, oidcStatus, parsePermissionTTL, publicOidcSnapshot, snapshotToForm, splitList,
} from './oidcForm';
import type { OidcSettingsSnapshot } from '@/lib/types';

const snap: OidcSettingsSnapshot = {
  enabled: true, issuerUrl: 'https://idp.example.test', clientId: 'coremetry',
  clientSecretStored: true, redirectUrl: 'https://apm.example.test/api/auth/oidc/callback',
  scopes: ['openid', 'email'], displayName: 'Kurumsal SSO', defaultRole: 'editor',
  allowedDomains: ['example.test', 'corp.example.test'], source: 'settings', active: true,
  permissionServiceEnabled: false, permissionServiceKeySet: false, permissionTTLSeconds: 300,
  permissionsClaim: 'permissions', roleFromClaim: false,
};

describe('oidcForm', () => {
  it('splitList: virgül/boşluk/satır ayracı, tekrarsız', () => {
    expect(splitList(' openid, email\nprofile  email ')).toEqual(['openid', 'email', 'profile']);
    expect(splitList('')).toEqual([]);
  });

  it('snapshotToForm secret alanını HER ZAMAN boş başlatır', () => {
    const f = snapshotToForm(snap);
    expect(f.clientSecret).toBe('');
    expect(f.scopes).toBe('openid email');
    expect(f.allowedDomains).toBe('example.test, corp.example.test');
  });

  it('formToInput: boş secret gövdeye girmez (sunucu kayıtlıyı korur), dolu secret girer', () => {
    const f = snapshotToForm(snap);
    expect('clientSecret' in formToInput(f)).toBe(false);
    expect(formToInput({ ...f, clientSecret: '  yeni  ' }).clientSecret).toBe('yeni');
  });

  it('formToInput: listeler bölünür, alan adları küçük harf', () => {
    const inp = formToInput({ ...snapshotToForm(snap), allowedDomains: 'Example.TEST,  Corp.Example.Test', scopes: 'openid,email groups' });
    expect(inp.allowedDomains).toEqual(['example.test', 'corp.example.test']);
    expect(inp.scopes).toEqual(['openid', 'email', 'groups']);
  });

  it('publicOidcSnapshot: viewer görünümü boş değil, yalnız public alanlar', () => {
    const p = publicOidcSnapshot({ local: { enabled: true }, oidc: { enabled: true, displayName: 'Kurumsal SSO' } });
    expect(p).toMatchObject({ enabled: true, active: true, displayName: 'Kurumsal SSO', clientSecretStored: false, issuerUrl: '' });
  });

  it('oidcStatus: açık / bağlanamadı / kapalı', () => {
    expect(oidcStatus(snap).label).toBe('AÇIK');
    expect(oidcStatus({ ...snap, active: false }).label).toBe('BAĞLANAMADI');
    expect(oidcStatus({ ...snap, enabled: false, active: false }).label).toBe('KAPALI');
  });
});

// v0.10.1110 — yetki servisi alanları.
describe('oidcForm — yetki servisi', () => {
  it('anahtar formda HER ZAMAN boş; boşsa gövdeye girmez, doluysa kırpılıp girer', () => {
    const f = snapshotToForm({ ...snap, permissionServiceEnabled: true, permissionServiceKeySet: true });
    expect(f.permissionServiceKey).toBe('');
    expect('permissionServiceKey' in formToInput(f)).toBe(false);
    expect(formToInput({ ...f, permissionServiceKey: '  9d1f6b2e7a4c3058e1b2 ' }).permissionServiceKey).toBe('9d1f6b2e7a4c3058e1b2');
  });

  it('TTL: boş/geçersiz → 300, [60, 3600] kıskacı', () => {
    expect(parsePermissionTTL('')).toBe(300);
    expect(parsePermissionTTL('abc')).toBe(300);
    expect(parsePermissionTTL('0')).toBe(300);
    expect(parsePermissionTTL('30')).toBe(60);
    expect(parsePermissionTTL('120')).toBe(120);
    expect(parsePermissionTTL('99999')).toBe(3600);
  });

  it('claim adı boşsa permissions; roleFromClaim varsayılan KAPALI', () => {
    const inp = formToInput({ ...snapshotToForm(snap), permissionsClaim: '  ' });
    expect(inp.permissionsClaim).toBe('permissions');
    expect(inp.roleFromClaim).toBe(false);
    const p = publicOidcSnapshot({ local: { enabled: true }, oidc: { enabled: true, displayName: 'SSO' } });
    expect(p.roleFromClaim).toBe(false);
    expect(p.permissionServiceKeySet).toBe(false);
  });
});
