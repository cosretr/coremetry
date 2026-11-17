import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2313 — metinler arayüz dilinde (TR aynı)
import { useRef } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Label } from '@/components/shadcn/label';
import { CheckboxField, Field, SelectField } from '@/components/coremetry/Field';
import type { ArgoCDTokenStatus } from '@/lib/types';
import {
  TOKEN_REF_INVALID, hubName, normalizeApiUrl, validTokenRef,
  type BufferErrors, type HubDraft, type InstanceBuffer, type RemoteCluster,
} from './argocdForm';

// ArgoCDInstanceForm — v0.10.974 — Instance tablosunun satır içi düzenleme
// formu (mockup Main "inst-edit"). Açık satırın hemen altında, tek açık form;
// "Tabloya uygula" taslağa yazar, Kaydet'e kadar hiçbir şey sunucuya gitmez.
//
//   • Kayıtlı satırın kimliği SALT OKUNUR (ClickHouse instance_id: v0.10.983'ten
//     beri argocd_app_status, v0.10.985'ten beri argocd_app_mapping satırları
//     buna bağlı; sunucu da reddeder, BE3). Kaydedilmemiş satırın kimliği düzenlenebilir.
//   • Hub seçimi yalnız GÜNCEL hub'lar (listeden çıkarılan hub seçilemez).
//   • API URL canlı önizleme: sunucunun kaydedeceği biçim (NormalizeAPIURL
//     aynası; port eklenmez, yol korunur).
//   • tokenRef: kutu HEP boş başlar (boş = kayıtlı korunur); kayıtlı
//     referans ve çözüm durumu ayrı satırda, "Referansı kaldır" / "Geri al".
//     Çözülen token değeri hiçbir yerde yok — yalnız referans.
//   • v0.10.974 — kaydedilmemiş satırda yazılmış geçerli ref mockup'ın
//     "Kayıtlı referans: <ref> · yeni, kaydedince denetlenir" hâlini alır
//     (Token kolonuyla aynı söz; "Kayıtlı referans yok" yalnız ne kayıtlı ne
//     yazılı ref varken). "Referansı kaldır" ⇄ "Geri al" TEK düğmedir: tıklanan
//     düğme DOM'dan düşmez, klavye odağı <body>'ye kaçmaz; yeni satırda kaldırma
//     kutuyu boşaltır ve odağı kutuya verir.
//   • Faz 2 dilim 1 R15: ui/ → coremetry/Field + shadcn. tokenRef alanı elle
//     (shadcn Label + Input, Field anatomisi): durum satırı (`acd-ed-ref-s`)
//     denetimle ipucu ARASINDA durur ve "· kayıtlı" etiketin İÇİNDE kalır
//     (secret sözleşmesi: kutu boş başlar, değer yankılanmaz).

const HINT = 'text-[length:var(--fs-xs)] leading-snug text-(--text3)';
const ERR = 'text-[length:var(--fs-xs)] leading-snug text-status-critical';
const FAINT = 'text-(--text3)';
const LINK = 'h-auto p-0 text-[length:var(--fs-xs)] text-(--text2)';

