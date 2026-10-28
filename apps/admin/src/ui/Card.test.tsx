import { screen, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../test/render';
import { Card } from './Card';
import { StatusChip } from './StatusChip';

// Карточка макета «Города и запуск» (07): заголовок, чип статуса, показатели (значение + подпись),
// примечание и кнопка действия. Кнопка описана заголовком карточки (aria-describedby): «Открыть» в
// сетке карточек читалка различает по городу.

describe('Card', () => {
  test('заголовок, чип, показатели, примечание, кнопка действия с подписью', async () => {
    const onClick = vi.fn();
    const { user } = renderWithIntl(
      <Card
        title="Ставрополь"
        chip={<StatusChip tone="success">живой</StatusChip>}
        stats={[
          { value: '450 680', label: 'жителей' },
          { value: '4', label: 'района' },
        ]}
        note="Ставропольский край"
        action={{ label: 'Открыть', onClick }}
      />,
    );
    const card = screen.getByRole('article', { name: 'Ставрополь' });
    expect(within(card).getByRole('heading', { level: 2, name: 'Ставрополь' })).toBeInTheDocument();
    expect(within(card).getByText('живой')).toBeInTheDocument();
    const stats = within(card).getByRole('list');
    expect(
      within(stats)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual(['450 680жителей', '4района']);
    expect(within(card).getByText('Ставропольский край')).toBeInTheDocument();
    const button = within(card).getByRole('button', { name: 'Открыть' });
    expect(button).toHaveAccessibleDescription('Ставрополь');
    await user.click(button);
    expect(onClick).toHaveBeenCalledOnce();
  });

  test('без чипа, показателей, примечания и действия — пустых блоков нет', () => {
    renderWithIntl(<Card title="Астрахань" />);
    const card = screen.getByRole('article', { name: 'Астрахань' });
    expect(within(card).queryByRole('list')).toBeNull();
    expect(within(card).queryByRole('button')).toBeNull();
    expect(card.querySelectorAll('footer')).toHaveLength(0);
  });

  // Выделение — только вид (макет: у живого города фон и рамка ярче); смысл несёт чип статуса.
  test('selected — выделенная карточка (вид, не смысл)', () => {
    renderWithIntl(
      <>
        <Card title="Ставрополь" selected />
        <Card title="Астрахань" />
      </>,
    );
    expect(screen.getByRole('article', { name: 'Ставрополь' })).toHaveAttribute('data-selected');
    expect(screen.getByRole('article', { name: 'Астрахань' })).not.toHaveAttribute('data-selected');
  });

  test('уровень заголовка задаёт экран', () => {
    renderWithIntl(<Card title="Ставрополь" headingLevel={3} />);
    expect(screen.getByRole('heading', { level: 3, name: 'Ставрополь' })).toBeInTheDocument();
  });
});
