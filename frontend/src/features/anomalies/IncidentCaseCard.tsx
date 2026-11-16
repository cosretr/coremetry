// IncidentCaseCard — CoSRE öğrenme Faz 3: kapanan Problem / çözülen exception
// grubunun vaka kaydı. Taslak (CoSRE kurdu) → editor tek "Onayla" ile nihai
// nedeni doğrular; form CoSRE'nin önerisiyle önceden dolu, değiştirilebilir.
// Onaylanmamış vaka hiçbir yerde olgu sayılmaz (similar_incidents, çekmece
// kanıtı "ONAYSIZ" işaretler). Viewer kartı salt-okunur görür. Vaka yoksa
// (açık problem / taslak henüz kurulmadı) kart çizilmez.
//
// v0.10.1475: ConfirmForm + CaseSignals /ai "Onay bekleyen vakalar"
// listesinin satır açılımında da kullanılır (tek form, çatal yok). Form
// öneriyle dolu ama düzeltmek kabul etmek kadar kolay: "Öneriyi temizle"
// tek tık, canlı not onayın manşet "bağımsız" ölçüye sayılıp sayılmadığını
// söyler (chstore.SameCause aynası). Hiçbir şey otomatik onaylanmaz.
import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Button, SelectField, TextareaField } from '@/components/ui';
import { toast } from '@/lib/toast';
import type { IncidentCase } from '@/lib/types';
import { Sect } from './detailSections';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2285 — metinler arayüz dilinde (TR aynı)
import { caseSignalLines, categoryLabel, correctnessLabel, defaultCaseCategory, independenceNote } from './incidentCaseView';

export function IncidentCaseCard({ source, sourceId }: { source: 'problem' | 'exception'; sourceId: string }) {
  const q = useQuery({
    queryKey: ['incident-case', source, sourceId],
    queryFn: () => api.incidentCase(source, sourceId),
    staleTime: 60_000,
  });
  const lang = useLang();
  const c = q.data?.case;
  if (!c) return null;
  return (
    <Sect title={t('case.title', lang)} accent={c.status === 'draft'}
      sub={c.status === 'confirmed'
        ? tf('case.confirmed', lang, { who: c.confirmedBy ?? '' })
        : t('case.draft', lang)}>
      <CaseSignals c={c} />
      {c.status === 'confirmed'
        ? <ConfirmedCase c={c} />
        : <ConfirmForm c={c} categories={q.data?.categories ?? []} />}
    </Sect>
  );
}

export function CaseSignals({ c }: { c: IncidentCase }) {
  const lang = useLang();
  const lines = caseSignalLines(c, lang);
  if (lines.length === 0) return null;
  return (
    <ul style={{ margin: '0 0 10px', paddingLeft: 18, fontSize: 13, color: 'var(--text2)' }}>
      {lines.map(l => <li key={l}>{l}</li>)}
    </ul>
  );
}

function ConfirmedCase({ c }: { c: IncidentCase }) {
  const lang = useLang();
  return (
    <div style={{ fontSize: 13, display: 'grid', gap: 4 }}>
      <div><b>{t('case.cause', lang)}</b> {c.finalCause}</div>
      <div><b>{t('case.category', lang)}</b> {categoryLabel(c.category, lang)}</div>
      {c.resolution && <div><b>{t('case.resolution', lang)}</b> {c.resolution}</div>}
      <div style={{ color: 'var(--text3)', fontSize: 12 }}>{tf('case.cosre', lang, { v: correctnessLabel(c.cosreCorrect, lang) })}</div>
    </div>
  );
}

export function ConfirmForm({ c, categories, onConfirmed }: {
  c: IncidentCase;
  categories: string[];
  /** Onay sunucuda yazıldıktan SONRA (ör. /ai listesi sıradaki satırı açar). */
  onConfirmed?: (confirmed: IncidentCase) => void;
}) {
  const { user } = useAuth();
  const isEditor = user?.role === 'admin' || user?.role === 'editor';
  const qc = useQueryClient();
  const [cause, setCause] = useState(c.suggestedCause);
  const [category, setCategory] = useState(defaultCaseCategory(c));
  const [resolution, setResolution] = useState('');
  const [busy, setBusy] = useState(false);
  const lang = useLang();
  useEffect(() => { setCause(c.suggestedCause); }, [c.caseId, c.suggestedCause]);
  if (!isEditor) {
    return (
      <div style={{ fontSize: 13 }}>
        <div><b>{t('case.suggested', lang)}</b> {c.suggestedCause || '—'}</div>
        <div style={{ color: 'var(--text3)', fontSize: 12, marginTop: 4 }}>{t('case.needsRole', lang)}</div>
      </div>
    );
  }
  const note = independenceNote(c.suggestedCause, cause, lang);
  const confirm = async () => {
    setBusy(true);
    try {
      const out = await api.confirmIncidentCase(c.caseId, { finalCause: cause, category, resolution });
      toast.success(t('case.toast.ok', lang));
      await qc.invalidateQueries({ queryKey: ['incident-case', c.source, c.sourceId] });
      onConfirmed?.(out?.case ?? c);
    } catch (e) {
      toast.error(tf('case.toast.err', lang, { e: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div style={{ display: 'grid', gap: 8, maxWidth: 720 }}>
      <TextareaField label={t('case.f.cause', lang)} rows={3} value={cause} onChange={e => setCause(e.target.value)}
        hint={t(c.suggestedCause ? 'case.f.cause.prefilled' : 'case.f.cause.none', lang)} />
      {c.suggestedCause && (
        <div className={note.independent ? 'field-hint' : 'field-hint case-accepts-suggestion'} data-testid="case-independence">
          {note.text}
        </div>
      )}
      <SelectField label={t('case.f.category', lang)} value={category} onChange={e => setCategory(e.target.value)}>
        {categories.map(k => <option key={k} value={k}>{categoryLabel(k, lang)}</option>)}
      </SelectField>
      <TextareaField label={t('case.f.resolution', lang)} rows={2} value={resolution} onChange={e => setResolution(e.target.value)} />
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <Button variant="primary" size="sm" onClick={confirm} loading={busy} disabled={!cause.trim()}>
          {t('case.btn.confirm', lang)}
        </Button>
        {c.suggestedCause && (
          <Button variant="secondary" size="sm" disabled={busy || cause === ''} onClick={() => setCause('')}
            title={t('case.btn.clear.title', lang)}>
            {t('case.btn.clear', lang)}
          </Button>
        )}
        {categories.includes('noise') && (
          <Button variant="ghost" size="sm" disabled={busy || category === 'noise'} onClick={() => setCategory('noise')}
            title={t('case.btn.noise.title', lang)}>
            {t('case.btn.noise', lang)}
          </Button>
        )}
      </div>
    </div>
  );
}
