import { describe, expect, test } from 'vitest';
import { CITY_OTHER, CITY_OWN, meModerator, meOwner } from '../test/me';
import { can, decide } from './permissions';

describe('can / decide по me.permissions', () => {
  test('allow — можно везде', () => {
    expect(can(meOwner, 'audit.log.read')).toBe(true);
    expect(can(meOwner, 'audit.log.read', CITY_OTHER)).toBe(true);
    expect(decide(meOwner, 'audit.log.read', CITY_OTHER)).toBe('allow');
  });

  test('scoped — в своём городе можно, в чужом нельзя, без города — раздел виден', () => {
    expect(can(meModerator, 'geo.cities.rename', CITY_OWN)).toBe(true);
    expect(decide(meModerator, 'geo.cities.rename', CITY_OWN)).toBe('allow');
    expect(can(meModerator, 'geo.cities.rename', CITY_OTHER)).toBe(false);
    expect(decide(meModerator, 'geo.cities.rename', CITY_OTHER)).toBe('deny');
    // Меню и списки: сервер сам отфильтрует до своих городов (M10).
    expect(can(meModerator, 'audit.log.read')).toBe(true);
    expect(decide(meModerator, 'audit.log.read')).toBe('allow');
  });

  test('dual — «можно запросить»: can true, decide needs_approval', () => {
    expect(can(meOwner, 'staff.roles.grant')).toBe(true);
    expect(decide(meOwner, 'staff.roles.grant')).toBe('needs_approval');
  });

  test('ключа нет — нельзя', () => {
    expect(can(meModerator, 'staff.accounts.read')).toBe(false);
    expect(decide(meModerator, 'staff.accounts.read')).toBe('deny');
    expect(can(meOwner, 'nope.nope.nope')).toBe(false);
  });

  test('незнакомое значение (enum открытый) — нельзя', () => {
    const me = { ...meOwner, permissions: { 'audit.log.read': 'future' as never } };
    expect(can(me, 'audit.log.read')).toBe(false);
    expect(decide(me, 'audit.log.read')).toBe('deny');
  });
});
