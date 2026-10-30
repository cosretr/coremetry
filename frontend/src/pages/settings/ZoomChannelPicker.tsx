import { useRef, useState } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3)
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/coremetry/overlay';
import { DataTableStateRow, type DataTableStateStaticProps } from '@/components/coremetry/DataTable';

// ZoomChannel mirrors the backend ZoomChannel struct.
interface ZoomChannelRow {
  id: string;
  jid: string;
  name: string;
  type?: number;
}

// ZoomChannelPicker — small button next to the Channel ID input
// that fetches every channel the configured S2S OAuth app can
// see and opens a searchable picker. Removes the
// memorise-the-JID requirement: a Zoom workspace can have
// hundreds of channels, so the modal includes an inline search
// box that filters by name / id / JID as the operator types.
// Click a row to inject the JID into the form.
//
// Faz 2 dilim 1 R9 (D8): elle yazılmış sabit perdeli kutu → coremetry/
// overlay Dialog (role=dialog + başlık, odak tuzağı, Esc lib/escLayer
// yığınından). ChannelModal'ın İÇİNDE açılır; iki diyalog da body portalı,
// üst üste binme PORTAL SIRASIYLA (sonra açılan üstte) ve Esc LIFO —
// içteki kapanır, dıştaki açık kalır (ChannelModal.nested.test.tsx).
// Satır içi stil yok; mevcut "Close" düğmesi başlıkta kaldığı için
// üretilen × basılmaz (iki kapat düğmesi olmasın).
const BARE_INPUT = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-md)] md:text-[length:var(--fs-md)] dark:bg-field';

