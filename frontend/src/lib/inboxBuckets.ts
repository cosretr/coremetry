// inboxBuckets.ts — v0.10.1803 (operatör 2026-11-05: "Oracle P1 problemse
// Problems'a dönsün; ama 30'dan fazla hata olursa P1 olsun").
//
// Oracle hata tablosu grubunun (`ora:` parmak izi) P1 exception satırı, tür
// süzgecinde "problem" seçiliyken Exceptions çipi kapalı olsa da görünür.
// Sunucu internal/api/inbox.go inboxRowInProblemBucket ile AYNI kural.
import type { InboxItem } from './types';

/** Oracle grubu parmak izi öneki — chstore.OracleGroupPrefix. */
export const ORACLE_GROUP_PREFIX = 'ora:';

type BucketRow = Pick<InboxItem, 'kind' | 'priority' | 'exception'>;

export function rowInProblemBucket(it: BucketRow): boolean {
  return it.kind === 'exception' && it.priority === 'P1' &&
    !!it.exception?.fingerprint.startsWith(ORACLE_GROUP_PREFIX);
}

/** Satır tür süzgecinden geçer mi: kendi türü seçili ya da "problem" kovasında. */
export function rowPassesKindFacet(it: BucketRow, kinds: ReadonlySet<string>): boolean {
  return kinds.has(it.kind) || (kinds.has('problem') && rowInProblemBucket(it));
}
