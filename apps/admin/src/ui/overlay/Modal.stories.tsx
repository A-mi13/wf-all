import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Modal } from './Modal';

// Модалка по центру: обычная и опасная (рамка и заголовок danger), действия — в подвале.
export default { title: 'UI/Overlay/Modal', visual: true } satisfies StoryMeta;

function Sample({ tone }: { tone?: 'danger' }) {
  const t = useTranslations();
  const [open, setOpen] = useState(true);
  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        {t('staff.invite.open')}
      </Button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        tone={tone}
        title={t('staff.invite.title')}
        description={t('staff.invite.text')}
        footer={
          <>
            <Button variant="secondary" onClick={() => setOpen(false)}>
              {t('actions.cancel')}
            </Button>
            <Button variant={tone === 'danger' ? 'danger' : 'primary'}>
              {t('actions.submitForApproval')}
            </Button>
          </>
        }
      />
    </>
  );
}

export const Default: Story = () => <Sample />;
export const Danger: Story = () => <Sample tone="danger" />;
