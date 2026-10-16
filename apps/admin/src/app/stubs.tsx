import { useTranslations } from 'use-intl';

// Служебные страницы роутера: «не найдено» и загрузка сессии. Нет доступа, нет связи и «только с
// компьютера» — состояния из src/ui (Task 12b).

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
