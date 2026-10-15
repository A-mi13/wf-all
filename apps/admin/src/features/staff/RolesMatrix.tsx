import { useId } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { type Column, DataTable } from '../../ui';
import { usePermissionLabel, useRoleLabel, useStaffRoles } from './queries';
import { twoHandsRules } from './roles';
import styles from './staff.module.css';

type RolePermission = components['schemas']['RolePermission'];
type Grant = components['schemas']['RoleGrant'];

// Значение клетки из контракта; незнакомое (enum открытый) — «—».
const isGrant = (g: unknown): g is Grant =>
  g === 'allow' || g === 'scoped' || g === 'dual' || g === 'deny';

const GRANT_STYLE: Record<Grant, string | undefined> = {
  allow: styles.allow,
  scoped: styles.scoped,
  dual: styles.dual,
  deny: styles.deny,
};

// Матрица прав (спека §9.4 п. 5, макет 07): строки — права в порядке каталога, столбцы — роли по
// старшинству, клетка — allow / scoped / dual / deny. Всё — из GET /v1/staff/roles; подписи прав —
// permissions.<ключ>, ролей — roles.<роль>. Незнакомые роль или значение клетки (enum открытый) —
// «сотрудник» и «—», а не падение.
export function RolesMatrix() {
  const t = useTranslations('staff.matrix');
  const q = useStaffRoles();
  const roleLabel = useRoleLabel();
  const permissionLabel = usePermissionLabel();
  const grantText = (g: unknown) => (isGrant(g) ? t(`grants.${g}`) : t('grants.unknown'));

  const roles = q.data?.roles ?? [];
  const columns: Column<RolePermission>[] = [
    { id: 'permission', header: t('permission'), cell: (p) => permissionLabel(p.key) },
    ...roles.map((r): Column<RolePermission> => ({
      id: r,
      header: roleLabel(r),
      width: 'sm',
      cell: (p) => {
        // у незнакомой роли клетки нет — undefined
        const g: unknown = p.grants[r];
        return (
          <span className={`${styles.grant} ${isGrant(g) ? GRANT_STYLE[g] : styles.deny}`}>
            {grantText(g)}
          </span>
        );
      },
    })),
  ];

  return (
    <DataTable
      label={t('label')}
      columns={columns}
      rowKey={(p) => p.key}
      rows={q.data?.permissions}
      error={q.data ? undefined : (q.error ?? undefined)}
      onRetry={() => void q.refetch()}
      retrying={q.isFetching}
    />
  );
}

// «Правило двух рук» — по матрице (D23): права, у которых хотя бы одна роль только вдвоём, с этими
// ролями. Пока матрица не пришла — блока нет (ошибку показывает таблица рядом).
export function TwoHandsNote() {
  const t = useTranslations('staff.two_hands');
  const titleId = useId();
  const q = useStaffRoles();
  const roleLabel = useRoleLabel();
  const permissionLabel = usePermissionLabel();
  if (!q.data) return null;
  const rules = twoHandsRules(q.data);
  return (
    <section aria-labelledby={titleId} className={styles.twoHands}>
      <h2 id={titleId} className={styles.twoHandsTitle}>
        {t('title')}
      </h2>
      <p className={styles.twoHandsText}>{rules.length > 0 ? t('text') : t('none')}</p>
      {rules.length > 0 && (
        <ul aria-labelledby={titleId} className={styles.rules}>
          {rules.map((r) => (
            <li key={r.key}>
              {t('item', {
                permission: permissionLabel(r.key),
                roles: r.roles.map(roleLabel).join(', '),
              })}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
