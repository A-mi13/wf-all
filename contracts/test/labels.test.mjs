// Каталоги подписей админки (D21): ключи прав и действия подтверждений, у которых в
// apps/admin/messages обязаны быть тексты ru и en. Страж локалей — в apps/admin/src/i18n.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { approvalActions, loadLabelCatalog, permissionKeys } from '../src/labels.mjs';

test('ключи прав — только не-draft, по порядку каталога', () => {
  const catalog = {
    service: ['anonymous', 'session', 'self', 'handler'],
    permissions: [
      { key: 'staff.roles.read', draft: false, mutating: false },
      { key: 'teams.captains.approve', draft: true, mutating: true },
      { key: 'audit.log.read', draft: false, mutating: false },
    ],
  };
  assert.deepEqual(permissionKeys(catalog), ['staff.roles.read', 'audit.log.read']);
});

test('действия подтверждений — enum схемы ApprovalAction бандла', () => {
  const doc = {
    components: { schemas: { ApprovalAction: { type: 'string', enum: ['b.two', 'a.one'] } } },
  };
  assert.deepEqual(approvalActions(doc), ['a.one', 'b.two']);
  assert.throws(() => approvalActions({ components: { schemas: {} } }), /ApprovalAction/);
});

test('каталог собранного контракта admin', async () => {
  const { permissions, approvalActions: actions } = await loadLabelCatalog();
  assert.ok(permissions.includes('staff.roles.grant'));
  assert.ok(!permissions.includes('teams.captains.approve'), 'draft-права не подписываются');
  assert.ok(actions.includes('staff.invite'));
});
