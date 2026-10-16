import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { Button, Form, SelectField, TextField } from '../../ui';
import { useAuditLabels } from './labels';
import { AUDIT_SECTIONS, type AuditSearch, isUuid } from './search';
import styles from './audit.module.css';

type Values = {
  action: string;
  object_type: string;
  object_id: string;
  actor: string;
  from: string;
  to: string;
};

const fromSearch = (s: AuditSearch): Values => ({
  action: s.action ?? '',
  object_type: s.object_type ?? '',
  object_id: s.object_id ?? '',
  actor: s.actor ?? '',
  from: s.from ?? '',
  to: s.to ?? '',
});

// Фильтры журнала в «Ещё фильтры» таблицы: действие (знакомые коды текущей вкладки), вид и id
// объекта, id сотрудника (его же ставит клик по имени в строке), период — даты в местном времени,
// оба дня включительно. Значения — из адреса; «Применить» уводит их в адрес (search-параметры).
// Негодный id и «по» раньше «с» — ошибки полей до запроса (сервер ответил бы 400).
export function AuditFilters({
  search,
  onApply,
}: {
  search: AuditSearch;
  onApply: (patch: Omit<AuditSearch, 'tab'>) => void;
}) {
  const t = useTranslations('audit.filters');
  const labels = useAuditLabels();
  const form = useForm<Values>({ values: fromSearch(search) });
  const prefix = search.tab ? AUDIT_SECTIONS[search.tab] : '';
  const actions = labels.knownActions.filter((c) => c.startsWith(prefix));
  // значение из адреса без подписи (код новее сборки) — тоже вариант списка, иначе поле его потеряет
  if (search.action && !actions.includes(search.action)) actions.push(search.action);
  const objects = [...labels.knownObjects];
  if (search.object_type && !objects.includes(search.object_type)) objects.push(search.object_type);
  const uuid = (v: string) => v.trim() === '' || isUuid(v.trim());

  return (
    <Form
      form={form}
      className={styles.filters}
      onSubmit={(v) => {
        const trim = (s: string) => s.trim() || undefined;
        onApply({
          action: trim(v.action),
          actor: trim(v.actor),
          object_type: trim(v.object_type),
          object_id: trim(v.object_id),
          from: trim(v.from),
          to: trim(v.to),
        });
      }}
    >
      <div className={styles.filterGrid}>
        <SelectField
          name="action"
          label={t('action')}
          options={[
            { value: '', label: t('action_all') },
            ...actions.map((c) => ({ value: c, label: labels.action(c) })),
          ]}
        />
        <SelectField
          name="object_type"
          label={t('object_type')}
          options={[
            { value: '', label: t('object_type_all') },
            ...objects.map((o) => ({ value: o, label: labels.object(o) })),
          ]}
        />
        <TextField
          name="object_id"
          label={t('object_id')}
          autoComplete="off"
          spellCheck={false}
          rules={{ validate: { format: uuid } }}
        />
        <TextField
          name="actor"
          label={t('actor')}
          autoComplete="off"
          spellCheck={false}
          rules={{ validate: { format: uuid } }}
        />
        <TextField name="from" type="date" label={t('from')} />
        <TextField
          name="to"
          type="date"
          label={t('to')}
          hint={t('period_hint')}
          rules={{
            validate: {
              // ISO-даты сравниваются строкой
              range: (v: string) => !v || !form.getValues('from') || v >= form.getValues('from'),
            },
          }}
        />
      </div>
      <div className={styles.filterActions}>
        <Button type="submit" variant="secondary">
          {t('apply')}
        </Button>
      </div>
    </Form>
  );
}
