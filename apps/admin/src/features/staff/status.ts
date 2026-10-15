import { useCallback, useState } from 'react';
import { useTranslations } from 'use-intl';
import type { ChipTone } from '../../ui';
import type { StaffAccount } from './roles';

// Статус учётки для чипа — список и панель сотрудника: выключен, приглашение не принято /
// просрочено (из invite), вход закрыт (locked_until в будущем), активен. «Сейчас» — на момент
// монтирования экрана (Date.now() в рендере запрещён линтом purity).
export function useStaffStatus(): (a: StaffAccount) => { tone: ChipTone; text: string } {
  const t = useTranslations('staff.status');
  const [now] = useState(() => Date.now());
  return useCallback(
    (a) => {
      if (a.status === 'disabled') return { tone: 'neutral', text: t('disabled') };
      if (a.status === 'invited')
        return a.invite?.expired
          ? { tone: 'danger', text: t('expired') }
          : { tone: 'warning', text: t('invited') };
      if (a.status === 'active')
        return a.locked_until && Date.parse(a.locked_until) > now
          ? { tone: 'danger', text: t('locked') }
          : { tone: 'success', text: t('active') };
      return { tone: 'neutral', text: t('unknown') };
    },
    [t, now],
  );
}
