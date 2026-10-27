import { Overlay, type OverlayProps } from './Overlay';

// Модалка по центру (макет 07: подтверждение, разблокировка сессии). tone="danger" — рамка и
// заголовок опасного действия.
export function Modal(props: OverlayProps & { tone?: 'default' | 'danger' }) {
  return <Overlay {...props} placement="center" />;
}
