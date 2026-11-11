import { describe, expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

// Единый словарь (спека UI-фундамента §4.7, принцип 13, план UF2a.9): общие действия — ключи actions.*, экран
// не заводит своего «Сохранить». Страж: (1) значение actions.* в ru не повторяется под другим ключом (кроме
// исключений с причиной); (2) запрещённые синонимы не встречаются.

type Tree = { [k: string]: string | Tree };
const flat = (tree: Tree, prefix = ''): [string, string][] =>
  Object.entries(tree).flatMap(([k, v]) =>
    typeof v === 'string' ? [[`${prefix}${k}`, v] as [string, string]] : flat(v, `${prefix}${k}.`),
  );

const ACTIONS = [
  'save',
  'cancel',
  'close',
  'back',
  'retry',
  'archive',
  'restore',
  'delete',
  'undo',
  'copy',
  'showMore',
  'resetFilters',
  'submitForApproval',
] as const;

// Ключ → значение actions.*, которое он законно повторяет, и причина.
const EXCEPTIONS: Record<string, string> = {
  'actions.undo':
    '«Отменить» в тосте (вернуть сделанное) и «Отменить» в диалоге — одно слово в ru, разные действия (en: Undo / Cancel)',
};

// Запрещённые синонимы общих действий; allowed — ключи, где слово значит другое действие (с причиной).
const FORBIDDEN: { word: string; instead: string; allowed: Record<string, string> }[] = [
  { word: 'Стереть', instead: 'actions.delete', allowed: {} },
  { word: 'Отмена', instead: 'actions.cancel («Отменить»)', allowed: {} },
  {
    word: 'Подтвердить',
    instead: 'actions.submitForApproval («Отправить на подтверждение»)',
    allowed: {
      'approvals.approve':
        'решение подтверждающего по чужому запросу — само подтверждение, а не запрос его',
      'auth.setup.confirm': 'подтверждение кода 2FA при настройке — не правило двух рук',
    },
  },
];

const entries = flat(ru as Tree);

describe('словарь actions.*', () => {
  test('все общие действия есть в ru и en', () => {
    expect(Object.keys((ru as Tree).actions as Tree).sort()).toEqual([...ACTIONS].sort());
    expect(Object.keys((en as Tree).actions as Tree).sort()).toEqual([...ACTIONS].sort());
  });

  test('значение actions.* не повторяется под другим ключом', () => {
    // значение → первое действие с ним (второе действие с тем же словом — тоже повтор, если не в исключениях)
    const values = new Map<string, string>();
    for (const a of ACTIONS) {
      const v = ((ru as Tree).actions as Tree)[a] as string;
      if (!values.has(v)) values.set(v, a);
    }
    const dupes = entries
      .filter(([key, value]) => {
        const owner = values.get(value);
        return owner !== undefined && key !== `actions.${owner}` && !(key in EXCEPTIONS);
      })
      .map(([key, value]) => `${key} = «${value}» → actions.${values.get(value)}`);
    expect(dupes).toEqual([]);
  });

  test('исключения не устарели', () => {
    const values = new Set(Object.values((ru as Tree).actions as Tree));
    const stale = Object.keys(EXCEPTIONS).filter((key) => {
      const v = entries.find(([k]) => k === key)?.[1];
      return v === undefined || !values.has(v);
    });
    expect(stale).toEqual([]);
  });
});

test('запрещённые синонимы не встречаются', () => {
  const found = FORBIDDEN.flatMap(({ word, instead, allowed }) =>
    entries
      .filter(([key, value]) => value.trim() === word && !(key in allowed))
      .map(([key]) => `${key} = «${word}» → ${instead}`),
  );
  expect(found).toEqual([]);
});
