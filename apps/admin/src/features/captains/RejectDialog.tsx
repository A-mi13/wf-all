import { useEffect, useId, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { ApiError } from '../../api/client';
import { Button, Form, Modal, SelectField, TextField, useToast } from '../../ui';
import { COMMENT_MAX } from './ApproveDialog';
import { REJECTION_CODES, useRejectionText } from './labels';
import { useRejectApplication } from './mutations';

type Values = { rejection_code: string; comment: string };

// «Отклонить» (спека identity §7.3–7.4): код причины из списка (подпись — captains.rejection.<код>) и комментарий
// для игрока; при «Другое» комментарий обязателен — пустой или из одних пробелов — ошибка поля до запроса (сервер
// ответил бы 422 по body.comment). Ответ 200 — тост, окно закрыто, раздел перечитан. Особые отказы разбирает
// карточка (onError → true), прочие (self_action, сеть) — текст в окне, введённое цело.
export function RejectDialog({
  id,
  open,
  onClose,
  onError,
}: {
  id: string;
  open: boolean;
  onClose: () => void;
  onError: (e: ApiError) => boolean;
}) {
  const t = useTranslations('captains');
  const toast = useToast();
  const reject = useRejectApplication(id);
  const formId = useId();
  const [dirty, setDirty] = useState(false);
  const submit = async (v: Values) => {
    const code = REJECTION_CODES.find((c) => c === v.rejection_code);
    // код обязателен правилом поля — без него форма сюда не дойдёт
    if (!code) return;
    await reject.mutateAsync({ rejection_code: code, comment: v.comment });
    toast.success(t('toasts.rejected'));
    onClose();
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('reject.title')}
      description={t('reject.text')}
      tone="danger"
      dismissible={!reject.isPending}
      dirty={dirty && !reject.isPending}
      footer={
        <Button type="submit" form={formId} variant="danger" loading={reject.isPending}>
          {t('reject.submit')}
        </Button>
      }
    >
      <RejectForm formId={formId} onSubmit={submit} onError={onError} onDirty={setDirty} />
    </Modal>
  );
}

function RejectForm({
  formId,
  onSubmit,
  onError,
  onDirty,
}: {
  formId: string;
  onSubmit: (v: Values) => Promise<void>;
  onError: (e: ApiError) => boolean;
  onDirty: (dirty: boolean) => void;
}) {
  const t = useTranslations('captains.reject');
  const rejection = useRejectionText();
  const form = useForm<Values>({ defaultValues: { rejection_code: '', comment: '' } });
  const { isDirty } = form.formState;
  useEffect(() => {
    onDirty(isDirty);
    return () => onDirty(false);
  }, [isDirty, onDirty]);
  return (
    <Form id={formId} form={form} onSubmit={onSubmit} onError={onError}>
      <SelectField
        name="rejection_code"
        label={t('code')}
        options={REJECTION_CODES.map((c) => ({ value: c, label: rejection(c) }))}
        rules={{ required: true }}
      />
      <TextField
        name="comment"
        label={t('comment')}
        autoComplete="off"
        maxLength={COMMENT_MAX}
        rules={{
          maxLength: COMMENT_MAX,
          // при «Другое» комментарий обязателен; пробелы — не комментарий
          validate: {
            required: (v: string) =>
              form.getValues('rejection_code') !== 'other' || v.trim() !== '',
          },
        }}
      />
    </Form>
  );
}
