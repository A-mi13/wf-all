import { useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { isUnknownOutcome, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import { useLocale } from '../../i18n/locale';
import { useInvalidateApprovals } from '../approvals/queries';
import { COUNTRIES_KEY, GEO_KEY, geoKeys } from './queries';

type GeoCountry = components['schemas']['GeoCountry'];
type GeoCountryList = components['schemas']['GeoCountryList'];
type GeoCountryPatch = components['schemas']['GeoCountryPatch'];
type GeoCountryCreate = components['schemas']['GeoCountryCreate'];

// Правка и создание страны (план B, Task 21). Ответ правки — 200 со страной (поля «сразу»: включение, телефонный
// код, неделя, названия) или 202 с запросом на подтверждение (правила: валюта, возрасты, выключение — весь patch
// уходит в запрос). 202: страна не изменилась — кэш стран НЕ трогается (значения формы в него не пишутся),
// перечитываются только подтверждения (счётчик и списки). 200: страна из ответа — в список, остальное «Справочников»
// (карточки городов показывают «страна выключена») — устарело. Исход неизвестен (обрыв, 5xx) — страны перечитываются.
// Создание — всегда 202.
export const COUNTRY_KEY = ['geo', 'country-action'] as const;

export type CountryEdit = { code: string; patch: GeoCountryPatch };

export function useUpdateCountry(mutationKey: readonly unknown[] = COUNTRY_KEY) {
  const api = useApi();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const invalidateApprovals = useInvalidateApprovals();
  return useAdminMutation(
    ({ code, patch }: CountryEdit, key: string) =>
      unwrap(
        api.PATCH('/v1/geo/countries/{code}', {
          params: {
            path: { code },
            header: { 'Idempotency-Key': key, 'Accept-Language': locale },
          },
          body: patch,
        }),
      ),
    {
      mutationKey,
      onSuccess: async (data) => {
        if ('approval' in data) {
          await invalidateApprovals();
          return;
        }
        queryClient.setQueryData<GeoCountryList>(geoKeys.countries(locale), (old) =>
          old ? { items: old.items.map((c) => (c.code === data.code ? data : c)) } : old,
        );
        await queryClient.invalidateQueries({ queryKey: GEO_KEY });
      },
      onError: (e) => {
        if (isUnknownOutcome(e)) void queryClient.invalidateQueries({ queryKey: COUNTRIES_KEY });
      },
    },
  );
}

export function useCreateCountry(mutationKey: readonly unknown[]) {
  const api = useApi();
  const invalidateApprovals = useInvalidateApprovals();
  return useAdminMutation(
    (body: GeoCountryCreate, key: string) =>
      unwrap(
        api.POST('/v1/geo/countries', {
          params: { header: { 'Idempotency-Key': key } },
          body,
        }),
      ),
    { mutationKey, onSuccess: () => invalidateApprovals() },
  );
}

// 202 — запрос на подтверждение, а не страна.
export const isApproval = (
  r: GeoCountry | components['schemas']['ApprovalAccepted'],
): r is components['schemas']['ApprovalAccepted'] => 'approval' in r;

// Подписи правил: первый день недели (1 пн … 7 вс; вне диапазона — «—», ответ шире контракта) и язык страны
// (знакомый — его имя, иначе сам код).
export function useCountryLabels() {
  const t = useTranslations('geo.countries');
  const tl = useTranslations('locale');
  return {
    weekday: (d: number) =>
      Number.isInteger(d) && d >= 1 && d <= 7 ? t(`weekday.${d}`) : t('unknown'),
    language: (l: string) => (l === 'ru' || l === 'en' ? tl(l) : l),
  };
}
