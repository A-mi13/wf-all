import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Tabs } from './Tabs';

// Вкладки: «Входящие 3 · Мои» (счётчик в имени), содержимое выбранной — панель.
export default { title: 'UI/Shell/Tabs', visual: true } satisfies StoryMeta;

export const Default: Story = () => {
  const t = useTranslations('approvals');
  const [value, setValue] = useState('incoming');
  return (
    <Tabs
      label={t('tabs.label')}
      value={value}
      onChange={setValue}
      items={[
        { value: 'incoming', label: t('tabs.incoming'), count: 3 },
        { value: 'mine', label: t('tabs.mine') },
      ]}
    >
      <p>{value === 'incoming' ? t('subtitle') : t('empty.mine_title')}</p>
    </Tabs>
  );
};
