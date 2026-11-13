import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { ApiError } from '../../api/client';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Button } from '../Button';
import { DataState } from './DataState';
import { Skeleton } from './Skeleton';
import { EmptyState, NoNetworkState } from './states';

// Пять состояний данных (спека UI-фундамента §4.3–§4.4): скелетоны всех форм, DataState в загрузке, ошибке и с
// данными, «нет сети», пустое с иллюстрацией. Запросы — без сети (кэш витрины): ответ — из queryFn истории.
export default { title: 'UI/DataState', visual: true } satisfies StoryMeta;

export const Skeletons: Story = () => (
  <div className={styles.column}>
    <Skeleton.Text lines={3} />
    <Skeleton.Text width="short" />
    <Skeleton.Block height="lg" />
    <Skeleton.Card />
    <Skeleton.Table columns={4} rows={3} />
    <Skeleton.Form fields={2} />
  </div>
);

const noop = () => undefined;

// Ответ не приходит — через 150 мс скелетон под aria-busy. Состояние запроса — снимком, а не вечным queryFn:
// режим shots ждёт тишины кэша запросов перед снимком (src/shots/main.tsx), вечный запрос её не дал бы.
const LOADING = {
  data: undefined,
  isError: false,
  error: null,
  fetchStatus: 'fetching',
  isFetching: true,
} as unknown as UseQueryResult<string>;

export const Loading: Story = () => {
  const query = LOADING;
  return (
    <DataState query={query} skeleton={<Skeleton.Table columns={3} rows={4} />}>
      {(d) => <p>{d}</p>}
    </DataState>
  );
};

export const Failed: Story = () => {
  const query = useQuery({
    queryKey: ['showcase', 'failed'],
    queryFn: (): Promise<string> => Promise.reject(new ApiError({ status: 500, code: 'internal' })),
  });
  return (
    <DataState query={query} skeleton={<Skeleton.Card />}>
      {(d) => <p>{d}</p>}
    </DataState>
  );
};

export const Empty: Story = () => {
  const t = useTranslations('geo.cities');
  const query = useQuery({
    queryKey: ['showcase', 'empty'],
    queryFn: () => Promise.resolve<string[]>([]),
  });
  return (
    <DataState
      query={query}
      skeleton={<Skeleton.Card />}
      isEmpty={(d) => d.length === 0}
      empty={
        <EmptyState
          tone="accent"
          illustration="pitch"
          title={t('empty_title')}
          text={t('empty_text')}
          action={<Button>{t('open')}</Button>}
        />
      }
    >
      {(d) => <p>{d.join(', ')}</p>}
    </DataState>
  );
};

// Рисунки пустых состояний (спека §4.4): свисток, мяч, поле, угловой, секундомер.
export const Illustrations: Story = () => (
  <div className={styles.column}>
    <EmptyState illustration="whistle" />
    <EmptyState illustration="ball" />
    <EmptyState illustration="corner" />
    <EmptyState illustration="time" />
  </div>
);

export const NoNetwork: Story = () => (
  <div className={styles.column}>
    <NoNetworkState />
    <NoNetworkState onRetry={noop} />
  </div>
);
