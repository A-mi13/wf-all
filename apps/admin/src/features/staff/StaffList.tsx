import { useInfiniteQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { Me } from '../../app/permissions';
import { paths } from '../../app/paths';
import { useDateTime } from '../../i18n/format';
import {
  Avatar,
  Button,
  type ChipTone,
  type Column,
  DataTable,
  Form,
  infiniteTableProps,
  SelectField,
  StatusChip,
} from '../../ui';
import { ACCOUNTS_KEY, useRoleLabel } from './queries';
import { hasActions, ROLES, type StaffAccount, staffActions } from './roles';
import { type StaffSearch, STATUSES } from './search';
import { useStaffStatus } from './status';
import styles from './staff.module.css';

const ROLE_TONE: Record<string, ChipTone> = {
  owner: 'success',
  admin: 'accent',
  moderator: 'neutral',
  support: 'neutral',
};

// «Кто имеет доступ» (спека §9.4 п. 5, §4.9; макет 07): DataTable по listStaffAccounts — поиск,
// чипы статуса, роль в «Ещё фильтры», курсор «Показать ещё». Фильтры — search-параметры маршрута
// (search), экран их только читает и меняет навигацией. Своя строка — «это вы» без действий
// (staff.self_action); «Управлять» — только если над этой учёткой есть хоть одно действие (§2.5).
export function StaffList({
  me,
  search,
  onManage,
  activeId,
}: {
  me: Me;
  search: StaffSearch;
  onManage: (row: StaffAccount) => void;
  activeId?: string;
}) {
  const t = useTranslations('staff');
  const api = useApi();
  const navigate = useNavigate();
  const roleLabel = useRoleLabel();
  const dateTime = useDateTime();
  const { q, status, role } = search;
  const statusOf = useStaffStatus();

  const list = useInfiniteQuery({
    queryKey: [...ACCOUNTS_KEY, 'list', { q, status, role }],
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/v1/staff/accounts', {
          params: { query: { q, status, role, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (p) => p.next_cursor ?? undefined,
  });

  // Фильтры — в адрес: replace, чтобы «Назад» не листал каждую букву поиска.
  const setSearch = (next: StaffSearch) =>
    void navigate({ to: paths.staff, search: next, replace: true });
  const update = (patch: Partial<StaffSearch>) => {
    const next: StaffSearch = { ...search, ...patch };
    for (const k of ['q', 'status', 'role'] as const) if (!next[k]) delete next[k];
    setSearch(next);
  };

  // Роль — поле «Ещё фильтры». Значение формы — из адреса; изменение сразу уходит в адрес.
  const roleForm = useForm<{ role: string }>({ values: { role: role ?? '' } });

  const columns: Column<StaffAccount>[] = [
    {
      id: 'person',
      header: t('list.columns.person'),
      cell: (a) => (
        <span className={styles.person}>
          <Avatar name={a.display_name} />
          <span className={styles.personText}>
            <span className={styles.name}>{a.display_name}</span>
            {/* почта как хранится: IDN-домен — в A-форме xn--… (Task 5), не декодируется */}
            <span className={styles.email}>{a.email}</span>
          </span>
        </span>
      ),
    },
    {
      id: 'role',
      header: t('list.columns.role'),
      width: 'md',
      cell: (a) => (
        <span className={styles.chips}>
          <StatusChip tone={ROLE_TONE[a.role] ?? 'neutral'}>{roleLabel(a.role)}</StatusChip>
          {a.id === me.id && <StatusChip tone="accent">{t('list.you')}</StatusChip>}
        </span>
      ),
    },
    {
      id: 'status',
      header: t('list.columns.status'),
      width: 'md',
      cell: (a) => {
        const s = statusOf(a);
        return <StatusChip tone={s.tone}>{s.text}</StatusChip>;
      },
    },
    {
      id: 'last_login',
      header: t('list.columns.last_login'),
      width: 'md',
      cell: (a) =>
        a.last_login_at ? (
          dateTime(a.last_login_at)
        ) : (
          <span className={styles.muted}>{t('list.never')}</span>
        ),
    },
    {
      id: 'actions',
      header: t('list.columns.actions'),
      width: 'sm',
      align: 'end',
      cell: (a) =>
        hasActions(staffActions(me, a)) ? (
          <Button
            variant="secondary"
            aria-label={t('list.manage_label', { name: a.display_name })}
            onClick={() => onManage(a)}
          >
            {t('list.manage')}
          </Button>
        ) : null,
    },
  ];

  const dirty = Boolean(q || status || role);
  return (
    <DataTable
      label={t('list.label')}
      columns={columns}
      rowKey={(a) => a.id}
      activeRowKey={activeId}
      {...infiniteTableProps(list)}
      search={{
        value: q ?? '',
        onChange: (v) => update({ q: v.trim() ? v : undefined }),
        placeholder: t('list.search'),
      }}
      chips={{
        label: t('list.status_filter'),
        value: status ?? '',
        options: [
          { value: '', label: t('list.status_all') },
          ...STATUSES.map((s) => ({ value: s, label: t(`list.statuses.${s}`) })),
        ],
        onChange: (v) => update({ status: STATUSES.find((s) => s === v) }),
      }}
      filters={{
        active: role ? 1 : 0,
        content: (
          <Form form={roleForm} onSubmit={() => {}}>
            <SelectField
              name="role"
              label={t('list.role_filter')}
              options={[
                { value: '', label: t('list.role_all') },
                ...ROLES.map((r) => ({ value: r, label: roleLabel(r) })),
              ]}
              rules={{
                onChange: (e: { target: { value: string } }) =>
                  update({ role: ROLES.find((r) => r === e.target.value) }),
              }}
            />
          </Form>
        ),
      }}
      dirty={dirty}
      onReset={() => setSearch({})}
    />
  );
}
