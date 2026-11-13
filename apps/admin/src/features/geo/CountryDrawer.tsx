import { useIsMutating } from '@tanstack/react-query';
import { useEffect, useId, useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError } from '../../api/client';
import type { components } from '../../api/gen/admin';
import { useLeaveGuard } from '../../app/leaveGuard';
import { useLeavePanelTo } from '../../app/panels';
import { paths } from '../../app/paths';
import type { Decision } from '../../app/permissions';
import {
  Button,
  CheckboxField,
  Drawer,
  EmptyState,
  ErrorState,
  Form,
  PendingApprovalNotice,
  SelectField,
  Skeleton,
  TermHint,
  TextField,
  useErrorText,
  useToast,
} from '../../ui';
import { isApproval, useCountryLabels, useCreateCountry, useUpdateCountry } from './countries';
import { CURRENCIES } from './currencies';
import styles from './geo.module.css';
import { withCurrent } from './options';
import { useGeoCountries } from './queries';

type GeoCountry = components['schemas']['GeoCountry'];
type GeoCountryPatch = components['schemas']['GeoCountryPatch'];

// Языки интерфейса и названий (контракт: default_locale — из поддерживаемых ru, en).
const LOCALES = ['ru', 'en'] as const;
type Locale = (typeof LOCALES)[number];
const WEEKDAYS = ['1', '2', '3', '4', '5', '6', '7'] as const;
// Код ISO в любом регистре (поле приводит к заглавным — setValueAs), пробелы по краям не считаются.
const CODE = /^\s*[A-Za-z]{2}\s*$/;
const PREFIX = /^\+\d{1,4}$/;
const AGE = /^\d{1,3}$/;

type Values = {
  code: string;
  names: Record<Locale, string>;
  currency: string;
  default_locale: string;
  phone_prefix: string;
  week_starts_on: string;
  min_signup_age: string;
  age_of_majority: string;
  is_enabled: boolean;
};

const FORM_KEY = ['geo', 'country-action', 'form'] as const;

const fromCountry = (c: GeoCountry): Values => ({
  code: c.code,
  names: { ru: c.names.ru ?? '', en: c.names.en ?? '' },
  currency: c.currency,
  default_locale: c.default_locale,
  phone_prefix: c.phone_prefix,
  week_starts_on: String(c.week_starts_on),
  min_signup_age: String(c.min_signup_age),
  age_of_majority: String(c.age_of_majority),
  is_enabled: c.is_enabled,
});

const EMPTY: Values = {
  code: '',
  names: { ru: '', en: '' },
  currency: '',
  default_locale: 'ru',
  phone_prefix: '',
  week_starts_on: '1',
  min_signup_age: '',
  age_of_majority: '',
  is_enabled: false,
};

// Изменённые поля правки: только отличные от страны; названия — изменённые непустые (удаление перевода здесь не
// делается). code и default_locale не уходят никогда (R12: язык задаётся только при создании, лишнее поле — 400).
function countryPatch(c: GeoCountry, v: Values): GeoCountryPatch {
  const p: GeoCountryPatch = {};
  if (v.currency !== c.currency) p.currency = v.currency;
  if (v.phone_prefix !== c.phone_prefix) p.phone_prefix = v.phone_prefix;
  if (Number(v.week_starts_on) !== c.week_starts_on) p.week_starts_on = Number(v.week_starts_on);
  if (Number(v.min_signup_age) !== c.min_signup_age) p.min_signup_age = Number(v.min_signup_age);
  if (Number(v.age_of_majority) !== c.age_of_majority)
    p.age_of_majority = Number(v.age_of_majority);
  const names: Record<string, string> = {};
  for (const l of LOCALES) {
    const n = v.names[l].trim();
    if (n !== '' && n !== (c.names[l] ?? '')) names[l] = n;
  }
  if (Object.keys(names).length > 0) p.names = names;
  return p;
}

// Панель страны (?edit=<код> | new). Закрытая не рендерится; смена страны в адресе при открытой панели (палитра) —
// новая панель (key), форма не переносит ввод на другую страну.
export function CountryDrawer({
  code,
  onClose,
  canManage,
  rules,
}: {
  code: string | undefined;
  onClose: () => void;
  canManage: boolean;
  rules: Decision;
}) {
  if (code === undefined) return null;
  return (
    <CountryPanel key={code} code={code} onClose={onClose} canManage={canManage} rules={rules} />
  );
}

