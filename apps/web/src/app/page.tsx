import { getTranslations } from 'next-intl/server';
import { ConnectionStatus } from '../features/connection/ConnectionStatus';

export default async function Home() {
  const t = await getTranslations('app');
  return (
    <main>
      <h1>{t('title')}</h1>
      <ConnectionStatus />
    </main>
  );
}
