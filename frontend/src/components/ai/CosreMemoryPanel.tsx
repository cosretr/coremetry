// CosreMemoryPanel — CoSRE Faz E (ekip notları / hafıza). CoSRE'nin açık
// düzeltmelerden ve Problem kök neden etiketlerinden öğrendiği notların
// kayıt listesi (coremetry DataTable: Kapsam · Not · Kaynak · Yazan · Tarih ·
// Durum + ⋯ Düzenle / Emekliye ayır / Sil). İki ev sahibi, ikisi de TEMBEL:
// Servis › Details › "CoSRE notları" bölümü (service verilir; yalnız o
// servisin notları, yeni not o servise) ve Ayarlar › CoSRE › Notlar (hepsi).
// Viewer listeyi GÖRÜR, yazamaz (form + eylem menüsü yok). ≤500 not (sunucu
// tavanı) → istemci sıralaması meşru; contentVisibility uzun listede.
// Yeni CSS yok: form/çubuk CosreRulesPanel'in cosre-rules__* sınıflarını
// paylaşır.
import { useMemo, useState, type FormEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, TextareaField } from '@/components/coremetry/Field';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { useConfirm } from '@/components/coremetry/Confirm';
import { ServicePicker } from '@/components/ServicePicker';
import { api } from '@/lib/api';
import { t, tf, useLang, type Lang } from '@/lib/i18n';
import { toast } from '@/lib/toast';
import { tsCompact, tsLong } from '@/lib/utils';
import type { CosreMemoryItem, CosreRuleScope, CosreRuleScopeKind } from '@/lib/types';
import {
  COSRE_MEMORY_KEY, MEM_MAX, dateInputToNs, memScopeLabel, memSingleLine, memSourceLabel, memState,
  memStatementError, memTs, nsToDateInput,
} from './cosreMemory';
import '@/styles/cosreChat.css';

const cols = (lang: Lang, withScope: boolean): ColumnDef<CosreMemoryItem>[] => [
  ...(withScope ? [{ id: 'scope', label: t('cmem.col.scope', lang), sortValue: (r: CosreMemoryItem) => memScopeLabel(r.scope, lang), naturalDir: 'asc' as const, width: 160, minWidth: 100 }] : []),
  { id: 'statement', label: t('cmem.col.statement', lang), sortValue: r => r.statement, naturalDir: 'asc', flex: true, minWidth: 220 },
  { id: 'source', label: t('cmem.col.source', lang), sortValue: r => memSourceLabel(r, lang), naturalDir: 'asc', width: 150, minWidth: 100 },
  { id: 'by', label: t('cmem.col.by', lang), sortValue: r => r.source.by || r.source.byEmail || '', naturalDir: 'asc', width: 140, minWidth: 90 },
  { id: 'at', label: t('cmem.col.at', lang), sortValue: r => memTs(r), numeric: true, naturalDir: 'desc', width: 120, minWidth: 90 },
  { id: 'status', label: t('cmem.col.status', lang), sortValue: r => memState(r), naturalDir: 'asc', width: 100, minWidth: 80,
    tone: r => (memState(r) === 'active' ? undefined : 'warn') },
];

interface Draft { id: string | null; statement: string; kind: CosreRuleScopeKind; value: string; expires: string; status: 'active' | 'retired' }

