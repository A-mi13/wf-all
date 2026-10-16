import { useInfiniteQuery } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTimeZone, useTranslations } from 'use-intl';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useDateTime } from '../../i18n/format';
import { Button, type Column, DataTable, infiniteTableProps, StatusChip } from '../../ui';
import { AuditFilters } from './AuditFilters';
import { sectionOf, tagTone, useAuditLabels } from './labels';
import { type AuditSearch, toAuditQuery } from './search';
import styles from './audit.module.css';

type AuditRecord = components['schemas']['AuditRecord'];

// Ключ журнала — фильтры уже в UTC: смена пояса (другой компьютер) — другой запрос.
const AUDIT_KEY = ['audit'] as const;

// Число заданных фильтров (без вкладки) — в имени кнопки «Ещё фильтры».
const FILTERS = ['action', 'actor', 'object_type', 'object_id', 'from', 'to'] as const;

// Таблица журнала (спека §5.3, макет 07): строка «время · кто · тег · действие · объект · id»,
// новые сверху (порядок сервера), курсор «Показать ещё». Модератор видит свои города и свои записи —
// сужает сервер, экран своих фильтров не добавляет. «Кто» — снимок actor_label (operator и system —
// подписями); имя сотрудника — кнопка: фильтр по нему. Раскрытие — было/стало (маскировано при
// записи; показывается как текст, без разметки), причина, сеть, браузер.
export function AuditView({
  search,
  onSearchChange,
}: {
  search: AuditSearch;
  onSearchChange: (next: AuditSearch) => void;
}) {
  const t = useTranslations('audit');
  const api = useApi();
  const timeZone = useTimeZone() ?? 'UTC';
  const dateTime = useDateTime();
  const labels = useAuditLabels();
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const query = toAuditQuery(search, timeZone);
  const list = useInfiniteQuery({
    queryKey: [...AUDIT_KEY, query],
    queryFn: ({ pageParam }) =>
      unwrap(api.GET('/v1/audit', { params: { query: { ...query, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (p) => p.next_cursor ?? undefined,
  });

  const toggle = (id: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  const columns: Column<AuditRecord>[] = [
    {
      id: 'time',
      header: t('table.columns.time'),
      width: 'md',
      cell: (r) => <span className={styles.time}>{dateTime(r.created_at)}</span>,
    },
    {
      id: 'who',
      header: t('table.columns.who'),
      width: 'md',
      cell: (r) => <Actor record={r} onFilter={(id) => onSearchChange({ ...search, actor: id })} />,
    },
    {
      id: 'tag',
      header: t('table.columns.tag'),
      width: 'sm',
      cell: (r) => (
        <StatusChip tone={tagTone(r.action)}>
          {t(`tags.${sectionOf(r.action) ?? 'other'}`)}
        </StatusChip>
      ),
    },
    {
      id: 'action',
      header: t('table.columns.action'),
      cell: (r) => <ActionCell record={r} open={open.has(r.id)} onToggle={() => toggle(r.id)} />,
    },
    {
      id: 'object',
      header: t('table.columns.object'),
      width: 'md',
      cell: (r) => (
        <span className={styles.stack}>
          <span>{labels.object(r.object_type)}</span>
          {r.object_id && <span className={styles.code}>{r.object_id.slice(0, 8)}</span>}
        </span>
      ),
    },
    {
      id: 'id',
      header: t('table.columns.id'),
      width: 'sm',
      cell: (r) => <span className={styles.code}>{r.id.slice(0, 8)}</span>,
    },
  ];

  const active = FILTERS.filter((k) => search[k] !== undefined).length;
  return (
    <DataTable
      label={t('table.label')}
      columns={columns}
      rowKey={(r) => r.id}
      {...infiniteTableProps(list)}
      filters={{
        active,
        content: (
          <AuditFilters
            search={search}
            onApply={(patch) => onSearchChange({ tab: search.tab, ...patch })}
          />
        ),
      }}
      dirty={active > 0}
      onReset={() => onSearchChange({ tab: search.tab })}
    />
  );
}

// «Кто»: сотрудник — кнопка-фильтр по нему; оператор сервера и платформа — подписями; без автора-
// человека (неудачный вход, анонимная повторная отправка) — «аноним». Пустой снимок имени — по роли
// (импорт GeoNames оператором пишет только actor_role, ревью M2).
function Actor({ record: r, onFilter }: { record: AuditRecord; onFilter: (id: string) => void }) {
  const t = useTranslations('audit.actor');
  const service = r.actor_role === 'operator' || r.actor_role === 'system';
  const label = r.actor_label || (!r.actor_staff_id && service ? r.actor_role : null);
  if (label === 'operator' && !r.actor_staff_id) return <span>{t('operator')}</span>;
  if (label === 'system' && !r.actor_staff_id) return <span>{t('system')}</span>;
  if (r.actor_staff_id && label) {
    const id = r.actor_staff_id;
    return (
      <Button variant="link" aria-label={t('filter', { name: label })} onClick={() => onFilter(id)}>
        {label}
      </Button>
    );
  }
  if (label) return <span>{label}</span>;
  return <span>{r.actor_role === 'anonymous' ? t('anonymous') : t('unknown')}</span>;
}

function ActionCell({
  record: r,
  open,
  onToggle,
}: {
  record: AuditRecord;
  open: boolean;
  onToggle: () => void;
}) {
  const t = useTranslations('audit.details');
  const labels = useAuditLabels();
  const detailsId = useId();
  return (
    <div className={styles.stack}>
      <span className={styles.actionRow}>
        <span className={styles.action}>{labels.action(r.action)}</span>
        <Button variant="link" aria-expanded={open} aria-controls={detailsId} onClick={onToggle}>
          {t('show')}
        </Button>
      </span>
      {open && (
        <section
          id={detailsId}
          aria-label={t('label', { id: r.id.slice(0, 8) })}
          className={styles.details}
        >
          <dl className={styles.facts}>
            <dt>{t('record')}</dt>
            <dd className={styles.code}>{r.id}</dd>
            <dt>{t('object')}</dt>
            <dd className={styles.code}>
              {[labels.object(r.object_type), r.object_id].filter(Boolean).join(' · ')}
            </dd>
            {r.reason && (
              <>
                <dt>{t('reason')}</dt>
                <dd>{r.reason}</dd>
              </>
            )}
            {r.ip && (
              <>
                <dt>{t('ip')}</dt>
                <dd className={styles.code}>{r.ip}</dd>
              </>
            )}
            {r.user_agent && (
              <>
                <dt>{t('user_agent')}</dt>
                <dd>{r.user_agent}</dd>
              </>
            )}
          </dl>
          <div className={styles.diff}>
            <State label={t('before')} value={r.before} none={t('none')} />
            <State label={t('after')} value={r.after} none={t('none')} />
          </div>
        </section>
      )}
    </div>
  );
}

// Было или стало: JSON как текст (React экранирует) — маскированные сервером значения как есть.
function State({ label, value, none }: { label: string; value: unknown; none: string }) {
  return (
    <div role="group" aria-label={label} className={styles.state}>
      <p aria-hidden="true" className={styles.stateTitle}>
        {label}
      </p>
      {value == null ? (
        <p className={styles.muted}>{none}</p>
      ) : (
        <pre className={styles.json}>{JSON.stringify(value, null, 2)}</pre>
      )}
    </div>
  );
}
