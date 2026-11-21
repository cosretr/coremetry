// CosreRulesPanel — v0.10.1289 (CoSRE kurallar / hafıza: "bunu hatırla").
// Operatör kurallarının kayıt listesi (useDataTable: Kapsam · Kural · Yazan ·
// Tarih · eylemler). İki ev sahibi: Ayarlar › CoSRE › Kurallar (?tab=rules)
// ve /cosre kenar çubuğundaki "Kurallar" modalı (her rol). Viewer listeyi
// GÖRÜR, yazamaz (form + eylemler yok). Yazım GET'in rev'iyle If-Match taşır:
// başka bir editör arada yazdıysa sunucu 409 döner → liste yenilenir, uyarı.
// ≤200 kural (sunucu tavanı) → istemci sıralaması meşru, sanallaştırma yok.
import { useMemo, useState, type FormEvent } from 'react';
import '@/styles/cosreChat.css';
import { useQueryClient } from '@tanstack/react-query';
import { Button, ButtonGroup, Field, SearchField, SelectField, TextareaField, useConfirm } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { ServicePicker } from '@/components/ServicePicker';
import { api } from '@/lib/api';
import { COSRE_RULES_KEY, useCosreRules } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { tsCompact, tsLong } from '@/lib/utils';
import type { CosreRule, CosreRuleScope, CosreRuleScopeKind } from '@/lib/types';
import { RULE_MAX, filterRules, ruleErrorText, ruleScopeLabel, ruleTextError, ruleTs, singleLine } from './cosreRules';

const COLS: ColumnDef<CosreRule>[] = [
  { id: 'scope', label: 'Kapsam', sortValue: r => ruleScopeLabel(r.scope), naturalDir: 'asc', width: 170, minWidth: 110, priority: 1 },
  { id: 'text', label: 'Kural', sortValue: r => r.text, naturalDir: 'asc', flex: true, minWidth: 200, truncate: 'wrap', priority: 1 },
  { id: 'author', label: 'Yazan', sortValue: r => r.author, naturalDir: 'asc', width: 150, minWidth: 100, priority: 3 },
  { id: 'at', label: 'Tarih', sortValue: r => ruleTs(r), width: 120, minWidth: 90, priority: 3 },
];
const ACTION_COL: ColumnDef<CosreRule> = { id: 'actions', label: 'Eylemler', kind: 'actions', width: 150, minWidth: 150 };

interface Draft { id: string | null; text: string; kind: CosreRuleScopeKind; value: string }
const EMPTY: Draft = { id: null, text: '', kind: 'global', value: '' };

