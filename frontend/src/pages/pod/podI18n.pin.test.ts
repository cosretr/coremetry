// podI18n.pin.test.ts — v0.10.2246: pod detay sayfası arayüz dilinde.
// Katalog: her podpage.* anahtarı iki dilde de var, İngilizce değerde Türkçe
// harf yok. Saf yardımcılar: EN çağrıda İngilizce, TR çağrıda bugünkü metin.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { t } from '@/lib/i18n';
import { podSentence, podStatusWordText, siblingSentence } from './podSentence';
import { podAgeText, podCosreQuestion } from './podMeta';

// v0.10.2307 — EN gövdesi lib/i18n.en.ts'te: iki dosya birlikte = iki katalog.
const src = readFileSync(new URL('../../lib/i18n.tr.ts', import.meta.url), 'utf8')
  + readFileSync(new URL('../../lib/i18n.en.ts', import.meta.url), 'utf8');
const keys = [...new Set([...src.matchAll(/^ {2}'(podpage\.[\w.]+)':/gm)].map(m => m[1]))];

describe('podpage.* katalog', () => {
  it('anahtarlar iki katalogda da tanımlı', () => {
    expect(keys.length).toBeGreaterThan(150);
    for (const k of keys) {
      const n = [...src.matchAll(new RegExp(`^ {2}'${k.replace(/\./g, '\\.')}':`, 'gm'))].length;
      expect(n, k).toBe(2);
    }
  });
  it('İngilizce değerlerde Türkçe harf yok', () => {
    const bad = keys.filter(k => /[çğıöşüÇĞİÖŞÜ]/.test(t(k, 'en')));
    expect(bad).toEqual([]);
  });
  it('OpenShift Console sözlüğü', () => {
    expect(t('podpage.infraTitle', 'en')).toBe('CPU / Memory · Thanos');
    expect(t('podpage.col.mem', 'en')).toBe('Memory');
    expect(t('podpage.infraTitle', 'tr')).toBe('CPU / Bellek · Thanos');
  });
});

describe('saf yardımcılar iki dilde', () => {
  const row = { phase: 'Running', restarts: 7, memPct: 99 };
  it('durum sözcüğü ve cümle', () => {
    expect(podStatusWordText('Sağlıklı', 'en')).toBe('Healthy');
    expect(podStatusWordText('Sorunlu', 'tr')).toBe('Sorunlu');
    expect(podSentence(row, undefined, 'en')).toBe('Running, 7 restarts in total, at 99% of its memory limit.');
    expect(podSentence(row, undefined, 'tr')).toBe("Çalışıyor, toplam 7 yeniden başlama, bellek limitinin %99'unda.");
    expect(podSentence({ phase: 'Pending' }, undefined, 'en')).toBe('Pending.');
  });
  it('kardeş cümlesi, yaş, CoSRE sorusu', () => {
    expect(siblingSentence({ memPct: 99 }, [{ memPct: 70 }, { memPct: 74 }], 'en'))
      .toBe('Memory median of the 2 pods in the same workload is 72%, this pod 99% — highest.');
    expect(siblingSentence({ memPct: 99 }, [{ memPct: 70 }, { memPct: 74 }], 'tr'))
      .toBe("Aynı iş yükündeki 2 pod'un bellek ortancası %72, bu pod %99 — en yüksek.");
    const now = Date.parse('2026-11-15T12:00:00Z');
    expect(podAgeText('2026-11-12T12:00:00Z', now, 'en')).toBe('3 days');
    expect(podAgeText('2026-11-15T11:00:00Z', now, 'en')).toBe('1 hour');
    expect(podAgeText('2026-11-12T12:00:00Z', now, 'tr')).toBe('3 gün');
    expect(podCosreQuestion({ cluster: 'c', namespace: 'n', pod: 'p', service: 's' }, 'en'))
      .toBe('What is the status of pod p, is there a problem? (cluster c, namespace n, service s)');
  });
});
