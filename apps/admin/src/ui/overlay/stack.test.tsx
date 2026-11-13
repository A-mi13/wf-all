import { render } from '@testing-library/react';
import { StrictMode, useRef } from 'react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { Popover } from '../popover/Popover';
import {
  focusables,
  hasModalOverlay,
  isOverlayStackEmpty,
  isTopModal,
  isTopOverlay,
  onModalOpen,
  pushOverlay,
  topOverlayPanel,
} from './stack';

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

// Запись стека с видом (спека UI-фундамента §4.8, R2-1): модальные (Modal, Drawer, Sheet, календарь)
// блокируют прокрутку, ловят Tab и дают панель тостам; немодальные (подсказка, меню, список) — только
// Esc по верхней из всех.
describe('вид записи', () => {
  test('немодальная запись: прокрутка не заблокирована, hasModalOverlay ложен, Esc — её', () => {
    const hint = Symbol('hint');
    const release = pushOverlay(hint, null, { modal: false });
    expect(document.body).not.toHaveAttribute('data-wf-scroll-lock');
    expect(hasModalOverlay()).toBe(false);
    expect(isOverlayStackEmpty()).toBe(false);
    expect(isTopOverlay(hint)).toBe(true);
    expect(isTopModal(hint)).toBe(false);
    release();
    expect(isOverlayStackEmpty()).toBe(true);
  });

  test('немодальный над Drawer: topOverlayPanel — панель Drawer, верхний модальный — Drawer, верхний — пузырь', () => {
    const panel = document.createElement('div');
    const drawer = Symbol('drawer');
    const list = Symbol('list');
    const releaseDrawer = pushOverlay(drawer, panel);
    const releaseList = pushOverlay(list, null, { modal: false });
    expect(topOverlayPanel()).toBe(panel);
    expect(isTopModal(drawer)).toBe(true);
    expect(isTopOverlay(drawer)).toBe(false);
    expect(isTopOverlay(list)).toBe(true);
    expect(hasModalOverlay()).toBe(true);
    expect(document.body).toHaveAttribute('data-wf-scroll-lock');
    releaseDrawer();
    // блок снят вместе с последней модальной, немодальная осталась
    expect(document.body).not.toHaveAttribute('data-wf-scroll-lock');
    expect(topOverlayPanel()).toBeNull();
    releaseList();
  });

  test('onModalOpen — на открытие модальной записи, не немодальной; отписка работает', () => {
    const cb = vi.fn();
    const off = onModalOpen(cb);
    const releaseHint = pushOverlay(Symbol('hint'), null, { modal: false });
    expect(cb).not.toHaveBeenCalled();
    const releaseModal = pushOverlay(Symbol('modal'), null);
    expect(cb).toHaveBeenCalledTimes(1);
    off();
    const again = pushOverlay(Symbol('modal2'), null);
    expect(cb).toHaveBeenCalledTimes(1);
    again();
    releaseModal();
    releaseHint();
  });

  test('размонтирование открытого Popover снимает запись; modal — и блок прокрутки', () => {
    function Open({ kind }: { kind: 'hint' | 'modal' }) {
      const anchor = useRef<HTMLButtonElement>(null);
      return (
        <>
          <button ref={anchor} type="button">
            Якорь
          </button>
          <Popover anchor={anchor} open kind={kind} label="Пузырь" onClose={() => {}}>
            <button type="button">Внутри</button>
          </Popover>
        </>
      );
    }
    const hint = render(<Open kind="hint" />);
    expect(isOverlayStackEmpty()).toBe(false);
    hint.unmount();
    expect(isOverlayStackEmpty()).toBe(true);
    const modal = render(<Open kind="modal" />);
    expect(hasModalOverlay()).toBe(true);
    expect(document.body).toHaveAttribute('data-wf-scroll-lock');
    modal.unmount();
    expect(isOverlayStackEmpty()).toBe(true);
    expect(document.body).not.toHaveAttribute('data-wf-scroll-lock');
  });

  test('StrictMode — одна запись: после закрытия стек пуст', () => {
    function Toggle({ open }: { open: boolean }) {
      const anchor = useRef<HTMLButtonElement>(null);
      return (
        <>
          <button ref={anchor} type="button">
            Якорь
          </button>
          <Popover anchor={anchor} open={open} kind="menu" onClose={() => {}}>
            <p>Список</p>
          </Popover>
        </>
      );
    }
    const { rerender } = render(
      <StrictMode>
        <Toggle open />
      </StrictMode>,
    );
    expect(isOverlayStackEmpty()).toBe(false);
    rerender(
      <StrictMode>
        <Toggle open={false} />
      </StrictMode>,
    );
    expect(isOverlayStackEmpty()).toBe(true);
  });
});
