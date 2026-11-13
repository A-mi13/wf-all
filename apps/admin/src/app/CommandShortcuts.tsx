import { useMemo } from 'react';
import { useTranslations } from 'use-intl';
import { useShortcut } from '../ui/shortcuts';
import { findPageSearch } from '../ui/table/model';
import { type Command, type CommandContext, sectionsFor } from './commands';
import type { Me } from './permissions';

// Клавиши каркаса (спека UI-фундамента §4.2, UF4 волна 2), под PaletteProvider:
// - g x — из shortcut команд разделов реестра (features/*/commands.ts) и только для разделов меню сотрудника
//   (sectionsFor — право как у пункта NAV); permission — в регистрации («только с компьютера» на телефоне молчит);
//   переход — run(context): роутер, значит, и вопрос useLeaveGuard при несохранённом;
// - «/» — одна регистрация: поле поиска страницы (DataTable, findPageSearch), его нет — палитра. Таблица сама клавиш
//   не регистрирует: две регистрации «/» были бы конфликтом (ревью UF4-K).
// «?» (шпаргалка) регистрирует ShortcutsProvider — она есть и на экранах входа; Ctrl/⌘+K — PaletteProvider.
export function CommandShortcuts({
  me,
  context,
  openPalette,
}: {
  me: Me;
  context: CommandContext;
  openPalette: () => void;
}) {
  const t = useTranslations();
  const sections = useMemo(() => sectionsFor(me), [me]);
  useShortcut(
    '/',
    () => {
      const field = findPageSearch();
      if (field) field.focus();
      else openPalette();
    },
    { label: t('ui.shortcuts.search') },
  );
  return sections.map((c) =>
    c.shortcut ? (
      <ShortcutBinding key={c.id} command={c} combo={c.shortcut} context={context} />
    ) : null,
  );
}

// Одна регистрация на команду: компонент в цикле, а не хук в цикле (число команд зависит от прав).
function ShortcutBinding({
  command,
  combo,
  context,
}: {
  command: Command;
  combo: string;
  context: CommandContext;
}) {
  const t = useTranslations();
  useShortcut(combo, () => command.run(context), {
    label: t(command.labelKey),
    permission: command.permission ?? undefined,
  });
  return null;
}
