import { useTranslations } from 'use-intl';
import { PageHeader } from '../ui';

// Временные страницы каркаса (Task 11): маршруты есть, экранов ещё нет. Заменяются экранами
// Task 15 (подтверждения, журнал); «Мой доступ» и «Роли и доступ» — Task 14, экраны входа —
// Task 13. Нет доступа, нет связи и «только с компьютера» — состояния из src/ui (Task 12b).

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

// Раздел внутри AppShell: страница уже в его <main>, здесь — только шапка.
export function SectionStub({ titleKey }: { titleKey: string }) {
  const t = useTranslations();
  return <PageHeader title={t(titleKey)} />;
}
