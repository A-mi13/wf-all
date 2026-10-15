import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import { useSession } from '../../app/session';
import { useToast } from '../../ui';

// Пятая неудача пароля или второго фактора подряд (attempts_left = 0, спека §4.6 п. 3): сервер
// закрыл вход на час и погасил все сессии. Тост «доступ закрыт на час» стоит над роутером и
// переживает переход; me перечитывается — сессии нет, дальше ведёт роутер (вход).
export function useLockout(): () => void {
  const t = useTranslations('me');
  const toast = useToast();
  const session = useSession();
  return useCallback(() => {
    toast.error(t('locked'));
    void session.refresh();
  }, [t, toast, session]);
}

// Неудача, после которой счётчик попыток ушёл в ноль.
export const isLock = (code: string, attemptsLeft: number | undefined) =>
  (code === 'staff.mfa_invalid' || code === 'staff.password_invalid') && attemptsLeft === 0;
