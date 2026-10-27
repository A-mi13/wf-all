import { screen } from '@testing-library/react';
import { useState } from 'react';
import { expect, test } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { PageHeader } from './PageHeader';
import { Tabs } from './Tabs';

test('PageHeader: заголовок страницы h1, подзаголовок и действия', () => {
  renderWithIntl(
    <PageHeader
      title="Роли и доступ"
      subtitle="кто что может"
      actions={<Button>Пригласить</Button>}
    />,
  );
  expect(screen.getByRole('heading', { level: 1, name: 'Роли и доступ' })).toBeInTheDocument();
  expect(screen.getByText('кто что может')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Пригласить' })).toBeInTheDocument();
});

function Approvals() {
  const [tab, setTab] = useState('incoming');
  return (
    <Tabs
      label="Подтверждения"
      value={tab}
      onChange={setTab}
      items={[
        { value: 'incoming', label: 'Входящие', count: 3 },
        { value: 'mine', label: 'Мои' },
        { value: 'all', label: 'Все', count: 120 },
      ]}
    >
      <p>Панель {tab}</p>
    </Tabs>
  );
}

test('Tabs: счётчик в имени вкладки (поиск по подстроке), выбор и панель', async () => {
  const { user } = renderWithIntl(<Approvals />);
  const incoming = screen.getByRole('tab', { name: /Входящие/ });
  expect(incoming).toHaveAccessibleName('Входящие 3');
  expect(screen.getByRole('tab', { name: /Все/ })).toHaveAccessibleName('Все 99+');
  expect(incoming).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByRole('tabpanel', { name: /Входящие/ })).toHaveTextContent('Панель incoming');
  await user.click(screen.getByRole('tab', { name: 'Мои' }));
  expect(screen.getByRole('tab', { name: 'Мои' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByRole('tabpanel', { name: 'Мои' })).toHaveTextContent('Панель mine');
});

test('Tabs: стрелки и Home/End переводят фокус и выбор, Tab — одна остановка', async () => {
  const { user } = renderWithIntl(<Approvals />);
  await user.tab();
  expect(screen.getByRole('tab', { name: /Входящие/ })).toHaveFocus();
  await user.keyboard('{ArrowRight}');
  expect(screen.getByRole('tab', { name: 'Мои' })).toHaveFocus();
  expect(screen.getByRole('tab', { name: 'Мои' })).toHaveAttribute('aria-selected', 'true');
  await user.keyboard('{End}');
  expect(screen.getByRole('tab', { name: /Все/ })).toHaveFocus();
  await user.keyboard('{ArrowRight}');
  expect(screen.getByRole('tab', { name: /Входящие/ })).toHaveFocus();
  await user.keyboard('{ArrowLeft}');
  expect(screen.getByRole('tab', { name: /Все/ })).toHaveFocus();
  await user.keyboard('{Home}');
  expect(screen.getByRole('tab', { name: /Входящие/ })).toHaveFocus();
  // неактивные вкладки — вне цикла Tab
  expect(screen.getByRole('tab', { name: 'Мои' })).toHaveAttribute('tabindex', '-1');
});