export function CosreMemoryPanel({ service, canEdit, storageKey }: { service?: string; canEdit: boolean; storageKey: string }) {
  const lang = useLang();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const fresh = (): Draft => ({ id: null, statement: '', kind: service ? 'service' : 'global', value: service ?? '', expires: '', status: 'active' });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [busy, setBusy] = useState(false);
  const q = useQuery({
    queryKey: [...COSRE_MEMORY_KEY, service ?? ''],
    queryFn: ({ signal }) => api.cosreMemory(service, signal),
    staleTime: 30_000,
  });
  const items = q.data?.items ?? [];
  const columns = useMemo(() => cols(lang, !service), [lang, service]);
  const refresh = () => qc.invalidateQueries({ queryKey: COSRE_MEMORY_KEY });
  const fail = (e: unknown) => toast.error(tf('cmem.toast.err', lang, { e: e instanceof Error ? e.message : String(e) }));

  const scopeOf = (d: Draft): CosreRuleScope => (d.kind === 'global' ? { kind: 'global' } : { kind: d.kind, value: d.value.trim() });
  const write = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true);
    try {
      await fn();
      toast.success(ok);
      await refresh();
      return true;
    } catch (e) {
      fail(e);
      return false;
    } finally {
      setBusy(false);
    }
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!draft || memStatementError(draft.statement, lang) || (draft.kind !== 'global' && !draft.value.trim())) return;
    const statement = draft.statement.trim();
    const expiresAt = dateInputToNs(draft.expires);
    const done = await write(() => (draft.id
      ? api.updateCosreMemory(draft.id, { statement, scope: scopeOf(draft), status: draft.status, expiresAt })
      : api.createCosreMemory({ statement, scope: scopeOf(draft), expiresAt, source: { kind: 'manual' } })), t('cmem.toast.saved', lang));
    if (done) setDraft(null);
  };
  const setStatus = (r: CosreMemoryItem, status: 'active' | 'retired') =>
    void write(() => api.updateCosreMemory(r.id, { statement: r.statement, scope: r.scope, status, expiresAt: r.expiresAt ?? 0 }), t('cmem.toast.saved', lang));
  const remove = async (r: CosreMemoryItem) => {
    if (!await confirm({ title: t('cmem.del.title', lang), body: tf('cmem.del.body', lang, { s: r.statement }), confirmLabel: t('cmem.del.ok', lang), danger: true })) return;
    if (await write(() => api.deleteCosreMemory(r.id), t('cmem.toast.deleted', lang)) && draft?.id === r.id) setDraft(null);
  };

  const err = draft?.statement ? memStatementError(draft.statement, lang) : undefined;
  return (
    <div className="cosre-rules" data-testid="cosre-memory-panel">
      <p className="cosre-rules__intro">
        {tf('cmem.intro', lang, { n: q.data?.limits.promptItems ?? 5 })}
        {!canEdit && <> {t('cmem.readonly', lang)}</>}
      </p>
      {canEdit && !draft && (
        <div className="cosre-rules__formactions">
          <Button variant="secondary" size="sm" onClick={() => setDraft(fresh())}>{t('cmem.add', lang)}</Button>
        </div>
      )}
      {canEdit && draft && (
        <form className="cosre-rules__form" onSubmit={e => { void submit(e); }} aria-label={t(draft.id ? 'cmem.form.edit' : 'cmem.form.new', lang)}>
          <TextareaField label={t(draft.id ? 'cmem.form.edit' : 'cmem.form.new', lang)} rows={2} value={draft.statement} maxLength={MEM_MAX + 20}
            error={err} hint={tf('cmem.form.hint', lang, { n: Array.from(draft.statement.trim()).length, max: MEM_MAX })}
            onChange={e => setDraft(d => d && ({ ...d, statement: memSingleLine(e.target.value) }))} />
          <div className="cosre-rules__scope">
            {!service && (
              <SelectField label={t('cmem.form.scope', lang)} value={draft.kind}
                onChange={e => setDraft(d => d && ({ ...d, kind: e.target.value as CosreRuleScopeKind, value: '' }))}>
                <option value="global">{t('cmem.scope.global', lang)}</option>
                <option value="service">{tf('cmem.scope.service', lang, { v: '' }).trim()}</option>
                <option value="team">{tf('cmem.scope.team', lang, { v: '' }).trim()}</option>
              </SelectField>
            )}
            {!service && draft.kind === 'service' && (
              <div className="field">
                <span className="field-label">{t('cmem.form.service', lang)}</span>
                <ServicePicker value={draft.value} onChange={v => setDraft(d => d && ({ ...d, value: v }))} />
              </div>
            )}
            {!service && draft.kind === 'team' && (
              <Field label={t('cmem.form.team', lang)} value={draft.value} onChange={e => setDraft(d => d && ({ ...d, value: e.target.value }))} />
            )}
            <Field label={t('cmem.form.expires', lang)} type="date" value={draft.expires}
              onChange={e => setDraft(d => d && ({ ...d, expires: e.target.value }))} />
          </div>
          <div className="cosre-rules__formactions">
            <Button type="submit" size="sm" loading={busy}
              disabled={busy || !!memStatementError(draft.statement, lang) || (draft.kind !== 'global' && !draft.value.trim())}>
              {t('cmem.form.save', lang)}
            </Button>
            <Button variant="secondary" size="sm" onClick={() => setDraft(null)}>{t('cmem.form.cancel', lang)}</Button>
          </div>
        </form>
      )}
      <DataTable<CosreMemoryItem> storageKey={storageKey} columns={columns} rows={items} ariaLabel={t('cmem.title', lang)}
        getRowKey={r => r.id} sort={{ initial: { id: 'at', dir: 'desc' } }} contentVisibility={items.length > 100}
        state={q.isLoading ? { kind: 'loading' } : q.isError ? { kind: 'error', message: t('cmem.err', lang), onRetry: () => { void q.refetch(); } }
          : { kind: 'empty', message: t('cmem.empty', lang) }}
        rowActions={canEdit ? {
          label: t('cmem.act.menu', lang),
          items: r => [
            { id: 'edit', label: t('cmem.act.edit', lang), disabled: busy,
              onSelect: () => setDraft({ id: r.id, statement: r.statement, kind: r.scope.kind, value: r.scope.value ?? '', expires: nsToDateInput(r.expiresAt), status: r.status }) },
            r.status === 'retired'
              ? { id: 'activate', label: t('cmem.act.activate', lang), disabled: busy, onSelect: () => setStatus(r, 'active') }
              : { id: 'retire', label: t('cmem.act.retire', lang), disabled: busy, onSelect: () => setStatus(r, 'retired') },
            { id: 'delete', label: t('cmem.act.delete', lang), danger: true, disabled: busy, onSelect: () => { void remove(r); } },
          ],
        } : undefined}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'scope': return memScopeLabel(r.scope, lang);
            case 'statement': return <span title={r.statement}>{r.statement}</span>;
            case 'source': return memSourceLabel(r, lang);
            case 'by': return <span title={r.source.byEmail}>{r.source.by || r.source.byEmail || '—'}</span>;
            case 'at': return <span title={tsLong(memTs(r)) + (r.updatedBy ? ` · ${r.updatedBy}` : '')}>{tsCompact(memTs(r))}</span>;
            case 'status': return t(`cmem.status.${memState(r)}`, lang);
          }
          return null;
        }} />
    </div>
  );
}

export default CosreMemoryPanel;
