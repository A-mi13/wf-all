import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { Button, Modal, Skeleton } from '../../ui';
import styles from './flags.module.css';

type Method = components['schemas']['IdentityAuthMethodKind'];

const USAGE_KEY = ['identity', 'auth-methods', 'usage'] as const;

// Выключение аварийного флага входа (спека identity §7.5; план I-4, Task 9): перед подтверждением — сколько игроков
// входят только этим способом (getAuthMethodsUsage; им останется «Забыли пароль?»). Статистика — агрегаты без ПД;
// читается при каждом открытии окна, без повторов. Ошибка статистики — текст и «Повторить» без числа, «Выключить»
// доступно: авария важнее числа. Открытое окно — единственное подтверждение (на пальце шторки нет).
export function EmergencyDialog({
  open,
  title,
  method,
  onClose,
  onConfirm,
}: {
  open: boolean;
  title: string;
  method: Method | undefined;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const t = useTranslations('flagsPage');
  const ta = useTranslations('actions');
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      description={t('emergency.text')}
      tone="danger"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {ta('cancel')}
          </Button>
          <Button variant="danger" onClick={onConfirm}>
            {t('confirm.off')}
          </Button>
        </>
      }
    >
      {method && <UsageLine method={method} />}
    </Modal>
  );
}

function UsageLine({ method }: { method: Method }) {
  const t = useTranslations('flagsPage.emergency');
  const ta = useTranslations('actions');
  const api = useApi();
  const usage = useQuery({
    queryKey: USAGE_KEY,
    queryFn: () => unwrap(api.GET('/v1/identity/auth-methods/usage')),
    staleTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
  if (usage.isPending) return <Skeleton.Text lines={1} />;
  if (usage.isError)
    return (
      <div className={styles.usage}>
        <p role="alert" className={styles.warning}>
          {t('error')}
        </p>
        <Button variant="secondary" loading={usage.isFetching} onClick={() => void usage.refetch()}>
          {ta('retry')}
        </Button>
      </div>
    );
  // нет строки способа (сервер старше или новее админки) — число неизвестно, а не «0 игроков»: окно не должно
  // утверждать, что выключение никого не заденет. Число приходит после открытия окна — живая строка (role=status).
  const row = usage.data.items.find((i) => i.method === method);
  return (
    <p role="status" className={styles.usageText}>
      {row ? t('count', { count: row.only_method }) : t('unknown')}
    </p>
  );
}