export function CosreRulesPanel({ canEdit, storageKey = 'cosre-rules' }: { canEdit: boolean; storageKey?: string }) {
  const q = useCosreRules(true);
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [filter, setFilter] = useState('');
  const [draft, setDraft] = useState<Draft>(EMPTY);
  const [busy, setBusy] = useState(false);
  const rules = useMemo(() => filterRules(q.data?.rules ?? [], filter), [q.data, filter]);
  const columns = useMemo(() => (canEdit ? [...COLS, ACTION_COL] : COLS), [canEdit]);
  const dt = useDataTable<CosreRule>({ storageKey, columns, rows: rules, initialSort: { id: 'at', dir: 'desc' } });
  const limits = q.data?.limits;
  const rev = q.data?.rev;

  const refresh = () => qc.invalidateQueries({ queryKey: COSRE_RULES_KEY });
  const fail = async (e: unknown, what: string) => {
    const { conflict, text } = ruleErrorText(e);
    toast.error(conflict ? `Kurallar sen düzenlerken değişti — liste yenilendi. ${what} yeniden dene.` : `${what} başarısız: ${text}`);
    if (conflict) await refresh();
  };
  const textErr = draft.text ? ruleTextError(draft.text) : undefined;
  const valueMissing = draft.kind !== 'global' && !draft.value.trim();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (ruleTextError(draft.text) || valueMissing) return;
    const scope: CosreRuleScope = draft.kind === 'global' ? { kind: 'global' } : { kind: draft.kind, value: draft.value.trim() };
    setBusy(true);
    try {
      if (draft.id) await api.updateCosreRule(draft.id, { text: draft.text.trim(), scope }, rev);
      else await api.createCosreRule({ text: draft.text.trim(), scope }, rev);
      toast.success(draft.id ? 'Kural güncellendi' : 'Kural eklendi');
      setDraft(EMPTY);
      await refresh();
    } catch (err) {
      await fail(err, draft.id ? 'Güncelleme' : 'Ekleme');
    } finally {
      setBusy(false);
    }
  };
  const remove = async (r: CosreRule) => {
    if (!await confirm({
      title: 'Kural silinsin mi?',
      body: <>“{r.text}” ({ruleScopeLabel(r.scope)}) CoSRE cevaplarına artık eklenmeyecek.</>,
      confirmLabel: 'Kuralı sil', danger: true,
    })) return;
    setBusy(true);
    try {
      await api.deleteCosreRule(r.id, rev);
      toast.success('Kural silindi');
      if (draft.id === r.id) setDraft(EMPTY);
      await refresh();
    } catch (err) {
      await fail(err, 'Silme');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="cosre-rules" data-testid="cosre-rules-panel">
      <p className="cosre-rules__intro">
        Operatörlerin CoSRE'ye bıraktığı kısa notlar ("bunu hatırla"). Bağlama uyan en çok 5 kural
        (servis &gt; takım &gt; genel, en yeni önce) cevaba <b>veri</b> olarak eklenir; araç seçimini,
        hipotez skorunu ya da güvenlik kurallarını değiştirmez.
        {limits && <> Tavan: {limits.maxRules} kural, kapsam başına {limits.maxPerScope}.</>}
      </p>
      {canEdit && (
        <form className="cosre-rules__form" onSubmit={e => { void submit(e); }} aria-label={draft.id ? 'Kuralı düzenle' : 'Kural ekle'}>
          <TextareaField label={draft.id ? 'Kuralı düzenle' : 'Yeni kural'} rows={2} value={draft.text} maxLength={RULE_MAX + 20}
            placeholder="ör. checkout'ta 02:00–02:20 arası batch işi çalışır, 5xx beklenir"
            error={textErr} hint={`${Array.from(draft.text.trim()).length}/${RULE_MAX} · tek cümle`}
            onChange={e => setDraft(d => ({ ...d, text: singleLine(e.target.value) }))} />
          <div className="cosre-rules__scope">
            <SelectField label="Kapsam" value={draft.kind}
              onChange={e => setDraft(d => ({ ...d, kind: e.target.value as CosreRuleScopeKind, value: '' }))}>
              <option value="global">genel</option>
              <option value="service">servis</option>
              <option value="team">takım</option>
            </SelectField>
            {draft.kind === 'service' && (
              <div className="field">
                <span className="field-label">Servis</span>
                <ServicePicker value={draft.value} onChange={v => setDraft(d => ({ ...d, value: v }))} placeholder="servis ara…" />
              </div>
            )}
            {draft.kind === 'team' && (
              <Field label="Takım" value={draft.value} placeholder="takım adı (katalog ownerTeam)"
                onChange={e => setDraft(d => ({ ...d, value: e.target.value }))} />
            )}
          </div>
          <div className="cosre-rules__formactions">
            <Button type="submit" variant="primary" size="sm" loading={busy}
              disabled={busy || !draft.text.trim() || !!ruleTextError(draft.text) || valueMissing}>
              {draft.id ? 'Güncelle' : 'Ekle'}
            </Button>
            {draft.id && <Button variant="secondary" size="sm" onClick={() => setDraft(EMPTY)}>Vazgeç</Button>}
          </div>
        </form>
      )}
      <div className="cosre-rules__bar">
        <SearchField value={filter} onChange={setFilter} placeholder="kural, kapsam ya da yazan ara…" aria-label="Kurallarda ara" width={260} />
        <span className="cosre-rules__count">{q.data ? `${q.data.rules.length} kural` : ''}</span>
      </div>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {rules.length === 0 ? (
              <DataTableState dt={dt} {...(q.isLoading ? { kind: 'loading' as const }
                : q.isError ? { kind: 'error' as const, message: 'Kurallar okunamadı.', onRetry: () => { void q.refetch(); } }
                : filter ? { kind: 'no-match' as const, onClearFilters: () => setFilter('') }
                : { kind: 'empty' as const, message: 'Henüz kural yok — sohbette "Hatırla: …" yaz ya da yukarıdan ekle.' })} />
            ) : dt.sortedRows.map(r => (
              <tr key={r.id}>
                <DataTableCell dt={dt} col="scope" row={r} value={ruleScopeLabel(r.scope)} />
                <DataTableCell dt={dt} col="text" row={r} value={r.text} />
                <DataTableCell dt={dt} col="author" row={r} value={r.author || r.authorEmail || '—'} title={r.authorEmail} />
                <DataTableCell dt={dt} col="at" row={r} value={tsCompact(ruleTs(r))}
                  title={tsLong(ruleTs(r)) + (r.updatedBy ? ` · düzenleyen ${r.updatedBy}` : '')} />
                {canEdit && (
                  <DataTableCell dt={dt} col="actions" row={r}>
                    <ButtonGroup aria-label={`${r.text} eylemleri`} size="sm">
                      <Button variant="secondary" disabled={busy}
                        onClick={() => setDraft({ id: r.id, text: r.text, kind: r.scope.kind, value: r.scope.value ?? '' })}>Düzenle</Button>
                      <Button variant="ghost-danger" disabled={busy} onClick={() => { void remove(r); }}>Sil</Button>
                    </ButtonGroup>
                  </DataTableCell>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
