import { useSearch } from '@tanstack/react-router';
import { useId, useState } from 'react';
import { useTranslations } from 'use-intl';
import { can } from '../../app/permissions';
import { useSession } from '../../app/session';
import { Button, PageHeader } from '../../ui';
import { InviteDrawer } from './InviteDrawer';
import { RolesMatrix, TwoHandsNote } from './RolesMatrix';
import { grantableRoles, type StaffAccount } from './roles';
import { parseStaffSearch } from './search';
import { StaffDrawer } from './StaffDrawer';
import { StaffList } from './StaffList';
import styles from './staff.module.css';

// «Роли и доступ» (спека §9.4 п. 5, §9.3; макет 07; право staff.roles.read — проверяет каркас;
// только с компьютера — desktopOnly в NAV): матрица прав и «Правило двух рук» — из
// GET /v1/staff/roles; «Кто имеет доступ» — только с staff.accounts.read; «Пригласить» — если
// сотрудник может выдать хоть одну роль; строка сотрудника — панель действий.
export function StaffPage() {
  const t = useTranslations();
  const session = useSession();
  const search = parseStaffSearch(useSearch({ strict: false }));
  const listId = useId();
  const [inviting, setInviting] = useState(false);
  const [target, setTarget] = useState<StaffAccount | null>(null);
  if (session.status !== 'active') return null;
  const me = session.me;
  const canInvite = grantableRoles(me).length > 0;

  return (
    <div className={styles.page}>
      <PageHeader
        title={t('nav.staff')}
        subtitle={t('staff.subtitle')}
        actions={
          canInvite && <Button onClick={() => setInviting(true)}>{t('staff.invite.open')}</Button>
        }
      />
      <div className={styles.top}>
        <div className={styles.matrix}>
          <RolesMatrix />
        </div>
        <TwoHandsNote />
      </div>
      {can(me, 'staff.accounts.read') && (
        <section aria-labelledby={listId} className={styles.section}>
          <h2 id={listId} className={styles.sectionTitle}>
            {t('staff.list.title')}
          </h2>
          <StaffList me={me} search={search} onManage={setTarget} activeId={target?.id} />
        </section>
      )}
      {canInvite && <InviteDrawer me={me} open={inviting} onClose={() => setInviting(false)} />}
      <StaffDrawer me={me} target={target} onClose={() => setTarget(null)} />
    </div>
  );
}
