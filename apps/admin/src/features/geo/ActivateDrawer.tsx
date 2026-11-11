import { useIsMutating, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useEffect, useId, useRef, useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError, isApiError, isUnknownOutcome, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import { paths } from '../../app/paths';
import { useLocale } from '../../i18n/locale';
import { Button, Drawer, Form, type Option, SelectField, useErrorText, useToast } from '../../ui';
import styles from './geo.module.css';
import { invalidateCityLists } from './mutations';
import { PlaceAction, type PlaceFilter, PlaceSearch } from './PlaceSearch';
import { GEO_PLACES_KEY, geoKeys, useGeoCountries, useGeoPlaces, useGeoRegions } from './queries';

type GeoPlace = components['schemas']['GeoPlace'];
type CityStatus = components['schemas']['CityStatus'];
type Body = components['schemas']['GeoCityActivateRequest'];

const ACTIVATE_KEY = ['geo', 'activate'] as const;
const STATUSES: readonly CityStatus[] = ['waitlist', 'pilot', 'live'];

// «Активировать город» (*; спека §9.4 п. 8, спека geo §5.3): поиск места GeoNames → выбор →
// страна и регион → статус. Заведённое место (city_id) не выбирается — «Открыть город». Пока
// запрос идёт, панель не закрывается: ответ должен дойти до экрана. Действие шага («Выбрать»,
// «Активировать») — в закреплённом подвале панели (V1 п. 9), прокручивается только список мест.
// Закрытая панель не рендерится: выбор и шаг при новом открытии чистые.
export function ActivateDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  return open ? <ActivateFlow onClose={onClose} /> : null;
}

// Исход активации неизвестен (обрыв, 5xx) и панель закрыли — город мог появиться: списки городов
// перечитываются (ревью G8, m14). Поиск и страна мест — здесь: «Другое место» возвращает к прежним
// результатам, выбор снят, фокус — на список (ревью V1, m1, m2).
function ActivateFlow({ onClose }: { onClose: () => void }) {
  const t = useTranslations('geo.activate');
  const queryClient = useQueryClient();
  const busy = useIsMutating({ mutationKey: ACTIVATE_KEY }) > 0;
  const formId = useId();
  const [filter, setFilter] = useState<PlaceFilter>({ q: '', country: '' });
  const [selected, setSelected] = useState<GeoPlace | null>(null);
  const [place, setPlace] = useState<GeoPlace | null>(null);
  const [returned, setReturned] = useState(false);
  const unknown = useRef(false);
  const close = () => {
    if (unknown.current) void invalidateCityLists(queryClient);
    onClose();
  };
  const footer = place ? (
    <Button type="submit" form={formId} loading={busy}>
      {t('submit')}
    </Button>
  ) : (
    <PlaceAction selected={selected} onChoose={setPlace} onClose={close} />
  );
  return (
    <Drawer open onClose={close} title={t('title')} dismissible={!busy} footer={footer}>
      <div className={place ? styles.panel : styles.fillPanel}>
        <p className={styles.text}>{t('text')}</p>
        {place ? (
          <ActivateForm
            id={formId}
            place={place}
            onBack={() => {
              setSelected(null);
              setPlace(null);
              setReturned(true);
            }}
            onClose={close}
            onUnknown={() => (unknown.current = true)}
          />
        ) : (
          <PlaceSearch
            selected={selected}
            onSelect={setSelected}
            onChoose={setPlace}
            filter={filter}
            onFilterChange={setFilter}
            focusList={returned}
          />
        )}
      </div>
    </Drawer>
  );
}

type Values = { country_code: string; region_id: string; status: CityStatus };

// Шаг 2: страна (по умолчанию — страна места; список — включённые страны и страна места), регион
// (по данным GeoNames; при смене страны — пусто: без выбора у города не будет региона), статус.
// country_code уходит, только если страну переопределили (иначе сервер снял бы регион по умолчанию),
// region_id — только выбранный. 409 geo.city_already_active — текст и поиск города в списке;
// 422 geo.country_not_enabled / geo.region_not_found — у поля. Повтор отправки после обрыва —
// тот же ключ (useAdminMutation: те же значения, исход неизвестен). 409 и успех — места GeoNames
// устарели (метка «уже заведён»); при 409 место перечитывается, и есть city_id — «Открыть город» ведёт
// на него (ревью G8, m3). Шаг открылся — фокус на «Страна города» (V1, m2).
function ActivateForm({
  id,
  place,
  onBack,
  onClose,
  onUnknown,
}: {
  id: string; // форма — по id: кнопка «Активировать» в подвале панели (form={id})
  place: GeoPlace;
  onBack: () => void;
  onClose: () => void;
  onUnknown: () => void; // исход неизвестен — при закрытии панели списки перечитаются
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
  useEffect(() => form.setFocus('country_code'), [form]);
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
        await Promise.all([
          invalidateCityLists(queryClient),
          queryClient.invalidateQueries({ queryKey: GEO_PLACES_KEY }),
        ]);
      },
      onError: (e) => {
        if (isUnknownOutcome(e)) onUnknown();
        if (isApiError(e, 'geo.city_already_active'))
          void queryClient.invalidateQueries({ queryKey: GEO_PLACES_KEY });
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
  // место перечитано после 409: город на нём — по city_id
  const fresh = useGeoPlaces({ q: place.name, country: place.country.code }, conflict);
  const existing = fresh.data?.pages
    .flatMap((p) => p.items)
    .find((p) => p.geoname_id === place.geoname_id)?.city_id;

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
      <Form id={id} form={form} onSubmit={submit} onError={onError}>
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
        {conflict && existing && (
          <Button
            variant="link"
            aria-label={t('open_city_label', { name: place.name })}
            onClick={() => {
              onClose();
              void navigate({ to: paths.city, params: { cityId: existing } });
            }}
          >
            {t('open_city')}
          </Button>
        )}
        {conflict && !existing && (
          <Button
            variant="link"
            // пока место перечитывается — занята: быстрый клик не уведёт в список вместо города (раунд 1)
            loading={fresh.isPending}
            onClick={() => {
              onClose();
              void navigate({ to: paths.cities, search: { q: place.name } });
            }}
          >
            {t('find_city')}
          </Button>
        )}
      </Form>
    </>
  );
}
