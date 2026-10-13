import { useTranslations } from 'use-intl';

// Временные страницы каркаса (Task 11): маршруты есть, экранов ещё нет. Заменяются экранами
// Task 13–15 (вход, «Мой доступ», «Роли и доступ», подтверждения, журнал) и состояниями Task 12b
// (ForbiddenState, загрузка, нет связи).

export function PageStub({ titleKey }: { titleKey: string }) {
  const t = useTranslations();
  return (
    <main>
      <h1>{t(titleKey)}</h1>
    </main>
  );
}

export function ForbiddenStub() {
  const t = useTranslations('states.forbidden');
  return (
    <main>
      <h1>{t('title')}</h1>
      <p>{t('text')}</p>
    </main>
  );
}

export function NotFoundStub() {
  const t = useTranslations('states.not_found');
  return (
    <main>
      <h1>{t('title')}</h1>
    </main>
  );
}

export function LoadingStub() {
  const t = useTranslations('states');
  return (
    <p role="status" aria-live="polite">
      {t('loading')}
    </p>
  );
}

export function ConnectionErrorStub({ onRetry }: { onRetry: () => void }) {
  const t = useTranslations('states.connection');
  return (
    <main>
      <p role="alert">{t('title')}</p>
      <button type="button" onClick={onRetry}>
        {t('retry')}
      </button>
    </main>
  );
}
