import type { components } from '../../api/gen/admin';
import { isKnownRole, type StaffRole } from './roles';

type StaffStatus = components['schemas']['StaffStatus'];

// Фильтры «Кто имеет доступ» — search-параметры маршрута /staff (спека §9.1): переживают
// перезагрузку и «Назад». Незнакомое значение из адреса отбрасывается — в запрос уходят только
// значения контракта.
export type StaffSearch = { q?: string; status?: StaffStatus; role?: StaffRole };

export const STATUSES: readonly StaffStatus[] = ['active', 'invited', 'disabled'];
const isStatus = (v: unknown): v is StaffStatus =>
  typeof v === 'string' && (STATUSES as readonly string[]).includes(v);

export function parseStaffSearch(s: Record<string, unknown>): StaffSearch {
  // Роутер разбирает значения как JSON: «123» в поиске приходит числом.
  const q = typeof s.q === 'string' || typeof s.q === 'number' ? String(s.q) : '';
  return {
    ...(q ? { q } : {}),
    ...(isStatus(s.status) ? { status: s.status } : {}),
    ...(typeof s.role === 'string' && isKnownRole(s.role) ? { role: s.role } : {}),
  };
}
