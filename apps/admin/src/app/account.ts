import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { unwrap } from '../api/client';
import { useApi } from '../api/context';
import { useLocale } from '../i18n/locale';
import { can, type Me } from './permissions';

// Карточка сотрудника в AppShell (макет 07): «Мария К.», «модератор · Ставрополь». Названия
// городов — GET /v1/geo/cities?ids=… на языке интерфейса (P28); без права geo.cities.read или пока
// ответа нет — число городов. У владельца, админа и поддержки городов нет — только роль.
export function useAccountCard(me: Me): { name: string; details: string } {
  const t = useTranslations();
  const api = useApi();
  const locale = useLocale();
  const ids = me.city_ids;
  const canNames = ids.length > 0 && can(me, 'geo.cities.read');
  const names = useQuery({
    queryKey: ['geo', 'cities', 'names', locale, ids],
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/cities', {
          params: {
            query: { ids, limit: Math.min(ids.length, 100) },
            header: { 'Accept-Language': locale },
          },
        }),
      ),
    enabled: canNames,
    staleTime: 10 * 60_000,
    select: (page) => page.items.map((c) => c.name).join(', '),
  });

  const roleKey = `roles.${me.role ?? 'unknown'}`;
  const role = t.has(roleKey) ? t(roleKey) : t('roles.unknown');
  const cities = ids.length === 0 ? '' : names.data || t('account.cities', { count: ids.length });
  return {
    name: me.display_name ?? me.email ?? '',
    details: [role, cities].filter(Boolean).join(' · '),
  };
}
