import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { podCpuText, podMemText, podLimitHint, podStatusText } from './podUsage';

// v0.10.2194 — pod kullanımı OpenShift Console gibi mutlak; limit yüzdesi ipucu.
describe('pod kullanım hücreleri', () => {
  it('CPU çekirdek, 3 ondalık', () => {
    expect(podCpuText(0.0041, 'en')).toBe('0.004 cores');
    expect(podCpuText(1.25, 'en')).toBe('1.250 cores');
    expect(podCpuText(1.25, 'tr')).toBe('1,250 çekirdek');
    expect(podCpuText(undefined, 'en')).toBe('');
  });
  it('bellek ikili birim, 1 ondalık', () => {
    expect(podMemText(248.3 * 1024 ** 2)).toBe('248.3 MiB');
    expect(podMemText(1.2 * 1024 ** 3)).toBe('1.2 GiB');
    expect(podMemText(0)).toBe('0 B');
  });
  it('limit yüzdesi yalnız limit varken', () => {
    expect(podLimitHint(45.4, 'en')).toBe('· 45% of limit');
    expect(podLimitHint(45.4, 'tr')).toBe('· limit: %45');
    expect(podLimitHint(undefined, 'en')).toBe('');
    expect(podLimitHint(0, 'tr')).toBe('');
  });
  it('durum i18n: EN faz adı, TR Türkçe', () => {
    expect(podStatusText({ phase: 'Running' }, 'en')).toBe('Running');
    expect(podStatusText({ phase: 'Pending' }, 'en')).toBe('Pending');
    expect(podStatusText({ phase: 'Failed' }, 'en')).toBe('Failed');
    expect(podStatusText({ phase: 'Running' }, 'tr')).not.toBe('Running');
  });
  it('Pods sekmesi ve namespace detayı bu yardımcılardan geçer', () => {
    const page = readFileSync(new URL('../Clusters.tsx', import.meta.url), 'utf8');
    expect(page).toContain("case 'phase': return podStatusText(r, lang)");
    expect(page).toContain("podCpuText(r.cpuCores, lang)");
    expect(page).toContain("hidden: ['cluster', 'cpuPct', 'memPct',");
    const ns = readFileSync(new URL('./NamespaceDetail.tsx', import.meta.url), 'utf8');
    expect(ns).toContain("podMemText(r.memBytes)");
    expect(ns).not.toContain("'Mem / limit'");
  });
});
