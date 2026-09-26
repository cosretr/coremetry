// waitingReason.ts — v0.10.929 (K5) — konteyner `waiting` nedeninin tonu (SAF).
//
// K5: normal hâl nötr, renk yalnız sapmada. `waiting` bir konteynerin
// başlangıç akışının olağan durağı da olabilir (ContainerCreating: imaj
// çekiliyor / sandbox kuruluyor; PodInitializing: init konteynerleri
// koşuyor) — bunlar bir GEÇİŞ, arıza değil: nötr rozet. Kubelet'in hata
// nedenleri (CrashLoopBackOff, ImagePull*, *ConfigError, RunContainerError …)
// konteyneri kalıcı olarak ayakta tutmayan bir sapma: kırmızı.
//
// Bilinmeyen neden → danger (önceki davranış korunur): `waiting` tanım
// gereği "koşmuyor" demek; tanımadığımız bir nedeni sessizce nötre çekmek
// bir arızayı gizleyebilir. Nötre yalnız bilinen olağan geçişler iner.
import type { Tone } from '@/components/ui/Badge';

/** Başlangıç akışının olağan durakları — geçici, arıza değil. */
export const TRANSIENT_WAITING_REASONS: ReadonlySet<string> = new Set([
  'ContainerCreating',
  'PodInitializing',
]);

/**
 * Kubelet'in bilinen arıza nedenleri (kubelet/container sync_result +
 * images/types). Belgeleme + test içindir: sınıflandırmada bilinmeyen
 * nedenler de danger'a düşer (başlık yorumu).
 */
export const FAILURE_WAITING_REASONS: ReadonlySet<string> = new Set([
  'CrashLoopBackOff',
  'ImagePullBackOff',
  'ErrImagePull',
  'ErrImageNeverPull',
  'InvalidImageName',
  'ImageInspectError',
  'RegistryUnavailable',
  'SignatureValidationFailed',
  'CreateContainerConfigError',
  'CreateContainerError',
  'RunContainerError',
  'PreStartHookError',
  'PostStartHookError',
  'PreCreateHookError',
  'KillContainerError',
  'VerifyNonRootError',
]);

export function waitingReasonTone(reason: string): Extract<Tone, 'neutral' | 'danger'> {
  return TRANSIENT_WAITING_REASONS.has(reason.trim()) ? 'neutral' : 'danger';
}
