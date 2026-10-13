// Набор компонентов админки (спека §9.2). Экраны (src/features) берут кнопки, поля, оверлеи и
// тосты, каркас, таблицу и состояния только отсюда (.claude/rules/admin.md).
export { Avatar } from './Avatar';
export { Button, type ButtonProps, type ButtonVariant } from './Button';
export { CountBadge } from './CountBadge';
export { useErrorText } from './errorText';
export {
  applyFieldErrors,
  CheckboxField,
  FieldError,
  Form,
  FormError,
  type FormProps,
  MultiSelectField,
  type Option,
  PasswordField,
  SelectField,
  TextField,
  useFieldErrorText,
} from './form';
export { useIsMobile } from './media';
export { useOnline } from './online';
export { OtpInput, type OtpInputHandle, type OtpInputProps } from './OtpInput';
export {
  ConfirmDialog,
  type ConfirmDialogProps,
  Drawer,
  Modal,
  type OverlayProps,
  Sheet,
} from './overlay';
export {
  AppShell,
  type AppShellProps,
  type NavGroup,
  type NavIcon,
  type NavItem,
  PageHeader,
  type TabItem,
  Tabs,
} from './shell';
export {
  ConnectionErrorState,
  DesktopOnlyState,
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingRows,
  OfflineBanner,
  PendingApprovalNotice,
  RateLimitedState,
} from './states';
export { type ChipTone, StatusChip } from './StatusChip';
export {
  type ChipOption,
  type Column,
  DataTable,
  type DataTableProps,
  infiniteTableProps,
  SEARCH_DEBOUNCE_MS,
  type SortState,
} from './table';
export { TOAST_HIDE_MS, type ToastApi, ToastProvider, useToast } from './toast';
