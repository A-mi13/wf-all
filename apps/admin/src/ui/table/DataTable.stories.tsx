import { useState } from 'react';
import { useFormatter, useTranslations } from 'use-intl';
import { ApiError } from '../../api/client';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Avatar } from '../Avatar';
import { Button } from '../Button';
import { StatusChip } from '../StatusChip';
import { DataTable } from './DataTable';
import type { Column, DataTableProps } from './model';

// Таблица (базовые состояния; новые возможности — UF2b): строки с поиском и чипами, скелетон, пусто,
// ошибка с кодом, «Показать ещё»; на узкой ширине — строки-карточки (layout: три ширины).
export default { title: 'UI/DataTable', visual: true, layout: true } satisfies StoryMeta;

type Row = {
  id: string;
  name: string;
  email: string;
  role: 'owner' | 'admin' | 'moderator';
  status: 'active' | 'invited' | 'disabled';
  lastLogin: string | null;
};

// сотрудники — данные (как в фикстурах тестов), не тексты интерфейса
const ROWS: Row[] = [
  {
    id: '1',
    name: 'Ольга В.',
    email: 'olga.v@wf.example',
    role: 'owner',
    status: 'active',
    lastLogin: '2026-10-28T09:30:00Z',
  },
  {
    id: '2',
    name: 'Мария К.',
    email: 'maria.k@wf.example',
    role: 'admin',
    status: 'active',
    lastLogin: '2026-10-27T18:05:00Z',
  },
  {
    id: '3',
    name: 'Иван П.',
    email: 'ivan.p@stavropol.wf.example',
    role: 'moderator',
    status: 'invited',
    lastLogin: null,
  },
  {
    id: '4',
    name: 'Пётр С.',
    email: 'petr.s@wf.example',
    role: 'moderator',
    status: 'disabled',
    lastLogin: '2026-09-01T07:00:00Z',
  },
];

const TONE = { active: 'success', invited: 'warning', disabled: 'neutral' } as const;

function useColumns(): Column<Row>[] {
  const t = useTranslations();
  const format = useFormatter();
  return [
    {
      id: 'person',
      header: t('staff.list.columns.person'),
      cell: (r) => (
        <span className={styles.row}>
          <Avatar name={r.name} />
          <span>
            {r.name}
            <br />
            {r.email}
          </span>
        </span>
      ),
    },
    {
      id: 'role',
      header: t('staff.list.columns.role'),
      cell: (r) => t(`roles.${r.role}`),
      width: 'sm',
    },
    {
      id: 'status',
      header: t('staff.list.columns.status'),
      cell: (r) => <StatusChip tone={TONE[r.status]}>{t(`staff.status.${r.status}`)}</StatusChip>,
    },
    {
      id: 'last_login',
      header: t('staff.list.columns.last_login'),
      align: 'end',
      sortable: true,
      cell: (r) =>
        r.lastLogin
          ? format.dateTime(new Date(r.lastLogin), { dateStyle: 'medium', timeStyle: 'short' })
          : t('staff.list.never'),
    },
    {
      id: 'actions',
      header: t('staff.list.columns.actions'),
      cell: (r) => (
        <Button variant="link" aria-label={t('staff.list.manage_label', { name: r.name })}>
          {t('staff.list.manage')}
        </Button>
      ),
    },
  ];
}

function Table(props: Partial<DataTableProps<Row>>) {
  const t = useTranslations('staff.list');
  const columns = useColumns();
  const [q, setQ] = useState('');
  const [status, setStatus] = useState('');
  return (
    <div className={styles.frame}>
      <DataTable<Row>
        label={t('label')}
        columns={columns}
        rowKey={(r) => r.id}
        rows={ROWS}
        onRetry={() => undefined}
        search={{ value: q, onChange: setQ, placeholder: t('search') }}
        chips={{
          label: t('status_filter'),
          value: status,
          onChange: setStatus,
          options: [
            { value: '', label: t('status_all') },
            { value: 'active', label: t('statuses.active') },
            { value: 'invited', label: t('statuses.invited') },
            { value: 'disabled', label: t('statuses.disabled') },
          ],
        }}
        sort={{ column: 'last_login', direction: 'desc' }}
        onSortChange={() => undefined}
        {...props}
      />
    </div>
  );
}

export const Default: Story = () => <Table hasMore onLoadMore={() => undefined} />;
export const Loading: Story = () => <Table rows={undefined} />;
export const Empty: Story = () => <Table rows={[]} dirty onReset={() => undefined} />;
export const Error: Story = () => (
  <Table rows={undefined} error={new ApiError({ status: 503, code: 'internal' })} />
);
// «Столбцы» и плотность (UF2b.3): «Последний вход» — второстепенный (на 768–1023 скрыт по ширине, на телефоне —
// под «Подробнее»).
export const Columns: Story = () => {
  const columns = useColumns().map((c) =>
    c.id === 'last_login' ? { ...c, priority: 'low' as const } : c,
  );
  return <Table tableId="showcase" columns={columns} />;
};

// Закреплённые шапка и первый столбец (UF2b.2): широкая таблица своей прокруткой и на телефоне.
export const Pinned: Story = () => <Table layout="table" stickyHeader stickyFirstColumn />;

export const LoadMoreError: Story = () => (
  <Table hasMore onLoadMore={() => undefined} loadMoreError={new TypeError('Failed to fetch')} />
);
