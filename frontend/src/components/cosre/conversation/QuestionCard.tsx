// QuestionCard — v0.10.2102 (docs/cosre/experience-audit.md D2, operatör
// onayı 2026-11-13 soru 2 ve 5). Netleştirme sorusunun cevap düğmeleri:
// soru cümlesi balonda düz metin olarak zaten var (v1 ve kart çizemeyen
// istemci aynısını görür), kart onu tekrar yazmaz; yalnız ≤3 düğme. Serbest
// metin cevabı aynen geçerli. Cevaplanmış (eski) kart pasif ve görünür,
// seçilen dolu düğme + işaret.
import { Check } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import type { CosreQuestion } from '@/lib/types';
import { COSRE_V2_STRINGS as T } from './strings';

export interface QuestionCardProps {
  question: CosreQuestion;
  /** Etkin kartın tıkı; yoksa kart pasif. */
  onAnswer?: (option: CosreQuestion['options'][number]) => void;
  /** Pasif kartta seçilen seçeneğin kimliği. */
  chosen?: string;
}

export function QuestionCard({ question, onAnswer, chosen }: QuestionCardProps) {
  const disabled = !onAnswer;
  return (
    <div className="cm-msg cm-msg--ai" data-cosre-question={question.kind}>
      <div className="flex flex-wrap gap-2" role="group" aria-label={question.text}>
        {question.options.map(o => {
          const picked = chosen === o.id;
          return (
            <Button key={o.id} size="sm" variant="secondary" disabled={disabled}
              aria-pressed={disabled ? picked : undefined} data-question-option={o.id}
              onClick={onAnswer ? () => onAnswer(o) : undefined}>
              {picked && <Check aria-hidden="true" />}
              {o.label}
              {picked && <span className="sr-only">{` (${T.questionChosen})`}</span>}
            </Button>
          );
        })}
      </div>
    </div>
  );
}
