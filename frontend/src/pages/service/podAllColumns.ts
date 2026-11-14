import { createContext } from 'react';

// v0.10.2145 (servis sekmeleri S6) — "Tüm kolonlar": üst tablonun (Nerede
// çalışıyor) düğmesi, satırın altında açılan pod tablosunu da genişletir.
export const PodAllColumnsContext = createContext(false);
