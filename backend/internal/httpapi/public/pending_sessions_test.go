package public_test

// pendingSessions — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 12 плана I-2.
var pendingSessions = []string{"listMySessions", "revokeMySession", "revokeOtherSessions", "getAuthMethods"}
