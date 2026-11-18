import { useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslations } from 'use-intl';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useCan } from '../../app/session';
import { useDateTime } from '../../i18n/format';
import { Button, ConfirmDialog, FormError, Modal, useToast } from '../../ui';
import { LookupForm } from '../players/LookupForm';
import { usePlayerLookup } from '../players/queries';
import { ACCOUNT_ACTION_KEY, ACCOUNTS_KEY } from './queries';
import type { StaffAccount } from './roles';
import styles from './staff.module.css';

export const LINK_PERMISSION = 'staff.player_links.manage';

// Связь «сотрудник → учётка игрока» в карточке сотрудника (спека identity §7.4; план I-4, Task 9; R14, R15): только
// владелец (staff.player_links.manage — у остальных и поля player_link в ответе нет). Связь входа не даёт — служит
// запретом действий над собой (self_action). «Связать» — окно: почта и причина (поиск игрока — тот же аудит ПД, что
// в «Игроках», причина обязательна; почта и причина — только в памяти окна) → «Связать с <ник>?» → PUT; ошибки
// (staff.player_link_taken, staff.player_not_found) — текстом в окне. «Снять связь» — ConfirmDialog с ником →
// DELETE. Успех — перечитывание учёток (карточка, version). Общий ключ действий панели — пока запрос идёт, панель
// не закрывается. На телефоне доступно (не выдача прав, R27).
export function PlayerLinkBlock({ target }: { target: StaffAccount }) {
  const t = useTranslations('staff.player_link');
  const dateTime = useDateTime();
  const titleId = useId();
  const canManage = useCan(LINK_PERMISSION);
  const [linking, setLinking] = useState(false);
  const [unlinking, setUnlinking] = useState(false);
  const unlink = useLinkAction((_: void, key: string, api) =>
    api.DELETE('/v1/staff/accounts/{staffId}/player-link', {
      params: { path: { staffId: target.id }, header: { 'Idempotency-Key': key } },
    }),
  );
  const toast = useToast();
  if (!canManage) return null;
  const link = target.player_link;

  return (
    <section aria-labelledby={titleId} className={styles.block}>
      <h3 id={titleId} className={styles.blockTitle}>
        {t('title')}
      </h3>
      {link ? (
        <dl className={styles.facts}>
          <dt>{t('player')}</dt>
          <dd>{link.nickname}</dd>
          <dt>{t('linked_by')}</dt>
          <dd>
            {link.linked_by_label}, {dateTime(link.linked_at)}
          </dd>
        </dl>
      ) : (
        <p className={styles.text}>{t('none')}</p>
      )}
      <p className={styles.text}>{t('text')}</p>
      <div className={styles.row}>
        <Button variant="secondary" onClick={() => setLinking(true)}>
          {link ? t('relink') : t('link')}
        </Button>
        {link && (
          <Button
            variant="danger-outline"
            onClick={() => {
              unlink.reset();
              setUnlinking(true);
            }}
          >
            {t('unlink')}
          </Button>
        )}
      </div>
      {linking && <LinkDialog target={target} onClose={() => setLinking(false)} />}
      {link && (
        <ConfirmDialog
          open={unlinking}
          onClose={() => setUnlinking(false)}
          onConfirm={() =>
            unlink.mutate(undefined, {
              onSuccess: () => {
                setUnlinking(false);
                toast.success(t('unlinked'));
              },
            })
          }
          title={t('unlink_title', { name: target.display_name, nickname: link.nickname })}
          text={t('unlink_text')}
          confirmWord={link.nickname}
          confirmLabel={t('unlink')}
          loading={unlink.isPending}
          error={unlink.error}
        />
      )}
    </section>
  );
}

// Мутация связи: общий ключ действий панели сотрудника и перечитывание учёток после ответа (и после ошибки —
// данные могли устареть: связь поменял другой владелец).
function useLinkAction<V, R>(
  fn: (vars: V, key: string, api: ReturnType<typeof useApi>) => Promise<R>,
) {
  const api = useApi();
  const queryClient = useQueryClient();
  return useAdminMutation((v: V, key: string) => fn(v, key, api), {
    mutationKey: ACCOUNT_ACTION_KEY,
    onSettled: () => queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY }),
  });
}

function LinkDialog({ target, onClose }: { target: StaffAccount; onClose: () => void }) {
  const t = useTranslations('staff.player_link');
  const tp = useTranslations('players.lookup');
  const toast = useToast();
  const formId = useId();
  const lookup = usePlayerLookup(`staff-link:${target.id}`);
  // введены почта или причина, игрок ещё не найден — закрытие окна спрашивает (найден — терять нечего)
  const [dirty, setDirty] = useState(false);
  const found = lookup.asked && !lookup.busy ? lookup.query.data : undefined;
  const put = useLinkAction((userId: string, key: string, api) =>
    unwrap(
      api.PUT('/v1/staff/accounts/{staffId}/player-link', {
        params: { path: { staffId: target.id }, header: { 'Idempotency-Key': key } },
        body: { user_id: userId },
      }),
    ),
  );
  return (
    <Modal
      open
      onClose={onClose}
      title={t('dialog_title')}
      description={t('dialog_text')}
      dismissible={!put.isPending}
      dirty={dirty && !found}
      footer={
        found ? (
          <Button
            loading={put.isPending}
            onClick={() =>
              put.mutate(found.id, {
                onSuccess: () => {
                  onClose();
                  toast.success(t('linked', { nickname: found.nickname }));
                },
              })
            }
          >
            {t('link')}
          </Button>
        ) : (
          <Button type="submit" form={formId} loading={lookup.busy}>
            {tp('submit')}
          </Button>
        )
      }
    >
      {found ? (
        <div className={styles.panel}>
          <p className={styles.text}>
            {t('confirm', { name: target.display_name, nickname: found.nickname })}
          </p>
          <FormError error={put.error} />
        </div>
      ) : (
        <LookupForm lookup={lookup} formId={formId} footer onDirty={setDirty} />
      )}
    </Modal>
  );
}
