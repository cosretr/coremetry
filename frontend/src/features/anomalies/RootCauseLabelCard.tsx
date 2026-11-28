// RootCauseLabelCard — CoSRE doğruluk ölçümü Faz A, dilim A1:
// Problem detayında ekibin elle girdiği "Kök neden" etiketi — gerçekte ne
// oldu (serbest metin) + kategori + isteğe bağlı suçlu servis/bileşen ve
// bağlantı. CoSRE araştırmasının doğruluğu (replay notlaması) YALNIZ buna
// göre ölçülür; 👍/👎 ve triage düğmeleri etiket değildir.
//
// Depo mevcut onaylı incident_cases vakası (GET/PUT /api/problems/{id}/
// root-cause-label): Problem için önceki "Vaka kaydı" kartının yerini alır
// (aynı vaka, aynı kategori kümesi; taslak varsa form CoSRE önerisiyle dolu,
// "bağımsız" notu aynen). Viewer salt-okunur görür, boş durum ekibi
// çözümden sonra doldurmaya çağırır.
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, TextareaField } from '@/components/coremetry/Field';
import { toast } from '@/lib/toast';
import { fmtDateTime } from '@/lib/utils';
import { SafeHref } from '@/components/ui/SafeHref';
import type { RootCauseLabel, RootCauseLabelInput } from '@/lib/types';
import { Sect } from './detailSections';
import { t, tf, useLang } from '@/lib/i18n';
import { categoryLabel, culpritIsService, independenceNote } from './incidentCaseView';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

export function RootCauseLabelCard({ problemId }: { problemId: string }) {
  const lang = useLang();
  const { user } = useAuth();
  const isEditor = user?.role === 'admin' || user?.role === 'editor';
  const [editing, setEditing] = useState(false);
  const q = useQuery({
    queryKey: ['root-cause-label', problemId],
    queryFn: ({ signal }) => api.rootCauseLabel(problemId, signal),
    staleTime: 60_000,
  });
  const label = q.data?.label ?? null;
  const sub = label ? tf('rcl.by', lang, { who: label.by || '—', at: label.at ? fmtDateTime(label.at) : '—' }) : undefined;
  return (
    <Sect title={t('rcl.title', lang)} accent={!label} sub={sub}>
      <div data-testid="root-cause-label">
        {editing && isEditor && q.data
          ? <LabelForm problemId={problemId} label={label} suggested={q.data.suggested ?? ''}
              categories={q.data.categories} onDone={() => setEditing(false)} />
          : label
            ? <LabelView label={label} />
            : <p style={{ margin: 0, fontSize: 13, color: 'var(--text2)' }}>{t('rcl.empty', lang)}</p>}
        {!editing && (isEditor
          ? (
            <div style={{ marginTop: 8 }}>
              <Button variant="secondary" size="sm" disabled={!q.data} onClick={() => setEditing(true)}>
                {t(label ? 'rcl.btn.edit' : 'rcl.btn.add', lang)}
              </Button>
            </div>
          )
          : !label && <div style={{ marginTop: 6, fontSize: 12, color: 'var(--text3)' }}>{t('rcl.viewer', lang)}</div>)}
      </div>
    </Sect>
  );
}

function LabelView({ label }: { label: RootCauseLabel }) {
  const lang = useLang();
  const culprit = label.culprit ?? '';
  return (
    <div style={{ fontSize: 13, display: 'grid', gap: 4 }}>
      <div><b>{t('case.cause', lang)}</b> {label.cause}</div>
      <div><b>{t('case.category', lang)}</b> {categoryLabel(label.category, lang)}</div>
      {culprit && (
        <div><b>{t('rcl.culprit', lang)}</b>{' '}
          {culpritIsService(culprit) ? <EntityLink to={{ kind: 'service', name: culprit }}>{culprit}</EntityLink> : <span className="mono">{culprit}</span>}
        </div>
      )}
      {label.resolution && <div><b>{t('case.resolution', lang)}</b> {label.resolution}</div>}
      {label.link && (
        <div><SafeHref href={label.link}>{t('rcl.link', lang)} ↗</SafeHref></div>
      )}
    </div>
  );
}

function LabelForm({ problemId, label, suggested, categories, onDone }: {
  problemId: string;
  label: RootCauseLabel | null;
  suggested: string;
  categories: string[];
  onDone: () => void;
}) {
  const lang = useLang();
  const qc = useQueryClient();
  const [v, setV] = useState<RootCauseLabelInput>({
    cause: label?.cause ?? suggested,
    category: label?.category ?? (categories.includes('other') ? 'other' : categories[0] ?? ''),
    culprit: label?.culprit ?? '',
    link: label?.link ?? '',
    resolution: label?.resolution ?? '',
  });
  const [busy, setBusy] = useState(false);
  const set = (k: keyof RootCauseLabelInput) => (e: { target: { value: string } }) => setV(p => ({ ...p, [k]: e.target.value }));
  const note = !label && suggested ? independenceNote(suggested, v.cause, lang) : null;
  const save = async () => {
    setBusy(true);
    try {
      await api.putRootCauseLabel(problemId, v);
      toast.success(t('rcl.toast.ok', lang));
      await qc.invalidateQueries({ queryKey: ['root-cause-label', problemId] });
      await qc.invalidateQueries({ queryKey: ['incident-case', 'problem', problemId] });
      onDone();
    } catch (e) {
      toast.error(tf('rcl.toast.err', lang, { e: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div style={{ display: 'grid', gap: 8, maxWidth: 720 }}>
      <TextareaField label={t('rcl.f.cause', lang)} rows={3} value={v.cause} onChange={set('cause')}
        hint={note?.text} />
      <SelectField label={t('case.f.category', lang)} value={v.category} onChange={set('category')}>
        {categories.map(k => <option key={k} value={k}>{categoryLabel(k, lang)}</option>)}
      </SelectField>
      <Field label={t('rcl.f.culprit', lang)} hint={t('rcl.f.culprit.hint', lang)} value={v.culprit} onChange={set('culprit')} maxLength={200} />
      <Field label={t('rcl.f.link', lang)} type="url" placeholder="https://" value={v.link} onChange={set('link')} maxLength={500} />
      <TextareaField label={t('case.f.resolution', lang)} rows={2} value={v.resolution} onChange={set('resolution')} />
      <div style={{ display: 'flex', gap: 8 }}>
        <Button size="sm" onClick={save} loading={busy} disabled={!v.cause.trim() || !v.category}>{t('rcl.btn.save', lang)}</Button>
        <Button variant="ghost" size="sm" disabled={busy} onClick={onDone}>{t('rcl.btn.cancel', lang)}</Button>
      </div>
    </div>
  );
}
