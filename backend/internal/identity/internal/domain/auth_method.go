package domain

// AuthMethod — способ первого шага входа (тип auth_method, sessions.auth_method): сессия помнит, чем вошли.
type AuthMethod string

const (
	MethodPassword AuthMethod = "password"
	MethodApple    AuthMethod = "apple"
	MethodGoogle   AuthMethod = "google"
	MethodPasskey  AuthMethod = "passkey"
)