function CountryPanel({
  code,
  onClose,
  canManage,
  rules,
}: {
  code: string;
  onClose: () => void;
  canManage: boolean;
  rules: Decision;
}) {
  const t = useTranslations('geo.countries.drawer');
  const tn = useTranslations('nav');
  const ta = useTranslations('actions');
  const list = useGeoCountries();
  const busy = useIsMutating({ mutationKey: FORM_KEY }) > 0;
  const leavePanelTo = useLeavePanelTo();
  const formId = useId();
  const [dirty, setDirty] = useState(false);
  const [sent, setSent] = useState(false);
  const isNew = code === 'new';
  const country = isNew ? undefined : list.data?.items.find((c) => c.code === code);
  const editable = isNew || canManage;

  const title = isNew
    ? t('title_new')
    : country
      ? t('title_edit', { name: country.name, code: country.code })
      : list.data
        ? t('not_found')
        : code;

  const footer = sent ? (
    <>
      <Button
        variant="secondary"
        onClick={() => leavePanelTo({ to: paths.approvals, search: { box: 'mine' } })}
      >
        {tn('approvals')}
      </Button>
      <Button onClick={onClose}>{ta('close')}</Button>
    </>
  ) : editable && (isNew || country) ? (
    <Button type="submit" form={formId} loading={busy}>
      {isNew ? ta('submitForApproval') : ta('save')}
    </Button>
  ) : undefined;

  let body;
  if (sent) body = <PendingApprovalNotice />;
  else if (isNew || country)
    body = (
      <CountryForm
        id={formId}
        country={country}
        readOnly={!editable}
        rulesLocked={!isNew && rules === 'deny'}
        onDirty={setDirty}
        onSent={() => setSent(true)}
        onSaved={onClose}
      />
    );
  else if (list.data)
    body = (
      <EmptyState illustration="map" title={t('not_found')} text={t('not_found_text', { code })} />
    );
  else if (list.error)
    body = (
      <ErrorState
        error={list.error}
        onRetry={() => void list.refetch()}
        retrying={list.isFetching}
      />
    );
  else body = <Skeleton.Form />;

  return (
    <Drawer
      open
      onClose={onClose}
      title={title}
      dismissible={!busy}
      dirty={dirty && !sent}
      footer={footer}
    >
      <div className={styles.panel}>{body}</div>
    </Drawer>
  );
}

