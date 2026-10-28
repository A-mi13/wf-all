import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import type { Locale } from '../../i18n/messages';
import { Button, ConfirmDialog, Form, StatusChip, TextField, useToast } from '../../ui';
import styles from './geo.module.css';
import { useUpdateCity } from './mutations';

type GeoCityDetails = components['schemas']['GeoCityDetails'];

// Поддерживаемые языки названий (контракт GeoNames: ru, en).
const LOCALES: readonly Locale[] = ['ru', 'en'];

type Values = { names: Record<string, string> };

// Названия города по языкам (*; geo.cities.rename в городе — модератор своего): строка на язык;
// язык страны (canonical_locale) — «основное», обязательно, удалить нельзя. «Сохранить» отправляет
// только изменённые языки (пустой перевод — не удаление: удаляет только кнопка); «Удалить перевод» —
// ConfirmDialog, тело names: {<язык>: null}. Без права — только чтение.
export function CityNamesCard({ city, canEdit }: { city: GeoCityDetails; canEdit: boolean }) {
  const t = useTranslations('geo.city.names');
  if (!canEdit)
    return (
      <dl className={styles.names}>
        {LOCALES.map((l) => (
          <div key={l} className={styles.nameItem}>
            <dt className={styles.muted}>{t(`locales.${l}`)}</dt>
            <dd className={styles.name}>{city.names[l] ?? t('empty')}</dd>
          </div>
        ))}
      </dl>
    );
  return <NamesForm city={city} />;
}

function NamesForm({ city }: { city: GeoCityDetails }) {
  const t = useTranslations('geo.city.names');
  const toast = useToast();
  const values: Values = {
    names: Object.fromEntries(LOCALES.map((l) => [l, city.names[l] ?? ''])),
  };
  const form = useForm<Values>({ values });
  const save = useUpdateCity(city.id, ['geo', 'city', city.id, 'names']);
  const remove = useUpdateCity(city.id, ['geo', 'city', city.id, 'names', 'delete']);
  const [deleting, setDeleting] = useState<Locale | null>(null);
  // Перевод удалён — кнопки «Удалить перевод» (открывателя окна) больше нет, и фокус упал бы на body
  // (ревью G8, m4): ставим его на поле этого языка. Окно закрывается и кнопка исчезает в разных
  // отрисовках (ответ в кэше — отдельным уведомлением): эффект ждёт и того, и другого.
  const [deleted, setDeleted] = useState<{ locale: Locale } | null>(null);
  useEffect(() => {
    if (!deleted || city.names[deleted.locale] !== undefined) return;
    const active = document.activeElement;
    if (!active || active === document.body) form.setFocus(`names.${deleted.locale}`);
  }, [deleted, form, city.names]);

  const submit = async (v: Values) => {
    const changed = Object.fromEntries(
      LOCALES.flatMap((l) => {
        const next = v.names[l] ?? '';
        return next !== '' && next !== (city.names[l] ?? '') ? [[l, next]] : [];
      }),
    );
    if (Object.keys(changed).length === 0) return;
    await save.mutateAsync({ names: changed });
    toast.success(t('saved'));
  };

  const confirmDelete = () => {
    if (!deleting) return;
    const locale = deleting;
    remove.mutate(
      { names: { [deleting]: null } },
      {
        onSuccess: () => {
          toast.success(t('deleted'));
          setDeleting(null);
          setDeleted({ locale });
        },
      },
    );
  };
  const closeDelete = () => {
    setDeleting(null);
    // ошибка прошлой попытки не залипает в следующем окне
    remove.reset();
  };

  return (
    <>
      <Form form={form} onSubmit={submit} className={styles.panel}>
        {LOCALES.map((l) => {
          const canonical = l === city.canonical_locale;
          return (
            <div key={l} className={styles.nameRow}>
              <TextField
                name={`names.${l}`}
                label={
                  <>
                    {t(`locales.${l}`)}{' '}
                    {canonical && <StatusChip tone="accent">{t('canonical')}</StatusChip>}
                  </>
                }
                autoComplete="off"
                rules={canonical ? { required: true, maxLength: 100 } : { maxLength: 100 }}
              />
              {!canonical && city.names[l] !== undefined && (
                <Button
                  variant="danger-outline"
                  aria-label={t('delete_label', { locale: t(`locales.${l}`) })}
                  onClick={() => setDeleting(l)}
                >
                  {t('delete')}
                </Button>
              )}
            </div>
          );
        })}
        <div className={styles.row}>
          <Button type="submit" loading={save.isPending}>
            {t('save')}
          </Button>
        </div>
      </Form>
      <ConfirmDialog
        open={deleting !== null}
        onClose={closeDelete}
        onConfirm={confirmDelete}
        title={t('confirm_title', { name: (deleting && city.names[deleting]) ?? '' })}
        text={t('confirm_text')}
        confirmWord={t('confirm_word')}
        confirmLabel={t('confirm_label')}
        loading={remove.isPending}
        error={remove.error}
      />
    </>
  );
}
