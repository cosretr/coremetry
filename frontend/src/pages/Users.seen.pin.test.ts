// Users.seen.pin.test.ts — v0.10.764.
//
// Operatör: "son görülme sütunu ekle" — kolon v0.8.403'ten beri vardı ama
// online satır damgayı "● online" rozetiyle GİZLİYORDU; iki yöneticinin
// farklı "online" sayısı görmesi (5 dk pencere, Redis damgası) ancak
// damga görünürse açıklanabilir. Bu pin online dalının da damgayı
// yazdığını çiviliyor (feedback-tested-but-unreachable).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'Users.tsx'), 'utf8');

function onlineBranch(): string {
  const a = src.indexOf('{u.online ? (');
  const b = src.indexOf(') : u.lastSeenAt ? (', a);
  expect(a).toBeGreaterThan(0);
  expect(b).toBeGreaterThan(a);
  return src.slice(a, b);
}

describe('Users — Last seen hücresi', () => {
  it('online satır rozetin yanında damgayı da yazar (bağıl + tam damga title)', () => {
    const br = onlineBranch();
    expect(br).toContain('● online');
    expect(br).toContain('tsRel(u.lastSeenAt)');
    expect(br).toContain('title={tsLong(u.lastSeenAt)}');
  });

  it('offline satır damgayı zaten yazıyor (iki dal, tek biçim)', () => {
    expect((src.match(/tsRel\(u\.lastSeenAt\)/g) ?? []).length).toBe(2);
  });
});
