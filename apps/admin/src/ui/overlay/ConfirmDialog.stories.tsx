import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { ApiError } from '../../api/client';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { ConfirmDialog } from './ConfirmDialog';

// Подтверждение разрушающего действия вводом почты цели: последствия, заметка о журнале; занятость и
// ошибка запроса внутри окна. На узком экране — шторка (layout: три ширины).
export default {
  title: 'UI/Overlay/ConfirmDialog',
  visual: true,
  layout: true,
} satisfies StoryMeta;

// почта и имя цели — данные (как в фикстурах тестов)
const EMAIL = 'maria.k@wf.example';
const NAME = 'Мария К.';

function Sample({ busy, error }: { busy?: boolean; error?: ApiError }) {
  const t = useTranslations('staff.drawer');
  const [open, setOpen] = useState(true);
  return (
    <>
      <Button variant="danger-outline" onClick={() => setOpen(true)}>
        {t('reset.button')}
      </Button>
      <ConfirmDialog
        open={open}
        onClose={() => setOpen(false)}
        onConfirm={() => undefined}
        title={t('reset.confirm_title', { name: NAME })}
        text={t('reset.text')}
        consequences={[
          t('reset.consequences.secrets'),
          t('reset.consequences.sessions'),
          t('reset.consequences.invite'),
        ]}
        confirmWord={EMAIL}
        confirmLabel={t('reset.confirm')}
        note={t('journal')}
        loading={busy}
        error={error}
      />
    </>
  );
}

export const Default: Story = () => <Sample />;
export const Busy: Story = () => <Sample busy />;
export const Error: Story = () => (
  <Sample error={new ApiError({ status: 409, code: 'staff.last_owner' })} />
);
