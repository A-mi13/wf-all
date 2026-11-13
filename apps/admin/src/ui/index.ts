// Набор компонентов админки (спека §9.2). Экраны (src/features) берут кнопки, поля, оверлеи и
// тосты, каркас, таблицу и состояния только отсюда (.claude/rules/admin.md).
export { Avatar } from './Avatar';
export { Button, type ButtonProps, type ButtonVariant } from './Button';
export { Card, type CardProps, type CardStat } from './Card';
export { CopyButton } from './CopyButton';
export { CountBadge } from './CountBadge';
export { useCountdown } from './countdown';
export { useErrorText } from './errorText';
export {
  applyFieldErrors,
  CheckboxField,
  DateField,
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
export { ActionGate } from './gate/ActionGate';
export { GLOSSARY_KEYS, type GlossaryKey } from './hint/glossary';
export { TermHint } from './hint/TermHint';
export { Tooltip } from './hint/Tooltip';
export { AttentionTile, type AttentionTileProps, AttentionTiles } from './home/AttentionTile';
export { type AttentionState, attentionState } from './home/state';
export { type TodoError, type TodoItem, TodoList } from './home/TodoList';
export { Icon, Illustration } from './icon';
export { useIsCoarse, useIsCompact, useIsHandheld, useIsMobile } from './media';
export { useReducedMotion } from './motion';
export { useOnline } from './online';
export { OtpInput, type OtpInputHandle, type OtpInputProps } from './OtpInput';
export {
  Popover,
  type PopoverCloseReason,
  type PopoverKind,
  type PopoverProps,
} from './popover/Popover';
export {
  ConfirmDialog,
  type ConfirmDialogProps,
  Drawer,
  LeaveDialog,
  type LeaveDialogProps,
  Modal,
  type OverlayProps,
  Sheet,
} from './overlay';
export {
  AppShell,
  type AppShellProps,
  type NavGroup,
  type NavItem,
  PageHeader,
  type TabItem,
  Tabs,
} from './shell';
export {
  ChunkErrorState,
  ConnectionErrorState,
  DataState,
  DesktopOnlyNotice,
  EmptyState,
  ErrorState,
  ForbiddenState,
  NoNetworkState,
  OfflineBanner,
  PendingApprovalNotice,
  RateLimitedState,
  SectionSkeleton,
  Skeleton,
  type SkeletonHeight,
  type SkeletonWidth,
} from './states';
export { NowProvider, type NowGranularity, RelativeTime, useNow } from './time';
export { QrCode } from './QrCode';
export { type SegmentedProps, type SegmentOption, Segmented } from './Segmented';
export {
  SHORTCUTS_SINGLE_KEY,
  ShortcutsHelp,
  type ShortcutsHelpItem,
  type ShortcutsHelpProps,
  ShortcutsProvider,
  type ShortcutOptions,
  useOpenShortcutsHelp,
  useShortcut,
} from './shortcuts';
export { type ChipTone, StatusChip } from './StatusChip';
export { Switch, type SwitchProps } from './Switch';
export {
  BULK_CONCURRENCY,
  BULK_RETRY_DEFAULT_MS,
  type BulkOptions,
  type BulkReport,
  retryBulk,
  runBulk,
} from './table/bulk';
export { BulkBar, type BulkBarProps } from './table/BulkBar';
export type { BulkAction, BulkOutcome } from './table/model';
export {
  type ChipOption,
  type Column,
  DataTable,
  type DataTableProps,
  type Density,
  infiniteTableProps,
  SEARCH_DEBOUNCE_MS,
  SearchBox,
  type SortState,
  useDensity,
} from './table';
export {
  type ColorName,
  THEME_STORAGE_KEY,
  ThemeProvider,
  type ThemePref,
  ThemeSwitch,
  useTheme,
  useThemeColors,
} from './theme';
export { TOAST_HIDE_MS, type ToastApi, ToastProvider, useToast } from './toast';
export {
  MATRIX_VIRTUALIZE_FROM,
  type MatrixFixedColumn,
  useVirtualRows,
  VIRTUAL_OVERSCAN,
  type VirtualItem,
  VirtualList,
  VirtualMatrix,
  type VirtualRows,
} from './virtual';
