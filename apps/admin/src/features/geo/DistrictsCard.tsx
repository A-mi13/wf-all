import { useEffect, useRef, useState } from 'react';
import { type UseFormReturn, useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { ApiError } from '../../api/client';
import type { components } from '../../api/gen/admin';
import {
  Button,
  type Column,
  DataTable,
  EmptyState,
  Form,
  Modal,
  StatusChip,
  TextField,
  useErrorText,
  useToast,
} from '../../ui';
import styles from './geo.module.css';
import { useCreateDistrict, useUpdateDistrict } from './mutations';
import { useGeoDistricts } from './queries';

type GeoDistrict = components['schemas']['GeoDistrict'];

// Районы города (*; geo.districts.manage в городе): таблица — действующие по имени, затем архивные
// с чипом (порядок даёт сервер); «Добавить район», «Переименовать» (окно), «В архив» / «Вернуть».
// 409 geo.district_name_taken — у поля (добавление, переименование) или тостом (возврат из архива:
// поля нет). Пока действие над строкой идёт, кнопки строк заняты. Без права — только чтение.
export function DistrictsCard({ cityId, canEdit }: { cityId: string; canEdit: boolean }) {
  const t = useTranslations('geo.districts');
  const ta = useTranslations('actions');
  const toast = useToast();
  const errorText = useErrorText();
  const list = useGeoDistricts(cityId);
  const update = useUpdateDistrict(cityId);
  const [pendingId, setPendingId] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<GeoDistrict | null>(null);
  // Архив и возврат меняют порядок строк (архивные — внизу): React переставляет строку в DOM, и в
  // браузере фокус с её кнопки слетает на body (ревью G8, m4). После ответа — на кнопку этой строки;
  // переименование, меняющее порядок (n3), — возврат фокуса Overlay на тот же узел-открыватель «Переименовать»
  // (строка переезжает до закрытия окна: мутация ждёт перечитывания районов). Цель одноразовая: как только
  // строка на месте, фокус ставится (если он на body) и цель гаснет — и в начале нового действия; иначе
  // следующее обновление списка увело бы фокус на давно обработанную строку (n2, раунд 1 minor 1).
  const root = useRef<HTMLDivElement>(null);
  const [refocus, setRefocus] = useState<string | null>(null);
  useEffect(() => {
    if (!refocus) return;
    const target = root.current?.querySelector<HTMLElement>(`[data-district-action="${refocus}"]`);
    if (!target) return;
    const active = document.activeElement;
    if (!active || active === document.body) target.focus();
    setRefocus(null);
  }, [refocus, list.data]);

  const setArchived = (d: GeoDistrict, archived: boolean) => {
    setRefocus(null);
    setPendingId(d.id);
    update.mutate(
      { id: d.id, patch: { archived } },
      {
        onSuccess: () => {
          toast.success(t(archived ? 'archived_toast' : 'restored_toast', { name: d.name }));
          setRefocus(d.id);
        },
        onError: (e) => toast.error(errorText(e)),
        onSettled: () => setPendingId(null),
      },
    );
  };

  const columns: Column<GeoDistrict>[] = [
    { id: 'name', header: t('columns.name'), cell: (d) => d.name },
    {
      id: 'state',
      header: t('columns.state'),
      width: 'md',
      cell: (d) =>
        d.archived_at === null ? (
          <StatusChip tone="success">{t('active')}</StatusChip>
        ) : (
          <StatusChip tone="neutral">{t('archived')}</StatusChip>
        ),
    },
  ];
  if (canEdit)
    columns.push({
      id: 'actions',
      header: t('columns.actions'),
      align: 'end',
      width: 'lg',
      cell: (d) => {
        const busy = pendingId !== null;
        return (
          <span className={styles.actions}>
            <Button
              variant="secondary"
              aria-label={t('rename_label', { name: d.name })}
              disabled={busy}
              onClick={() => {
                setRefocus(null);
                setRenaming(d);
              }}
            >
              {t('rename')}
            </Button>
            {d.archived_at === null ? (
              <Button
                variant="danger-outline"
                aria-label={t('archive_label', { name: d.name })}
                data-district-action={d.id}
                loading={pendingId === d.id}
                disabled={busy && pendingId !== d.id}
                onClick={() => setArchived(d, true)}
              >
                {ta('archive')}
              </Button>
            ) : (
              <Button
                variant="secondary"
                aria-label={t('restore_label', { name: d.name })}
                data-district-action={d.id}
                loading={pendingId === d.id}
                disabled={busy && pendingId !== d.id}
                onClick={() => setArchived(d, false)}
              >
                {ta('restore')}
              </Button>
            )}
          </span>
        );
      },
    });

  return (
    <div ref={root} className={styles.panel}>
      {canEdit && <AddDistrict cityId={cityId} />}
      <DataTable
        label={t('label')}
        columns={columns}
        rowKey={(d) => d.id}
        rows={list.data?.items}
        error={list.data ? undefined : (list.error ?? undefined)}
        onRetry={() => void list.refetch()}
        retrying={list.isFetching}
        summary={false}
        empty={<EmptyState title={t('empty_title')} text={t('empty_text')} />}
      />
      {canEdit && (
        <RenameDistrict cityId={cityId} district={renaming} onClose={() => setRenaming(null)} />
      )}
    </div>
  );
}

function useNameTaken() {
  const errorText = useErrorText();
  return (form: UseFormReturn<{ name: string }>) => (e: ApiError) => {
    if (e.code !== 'geo.district_name_taken') return false;
    form.setError('name', { type: 'server', message: errorText(e) }, { shouldFocus: true });
    return true;
  };
}

function AddDistrict({ cityId }: { cityId: string }) {
  const t = useTranslations('geo.districts');
  const toast = useToast();
  const form = useForm<{ name: string }>({ defaultValues: { name: '' } });
  const create = useCreateDistrict(cityId);
  const onError = useNameTaken()(form);
  const submit = async ({ name }: { name: string }) => {
    const d = await create.mutateAsync(name);
    toast.success(t('added', { name: d.name }));
    form.reset({ name: '' });
  };
  return (
    <Form form={form} onSubmit={submit} onError={onError}>
      <div className={styles.nameRow}>
        <TextField
          name="name"
          label={t('add_name')}
          autoComplete="off"
          rules={{ required: true, maxLength: 100 }}
        />
        <Button type="submit" loading={create.isPending}>
          {t('add')}
        </Button>
      </div>
    </Form>
  );
}

function RenameDistrict({
  cityId,
  district,
  onClose,
}: {
  cityId: string;
  district: GeoDistrict | null;
  onClose: () => void;
}) {
  const t = useTranslations('geo.districts');
  const update = useUpdateDistrict(cityId);
  return (
    <Modal
      open={district !== null}
      onClose={onClose}
      title={t('rename_title')}
      dismissible={!update.isPending}
    >
      {district && <RenameForm district={district} update={update} onClose={onClose} />}
    </Modal>
  );
}

function RenameForm({
  district,
  update,
  onClose,
}: {
  district: GeoDistrict;
  update: ReturnType<typeof useUpdateDistrict>;
  onClose: () => void;
}) {
  const t = useTranslations('geo.districts');
  const ta = useTranslations('actions');
  const toast = useToast();
  const form = useForm<{ name: string }>({ defaultValues: { name: district.name } });
  const onError = useNameTaken()(form);
  const submit = async ({ name }: { name: string }) => {
    if (name === district.name) return onClose();
    await update.mutateAsync({ id: district.id, patch: { name } });
    toast.success(t('renamed'));
    onClose();
  };
  return (
    <Form form={form} onSubmit={submit} onError={onError} className={styles.panel}>
      <TextField
        name="name"
        label={t('rename_name')}
        autoComplete="off"
        rules={{ required: true, maxLength: 100 }}
      />
      <div className={styles.row}>
        <Button type="submit" loading={update.isPending}>
          {ta('save')}
        </Button>
      </div>
    </Form>
  );
}
