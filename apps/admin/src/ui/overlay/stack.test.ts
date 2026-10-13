import { afterEach, expect, test } from 'vitest';
import { focusables } from './stack';

afterEach(() => {
  document.body.innerHTML = '';
});

test('focusables: без disabled, полей в fieldset[disabled], hidden и inert', () => {
  document.body.innerHTML = `
    <div id="root">
      <button>Видимая</button>
      <button disabled>Выключенная</button>
      <fieldset disabled>
        <legend><button>В легенде</button></legend>
        <input aria-label="В выключенном fieldset" />
      </fieldset>
      <div hidden><button>Скрытая</button></div>
      <div inert><a href="#x">Инертная</a></div>
      <span tabindex="-1">Только программно</span>
      <input type="hidden" />
      <a href="#y">Ссылка</a>
    </div>`;
  const names = focusables(document.getElementById('root')).map((el) => el.textContent?.trim());
  expect(names).toEqual(['Видимая', 'В легенде', 'Ссылка']);
});

// trapTab ведёт каждое нажатие Tab сам: элемент с tabindex="-1" (фокус только программно) в цикл
// не входит, даже если он кнопка или ссылка; summary и contenteditable — входят.
test('focusables: tabindex="-1" исключает любой элемент; summary и contenteditable — в цикле', () => {
  document.body.innerHTML = `
    <div id="root">
      <button tabindex="-1">Кнопка вне Tab</button>
      <a href="#x" tabindex="-1">Ссылка вне Tab</a>
      <input tabindex="-1" value="Поле вне Tab" />
      <details><summary>Подробнее</summary><p>текст</p></details>
      <details><summary tabindex="-1">Сводка вне Tab</summary></details>
      <div contenteditable="true">Редактор</div>
      <div contenteditable="false">Не редактор</div>
      <div contenteditable tabindex="-1">Редактор вне Tab</div>
      <button>Обычная</button>
    </div>`;
  const names = focusables(document.getElementById('root')).map((el) => el.textContent?.trim());
  expect(names).toEqual(['Подробнее', 'Редактор', 'Обычная']);
});
