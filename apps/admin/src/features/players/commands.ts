import type { Command } from '../../app/commands';
import { paths } from '../../app/paths';

// Команды палитры «Игроков» (план I-4, Task 9): только пути и ключи локалей — экран не импортировать (страж
// src/app/commands.test.ts). Право — как у пункта меню. Почта поиска в палитру и «Недавнее» не попадает (R19).
export const playersCommands: Command[] = [
  {
    id: 'section.players',
    group: 'section',
    labelKey: 'nav.players',
    keywordsKey: 'palette.keywords.players',
    icon: 'team',
    permission: 'identity.pii.read',
    shortcut: 'g u',
    path: paths.players,
    run: (ctx) => ctx.navigate({ to: paths.players }),
  },
];
