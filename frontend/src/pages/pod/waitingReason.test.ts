import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { waitingReasonTone, TRANSIENT_WAITING_REASONS, FAILURE_WAITING_REASONS } from './waitingReason';

// v0.10.929 (K5) — konteyner waiting nedeni: olağan başlangıç geçişi nötr,
// kubelet arıza nedenleri kırmızı. Eskiden HER waiting nedeni kırmızıydı
// (ContainerCreating dahil — yeni açılan her pod bir an "arıza" gösterirdi).
describe('waitingReasonTone', () => {
  const cases: Array<[string, 'neutral' | 'danger']> = [
    ['ContainerCreating', 'neutral'],
    ['PodInitializing', 'neutral'],
    [' ContainerCreating ', 'neutral'],
    ['CrashLoopBackOff', 'danger'],
    ['ImagePullBackOff', 'danger'],
    ['ErrImagePull', 'danger'],
    ['ErrImageNeverPull', 'danger'],
    ['InvalidImageName', 'danger'],
    ['CreateContainerConfigError', 'danger'],
    ['CreateContainerError', 'danger'],
    ['RunContainerError', 'danger'],
    ['PostStartHookError', 'danger'],
    // Bilinmeyen neden: waiting = koşmuyor; sessizce nötre çekilmez.
    ['SomeFutureReason', 'danger'],
    // Büyük/küçük harf farkı kubelet'ten gelmez; gelirse tanımadık say.
    ['containercreating', 'danger'],
  ];
  for (const [reason, tone] of cases) {
    it(`${JSON.stringify(reason)} → ${tone}`, () => {
      expect(waitingReasonTone(reason)).toBe(tone);
    });
  }

  it('geçici ve arıza kümeleri ayrık; her arıza nedeni danger', () => {
    for (const r of FAILURE_WAITING_REASONS) {
      expect(TRANSIENT_WAITING_REASONS.has(r)).toBe(false);
      expect(waitingReasonTone(r)).toBe('danger');
    }
    for (const r of TRANSIENT_WAITING_REASONS) expect(waitingReasonTone(r)).toBe('neutral');
  });

  it('PodContainersTable waiting rozeti tonu helper\'dan alır (sabit danger yok)', () => {
    const src = readFileSync(resolve(__dirname, 'PodContextTables.tsx'), 'utf8');
    expect(src).toContain('<Badge tone={waitingReasonTone(c.waitingReason)}>{c.waitingReason}</Badge>');
    expect(src).not.toContain('<Badge tone="danger">{c.waitingReason}</Badge>');
  });
});
