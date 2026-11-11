import { useId, useState } from 'react';
import { useLocale, useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import fieldStyles from './Field.module.css';
import { Select } from './Select';
import type { Option } from './SelectField';

// Свой выпадающий список (спека UI-фундамента §4.16), открытый: короткий список с выбранной, длинный
// (страны ~250, прокрутка внутри пузыря), пустой. На ширине < breakpoint-mobile тот же список — шторкой
// снизу (подпись поля в заголовке, > 10 опций — поиск): истории снимаются и на 390.
export default { title: 'UI/Form/Select', visual: true, layout: true } satisfies StoryMeta;

function Sample({ options, initial }: { options: Option[]; initial: string }) {
  const t = useTranslations('staff.invite');
  const id = useId();
  const [value, setValue] = useState(initial);
  return (
    <div className={styles.column}>
      <div className={fieldStyles.field}>
        <span id={`${id}-label`} className={fieldStyles.label}>
          {t('role')}
        </span>
        <Select
          id={id}
          name="role"
          value={value}
          onChange={setValue}
          onBlur={() => undefined}
          options={options}
          placeholder={t('role_placeholder')}
          labelledBy={`${id}-label`}
          label={t('role')}
          defaultOpen
        />
      </div>
    </div>
  );
}

function useRoles(): Option[] {
  const t = useTranslations('roles');
  return [
    { value: 'owner', label: t('owner'), disabled: true },
    { value: 'admin', label: t('admin') },
    { value: 'moderator', label: t('moderator') },
    { value: 'support', label: t('support') },
  ];
}

// Страны — названия из Intl (данные, не строки интерфейса), ~250 кодов ISO 3166; служебные коды CLDR
// (псевдоязыки, союзы) — мимо.
const NOT_COUNTRY = new Set(['EU', 'EZ', 'QO', 'UN', 'XA', 'XB', 'ZZ']);
function useCountries(): Option[] {
  const locale = useLocale();
  const names = new Intl.DisplayNames([locale], { type: 'region', fallback: 'none' });
  const out: Option[] = [];
  for (let a = 65; a <= 90; a++) {
    for (let b = 65; b <= 90; b++) {
      const code = String.fromCharCode(a, b);
      const name = names.of(code);
      if (name && name !== code && !NOT_COUNTRY.has(code)) out.push({ value: code, label: name });
    }
  }
  return out.sort((x, y) => x.label.localeCompare(y.label, locale));
}

export const Open: Story = () => <Sample options={useRoles()} initial="moderator" />;
Open.storyName = 'Open';

export const LongList: Story = () => <Sample options={useCountries()} initial="RU" />;
LongList.storyName = 'Long list';

export const Empty: Story = () => <Sample options={[]} initial="" />;
Empty.storyName = 'Empty list';
