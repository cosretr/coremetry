import { useRef, type CSSProperties } from 'react';
import { Button, Field, LinkButton, SelectField } from '@/components/ui';
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

const TITLE: CSSProperties = { margin: 0, fontSize: 'var(--fs-md)', fontWeight: 600, color: 'var(--text)' };

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
  const hn = hubName(b.hubClusterId, clusters);
  const idEditable = b.isNew || b.unsaved;
  const url = normalizeApiUrl(b.apiUrl);
  const urlErr = url.error || errors.url || '';
  const typed = b.tokenInput.trim();
  const refErr = typed && !validTokenRef(typed) ? TOKEN_REF_INVALID : (errors.ref ?? '');
  const stored = !!b.storedRef && !b.clearToken;
  const st = b.savedId ? tokens[b.savedId] : undefined;
  const resText = !st || st.tokenRef !== b.storedRef ? ''
    : st.resolved ? '· çözüldü' : `· çözülemedi: ${(st.error ?? '').replace(/^tokenRef \S+: /, '')}`;
  const nField = Object.values(errors).filter(Boolean).length;
  const summary = [
    pending ? 'Açık formda tabloya uygulanmamış değişiklik var: önce “Tabloya uygula” ya da “Vazgeç”.' : '',
    nField ? `${nField} alan düzeltilmeli; ayrıntı alanların altında.` : '',
  ].filter(Boolean).join(' ');
  const hubMissing = !hubs.some(h => h.clusterId === b.hubClusterId);

  return (
    <div className="stack gap-4">
      <div className="row gap-4 row-wrap">
        <h4 style={TITLE}>{b.isNew ? 'Yeni instance' : `${b.origId} düzenleniyor`}</h4>
        <span className="field-hint">
          {b.isNew ? 'elle ekleniyor · Kaydet’e kadar yazılmaz'
            : `${hn} · ${b.discovered ? 'keşiften eklendi' : 'elle eklendi'}${b.unsaved ? ' · kaydedilmedi' : ''}`}
        </span>
      </div>

      <div className="grid-3 gap-4">
        <Field id="acd-ed-id" label="Kimlik (id)" className="mono" value={b.id} readOnly={!idEditable}
          autoComplete="off" spellCheck={false} error={errors.id}
          hint={b.isNew ? "Küçük harf, rakam, tire (≤63). Hub'lar arasında da tekil."
            : b.unsaved ? `Kaydedilmemiş satır: kimlik şimdi değiştirilebilir (ör. ${b.origId}-${hn}); Kaydet'ten sonra değişmez.`
              : "Kayıttan sonra değişmez: ClickHouse instance_id (durum ve eşleme satırları bu kimliğe bağlı)."}
          onChange={e => set({ id: e.target.value })} />
        <Field id="acd-ed-name" label="Görünen ad" value={b.name} autoComplete="off" placeholder="boşsa id gösterilir"
          error={errors.name} hint="Yalnız ekranda; eşlemeye katılmaz." onChange={e => set({ name: e.target.value })} />
        <SelectField id="acd-ed-hub" label="Hub" className="mono" value={b.hubClusterId} error={errors.hub}
          hint={`Küme-içi hedef (kubernetes.default.svc) → ${hn}`}
          onChange={e => set({ hubClusterId: e.target.value })}>
          {hubMissing && <option value={b.hubClusterId} disabled>{hn} — listede değil</option>}
          {hubs.map(h => <option key={h.key} value={h.clusterId}>{hubName(h.clusterId, clusters)}</option>)}
        </SelectField>
      </div>

      <div className="grid-2 gap-4">
        <Field id="acd-ed-ns" label="Hub namespace" className="mono" value={b.hubNamespace} autoComplete="off" spellCheck={false}
          error={errors.ns} hint="Argo CD'nin çalıştığı namespace (zorunlu). Aynı hub'da namespace başına tek instance."
          onChange={e => set({ hubNamespace: e.target.value })} />
        <Field id="acd-ed-job" label={<>metricsJob <span className="cell-faint">(opsiyonel)</span></>} className="mono"
          value={b.metricsJob} autoComplete="off" spellCheck={false} placeholder="team-a-prod-metrics" error={errors.job}
          hint={<>Serilerdeki <code>job</code> etiketi. Boşsa yalnız namespace ile eşlenir.</>}
          onChange={e => set({ metricsJob: e.target.value })} />
      </div>

      <Field id="acd-ed-url" label={<>API URL <span className="cell-faint">(Argo CD sunucusu)</span></>} className="mono"
        value={b.apiUrl} autoComplete="off" spellCheck={false} placeholder="https://argocd.team-a.example.invalid"
        error={urlErr || undefined}
        hint={url.value
          ? <>Asla türetilmez. Kaydedilecek biçim (küçük harf, sondaki / atılır; yol korunur, port eklenmez): <span className="mono">{url.value}</span></>
          : "Asla türetilmez. Boşsa API işçisi bu instance'ı atlar."}
        onChange={e => set({ apiUrl: e.target.value })} />

      <div className="field">
        <label htmlFor="acd-ed-ref" className="field-label">
          Token referansı <span className="mono">tokenRef</span>
          {stored && <span style={{ marginLeft: 'var(--sp-4)', color: 'var(--text3)' }}>· kayıtlı</span>}
        </label>
        <input id="acd-ed-ref" ref={refInput} className="mono" value={b.tokenInput} autoComplete="off" spellCheck={false}
          aria-invalid={refErr ? 'true' : undefined} aria-describedby="acd-ed-ref-s acd-ed-ref-h"
          placeholder={stored ? 'boş bırak = kayıtlı referans korunur' : 'env:ARGOCD_TEAM_C_TOKEN  ·  file:/etc/coremetry/secrets/argocd-team-c'}
          onChange={e => set({ tokenInput: e.target.value })} />
        <div id="acd-ed-ref-s" className="row gap-3 row-wrap field-hint">
          {b.storedRef ? (
            <>
              {b.clearToken
                ? <span>Kaydedince referans silinir; instance yalnız metriklerle izlenir.</span>
                : (
                  <>
                    <span>Kayıtlı referans:</span>
                    <span className="mono">{b.storedRef}</span>
                    {resText && <span className={st && !st.resolved ? 'is-err' : undefined}>{resText}</span>}
                  </>
                )}
              <LinkButton id="acd-ed-ref-toggle" tone="muted"
                onClick={() => set(b.clearToken ? { clearToken: false } : { clearToken: true, tokenInput: '' })}>
                {b.clearToken ? 'Geri al' : 'Referansı kaldır'}
              </LinkButton>
            </>
          ) : typed && validTokenRef(typed) ? (
            <>
              <span>Kayıtlı referans:</span>
              <span className="mono">{typed}</span>
              <span>· yeni, kaydedince denetlenir</span>
              <LinkButton id="acd-ed-ref-toggle" tone="muted" onClick={() => { set({ tokenInput: '' }); refInput.current?.focus(); }}>
                Referansı kaldır
              </LinkButton>
            </>
          ) : <span>Kayıtlı referans yok — instance yalnız metriklerle izlenir (API işçisi atlar).</span>}
        </div>
        <span id="acd-ed-ref-h" className={refErr ? 'field-error' : 'field-hint'}>
          {refErr || 'env:NAME → Helm extraEnv + existingSecret · file:/mutlak/yol → mount edilmiş Secret. Düz token alanı yok; çözülemeyen referansta instance atlanır (fail-closed).'}
        </span>
      </div>

      <div className="row gap-6 row-wrap">
        <label className="row gap-2">
          <input type="checkbox" checked={b.enabled} onChange={e => set({ enabled: e.target.checked })} />
          Etkin
        </label>
        <label className="row gap-2">
          <input type="checkbox" checked={b.insecureSkipVerify} onChange={e => set({ insecureSkipVerify: e.target.checked })} />
          TLS doğrulamasını atla <span className="cell-faint">(iç CA / POC)</span>
        </label>
        <label className="row gap-2">
          <input type="checkbox" checked={b.appsAnyNamespace} onChange={e => set({ appsAnyNamespace: e.target.checked })} />
          Application'lar her namespace'te <span className="mono cell-faint">exported_namespace ≠ namespace</span>
        </label>
      </div>

      <div className="row gap-4 row-wrap">
        <Button variant="primary" size="sm" id="acd-ed-apply" onClick={onApply}>Tabloya uygula</Button>
        <Button variant="ghost" size="sm" onClick={onCancel}>Vazgeç</Button>
        {!b.isNew && <span className="field-hint">Değişiklik Kaydet’e kadar yazılmaz.</span>}
        <span className="row-grow" />
        <Button variant="ghost-danger" size="sm" onClick={onRemove}>Tablodan kaldır</Button>
      </div>
      {summary && <div role="alert" className="field-error">{summary}</div>}
    </div>
  );
}
