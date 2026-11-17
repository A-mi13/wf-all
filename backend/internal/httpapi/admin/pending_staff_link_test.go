package admin_test

// pendingStaffLink — операции области в pendingOps (раскладка — R16 плана I-4): реализовала задача операцию — убрать её
// отсюда (TestNoStubOperations: «убрать из pendingOps»). Связь «сотрудник → учётка игрока» (тег staff, спека identity
// §7.6) — план I-4, Task 6.
var pendingStaffLink = []string{"putStaffPlayerLink", "deleteStaffPlayerLink"}
