// Набор компонентов админки (спека §9.2). Экраны (src/features) берут кнопки, поля, оверлеи и
// тосты только отсюда (.claude/rules/admin.md). Каркас, таблица и состояния — Task 12b.
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
export { OtpInput, type OtpInputHandle, type OtpInputProps } from './OtpInput';
export {
  ConfirmDialog,
  type ConfirmDialogProps,
  Drawer,
  Modal,
  type OverlayProps,
  Sheet,
} from './overlay';
export { type ChipTone, StatusChip } from './StatusChip';
export { TOAST_HIDE_MS, type ToastApi, ToastProvider, useToast } from './toast';
