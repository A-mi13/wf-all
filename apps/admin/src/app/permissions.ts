import type { components } from '../api/gen/admin';

// Права на клиенте (спека сотрудников §2.4, §9.3, D23) — только из me.permissions: меню и кнопки
// строятся по ним, но решает всегда сервер. Значения: allow — сразу; scoped — в своих городах
// (me.city_ids); dual — только вдвоём, действие создаёт запрос на подтверждение. Ключа нет или
// значение незнакомо (enum открытый) — нельзя.

export type Me = components['schemas']['StaffMe'];
export type Decision = 'deny' | 'allow' | 'needs_approval';

// cityId не задан — вопрос «есть ли право вообще» (раздел меню, список): scoped — да, сервер
// сам ограничит выдачу своими городами (M10).
export function decide(me: Me, permission: string, cityId?: string): Decision {
  switch (me.permissions[permission]) {
    case 'allow':
      return 'allow';
    case 'scoped':
      return cityId === undefined || me.city_ids.includes(cityId) ? 'allow' : 'deny';
    case 'dual':
      return 'needs_approval';
    default:
      return 'deny';
  }
}

// Можно ли начать действие; для dual — «можно запросить подтверждение».
export function can(me: Me, permission: string, cityId?: string): boolean {
  return decide(me, permission, cityId) !== 'deny';
}
