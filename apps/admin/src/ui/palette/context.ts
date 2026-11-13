import { createContext, useContext } from 'react';

// Открытие палитры (спека UI-фундамента §4.1): кнопка «Искать везде» каркаса, пункт шторки «Ещё», Ctrl/⌘+K (волна 2,
// useShortcut). Провайдер — PaletteProvider (src/app/PaletteProvider.tsx) в каркасе активной стадии; вне его (экраны
// входа, витрина) — null, кнопки поиска нет. open() поверх модального оверлея ничего не делает (m15).
export type PaletteApi = { open: () => void };

export const PaletteContext = createContext<PaletteApi | null>(null);

export function usePalette(): PaletteApi | null {
  return useContext(PaletteContext);
}