function CountryForm({
  id,
  country: current,
  readOnly,
  rulesLocked,
  onDirty,
  onSent,
  onSaved,
}: {
  id: string;
  country: GeoCountry | undefined;
  readOnly: boolean;
  rulesLocked: boolean;
  onDirty: (dirty: boolean) => void;
  onSent: () => void;
  onSaved: () => void;
}) {
  const t = useTranslations('geo.countries.drawer');
  const tl = useTranslations('locale');
  const toast = useToast();
  const errorText = useErrorText();
  const labels = useCountryLabels();
  const titleId = useId();
  // Снимок страны, из которого собрана форма (ревью G9-C, I-1): основа patch и обязательности названий. Список под
  // открытой формой перечитывается (фокус, инвалидации) — сравнение с ним отправило бы поля, которых сотрудник не
  // трогал, со старыми значениями и откатило бы чужую правку (version в patch нет — последняя запись выигрывает).
  const [country] = useState(current);
  const [initial] = useState<Values>(() => (country ? fromCountry(country) : EMPTY));
  const form = useForm<Values>({ defaultValues: initial });
  const locale = useWatch({ control: form.control, name: 'default_locale' });
  const [done, setDone] = useState(false);
  const create = useCreateCountry([...FORM_KEY, 'create']);
  const update = useUpdateCountry([...FORM_KEY, 'update']);

  // Введённое не отправлено (спека UI-фундамента §4.11): закрытие панели спрашивает Drawer.dirty, уход мимо панели —
  // useLeaveGuard; отправленное — не потеря.
  const dirty = form.formState.isDirty && !done;
  useLeaveGuard(dirty);
  useEffect(() => onDirty(dirty), [dirty, onDirty]);
  useEffect(() => () => onDirty(false), [onDirty]);

  const submit = async (v: Values) => {
    if (readOnly) return;
    if (!country) {
      const names: Record<string, string> = {};
      for (const l of LOCALES) if (v.names[l].trim() !== '') names[l] = v.names[l].trim();
      await create.mutateAsync({
        code: v.code,
        currency: v.currency,
        default_locale: v.default_locale,
        phone_prefix: v.phone_prefix,
        week_starts_on: Number(v.week_starts_on),
        min_signup_age: Number(v.min_signup_age),
        age_of_majority: Number(v.age_of_majority),
        names,
        is_enabled: v.is_enabled,
      });
      setDone(true);
      onSent();
      return;
    }
    const patch = countryPatch(country, v);
    // ничего не изменилось — запроса нет
    if (Object.keys(patch).length === 0) {
      onSaved();
      return;
    }
    const r = await update.mutateAsync({ code: country.code, patch });
    setDone(true);
    if (isApproval(r)) {
      onSent();
      return;
    }
    toast.success(t('saved', { name: r.name }));
    onSaved();
  };

  const onError = (e: ApiError) => {
    if (e.code !== 'geo.country_exists') return false;
    form.setError('code', { type: 'server', message: errorText(e) }, { shouldFocus: true });
    return true;
  };

  const canonical = country?.default_locale ?? locale;
  const nameRules = (l: Locale) => ({
    validate: {
      // название на языке страны обязательно; заведённый перевод здесь не удаляется
      required: (v: unknown) =>
        (canonical !== l && !country?.names[l]) || (typeof v === 'string' && v.trim() !== ''),
      maxLength: (v: unknown) => typeof v !== 'string' || v.trim().length <= 100,
    },
  });
  const age = {
    required: true,
    pattern: AGE,
    validate: {
      min: (v: unknown) => Number(v) >= 1,
      max: (v: unknown) => Number(v) <= 120,
    },
  };
  const ruleHint = t('dual');
  const currencies = withCurrent(
    CURRENCIES.map((c) => ({ value: c.code, label: c.code })),
    initial.currency || undefined,
  );

  return (
    <>
      <p className={styles.text}>{country ? t('text_edit') : t('text_new')}</p>
      <Form id={id} form={form} onSubmit={submit} onError={onError} className={styles.panel}>
        {!country && (
          <TextField
            name="code"
            label={t('code')}
            hint={t('code_hint')}
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="characters"
            autoFocus
            // регистр приводится сам: autoCapitalize на компьютере не работает (ревью G9-C, m-6)
            rules={{
              required: true,
              pattern: CODE,
              setValueAs: (v: unknown) => (typeof v === 'string' ? v.trim().toUpperCase() : v),
            }}
          />
        )}
        {LOCALES.map((l) => (
          <TextField
            key={l}
            name={`names.${l}`}
            label={l === 'ru' ? t('name_ru') : t('name_en')}
            autoComplete="off"
            readOnly={readOnly}
            rules={nameRules(l)}
          />
        ))}
        {country ? (
          <p className={styles.text}>
            {t('locale_fixed', { locale: labels.language(country.default_locale) })}
          </p>
        ) : (
          <SelectField
            name="default_locale"
            label={t('locale')}
            hint={t('locale_hint')}
            options={LOCALES.map((l) => ({ value: l, label: tl(l) }))}
            rules={{ required: true }}
          />
        )}
        <TextField
          name="phone_prefix"
          label={t('prefix')}
          hint={t('prefix_hint')}
          inputMode="tel"
          autoComplete="off"
          readOnly={readOnly}
          rules={{ required: true, pattern: PREFIX }}
        />
        <SelectField
          name="week_starts_on"
          label={t('week')}
          options={withCurrent(
            WEEKDAYS.map((d) => ({ value: d, label: labels.weekday(Number(d)) })),
            initial.week_starts_on,
          )}
          disabled={readOnly}
          rules={{ required: true }}
        />
        <section aria-labelledby={titleId} className={styles.section}>
          <div className={styles.note}>
            <h3 id={titleId} className={styles.sectionTitle}>
              {t('rules_title')}
            </h3>
            <TermHint term="dual_approval" describedBy={titleId} />
          </div>
          <p className={styles.text}>{t('source_hint')}</p>
          <SelectField
            name="currency"
            label={t('currency')}
            hint={ruleHint}
            placeholder={t('currency_placeholder')}
            options={currencies}
            disabled={readOnly || rulesLocked}
            rules={{ required: true }}
          />
          <TextField
            name="min_signup_age"
            label={t('min_age')}
            hint={ruleHint}
            inputMode="numeric"
            autoComplete="off"
            readOnly={readOnly || rulesLocked}
            rules={age}
          />
          <TextField
            name="age_of_majority"
            label={t('majority')}
            hint={ruleHint}
            inputMode="numeric"
            autoComplete="off"
            readOnly={readOnly || rulesLocked}
            rules={{
              ...age,
              validate: {
                ...age.validate,
                // не меньше минимального возраста регистрации (сервер ответил бы minimum)
                minimum: (v: unknown) => Number(v) >= Number(form.getValues('min_signup_age')),
              },
            }}
          />
        </section>
        {!country && <CheckboxField name="is_enabled" label={t('enable_now')} />}
      </Form>
    </>
  );
}
