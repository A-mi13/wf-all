import type { Command } from '../../app/commands';
import { paths } from '../../app/paths';

// Команды палитры «Ролей и доступа» (спека UI-фундамента §4.1): только пути и ключи локалей — экран не импортировать
// (страж src/app/commands.test.ts). «Пригласить» — переход на ?invite=1 (панель в адресе, UF2b.8): право выдачи (у
// владельца — dual, палитра пометит «на подтверждение»), выдача ролей — только с компьютера (desktopOnly.ts).
export const staffCommands: Command[] = [
  {
    id: 'section.staff',
    group: 'section',
    labelKey: 'nav.staff',
    keywordsKey: 'palette.keywords.staff',
    icon: 'key',
    permission: 'staff.roles.read',
    shortcut: 'g r',
    path: paths.staff,
    run: (ctx) => ctx.navigate({ to: paths.staff }),
  },
  {
    id: 'staff.invite',
    group: 'action',
    labelKey: 'staff.invite.open',
    keywordsKey: 'palette.keywords.invite',
    icon: 'invite',
    permission: 'staff.roles.grant',
    desktopOnly: true,
    run: (ctx) => ctx.navigate({ to: paths.staff, search: { invite: 1 } }),
  },
];
