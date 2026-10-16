import { useNavigate, useSearch } from '@tanstack/react-router';
import { useTranslations } from 'use-intl';
import { paths } from '../../app/paths';
import { PageHeader, Tabs } from '../../ui';
import { AuditView } from './AuditView';
import { type AuditSearch, parseAuditSearch, SECTIONS } from './search';
import styles from './audit.module.css';

// Порядок параметров в адресе — постоянный (одинаковые фильтры — один адрес).
const ORDER = ['tab', 'action', 'actor', 'object_type', 'object_id', 'from', 'to'] as const;

function clean(s: AuditSearch): AuditSearch {
  const out: AuditSearch = {};
  for (const k of ORDER) {
    const v = s[k];
    if (v !== undefined && v !== '') Object.assign(out, { [k]: v });
  }
  return out;
}

// «Журнал действий» (спека §9.4 п. 7, §5.3; макет 07; право audit.log.read — проверяет каркас; на
// телефоне открыт — D22): вкладки по разделам → action_prefix, фильтры и период — search-параметры
// маршрута (validateSearch = parseAuditSearch). Смена вкладки сбрасывает только действие (оно —
// внутри раздела), остальные фильтры остаются.
export function AuditPage() {
  const t = useTranslations('audit');
  const tn = useTranslations('nav');
  const navigate = useNavigate();
  const search = parseAuditSearch(useSearch({ strict: false }));
  const go = (next: AuditSearch) => void navigate({ to: paths.audit, search: clean(next) });

  return (
    <div className={styles.page}>
      <PageHeader title={tn('audit')} subtitle={t('subtitle')} />
      <Tabs
        label={t('tabs.label')}
        items={[
          { value: 'all', label: t('tabs.all') },
          ...SECTIONS.map((s) => ({ value: s, label: t(`tabs.${s}`) })),
        ]}
        value={search.tab ?? 'all'}
        onChange={(v) => {
          const tab = SECTIONS.find((s) => s === v);
          go({ ...search, tab, action: undefined });
        }}
      >
        <AuditView search={search} onSearchChange={go} />
      </Tabs>
    </div>
  );
}
