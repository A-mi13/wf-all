import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { StatusChip } from '../StatusChip';
import { Drawer } from './Drawer';

// Панель справа (карточка сотрудника, приглашение): содержимое прокручивается, действия — в подвале.
export default { title: 'UI/Overlay/Drawer', visual: true, layout: true } satisfies StoryMeta;

function Sample({ busy }: { busy?: boolean }) {
  const t = useTranslations();
  const [open, setOpen] = useState(true);
  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        {t('staff.list.manage')}
      </Button>
      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        dismissible={!busy}
        title={t('staff.drawer.title')}
        description={t('staff.drawer.disable.text')}
        footer={
          <>
            <Button variant="secondary" disabled={busy} onClick={() => setOpen(false)}>
              {t('ui.confirm.cancel')}
            </Button>
            <Button variant="danger" loading={busy}>
              {t('staff.drawer.disable.button')}
            </Button>
          </>
        }
      >
        <StatusChip tone="success">{t('staff.status.active')}</StatusChip>
        <p>{t('staff.drawer.journal')}</p>
      </Drawer>
    </>
  );
}

export const Default: Story = () => <Sample />;
export const Busy: Story = () => <Sample busy />;
