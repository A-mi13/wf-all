import type { IconName } from '@wf/icons';
import { approvalsCommands } from '../features/approvals/commands';
import { auditCommands } from '../features/audit/commands';
import { geoCommands } from '../features/geo/commands';
import { meCommands } from '../features/me/commands';
import { staffCommands } from '../features/staff/commands';
import type { Locale } from '../i18n/messages';
import type { Density } from '../ui/table/prefs';
import type { ThemePref } from '../ui/theme/context';
import { navFor } from './nav';
import { type AppPath, paths } from './paths';
import type { Me } from './permissions';

// Реестр команд палитры (спека UI-фундамента §4.1, план UF4.3). Команды объявляют модули рядом со своими экранами
// (features/<раздел>/commands.ts), здесь они собираются вместе с общими. features/*/commands.ts — в основном куске
// сборки, поэтому не импортируют экраны (только пути, типы, ключи локалей, хуки прав; страж commands.test.ts, m17).
// - section — раздел: path — его адрес (палитра и клавиши — только разделы из navFor(me), sectionsFor), shortcut —
//   клавиши §4.2 (g x регистрирует CommandShortcuts в каркасе);
// - action — действие: permission — право самого действия (нет — не показывается, dual — с пометкой «на
//   подтверждение»), desktopOnly — на карманном устройстве не показывается (как ActionGate). Действие, которому нужен
//   экран, — переход с ключом панели в адресе: работает и из адреса, и после F5 (UF2b.8). fineOnly — только с мышью
//   (плотность таблиц: на устройстве с пальцем useDensity всегда «обычная», видимого результата не было бы).
// Решает всегда сервер: фильтр палитры — удобство, не защита.

export type NavigateTo = { to: string; search?: Record<string, unknown> };

export type CommandContext = {
  navigate: (to: NavigateTo) => void;
  setTheme: (theme: ThemePref) => void;
  setLocale: (locale: Locale) => void;
  setDensity: (density: Density) => void;
  openShortcuts: () => void;
  logout: () => void;
};

export type Command = {
  id: string;
  group: 'section' | 'action';
  labelKey: string;
  keywordsKey?: string; // английские и другие синонимы: «city» находит «Города» при русском интерфейсе
  icon?: IconName;
  permission?: string | null; // null и нет — любому активному
  desktopOnly?: boolean;
  fineOnly?: boolean; // на устройстве с пальцем (useIsCoarse) не показывается
  shortcut?: string; // 'g c' — последовательность клавиш §4.2
  path?: AppPath; // у раздела — его адрес
  run: (ctx: CommandContext) => void;
};

// Общие: «Главная» (подпись — пункта меню nav.home), тема, язык, плотность таблиц, горячие клавиши, выход.
const COMMON: Command[] = [
  {
    id: 'section.home',
    group: 'section',
    labelKey: 'nav.home',
    keywordsKey: 'palette.keywords.home',
    icon: 'home',
    permission: null,
    shortcut: 'g h',
    path: paths.root,
    run: (ctx) => ctx.navigate({ to: paths.root }),
  },
  {
    id: 'theme.system',
    group: 'action',
    labelKey: 'palette.commands.theme_system',
    keywordsKey: 'palette.keywords.theme',
    icon: 'monitor',
    run: (ctx) => ctx.setTheme('system'),
  },
  {
    id: 'theme.light',
    group: 'action',
    labelKey: 'palette.commands.theme_light',
    keywordsKey: 'palette.keywords.theme',
    icon: 'sun',
    run: (ctx) => ctx.setTheme('light'),
  },
  {
    id: 'theme.dark',
    group: 'action',
    labelKey: 'palette.commands.theme_dark',
    keywordsKey: 'palette.keywords.theme',
    icon: 'moon',
    run: (ctx) => ctx.setTheme('dark'),
  },
  {
    id: 'locale.ru',
    group: 'action',
    labelKey: 'palette.commands.locale_ru',
    keywordsKey: 'palette.keywords.locale',
    icon: 'globe',
    run: (ctx) => ctx.setLocale('ru'),
  },
  {
    id: 'locale.en',
    group: 'action',
    labelKey: 'palette.commands.locale_en',
    keywordsKey: 'palette.keywords.locale',
    icon: 'globe',
    run: (ctx) => ctx.setLocale('en'),
  },
  {
    id: 'density.compact',
    group: 'action',
    labelKey: 'palette.commands.density_compact',
    keywordsKey: 'palette.keywords.density',
    icon: 'columns',
    fineOnly: true,
    run: (ctx) => ctx.setDensity('compact'),
  },
  {
    id: 'density.normal',
    group: 'action',
    labelKey: 'palette.commands.density_normal',
    keywordsKey: 'palette.keywords.density',
    icon: 'columns',
    fineOnly: true,
    run: (ctx) => ctx.setDensity('normal'),
  },
  {
    id: 'shortcuts',
    group: 'action',
    labelKey: 'palette.commands.shortcuts',
    keywordsKey: 'palette.keywords.shortcuts',
    icon: 'keyboard',
    shortcut: '?',
    run: (ctx) => ctx.openShortcuts(),
  },
  {
    id: 'logout',
    group: 'action',
    labelKey: 'nav.logout',
    keywordsKey: 'palette.keywords.logout',
    icon: 'logout',
    run: (ctx) => ctx.logout(),
  },
];

// Порядок — порядок групп палитры при пустом запросе: разделы как в меню, затем действия.
export const COMMANDS: readonly Command[] = [
  ...COMMON.filter((c) => c.group === 'section'),
  ...auditCommands,
  ...staffCommands,
  ...approvalsCommands,
  ...meCommands,
  ...geoCommands,
  ...COMMON.filter((c) => c.group === 'action'),
];

// Разделы сотрудника — команды разделов, чьи пути есть в его меню (navFor: право как у пункта NAV). Один фильтр для
// палитры и клавиш g x.
export function sectionsFor(me: Me): Command[] {
  const menu = new Set<string>(navFor(me).flatMap((g) => g.items.map((i) => i.path)));
  return COMMANDS.filter((c) => c.group === 'section' && c.path !== undefined && menu.has(c.path));
}
