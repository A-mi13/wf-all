import { screen, waitFor, within } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import { expect } from 'vitest';

// Помощники для своего выпадающего списка (Select, спека UI-фундамента §4.16) — замена user.selectOptions,
// toHaveValue и чтения option у нативного списка select. Поле — combobox по подписи (имя — только подпись поля)
// или сам элемент. Список рендерится рядом с полем (Popover) или в шторке (Sheet, телефон) — опции ищутся
// по aria-controls combobox, а не внутри кнопки.

export type OptionInfo = { el: HTMLElement; value: string; label: string; disabled: boolean };

const fieldOf = (field: string | RegExp | HTMLElement): HTMLElement =>
  field instanceof HTMLElement ? field : screen.getByRole('combobox', { name: field });

const listOf = (combo: HTMLElement): HTMLElement => {
  const id = combo.getAttribute('aria-controls');
  const list = id ? document.getElementById(id) : null;
  if (!list) throw new Error('pick: у combobox нет открытого списка (aria-controls)');
  return list;
};

const isOpen = (combo: HTMLElement) => combo.getAttribute('aria-expanded') === 'true';

async function open(user: UserEvent, combo: HTMLElement): Promise<HTMLElement> {
  if (!isOpen(combo)) await user.click(combo);
  await waitFor(() => expect(isOpen(combo)).toBe(true));
  return listOf(combo);
}

const info = (el: HTMLElement): OptionInfo => ({
  el,
  value: el.dataset.value ?? '',
  label: (el.textContent ?? '').trim(),
  disabled: el.getAttribute('aria-disabled') === 'true',
});

// Открыть список (если закрыт) и вернуть опции; закрыть — Esc.
export async function openOptions(
  user: UserEvent,
  field: string | RegExp | HTMLElement,
): Promise<OptionInfo[]> {
  const list = await open(user, fieldOf(field));
  return within(list).queryAllByRole('option').map(info);
}

// Выбрать опцию по имени (строка — точное, RegExp); строка, не найденная по имени, — по значению
// (data-value), как у selectOptions. Ждёт закрытия списка.
export async function pick(
  user: UserEvent,
  field: string | RegExp | HTMLElement,
  option: string | RegExp,
): Promise<void> {
  const combo = fieldOf(field);
  const list = await open(user, combo);
  const options = within(list).queryAllByRole('option');
  const byName = within(list).queryAllByRole('option', { name: option });
  const target =
    byName[0] ??
    (typeof option === 'string' ? options.find((o) => o.dataset.value === option) : undefined);
  if (!target) {
    throw new Error(
      `pick: нет опции «${String(option)}» среди: ${options.map((o) => o.textContent).join(', ')}`,
    );
  }
  await user.click(target);
  await waitFor(() => expect(isOpen(combo)).toBe(false));
}

// Мультивыбор (MultiSelectField): открыть список (если закрыт), отметить или снять каждую опцию по имени
// (опция может прийти позже — поиск на сервере), закрыть Esc и дождаться закрытия.
export async function pickMany(
  user: UserEvent,
  field: string | RegExp | HTMLElement,
  options: (string | RegExp)[],
): Promise<void> {
  const combo = fieldOf(field);
  const list = await open(user, combo);
  for (const name of options) {
    await user.click(await within(list).findByRole('option', { name }));
  }
  await user.keyboard('{Escape}');
  await waitFor(() => expect(isOpen(combo)).toBe(false));
}

// Выбранное значение — текст кнопки (подпись выбранной опции или placeholder).
export function selectedText(field: string | RegExp | HTMLElement): string {
  return (fieldOf(field).textContent ?? '').trim();
}
