package public_test

// pendingMfa — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 16 плана I-2.
var pendingMfa = []string{"getMyMfa", "setupTotp", "confirmTotp", "disableTotp", "regenerateRecoveryCodes", "completeMfa"}
