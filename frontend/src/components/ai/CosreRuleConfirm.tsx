// CosreRuleConfirm — v0.10.1289 (CoSRE kurallar / hafıza: "bunu hatırla").
// Sohbetin hafıza kademesinin onay kartı. Yük sunucudan (chat_memory.go
// cosreRuleAction): önerilen metin + varsayılan kapsam + seçenekler. Kart
// metni ve kapsamı DÜZENLETİR; yalnız "Kaydet" tıkı POST /api/ai/rules
// çağırır (editör+, audit) — tıklanmadan hiçbir şey kaydedilmez. Viewer kartı
// görür, düğme sunucunun gerekçesiyle kapalı (canSave=false).
import { useId, useMemo, useState } from 'react';
import '@/styles/cosreChat.css';
import { useQueryClient } from '@tanstack/react-query';
import { Button, SelectField, TextareaField } from '@/components/ui';
import { api } from '@/lib/api';
import { COSRE_RULES_KEY } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { cosreRuleScopeKey, type CosreRuleAction } from '@/lib/pageActions';
import { RULE_MAX, ruleTextError, singleLine } from './cosreRules';

export function CosreRuleConfirm({ action, onSaved }: { action: CosreRuleAction; onSaved?: () => void }) {
  const qc = useQueryClient();
  const [text, setText] = useState(action.text);
  const [scopeKey, setScopeKey] = useState(cosreRuleScopeKey(action.scope));
  const [state, setState] = useState<'idle' | 'busy' | 'done'>('idle');
  const titleId = useId();
  const scope = useMemo(
    () => action.scopeOptions.find(o => cosreRuleScopeKey(o) === scopeKey) ?? action.scope,
    [action.scopeOptions, action.scope, scopeKey],
  );
  const err = ruleTextError(text);
  const disabledReason = !action.canSave ? (action.reason ?? 'Kural kaydetmek editör ya da admin rolü ister.') : undefined;
  const save = async () => {
    setState('busy');
    try {
      await api.createCosreRule({ text: text.trim(), scope: scope.kind === 'global' ? { kind: 'global' } : { kind: scope.kind, value: scope.value } });
      setState('done');
      onSaved?.();
      toast.success('Kural kaydedildi — CoSRE bundan sonra bu bağlamda dikkate alır');
      await qc.invalidateQueries({ queryKey: COSRE_RULES_KEY });
    } catch (e) {
      setState('idle');
      toast.error(`Kural kaydedilemedi: ${e instanceof Error ? e.message : String(e)}`);
    }
  };
  const done = state === 'done';
  return (
    <section className="cosre-rule-card" aria-labelledby={titleId} data-testid="cosre-rule-confirm">
      <div id={titleId} className="cosre-rule-card__title">🧠 Operatör kuralı önerisi</div>
      <TextareaField label="Kural" rows={2} value={text} maxLength={RULE_MAX + 20} readOnly={done || !action.canSave}
        error={done ? undefined : err}
        hint={`${Array.from(text.trim()).length}/${RULE_MAX} · tek cümle; yazan ve tarih kuralla birlikte görünür`}
        onChange={e => setText(singleLine(e.target.value))} />
      <SelectField label="Kapsam" value={scopeKey} disabled={done || !action.canSave}
        onChange={e => setScopeKey(e.target.value)}>
        {action.scopeOptions.map(o => <option key={cosreRuleScopeKey(o)} value={cosreRuleScopeKey(o)}>{o.label}</option>)}
      </SelectField>
      <div className="cosre-rule-card__actions">
        <Button variant="primary" size="sm" loading={state === 'busy'} disabled={done || !!err || !action.canSave}
          title={disabledReason} onClick={() => { void save(); }}>
          {done ? 'Kaydedildi' : 'Kaydet'}
        </Button>
        {disabledReason && <span className="cosre-rule-card__reason">{disabledReason}</span>}
        {!disabledReason && !done && <span className="cosre-rule-card__reason">Kaydet'e basmadan hiçbir şey saklanmaz.</span>}
      </div>
    </section>
  );
}
