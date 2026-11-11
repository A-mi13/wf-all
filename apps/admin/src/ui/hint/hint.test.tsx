import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { TermHint } from './TermHint';
import { TOOLTIP_HIDE_MS, Tooltip } from './Tooltip';

// Подсказка и термин (спека UI-фундамента §4.8, WCAG 1.4.13): Tooltip — по наведению и фокусу, Esc
// скрывает, наведение на саму подсказку её держит, связь — aria-describedby; TermHint — пунктир и
// кнопка «i» (toggletip): нажатие открывает, текст — в вежливой живой области.

const GLOSSARY_DUAL =
  'Действие исполнится только после того, как его подтвердит второй сотрудник с тем же правом: один человек не сделает его в одиночку.';

describe('Tooltip', () => {
  test('наведение открывает, уход — закрывает; aria-describedby — на подсказку', async () => {
    const { user } = renderWithIntl(
      <Tooltip content="Код для поддержки">
        <Button>Код</Button>
      </Tooltip>,
    );
    const anchor = screen.getByRole('button', { name: 'Код' });
    expect(screen.queryByRole('tooltip')).toBeNull();
    await user.hover(anchor);
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('Код для поддержки');
    expect(anchor).toHaveAttribute('aria-describedby', tip.id);
    expect(anchor).toHaveAccessibleDescription('Код для поддержки');
    await user.unhover(anchor);
    await waitFor(() => expect(screen.queryByRole('tooltip')).toBeNull());
  });

  test('наведение на саму подсказку её держит (WCAG 1.4.13)', async () => {
    const { user } = renderWithIntl(
      <Tooltip content="Код для поддержки">
        <Button>Код</Button>
      </Tooltip>,
    );
    const anchor = screen.getByRole('button', { name: 'Код' });
    await user.hover(anchor);
    const tip = await screen.findByRole('tooltip');
    // курсор переходит на текст подсказки (содержимое заполняет пузырь)
    const text = within(tip).getByText('Код для поддержки');
    await user.unhover(anchor);
    await user.hover(text);
    await act(() => new Promise((r) => setTimeout(r, TOOLTIP_HIDE_MS * 2)));
    expect(screen.getByRole('tooltip')).toBeVisible();
    await user.unhover(text);
    await waitFor(() => expect(screen.queryByRole('tooltip')).toBeNull());
  });

  test('фокус открывает, Esc скрывает (фокус остаётся)', async () => {
    const { user } = renderWithIntl(
      <Tooltip content="Код для поддержки">
        <Button>Код</Button>
      </Tooltip>,
    );
    await user.tab();
    const anchor = screen.getByRole('button', { name: 'Код' });
    expect(anchor).toHaveFocus();
    expect(await screen.findByRole('tooltip')).toBeVisible();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('tooltip')).toBeNull();
    expect(anchor).toHaveFocus();
  });

  // Ревью UF2a/I, m3: на касании браузер шлёт совместимые mouseenter без mouseleave — подсказка висела
  // бы после перехода. Наведение — только мышь (pointerType 'mouse'); касание фокус не меняет.
  test('касание (pointerType touch) и совместимый mouseenter подсказку не открывают', async () => {
    renderWithIntl(
      <Tooltip content="Код для поддержки">
        <Button>Код</Button>
      </Tooltip>,
    );
    const anchor = screen.getByRole('button', { name: 'Код' });
    fireEvent.pointerEnter(anchor, { pointerType: 'touch' });
    fireEvent.mouseEnter(anchor);
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  // Ревью UF2a/I, m2: подсказка, совпадающая с именем элемента (значок полосы с aria-label), — без
  // aria-describedby, иначе читалка слышит имя дважды.
  test('describe={false} — без aria-describedby', async () => {
    const { user } = renderWithIntl(
      <Tooltip content="Журнал действий" describe={false}>
        <Button aria-label="Журнал действий">i</Button>
      </Tooltip>,
    );
    const anchor = screen.getByRole('button', { name: 'Журнал действий' });
    await user.hover(anchor);
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Журнал действий');
    expect(anchor).not.toHaveAttribute('aria-describedby');
    expect(anchor).toHaveAccessibleDescription('');
  });

  test('open — управляемый режим: открыта без наведения', () => {
    renderWithIntl(
      <Tooltip content="Подсказка" open>
        <Button>Код</Button>
      </Tooltip>,
    );
    expect(screen.getByRole('tooltip')).toBeVisible();
  });
});

describe('TermHint', () => {
  test('«i» — нажатие открывает, текст — в живой области; повторное — закрывает', async () => {
    const { user } = renderWithIntl(
      <p>
        Это действие требует <TermHint term="dual_approval">подтверждения вдвоём</TermHint>.
      </p>,
    );
    const live = document.querySelector('[data-term-live]');
    expect(live).toHaveAttribute('aria-live', 'polite');
    expect(live).toHaveTextContent('');
    const info = screen.getByRole('button', { name: 'Что это значит?' });
    expect(info).toHaveAccessibleDescription('подтверждения вдвоём');
    expect(info).toHaveAttribute('aria-expanded', 'false');
    await user.click(info);
    expect(info).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('tooltip')).toHaveTextContent(GLOSSARY_DUAL);
    expect(live).toHaveTextContent(GLOSSARY_DUAL);
    await user.click(info);
    expect(screen.queryByRole('tooltip')).toBeNull();
    expect(live).toHaveTextContent('');
  });

  // Ревью UF2a/III-S m1: термин — заголовок рядом; кнопка описана им (describedBy), своей обёртки термина нет.
  test('describedBy — только кнопка «i», описанная внешним термином', async () => {
    const { user } = renderWithIntl(
      <div>
        <h2 id="term-h">Правило двух рук</h2>
        <TermHint term="dual_approval" describedBy="term-h" />
      </div>,
    );
    const info = screen.getByRole('button', { name: 'Что это значит?' });
    expect(info).toHaveAccessibleDescription('Правило двух рук');
    await user.click(info);
    expect(screen.getByRole('tooltip')).toHaveTextContent(GLOSSARY_DUAL);
    expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('Правило двух рук');
  });

  test('Esc закрывает подсказку термина', async () => {
    const { user } = renderWithIntl(<TermHint term="slug">адрес</TermHint>);
    await user.click(screen.getByRole('button', { name: 'Что это значит?' }));
    expect(screen.getByRole('tooltip')).toBeVisible();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('tooltip')).toBeNull();
  });
});
