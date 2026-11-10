import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { Sheet } from './Sheet';

// Шторка снизу (телефон: «Ещё», подтверждения): заголовок, текст, действия в подвале.
export default { title: 'UI/Overlay/Sheet', visual: true, layout: true } satisfies StoryMeta;

export const Default: Story = () => {
  const t = useTranslations();
  const [open, setOpen] = useState(true);
  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        {t('nav.more')}
      </Button>
      <Sheet
        open={open}
        onClose={() => setOpen(false)}
        title={t('approvals.reject_dialog.title')}
        description={t('approvals.reject_dialog.text')}
        footer={
          <>
            <Button variant="secondary" onClick={() => setOpen(false)}>
              {t('ui.confirm.cancel')}
            </Button>
            <Button variant="danger">{t('approvals.reject_dialog.submit')}</Button>
          </>
        }
      />
    </>
  );
};
