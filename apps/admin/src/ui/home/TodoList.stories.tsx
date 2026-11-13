import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { TodoList } from './TodoList';

// Список дел главной (спека UI-фундамента §4.13): строки с тоном и пусто «Всё спокойно».
export default { title: 'UI/TodoList', visual: true } satisfies StoryMeta;

const noop = () => undefined;

export const Default: Story = () => {
  const t = useTranslations('home');
  return (
    <TodoList
      label={t('todo_title')}
      items={[
        {
          id: 'incoming',
          text: t('todo.incoming', { count: 3 }),
          href: '#approvals',
          tone: 'warning',
          onNavigate: noop,
        },
        {
          id: 'import-RU',
          text: t('todo.import_failed', { country: 'RU' }),
          href: '#imports',
          tone: 'danger',
          onNavigate: noop,
        },
        { id: 'waitlist', text: t('todo.waitlist'), href: '#cities', onNavigate: noop },
        {
          id: 'codes',
          text: t('todo.codes', { count: 2 }),
          href: '#me',
          tone: 'warning',
          onNavigate: noop,
        },
      ]}
    />
  );
};

export const Empty: Story = () => <TodoList items={[]} />;

export const WithError: Story = () => {
  const t = useTranslations('home');
  return (
    <TodoList
      label={t('todo_title')}
      items={[
        {
          id: 'incoming',
          text: t('todo.incoming', { count: 3 }),
          href: '#approvals',
          tone: 'warning',
          onNavigate: noop,
        },
      ]}
      error={{ onRetry: noop }}
    />
  );
};

export const ErrorOnly: Story = () => (
  <TodoList items={[]} error={{ onRetry: noop, retrying: true }} />
);
