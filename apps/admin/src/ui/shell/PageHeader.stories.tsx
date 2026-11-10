import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { StatusChip } from '../StatusChip';
import { PageHeader } from './PageHeader';

// Шапка страницы: заголовок (h1), подзаголовок, действия справа; длинный текст переносится.
export default { title: 'UI/Shell/PageHeader', visual: true, layout: true } satisfies StoryMeta;

export const Default: Story = () => {
  const t = useTranslations();
  return (
    <PageHeader
      title={t('nav.staff')}
      subtitle={t('staff.subtitle')}
      actions={<Button>{t('staff.invite.open')}</Button>}
    />
  );
};

export const LongText: Story = () => {
  const t = useTranslations();
  return (
    <PageHeader
      title={t('pages.setup2fa.title')}
      subtitle={t('staff.invite.text')}
      actions={
        <>
          <StatusChip tone="success">{t('me.mfa.on')}</StatusChip>
          <Button variant="secondary">{t('me.mfa.regenerate')}</Button>
        </>
      }
    />
  );
};
