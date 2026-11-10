import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import { Segmented } from './Segmented';

// Сегментный переключатель: текстовые сегменты («RU · EN»), значки, занятость.
export default { title: 'UI/Segmented', visual: true } satisfies StoryMeta;

function Locales({ busy }: { busy?: boolean }) {
  const t = useTranslations('locale');
  const [value, setValue] = useState<'ru' | 'en'>('ru');
  return (
    <Segmented
      label={t('label')}
      value={value}
      onChange={setValue}
      busy={busy}
      options={(['ru', 'en'] as const).map((code) => ({
        value: code,
        text: t(`short.${code}`),
        name: t('option', { code: t(`short.${code}`), name: t(code) }),
        lang: code,
      }))}
    />
  );
}

export const Default: Story = () => <Locales />;
export const Busy: Story = () => <Locales busy />;
