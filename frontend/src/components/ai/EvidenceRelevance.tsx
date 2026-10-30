// EvidenceRelevance — v0.10.1659 (CoSRE v2 Faz 2b B4, plan §3). 👍/👎 yerine
// (aiVoteRemoved ratchet) iki öğrenme sinyali (ui/ dondurulmuş → shadcn button +
// coremetry Field, uiImportRatchet):
//   • RelevanceToggle — araç kanıtı başına "İlgili / İlgisiz": iki durumlu,
//     optimistik, yerel; her geçiş POST /api/ai/signal {exchangeId, kind:
//     evidence_relevant|evidence_irrelevant, detail: "s<i>"}. Aynı duruma ikinci
//     tık bir şey göndermez; ters yöne tık ters sinyali gönderir (ağırlık
//     cosre_signal_weights: +0.10 / −0.10).
//   • MessageNote — mesaj düzeyinde serbest not (kind: note, detail ≤ 512 bayt
//     UTF-8, lib/cosre/signalDetail).
// exchangeId yokken (akış bitmeden / arşiv turu) düğmeler devre dışı.
import { useState } from 'react';
import { api } from '@/lib/api';
import { noteDetail } from '@/lib/cosre/signalDetail';
import { Button } from '@/components/shadcn/button';
import { TextareaField } from '@/components/coremetry/Field';

export type Relevance = 'relevant' | 'irrelevant';

export function RelevanceToggle({ exchangeId, stepId }: { exchangeId?: string; stepId: string }) {
  const [state, setState] = useState<Relevance | null>(null);
  const pick = (v: Relevance) => {
    if (!exchangeId || state === v) return;
    setState(v);
    api.aiSignal(exchangeId, v === 'relevant' ? 'evidence_relevant' : 'evidence_irrelevant', stepId);
  };
  const off = !exchangeId;
  const tip = off ? 'Cevap tamamlanınca işaretlenebilir' : undefined;
  return (
    <div className="cm-ev-rel" role="group" aria-label="Bu kanıt cevapla ilgili mi?" data-cosre-relevance={stepId}>
      <Button variant={state === 'relevant' ? 'secondary' : 'ghost'} size="xs" disabled={off} title={tip}
        aria-pressed={state === 'relevant'} onClick={() => pick('relevant')}>✓ İlgili</Button>
      <Button variant={state === 'irrelevant' ? 'secondary' : 'ghost'} size="xs" disabled={off} title={tip}
        aria-pressed={state === 'irrelevant'} onClick={() => pick('irrelevant')}>✕ İlgisiz</Button>
    </div>
  );
}

export function MessageNote({ exchangeId }: { exchangeId?: string }) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState('');
  const [sent, setSent] = useState(false);
  if (sent) return <span className="cell-hint" data-cosre-note-sent="">✓ Not kaydedildi</span>;
  if (!open) {
    return (
      <Button variant="ghost" size="xs" disabled={!exchangeId} onClick={() => setOpen(true)}
        title="Bu cevap hakkında not bırak (CoSRE öğrenmesine gider)" aria-label="Cevaba not ekle">✎ Not</Button>
    );
  }
  const detail = noteDetail(text);
  const submit = () => {
    if (!exchangeId || !detail) return;
    api.aiSignal(exchangeId, 'note', detail);
    setSent(true);
  };
  return (
    <form className="cm-msg-note-form" data-cosre-note="" onSubmit={e => { e.preventDefault(); submit(); }}>
      <TextareaField label="Not" rows={2} value={text} autoFocus maxLength={2000}
        hint="En çok 512 bayt gönderilir." onChange={e => setText(e.target.value)} />
      <div className="cm-msg-note-form__row">
        <Button variant="secondary" size="xs" type="button" onClick={() => { setOpen(false); setText(''); }}>Vazgeç</Button>
        <Button variant="default" size="xs" type="submit" disabled={!detail}>Kaydet</Button>
      </div>
    </form>
  );
}
