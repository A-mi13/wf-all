import { useTranslations } from 'use-intl';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useApplyLocale, useLocale } from '../../i18n/locale';
import type { Locale } from '../../i18n/messages';
import { FormError, Segmented } from '../../ui';

const LOCALES: Locale[] = ['ru', 'en'];

// Переключатель языка (спека §9.1) — в меню профиля AppShell (карточка сотрудника, «Ещё» на
// телефоне): компактный сегмент «RU · EN» в одну строку (в макете 07 переключателя нет). На кнопке —
// код языка, имя «RU — Русский» начинается с видимого текста (WCAG 2.5.3), полное — и в подсказке.
// Выбор сохраняется PATCH /v1/staff/me/locale и после 204 сразу меняет тексты, без перезагрузки.
// Ошибка — FormError (текст по коду, откат на errors.internal).
export function LocaleSwitcher() {
  const t = useTranslations('locale');
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
    <div>
      <Segmented
        label={t('label')}
        value={current}
        busy={save.isPending}
        onChange={(l) => save.mutate(l)}
        options={LOCALES.map((l) => ({
          value: l,
          text: t(`short.${l}`),
          name: t('option', { code: t(`short.${l}`), name: t(l) }),
          title: t(l),
          lang: l,
        }))}
      />
      <FormError error={save.error} />
    </div>
  );
}
