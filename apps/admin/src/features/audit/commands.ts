import type { Command } from '../../app/commands';
import { paths } from '../../app/paths';

// Команды палитры «Журнала действий» (спека UI-фундамента §4.1): только пути и ключи локалей — экран не импортировать
// (страж src/app/commands.test.ts).
export const auditCommands: Command[] = [
  {
    id: 'section.audit',
    group: 'section',
    labelKey: 'nav.audit',
    keywordsKey: 'palette.keywords.audit',
    icon: 'log',
    permission: 'audit.log.read',
    shortcut: 'g a',
    path: paths.audit,
    run: (ctx) => ctx.navigate({ to: paths.audit }),
  },
];
