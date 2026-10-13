import { useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import { StateBlock } from './StateBlock';

// Ответ 429 (макет 07, «Слишком часто»): отсчёт по Retry-After (ApiError.retryAfter, секунды),
// пока он идёт — кнопка «Подождать N с» недоступна, потом — «Повторить». Новый retryAfter
// перезапускает отсчёт; повторный 429 с тем же значением — экран ставит key={error}.
export function RateLimitedState({
  retryAfter,
  onRetry,
}: {
  retryAfter?: number;
  onRetry: () => void;
}) {
  const t = useTranslations('states.rate_limited');
  const left = useCountdown(retryAfter ?? 0);
  return (
    <StateBlock
      tone="warning"
      titleAs="h2"
      title={t('title')}
      text={left > 0 ? t('text', { seconds: left }) : t('ready')}
      action={
        left > 0 ? (
          <Button variant="secondary" disabled>
            {t('wait', { seconds: left })}
          </Button>
        ) : (
          <Button variant="secondary" onClick={onRetry}>
            {t('retry')}
          </Button>
        )
      }
    />
  );
}

// Секунды до конца отсчёта (вверх до целой): срок — от момента, когда пришло значение.
function useCountdown(seconds: number): number {
  const total = Math.max(0, Math.ceil(seconds));
  const [state, setState] = useState({ total, left: total });
  // новое значение — отсчёт заново (подстройка состояния во время рендера, без лишнего эффекта)
  if (state.total !== total) setState({ total, left: total });

  useEffect(() => {
    if (total === 0) return;
    const deadline = Date.now() + total * 1000;
    const timer = setInterval(() => {
      const left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      setState((s) => (s.total === total ? { total, left } : s));
      if (left === 0) clearInterval(timer);
    }, 1000);
    return () => clearInterval(timer);
  }, [total]);

  return state.total === total ? state.left : total;
}
