import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import stories from '../showcase/Stories.module.css';
import { CopyButton } from './CopyButton';

// Копирование в один клик (спека UI-фундамента §4.8): значок рядом со значением и кнопка с подписью.
// Успех и отказ — нажатием или пропом preview (истории Copied, Manual copy fallback).
export default { title: 'UI/CopyButton', visual: true } satisfies StoryMeta;

export const Icon: Story = () => {
  const t = useTranslations('staff.drawer');
  return (
    <span className={stories.row}>
      <span>maria.k@wf.example</span>
      <CopyButton value="maria.k@wf.example" label={t('email')} />
    </span>
  );
};
Icon.storyName = 'Icon';

export const WithText: Story = () => {
  const t = useTranslations('staff.drawer');
  return (
    <CopyButton value="0192f1c4-1111-7b21-9c5d-000000000001" label={t('email')} display="text" />
  );
};
WithText.storyName = 'With text';

// Состояния без нажатия (preview): «галочка» после копирования и отказ — поле только для чтения с
// выделенным значением (тост отказа появляется только по нажатию).
export const Copied: Story = () => {
  const t = useTranslations('staff.drawer');
  return <CopyButton value="maria.k@wf.example" label={t('email')} preview="copied" />;
};
Copied.storyName = 'Copied';

export const Manual: Story = () => {
  const t = useTranslations('staff.drawer');
  return <CopyButton value="maria.k@wf.example" label={t('email')} preview="manual" />;
};
Manual.storyName = 'Manual copy fallback';
