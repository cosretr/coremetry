// SummaryShell — v0.10.1907. Login sağ sütununun kabuğu + iskeleti. LoginSummary
// (lazy chunk) ile Login.tsx (statik, iskelet için) AYNI kabuğu paylaşır; ayrı
// dosya çünkü iskelet lazy modülün içinde olsaydı chunk yüklenmeden çizilemezdi.
import type { ReactNode } from 'react';
import { Skeleton } from '@/components/shadcn/skeleton';

/** Sağ sütun çerçevesi (iskelet ve içerik aynı kabuğu paylaşır). */
export function SummaryShell({ children }: { children: ReactNode }) {
  return (
    <aside className="flex min-w-0 flex-col gap-3 rounded-r-2xl border-l border-border bg-muted/40 px-8 py-7">
      {children}
    </aside>
  );
}

/** Özet gelene kadar (geniş ekranda) sağ sütunun yer tutucusu. */
export function LoginSummarySkeleton() {
  return (
    <SummaryShell>
      <Skeleton className="h-5 w-40" />
      <Skeleton className="h-3.5 w-64" />
      <div className="grid grid-cols-3 gap-3">
        <Skeleton className="h-[66px]" />
        <Skeleton className="h-[66px]" />
        <Skeleton className="h-[66px]" />
      </div>
      <Skeleton className="h-16" />
    </SummaryShell>
  );
}

