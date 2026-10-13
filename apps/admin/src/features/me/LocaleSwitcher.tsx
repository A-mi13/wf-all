import { useTranslations } from 'use-intl';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useApplyLocale, useLocale } from '../../i18n/locale';
import type { Locale } from '../../i18n/messages';
import { Button, FormError } from '../../ui';
import styles from './LocaleSwitcher.module.css';

const LOCALES: Locale[] = ['ru', 'en'];

// Переключатель языка (спека §9.1) — в меню профиля AppShell (карточка сотрудника, «Ещё» на
// телефоне): выбор сохраняется PATCH /v1/staff/me/locale и после 204 сразу меняет тексты, без
// перезагрузки. Ошибка — FormError (текст по коду, откат на errors.internal).
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
    <div role="group" aria-label={t('label')}>
      <div className={styles.segments}>
        {LOCALES.map((l) => (
          <Button
            key={l}
            variant={l === current ? 'primary' : 'secondary'}
            className={styles.segment}
            lang={l}
            aria-pressed={l === current}
            disabled={save.isPending}
            onClick={() => l !== current && save.mutate(l)}
          >
            {t(l)}
          </Button>
        ))}
      </div>
      <FormError error={save.error} />
    </div>
  );
}
