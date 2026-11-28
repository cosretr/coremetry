// HelpTip — v0.10.2487 (entity tutarlılık denetimi dilim 3, §1.5): veri
// VARKEN bölümün açıklaması kalıcı paragraf değil, başlığın yanındaki (?)
// yardım balonu. Metin düğmenin erişilebilir adıdır (ekran okuyucu ve test
// aynı metni okur); balon üzerine gelince / odakta açılır. Veri YOKKEN
// açıklama Empty gövdesine gider (components/Spinner Empty).
import { CircleHelp } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/coremetry/overlay';

export function HelpTip({ text, testId }: { text: string; testId?: string }) {
  if (!text) return null;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-xs" aria-label={text} data-testid={testId} className="text-muted-foreground">
          <CircleHelp size={13} strokeWidth={1.75} aria-hidden="true" />
        </Button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{text}</TooltipContent>
    </Tooltip>
  );
}
