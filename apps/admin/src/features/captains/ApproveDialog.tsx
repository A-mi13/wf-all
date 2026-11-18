import { useEffect, useId, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { ApiError } from '../../api/client';
import { Button, Form, Modal, TextField, useToast } from '../../ui';
import { type ApproveVars, useApproveApplication } from './mutations';

// Предел комментария — IdentityCaptainApprove.comment (minLength 1, maxLength 1000).
export const COMMENT_MAX = 1000;

// «Одобрить» (спека identity §7.4): окно с необязательным комментарием — его увидит игрок в письме. Ответ 200 —
// тост, окно закрыто, раздел перечитан (useApproveApplication). Особые отказы (заявка закрыта, условия не
// выполнены, нет заявки) разбирает карточка — onError возвращает true; прочие (self_action, сеть) — текст в
// окне, введённое цело, повтор с тем же комментарием после неизвестного исхода — тем же ключом.
export function ApproveDialog({
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
  const approve = useApproveApplication(id);
  const formId = useId();
  const [dirty, setDirty] = useState(false);
  const submit = async (v: ApproveVars) => {
    await approve.mutateAsync(v);
    toast.success(t('toasts.approved'));
    onClose();
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('approve.title')}
      description={t('approve.text')}
      dismissible={!approve.isPending}
      dirty={dirty && !approve.isPending}
      footer={
        <Button type="submit" form={formId} loading={approve.isPending}>
          {t('approve.submit')}
        </Button>
      }
    >
      <ApproveForm formId={formId} onSubmit={submit} onError={onError} onDirty={setDirty} />
    </Modal>
  );
}

function ApproveForm({
  formId,
  onSubmit,
  onError,
  onDirty,
}: {
  formId: string;
  onSubmit: (v: ApproveVars) => Promise<void>;
  onError: (e: ApiError) => boolean;
  onDirty: (dirty: boolean) => void;
}) {
  const t = useTranslations('captains.approve');
  const form = useForm<ApproveVars>({ defaultValues: { comment: '' } });
  const { isDirty } = form.formState;
  useEffect(() => {
    onDirty(isDirty);
    return () => onDirty(false);
  }, [isDirty, onDirty]);
  return (
    <Form id={formId} form={form} onSubmit={onSubmit} onError={onError}>
      <TextField
        name="comment"
        label={t('comment')}
        autoComplete="off"
        maxLength={COMMENT_MAX}
        autoFocus
        rules={{ maxLength: COMMENT_MAX }}
      />
    </Form>
  );
}
