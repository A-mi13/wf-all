import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { useApi } from '../../api/context';

export function HealthStatus() {
  const api = useApi();
  const t = useTranslations('health');
  const health = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/health');
      if (error || !data) throw new Error('health: API недоступен');
      return data;
    },
  });
  if (health.isPending) return <p role="status">{t('checking')}</p>;
  return <p role="status">{health.isError ? t('down') : t('ok')}</p>;
}
