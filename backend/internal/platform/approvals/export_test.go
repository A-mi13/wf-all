package approvals

// SetBatchForTest — размер порции ExpireDue и PurgeDecided на время теста.
func SetBatchForTest(n int) (restore func()) {
	prev := batchSize
	batchSize = n
	return func() { batchSize = prev }
}

// Statuses — состояния запроса для сверки с перечислением approval_status.
var Statuses = statuses
