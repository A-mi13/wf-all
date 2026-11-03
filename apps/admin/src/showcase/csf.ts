import type { ReactNode } from 'react';

// Истории компонентов — CSF на своих типах (план UI-фундамента, U12, m16): не зависят от выбора
// витрины (Ladle или своя, UF3.1), формат совместим с Ladle. Форма заморожена UF1a.3: UF3 её не меняет,
// пока UF2a/UF2b пишут истории своих компонентов.
// - StoryMeta — default-экспорт файла *.stories.tsx: title — раздел витрины; visual — история входит в
//   визуальное сравнение в CI (§5.3); layout — снимается на трёх ширинах.
// - Story<P> — именованный экспорт: компонент истории; storyName — подпись, meta — метки этой истории.
export type StoryMeta = { title: string; visual?: boolean; layout?: boolean };

export type Story<P = Record<string, never>> = {
  (args: P): ReactNode;
  storyName?: string;
  meta?: StoryMeta;
};
