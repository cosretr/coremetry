// QueryBlock — adım kartının sorgu metni (evidence.query; tools-audit Q6):
// varsayılan KAPALI, salt okunur, kopyalanabilir.
import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { copyToClipboard } from '@/lib/clipboard';
import { EVIDENCE_STRINGS as T } from './strings';
import { queryKindLabel } from './stepView';

export function QueryBlock({ query, queryKind, source }: { query: string; queryKind?: string; source?: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    if (await copyToClipboard(query)) {
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    }
  };
  return (
    <Collapsible data-cosre-query="">
      <CollapsibleTrigger asChild>
        <Button variant="link" size="xs" className="h-auto px-0 text-muted-foreground">{T.queryShow(queryKindLabel(queryKind))}</Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className="mt-1 overflow-x-auto rounded-md border border-border bg-muted p-2 font-mono text-xs leading-relaxed whitespace-pre">{query}</pre>
        <div className="mt-1 flex items-center justify-between gap-2 text-xs text-muted-foreground">
          <span>{T.queryReadOnly}{source ? ` · ${T.querySource(source)}` : ''}</span>
          <Button variant="outline" size="xs" onClick={() => void copy()}>{copied ? T.copied : T.copy}</Button>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
