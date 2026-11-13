import { type RefObject, useEffect, useRef } from 'react';

// Шторка поля закрылась (выбор, Esc, «Закрыть») — фокус на якорь поля (кнопку списка или календаря).
// Overlay возвращает фокус открывателю, но на устройстве, где нажатие кнопку не фокусирует (Safari),
// открывателем запомнен body (или поле, где стоял курсор, — на iPhone выехала бы клавиатура).
export function useFocusAfterSheet(sheet: boolean, target: RefObject<HTMLElement | null>) {
  const was = useRef(false);
  useEffect(() => {
    if (was.current && !sheet) target.current?.focus();
    was.current = sheet;
  }, [sheet, target]);
}
