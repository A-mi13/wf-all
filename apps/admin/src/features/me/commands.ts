import type { Command } from '../../app/commands';
import { paths } from '../../app/paths';

// Команды палитры «Моего доступа» (спека UI-фундамента §4.1): только пути и ключи локалей — экран не импортировать
// (страж src/app/commands.test.ts).
export const meCommands: Command[] = [
  {
    id: 'section.me',
    group: 'section',
    labelKey: 'nav.me',
    keywordsKey: 'palette.keywords.me',
    icon: 'user',
    permission: null,
    shortcut: 'g m',
    path: paths.me,
    run: (ctx) => ctx.navigate({ to: paths.me }),
  },
];
