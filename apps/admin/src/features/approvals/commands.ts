import type { Command } from '../../app/commands';
import { paths } from '../../app/paths';

// Команды палитры «Подтверждений» (спека UI-фундамента §4.1): только пути и ключи локалей — экран не импортировать
// (страж src/app/commands.test.ts).
export const approvalsCommands: Command[] = [
  {
    id: 'section.approvals',
    group: 'section',
    labelKey: 'nav.approvals',
    keywordsKey: 'palette.keywords.approvals',
    icon: 'shield',
    permission: null,
    shortcut: 'g p',
    path: paths.approvals,
    run: (ctx) => ctx.navigate({ to: paths.approvals }),
  },
];
