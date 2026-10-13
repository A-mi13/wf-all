// Открытые оверлеи (модалка, панель, шторка) — стопкой: Esc и Tab обрабатывает только верхний
// (подтверждение поверх панели закрывается первым). Пока открыт хоть один — прокрутка фона
// заблокирована: атрибут data-wf-scroll-lock на body, правило — в Overlay.module.css (не
// inline-стиль: CSP админки).
const stack: symbol[] = [];
const LOCK_ATTR = 'data-wf-scroll-lock';

export function pushOverlay(id: symbol): () => void {
  stack.push(id);
  document.body.setAttribute(LOCK_ATTR, '');
  return () => {
    const i = stack.lastIndexOf(id);
    if (i !== -1) stack.splice(i, 1);
    if (stack.length === 0) document.body.removeAttribute(LOCK_ATTR);
  };
}

export const isTopOverlay = (id: symbol): boolean => stack[stack.length - 1] === id;

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

export function focusables(root: ParentNode | null | undefined): HTMLElement[] {
  return root ? Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)) : [];
}

// Фокус-ловушка: Tab с последнего элемента — на первый, Shift+Tab с первого — на последний;
// фокус снаружи диалога — внутрь.
export function trapTab(e: KeyboardEvent, root: HTMLElement | null) {
  if (!root) return;
  const items = focusables(root);
  const first = items[0];
  const last = items[items.length - 1];
  const active = document.activeElement;
  if (!first || !last) {
    e.preventDefault();
    root.focus();
  } else if (!root.contains(active)) {
    e.preventDefault();
    first.focus();
  } else if (e.shiftKey && (active === first || active === root)) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && active === last) {
    e.preventDefault();
    first.focus();
  }
}
