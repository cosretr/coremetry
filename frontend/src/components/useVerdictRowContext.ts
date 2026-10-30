import { useMemo } from 'react';
import { useAuth } from '@/components/AuthProvider';
import { useProblemVerdicts, useSetProblemVerdict } from '@/lib/queries';
import { verdictIndex, type VerdictIndex, type VerdictSubject } from '@/lib/problemVerdict';
import type { NoiseReasonChoice } from '@/components/NoiseReasonButton';
import type { ProblemVerdictInput, ProblemVerdictKind } from '@/lib/types';

// useVerdictRowContext — v0.10.1626 (perf denetimi §10 i). Liste tablosunun
// sahibi (Inbox) karar sorgusunu, imza dizinini ve mutation'ı BİR kez kurar;
// satır hücreleri (ProblemVerdictRow) yalnız bakar. Eskiden her satır kendi
// sorgu + mutation örneğini açıp verdictIndex'i satır başına yeniden kuruyordu.

/** PUT gövdesi: imza + üst veri; gerekçe yalnız "noise"te. */
export function verdictInput(subj: VerdictSubject, verdict: ProblemVerdictKind | '', noise?: NoiseReasonChoice): ProblemVerdictInput {
  return {
    signature: subj.signature, verdict, label: subj.label, kind: subj.kind, service: subj.service,
    priority: subj.priority, magnitude: subj.magnitude,
    ...(verdict === 'noise' && noise ? { reason: noise.reason, note: noise.note || undefined, permanent: noise.permanent || undefined } : {}),
  };
}

/** Tablo düzeyinde BİR kez kurulan karar bağlamı (ProblemVerdictRow). */
export interface VerdictRowContext {
  index: VerdictIndex;
  isEditor: boolean;
  isAdmin: boolean;
  /** Yazımı süren / son yazımı hata veren satırın imzası (tek mutation örneği). */
  pendingSig: string | null;
  errorSig: string | null;
  write: (input: ProblemVerdictInput) => void;
}

/** Tablonun sahibi çağırır: tek karar sorgusu + tek mutation + tek dizin. */
export function useVerdictRowContext(): VerdictRowContext {
  const { user } = useAuth();
  const q = useProblemVerdicts();
  const set = useSetProblemVerdict();
  const index = useMemo(() => verdictIndex(q.data?.verdicts), [q.data]);
  const sig = set.variables?.signature ?? null;
  const pendingSig = set.isPending ? sig : null;
  const errorSig = set.isError ? sig : null;
  const { mutate } = set;
  const isEditor = user?.role === 'admin' || user?.role === 'editor';
  const isAdmin = user?.role === 'admin';
  return useMemo(() => ({ index, isEditor, isAdmin, pendingSig, errorSig, write: (input: ProblemVerdictInput) => mutate(input) }),
    [index, isEditor, isAdmin, pendingSig, errorSig, mutate]);
}

