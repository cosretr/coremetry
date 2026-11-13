import { useState } from 'react';
import { Users } from 'lucide-react';
import { api } from '@/lib/api';
import { Button } from '@/components/shadcn/button';
import type { Problem } from '@/lib/types';

// AssigneeCell — v0.5.209 triage column. Renders the current
// assignee (team name auto-set on open from service_metadata.
// ownerTeam, OR an operator email after manual claim), with two
// inline affordances:
//   • "Take it" — PATCH self-email when the problem is
//     unassigned or assigned to a team. One click, no modal.
//   • Click-to-edit — prompt() lets the operator type any
//     value (reassign to a teammate / change team / clear).
// Kept dependency-light: no inline picker component, no
// suggestions list. v2 can promote this to a typeahead against
// the users table if the prompt() ergonomics annoy operators.
// v0.10.2125 (Problems S5) — ProblemsSection.tsx'ten kendi modülüne taşındı:
// Problems listesinin İlgilenen hücresi aynı düğmeyi kullanır (alt "Alert
// rules" tablosu kalktı). Düğme ui/ yerine shadcn (ui/ donuk); davranış aynı.
export function AssigneeCell({ problem, currentUserEmail, onChanged }: {
  problem: Pick<Problem, 'id' | 'assignee'>;
  currentUserEmail: string;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const assignee = problem.assignee ?? '';
  const isSelf = currentUserEmail !== '' && assignee === currentUserEmail;
  const isTeam = assignee !== '' && !assignee.includes('@');

  const set = async (next: string) => {
    if (busy || next === assignee) return;
    setBusy(true);
    try { await api.setProblemAssignee(problem.id, next); onChanged(); }
    finally { setBusy(false); }
  };
  const editPrompt = () => {
    const v = window.prompt('Assignee (email or team name; empty = unassign):', assignee);
    if (v === null) return;
    void set(v.trim());
  };

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      {assignee
        ? (
          // v0.10.922 (sade palet adım 1) — atanan kişi üst veri: nötr.
          // "Bana atanmış" bilgisi yeşilde değil metnin kendisinde (kendi
          // e-postan) ve "Take it" düğmesinin yokluğunda.
          <span onClick={editPrompt}
            title="Click to reassign or clear"
            className="badge b-gray"
            style={{ cursor: 'pointer' }}>
            {isTeam && <Users size={11} strokeWidth={1.75} />}{assignee}
          </span>
        )
        : <span style={{ color: 'var(--text3)' }}>—</span>}
      {currentUserEmail !== '' && !isSelf && (
        <Button variant="secondary" size="xs" disabled={busy}
          onClick={() => void set(currentUserEmail)}
          title="Claim this problem for yourself">
          Take it
        </Button>
      )}
    </span>
  );
}
