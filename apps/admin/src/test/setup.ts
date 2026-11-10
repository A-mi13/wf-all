import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

// Язык браузера в тестах — русский (у jsdom en-US): до входа язык интерфейса — выбор устройства или язык
// браузера (src/i18n/deviceLocale.ts), а тексты экранов в тестах — ru. Геттер (а не значение): тест языка
// подменяет его через vi.spyOn(navigator, 'language', 'get').
Object.defineProperty(navigator, 'language', { get: () => 'ru-RU', configurable: true });

// Прокладка Popover API (спека UI-фундамента §4.8, n3, m-b): jsdom 30 его не знает (showPopover —
// undefined). Показ и скрытие ставят и снимают атрибут-состояние data-popover-open и шлют toggle, как
// браузер; matches(':popover-open') — по нему же. Как в браузере, показ открытого или отключённого
// элемента и элемента без атрибута popover бросает InvalidStateError — код обязан проверять сам. CSS
// Modules в Vitest стилей не подключают, поэтому тестовый <style> прячет закрытый пузырь и показывает открытый: toBeVisible
// и дерево доступности отражают открытость (страж — popover.test › «закрытый — нет в дереве»). CSP тестов
// не касается (csp.test исключает src/test/**, U20).
const OPEN_ATTR = 'data-popover-open';
// (стражи e2e/*.test.ts идут в окружении node — там DOM нет, прокладка не нужна)
if (typeof HTMLElement !== 'undefined' && typeof HTMLElement.prototype.showPopover !== 'function') {
  const invalid = (what: string) => new DOMException(what, 'InvalidStateError');
  const toggle = (el: HTMLElement, open: boolean) => {
    const event = Object.assign(new Event('toggle'), {
      oldState: open ? 'closed' : 'open',
      newState: open ? 'open' : 'closed',
    });
    el.dispatchEvent(event);
  };
  Object.defineProperties(HTMLElement.prototype, {
    showPopover: {
      configurable: true,
      value(this: HTMLElement) {
        if (!this.hasAttribute('popover')) throw invalid('showPopover: нет атрибута popover');
        if (!this.isConnected) throw invalid('showPopover: элемент не в документе');
        if (this.hasAttribute(OPEN_ATTR)) throw invalid('showPopover: уже открыт');
        this.setAttribute(OPEN_ATTR, '');
        toggle(this, true);
      },
    },
    hidePopover: {
      configurable: true,
      value(this: HTMLElement) {
        if (!this.hasAttribute(OPEN_ATTR)) throw invalid('hidePopover: уже закрыт');
        this.removeAttribute(OPEN_ATTR);
        toggle(this, false);
      },
    },
    togglePopover: {
      configurable: true,
      value(this: HTMLElement, force?: boolean) {
        const open = force ?? !this.hasAttribute(OPEN_ATTR);
        if (open && !this.hasAttribute(OPEN_ATTR)) this.showPopover();
        else if (!open && this.hasAttribute(OPEN_ATTR)) this.hidePopover();
        return open;
      },
    },
  });
  const matches = Element.prototype.matches;
  Element.prototype.matches = function (this: Element, selectors: string) {
    if (selectors === ':popover-open') return this.hasAttribute(OPEN_ATTR);
    return matches.call(this, selectors);
  } as typeof Element.prototype.matches;
  const style = document.createElement('style');
  // своё правило открытого нужно тоже: таблица стилей jsdom прячет [popover]:not(:popover-open), а
  // :popover-open движок стилей jsdom не знает — без него пузырь скрыт всегда
  style.textContent = `[popover]:not([${OPEN_ATTR}]) { display: none !important; } [popover][${OPEN_ATTR}] { display: block !important; }`;
  document.head.append(style);
}

// Неописанный запрос роняет тест (повторное ревью 27.10, Minor 5): msw 3 при onUnhandledFrame 'error'
// только пишет в консоль и обрывает fetch — экран, проглотивший обрыв, остался бы зелёным. Запросы
// копятся и проверяются после теста.
const unhandled: string[] = [];
server.events.on('request:unhandled', ({ request }) => {
  unhandled.push(`${request.method} ${request.url}`);
});

beforeAll(() => server.listen({ onUnhandledFrame: 'error' }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
  const sent = unhandled.splice(0);
  if (sent.length > 0) throw new Error(`MSW: неописанные запросы — ${sent.join('; ')}`);
});
afterAll(() => server.close());
