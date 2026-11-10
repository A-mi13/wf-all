// Единственная запись стилей из JS во всём src (спека UI-фундамента §4.5, стр. 577–588; страж —
// csp.test.ts): CSS-переменная --wf-* на самом элементе, значение — только число в px. CSP admin-api
// (style-src 'self') режет атрибут style из разметки, но не CSSOM: setProperty на элементе разрешён.
// Вызыватели — только ui/popover/* (положение пузыря от якоря; UF2b добавит ui/virtual/*).
export type WfCssVar = `--wf-${string}`;

export function setCssPx(el: HTMLElement, name: WfCssVar, px: number): void {
  if (!Number.isFinite(px)) return;
  el.style.setProperty(name, `${Math.round(px * 100) / 100}px`);
}
