// CosreAliasConfirm — v0.10.2335 (conversation-audit §6.1 G). Kullanıcı bir
// varlığı düzelttiğinde ("o değil, payments") sunucunun önerdiği eş ad kartı
// (chat_alias_proposal.go). Kart CosreRuleConfirm'ün yerini ve görünüşünü
// paylaşır; HİÇBİR ŞEY tıklanmadan kaydedilmez: "Kaydet" POST
// /api/cosre/user-prefs/aliases (kullanıcının kendi kaydı, her rol), "Hayır"
// yalnız kartı kapatır (sunucuya istek yok; sunucu aynı konuşmada öneriyi
// yinelemez).
import { useState } from 'react';
import '@/styles/cosreChat.css';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { api } from '@/lib/api';
import { COSRE_USER_PREFS_KEY } from '@/lib/queries';
import { toast } from '@/lib/toast';
import type { CosreAliasAction } from '@/lib/pageActions';

export function CosreAliasConfirm({ action, exchangeId }: { action: CosreAliasAction; exchangeId?: string }) {
  const qc = useQueryClient();
  const [state, setState] = useState<'idle' | 'busy' | 'done' | 'dismissed'>('idle');
  if (state === 'dismissed') {
    return <div className="cm-msg-note" data-testid="cosre-alias-dismissed">Tamam, kaydetmedim.</div>;
  }
  const save = async () => {
    setState('busy');
    try {
      await api.saveCosreAlias({ phrase: action.phrase, entityId: action.entityId, notId: action.notId, exchangeId });
      setState('done');
      toast.success(`Kaydedildi — «${action.phrase}» bundan sonra ${action.name} demek`);
      await qc.invalidateQueries({ queryKey: COSRE_USER_PREFS_KEY });
    } catch (e) {
      setState('idle');
      toast.error(`Kaydedilemedi: ${e instanceof Error ? e.message : String(e)}`);
    }
  };
  const done = state === 'done';
  return (
    <section className="cosre-rule-card" aria-label="Eş ad önerisi" data-testid="cosre-alias-confirm">
      <div className="cosre-rule-card__title">{action.text}</div>
      <div className="cosre-rule-card__actions">
        <Button size="sm" disabled={done || state === 'busy'} aria-busy={state === 'busy' || undefined} onClick={() => { void save(); }}>
          {done ? 'Kaydedildi' : action.label}
        </Button>
        {!done && (
          <Button variant="outline" size="sm" disabled={state === 'busy'} onClick={() => setState('dismissed')}>
            {action.dismissLabel}
          </Button>
        )}
        <span className="cosre-rule-card__reason">
          {done ? 'CoSRE menüsündeki «Eş adlarım» listesinden silebilirsin.' : 'Yalnız senin için; Kaydet\'e basmadan hiçbir şey saklanmaz.'}
        </span>
      </div>
    </section>
  );
}
