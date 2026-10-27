export interface PermissionCatalog {
  service: string[];
  permissions: { key: string; draft: boolean; mutating: boolean }[];
}
export function permissionKeys(catalog: PermissionCatalog): string[];
export function approvalActions(doc: unknown): string[];
export function loadLabelCatalog(): Promise<{ permissions: string[]; approvalActions: string[] }>;
