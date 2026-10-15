import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { useDateTime } from '../../i18n/format';
import { deviceOf } from './device';

type Session = components['schemas']['StaffSession'];

// Подписи сеанса — «Мой доступ» и сеансы сотрудника в «Ролях и доступе»: «Chrome · Windows»,
// «Сеть … · вход … · активность …». Сеть приходит уже маскированной (/24, /48).
export function useSessionLabel() {
  const t = useTranslations('me.sessions');
  const dateTime = useDateTime();
  return useCallback(
    (s: Session) => {
      const d = deviceOf(s.user_agent);
      const device = d
        ? t('device', { browser: t(`browsers.${d.browser}`), os: t(`systems.${d.os}`) })
        : t('unknown_device');
      const created = dateTime(s.created_at);
      return {
        device,
        created,
        details: t('details', { ip: s.ip, created, seen: dateTime(s.last_seen_at) }),
      };
    },
    [t, dateTime],
  );
}
