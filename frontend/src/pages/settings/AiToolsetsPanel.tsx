// AiToolsetsPanel — v0.10.1434 (CoSRE araştırma programı PR 1;
// docs/audits/cosre-investigation-model-audit.md §6.1). Ayarlar › CoSRE ›
// "Araç setleri" (?tab=toolsets): 74 aracın kaynak bazlı 12 toolset'i, her
// birinin türetilmiş sağlığı ve aç/kapa durumu. GET /api/ai/toolsets her rol
// (viewer salt-okunur GÖRÜR); PUT admin + audit (sunucu kapısı esas —
// buradaki canEdit yalnız düğmeleri gizler).
//
// Tablo türü: statik <table> (T1 gerekçesi — düzenlenebilir sabit katalog:
// 12 satır, sunucu sırası = toolset kataloğunun sırası, sıralanmaz; satır
// başına aç/kapa düğmesi). useDataTable değil: sıralama/yeniden boyutlama
// sabit bir katalogda anlam taşımaz. Polling yok (tek okuma; yazım cevabı
// yeni durumu taşır) — pollingBudget.pin'e girmez.
//
// Renk yalnız sapan değerde (CLAUDE.md tablo standardı): sağlıklı = nötr
// rozet; kısmi = uyarı; yapılandırılmamış = bilgi; erişilemiyor / yetki /
// zaman aşımı / hata = tehlike.
import { useEffect, useState, type ReactNode } from 'react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTableStateRow } from '@/components/coremetry/DataTable';
import { useConfirm } from '@/components/coremetry/Confirm';
import type { Status } from '@/lib/statusTone';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { toast } from '@/lib/toast';
import { fmtDateTime } from '@/lib/utils';
import type { AISourceToolset, AISourceToolsetHealthState, AISourceToolsetsPayload } from '@/lib/types';
import { SectionTitle, humanize } from './shared';

// Faz 2 dilim 1 R11 (D7): sapma StatusBadge (warn / info / critical); sağlıklı
// (null) = nötr shadcn Badge — renk yalnız sapan değerde, eşleme aynı.
const HEALTH_STATUS: Record<AISourceToolsetHealthState, Status | null> = {
  ok: null, empty: null, delayed: 'warn', truncated: 'warn', partial: 'warn',
  not_configured: 'info', unreachable: 'critical', unauthorized: 'critical', timeout: 'critical', error: 'critical',
};

/** Durum rozeti: sapma StatusBadge, nötr shadcn Badge (secondary). */
function ToneBadge({ status, children }: { status: Status | null; children: ReactNode }) {
  return status ? <StatusBadge status={status}>{children}</StatusBadge> : <Badge variant="secondary">{children}</Badge>;
}

/** SAF: bir toolset kapatılınca/açılınca gönderilecek kapalı ad listesi (sıralı, tekrarsız). */
export function nextDisabled(current: string[], name: string, disable: boolean): string[] {
  const set = new Set(current);
  if (disable) set.add(name); else set.delete(name);
  return [...set].sort();
}

/** SAF: bilinmeyen anahtar için sunucu metnine düşen çeviri. */
function tOr(t: (k: string) => string, key: string, fallback: string): string {
  const v = t(key);
  return v === key ? fallback : v;
}

