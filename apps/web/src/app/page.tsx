import { getTranslations } from 'next-intl/server';
import { HealthStatus } from '../features/health/HealthStatus';

export default async function Home() {
  const t = await getTranslations('app');
  return (
    <main>
      <h1>{t('title')}</h1>
      <HealthStatus />
    </main>
  );
}
