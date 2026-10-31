// OnlineUsersSheet — v0.10.1736 "Kim çevrimiçi" (her rol). Kenar çubuğu
// kullanıcı menüsündeki "Çevrimiçi · N" satırı bunu TEMBEL açar (kabuk
// coremetry/overlay'i statik import edemez — eslint kabuk bloğu, plan §1.2).
// Liste GET /api/presence/online: ad · takım · son görülme. Admin'de ad
// /users/:id'ye bağlanır, diğer rollerde düz metin. ≤200 satır: her satır
// content-visibility:auto (Table > 100 kuralı).
import { Link } from 'react-router-dom';
import { Users } from 'lucide-react';
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from './overlay';
import { Spinner, Empty } from '@/components/Spinner';
import { usePresenceOnline } from '@/lib/queries';
import { agoTR } from '@/lib/utils';
import type { OnlineUser } from '@/lib/types';

export const ONLINE_STRINGS = {
  title: 'Çevrimiçi',
  description: 'Son 5 dakikada etkin olan kullanıcılar',
  empty: 'Şu an kimse çevrimiçi değil',
  error: 'Çevrimiçi listesi yüklenemedi',
  loading: 'Yükleniyor…',
} as const;

export function OnlineUsersSheet({ open, onOpenChange, isAdmin }: {
  open: boolean;
  onOpenChange(open: boolean): void;
  isAdmin: boolean;
}) {
  const q = usePresenceOnline(open);
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full gap-0 border-border bg-card p-0 sm:max-w-sm">
        <SheetHeader className="border-b border-border px-4 py-3 pr-12">
          <SheetTitle>{ONLINE_STRINGS.title}{q.data ? ` · ${q.data.length}` : ''}</SheetTitle>
          <SheetDescription>{ONLINE_STRINGS.description}</SheetDescription>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
          <OnlineUsersBody isLoading={q.isLoading} isError={q.isError} rows={q.data} isAdmin={isAdmin}
            onNavigate={() => onOpenChange(false)} />
        </div>
      </SheetContent>
    </Sheet>
  );
}

// Gövde ayrı: yükleniyor / hata / boş / liste durumları test edilebilir.
export function OnlineUsersBody({ isLoading, isError, rows, isAdmin, onNavigate, nowMs = Date.now() }: {
  isLoading: boolean;
  isError: boolean;
  rows: OnlineUser[] | undefined;
  isAdmin: boolean;
  onNavigate?: () => void;
  nowMs?: number;
}) {
  if (isLoading) return <div className="px-2 py-4"><Spinner label={ONLINE_STRINGS.loading} /></div>;
  if (isError) return <Empty compact icon={<Users className="size-5" />} title={ONLINE_STRINGS.error} />;
  if (!rows || rows.length === 0) return <Empty compact icon={<Users className="size-5" />} title={ONLINE_STRINGS.empty} />;
  return (
    <ul className="flex flex-col" aria-label={ONLINE_STRINGS.title}>
      {rows.map(u => (
        <li key={u.id}
          className="flex items-center gap-2 rounded-sm px-2 py-1.5 text-sm [contain-intrinsic-size:auto_32px] [content-visibility:auto]">
          <span aria-hidden className="size-2 shrink-0 rounded-full bg-[var(--ok)]" />
          <span className="min-w-0 flex-1 truncate" title={u.team ? `${u.name} · ${u.team}` : u.name}>
            {isAdmin
              ? <Link to={`/users/${encodeURIComponent(u.id)}`} onClick={onNavigate} className="text-foreground hover:underline">{u.name}</Link>
              : <span className="text-foreground">{u.name}</span>}
            {u.team && <span className="text-muted-foreground"> · {u.team}</span>}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{agoTR(u.lastSeenAt, nowMs)}</span>
        </li>
      ))}
    </ul>
  );
}

export default OnlineUsersSheet;
