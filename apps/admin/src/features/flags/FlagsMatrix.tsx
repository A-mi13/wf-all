import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { Switch, VirtualMatrix } from '../../ui';
import styles from './flags.module.css';
import { atLimit, useFlagLabel, type useFlagToggle } from './toggle';

type FeatureFlag = components['schemas']['FeatureFlag'];
type GeoCity = components['schemas']['GeoCity'];
export type FlagToggler = ReturnType<typeof useFlagToggle>;

// Подпись строки флага: название (незнакомый ключ — сам ключ и «нет подписи»), пояснение, состояние клеток —
// «включено везде» (городские неактивны) или предел контракта (включить следующий город нельзя).
export function FlagHead({ flag }: { flag: FeatureFlag }) {
  const t = useTranslations('flagsPage');
  const { title, note, known } = useFlagLabel()(flag.key);
  return (
    <span className={styles.head}>
      <span className={styles.title}>{title}</span>
      <span className={styles.subtle}>{known ? note : t('no_title')}</span>
      {flag.enabled_globally && <span className={styles.subtle}>{t('on_everywhere')}</span>}
      {atLimit(flag) && <span className={styles.warning}>{t('limit')}</span>}
    </span>
  );
}

// Матрица «функция × город» (макет 07, план B, Task 22): строки — флаги, столбцы — «Везде» и города (виртуально
// по горизонтали, до 2000 — VirtualMatrix), клетка — Switch. Значение и доступность клетки — useFlagToggle.
export function FlagsMatrix({
  flags,
  cities,
  toggler,
}: {
  flags: FeatureFlag[];
  cities: GeoCity[];
  toggler: FlagToggler;
}) {
  const t = useTranslations('flagsPage');
  const label = useFlagLabel();
  return (
    <div className={styles.card}>
      <VirtualMatrix
        label={t('label')}
        rows={flags}
        rowKey={(f) => f.key}
        corner={t('corner')}
        rowHeader={(f) => <FlagHead flag={f} />}
        fixed={[
          {
            id: 'everywhere',
            header: t('everywhere'),
            cell: (f) => {
              const s = toggler.cell(f, { kind: 'global' });
              return (
                <Switch
                  {...s}
                  onChange={() => toggler.toggle(f, { kind: 'global' })}
                  label={t('everywhere_label', { flag: label(f.key).title })}
                  hideLabel
                />
              );
            },
          },
        ]}
        columns={cities}
        columnKey={(c) => c.id}
        columnHeader={(c) => c.name}
        cell={(f, c) => {
          const target = { kind: 'city', cityId: c.id, cityName: c.name } as const;
          return (
            <Switch
              {...toggler.cell(f, target)}
              onChange={() => toggler.toggle(f, target)}
              label={t('city_label', { flag: label(f.key).title, city: c.name })}
              hideLabel
            />
          );
        }}
      />
    </div>
  );
}
