import { useState } from 'react';
import { type RegisterOptions, useFormContext, useWatch } from 'react-hook-form';
import { MultiSelectField, type Option } from '../../ui';
import { cityLabel, useCityNames, useCitySearch } from './cities';

// Города модератора (* — форма приглашения и смена роли): MultiSelectField с поиском на сервере
// (listGeoCities, q). Варианты — найденные плюс уже выбранные (их подписи — по ids: черновик или
// роль сотрудника приносят только id).
export function CityField({
  name,
  label,
  hint,
  searchPlaceholder,
  rules,
}: {
  name: string;
  label: string;
  hint?: string;
  searchPlaceholder?: string;
  rules?: RegisterOptions;
}) {
  const { control } = useFormContext();
  const value: unknown = useWatch({ control, name });
  const selected = Array.isArray(value) ? value.filter((v) => typeof v === 'string') : [];
  const [text, setText] = useState('');
  const found = useCitySearch(text);
  const names = useCityNames(selected);

  const options: Option[] = [];
  const seen = new Set<string>();
  for (const c of [...(names.data?.items ?? []), ...(found.data?.items ?? [])]) {
    if (seen.has(c.id)) continue;
    seen.add(c.id);
    options.push({ value: c.id, label: cityLabel(c) });
  }

  return (
    <MultiSelectField
      name={name}
      label={label}
      hint={hint}
      options={options}
      searchable
      onSearch={setText}
      searchPlaceholder={searchPlaceholder}
      rules={rules}
    />
  );
}
