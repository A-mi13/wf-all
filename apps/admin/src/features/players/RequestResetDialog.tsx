import { useId } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { Button, Form, Modal, TextField } from '../../ui';
import { type ResetVars, useRequestMfaReset } from './mutations';

type Player = components['schemas']['IdentityPlayerLookup'];

// Пределы контракта IdentityMfaResetRequest: причина 3–500 символов, номер обращения — до 100.
const REASON_MIN = 3;
const REASON_MAX = 500;
const TICKET_MAX = 100;
const length = (v: string) => [...v.trim()].length;

// «Запросить сброс 2FA» (спека identity §7.4; план I-4, Task 9): причина — почему и как проверена личность
// (уходит в запрос на подтверждение и журнал; подсказка R41 — без ПД игрока), номер обращения необязателен.
// Всегда вдвоём: 202 → onRequested (пометка «на подтверждение» в карточке). Ошибки (409 mfa_not_enabled,
// mfa_reset_pending, self_action, 404) — текстом в окне, введённое цело. Пока запрос идёт, окно не закрывается;
// введённое — dirty окна (закрытие спрашивает).
export function RequestResetDialog({
  player,
  onClose,
  onRequested,
}: {
  player: Player;
  onClose: () => void;
  onRequested: () => void;
}) {
  const t = useTranslations('players.reset');
  const ta = useTranslations('actions');
  const formId = useId();
  const form = useForm<ResetVars>({ defaultValues: { reason: '', ticket: '' } });
  const request = useRequestMfaReset(player.id);
  return (
    <Modal
      open
      onClose={onClose}
      title={t('title', { name: player.nickname })}
      description={t('text')}
      dismissible={!request.isPending}
      dirty={form.formState.isDirty}
      footer={
        <Button type="submit" form={formId} loading={request.isPending}>
          {ta('submitForApproval')}
        </Button>
      }
    >
      <Form
        id={formId}
        form={form}
        onSubmit={async (v) => {
          await request.mutateAsync(v);
          onRequested();
        }}
      >
        <TextField
          name="reason"
          label={t('reason')}
          hint={t('reason_hint')}
          autoComplete="off"
          autoFocus
          rules={{
            validate: {
              minLength: (v: string) => length(v) >= REASON_MIN,
              maxLength: (v: string) => length(v) <= REASON_MAX,
            },
          }}
        />
        <TextField
          name="ticket"
          label={t('ticket')}
          autoComplete="off"
          spellCheck={false}
          rules={{ validate: { maxLength: (v: string) => length(v) <= TICKET_MAX } }}
        />
      </Form>
    </Modal>
  );
}
