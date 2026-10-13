// Открытые оверлеи (модалка, панель, шторка) — стопкой: Esc и Tab обрабатывает только верхний
// (подтверждение поверх панели закрывается первым). Пока открыт хоть один — прокрутка фона
// заблокирована: атрибут data-wf-scroll-lock на body, правило — в Overlay.module.css (не
// inline-стиль: CSP админки).
type Entry = { id: symbol; panel: HTMLElement | null };
const stack: Entry[] = [];
const LOCK_ATTR = 'data-wf-scroll-lock';

// panel — элемент диалога: на него уходит фокус, когда вернуть его больше некуда (тосты).
export function pushOverlay(id: symbol, panel: HTMLElement | null = null): () => void {
  stack.push({ id, panel });
  document.body.setAttribute(LOCK_ATTR, '');
  return () => {
    const i = stack.findLastIndex((e) => e.id === id);
    if (i !== -1) stack.splice(i, 1);
    if (stack.length === 0) document.body.removeAttribute(LOCK_ATTR);
  };
}

export const isTopOverlay = (id: symbol): boolean => stack[stack.length - 1]?.id === id;

// Панель верхнего открытого оверлея — по стеку открытия, не по порядку в DOM.
export const topOverlayPanel = (): HTMLElement | null => stack[stack.length - 1]?.panel ?? null;

// Цикл Tab ведёт trapTab сам, поэтому список — ровно то, куда браузер ставит фокус по Tab:
// tabindex="-1" (фокус только программно) исключает любой элемент, даже кнопку и ссылку.
const NOT_SKIPPED = ':not([tabindex="-1"])';
const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'summary',
  '[contenteditable]:not([contenteditable="false"])',
  '[tabindex]',
]
  .map((s) => s + NOT_SKIPPED)
  .join(',');

// Поле в fieldset[disabled] выключено, кроме содержимого его первой legend (HTML).
function inDisabledFieldset(el: HTMLElement): boolean {
  for (let f = el.closest('fieldset'); f; f = f.parentElement?.closest('fieldset') ?? null) {
    if (!f.disabled) continue;
    const legend = Array.from(f.children).find((c) => c.tagName === 'LEGEND');
    if (!legend?.contains(el)) return true;
  }
  return false;
}

// Невидимое не получает фокус: hidden и inert у предка; в браузере ещё и checkVisibility()
// (display: none, visibility: hidden) — в jsdom его нет, там хватает атрибутов.
function visible(el: HTMLElement): boolean {
  if (el.closest('[hidden], [inert]')) return false;
  return typeof el.checkVisibility === 'function'
    ? el.checkVisibility({ visibilityProperty: true })
    : true;
}

export function focusables(root: ParentNode | null | undefined): HTMLElement[] {
  if (!root) return [];
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => !inDisabledFieldset(el) && visible(el),
  );
}

// Область тостов (ToastProvider) — часть цикла Tab открытого оверлея: тост-ошибку можно закрыть с
// клавиатуры, не закрывая модалку.
export const TOASTS_ATTR = 'data-wf-toasts';
const toastRegion = () => document.querySelector<HTMLElement>(`[${TOASTS_ATTR}]`);

// Фокус-ловушка: Tab ведём сами по списку «диалог, затем тосты» по кругу (порядок DOM тут не
// помогает: область тостов — отдельный портал); фокус снаружи — на первый (Shift — на последний).
export function trapTab(e: KeyboardEvent, root: HTMLElement | null) {
  if (!root) return;
  e.preventDefault();
  const items = [...focusables(root), ...focusables(toastRegion())];
  if (items.length === 0) {
    root.focus();
    return;
  }
  const i = items.indexOf(document.activeElement as HTMLElement);
  const step = e.shiftKey ? -1 : 1;
  const next =
    i === -1 ? (e.shiftKey ? items.length - 1 : 0) : (i + step + items.length) % items.length;
  items[next]?.focus();
}
