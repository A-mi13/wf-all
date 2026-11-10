import { useTranslations } from 'use-intl';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useSession } from '../../app/session';
import { setDeviceLocale } from '../../i18n/deviceLocale';
import { useApplyLocale, useLocale } from '../../i18n/locale';
import type { Locale } from '../../i18n/messages';
import { FormError, Segmented } from '../../ui';

const LOCALES: Locale[] = ['ru', 'en'];

// Переключатель языка (спека §9.1) — в меню профиля AppShell (карточка сотрудника, «Ещё» на
// телефоне): компактный сегмент «RU · EN» в одну строку (в макете 07 переключателя нет). На кнопке —
// код языка, имя «RU — Русский» начинается с видимого текста (WCAG 2.5.3), полное — и в подсказке.
// Выбор сохраняется PATCH /v1/staff/me/locale и после 204 сразу меняет тексты, без перезагрузки.
// Ошибка — FormError (текст по коду, откат на errors.internal). Фрагмент без обёртки: карточка
// сотрудника раскладывает сегменты и ошибку по своей сетке (ошибка — строкой ниже во всю ширину).
export function LocaleSwitcher() {
  const api = useApi();
  const current = useLocale();
  const apply = useApplyLocale();
  const save = useAdminMutation(
    (locale: Locale, key: string) =>
      api.PATCH('/v1/staff/me/locale', {
        params: { header: { 'Idempotency-Key': key } },
        body: { locale },
      }),
    { onSuccess: (_, locale) => apply(locale) },
  );

  return (
    <>
      <LocaleSegmented value={current} busy={save.isPending} onChange={(l) => save.mutate(l)} />
      <FormError error={save.error} />
    </>
  );
}

// Переключатель языка до входа (решение пользователя 10.11; экраны входа, AuthLayout): выбор устройства
// wf.locale — без запроса (учётки ещё нет), тексты меняются сразу. На setup и active язык — me.locale (его
// меняет LocaleSwitcher каркаса; PATCH на setup сервер не примет): выбор устройства там ничего бы не поменял,
// поэтому переключатель не рисуется (тупик без результата).
export function DeviceLocaleSwitcher() {
  const s = useSession();
  const current = useLocale();
  if (s.status === 'setup' || s.status === 'active') return null;
  return <LocaleSegmented value={current} onChange={setDeviceLocale} />;
}

// «RU · EN»: на кнопке код языка, имя «RU — Русский» начинается с видимого текста (WCAG 2.5.3), полное — и
// в подсказке.
function LocaleSegmented(props: { value: Locale; onChange: (l: Locale) => void; busy?: boolean }) {
  const t = useTranslations('locale');
  return (
    <Segmented
      label={t('label')}
      value={props.value}
      busy={props.busy}
      onChange={props.onChange}
      options={LOCALES.map((l) => ({
        value: l,
        text: t(`short.${l}`),
        name: t('option', { code: t(`short.${l}`), name: t(l) }),
        title: t(l),
        lang: l,
      }))}
    />
  );
}
