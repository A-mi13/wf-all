import type { IconName } from '@wf/icons';
import { useTranslations } from 'use-intl';
import { Segmented } from '../Segmented';
import { type ThemePref, useTheme } from './context';

const OPTIONS: { value: ThemePref; icon: IconName }[] = [
  { value: 'system', icon: 'monitor' },
  { value: 'light', icon: 'sun' },
  { value: 'dark', icon: 'moon' },
];

// Переключатель темы «Система · Светлая · Тёмная» (спека §4.12): Segmented из трёх значков (дорисованные
// monitor, sun, moon из @wf/icons); имя — для читалки, title — для мыши. Стоит в карточке сотрудника, в
// шторке «Ещё» и на экранах входа. Смена — мгновенно, без анимации.
export function ThemeSwitch({ className }: { className?: string }) {
  const t = useTranslations('ui.theme');
  const { theme, setTheme } = useTheme();
  return (
    <Segmented
      label={t('label')}
      value={theme}
      onChange={setTheme}
      className={className}
      options={OPTIONS.map((o) => ({
        value: o.value,
        icon: o.icon,
        name: t(o.value),
        title: t(o.value),
      }))}
    />
  );
}