export function AiToolsetsPanel({ canEdit: canEditProp }: { canEdit?: boolean }) {
  const t = useT();
  const confirm = useConfirm();
  const role = useAuthRole();
  const canEdit = canEditProp ?? role === 'admin';
  const [data, setData] = useState<AISourceToolsetsPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    setErr(null);
    api.aiToolsets(ctl.signal)
      .then(d => setData(d))
      .catch(e => { if (!ctl.signal.aborted) setErr(humanize(e)); });
    return () => ctl.abort();
  }, [reload]);

  const toggle = async (ts: AISourceToolset) => {
    if (!data) return;
    const disable = ts.enabled;
    if (disable && !await confirm({
      title: t('aiToolsets.confirmTitle').replace('{name}', ts.name),
      body: t('aiToolsets.confirmBody').replace('{n}', String(ts.tools.length)),
      confirmLabel: t('aiToolsets.disable'),
      danger: true,
    })) return;
    setBusy(ts.name);
    try {
      const next = await api.putAIToolsets(nextDisabled(data.disabled, ts.name, disable));
      setData(next);
      toast.success(t('aiToolsets.saved').replace('{name}', ts.name).replace('{state}', t(disable ? 'aiToolsets.off' : 'aiToolsets.on')));
    } catch (e) {
      toast.error(t('aiToolsets.saveFailed').replace('{err}', humanize(e)));
    } finally {
      setBusy(null);
    }
  };

  const toolCount = data?.toolsets.reduce((n, ts) => n + ts.tools.length, 0) ?? 74;
  const rows = data?.toolsets ?? [];
  return (
    <div data-testid="ai-toolsets-panel" className="mt-3">
      <SectionTitle>{t('aiToolsets.title')}</SectionTitle>
      <p className="mt-0 max-w-[820px] text-[length:var(--fs-sm)] text-(--text2)">
        {t('aiToolsets.intro').replace('{n}', String(toolCount))}
      </p>
      {!canEdit && <p className="field-hint">{t('aiToolsets.readOnly')}</p>}
      {data?.updatedBy && data.updatedAt && (
        <p className="field-hint">
          {t('aiToolsets.lastChange').replace('{by}', data.updatedBy).replace('{at}', fmtDateTime(new Date(data.updatedAt)))}
        </p>
      )}
      {/* v0.10.1434 — statik tablo (T1): düzenlenebilir sabit katalog, 12 satır, sunucu sırası, sıralanmaz.
          Faz 2 dilim 1 R11: shadcn Table (T1 gerekçesi aynı), durum satırı coremetry DataTableStateRow. */}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('aiToolsets.col.toolset')}</TableHead>
            <TableHead>{t('aiToolsets.col.source')}</TableHead>
            <TableHead>{t('aiToolsets.col.tools')}</TableHead>
            <TableHead>{t('aiToolsets.col.health')}</TableHead>
            <TableHead>{t('aiToolsets.col.status')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {err ? (
            <DataTableStateRow colSpan={5} kind="error" message={t('aiToolsets.loadFailed')} detail={err} onRetry={() => setReload(n => n + 1)} />
          ) : !data ? (
            <DataTableStateRow colSpan={5} kind="loading" skeletonRows={6} />
          ) : rows.length === 0 ? (
            <DataTableStateRow colSpan={5} kind="empty" message={t('aiToolsets.empty')} />
          ) : rows.map(ts => {
            const h = ts.health;
            const healthNote = [h.detail, ...(h.notes ?? [])].filter(Boolean).join(' · ');
            return (
              <TableRow key={ts.name} data-toolset={ts.name}>
                <TableCell className="font-mono">
                  {ts.name}
                  {ts.required && <> <Badge variant="secondary">{t('aiToolsets.required')}</Badge></>}
                </TableCell>
                <TableCell>{tOr(t, `aiToolsets.src.${ts.name}`, ts.source)}</TableCell>
                <TableCell className="whitespace-normal" title={ts.tools.join(', ')}>
                  <span className="cell-strong tabular-nums">{ts.tools.length}</span>
                  <div className="field-hint font-mono">{ts.tools.join(', ')}</div>
                </TableCell>
                <TableCell className="whitespace-normal">
                  <span data-health={h.state}>
                    <ToneBadge status={HEALTH_STATUS[h.state] ?? null}>{tOr(t, `aiToolsets.health.${h.state}`, h.state)}</ToneBadge>
                  </span>
                  {h.backend && <span className="cell-hint mono">{h.backend}</span>}
                  {healthNote && <div className="field-hint">{healthNote}</div>}
                </TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5">
                    <ToneBadge status={ts.enabled ? null : 'warn'}>{t(ts.enabled ? 'aiToolsets.on' : 'aiToolsets.off')}</ToneBadge>
                    {canEdit && !ts.required && (
                      <Button variant="ghost" size="xs" loading={busy === ts.name} disabled={busy !== null}
                        onClick={() => { void toggle(ts); }}>
                        {t(ts.enabled ? 'aiToolsets.disable' : 'aiToolsets.enable')}
                      </Button>
                    )}
                  </span>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
