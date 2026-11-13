import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { Drawer } from './Drawer';
import { LeaveDialog } from './Overlay';

// Вопрос при уходе с несохранённого (§4.11): «Остаться» — фокус по умолчанию; на узком экране — шторка.
export default { title: 'UI/Overlay/LeaveDialog', visual: true, layout: true } satisfies StoryMeta;

export const Default: Story = () => {
  const [open, setOpen] = useState(true);
  return <LeaveDialog open={open} onStay={() => setOpen(false)} onLeave={() => setOpen(false)} />;
};

// Панель с введённым: Esc, фон и «Закрыть» спрашивают (Overlay.dirty)
export const DirtyDrawer: Story = () => {
  const t = useTranslations();
  const [open, setOpen] = useState(true);
  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        {t('staff.list.manage')}
      </Button>
      <Drawer
        open={open}
        dirty
        onClose={() => setOpen(false)}
        title={t('staff.drawer.title')}
        footer={<Button>{t('actions.save')}</Button>}
      >
        <p>{t('staff.drawer.journal')}</p>
      </Drawer>
    </>
  );
};
DirtyDrawer.meta = { title: 'UI/Overlay/LeaveDialog', visual: false };
