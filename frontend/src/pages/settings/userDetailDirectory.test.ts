// v0.10.1741 (operator-reported) — "AD ile denetle" sonrası fark paneli yeni
// değerleri gösterdi, kartlar "—" kaldı; yönetici ham DN'di. Denetim cevabının
// `after`'ı (sunucunun yazdığı satır) detay önbelleğine konur ve users.all
// tazelenir; yönetici etiketi çözülmüş ad (+ sicil), ham DN asla.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { UserDetail, UserDetailUser, UserDirectoryCheck } from '@/lib/types';
import { managerLabel, withDirectoryCheck } from './userDetailModel';

const user = (over: Partial<UserDetailUser> = {}): UserDetailUser =>
  ({ id: 'u1', email: 'user1@example.com', role: 'viewer', authProvider: 'ldap', ...over } as UserDetailUser);

const detail = (u: UserDetailUser): UserDetail => ({ user: u, groups: [], groupSyncEnabled: false, online: false });

describe('withDirectoryCheck', () => {
  const after = user({ directory: { title: 'Uzman', employeeId: 'E100', office: 'Ofis 1' } });
  const check = (over: Partial<UserDirectoryCheck> = {}): UserDirectoryCheck =>
    ({ before: user(), after, diff: [], written: true, directoryStored: true, ...over });

  it('yazılan satır kartlara gider', () => {
    expect(withDirectoryCheck(detail(user()), check())?.user.directory?.title).toBe('Uzman');
  });
  it('yazım yoksa / başka kullanıcı / önbellek yoksa dokunulmaz', () => {
    const old = detail(user());
    expect(withDirectoryCheck(old, check({ written: false }))).toBe(old);
    expect(withDirectoryCheck(old, check({ after: user({ id: 'u2' }) }))).toBe(old);
    expect(withDirectoryCheck(undefined, check())).toBeUndefined();
  });
});

describe('managerLabel', () => {
  const dn = 'CN=Manager One,OU=Users,DC=example,DC=com';
  it.each([
    ['çözülmüş ad + sicil', { name: 'Manager One Display', employeeId: 'E200' }, { managerDn: dn, managerName: 'Manager One' }, 'Manager One Display · E200'],
    ['çözülmüş ad, sicil yok', { name: 'Manager One Display' }, { managerDn: dn }, 'Manager One Display'],
    ['çözüm yok: saklı CN', undefined, { managerDn: dn, managerName: 'Manager One' }, 'Manager One'],
    ['yalnız DN: gösterilmez', undefined, { managerDn: dn }, undefined],
    ['yönetici yok', undefined, {}, undefined],
  ])('%s', (_n, manager, dir, want) => {
    const got = managerLabel(manager, dir);
    expect(got).toBe(want);
    expect(got ?? '').not.toContain('=');
  });
});

describe('UserDetail bağlantısı', () => {
  const src = readFileSync(resolve(__dirname, 'UserDetail.tsx'), 'utf8');
  it('denetim cevabı detay önbelleğine yazılır ve users.all tazelenir', () => {
    const run = src.slice(src.indexOf('const runCheck'), src.indexOf('const groupsState'));
    expect(run).toMatch(/qc\.setQueryData<UserDetailT>\(keys\.users\.detail\(u\.id\), old => withDirectoryCheck\(old, r\)\)/);
    expect(run).toMatch(/if \(r\.written\) refresh\(\)/);
    expect(src).toMatch(/const refresh = \(\) => \{ void qc\.invalidateQueries\(\{ queryKey: keys\.users\.all \}\); \};/);
  });
  it('Kuruluş kartı yöneticiyi DN ile göstermez', () => {
    expect(src).toContain("{ k: 'Bağlı olduğu yönetici', v: managerLabel(q.data.manager, dir) }");
    expect(src).not.toMatch(/dir\.managerName \|\| dir\.managerDn/);
  });
});
