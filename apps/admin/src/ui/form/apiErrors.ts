import type { FieldValues, Path, UseFormReturn } from 'react-hook-form';
import type { ApiError } from '../../api/client';

// Поле тела запроса → имя поля формы: body.city_ids → city_ids, body.a.b → a.b. Поля query.*,
// path.*, header.* формой не вводятся.
function formField(field: string): string | null {
  return field.startsWith('body.') && field.length > 'body.'.length ? field.slice(5) : null;
}

// Раскладывает 400 validation.failed по полям формы (спека §9.1): setError с кодом поля как типом
// (текст — ui.field_errors.<код>), фокус — на первое. Поле есть в форме, если у него есть значение
// (зарегистрировано или задано в defaultValues). Возвращает true, если разложено всё и общий текст
// не нужен; false — показать FormError.
export function applyFieldErrors<T extends FieldValues>(
  form: UseFormReturn<T>,
  e: ApiError,
): boolean {
  if (e.code !== 'validation.failed' || !e.fields?.length) return false;
  let all = true;
  let focused = false;
  for (const { field, code } of e.fields) {
    const name = formField(field) as Path<T> | null;
    if (!name || form.getValues(name) === undefined) {
      all = false;
      continue;
    }
    form.setError(name, { type: code }, { shouldFocus: !focused });
    focused = true;
  }
  return all;
}
