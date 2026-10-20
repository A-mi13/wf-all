import { useIsMutating } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError, isUnknownOutcome, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import { Button, Form, Modal, TextField, useErrorText, useToast } from '../../ui';
import styles from './approvals.module.css';
import { useApprovalStatus } from './labels';
import { useFindApproval } from './queries';

type Approval = components['schemas']['Approval'];

// Предел комментария — ApprovalReject.comment (minLength 1, maxLength 500).
const COMMENT_MAX = 500;
const REJECT_KEY = ['approvals', 'reject'] as const;

// «Отклонить» (спека §4.8): окно с обязательным комментарием — его увидит инициатор, он попадёт в
// журнал. Пустой или из одних пробелов — ошибка поля до запроса (сервер обрезает края и ответил бы
// 400). Ответ 200 — «отклонён»; запрос уже не тот (platform.approval_*: решён, отменён, просрочен, нет
// его, свой) — тост, окно закрыто, списки перечитаны; прочее — ошибка в окне, комментарий цел. Исход
// неизвестен (isUnknownOutcome, уборка №8 m4) — решение не повторяется: запрос перечитывается
// («Входящие», все состояния); решён — тост по итогу, окно закрыто; всё ещё ждёт или не нашли —
// ошибка в окне.
export function RejectDialog({
  approval,
  onClose,
  onDone,
}: {
  approval: Approval | null;
  onClose: () => void;
  // решение отправлено: ответ (отклонён) — строка на месте с итогом; ошибка — всё перечитано
  onDone: (result?: Approval) => Promise<void>;
}) {
  const t = useTranslations('approvals.reject_dialog');
  const busy = useIsMutating({ mutationKey: REJECT_KEY }) > 0;
  return (
    <Modal
      open={approval !== null}
      onClose={onClose}
      title={t('title')}
      description={t('text')}
      dismissible={!busy}
    >
      {approval && <RejectForm approval={approval} onClose={onClose} onDone={onDone} />}
    </Modal>
  );
}

function RejectForm({
  approval,
  onClose,
  onDone,
}: {
  approval: Approval;
  onClose: () => void;
  onDone: (result?: Approval) => Promise<void>;
}) {
  const t = useTranslations('approvals');
  const api = useApi();
  const toast = useToast();
  const errorText = useErrorText();
  const statusOf = useApprovalStatus();
  const findIncoming = useFindApproval('incoming');
  const form = useForm<{ comment: string }>({ defaultValues: { comment: '' } });
  const reject = useAdminMutation(
    (v: { comment: string }, key: string) =>
      unwrap(
        api.POST('/v1/approvals/{approvalId}/reject', {
          params: { path: { approvalId: approval.id }, header: { 'Idempotency-Key': key } },
          body: { comment: v.comment.trim() },
        }),
      ),
    {
      mutationKey: REJECT_KEY,
      onSuccess: () => {
        toast.success(t('done.rejected'));
        onClose();
      },
    },
  );

  // Ответ — итог на месте (onDone(result)); ошибка — всё перечитывается (onDone()); исход неизвестен —
  // итог перечитанного запроса.
  const submit = async (v: { comment: string }) => {
    let result: Approval;
    try {
      result = await reject.mutateAsync(v);
    } catch (e) {
      const found = isUnknownOutcome(e)
        ? await findIncoming(approval).catch(() => undefined)
        : undefined;
      if (!found || found.status === 'pending') {
        await onDone(found);
        throw e;
      }
      if (found.status === 'rejected') toast.success(t('done.rejected'));
      else toast.info(t('done.other', { status: statusOf(found.status).text }));
      onClose();
      await onDone(found);
      return;
    }
    await onDone(result);
  };

  const onError = (e: ApiError) => {
    if (!e.code.startsWith('platform.approval_')) return false;
    toast.error(errorText(e));
    onClose();
    return true;
  };

  return (
    <Form form={form} onSubmit={submit} onError={onError}>
      <TextField
        name="comment"
        label={t('reject_dialog.comment')}
        autoComplete="off"
        maxLength={COMMENT_MAX}
        autoFocus
        rules={{
          maxLength: COMMENT_MAX,
          validate: { required: (v: string) => v.trim() !== '' },
        }}
      />
      <div className={styles.actions}>
        <Button type="submit" variant="danger" loading={reject.isPending}>
          {t('reject_dialog.submit')}
        </Button>
      </div>
    </Form>
  );
}