export function ArgoCDInstanceForm({ buffer: b, errors, pending, hubs, clusters, tokens, onChange, onApply, onCancel, onRemove }: {
  buffer: InstanceBuffer;
  errors: BufferErrors;
  pending: boolean;
  hubs: HubDraft[];
  clusters: RemoteCluster[];
  tokens: Record<string, ArgoCDTokenStatus>;
  onChange: (next: InstanceBuffer) => void;
  onApply: () => void;
  onCancel: () => void;
  onRemove: () => void;
}) {
  const set = (p: Partial<InstanceBuffer>) => onChange({ ...b, ...p });
  const refInput = useRef<HTMLInputElement>(null);
  const lang = useLang();
  const hn = hubName(b.hubClusterId, clusters);
  const idEditable = b.isNew || b.unsaved;
  const url = normalizeApiUrl(b.apiUrl);
  const urlErr = url.error || errors.url || '';
  const typed = b.tokenInput.trim();
  const refErr = typed && !validTokenRef(typed) ? TOKEN_REF_INVALID : (errors.ref ?? '');
  const stored = !!b.storedRef && !b.clearToken;
  const st = b.savedId ? tokens[b.savedId] : undefined;
  const resText = !st || st.tokenRef !== b.storedRef ? ''
    : st.resolved ? t('acd.ed.resolved', lang) : tf('acd.ed.unresolved', lang, { e: (st.error ?? '').replace(/^tokenRef \S+: /, '') });
  const nField = Object.values(errors).filter(Boolean).length;
  const summary = [
    pending ? t('acd.pendingForm', lang) : '',
    nField ? tf('acd.ed.nField', lang, { n: nField }) : '',
  ].filter(Boolean).join(' ');
  const hubMissing = !hubs.some(h => h.clusterId === b.hubClusterId);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-4">
        <h4 className="m-0 text-[length:var(--fs-md)] font-semibold text-(--text)">{b.isNew ? t('acd.ed.newTitle', lang) : tf('acd.ed.editing', lang, { id: b.origId })}</h4>
        <span className={HINT}>
          {b.isNew ? t('acd.ed.newSub', lang)
            : `${hn} · ${t(b.discovered ? 'acd.ed.fromDisc' : 'acd.ed.byHand', lang)}${b.unsaved ? t('acd.ed.unsavedSuffix', lang) : ''}`}
        </span>
      </div>

      <div className="grid grid-cols-3 gap-4">
        <Field id="acd-ed-id" label={t('acd.ed.id', lang)} controlClassName="font-mono" value={b.id} readOnly={!idEditable}
          autoComplete="off" spellCheck={false} error={errors.id}
          hint={b.isNew ? t('acd.ed.idHintNew', lang)
            : b.unsaved ? tf('acd.ed.idHintUnsaved', lang, { ex: `${b.origId}-${hn}` })
              : t('acd.ed.idHintSaved', lang)}
          onChange={e => set({ id: e.target.value })} />
        <Field id="acd-ed-name" label={t('acd.ed.name', lang)} value={b.name} autoComplete="off" placeholder={t('acd.ed.namePh', lang)}
          error={errors.name} hint={t('acd.ed.nameHint', lang)} onChange={e => set({ name: e.target.value })} />
        <SelectField id="acd-ed-hub" label="Hub" controlClassName="font-mono" value={b.hubClusterId} error={errors.hub}
          hint={tf('acd.ed.hubHint', lang, { hub: hn })}
          onChange={e => set({ hubClusterId: e.target.value })}>
          {hubMissing && <option value={b.hubClusterId} disabled>{tf('acd.ed.notListed', lang, { hub: hn })}</option>}
          {hubs.map(h => <option key={h.key} value={h.clusterId}>{hubName(h.clusterId, clusters)}</option>)}
        </SelectField>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <Field id="acd-ed-ns" label="Hub namespace" controlClassName="font-mono" value={b.hubNamespace} autoComplete="off" spellCheck={false}
          error={errors.ns} hint={t('acd.ed.nsHint', lang)}
          onChange={e => set({ hubNamespace: e.target.value })} />
        <Field id="acd-ed-job" label={<>metricsJob <span className={FAINT}>{t('acd.ed.optional', lang)}</span></>} controlClassName="font-mono"
          value={b.metricsJob} autoComplete="off" spellCheck={false} placeholder="team-a-prod-metrics" error={errors.job}
          hint={<>{t('acd.ed.jobHint.a', lang)}<code>job</code>{t('acd.ed.jobHint.b', lang)}</>}
          onChange={e => set({ metricsJob: e.target.value })} />
      </div>

      <Field id="acd-ed-url" label={<>API URL <span className={FAINT}>{t('acd.ed.urlSub', lang)}</span></>} controlClassName="font-mono"
        value={b.apiUrl} autoComplete="off" spellCheck={false} placeholder="https://argocd.team-a.example.invalid"
        error={urlErr || undefined}
        hint={url.value
          ? <>{t('acd.ed.urlHintFmt', lang)}<span className="font-mono">{url.value}</span></>
          : t('acd.ed.urlHintEmpty', lang)}
        onChange={e => set({ apiUrl: e.target.value })} />

      <div data-cm-field="" className="flex min-w-0 flex-col gap-1">
        <Label htmlFor="acd-ed-ref" className="text-[length:var(--fs-sm)] font-normal leading-tight text-(--text2)">
          {t('acd.ed.tokenRef', lang)}<span className="font-mono">tokenRef</span>
          {stored && <span className="ml-2 text-(--text3)">{t('acd.ed.savedSuffix', lang)}</span>}
        </Label>
        <Input id="acd-ed-ref" ref={refInput} value={b.tokenInput} autoComplete="off" spellCheck={false}
          className="h-8 border-(--border-control) bg-field font-mono text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field"
          aria-invalid={refErr ? 'true' : undefined} aria-describedby="acd-ed-ref-s acd-ed-ref-h"
          placeholder={stored ? t('acd.ed.refKeepPh', lang) : 'env:ARGOCD_TEAM_C_TOKEN  ·  file:/etc/coremetry/secrets/argocd-team-c'}
          onChange={e => set({ tokenInput: e.target.value })} />
        <div id="acd-ed-ref-s" className={`flex flex-wrap items-center gap-3 ${HINT}`}>
          {b.storedRef ? (
            <>
              {b.clearToken
                ? <span>{t('acd.ed.refCleared', lang)}</span>
                : (
                  <>
                    <span>{t('acd.ed.savedRef', lang)}</span>
                    <span className="font-mono">{b.storedRef}</span>
                    {resText && <span className={st && !st.resolved ? 'text-status-critical' : undefined}>{resText}</span>}
                  </>
                )}
              <Button variant="link" size="xs" className={LINK} id="acd-ed-ref-toggle"
                onClick={() => set(b.clearToken ? { clearToken: false } : { clearToken: true, tokenInput: '' })}>
                {b.clearToken ? t('acd.undo', lang) : t('acd.ed.removeRef', lang)}
              </Button>
            </>
          ) : typed && validTokenRef(typed) ? (
            <>
              <span>{t('acd.ed.savedRef', lang)}</span>
              <span className="font-mono">{typed}</span>
              <span>{t('acd.ed.newRef', lang)}</span>
              <Button variant="link" size="xs" className={LINK} id="acd-ed-ref-toggle" onClick={() => { set({ tokenInput: '' }); refInput.current?.focus(); }}>
                {t('acd.ed.removeRef', lang)}
              </Button>
            </>
          ) : <span>{t('acd.ed.noRef', lang)}</span>}
        </div>
        <span id="acd-ed-ref-h" className={refErr ? ERR : HINT}>
          {refErr || t('acd.ed.refHint', lang)}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-6">
        <CheckboxField id="acd-ed-enabled" label={t('acd.ed.enabled', lang)} checked={b.enabled} onCheckedChange={v => set({ enabled: v })} />
        <CheckboxField id="acd-ed-insecure" label={<>{t('acd.ed.insecure', lang)}<span className={FAINT}>{t('acd.ed.insecureSub', lang)}</span></>}
          checked={b.insecureSkipVerify} onCheckedChange={v => set({ insecureSkipVerify: v })} />
        <CheckboxField id="acd-ed-anyns" label={<>{t('acd.ed.anyNs', lang)}<span className={`font-mono ${FAINT}`}>exported_namespace ≠ namespace</span></>}
          checked={b.appsAnyNamespace} onCheckedChange={v => set({ appsAnyNamespace: v })} />
      </div>

      <div className="flex flex-wrap items-center gap-4">
        <Button size="sm" id="acd-ed-apply" onClick={onApply}>{t('acd.ed.apply', lang)}</Button>
        <Button variant="ghost" size="sm" onClick={onCancel}>{t('acd.cancel', lang)}</Button>
        {!b.isNew && <span className={HINT}>{t('acd.ed.untilSave', lang)}</span>}
        <span className="flex-1" />
        <Button variant="ghost-danger" size="sm" onClick={onRemove}>{t('acd.removeFromTable', lang)}</Button>
      </div>
      {summary && <div role="alert" className={ERR}>{summary}</div>}
    </div>
  );
}
