import { useIsMutating, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useFormatter, useTranslations } from 'use-intl';
import { type ApiError, isApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import { paths } from '../../app/paths';
import { useLocale } from '../../i18n/locale';
import {
  Button,
  type Column,
  DataTable,
  Drawer,
  EmptyState,
  Form,
  infiniteTableProps,
  type Option,
  SelectField,
  useErrorText,
  useToast,
} from '../../ui';
import styles from './geo.module.css';
import { CITIES_KEY, geoKeys, useGeoCountries, useGeoPlaces, useGeoRegions } from './queries';

type GeoPlace = components['schemas']['GeoPlace'];
type CityStatus = components['schemas']['CityStatus'];
type Body = components['schemas']['GeoCityActivateRequest'];

const ACTIVATE_KEY = ['geo', 'activate'] as const;
const STATUSES: readonly CityStatus[] = ['waitlist', 'pilot', 'live'];

// «Активировать город» (*; спека §9.4 п. 8, спека geo §5.3): поиск места GeoNames → выбор →
// страна и регион → статус. Заведённое место (city_id) не выбирается — ссылка на его город. Пока
// запрос идёт, панель не закрывается: ответ должен дойти до экрана.
export function ActivateDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTranslations('geo.activate');
  const busy = useIsMutating({ mutationKey: ACTIVATE_KEY }) > 0;
  return (
    <Drawer open={open} onClose={onClose} title={t('title')} dismissible={!busy}>
      <ActivateBody onClose={onClose} />
    </Drawer>
  );
}

function ActivateBody({ onClose }: { onClose: () => void }) {
  const t = useTranslations('geo.activate');
  const [place, setPlace] = useState<GeoPlace | null>(null);
  return (
    <div className={styles.panel}>
      <p className={styles.text}>{t('text')}</p>
      {place ? (
        <ActivateForm place={place} onBack={() => setPlace(null)} onClose={onClose} />
      ) : (
        <PlaceSearch onChoose={setPlace} onClose={onClose} />
      )}
    </div>
  );
}

// Шаг 1: поиск места. q — от 1 символа (пустой сервер отверг бы); страна — фильтр поиска.
function PlaceSearch({
  onChoose,
  onClose,
}: {
  onChoose: (p: GeoPlace) => void;
  onClose: () => void;
}) {
  const t = useTranslations('geo.activate');
  const navigate = useNavigate();
  const format = useFormatter();
  const [q, setQ] = useState('');
  const filter = useForm<{ country: string }>({ defaultValues: { country: '' } });
  const country = useWatch({ control: filter.control, name: 'country' });
  const countries = useGeoCountries();
  const text = q.trim();
  const places = useGeoPlaces({ q: text, ...(country ? { country } : {}) }, text.length > 0);

  const columns: Column<GeoPlace>[] = [
    {
      id: 'place',
      header: t('columns.place'),
      cell: (p) => (
        <span className={styles.place}>
          <span className={styles.name}>{p.name}</span>
          <span className={styles.muted}>{p.ascii_name}</span>
        </span>
      ),
    },
    { id: 'region', header: t('columns.region'), cell: (p) => p.region_name ?? '—' },
    { id: 'country', header: t('columns.country'), cell: (p) => p.country.name },
    {
      id: 'population',
      header: t('columns.population'),
      align: 'end',
      cell: (p) => format.number(p.population),
    },
    {
      id: 'action',
      header: t('columns.action'),
      align: 'end',
      width: 'md',
      cell: (p) => {
        const cityId = p.city_id;
        if (cityId)
          return (
            <span className={styles.place}>
              <span className={styles.muted}>{t('already')}</span>
              <Button
                variant="link"
                aria-label={t('open_city_label', { name: p.name })}
                onClick={() => {
                  onClose();
                  void navigate({ to: paths.city, params: { cityId } });
                }}
              >
                {t('open_city')}
              </Button>
            </span>
          );
        return (
          <Button
            variant="secondary"
            aria-label={t('choose_label', { name: p.name })}
            onClick={() => onChoose(p)}
          >
            {t('choose')}
          </Button>
        );
      },
    },
  ];

  return (
    <>
      <Form form={filter} onSubmit={() => {}}>
        <SelectField
          name="country"
          label={t('place_country')}
          options={[
            { value: '', label: t('place_country_all') },
            ...(countries.data?.items ?? []).map((c) => ({ value: c.code, label: c.name })),
          ]}
        />
      </Form>
      <DataTable
        label={t('places_label')}
        columns={columns}
        rowKey={(p) => String(p.geoname_id)}
        {...(text ? infiniteTableProps(places) : { rows: [], onRetry: () => {} })}
        search={{ value: q, onChange: setQ, placeholder: t('search') }}
        summary={text.length > 0}
        empty={text ? undefined : <EmptyState title={t('prompt')} text={t('prompt_text')} />}
      />
    </>
  );
}

type Values = { country_code: string; region_id: string; status: CityStatus };