export function ZoomChannelPicker({
  existingChannelId,
  accountId, clientId, clientSecret,
  oauthBaseUrl, apiBaseUrl, insecureSkipVerify, onPick,
}: {
  existingChannelId?: string;
  accountId: string;
  clientId: string;
  clientSecret: string;
  oauthBaseUrl: string;
  apiBaseUrl: string;
  insecureSkipVerify?: boolean;
  onPick: (jid: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [rows, setRows] = useState<ZoomChannelRow[] | null>(null);
  const [search, setSearch] = useState('');
  // v0.10.967 — "Filtreleri temizle" kendini kaldırınca odak arama kutusuna döner.
  const searchRef = useRef<HTMLInputElement>(null);

  const canFetch = (
    // For an unsaved channel we need all three credential fields
    // inline; for an existing channel the saved (redacted)
    // secret can be reused server-side via existingChannelId.
    (accountId.trim() && clientId.trim() && clientSecret.trim()) ||
    !!existingChannelId
  );

  const load = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await fetch('/api/channels/zoom/list-channels', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          existingChannelId: existingChannelId,
          accountId: accountId.trim(),
          clientId: clientId.trim(),
          clientSecret: clientSecret.trim(),
          oauthBaseUrl: oauthBaseUrl.trim(),
          apiBaseUrl: apiBaseUrl.trim(),
          insecureSkipVerify: !!insecureSkipVerify,
        }),
      });
      if (!r.ok) {
        const body = await r.text();
        // Backend returns partial channels on truncation. Try to
        // surface those alongside the warning so the operator
        // can still pick from what we got.
        try {
          const parsed = JSON.parse(body);
          if (Array.isArray(parsed.channels) && parsed.channels.length > 0) {
            setRows(parsed.channels);
          }
          setErr(parsed.error ?? `HTTP ${r.status}`);
        } catch {
          setErr(`HTTP ${r.status}: ${body.slice(0, 200)}`);
        }
        return;
      }
      const j = await r.json();
      setRows(j.channels ?? []);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const onOpen = () => {
    setOpen(true);
    if (!rows && !busy) load();
  };

  const filtered = (rows ?? []).filter(r => {
    if (!search.trim()) return true;
    const t = search.toLowerCase();
    return (
      r.name.toLowerCase().includes(t) ||
      r.jid.toLowerCase().includes(t) ||
      r.id.toLowerCase().includes(t)
    );
  });

  // v0.10.967 — tablo standardı dilim 5 (P-2, recipe P7): tablonun ÜSTÜNDEKİ
  // "Loading channels…" satırı, kırmızı hata kutusu ve boş cümle tablonun
  // İÇİNE indi; arama kutusu yerinde. Satırlar kazanır: Refresh sürerken
  // eldeki satırlar kalır (üstteki "Loading channels…" satırı yalnız o
  // zaman), satır varken gelen hata ÜSTTE şerit olarak kalır — sunucu
  // kesilmiş listede kısmi kanalları hatayla BİRLİKTE döner, ya da düşen
  // Refresh eski listeyi bırakır; iki hâlde de hata görünür, sessiz bayat
  // satır yok. Eskiden aramanın hepsini elediği liste boş bir gövdeydi:
  // artık "Eşleşme yok" + "Filtreleri temizle" (arama kutusunu boşaltır).
  const hasRows = (rows?.length ?? 0) > 0;
  const showRows = filtered.length > 0;
  const listState: Omit<DataTableStateStaticProps, 'colSpan'> =
    busy ? { kind: 'loading', message: 'Kanallar yükleniyor' }
    : err && !hasRows ? { kind: 'error', message: `Zoom kanalları okunamadı: ${err}` }
    : rows === null ? { kind: 'loading', message: 'Kanallar yükleniyor' }
    : rows.length === 0 ? { kind: 'empty', message: 'Bu S2S uygulamasına görünen kanal yok — kanalın burada görünmesi için bot kullanıcısının o kanala üye olması gerekir.' }
    : { kind: 'no-match', onClearFilters: () => setSearch(''), returnFocusRef: searchRef };

  const channelType = (t?: number) =>
    t === 1 ? 'DM'
    : t === 2 ? 'Group'
    : t === 3 ? 'Public'
    : t === 4 ? 'Private'
    : '—';

  return (
    <>
      <Button variant="outline"
        disabled={!canFetch}
        title={canFetch
          ? 'List channels via the configured S2S OAuth app'
          : 'Enter Account ID / Client ID / Client Secret (or save first), then try again'}
        onClick={onOpen}
        className="whitespace-nowrap">
        List my channels…
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        {/* v0.10.933 (tablo standardı T2) — diyalog yüzeyi bg2 değil bg1
            (.modal-dialog ile aynı modal yüzeyi): paylaşılan satır hover'ı
            bg2, bg2 zeminde görünmüyordu. */}
        <DialogContent showCloseButton={false} aria-describedby={undefined}
          className="flex max-h-[82vh] w-[720px] max-w-[94vw] flex-col gap-0 rounded-lg border-(--border) bg-(--bg1) p-[18px] sm:max-w-[94vw]">
          <DialogHeader className="mb-2.5 flex-row items-baseline gap-2">
            <DialogTitle className="text-[length:var(--fs-lg)] font-bold">
              Pick a Zoom channel
            </DialogTitle>
            <span className="text-[length:var(--fs-xs)] text-(--text3)">
              {rows ? `${filtered.length} of ${rows.length}` : ''}
            </span>
            <span className="ml-auto flex gap-2">
              <Button variant="outline" size="sm" disabled={busy} onClick={load}>
                Refresh
              </Button>
              <Button variant="outline" size="sm" onClick={() => setOpen(false)}>Close</Button>
            </span>
          </DialogHeader>

          <Input ref={searchRef} value={search} onChange={e => setSearch(e.target.value)}
            placeholder="Filter by name, ID, or JID…" aria-label="Filter channels"
            autoFocus
            className={`mb-2.5 ${BARE_INPUT}`} />

          {busy && showRows && <div className="text-[length:var(--fs-sm)] text-(--text3)">Loading channels…</div>}
          {err && hasRows && (
            <div className="mb-2 rounded-sm border border-status-critical-border bg-status-critical-bg px-2 py-1.5 text-[length:var(--fs-xs)] text-status-critical">
              {err}
            </div>
          )}

          {/* v0.10.977 — tablo standardı T10: iç kaydırmalı kap paylaşılan
              `table-wrap is-scroll` (tek çerçeve; yapışkan başlık, opak zemini ve
              alt çizgisi `thead th` kuralından). Yükseklik diyaloğun flex
              sütunundan (max-h 82vh): `flex-1`, maxHeight yok. R9: shadcn
              Table'ın kendi kabı kaydırma kabı OLMASIN (overflow-visible) —
              yoksa yapışkan başlık kaymayan iç kaba bağlanırdı. */}
          <div className="table-wrap is-scroll min-h-0 flex-1 [&>div]:overflow-visible">
            {/* v0.10.942 — statik tablo: seçici liste, satır tıkı JID'i forma yazar (T1). */}
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>JID</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {/* v0.10.967 (P-2) — statik tablonun durum satırı; colSpan = thead'deki 3 <th>. */}
                {!showRows ? <DataTableStateRow colSpan={3} {...listState} /> : filtered.map(r => (
                  <TableRow key={r.id || r.jid}
                    {...rowActivation(() => { onPick(r.jid); setOpen(false); })}
                    className={filtered.length > 100 ? 'cv-row' : undefined}>
                    <TableCell className="cell-strong">{r.name || '(unnamed)'}</TableCell>
                    <TableCell className="mono cell-faint">{channelType(r.type)}</TableCell>
                    <TableCell className="mono cell-muted">{r.jid}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className="mt-2 text-[length:var(--fs-xs)] text-(--text3)">
            The bot user behind your S2S OAuth app must be a member of a channel
            for it to appear in this list. If a channel is missing, add the bot
            from the Zoom web UI (channel settings → People → Add) and click
            Refresh.
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
