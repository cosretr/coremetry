// v0.10.1810 — takım önceliği: oturum seçimi > sunucu users.team > saklı
// tarayıcı seçimi (yalnız sunucu boş + settled). Regresyon: eski sırada
// bayat localStorage değeri doğru sunucu takımını her istekte eziyordu.
import { describe, expect, it } from 'vitest';
import { resolveTeamScope } from './useCosreTeamScope';

describe('resolveTeamScope', () => {
  const cases: [string, Parameters<typeof resolveTeamScope>[0], ReturnType<typeof resolveTeamScope>][] = [
    ['server team beats a stale stored pick',
      { sessionPick: null, stored: 'old-team', serverTeam: 'platform', settled: true },
      { team: 'platform', picked: '', unknown: false }],
    ['stored pick used only when the server has no team',
      { sessionPick: null, stored: 'old-team', serverTeam: '', settled: true },
      { team: 'old-team', picked: 'old-team', unknown: false }],
    ['stored pick not used before the server answered',
      { sessionPick: null, stored: 'old-team', serverTeam: '', settled: false },
      { team: '', picked: '', unknown: false }],
    ['session pick beats the server team',
      { sessionPick: 'payments', stored: null, serverTeam: 'platform', settled: true },
      { team: 'payments', picked: 'payments', unknown: false }],
    ['session clear wins over server and stored',
      { sessionPick: '', stored: 'old-team', serverTeam: 'platform', settled: true },
      { team: '', picked: '', unknown: false }],
    ['nothing anywhere after settle = unknown',
      { sessionPick: null, stored: null, serverTeam: ' ', settled: true },
      { team: '', picked: '', unknown: true }],
    ['loading is not unknown',
      { sessionPick: null, stored: null, serverTeam: '', settled: false },
      { team: '', picked: '', unknown: false }],
  ];
  for (const [name, input, want] of cases) {
    it(name, () => expect(resolveTeamScope(input)).toEqual(want));
  }
});
