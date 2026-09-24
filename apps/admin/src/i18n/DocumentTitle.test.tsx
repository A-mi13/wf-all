import { expect, test } from 'vitest';
import { renderWithProviders } from '../test/render';
import { DocumentTitle } from './DocumentTitle';

test('заголовок вкладки берётся из messages (app.title)', () => {
  document.title = '';
  renderWithProviders(<DocumentTitle />);
  expect(document.title).toBe('Администрирование');
});
