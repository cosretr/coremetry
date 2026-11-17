// CosreAliasPanel — v0.10.2335 (conversation-audit §6.1 G). Kullanıcının
// KENDİ eş adları: v2 sohbetin başlığındaki «Eş adlarım» düğmesiyle açılan
// küçük liste (Geçmiş paneliyle aynı yer). Yalnız görme ve silme; ekleme
// yalnız sohbetteki öneri kartının Kaydet'iyle. Bayat eşleme (servis artık
// katalogda yok) işaretli — CoSRE onu kullanmaz. Liste ≤50 satır, düzenlenebilir
// liste olduğu için sıralanmaz (tablo standardı: düz liste).
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { useConfirm } from '@/components/ui/ConfirmDialog';
import { api } from '@/lib/api';
import { COSRE_USER_PREFS_KEY, useCosreUserPrefs } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { COSRE_V2_STRINGS as T } from './strings';

export function CosreAliasPanel() {
  const q = useCosreUserPrefs(true);
  const qc = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  const confirm = useConfirm();
  const remove = async (phrase: string) => {
    if (!await confirm({
      title: T.aliasesDeleteTitle,
      body: <>«{phrase}» eşlemesi silinecek; CoSRE bu ifadeyi yeniden katalogdan çözer.</>,
      confirmLabel: T.aliasesDelete,
      danger: true,
    })) return;
    setBusy(phrase);
    try {
      await api.deleteCosreAlias(phrase);
      await qc.invalidateQueries({ queryKey: COSRE_USER_PREFS_KEY });
    } catch (e) {
      toast.error(`${T.aliasesDeleteError}: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(null);
    }
  };
  const list = q.data?.aliases ?? [];
  return (
    <section className="border-b border-border px-4 py-3 text-sm" aria-label={T.aliasesTitle} data-testid="cosre-alias-panel">
      <div className="font-semibold">{T.aliasesTitle}</div>
      <p className="mt-1 text-xs text-muted-foreground">{T.aliasesIntro}</p>
      {q.isLoading && <p className="mt-2 text-xs text-muted-foreground">{T.aliasesLoading}</p>}
      {q.isError && <p className="mt-2 text-xs text-destructive">{T.aliasesError}</p>}
      {q.isSuccess && list.length === 0 && <p className="mt-2 text-xs text-muted-foreground">{T.aliasesEmpty}</p>}
      {list.length > 0 && (
        <ul className="mt-2 flex flex-col gap-1">
          {list.map(a => (
            <li key={a.phrase} className="flex items-center gap-2" data-stale={a.stale || undefined}>
              <span className="min-w-0 flex-1 truncate">«{a.phrase}» → {a.name}{a.notName ? ` (${T.aliasesNot} ${a.notName})` : ''}</span>
              {a.stale && <Badge variant="secondary" title={T.aliasesStaleTitle}>{T.aliasesStale}</Badge>}
              <Button variant="destructive" size="xs" disabled={busy === a.phrase} onClick={() => { void remove(a.phrase); }}
                aria-label={`${T.aliasesDelete}: ${a.phrase}`}>{T.aliasesDelete}</Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
