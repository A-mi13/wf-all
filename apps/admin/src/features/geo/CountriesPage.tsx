import { useNavigate, useSearch } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { usePanelParam } from '../../app/panels';
import { paths } from '../../app/paths';
import { useCan, useDecide } from '../../app/session';
import { useNumber } from '../../i18n/format';
import {
  Button,
  type Column,
  ConfirmDialog,
  DataTable,
  EmptyState,
  PageHeader,
  Switch,
  useErrorText,
  useToast,
} from '../../ui';
import { CountryDrawer } from './CountryDrawer';
import { isApproval, useCountryLabels, useUpdateCountry } from './countries';
import styles from './geo.module.css';
import { useGeoCountries } from './queries';

type GeoCountry = components['schemas']['GeoCountry'];

// «Страны» (план B, Task 21; спека UI-фундамента §6.2; раздел открыт на любой ширине, право geo.countries.read
// проверяет каркас): таблица правил всех стран справочника. Панель страны — ?edit=<код> (I1: country — фильтр
// списков), создание — ?edit=new (одна панель на экран: SPEC.edit в panels.ts принимает код ISO или new). Ключ
// без права снимается (I-12): new — нужна geo.countries.rules (allow или dual), код — чтение (без manage панель
// только для чтения).
// Включение страны — сразу (200, geo.countries.manage), без «Отменить» (§4.10: обратное — выключение — вдвоём).
// Выключение — ConfirmDialog (код страны) → 202: запрос на подтверждение, переключатель остаётся «включено» до
// исполнения (не оптимистично: кэш не трогается).
export function CountriesPage() {
  const t = useTranslations('geo.countries');
  const tn = useTranslations('nav');
  const toast = useToast();
  const errorText = useErrorText();
  const navigate = useNavigate();
  const number = useNumber();
  const labels = useCountryLabels();
  const canManage = useCan('geo.countries.manage');
  const rules = useDecide('geo.countries.rules');
  const canCreate = rules !== 'deny';
  const canRead = useCan('geo.countries.read');
  const raw = (useSearch({ strict: false }) as { edit?: unknown }).edit;
  const editing = usePanelParam('edit', { allowed: raw === 'new' ? canCreate : canRead });
  const list = useGeoCountries();

  // Включение (200) и выключение (202) — разные мутации: у выключения ошибка живёт в окне подтверждения.
  const enable = useUpdateCountry(['geo', 'country-action', 'enable']);
  const disable = useUpdateCountry(['geo', 'country-action', 'disable']);
  const [enabling, setEnabling] = useState<string | null>(null);
  const [disabling, setDisabling] = useState<GeoCountry | null>(null);

  const toggle = (c: GeoCountry, next: boolean) => {
    if (!next) {
      disable.reset();
      setDisabling(c);
      return;
    }
    setEnabling(c.code);
    enable.mutate(
      { code: c.code, patch: { is_enabled: true } },
      {
        onSuccess: () => toast.success(t('enabled_toast', { name: c.name })),
        onError: (e) => toast.error(errorText(e)),
        onSettled: () => setEnabling(null),
      },
    );
  };

  const confirmDisable = () => {
    const c = disabling;
    if (!c) return;
    disable.mutate(
      { code: c.code, patch: { is_enabled: false } },
      {
        onSuccess: (r) => {
          setDisabling(null);
          // таблица устарела (страну уже выключили): выключение «сразу» правилом не считается — 200, тост по факту
          // ответа (ревью G9-C, m-1); страна из ответа уже в списке (useUpdateCountry)
          if (!isApproval(r)) {
            toast.success(t('disabled_toast', { name: r.name }));
            return;
          }
          toast.success(t('disable.requested', { name: c.name }), {
            action: {
              label: tn('approvals'),
              onClick: () => void navigate({ to: paths.approvals, search: { box: 'mine' } }),
            },
          });
        },
      },
    );
  };

  const columns: Column<GeoCountry>[] = [
    { id: 'code', header: t('columns.code'), width: 'sm', cell: (c) => c.code },
    { id: 'name', header: t('columns.name'), width: 'md', hideable: false, cell: (c) => c.name },
    { id: 'currency', header: t('columns.currency'), width: 'sm', cell: (c) => c.currency },
    {
      id: 'locale',
      header: t('columns.locale'),
      priority: 'low',
      cell: (c) => labels.language(c.default_locale),
    },
    { id: 'prefix', header: t('columns.prefix'), width: 'sm', cell: (c) => c.phone_prefix },
    {
      id: 'week',
      header: t('columns.week'),
      priority: 'low',
      cell: (c) => labels.weekday(c.week_starts_on),
    },
    {
      id: 'ages',
      header: t('columns.ages'),
      cell: (c) => t('ages_value', { min: c.min_signup_age, majority: c.age_of_majority }),
    },
    {
      id: 'enabled',
      header: t('columns.enabled'),
      cell: (c) => (
        <Switch
          checked={c.is_enabled}
          label={t('switch_label', { name: c.name })}
          hideLabel
          // включить — manage, выключить — rules (dual): без права переключатель только показывает состояние
          disabled={c.is_enabled ? rules === 'deny' : !canManage}
          busy={enabling === c.code || (disable.isPending && disabling?.code === c.code)}
          onChange={(next) => toggle(c, next)}
        />
      ),
    },
    {
      id: 'open_cities',
      header: t('columns.open_cities'),
      align: 'end',
      cell: (c) => number(c.open_cities),
    },
    {
      id: 'actions',
      header: t('columns.actions'),
      cell: (c) => (
        <Button
          variant="link"
          aria-label={t(canManage ? 'edit_label' : 'open_label', { name: c.name })}
          onClick={() => editing.open(c.code)}
        >
          {t(canManage ? 'edit' : 'open')}
        </Button>
      ),
    },
  ];

  const add = canCreate && <Button onClick={() => editing.open('new')}>{t('add')}</Button>;

  return (
    <div className={styles.page}>
      <PageHeader title={tn('countries')} subtitle={t('subtitle')} actions={add || undefined} />
      <DataTable
        label={t('table_label')}
        tableId="geo-countries"
        columns={columns}
        rowKey={(c) => c.code}
        rows={list.data?.items}
        error={list.data ? undefined : (list.error ?? undefined)}
        onRetry={() => void list.refetch()}
        retrying={list.isFetching}
        activeRowKey={editing.value}
        empty={
          <EmptyState
            illustration="map"
            title={t('empty_title')}
            text={t('empty_text')}
            action={add || undefined}
          />
        }
      />
      <CountryDrawer
        code={editing.value}
        onClose={editing.close}
        canManage={canManage}
        rules={rules}
      />
      <ConfirmDialog
        open={disabling !== null}
        onClose={() => setDisabling(null)}
        onConfirm={confirmDisable}
        title={t('disable.title', { name: disabling?.name ?? '' })}
        text={t('disable.text')}
        consequences={
          disabling && disabling.open_cities > 0
            ? [t('disable.open_cities', { count: disabling.open_cities })]
            : undefined
        }
        confirmWord={disabling?.code ?? ''}
        confirmLabel={t('disable.confirm')}
        loading={disable.isPending}
        error={disable.error}
      />
    </div>
  );
}
