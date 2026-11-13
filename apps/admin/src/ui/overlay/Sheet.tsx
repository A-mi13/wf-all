import { Overlay, type OverlayProps } from './Overlay';

// Шторка снизу на телефоне (макеты 08 и 13 «Шит снизу»): фильтры, подтверждение на узком экране. fill — во всю
// высоту предела шторки (палитра), иначе — по содержимому.
export function Sheet(props: OverlayProps & { tone?: 'default' | 'danger'; fill?: boolean }) {
  return <Overlay {...props} placement="bottom" />;
}
