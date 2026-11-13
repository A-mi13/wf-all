import { useNavigate, useSearch } from '@tanstack/react-router';
import { type ReactNode, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { usePanelParam } from '../../app/panels';
import { paths } from '../../app/paths';
import { useCan } from '../../app/session';
import { useNumber } from '../../i18n/format';
import {
  Button,
  type ChipTone,
  type Column,
  DataTable,
  EmptyState,
  Form,
  infiniteTableProps,
  PageHeader,
  RelativeTime,
  SelectField,
  StatusChip,
  useToast,
} from '../../ui';
import { ImportJobStatus } from './ImportJobStatus';
import { ImportReport } from './ImportReport';
import styles from './imports.module.css';
import { type ImportsSearch, parseImportsSearch, toImportsQuery } from './importsSearch';
import { useGeoCountries, useGeoImports } from './queries';
import { StartImportDialog } from './StartImportDialog';

type GeoImport = components['schemas']['GeoImport'];

// Открытый отчёт — одна панель над журналом (ревью m-5): кнопка строки ссылается на неё aria-controls.
const REPORT_ID = 'geo-import-report';

const TONES: Record<string, ChipTone> = {
  running: 'accent',
  succeeded: 'success',
  failed: 'danger',
};

// «Импорт GeoNames» (план B, Task 20, экран 9 *; право geo.imports.read — проверяет каркас; открыт и на телефоне —
// спека UI-фундамента §6.2): строка задачи (?job=<id>) над журналом, журнал импортов (DataTable; на телефоне —
// карточки) с фильтром страны (?country=, ссылки главной), раскрытый отчёт сверки (?import=<id>, switch — одна
// запись истории), «Запустить импорт» (?start=1, geo.imports.run). Поддержка — только чтение: без запуска, отмены и
// привязки.
export function ImportsPage() {
  const t = useTranslations('geo.imports');
  const tn = useTranslations('nav');
  const toast = useToast();
  const navigate = useNavigate();
  const search = parseImportsSearch(useSearch({ strict: false }));
  const canRun = useCan('geo.imports.run');
  const starting = usePanelParam('start', { allowed: canRun });
  const report = usePanelParam('import', { allowed: true });
  const list = useGeoImports(toImportsQuery(search));
  // импорт оператора уже идёт (409 без job_id): текст и подсветка его строки — до следующего запуска или ухода
  const [operatorBusy, setOperatorBusy] = useState<string | null>(null);
  // отчёт открыт щелчком (не глубокой ссылкой) — панель забирает фокус
  const [openedByClick, setOpenedByClick] = useState(false);

  // Фильтры правятся слиянием с адресом: ключи панелей (?import= — открытый отчёт) не теряются (ревью m-1);
  // undefined — убрать ключ.
  const merge = (patch: Record<string, unknown>) => (prev: Record<string, unknown>) => {
    const next = { ...prev, ...patch };
    for (const k of Object.keys(next)) if (next[k] === undefined) delete next[k];
    return next;
  };
  const setSearch = (patch: Partial<ImportsSearch>) =>
    void navigate({ to: paths.imports, search: merge(patch), replace: true, resetScroll: false });

  // Задача поставлена (или найдена по 409): панель запуска заменяется строкой задачи — replace, «Назад» ведёт на
  // журнал, а не в окно (одна запись истории).
  // Фильтр другой страны сменяется страной задачи: иначе строки её импорта в журнале не видно (ревью m-2).
  const showJob = (jobId: number, country: string, already: boolean) => {
    setOperatorBusy(null);
    void navigate({
      to: paths.imports,
      search: merge({
        job: jobId,
        start: undefined,
        country: search.country === undefined ? undefined : country,
      }),
      replace: true,
      ignoreBlocker: true,
      resetScroll: false,
    });
    if (already) toast.info(t('already', { country }));
    else toast.success(t('started', { country }));
  };

  const rows = list.data?.pages.flatMap((p) => p.items);
  const busyRow = operatorBusy
    ? rows?.find((r) => r.country_code === operatorBusy && r.status === 'running')
    : undefined;

  const startButton = canRun && <Button onClick={() => starting.open('1')}>{t('start')}</Button>;

  return (
    <div className={styles.page}>
      <PageHeader
        title={tn('imports')}
        subtitle={t('subtitle')}
        actions={startButton || undefined}
      />
      {search.job !== undefined && (
        <ImportJobStatus
          key={search.job}
          jobId={search.job}
          canRun={canRun}
          onOpenReport={(id) => report.switch(id)}
          onHide={() => setSearch({ job: undefined })}
        />
      )}
      {report.value !== undefined && (
        <ImportReport
          key={report.value}
          id={REPORT_ID}
          importId={report.value}
          onClose={report.close}
          focusOnOpen={openedByClick}
        />
      )}
      {operatorBusy && (
        <p className={styles.notice} role="status">
          {t('busy_operator', { country: operatorBusy })}
        </p>
      )}
      <ImportsTable
        search={search}
        onSearchChange={setSearch}
        list={list}
        openReport={report.value}
        onToggleReport={(id) => {
          if (report.value === id) return report.close();
          setOpenedByClick(true);
          report.switch(id);
        }}
        activeRowKey={busyRow?.id ?? report.value}
        emptyAction={startButton || undefined}
      />
      {canRun && (
        <StartImportDialog
          open={starting.value !== undefined}
          onClose={starting.close}
          onStarted={showJob}
          onBusy={(country) => {
            setOperatorBusy(country);
            starting.close();
          }}
        />
      )}
    </div>
  );
}

function ImportsTable({
  search,
  onSearchChange,
  list,
  openReport,
  onToggleReport,
  activeRowKey,
  emptyAction,
}: {
  search: ImportsSearch;
  onSearchChange: (patch: Partial<ImportsSearch>) => void;
  list: ReturnType<typeof useGeoImports>;
  openReport: string | undefined;
  onToggleReport: (id: string) => void;
  activeRowKey: string | undefined;
  emptyAction: ReactNode;
}) {
  const t = useTranslations('geo.imports');
  const format = useNumber();
  const canCountries = useCan('geo.countries.read');
  const countries = useGeoCountries(canCountries);
  const filterForm = useForm<{ country: string }>({ values: { country: search.country ?? '' } });

  // страны фильтра: справочник (с правом на него), иначе — встреченные в журнале; значение из адреса — всегда
  const codes = new Set<string>();
  const options: { value: string; label: string }[] = [];
  const add = (value: string, label: string) => {
    if (codes.has(value)) return;
    codes.add(value);
    options.push({ value, label });
  };
  for (const c of countries.data?.items ?? []) add(c.code, `${c.name} (${c.code})`);
  for (const r of list.data?.pages.flatMap((p) => p.items) ?? [])
    add(r.country_code, r.country_code);
  if (search.country) add(search.country, search.country);

  const columns: Column<GeoImport>[] = [
    {
      id: 'country',
      header: t('columns.country'),
      width: 'sm',
      cell: (r) => <span className={styles.name}>{r.country_code}</span>,
    },
    {
      id: 'status',
      header: t('columns.status'),
      width: 'sm',
      cell: (r) => <ImportStatusChip status={r.status} />,
    },
    {
      id: 'who',
      header: t('columns.who'),
      width: 'md',
      cell: (r) => <Who row={r} />,
    },
    {
      id: 'started',
      header: t('columns.started'),
      width: 'md',
      cell: (r) => (
        <span className={styles.time}>
          <RelativeTime value={r.started_at} />
        </span>
      ),
    },
    {
      id: 'duration',
      header: t('columns.duration'),
      width: 'sm',
      align: 'end',
      cell: (r) => <Duration row={r} format={format} />,
    },
    {
      id: 'counts',
      header: t('columns.counts'),
      priority: 'low',
      cell: (r) =>
        r.places_upserted === null ? (
          <span className={styles.muted}>—</span>
        ) : (
          <span className={styles.stack}>
            <span>
              {t('counts', {
                places: format(r.places_upserted),
                names: format(r.names_upserted ?? 0),
              })}
            </span>
            <span className={styles.muted}>
              {t('counts_removed', {
                removed: format(r.places_removed ?? 0),
                missing: format(r.places_missing ?? 0),
              })}
            </span>
          </span>
        ),
    },
    {
      id: 'report',
      header: t('columns.report'),
      cell: (r) => (
        <ReportCell row={r} open={openReport === r.id} onToggle={() => onToggleReport(r.id)} />
      ),
    },
  ];

  return (
    <DataTable
      label={t('label')}
      tableId="geo-imports"
      columns={columns}
      rowKey={(r) => r.id}
      activeRowKey={activeRowKey}
      {...infiniteTableProps(list)}
      filters={{
        active: search.country ? 1 : 0,
        content: (
          <Form form={filterForm} onSubmit={() => {}}>
            <SelectField
              name="country"
              label={t('country_filter')}
              options={[{ value: '', label: t('country_all') }, ...options]}
              rules={{
                onChange: (e: { target: { value: string } }) =>
                  onSearchChange({ country: e.target.value || undefined }),
              }}
            />
          </Form>
        ),
      }}
      dirty={search.country !== undefined}
      onReset={() => onSearchChange({ country: undefined })}
      empty={
        <EmptyState
          illustration="time"
          title={t('empty_title')}
          text={t('empty_text')}
          action={emptyAction}
        />
      }
    />
  );
}

function ImportStatusChip({ status }: { status: string }) {
  const t = useTranslations('geo.imports.statuses');
  // подписи — литералами (страж неиспользуемых ключей); незнакомый статус — «—»
  const text: Record<string, string> = {
    running: t('running'),
    succeeded: t('succeeded'),
    failed: t('failed'),
  };
  const known = Object.hasOwn(TONES, status);
  return (
    <StatusChip tone={(known && TONES[status]) || 'neutral'}>
      {(known && text[status]) || t('unknown')}
    </StatusChip>
  );
}

// «Кто запустил» (R3): started_by_label — имя из записи журнала о постановке; started_by null — оператор сервера;
// started_by задан, а имени нет (запись не найдена) — «сотрудник».
function Who({ row }: { row: GeoImport }) {
  const t = useTranslations('geo.imports.who');
  if (row.started_by === null) return <span>{t('operator')}</span>;
  return <span>{row.started_by_label ?? t('staff')}</span>;
}

// Длительность — от начала до конца (идёт — «идёт»): < 1 мин — секунды, < 1 ч — минуты, дальше — часы с десятой.
function Duration({ row, format }: { row: GeoImport; format: ReturnType<typeof useNumber> }) {
  const t = useTranslations('geo.imports');
  if (!row.finished_at) return <span className={styles.muted}>{t('running')}</span>;
  const ms = Math.max(0, Date.parse(row.finished_at) - Date.parse(row.started_at));
  if (Number.isNaN(ms)) return <span className={styles.muted}>—</span>;
  const unit = (n: number, u: 'second' | 'minute' | 'hour', max = 0) =>
    format(n, { style: 'unit', unit: u, unitDisplay: 'short', maximumFractionDigits: max });
  if (ms < 60_000) return <span>{unit(Math.round(ms / 1000), 'second')}</span>;
  if (ms < 3_600_000) return <span>{unit(Math.round(ms / 60_000), 'minute')}</span>;
  return <span>{unit(ms / 3_600_000, 'hour', 1)}</span>;
}

function ReportCell({
  row,
  open,
  onToggle,
}: {
  row: GeoImport;
  open: boolean;
  onToggle: () => void;
}) {
  const t = useTranslations('geo.imports');

  return (
    <div className={styles.stack}>
      <Button
        variant="link"
        aria-expanded={open}
        aria-controls={open ? REPORT_ID : undefined}
        aria-label={`${t('report_show')}: ${row.country_code}`}
        onClick={onToggle}
      >
        {t('report_show')}
      </Button>
    </div>
  );
}
