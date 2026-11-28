// flowView — v0.10.2491 (CoSRE sohbet dilim 1): inline akışın SAF görünüm
// modeli. Sunucu `cosre_conversation.flow=inline` iken her araç grubunun başına
// kalıcı bir `data-narration` parçası yazar (api chat_narration.go); grup =
// o parçadan sonraki, bir sonraki anlatıma kadar gelen araç / sözleşme adımları
// (lib/cosre/flowParts.ts). Mesajda hiç data-narration yoksa (block kipi, eski
// sunucu, eski arşiv) null: çizici bugünkü StepList'e düşer (görünüm aynen).
import { slotsOf } from '@/lib/cosre/flowParts';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import type { StepView, StepsView } from './stepView';

type Part = CosreUIMessage['parts'][number];

export interface FlowGroup {
  /** Anlatım parçasının id'si (n<k>); anlatımsız baş grup 'g0'. */
  id: string;
  text?: string;
  steps: StepView[];
  /** Grupta çalışan adım var mı. */
  running: boolean;
}

export interface FlowView {
  /** Ekran bağlamı cümlesi (ilk anlatımın `context`i). */
  context?: string;
  groups: FlowGroup[];
}

/** SAF: inline görünüm; anlatım yoksa null (block görünümüne düş). */
export function flowView(parts: readonly Part[], view: StepsView): FlowView | null {
  const s = slotsOf(parts);
  if (!s) return null;
  const byId = new Map(view.steps.map(st => [st.id, st] as const));
  const used = new Set<string>();
  const groups: FlowGroup[] = [];
  for (const sl of s.slots) {
    const steps: StepView[] = [];
    for (const id of sl.ids) {
      const st = byId.get(id);
      if (!st || used.has(id)) continue;
      used.add(id);
      const purpose = sl.purposes.get(id);
      steps.push(purpose ? { ...st, purpose } : st);
    }
    if (!sl.text && steps.length === 0) continue;
    groups.push({ id: sl.id, ...(sl.text ? { text: sl.text } : {}), steps, running: steps.some(x => x.kind === 'running') });
  }
  // Sıraya oturmayan adım (beklenmez) son gruba: hiçbir adım kaybolmaz.
  const rest = view.steps.filter(st => !used.has(st.id));
  if (rest.length) {
    if (groups.length === 0) groups.push({ id: 'g0', steps: [], running: false });
    const last = groups[groups.length - 1];
    last.steps.push(...rest);
    last.running = last.steps.some(x => x.kind === 'running');
  }
  return { ...(s.context ? { context: s.context } : {}), groups };
}

