import { Overlay, type OverlayProps } from './Overlay';

// Панель справа (* — формы «Пригласить», строка сотрудника, правка страны): на всю высоту, ширина
// до 480 px; на узком экране — во всю ширину.
export function Drawer(props: OverlayProps) {
  return <Overlay {...props} placement="right" />;
}
