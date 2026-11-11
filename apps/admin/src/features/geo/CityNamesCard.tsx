import { useEffect, useRef, useState } from 'react';
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
// ConfirmDialog, тело names: {<язык>: null}. Без права — только чтение. Стёртый перевод при «Сохранить»
// возвращается к сохранённому с подсказкой «удалить — кнопкой» (ревью G8, m6). readOnly — карточка
// перечитывается на новом языке: ввод остаётся, отправка недоступна (m18).
export function CityNamesCard({
  city,
  canEdit,
  readOnly,
}: {
  city: GeoCityDetails;
  canEdit: boolean;
  readOnly?: boolean;
}) {
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
  return <NamesForm city={city} readOnly={readOnly} />;
}

function NamesForm({ city, readOnly }: { city: GeoCityDetails; readOnly?: boolean }) {
  const t = useTranslations('geo.city.names');
  const ta = useTranslations('actions');
  const toast = useToast();
  const values: Values = {
    names: Object.fromEntries(LOCALES.map((l) => [l, city.names[l] ?? ''])),
  };
  const form = useForm<Values>({ values, resetOptions: { keepDirtyValues: true } });
  const save = useUpdateCity(city.id, ['geo', 'city', city.id, 'names']);
  const remove = useUpdateCity(city.id, ['geo', 'city', city.id, 'names', 'delete']);
  const [deleting, setDeleting] = useState<Locale | null>(null);
  // Перевод удалён — кнопки «Удалить перевод» (открывателя окна) больше нет, и фокус упал бы на body
  // (ревью G8, m4): ставим его на поле этого языка. Окно закрывается и кнопка исчезает в разных
  // отрисовках (ответ в кэше — отдельным уведомлением): эффект ждёт и того, и другого. Цель гасится, как
  // только применена, и в начале нового действия: иначе следующее обновление карточки при фокусе на body
  // снова увело бы фокус на поле давно удалённого перевода (ревью G8, n2).
  // Цель — в ref (гасится без лишней отрисовки), deletedAt — повод перепроверить после удаления.
  const deleted = useRef<Locale | null>(null);
  const [deletedAt, setDeletedAt] = useState(0);
  useEffect(() => {
    const locale = deleted.current;
    if (!locale || city.names[locale] !== undefined) return;
    const active = document.activeElement;
    if (!active || active === document.body) form.setFocus(`names.${locale}`);
    deleted.current = null;
  }, [deletedAt, form, city.names]);
  // Стёртые при «Сохранить» переводы: поле вернулось к сохранённому, под ним — «удалить — кнопкой».
  const [restored, setRestored] = useState<Locale[]>([]);

  const submit = async (v: Values) => {
    if (readOnly) return;
    deleted.current = null;
    const cleared = LOCALES.filter((l) => v.names[l] === '' && city.names[l] !== undefined);
    for (const l of cleared)
      form.setValue(`names.${l}`, city.names[l] ?? '', { shouldDirty: true });
    setRestored(cleared);
    const changed = Object.fromEntries(
      LOCALES.flatMap((l) => {
        const next = v.names[l] ?? '';
        return next !== '' && next !== (city.names[l] ?? '') ? [[l, next]] : [];
      }),
    );
    if (Object.keys(changed).length === 0) return;
    const updated = await save.mutateAsync({ names: changed });
    // форма — к ответу сервера: сохранённое больше не «грязное» и не перекрывает будущие данные (раунд 1, I1)
    form.reset(
      { names: Object.fromEntries(LOCALES.map((l) => [l, updated.names[l] ?? ''])) },
      // resetOptions формы (keepDirtyValues) здесь не нужны: сохранённое — уже не ввод
      { keepDirtyValues: false },
    );
    toast.success(t('saved'));
  };

  const confirmDelete = () => {
    if (!deleting) return;
    const locale = deleting;
    deleted.current = null;
    remove.mutate(
      { names: { [deleting]: null } },
      {
        onSuccess: () => {
          // удалённый перевод — пустое несохранённое значение, а не прежний ввод (раунд 1, I1)
          form.resetField(`names.${locale}`, { defaultValue: '' });
          toast.success(t('deleted'));
          setDeleting(null);
          deleted.current = locale;
          setDeletedAt((n) => n + 1);
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
                hint={restored.includes(l) ? t('delete_hint') : undefined}
                rules={canonical ? { required: true, maxLength: 100 } : { maxLength: 100 }}
              />
              {!canonical && city.names[l] !== undefined && (
                <Button
                  variant="danger-outline"
                  disabled={readOnly}
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
          <Button type="submit" loading={save.isPending} disabled={readOnly}>
            {ta('save')}
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
        confirmLabel={ta('delete')}
        loading={remove.isPending}
        error={remove.error}
      />
    </>
  );
}
