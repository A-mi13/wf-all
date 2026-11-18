package public_test

// pendingI3 — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Соцвходы, ключи доступа, аттестация — план I-3; после
// плана I-4 это единственные заглушки public (R1 плана I-4). Список удаляет последняя задача плана I-3.
var pendingI3 = []string{"createSocialNonce", "socialLogin", "completeSocialSignup", "linkIdentity", "unlinkIdentity", "startPasskeyLogin", "passkeyLogin", "startPasskeyRegistration", "registerPasskey", "renamePasskey", "deletePasskey", "startMfaPasskey", "startReauthPasskey", "createDeviceChallenge"}
