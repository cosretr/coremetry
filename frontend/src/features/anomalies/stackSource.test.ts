// stackSource.test.ts — v0.10.1048 (operatör: "Exception sayfasındaki dosya
// bağlantıları hâlâ daldan açılıyor; kod incelemesi artık sürümden okuyor.
// İkisi aynı yere baksın."). Stack ve frame linklerinin sürümü AYNI örnekten;
// linklerin gittiği ref AI panelinin kelimeleriyle (codeSourceRef).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { ExceptionSample, StackFramesResult } from '@/lib/types';
import { representativeStack, frameLinksRef } from './stackSource';

const sample = (p: Partial<ExceptionSample>): ExceptionSample => ({
  traceId: 't', spanId: 's', time: 0, message: 'm', stacktrace: '', spanName: 'op', statusMsg: '', ...p,
});

describe('representativeStack — stack ve sürüm aynı örnekten', () => {
  it('stack taşıyan ilk örnek ve ONUN sürümü; en yeni (stack\'siz) örneğin sürümü yamanmaz', () => {
    const r = representativeStack([
      sample({ traceId: 'newest', stacktrace: '', runningVersion: '2.0.0' }),
      sample({ traceId: 'older', stacktrace: 'java.lang.IllegalStateException: x\n\tat a.B.c(B.java:1)', runningVersion: '1.4.2' }),
      sample({ traceId: 'oldest', stacktrace: 'other', runningVersion: '1.4.1' }),
    ]);
    expect(r.stack.startsWith('java.lang.IllegalStateException')).toBe(true);
    expect(r.version).toBe('1.4.2');
  });
  it('stack\'li örnek sürüm taşımıyorsa sürüm boş — başka örneğin sürümüne düşülmez', () => {
    const r = representativeStack([
      sample({ stacktrace: 'S', runningVersion: undefined }),
      sample({ stacktrace: 'S2', runningVersion: '1.4.2' }),
    ]);
    expect(r).toEqual({ stack: 'S', version: '' });
  });
  it('eski sunucu (alan yok) / boşluk / örnek yok', () => {
    expect(representativeStack([sample({ stacktrace: 'S' })])).toEqual({ stack: 'S', version: '' });
    expect(representativeStack([sample({ stacktrace: 'S', runningVersion: ' 1.4.2 ' })]).version).toBe('1.4.2');
    expect(representativeStack([])).toEqual({ stack: '', version: '' });
    expect(representativeStack([sample({ runningVersion: '1.4.2' })])).toEqual({ stack: '', version: '' });
  });
});

describe('frameLinksRef — linklerin gittiği ref (codeSourceRef)', () => {
  const linked = (p: Partial<StackFramesResult>): StackFramesResult => ({
    configured: true, branch: 'release', revisionWarning: 'w',
    frames: [{ lineIndex: 1, class: 'a.B', method: 'c', file: 'a/B.java', line: 1, isApp: true, tier: 1, url: 'https://scm.example.invalid/x' }],
    ...p,
  });
  it('sürüm commit\'e bağlandı → "· 1.4.2 (çalışan sürüm)"', () => {
    expect(frameLinksRef(linked({ revision: { version: '1.4.2', ref: 'tags/1.4.2', sha: 'c0ffee00', verified: true } })))
      .toBe(' · 1.4.2 (çalışan sürüm)');
  });
  it('sürüm bulunamadı ya da yok → dal: "· release (dal)"', () => {
    expect(frameLinksRef(linked({ revision: { version: '9.9.9', verified: false, note: 'tags/9.9.9 bulunamadı' } })))
      .toBe(' · release (dal)');
    expect(frameLinksRef(linked({}))).toBe(' · release (dal)');
  });
  it('link çizilmediyse ya da ayarsızsa ek yok', () => {
    expect(frameLinksRef(undefined)).toBe('');
    expect(frameLinksRef({ configured: false, revisionWarning: '', frames: [] })).toBe('');
    expect(frameLinksRef(linked({ frames: [{ lineIndex: 1, class: 'a.B', method: 'c', file: 'a/B.java', line: 1, isApp: true, tier: 1, reason: 'yok' }] }))).toBe('');
    // Kütüphane frame'inin URL'si StackTrace'te link sayılmaz (hasLink yüklemi).
    expect(frameLinksRef(linked({ frames: [{ lineIndex: 1, class: 'org.X', method: 'c', file: 'X.java', line: 1, isApp: false, tier: 3, url: 'https://scm.example.invalid/y' }] }))).toBe('');
  });
});

// Kaynak pini: sayfa stack'i ve sürümü TEK yardımcıdan alır, sürümü frame-links
// sorgusuna verir, ref'i başlığın alt yazısına ekler; düzen aynı.
describe('ProblemDetail kablolaması (v0.10.1048)', () => {
  const src = readFileSync(resolve(__dirname, 'ProblemDetail.tsx'), 'utf8');
  it('stack + sürüm representativeStack(samples) ile; sorguya version gider', () => {
    expect(src).toContain('const { stack, version: stackVersion } = representativeStack(samples);');
    expect(src).toContain('useStackFrameLinks({ service: group.service, stack: stackNorm, version: stackVersion, enabled: !!stackNorm })');
    // İkinci bir seçim yolu yok: sayfa kendi sürüm zincirini kurmaz.
    expect(src).not.toContain('samples.find(s => s.stacktrace)');
    expect(src).not.toMatch(/runningVersion\(/);
  });
  it('ref eki mevcut alt yazının sonunda; codeSourceRef üzerinden', () => {
    expect(src).toContain('const linkRef = frameLinksRef(framed);');
    expect(src).toContain("<span className=\"ov-sub\">representative sample{stackLines.length > 0 ? ` · ${stackLines.length} satır` : ''}{linkRef}</span>");
    const helper = readFileSync(resolve(__dirname, 'stackSource.ts'), 'utf8');
    expect(helper).toContain("import { codeSourceRef } from '@/components/codeSourceRef';");
    expect(helper).toContain('return codeSourceRef({ version: r.revision?.verified ? r.revision.version : undefined, branch: r.branch });');
  });
});
