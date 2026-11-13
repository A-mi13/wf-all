import { createElement, useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { useDateTime } from '../../i18n/format';
import { RelativeTime } from '../../ui';
import { deviceOf } from './device';

type Session = components['schemas']['StaffSession'];

// Подписи сеанса — «Мой доступ» и сеансы сотрудника в «Ролях и доступе»: «Chrome · Windows»,
// «Сеть … · вход 5 мин назад · активность только что» (время — RelativeTime: точное — скрытым текстом и
// подсказкой, план UF2a.10). Сеть приходит уже маскированной (/24, /48). created — точное время строкой для
// имени кнопки «Завершить сеанс: …» (имя — текст, не разметка).
export function useSessionLabel() {
  const t = useTranslations('me.sessions');
  const dateTime = useDateTime();
  return useCallback(
    (s: Session) => {
      const d = deviceOf(s.user_agent);
      const device = d
        ? t('device', { browser: t(`browsers.${d.browser}`), os: t(`systems.${d.os}`) })
        : t('unknown_device');
      return {
        device,
        created: dateTime(s.created_at),
        details: t.rich('details', {
          ip: s.ip,
          created: () => createElement(RelativeTime, { value: s.created_at }),
          seen: () => createElement(RelativeTime, { value: s.last_seen_at }),
        }),
      };
    },
    [t, dateTime],
  );
}
