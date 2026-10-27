import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { ApiError } from '../api/client';

// Текст ошибки API по коду: errors.<код> из локалей. Список кодов открытый (сервер может прислать
// код новее сборки) — код без текста, пустой или указывающий на группу ключей получает общий
// текст errors.internal. Единственное место такого отката: формы (FormError), тосты, экраны.
export function useErrorText(): (error: ApiError | string) => string {
  const t = useTranslations('errors');
  return useCallback(
    (error) => {
      const code = typeof error === 'string' ? error : error.code;
      // has() верен и для группы («auth»), текст есть только у листа-строки
      return code && t.has(code) && typeof t.raw(code) === 'string' ? t(code) : t('internal');
    },
    [t],
  );
}
