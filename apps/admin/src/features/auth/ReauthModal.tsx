import { useEffect, useSyncExternalStore } from 'react';
import { useTranslations } from 'use-intl';
import { useApi } from '../../api/context';
import { useLogout } from '../../app/logout';
import { reauthBus } from '../../app/reauth';
import { useSession } from '../../app/session';
import { Button, Modal } from '../../ui';
import { FactorForm } from './FactorForm';

// Окно разблокировки сессии (спека §9.3, P20). Открывается, пока ждёт reauthBus: 401
// auth.reauth_required любого запроса (и фонового опроса) или me.reauth_required после
// перезагрузки (SessionProvider). Модалка поверх текущей страницы — формы под ней не
// размонтируются. Код или резервный код → staffUnlock → 204 (новая cookie, D9) → resolve():
// ждущие запросы повторяются тем же клоном (тот же Idempotency-Key). Закрыть без разблокировки
// нельзя; «Выйти» — выход каркаса (вход без return); блокировка, конец сессии — reject(), ждущие
// запросы падают исходной ошибкой, me перечитан → экран входа.
export function ReauthModal() {
  const t = useTranslations('auth.reauth');
  const api = useApi();
  const session = useSession();
  const open = useSyncExternalStore(reauthBus.subscribe, reauthBus.pending, () => false);

  // Сессии больше нет (истекла, погашена) — разблокировки не будет.
  const anonymous = session.status === 'anonymous';
  useEffect(() => {
    if (open && anonymous) reauthBus.reject();
  }, [open, anonymous]);

  const logout = useLogout({ onSignedOut: () => reauthBus.reject() });

  return (
    <Modal
      open={open}
      onClose={() => {}}
      dismissible={false}
      title={t('title')}
      description={t('text')}
      footer={
        <Button variant="link" loading={logout.isPending} onClick={() => logout.mutate()}>
          {t('logout')}
        </Button>
      }
    >
      <FactorForm
        send={(factor, key) =>
          api.POST('/v1/staff/auth/unlock', {
            params: { header: { 'Idempotency-Key': key } },
            body: factor,
          })
        }
        submitLabel={t('submit')}
        onSuccess={() => reauthBus.resolve()}
        // 401 auth.unauthenticated: me перечитывает кэш запросов (query.ts), эффект выше — тоже
        onExpired={() => reauthBus.reject()}
        // 409: сессия уже не ждёт разблокировки (другая вкладка успела — cookie общая) — запросы
        // можно повторять. Две вкладки одним кодом одновременно: вторая получает staff.mfa_invalid
        // без счёта неудачи — обычный текст «Код не подошёл».
        onWrongStage={() => {
          reauthBus.resolve();
          void session.refresh();
        }}
        onLocked={() => {
          reauthBus.reject();
          void session.refresh();
        }}
      />
    </Modal>
  );
}
