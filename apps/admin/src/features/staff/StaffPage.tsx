import { useSearch } from '@tanstack/react-router';
import { useId, useState } from 'react';
import { useTranslations } from 'use-intl';
import { isDesktopOnly } from '../../app/desktopOnly';
import { usePanelParam } from '../../app/panels';
import { can } from '../../app/permissions';
import { useSession } from '../../app/session';
import { ActionGate, Button, PageHeader, useIsHandheld } from '../../ui';
import { InviteDrawer } from './InviteDrawer';
import { RolesMatrix, TwoHandsNote } from './RolesMatrix';
import { grantableRoles, INVITE_PERMISSIONS, type StaffAccount } from './roles';
import { parseStaffSearch } from './search';
import { StaffDrawer } from './StaffDrawer';
import { StaffList } from './StaffList';
import styles from './staff.module.css';

// «Роли и доступ» (спека §9.4 п. 5, §9.3; макет 07; право staff.roles.read — проверяет каркас; на
// телефоне — просмотр: выдача и снятие прав за ActionGate, спека UI-фундамента §3.1–§3.2): матрица прав и «Правило двух рук» — из
// GET /v1/staff/roles; «Кто имеет доступ» — только с staff.accounts.read; «Пригласить» — если
// сотрудник может выдать хоть одну роль; строка сотрудника — панель действий. Обе панели — в адресе (спека
// UI-фундамента §4.6): ?invite=1 и ?account=<id>; ключ без права (выдачи — или она только с компьютера на
// карманном устройстве; staff.accounts.read) снимается.
export function StaffPage() {
  const t = useTranslations();
  const session = useSession();
  const search = parseStaffSearch(useSearch({ strict: false }));
  const listId = useId();
  const handheld = useIsHandheld();
  const me = session.status === 'active' ? session.me : null;
  const canInvite = me !== null && grantableRoles(me).length > 0;
  const canList = me !== null && can(me, 'staff.accounts.read');
  const invite = usePanelParam('invite', {
    allowed: canInvite && !(handheld && isDesktopOnly(INVITE_PERMISSIONS)),
  });
  const account = usePanelParam('account', { allowed: canList });
  // строка, по которой открыли панель, — её данные до ответа карточки (по глубокой ссылке строки нет)
  const [picked, setPicked] = useState<StaffAccount | null>(null);
  if (!me) return null;

  return (
    <div className={styles.page}>
      <PageHeader
        title={t('nav.staff')}
        subtitle={t('staff.subtitle')}
        actions={
          canInvite && (
            <ActionGate permission={INVITE_PERMISSIONS} compact>
              <Button onClick={() => invite.open('1')}>{t('staff.invite.open')}</Button>
            </ActionGate>
          )
        }
      />
      <div className={styles.top}>
        <div className={styles.matrix}>
          <RolesMatrix />
        </div>
        <TwoHandsNote />
      </div>
      {canList && (
        <section aria-labelledby={listId} className={styles.section}>
          <h2 id={listId} className={styles.sectionTitle}>
            {t('staff.list.title')}
          </h2>
          <StaffList
            me={me}
            search={search}
            onManage={(a) => {
              setPicked(a);
              account.open(a.id);
            }}
            activeId={account.value}
          />
        </section>
      )}
      {canInvite && (
        <InviteDrawer me={me} open={invite.value !== undefined} onClose={invite.close} />
      )}
      <StaffDrawer
        me={me}
        id={account.value}
        initial={picked && picked.id === account.value ? picked : undefined}
        onClose={account.close}
      />
    </div>
  );
}