// Шаг 2: страна (по умолчанию — страна места; список — включённые страны и страна места), регион
// (по данным GeoNames; при смене страны — пусто: без выбора у города не будет региона), статус.
// country_code уходит, только если страну переопределили (иначе сервер снял бы регион по умолчанию),
// region_id — только выбранный. 409 geo.city_already_active — текст и поиск города в списке;
// 422 geo.country_not_enabled / geo.region_not_found — у поля. Повтор отправки после обрыва —
// тот же ключ (useAdminMutation: те же значения, исход неизвестен).
function ActivateForm({
  place,
  onBack,
  onClose,
}: {
  place: GeoPlace;
  onBack: () => void;
  onClose: () => void;
}) {
  const t = useTranslations('geo.activate');
  const ts = useTranslations('geo.status');
  const api = useApi();
  const locale = useLocale();
  const navigate = useNavigate();
  const toast = useToast();
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const form = useForm<Values>({
    defaultValues: { country_code: place.country.code, region_id: '', status: 'waitlist' },
  });
  const country = useWatch({ control: form.control, name: 'country_code' });
  // Ответ пришёл, когда сотрудник уже ушёл со страницы (меню), — кэш и тост, но без перехода: не
  // уводить его из раздела, где он теперь. StrictMode: эффект ставит флаг заново при повторном монтировании.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const overridden = country !== place.country.code;
  const countries = useGeoCountries();
  const regions = useGeoRegions(country);

  const activate = useAdminMutation(
    (body: Body, key: string) =>
      unwrap(
        api.POST('/v1/geo/cities', {
          params: { header: { 'Idempotency-Key': key, 'Accept-Language': locale } },
          body,
        }),
      ),
    {
      mutationKey: ACTIVATE_KEY,
      onSuccess: async (city) => {
        // ответ — карточка города: страница откроется без второго запроса
        queryClient.setQueryData(geoKeys.city(city.id, locale), city);
        toast.success(t('success', { name: city.name }));
        if (alive.current) {
          onClose();
          void navigate({ to: paths.city, params: { cityId: city.id } });
        }
        await queryClient.invalidateQueries({ queryKey: CITIES_KEY });
      },
    },
  );

  const submit = (v: Values) =>
    activate.mutateAsync({
      geoname_id: place.geoname_id,
      status: v.status,
      ...(v.country_code !== place.country.code ? { country_code: v.country_code } : {}),
      ...(v.region_id ? { region_id: v.region_id } : {}),
    });

  const onError = (e: ApiError) => {
    const field =
      e.code === 'geo.country_not_enabled'
        ? 'country_code'
        : e.code === 'geo.region_not_found'
          ? 'region_id'
          : null;
    if (!field) return false;
    form.setError(field, { type: 'server', message: errorText(e) }, { shouldFocus: true });
    return true;
  };

  const enabled = (countries.data?.items ?? []).filter((c) => c.is_enabled);
  const countryOptions: Option[] = enabled.map((c) => ({ value: c.code, label: c.name }));
  // страна места — в списке всегда: выбор по умолчанию виден, даже если она выключена
  if (!countryOptions.some((o) => o.value === place.country.code))
    countryOptions.unshift({
      value: place.country.code,
      label: t('country_off', { name: place.country.name }),
    });
  const regionDefault = overridden
    ? t('region_none')
    : place.region_name
      ? t('region_default', { region: place.region_name })
      : t('region_default_unknown');
  const conflict = isApiError(activate.error, 'geo.city_already_active');

  return (
    <>
      <div className={styles.chosen}>
        <span className={styles.place}>
          <span className={styles.label}>{t('place')}</span>
          <span className={styles.name}>
            {place.region_name
              ? t('place_value', {
                  name: place.name,
                  region: place.region_name,
                  country: place.country.name,
                })
              : t('place_value_no_region', { name: place.name, country: place.country.name })}
          </span>
        </span>
        <Button variant="link" disabled={activate.isPending} onClick={onBack}>
          {t('change_place')}
        </Button>
      </div>
      <Form form={form} onSubmit={submit} onError={onError}>
        <SelectField
          name="country_code"
          label={t('country')}
          hint={t('country_hint')}
          options={countryOptions}
          rules={{
            // другая страна — регион прежней к ней не относится
            onChange: () => form.setValue('region_id', ''),
          }}
        />
        <SelectField
          name="region_id"
          label={t('region')}
          hint={overridden ? t('region_hint_override') : t('region_hint')}
          options={[
            { value: '', label: regionDefault },
            ...(regions.data?.items ?? []).map((r) => ({ value: r.id, label: r.name })),
          ]}
        />
        <SelectField
          name="status"
          label={t('status')}
          options={STATUSES.map((s) => ({ value: s, label: ts(s) }))}
        />
        {conflict && (
          <Button
            variant="link"
            onClick={() => {
              onClose();
              void navigate({ to: paths.cities, search: { q: place.name } });
            }}
          >
            {t('find_city')}
          </Button>
        )}
        <Button type="submit" loading={activate.isPending}>
          {t('submit')}
        </Button>
      </Form>
    </>
  );
}
