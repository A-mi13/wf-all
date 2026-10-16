// Вкладка «Подтверждений» — search-параметр маршрута /approvals (переживает перезагрузку и «Назад»):
// без параметра — «Входящие», box=mine — «Мои». Незнакомое значение отбрасывается.
export type ApprovalBox = 'incoming' | 'mine';
export type ApprovalsSearch = { box?: 'mine' };

export function parseApprovalsSearch(s: Record<string, unknown>): ApprovalsSearch {
  return s.box === 'mine' ? { box: 'mine' } : {};
}
