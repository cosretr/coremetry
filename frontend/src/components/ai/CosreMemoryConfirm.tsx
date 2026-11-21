// CosreMemoryConfirm — CoSRE Faz E (ekip notları). İki AÇIK düzeltme yolu,
// ikisi de tık-only kayıt (POST /api/ai/memory, source correction, editör+,
// audit); tıklanmadan hiçbir şey saklanmaz:
//   • CosreMemoryConfirm — sohbette "Düzeltme: …" mesajının onay kartı
//     (sunucu chat_correction.go): metin + kapsam düzenlenir.
//   • CosreCorrectForm — cevabın altındaki "Düzelt" düğmesinin formu (v2
//     sohbet eylem satırı): doğru bilgi + kapsam (genel ya da servis).
// CoSRE yüzeyi sabit Türkçe (tCosre emsali). Görünüş CosreRuleConfirm ile
// aynı (cosre-rule-card sınıfları; yeni CSS yok). Viewer kartı görür, düğme
// gerekçesiyle kapalı. Tembel yüklenir (ChatBubble / CosreCorrectButton).
import { useId, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { SelectField, TextareaField } from '@/components/coremetry/Field';
import { ServicePicker } from '@/components/ServicePicker';
import { api } from '@/lib/api';
import { toast } from '@/lib/toast';
import { cosreRuleScopeKey, type CosreMemoryAction } from '@/lib/pageActions';
import type { CosreRuleScope } from '@/lib/types';
import { COSRE_MEMORY_KEY, MEM_MAX, memSingleLine, memStatementError } from './cosreMemory';
import '@/styles/cosreChat.css';

const VIEWER_REASON = 'Ekip notu kaydetmek editör ya da admin rolü ister.';
const SAVED_TOAST = 'Not kaydedildi — CoSRE bu kapsamda bundan sonra veri olarak dikkate alır';

function useSaveCorrection(exchangeId?: string) {
  const qc = useQueryClient();
  const [state, setState] = useState<'idle' | 'busy' | 'done'>('idle');
  const save = async (statement: string, scope: CosreRuleScope) => {
    setState('busy');
    try {
      await api.createCosreMemory({ statement: statement.trim(), scope: scope.kind === 'global' ? { kind: 'global' } : { kind: scope.kind, value: scope.value },
        source: { kind: 'correction', exchangeId } });
      setState('done');
      toast.success(SAVED_TOAST);
      await qc.invalidateQueries({ queryKey: COSRE_MEMORY_KEY });
    } catch (e) {
      setState('idle');
      toast.error(`Not kaydedilemedi: ${e instanceof Error ? e.message : String(e)}`);
    }
  };
  return { state, save };
}

export function CosreMemoryConfirm({ action }: { action: CosreMemoryAction }) {
  const [text, setText] = useState(action.statement);
  const [scopeKey, setScopeKey] = useState(cosreRuleScopeKey(action.scope));
  const titleId = useId();
  const { state, save } = useSaveCorrection(action.exchangeId);
  const scope = useMemo(() => action.scopeOptions.find(o => cosreRuleScopeKey(o) === scopeKey) ?? action.scope,
    [action.scopeOptions, action.scope, scopeKey]);
  const err = memStatementError(text, 'tr');
  const reason = !action.canSave ? (action.reason ?? VIEWER_REASON) : undefined;
  const done = state === 'done';
  return (
    <section className="cosre-rule-card" aria-labelledby={titleId} data-testid="cosre-memory-confirm">
      <div id={titleId} className="cosre-rule-card__title">📝 Ekip notu önerisi (düzeltme)</div>
      <TextareaField label="Not" rows={2} value={text} maxLength={MEM_MAX + 20} readOnly={done || !action.canSave}
        error={done ? undefined : err} hint={`${Array.from(text.trim()).length}/${MEM_MAX} · tek cümle, olgu; yazan ve tarih notla birlikte görünür`}
        onChange={e => setText(memSingleLine(e.target.value))} />
      <SelectField label="Kapsam" value={scopeKey} disabled={done || !action.canSave} onChange={e => setScopeKey(e.target.value)}>
        {action.scopeOptions.map(o => <option key={cosreRuleScopeKey(o)} value={cosreRuleScopeKey(o)}>{o.label}</option>)}
      </SelectField>
      <div className="cosre-rule-card__actions">
        <Button size="sm" loading={state === 'busy'} disabled={done || !!err || !action.canSave} title={reason}
          onClick={() => { void save(text, scope); }}>
          {done ? 'Kaydedildi' : 'Kaydet'}
        </Button>
        <span className="cosre-rule-card__reason">{reason ?? (done ? '' : "Kaydet'e basmadan hiçbir şey saklanmaz.")}</span>
      </div>
    </section>
  );
}

export function CosreCorrectForm({ exchangeId, canSave, onClose }: { exchangeId?: string; canSave: boolean; onClose: () => void }) {
  const [text, setText] = useState('');
  const [kind, setKind] = useState<'global' | 'service'>('service');
  const [service, setService] = useState('');
  const titleId = useId();
  const { state, save } = useSaveCorrection(exchangeId);
  const err = text ? memStatementError(text, 'tr') : undefined;
  const scope: CosreRuleScope = kind === 'service' ? { kind: 'service', value: service.trim() } : { kind: 'global' };
  const invalid = !!memStatementError(text, 'tr') || (kind === 'service' && !service.trim());
  if (state === 'done') return <span className="cell-hint" data-cosre-correct-sent="">✓ Düzeltme ekip notu olarak kaydedildi</span>;
  return (
    <section className="cosre-rule-card" aria-labelledby={titleId} data-testid="cosre-correct-form">
      <div id={titleId} className="cosre-rule-card__title">✎ Cevabı düzelt — doğrusu nedir?</div>
      <TextareaField label="Doğru bilgi" rows={2} value={text} autoFocus maxLength={MEM_MAX + 20} readOnly={!canSave}
        error={err} hint={`${Array.from(text.trim()).length}/${MEM_MAX} · ör. "checkout'un veritabanı orders-db değil, pay-db"`}
        onChange={e => setText(memSingleLine(e.target.value))} />
      <SelectField label="Kapsam" value={kind} disabled={!canSave} onChange={e => setKind(e.target.value as 'global' | 'service')}>
        <option value="service">servis</option>
        <option value="global">genel</option>
      </SelectField>
      {kind === 'service' && (
        <div className="field">
          <span className="field-label">Servis</span>
          <ServicePicker value={service} onChange={setService} placeholder="servis ara…" />
        </div>
      )}
      <div className="cosre-rule-card__actions">
        <Button size="sm" loading={state === 'busy'} disabled={!canSave || invalid || state === 'busy'} title={canSave ? undefined : VIEWER_REASON}
          onClick={() => { void save(text, scope); }}>Kaydet</Button>
        <Button variant="secondary" size="sm" onClick={onClose}>Vazgeç</Button>
        <span className="cosre-rule-card__reason">{canSave ? 'Oylar not olmaz; yalnız bu açık düzeltme kaydedilir.' : VIEWER_REASON}</span>
      </div>
    </section>
  );
}

export default CosreMemoryConfirm;
