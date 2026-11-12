// Вкладка «Подтверждений» — search-параметр маршрута /approvals (переживает перезагрузку и «Назад»):
// без параметра — «Входящие», box=mine — «Мои». Незнакомое значение отбрасывается.
import { parseKeys } from '../../app/search';

// Ключи фильтров пути (FILTER_SEARCH_KEYS, страж searchKeys.test.ts): парсер разбирает только их.
export const KEYS = ['box'] as const;

export type ApprovalBox = 'incoming' | 'mine';
export type ApprovalsSearch = { box?: 'mine' };

export function parseApprovalsSearch(s: Record<string, unknown>): ApprovalsSearch {
  return parseKeys<(typeof KEYS)[number], ApprovalsSearch>(
    KEYS,
    { box: (v) => (v === 'mine' ? 'mine' : undefined) },
    s,
  );
}
