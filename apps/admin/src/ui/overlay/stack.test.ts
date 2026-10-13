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
