// Раскладка для тестов виртуализации таблицы и списка выбора: jsdom не считает размеры. Окно прокрутки
// (область таблицы [data-table-scroll], список выбора [role=listbox]) — 400 px, строка (элемент с
// data-index) — 40 px, полная длина — с запасом; scrollTop хранится здесь, scrollTo ставит его и шлёт
// scroll, как браузер. Возвращает функцию, снимающую подмену.
export const STUB_VIEWPORT = 400;
export const STUB_ROW = 40;

const isViewport = (el: Element) =>
  el.hasAttribute('data-table-scroll') ||
  el.hasAttribute('data-virtual-viewport') ||
  el.getAttribute('role') === 'listbox';

export function stubTableLayout(): () => void {
  const proto = HTMLElement.prototype;
  const saved = (['clientHeight', 'offsetHeight', 'scrollHeight', 'scrollTop'] as const).map(
    (name) => [name, Object.getOwnPropertyDescriptor(proto, name)] as const,
  );
  const scrollTo = proto.scrollTo;
  const tops = new WeakMap<Element, number>();
  Object.defineProperty(proto, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) {
      return tops.get(this) ?? 0;
    },
    set(this: HTMLElement, v: number) {
      tops.set(this, v);
    },
  });
  Object.defineProperty(proto, 'clientHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return isViewport(this) ? STUB_VIEWPORT : 0;
    },
  });
  Object.defineProperty(proto, 'scrollHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return isViewport(this) ? 1_000_000 : 0;
    },
  });
  Object.defineProperty(proto, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.hasAttribute('data-index') ? STUB_ROW : 0;
    },
  });
  proto.scrollTo = function (this: HTMLElement, opts?: ScrollToOptions | number) {
    this.scrollTop = typeof opts === 'object' ? (opts.top ?? 0) : 0;
    this.dispatchEvent(new Event('scroll'));
  } as typeof proto.scrollTo;
  return () => {
    for (const [name, desc] of saved) {
      if (desc) Object.defineProperty(proto, name, desc);
      else delete (proto as unknown as Record<string, unknown>)[name];
    }
    proto.scrollTo = scrollTo;
  };
}
