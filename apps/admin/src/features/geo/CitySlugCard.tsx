import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { ApiError } from '../../api/client';
import type { components } from '../../api/gen/admin';
import { useLeaveGuard } from '../../app/leaveGuard';
import { useDateTime } from '../../i18n/format';
import { Button, Form, TextField, useErrorText, useToast } from '../../ui';
import styles from './geo.module.css';
import { useUpdateCity } from './mutations';

type GeoCityDetails = components['schemas']['GeoCityDetails'];

// Формат адреса — как CHECK cities_slug_format: латиница в нижнем регистре и цифры, дефис только
// между частями; до 100 символов (контракт GeoCityPatch.slug).
const SLUG = /^[a-z0-9]+(-[a-z0-9]+)*$/;

// Адрес города в вебе (*; geo.cities.manage): поле с форматом (проверка до запроса) и подсказкой
// «старый адрес продолжит работать», история прежних адресов (из карточки); 409 geo.slug_taken —
// у поля. Тот же адрес — запроса нет. readOnly — карточка перечитывается на новом языке: ввод остаётся
// (keepDirtyValues), отправка недоступна (ревью G8, m18).
export function CitySlugCard({ city, readOnly }: { city: GeoCityDetails; readOnly?: boolean }) {
  const t = useTranslations('geo.city.slug');
  const ta = useTranslations('actions');
  const toast = useToast();
  const errorText = useErrorText();
  const dateTime = useDateTime();
  const form = useForm<{ slug: string }>({
    values: { slug: city.slug },
    resetOptions: { keepDirtyValues: true },
  });
  const update = useUpdateCity(city.id, ['geo', 'city', city.id, 'slug']);
  // несохранённый ввод — уход со страницы спрашивает (спека UI-фундамента §4.11); ответ сервера сбрасывает dirty
  useLeaveGuard(form.formState.isDirty);

  const submit = async ({ slug }: { slug: string }) => {
    if (readOnly || slug === city.slug) return;
    const updated = await update.mutateAsync({ slug });
    // форма — к ответу: сохранённое поле не «залипает» поверх будущих данных (раунд 1, I1)
    form.reset({ slug: updated.slug }, { keepDirtyValues: false });
    toast.success(t('saved'));
  };
  const onError = (e: ApiError) => {
    if (e.code !== 'geo.slug_taken') return false;
    form.setError('slug', { type: 'server', message: errorText(e) }, { shouldFocus: true });
    return true;
  };

  return (
    <div className={styles.panel}>
      <Form form={form} onSubmit={submit} onError={onError} className={styles.panel}>
        <TextField
          name="slug"
          label={t('label')}
          hint={t('hint')}
          autoComplete="off"
          spellCheck={false}
          autoCapitalize="none"
          rules={{ required: true, maxLength: 100, pattern: SLUG }}
        />
        <div className={styles.row}>
          <Button type="submit" loading={update.isPending} disabled={readOnly}>
            {ta('save')}
          </Button>
        </div>
      </Form>
      {city.slug_history.length > 0 ? (
        <ul aria-label={t('history')} className={styles.history}>
          {city.slug_history.map((h) => (
            <li key={`${h.slug}:${h.replaced_at}`} className={styles.historyItem}>
              <span className={styles.name}>{h.slug}</span>
              <span className={styles.muted}>
                {t('replaced', { when: dateTime(h.replaced_at) })}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className={styles.text}>{t('history_empty')}</p>
      )}
    </div>
  );
}
