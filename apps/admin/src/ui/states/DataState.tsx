import { useIsFetching, useQueryClient, type UseQueryResult } from '@tanstack/react-query';
import { type ReactNode, useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import { isApiError } from '../../api/client';
import { ME_KEY } from '../../app/query';
import { RateLimitedState } from './RateLimitedState';
import { EmptyState, ErrorState, ForbiddenState, NoNetworkState } from './states';
import styles from './States.module.css';

// Скелетон появляется не сразу: быстрый ответ не мигает (спека UI-фундамента §4.3).
export const SKELETON_DELAY_MS = 150;

// Единый вход экрана в данные (спека §4.3, план UF2a.8): загрузка — скелетон по форме через 150 мс;
// пусто — empty (по умолчанию EmptyState); ошибка — ErrorState, 403 — ForbiddenState, 429 — RateLimitedState;
// нет сети — NoNetworkState; данные — children. Порядок важен: есть данные — показываем их, даже если
// фоновое перечитывание упало (баннер «нет сети» — в каркасе). scope 'block' — данные карточки или панели
// внутри страницы: 403 — ForbiddenState без h1 (ревью UF2a/II-B m5). pending — данные есть, но устарели и
// показывать их нельзя (секрет 2FA, перевыпускаемый «Начать заново»): тот же скелетон, что у загрузки.
// pending прикрывает только сам перезапрос: упал — data прежние, и DataState покажет их. Данные, которые
// нельзя показывать после неудачи, экран стирает до запроса (resetQueries), а не прячет pending'ом.
export function DataState<T>({
  query,
  skeleton,
  isEmpty,
  empty,
  scope = 'page',
  pending,
  children,
}: {
  query: UseQueryResult<T>;
  skeleton: ReactNode;
  isEmpty?: (data: T) => boolean;
  empty?: ReactNode;
  scope?: 'page' | 'block';
  pending?: boolean;
  children: (data: T) => ReactNode;
}) {
  const retry = () => void query.refetch();
  // 401 любого запроса перечитывает me (app/query.ts): пока он идёт — ждём решения сессии
  const meFetching = useIsFetching({ queryKey: ME_KEY }) > 0;
  // me сам ответил 401 — сессия кончилась, роутер уводит на вход эффектами следом: до ухода скелетон, а не
  // кадр ErrorState role="alert" (ревью UF2a/II-B m-r1). Перечитывается по смене meFetching выше.
  const meError = useQueryClient().getQueryState(ME_KEY)?.error;
  const sessionEnded = isApiError(meError, 'auth.unauthenticated');
  if (pending) return <Loading skeleton={skeleton} />;
  if (query.data !== undefined) {
    if (isEmpty?.(query.data)) return <>{empty ?? <EmptyState />}</>;
    return <>{children(query.data)}</>;
  }
  // networkMode 'online': без сети запрос не уходит и не падает, а встаёт на паузу — продолжится сам. Раньше
  // разбора ошибки: «Повторить» без сети после ошибки — тоже пауза (v5 при новом запросе без данных и так
  // сбрасывает ошибку, проверка — страховка порядка).
  if (query.fetchStatus === 'paused') return <NoNetworkState />;
  if (query.isError) {
    const error = query.error;
    // 401 auth.unauthenticated — пока перечитывается me, скелетон: сессия кончилась — роутер уведёт на вход с
    // возвратом. me ответил, а экран всё ещё здесь (гонка смены cookie, D9) — обычная ошибка с «Повторить», а не
    // вечная «Загрузка…» (ревью UF2a/II-B, m2).
    if (isApiError(error, 'auth.unauthenticated') && (meFetching || sessionEnded))
      return <Loading skeleton={skeleton} />;
    if (isApiError(error) && error.status === 403) return <ForbiddenState scope={scope} />;
    if (isApiError(error) && error.status === 429)
      return (
        <RateLimitedState
          key={query.errorUpdatedAt}
          retryAfter={error.retryAfter}
          onRetry={retry}
        />
      );
    // не ответ сервера, а сбой fetch (TypeError) — связи нет, хотя браузер «онлайн»
    if (error instanceof TypeError)
      return <NoNetworkState onRetry={retry} retrying={query.isFetching} />;
    return <ErrorState error={error} onRetry={retry} retrying={query.isFetching} />;
  }
  return <Loading skeleton={skeleton} />;
}

// Загрузка: до SKELETON_DELAY_MS — пусто; потом скелетон под aria-busy и «Загрузка…» для читалки один раз
// (живая область постоянна и вне aria-busy — иначе читалка может не объявить).
function Loading({ skeleton }: { skeleton: ReactNode }) {
  const t = useTranslations('states');
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => setShown(true), SKELETON_DELAY_MS);
    return () => clearTimeout(timer);
  }, []);
  return (
    <div data-wf-loading="">
      <span role="status" className={styles.srOnly}>
        {shown && t('loading')}
      </span>
      <div aria-busy="true">{shown && skeleton}</div>
    </div>
  );
}
