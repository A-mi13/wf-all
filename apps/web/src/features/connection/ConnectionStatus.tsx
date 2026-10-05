'use client';

import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useApi } from '../../api/context';

// Проверка связи с публичным API. Отдельной ручки здоровья в контракте нет (спека geo §4.4),
// поэтому — самый дешёвый анонимный запрос контракта: одна запись справочника городов
// (listCities, limit=1). Любой не-2xx и сетевая ошибка — «нет связи».
export function ConnectionStatus() {
  const api = useApi();
  const t = useTranslations('connection');
  const check = useQuery({
    queryKey: ['connection'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/cities', { params: { query: { limit: 1 } } });
      if (error || !data) throw new Error('connection: API не ответил');
      return true;
    },
  });
  if (check.isPending) return <p role="status">{t('checking')}</p>;
  return <p role="status">{check.isError ? t('down') : t('ok')}</p>;
}
