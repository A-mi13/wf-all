import type { Command } from '../../app/commands';
import { paths } from '../../app/paths';

// Команды палитры «Капитанов» (план I-4, Task 8): только пути и ключи локалей — экран не импортировать (страж
// src/app/commands.test.ts). Право — как у пункта меню.
export const captainsCommands: Command[] = [
  {
    id: 'section.captains',
    group: 'section',
    labelKey: 'nav.captains',
    keywordsKey: 'palette.keywords.captains',
    icon: 'captain',
    permission: 'identity.captain_applications.read',
    shortcut: 'g k',
    path: paths.captains,
    run: (ctx) => ctx.navigate({ to: paths.captains }),
  },
];
