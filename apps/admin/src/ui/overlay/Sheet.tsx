import { Overlay, type OverlayProps } from './Overlay';

// Шторка снизу на телефоне (макеты 08 и 13 «Шит снизу»): фильтры, подтверждение на узком экране.
export function Sheet(props: OverlayProps & { tone?: 'default' | 'danger' }) {
  return <Overlay {...props} placement="bottom" />;
}
