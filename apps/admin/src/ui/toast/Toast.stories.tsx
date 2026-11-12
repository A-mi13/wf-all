import { useEffect, useRef } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { ConfirmDialog } from '../overlay/ConfirmDialog';
import { Drawer } from '../overlay/Drawer';
import { LeaveDialog } from '../overlay/Overlay';
import { useToast } from './context';

// Тосты: успех с действием (исчезает через 8 с — в снимок не входит), справка, ошибка (только руками).
export default { title: 'UI/Toast', visual: true } satisfies StoryMeta;

// Тост — при монтировании истории, один раз (StrictMode монтирует эффект дважды, ref переживает это).
function useOnce(show: () => void) {
  const shown = useRef(false);
  useEffect(() => {
    if (shown.current) return;
    shown.current = true;
    show();
  });
}

export const Default: Story = () => {
  const t = useTranslations();
  const toast = useToast();
  useOnce(() => {
    toast.success(t('staff.drawer.disable.done'), {
      action: { label: t('actions.retry'), onClick: () => undefined },
    });
    toast.info(t('me.sessions.revoked'));
  });
  return null;
};
// успех и справка гаснут через TOAST_HIDE_MS — для сравнения снимков нестабильны
Default.meta = { title: 'UI/Toast', visual: false };

export const Error: Story = () => {
  const t = useTranslations();
  const toast = useToast();
  useOnce(() => {
    toast.error(t('errors.staff.last_owner'), {
      action: { label: t('actions.retry'), onClick: () => undefined },
    });
  });
  return null;
};

// Тост с «Отменить» (§4.10): сам не исчезает — стабилен для снимка; на телефоне — одна строка над таббаром.
export const Undo: Story = () => {
  const t = useTranslations();
  const toast = useToast();
  useOnce(() => {
    toast.success(t('staff.drawer.disable.done'), { undo: async () => undefined });
  });
  return null;
};

// Открыта панель с подвалом + тост с «Отменить»: тост над подвалом, не над кнопками (m23).
export const OverDrawerFooter: Story = () => {
  const t = useTranslations();
  const toast = useToast();
  useOnce(() => {
    toast.success(t('staff.drawer.disable.done'), { undo: async () => undefined });
  });
  return (
    <Drawer
      open
      onClose={() => undefined}
      title={t('staff.drawer.title')}
      footer={
        <>
          <Button variant="secondary">{t('actions.cancel')}</Button>
          <Button variant="danger">{t('staff.drawer.disable.button')}</Button>
        </>
      }
    >
      <p>{t('staff.drawer.journal')}</p>
    </Drawer>
  );
};
OverDrawerFooter.meta = { title: 'UI/Toast', layout: true };

// Телефон: поверх панели с подвалом — вопрос «Уйти без сохранения?» (без подвала, кнопки в теле) и тост с
// «Отменить»: тост у края окна, кнопки вопроса над ним (ревью UF2b/II-A, I1).
export const OverLeaveDialog: Story = () => {
  const t = useTranslations();
  const toast = useToast();
  useOnce(() => {
    toast.success(t('staff.drawer.disable.done'), { undo: async () => undefined });
  });
  return (
    <>
      <Drawer
        open
        dirty
        onClose={() => undefined}
        title={t('staff.drawer.title')}
        footer={<Button variant="danger">{t('staff.drawer.disable.button')}</Button>}
      >
        <p>{t('staff.drawer.journal')}</p>
      </Drawer>
      <LeaveDialog open onStay={() => undefined} onLeave={() => undefined} />
    </>
  );
};
OverLeaveDialog.meta = { title: 'UI/Toast', layout: true };

// Широкий экран, высокое подтверждение по центру (последствия, поля, слово — во всю высоту окна) и тост с
// «Отменить»: кнопки подтверждения над тостом, а не под ним (ревью UF2b/II-A, r1-1). Тело прокручено до конца —
// там кнопки.
export const OverTallConfirm: Story = () => {
  const t = useTranslations();
  const toast = useToast();
  useOnce(() => {
    toast.success(t('staff.drawer.disable.done'), { undo: async () => undefined });
  });
  useEffect(() => {
    // после применения предела высоты (атрибут тоста ставит эффект провайдера)
    const timer = setTimeout(() => {
      for (const el of document.querySelectorAll('[role="dialog"] *'))
        if (el.scrollHeight > el.clientHeight) el.scrollTop = el.scrollHeight;
    }, 100);
    return () => clearTimeout(timer);
  });
  return (
    <ConfirmDialog
      open
      variant="modal"
      onClose={() => undefined}
      onConfirm={() => undefined}
      title={t('staff.drawer.reset.confirm_title', { name: 'Мария К.' })}
      text={t('staff.drawer.reset.text')}
      consequences={[
        t('staff.drawer.reset.consequences.secrets'),
        t('staff.drawer.reset.consequences.sessions'),
        t('staff.drawer.reset.consequences.invite'),
      ]}
      confirmWord="maria.k@wf.example"
      confirmLabel={t('actions.submitForApproval')}
      note={t('staff.drawer.journal')}
    >
      {[1, 2, 3, 4, 5, 6].map((i) => (
        <p key={i}>{t('staff.drawer.journal')}</p>
      ))}
    </ConfirmDialog>
  );
};
OverTallConfirm.meta = { title: 'UI/Toast', layout: true };
