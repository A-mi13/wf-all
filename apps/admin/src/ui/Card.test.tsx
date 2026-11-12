import { act, screen, within } from '@testing-library/react';
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

  // Ревью G8, m11: переход — ссылка (открывается и в новой вкладке), обычный клик — навигацией приложения;
  // имя «Открыть: <город>» начинается с видимого текста (WCAG 2.5.3).
  test('действие-ссылка: href, имя по name, обычный клик — onNavigate, Ctrl+клик — браузеру', async () => {
    const onNavigate = vi.fn();
    const { user } = renderWithIntl(
      <Card
        title="Ставрополь"
        action={{
          label: 'Открыть',
          name: 'Открыть: Ставрополь',
          href: '/geo/cities/1',
          onNavigate,
        }}
      />,
    );
    const link = screen.getByRole('link', { name: 'Открыть: Ставрополь' });
    expect(link).toHaveAttribute('href', '/geo/cities/1');
    expect(link).toHaveTextContent('Открыть');
    expect(screen.queryByRole('button')).toBeNull();
    await user.click(link);
    expect(onNavigate).toHaveBeenCalledOnce();
    // jsdom навигацию по ссылке не исполняет; модификатор — onNavigate не зовётся
    await user.keyboard('{Control>}');
    await user.click(link);
    await user.keyboard('{/Control}');
    expect(onNavigate).toHaveBeenCalledOnce();
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

// Источник перехода «список → карточка» (§4.15; ревью UF2b/II-B, m5): класс .vtSource — только у нажатой карточки
// и только пока она на экране; список снят (переход состоялся) — при возврате имя wf-entity ни у кого.
describe('Card: источник View Transition', () => {
  const link = (href: string, name: string) => ({
    label: 'Открыть',
    name,
    href,
    onNavigate: () => {},
  });
  const article = (name: string) => screen.getByRole('article', { name });

  test('нажатая — .vtSource, прежняя нажатая его теряет', async () => {
    const { user } = renderWithIntl(
      <>
        <Card title="А" action={link('/a', 'Открыть: А')} />
        <Card title="Б" action={link('/b', 'Открыть: Б')} />
      </>,
    );
    await user.click(screen.getByRole('link', { name: 'Открыть: А' }));
    expect(article('А').className).toMatch(/vtSource/);
    await user.click(screen.getByRole('link', { name: 'Открыть: Б' }));
    expect(article('А').className).not.toMatch(/vtSource/);
    expect(article('Б').className).toMatch(/vtSource/);
  });

  test('список снят и показан снова — источника нет', async () => {
    const first = renderWithIntl(<Card title="А" action={link('/a', 'Открыть: А')} />);
    await first.user.click(screen.getByRole('link', { name: 'Открыть: А' }));
    first.unmount();
    // переход на страницу города — уже после снятия списка (не в том же коммите)
    await act(async () => {});
    renderWithIntl(<Card title="А" action={link('/a', 'Открыть: А')} />);
    expect(article('А').className).not.toMatch(/vtSource/);
  });
});
