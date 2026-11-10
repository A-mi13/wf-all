import { useEffect, useRef } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
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
      action: { label: t('states.error.retry'), onClick: () => undefined },
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
      action: { label: t('states.error.retry'), onClick: () => undefined },
    });
  });
  return null;
};
