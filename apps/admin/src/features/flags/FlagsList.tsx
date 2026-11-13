import { useId } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { Button, StatusChip, Switch } from '../../ui';
import styles from './flags.module.css';
import { FlagHead, type FlagToggler } from './FlagsMatrix';
import { useFlagLabel } from './toggle';

type FeatureFlag = components['schemas']['FeatureFlag'];
type GeoCity = components['schemas']['GeoCity'];

// Телефон (спека UI-фундамента §3.5): не матрица, а список флагов; флаг раскрывается — его города (поиск и статус —
// те же параметры адреса, что у матрицы) с переключателем в строке; раскрытый флаг — ?flag= (принцип 10).
// Длинный список городов — content-visibility строк (CSS), без JS.
export function FlagsList({
  flags,
  cities,
  toggler,
  open,
  onOpen,
}: {
  flags: FeatureFlag[];
  cities: GeoCity[];
  toggler: FlagToggler;
  open: string | undefined;
  onOpen: (key: string | undefined) => void;
}) {
  const t = useTranslations('flagsPage');
  const label = useFlagLabel();
  const base = useId();
  return (
    <ul className={styles.list} aria-label={t('label')}>
      {flags.map((f) => {
        const { title } = label(f.key);
        const expanded = open === f.key;
        const listId = `${base}-${f.key}`;
        const all = toggler.cell(f, { kind: 'global' });
        const count = f.enabled_city_ids.length;
        return (
          <li key={f.key} aria-label={title} className={styles.item}>
            <div className={styles.itemHead}>
              <FlagHead flag={f} />
              {f.enabled_globally ? (
                <StatusChip tone="success">{t('state.everywhere')}</StatusChip>
              ) : count > 0 ? (
                <StatusChip tone="accent">{t('state.cities', { count })}</StatusChip>
              ) : (
                <StatusChip tone="neutral">{t('state.off')}</StatusChip>
              )}
            </div>
            <div className={styles.itemRow}>
              <span className={styles.rowName} aria-hidden="true">
                {t('everywhere')}
              </span>
              <Switch
                {...all}
                onChange={() => toggler.toggle(f, { kind: 'global' })}
                label={t('everywhere_label', { flag: title })}
                hideLabel
              />
            </div>
            <Button
              variant="link"
              aria-expanded={expanded}
              aria-controls={expanded ? listId : undefined}
              aria-label={t('cities', { flag: title })}
              onClick={() => onOpen(expanded ? undefined : f.key)}
            >
              {t('open_cities')}
            </Button>
            {expanded && (
              <ul id={listId} aria-label={t('cities', { flag: title })} className={styles.cities}>
                {cities.map((c) => {
                  const target = { kind: 'city', cityId: c.id, cityName: c.name } as const;
                  return (
                    <li key={c.id} className={styles.itemRow}>
                      <span className={styles.rowName} aria-hidden="true">
                        {c.name}
                      </span>
                      <Switch
                        {...toggler.cell(f, target)}
                        onChange={() => toggler.toggle(f, target)}
                        label={t('city_label', { flag: title, city: c.name })}
                        hideLabel
                      />
                    </li>
                  );
                })}
              </ul>
            )}
          </li>
        );
      })}
    </ul>
  );
}
