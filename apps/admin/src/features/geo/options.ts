import type { Option } from '../../ui';

// Текущее значение — в списке сразу, пока справочник грузится: нативный select не теряет выбор
// (подпись — сам код до ответа).
export function withCurrent(options: Option[], current: string | undefined): Option[] {
  if (!current || options.some((o) => o.value === current)) return options;
  return [...options, { value: current, label: current }];
}
